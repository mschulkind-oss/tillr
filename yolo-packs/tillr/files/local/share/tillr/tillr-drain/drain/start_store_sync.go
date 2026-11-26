package drain

import (
	"context"
	"encoding/json"
	"maps"
	"reflect"
	"slices"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// The watcher alone owns this reader and its cursor. data_version values are
// comparable only on the same SQLite connection, not across fresh opens.
type startStoreSync struct {
	reader                 *store.Store
	dataVersion            int64
	policy                 *Policy
	quotaChecked, lastFull time.Time
	itemsSignature         [32]byte
	laneRebuildOwed        bool
	onReadTx               func(open bool)
}

func (s *startStoreSync) close() {
	if s.reader != nil {
		_ = s.reader.Close()
		s.reader = nil
	}
}

func (s *startStoreSync) syncStore(ctx context.Context, path string, at time.Time, policy *Policy, prev startSnapshot, rebuild func(*store.Reader, *startSnapshot, map[string]bool) (func() error, error)) (startSnapshot, error) {
	return s.syncStoreWithRefresh(ctx, path, at, policy, prev, false, nil, rebuild)
}

func (s *startStoreSync) syncStoreWithRefresh(ctx context.Context, path string, at time.Time, policy *Policy, prev startSnapshot, inspectExternal bool, prepare func(*store.Store) error, rebuild func(*store.Reader, *startSnapshot, map[string]bool) (func() error, error)) (startSnapshot, error) {
	fresh := s.reader == nil
	if fresh {
		var err error
		s.reader, err = store.OpenRead(ctx, path)
		if err != nil {
			return prev, err
		}
	}
	if prepare != nil {
		if err := prepare(s.reader); err != nil {
			s.close()
			return prev, err
		}
	}
	var dv int64
	next := prev
	quotaChecked, lastFull := s.quotaChecked, s.lastFull
	signature, owed := s.itemsSignature, s.laneRebuildOwed
	var finishRebuild func() error
	err := s.reader.ReadTx(ctx, func(r *store.Reader) error {
		if s.onReadTx != nil {
			s.onReadTx(true)
		}
		if err := r.QueryRow(`PRAGMA data_version`).Scan(&dv); err != nil {
			return err
		}
		head, err := r.EventHeadSeq()
		if err != nil {
			return err
		}
		reseed := func() error {
			next = startSnapshot{}
			if err := next.seed(r, at, policy); err != nil {
				return err
			}
			quotaChecked, lastFull, owed = at, at, false
			signature, err = readStartItemsSignature(r, next.versionSchema)
			if err == nil && rebuild != nil {
				finishRebuild, err = rebuild(r, &next, nil)
			}
			return err
		}
		if fresh || prev.pending == nil || prev.backendOutcomes == nil || head < prev.lastEventSeq || dv < s.dataVersion || s.policy != policy {
			return reseed()
		}
		var delta startStoreDelta
		now := at.UnixMilli()
		delta.lanes = prev.nextLeaseExpiry != 0 && prev.nextLeaseExpiry <= now
		delta.quota = (prev.nextWindowExpiry != 0 && prev.nextWindowExpiry <= now) || at.Sub(s.quotaChecked) >= QuotaProbeInterval
		if head > prev.lastEventSeq {
			events, err := r.Events(prev.lastEventSeq)
			if err != nil {
				return err
			}
			if len(events) == 0 || events[len(events)-1].Seq != head || !delta.classify(events) {
				return reseed()
			}
		}
		// An unlogged write can be an observation or a lease/window update,
		// but it can also be a raw edit of the queue.
		if head == prev.lastEventSeq && dv != s.dataVersion {
			declared, readErr := definition.ForReader(r)
			if readErr != nil {
				return readErr
			}
			if declared.SHA256 != prev.definitionSHA {
				return reseed()
			}
			signature, err = readStartItemsSignature(r, next.versionSchema)
			if err != nil {
				return err
			}
			if signature != s.itemsSignature {
				return reseed()
			}
			delta.lanes, delta.quota, delta.unsavedOutcomeInputs, delta.unlogged = true, true, true, true
		}
		if inspectExternal && next.backendOutcomes != nil && next.backendOutcomes.unsaved > 0 {
			delta.unsavedOutcomeInputs = true
		}
		touched := map[string]bool{}
		if err := delta.refreshItems(r, &next, at, touched); err != nil {
			return err
		}
		if delta.place || len(delta.items) > 0 {
			ok, err := next.refreshRanks(r)
			if err != nil {
				return err
			}
			if !ok {
				return reseed()
			}
			next.rebuildReverseEdges()
			signature, err = readStartItemsSignature(r, next.versionSchema)
			if err != nil {
				return err
			}
		}
		next.lastEventSeq = head
		if err := delta.apply(r, &next, at, policy); err != nil {
			return err
		}
		if delta.quota {
			quotaChecked = at
		}
		delta.touchEventItems(prev, next, touched)
		if len(touched) > 0 {
			next.attempts, next.nodeCounts = maps.Clone(next.attempts), maps.Clone(next.nodeCounts)
			for id := range touched {
				if err := next.refreshAttempt(r, id); err != nil {
					return err
				}
			}
		}
		lanesChanged := delta.rotation || !maps.Equal(prev.storeLanes, next.storeLanes) ||
			!maps.Equal(prev.deskOwners, next.deskOwners) || !reflect.DeepEqual(prev.forks, next.forks) ||
			!maps.Equal(prev.activeOrdinaryAgents, next.activeOrdinaryAgents) ||
			((delta.lanes || len(delta.pending) > 0 || len(delta.reopened) > 0) && !maps.Equal(prev.heldLanes(), next.heldLanes()))
		for _, id := range delta.reopened {
			touched[id] = true
		}
		// Publish occupancy now; amortize the queue-wide repair of cached
		// lane-sensitive holds over the safety interval.
		full := !maps.Equal(prev.shut, next.shut) || !maps.Equal(prev.quotaShut, next.quotaShut) ||
			(owed && at.Sub(lastFull) >= startViewRefreshInterval)
		owed = owed || lanesChanged
		if full {
			lastFull, owed, touched = at, false, nil
		}
		if rebuild != nil && (inspectExternal || full || lanesChanged || len(touched) > 0) {
			finishRebuild, err = rebuild(r, &next, touched)
			return err
		}
		if rebuild != nil {
			next.occupied = prev.occupied
		}
		return nil
	})
	if s.onReadTx != nil {
		s.onReadTx(false)
	}
	if err == nil && finishRebuild != nil {
		err = finishRebuild()
	}
	if err != nil {
		// A new read transaction on a re-opened connection must reseed. Leave
		// both the published snapshot and its cursor untouched on failure.
		s.close()
		return prev, err
	}
	s.dataVersion, s.policy, s.quotaChecked = dv, policy, quotaChecked
	s.lastFull, s.itemsSignature, s.laneRebuildOwed = lastFull, signature, owed
	return next, nil
}

type startStoreDelta struct {
	place, history, priors, inputs, lanes, quota, rotation bool
	backendEvents, unsavedOutcomeInputs, unlogged          bool
	pending, faults                                        []event.Event
	relevant, items                                        []event.Event
	reopened                                               []string
}

func isPlaceOnlyEvent(e event.Event) bool {
	if e.Kind == "item.place" {
		return true
	}
	if e.Kind == item.UpdateKind || e.Kind == item.RecordKind {
		var obj struct {
			PlacedBy string `json:"placed_by"`
		}
		return json.Unmarshal(e.Payload, &obj) == nil && obj.PlacedBy != ""
	}
	return false
}

func (d *startStoreDelta) classify(events []event.Event) bool {
	for _, e := range events {
		if isPlaceOnlyEvent(e) {
			d.place = true
			continue
		}
		known := false
		if slices.Contains(pendingHistoryKinds, e.Kind) {
			d.pending = append(d.pending, e)
			d.lanes = true                // pending transitions such as retractClaim update lanes without a run.release event
			d.quota = true                // pending endings can change whether a backend closes
			d.unsavedOutcomeInputs = true // unsaved endings consult pendingHistory byRun
			known = true
		}
		switch e.Kind {
		case GateVerdictKind, RefusalKind, callOffered, obstacles.HeldKind, item.WaitReleasedKind:
			d.history, known = true, true
		}
		switch e.Kind {
		case run.FinishKind, run.RecordKind, run.RequestKind, run.ResumeKind,
			loopOrphanedKind, staleBaseKind, repeatingCallKind, judgeRequestKind,
			runSupersededKind, backendOutcomeKind, GateVerdictKind, RefusalKind, callOffered:
			d.priors, d.quota, known = true, true, true
		}
		switch e.Kind {
		case run.FinishKind, backendOutcomeKind, callOffered, backendProbeKind:
			d.backendEvents = true
		case run.ResumeKind, loopOrphanedKind, staleBaseKind:
			d.unsavedOutcomeInputs = true
		case rollPendingKind:
			d.unsavedOutcomeInputs, d.quota, known = true, true, true
		}
		switch e.Kind {
		case run.AllocateKind, run.FinishKind, run.ReleaseKind:
			d.lanes, known = true, true
		case item.AddKind, item.RecordKind, item.UpdateKind, item.ConflictKind, item.VerdictRecordKind, item.VerdictsWithdrawnKind:
			if e.ObjectType != "item" || e.ObjectID == nil {
				return false
			}
			d.items = append(d.items, e)
			known = true
		case backendProbeKind, quotaUpdatedKind:
			d.quota, known = true, true
		case laneRefusedKind, laneRestoredKind:
			d.rotation, known = true, true
		case inputRecordedKind:
			d.inputs, known = true, true
		case faults.HeldKind, faults.ClearedKind, BoundaryCheckKind:
			d.faults = append(d.faults, e)
			known = true
		case "run.progress", run.UsageKind, run.CopiedUsageKind,
			"run.child-session", "run.children-recorded",
			GateStartedKind, GateStoppedKind, GatePlanKind, FastGateTimingKind:
			// These are observations, not scheduling changes.
			if e.ObjectType != "run" {
				return false
			}
			continue
		case HeavyProgressKind:
			if e.ObjectType != "trunk" {
				return false
			}
			continue
		case CheckpointNotRestartedKind, LoopPassKind, ProcessCPUKind, StoreBusyKind:
			if e.ObjectType != "session" {
				return false
			}
			continue
		case dispatchDeclinedKind:
			if e.ObjectType != "item" {
				return false
			}
			continue
		case "watch.alert.opened", "watch.alert.resolved":
			if e.ObjectType != "alert" {
				return false
			}
			continue
		}
		if !known {
			return false
		}
		d.relevant = append(d.relevant, e)
	}
	return true
}

func (d *startStoreDelta) apply(r *store.Reader, s *startSnapshot, at time.Time, policy *Policy) error {
	pendingCloned := false
	if len(d.pending) > 0 {
		s.pending = clonePendingHistoryState(s.pending)
		pendingCloned = true
		if err := foldPendingHistory(nil, 0, s.pending, d.pending); err != nil {
			return err
		}
	}
	if len(d.faults) > 0 {
		private := make(map[string]map[string]ItemFault, len(s.faultsByItemReason))
		for id, byReason := range s.faultsByItemReason {
			private[id] = maps.Clone(byReason)
		}
		if err := faults.Fold(nil, 0, private, d.faults); err != nil {
			return err
		}
		s.faultsByItemReason, s.faults = private, faults.Resolve(private)
	}
	outcomesChanged := false
	if d.backendEvents || (s.backendOutcomes != nil && s.backendOutcomes.unsaved > 0 && (d.unsavedOutcomeInputs || d.quota)) {
		var err error
		outcomesChanged, err = s.advanceBackendOutcomes(r, d.backendEvents)
		if err != nil {
			return err
		}
		if outcomesChanged {
			d.priors = true
		}
	}
	if d.history || d.priors {
		if err := s.refreshRunFacts(r, d.history, d.priors); err != nil {
			return err
		}
	}
	if !d.history && len(d.items) > 0 {
		s.history.known, s.history.ranBefore = maps.Clone(s.history.known), maps.Clone(s.history.ranBefore)
		for _, e := range d.items {
			var payload struct {
				State string `json:"state"`
			}
			if json.Unmarshal(e.Payload, &payload) == nil && payload.State != "" {
				id := *e.ObjectID
				s.history.known[id] = true
				if payload.State == StateInProgress {
					s.history.ranBefore[id] = true
				}
			}
		}
	}
	if d.inputs {
		var err error
		s.inputDecisions, err = readInputDecisions(r)
		if err != nil {
			return err
		}
	}
	if len(d.pending) > 0 || d.history || d.priors || d.unlogged || len(d.faults) > 0 || len(d.items) > 0 {
		var err error
		s.pending, d.reopened, err = reopenPausedJudgeUnreadableSnapshot(r, s.pending, pendingCloned)
		if err != nil {
			return err
		}
	}
	if d.lanes || len(d.pending) > 0 || len(d.reopened) > 0 || len(d.items) > 0 {
		if err := s.refreshLaneFacts(r, at, d.lanes); err != nil {
			return err
		}
	}
	if d.quota || outcomesChanged {
		if err := s.refreshBackendsWithOutcomes(r, at, policy, outcomesChanged); err != nil {
			return err
		}
	}
	if d.lanes || d.quota {
		return s.readExpiries(r, at)
	}
	return nil
}

func (s *startSnapshot) refreshRanks(r *store.Reader) (bool, error) {
	rows, err := r.Query(`SELECT item_id, COALESCE(rank,'') FROM items ` + queueOrderSQL)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	items := make([]startQueueRow, 0, len(s.items))
	byID := maps.Clone(s.itemsByID)
	for rows.Next() {
		var rank startQueueRow
		if err := rows.Scan(&rank.id, &rank.rank); err != nil {
			return false, err
		}
		row, found := byID[rank.id]
		if !found {
			return false, nil
		}
		row.Rank = rank.rank
		byID[rank.id] = row
		items = append(items, rank)
	}
	if err := rows.Err(); err != nil {
		return false, err
	}
	if len(items) != len(byID) {
		return false, nil
	}
	s.items, s.itemsByID = items, byID
	return true, nil
}
