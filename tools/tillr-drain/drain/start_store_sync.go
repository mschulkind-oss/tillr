package drain

import (
	"context"
	"encoding/json"
	"maps"
	"slices"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// The watcher alone owns this reader and its cursor. data_version values are
// comparable only on the same SQLite connection, not across fresh opens.
type startStoreSync struct {
	reader       *store.Store
	dataVersion  int64
	policy       *Policy
	quotaChecked time.Time
}

func (s *startStoreSync) close() {
	if s.reader != nil {
		_ = s.reader.Close()
		s.reader = nil
	}
}

func (s *startStoreSync) syncStore(ctx context.Context, path string, at time.Time, policy *Policy, prev startSnapshot, rebuild func(*store.Reader, *startSnapshot) error) (startSnapshot, error) {
	fresh := s.reader == nil
	if fresh {
		var err error
		s.reader, err = store.OpenRead(ctx, path)
		if err != nil {
			return prev, err
		}
	}
	var dv int64
	next := prev
	quotaChecked := s.quotaChecked
	err := s.reader.ReadTx(ctx, func(r *store.Reader) error {
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
			quotaChecked = at
			if rebuild != nil {
				return rebuild(r, &next)
			}
			return nil
		}
		// Without an event to explain a write, don't presume cached inputs
		// are still valid. Narrower raw-write detection belongs in slice 7.
		if fresh || prev.pending == nil || head < prev.lastEventSeq || dv < s.dataVersion ||
			(head == prev.lastEventSeq && dv != s.dataVersion) || s.policy != policy {
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
		if delta.place {
			ok, err := next.refreshRanks(r)
			if err != nil {
				return err
			}
			if !ok {
				return reseed()
			}
		}
		next.lastEventSeq = head
		if err := delta.apply(r, &next, at, policy); err != nil {
			return err
		}
		if delta.quota {
			quotaChecked = at
		}
		// Rank-only and observation-only changes keep the already computed
		// decisions and readiness instants. Other supported changes still
		// rebuild all proposals; per-item recomputation is a separate step.
		if rebuild != nil && delta.rebuild() {
			return rebuild(r, &next)
		}
		return nil
	})
	if err != nil {
		// A new read transaction on a re-opened connection must reseed. Leave
		// both the published snapshot and its cursor untouched on failure.
		s.close()
		return prev, err
	}
	s.dataVersion, s.policy, s.quotaChecked = dv, policy, quotaChecked
	return next, nil
}

type startStoreDelta struct {
	place, history, priors, inputs, lanes, quota, rotation bool
	pending, faults                                        []event.Event
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
			d.quota = true // pending endings can change whether a backend closes
			known = true
		}
		switch e.Kind {
		case GateVerdictKind, RefusalKind, callOffered, obstacles.HeldKind:
			d.history, known = true, true
		}
		switch e.Kind {
		case run.FinishKind, run.RecordKind, run.RequestKind, run.ResumeKind,
			loopOrphanedKind, staleBaseKind, repeatingCallKind, judgeRequestKind,
			runSupersededKind, backendOutcomeKind, GateVerdictKind, RefusalKind, callOffered:
			d.priors, d.quota, known = true, true, true
		}
		switch e.Kind {
		case run.AllocateKind, run.FinishKind, run.ReleaseKind:
			d.lanes, known = true, true
		case laneRefusedKind, laneRestoredKind:
			d.rotation, known = true, true
		case inputRecordedKind:
			d.inputs, known = true, true
		case faults.HeldKind, faults.ClearedKind, BoundaryCheckKind:
			d.faults = append(d.faults, e)
			known = true
		case "run.progress", run.UsageKind, run.CopiedUsageKind:
			// These are observations, not scheduling changes.
			known = e.ObjectType == "run"
		}
		if !known {
			return false
		}
	}
	return true
}

func (d startStoreDelta) rebuild() bool {
	return d.history || d.priors || d.inputs || d.lanes || d.quota || d.rotation || len(d.pending) > 0 || len(d.faults) > 0
}

func (d startStoreDelta) apply(r *store.Reader, s *startSnapshot, at time.Time, policy *Policy) error {
	if len(d.pending) > 0 {
		s.pending = clonePendingHistoryState(s.pending)
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
	if d.history || d.priors {
		if err := s.refreshRunFacts(r, d.history, d.priors); err != nil {
			return err
		}
	}
	if d.priors || len(d.pending) > 0 {
		if err := s.refreshAttemptOrdinals(r); err != nil {
			return err
		}
	}
	if d.inputs {
		var err error
		s.inputDecisions, err = readInputDecisions(r)
		if err != nil {
			return err
		}
	}
	if d.lanes || len(d.pending) > 0 {
		if err := s.refreshLaneFacts(r, at, d.lanes); err != nil {
			return err
		}
	}
	if d.quota {
		if err := s.refreshBackends(r, at, policy); err != nil {
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
	if len(items) != len(s.items) {
		return false, nil
	}
	s.items, s.itemsByID = items, byID
	return true, nil
}
