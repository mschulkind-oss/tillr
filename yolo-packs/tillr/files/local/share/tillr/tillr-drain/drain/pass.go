package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// These events record effects, not a second stored item state: the request handed to
// a runner, the candidate a terminal run produced, and the completed examination.
// Positions are derived anew from these facts, the terminal record and the lease.
const (
	dispatchStartedKind     = "drain.started"
	candidateReadyKind      = "drain.candidate"
	candidateRetirementKind = "drain.retirement"
	candidatePreparedKind   = "drain.prepared-candidate"
	examinationDoneKind     = "drain.examined"
	loopOrphanedKind        = "drain.orphaned"
	ActionSettle            = "settle"
	ActionJudge             = "judge"
)

func init() {
	for _, kind := range []string{dispatchStartedKind, candidateReadyKind, candidateRetirementKind, candidatePreparedKind, examinationDoneKind, loopOrphanedKind, nodeAdvancedKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

type pendingWork struct {
	Ready          bool        `json:"ready,omitempty"`
	ParentRunID    string      `json:"parent_run_id,omitempty"`
	DirtyHold      *dirtyHold  `json:"dirty_hold,omitempty"`
	RepairHold     *repairHold `json:"repair_hold,omitempty"`
	Decision       Decision    `json:"decision"`
	Owner          *LoopOwner  `json:"loop_owner,omitempty"`
	BaseSHA        string      `json:"base_sha"`
	Candidate      *Candidate  `json:"candidate,omitempty"`
	Done           bool        `json:"-"`
	Orphaned       bool        `json:"-"`
	BoundaryFailed bool        `json:"boundary_failed,omitempty"`
}

func readPending(r *store.Reader) (map[string]*pendingWork, error) {
	events, err := r.Events(0)
	if err != nil {
		return nil, err
	}
	works := map[string]*pendingWork{}
	byRun := map[string]*pendingWork{}
	for _, e := range events {
		if e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case dispatchStartedKind, nodeAdvancedKind:
			var work pendingWork
			if err := json.Unmarshal(e.Payload, &work); err != nil {
				return nil, err
			}
			works[work.Decision.ItemID] = &work
			byRun[*e.ObjectID] = &work
		case candidateReadyKind, candidateRetirementKind, candidatePreparedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var c Candidate
				if err := json.Unmarshal(e.Payload, &c); err != nil {
					return nil, err
				}
				c.Request = work.Decision.RunRequest
				c.Seat = work.Decision.Seat
				work.Candidate = &c
			}
		case BoundaryCheckKind:
			var check boundaryCheckPayload
			if err := json.Unmarshal(e.Payload, &check); err != nil {
				return nil, err
			}
			if !check.Clean {
				// Continuation checks have segment identities; the payload retains
				// the attempt's run identity, including standalone resume checks.
				for _, ref := range check.Runs {
					if work := byRun[ref.RunID]; work != nil {
						work.BoundaryFailed = true
					}
				}
			}
		case loopOrphanedKind:
			if work := byRun[*e.ObjectID]; work != nil {
				work.Orphaned = true
			}
		case dirtyHeldKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var hold dirtyHold
				if err := json.Unmarshal(e.Payload, &hold); err != nil {
					return nil, err
				}
				work.DirtyHold = &hold
				work.Candidate = &hold.Candidate
			}
		case repairHeldKind:
			if work := byRun[*e.ObjectID]; work != nil {
				var hold repairHold
				if err := json.Unmarshal(e.Payload, &hold); err != nil {
					return nil, err
				}
				work.RepairHold = &hold
			}
		case itemFaultClearedKind:
			var fault ItemFault
			if err := json.Unmarshal(e.Payload, &fault); err != nil {
				return nil, err
			}
			if work := byRun[fault.RunID]; work != nil && fault.Reason == StopHostBoundaryViolation {
				work.BoundaryFailed = false
			}
		case examinationDoneKind:
			if work := byRun[*e.ObjectID]; work != nil {
				work.Done = true
			}
		}
	}
	return works, nil
}

func terminalRecord(r *store.Reader, id string) (*run.Record, error) {
	env, found, err := run.StatusRecord(r, id)
	if err != nil || !found {
		return nil, err
	}
	var status struct {
		Run *run.Record `json:"run"`
	}
	if err := json.Unmarshal(env.Payload, &status); err != nil {
		return nil, err
	}
	// The status row has no resumed column. The terminal event retains that
	// segment fact, so a replacement Loop does not continue it a second time.
	var payload []byte
	err = r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`, run.FinishKind, id).Scan(&payload)
	if err == nil && status.Run != nil {
		var terminal run.Record
		if err := json.Unmarshal(payload, &terminal); err != nil {
			return nil, err
		}
		status.Run.Resumed = terminal.Resumed
	} else if err != nil && err != sql.ErrNoRows {
		return nil, err
	}
	return status.Run, nil
}

func pendingDecision(r *store.Reader, work *pendingWork, at time.Time, inputs *RunInputs, template *string, policy *Policy, shut map[string]bool) (Decision, error) {
	d := work.Decision
	d.From, d.To = "running", ""
	d.Action, d.Reason = ActionHold, "run-live"
	if work.Ready {
		if policy == nil {
			d.Action, d.Reason, d.From, d.To = ActionHold, ReasonNoPolicy, "running", ""
			d.RunRequest = nil
			identifyDecision(&d)
			return d, nil
		}
		d.Action, d.Reason, d.From, d.To = ActionDispatch, "workflow-next-node", "running", "running"
		name := policy.firstBackend(itemRow{ItemID: d.ItemID, Seat: d.Seat})
		name, degraded, ok := policy.availableBackend(name, d.Seat, shut)
		if !ok {
			d.Action, d.Reason, d.To = ActionHold, ReasonBackendWindow, ""
		} else {
			request := policy.request(d.ItemID, d.LaneID, d.RunRequest.RunID, name)
			if request == nil {
				return Decision{}, Usagef("workflow seat %s cannot build a request", d.Seat)
			}
			d.RunRequest = request
			if degraded != "" {
				d.Reason = seatDegradedReason(degraded, name)
			}
		}
		identifyDecision(&d)
		return d, nil
	}
	if work.BoundaryFailed {
		d.From, d.To, d.Action, d.Reason = "held", "", ActionHold, StopHostBoundaryViolation
		identifyDecision(&d)
		return d, nil
	}
	if work.RepairHold != nil {
		unchanged, err := work.RepairHold.unchanged(inputs, template)
		if err != nil {
			return Decision{}, err
		}
		if unchanged {
			d.From, d.To, d.Action, d.Reason = "held", "", ActionHold, ReasonRepairStagingFailed
			identifyDecision(&d)
			return d, nil
		}
	}
	if work.DirtyHold != nil {
		paths, err := work.DirtyHold.paths(context.Background())
		if err != nil {
			return Decision{}, err
		}
		if len(paths) > 0 {
			d.From, d.To, d.Action, d.Reason = "candidate", "", ActionHold, dirtyReason(paths)
			identifyDecision(&d)
			return d, nil
		}
	}
	if work.Candidate != nil {
		d.From, d.To, d.Action, d.Reason = "candidate", "landed", ActionJudge, ActionJudge
	} else {
		rec, err := terminalRecord(r, d.RunRequest.RunID)
		if err != nil {
			return Decision{}, err
		}
		if rec != nil {
			d.To, d.Action, d.Reason = "candidate", ActionSettle, ActionSettle
		}
	}
	if d.Action != ActionHold {
		leases, err := activeLeases(r, at, []string{d.LaneID})
		if err != nil {
			return Decision{}, err
		}
		if len(leases) != 0 {
			d.Action, d.Reason, d.To = ActionHold, ReasonNoLane, ""
		}
	}
	identifyDecision(&d)
	return d, nil
}

// inputVersion binds an application to the item's state, latest terminal run and
// assigned lane's lease. JSON retains SQL NULL and all values; equality is an exact
// comparison, not an assertion that the precomputed decision is still appropriate.
// The pending facts and item fields are included so an intervening editor or pass also
// invalidates a plan. No event or decision is written on a failed comparison.
func inputVersion(r *store.Reader, d Decision) (string, error) {
	var itemState, rank, allowed, design, seat sql.NullString
	var updated int64
	if err := r.QueryRow(`SELECT state, rank, allowed_paths, design_doc, seat, updated_at FROM items WHERE item_id = ?`, d.ItemID).
		Scan(&itemState, &rank, &allowed, &design, &seat, &updated); err != nil {
		return "", err
	}
	var latest, laneState, laneRun sql.NullString
	var seq, until sql.NullInt64
	err := r.QueryRow(`SELECT run_id, event_seq FROM runs WHERE item_id = ? ORDER BY started_at DESC, event_seq DESC, rowid DESC LIMIT 1`, d.ItemID).Scan(&latest, &seq)
	if err != nil && err != sql.ErrNoRows {
		return "", err
	}
	if d.LaneID != "" {
		err = r.QueryRow(`SELECT state, run_id, leased_until FROM lanes WHERE lane_id = ?`, d.LaneID).Scan(&laneState, &laneRun, &until)
		if err != nil && err != sql.ErrNoRows {
			return "", err
		}
	}
	works, err := readPending(r)
	if err != nil {
		return "", err
	}
	faults, err := ReadItemFaults(r)
	if err != nil {
		return "", err
	}
	work := works[d.ItemID]
	raw, err := json.Marshal([]any{itemState, rank, allowed, design, seat, updated, latest, seq, laneState, laneRun, until, work, work != nil && work.Done, faults[d.ItemID], faults[d.ItemID].Seq})
	return string(raw), err
}

type plannedApplication struct {
	decision    Decision
	version     string
	parentRunID string
}

type runCompletion struct {
	id      string
	outcome laneOutcome
	detail  string
	result  *LoopResult
	timing  *PassTiming
}

// passPlans takes one read of the world. Application takes one write transaction per
// item, in this same rank order. The clock is only an observation for lease checks.
func (l *Loop) passPlans(ctx context.Context, at time.Time) ([]plannedApplication, []LaneLease, error) {
	generation := ""
	return l.passPlansGeneration(ctx, at, &generation)
}

func (l *Loop) passPlansGeneration(ctx context.Context, at time.Time, generation *string) ([]plannedApplication, []LaneLease, error) {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return nil, nil, err
	}
	defer s.Close()
	nextGeneration := *generation
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var before, after int64
		if err := tx.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM events").Scan(&before); err != nil {
			return err
		}
		if err := l.reapDeadLoopRuns(ctx, tx, at); err != nil {
			return err
		}
		released, err := run.ReapExpiredLeases(ctx, tx, at)
		if err != nil {
			return err
		}
		if err := tx.QueryRow("SELECT COALESCE(MAX(seq), 0) FROM events").Scan(&after); err != nil {
			return err
		}
		if (len(released) > 0 || after != before) && nextGeneration == "" {
			nextGeneration, err = allocateGeneration(tx)
		}
		return err
	}); err != nil {
		return nil, nil, err
	}
	*generation = nextGeneration
	var plans []plannedApplication
	var leases []LaneLease
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		leases, err = activeLeases(r, at, l.cfg.Lanes)
		if err != nil {
			return err
		}
		tick := Pass{Build: &l.build, Now: at.Format(time.RFC3339), AvailableLanes: &l.cfg.Lanes}
		if stager, ok := l.cfg.Stage.(*LaneStager); ok {
			tick.BriefTemplate = &stager.Template
		}
		questions := l.questions
		if questions == nil {
			// Standalone planner callers do not own a document refresh lifecycle.
			questions = &VantageQuestions{Root: l.cfg.Root, Command: l.cfg.QuestionsCmd, Context: ctx}
		}
		envs, err := Plan(r, tick, l.cfg.Policy, questions)
		if err != nil {
			return err
		}
		works, err := readPending(r)
		if err != nil {
			return err
		}
		for _, env := range envs {
			var d Decision
			if err := json.Unmarshal(env.Payload, &d); err != nil {
				return err
			}
			if d.Reason == ReasonNoPolicy {
				return Usagef("the loop's policy cannot build a request for item %s (seat %s)", d.ItemID, d.Seat)
			}
			if reason, held := l.heldFor(r, d.ItemID); held {
				d.Action, d.Reason, d.From, d.To = ActionHold, reason, "held", ""
				d.LaneID, d.RunRequest = "", nil
				identifyDecision(&d)
			}
			version, err := inputVersion(r, d)
			if err != nil {
				return err
			}
			parent := ""
			if work := works[d.ItemID]; work != nil {
				parent = work.ParentRunID
			}
			plans = append(plans, plannedApplication{decision: d, version: version, parentRunID: parent})
		}
		return nil
	})
	return plans, leases, err
}

func appendEffect(tx *store.Tx, kind, id string, value any, at time.Time) error {
	body, err := json.Marshal(value)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: kind, ObjectType: "run", ObjectID: &id, Payload: body, At: at})
	return err
}

// applyPlan records the transition and claims its resource atomically, after re-reading
// all three inputs. Subprocesses run after commit, never while the store is locked.
func (l *Loop) applyPlan(ctx context.Context, p plannedApplication, mainSHA string, at time.Time) (bool, *pendingWork, error) {
	generation := ""
	return l.applyPlanGeneration(ctx, p, mainSHA, at, &generation)
}

func (l *Loop) applyPlanGeneration(ctx context.Context, p plannedApplication, mainSHA string, at time.Time, generation *string) (bool, *pendingWork, error) {
	if p.decision.Action == ActionHold {
		if !strings.HasPrefix(p.decision.Reason, ReasonTrunkPathDirty+": ") {
			return false, nil, nil
		}
		s, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return false, nil, err
		}
		defer s.Close()
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			version, err := inputVersion(tx.Reader(), p.decision)
			if err != nil || version != p.version {
				return err
			}
			return recordDirtyDecision(tx, p.decision, at)
		})
		return false, nil, err
	}
	nextGeneration := *generation
	correlate := func(tx *store.Tx, d *Decision) error {
		if nextGeneration == "" {
			var err error
			nextGeneration, err = allocateGeneration(tx)
			if err != nil {
				return err
			}
		}
		d.PassID = nextGeneration
		if d.RunRequest != nil {
			request := *d.RunRequest
			request.CorrelationID = nextGeneration
			d.RunRequest = &request
		}
		return nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, nil, err
	}
	defer s.Close()
	applied := false
	var work *pendingWork
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		version, err := inputVersion(tx.Reader(), p.decision)
		if err != nil {
			return err
		}
		if version != p.version {
			return nil
		}
		d := p.decision
		if d.Inputs != nil && (d.Action == ActionDispatch || d.Action == ActionRetry) {
			rows, err := readItems(tx.Reader())
			if err != nil {
				return err
			}
			for _, row := range rows {
				if row.ItemID != d.ItemID {
					continue
				}
				now, err := currentRunInputs(tx.Reader(), row, l.cfg.Policy, l.cfg.Root, l.build)
				if err != nil {
					return err
				}
				if *now != *d.Inputs {
					return nil
				}
			}
		}
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		work = works[d.ItemID]
		switch d.Action {
		case ActionDispatch, ActionRetry:
			for _, owner := range works {
				if !owner.Done && owner.Decision.LaneID == d.LaneID && !(owner.Ready && owner.Decision.ItemID == d.ItemID) {
					return nil
				}
			}
			leases, err := activeLeases(tx.Reader(), at, []string{d.LaneID})
			if err != nil {
				return err
			}
			if len(leases) != 0 {
				return nil
			}
			heldForks, err := liveForks(tx.Reader(), at)
			if err != nil {
				return err
			}
			items, err := readItems(tx.Reader())
			if err != nil {
				return err
			}
			for _, item := range items {
				if item.ItemID != d.ItemID {
					continue
				}
				fork := forkOf(item.Allowed)
				for _, held := range heldForks {
					if fork != "" && held.fork == fork && !(work != nil && work.Ready && held.itemID == d.ItemID) {
						return nil
					}
				}
			}
			if work != nil && !work.Done && !work.Ready {
				return nil
			}
			if err := correlate(tx, &d); err != nil {
				return err
			}
			owner, err := currentLoopOwner()
			if err != nil {
				return err
			}
			if d.BlockConflict != nil {
				c := *d.BlockConflict
				c.Status, c.DesignRunID = "awaiting", d.RunRequest.RunID
				d.BlockConflict = &c
				if err := item.RecordConflict(tx, c, at); err != nil {
					return err
				}
			} else if d.ConflictID != "" {
				conflicts, err := item.ReadConflicts(tx.Reader())
				if err != nil {
					return err
				}
				c := conflicts[d.ItemID]
				if c.ConflictID != d.ConflictID || c.Status != "answered" {
					return nil
				}
				c.Status = "closed"
				if err := item.RecordConflict(tx, c, at); err != nil {
					return err
				}
			}
			base := mainSHA
			if work != nil && work.Ready {
				base = work.BaseSHA
			}
			work = &pendingWork{Decision: d, BaseSHA: base, Owner: owner}
			if _, err := run.ClaimLease(ctx, tx, *d.RunRequest, d.LaneID, at); err != nil {
				return err
			}
			if err := startNode(tx, d); err != nil {
				return err
			}
			if err := appendEffect(tx, dispatchStartedKind, d.RunRequest.RunID, work, at); err != nil {
				return err
			}
		case ActionSettle, ActionJudge:
			if work == nil || work.Done {
				return nil
			}
			leases, err := activeLeases(tx.Reader(), at, []string{d.LaneID})
			if err != nil {
				return err
			}
			if len(leases) > 0 {
				return nil
			}
			if err := correlate(tx, &d); err != nil {
				return err
			}
			// Settlement must retain the candidate's worktree too. A terminal record
			// released the run's lease, so reclaim it before inspecting or gating it.
			if _, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = ?, leased_until = NULL WHERE lane_id = ?`, run.LaneStateLeased, d.RunRequest.RunID, d.LaneID); err != nil {
				return err
			}
		}
		if err := recordPassDecision(tx, d, at); err != nil {
			return err
		}
		applied = d.Action != ActionHold
		return nil
	})
	if err == nil && applied {
		*generation = nextGeneration
	}
	return applied, work, err
}

func (l *Loop) releaseExamination(ctx context.Context, d Decision) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = ? AND run_id = ?`, run.LaneStateIdle, d.LaneID, d.RunRequest.RunID)
		return err
	})
}

func (l *Loop) markExamined(ctx context.Context, d Decision) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		at := l.cfg.Now().UTC()
		if err := appendEffect(tx, examinationDoneKind, d.RunRequest.RunID, struct{}{}, at); err != nil {
			return err
		}
		return routeSecondFailure(tx, d, at)
	})
}

func (l *Loop) launchRun(ctx context.Context, d Decision, at time.Time, watched []string, result *LoopResult) {
	id := d.RunRequest.RunID
	l.active[id] = true
	l.mu.Lock()
	l.laneChanges[d.LaneID]++
	epochs := map[string]int{}
	for _, lane := range watched {
		epochs[lane] = l.laneChanges[lane]
	}
	l.mu.Unlock()
	result.Dispatched++
	if d.Reason == ReasonFailover {
		l.reportf("loop: failover %s -> %s on %s\n", d.ItemID, d.RunRequest.Backend, d.LaneID)
	}
	if len(d.Reason) > len("seat-degraded") && d.Reason[:len("seat-degraded")] == "seat-degraded" {
		l.reportf("loop: %s on %s\n", d.Reason, d.LaneID)
	}
	l.reportf("loop: tick %d dispatch %s -> %s on %s\n", result.Ticks, d.ItemID, d.RunRequest.Backend, d.LaneID)
	go func() {
		start := time.Now()
		completion := runCompletion{id: id, timing: &PassTiming{TickID: d.RunRequest.CorrelationID, AtMS: at.UnixMilli(), GateBudgetMS: l.gateBudget.Milliseconds()}}
		local := LoopResult{}
		before, err := l.snapshot(ctx, []string{l.lanePath(d.LaneID)}, watched)
		if err == nil {
			_, _, err = l.cfg.Steps.Run(ctx, *d.RunRequest)
			if ctx.Err() != nil {
				// Preserve why this worker ended even though the loop's context is gone.
				if markErr := appendJudgment(context.WithoutCancel(ctx), l.cfg.Store, loopOrphanedKind, "run", id, struct{}{}, l.cfg.Now().UTC()); markErr != nil {
					err = markErr
				}
			}
			if err == nil {
				completion.outcome.stop, err = l.closeAsyncBracket(context.WithoutCancel(ctx), d, at, before, &local, watched, epochs)
			}
		}
		completion.outcome.err = err
		completion.detail = local.Detail
		completion.timing.DispatchMS = time.Since(start).Milliseconds()
		l.completions <- completion
	}()
}

func (l *Loop) takeCompletion(c runCompletion, result *LoopResult) (string, error) {
	// The worker's durable outcome belongs to its already applied transition.
	// Index it even when no successor is dispatchable, or when shutdown is
	// consuming the completion. Empty polls never call this path.
	defer l.index(context.Background())
	delete(l.active, c.id)
	if c.result != nil {
		mergeExamination(result, c.result)
		if c.result.Rejected > 0 {
			l.finalizeRepeatedRefusal(result)
			if line := RepeatedRefusalLine(*result); line != "" {
				l.reportf("loop: repeated-refusal: %s\n", line)
			}
		}
	}
	if c.timing != nil {
		if err := l.recordPass(c.timing); err != nil {
			return "", err
		}
	}
	if c.detail != "" {
		result.Detail = c.detail
	}
	return c.outcome.stop, c.outcome.err
}

// pass advances each item's one planned transition without waiting for a sibling.
// A fixed point waits on completion, expiry, polling, or operator cancellation.
func (l *Loop) pass(ctx context.Context, result *LoopResult) (stop string, err error) {
	for {
		select {
		case c := <-l.completions:
			if stop, err := l.takeCompletion(c, result); stop != "" || err != nil {
				return stop, err
			}
		default:
			goto planned
		}
	}
planned:
	mainSHA, err := l.refreshDocuments(ctx, result)
	if err != nil {
		return "", err
	}
	if err := l.maybeRolling(ctx, mainSHA, false, result); err != nil {
		return "", err
	}
	at := l.cfg.Now().UTC()
	timing := &PassTiming{AtMS: at.UnixMilli(), GateBudgetMS: l.gateBudget.Milliseconds()}
	defer func() {
		if timing.TickID != "" {
			result.Ticks++
			if result.FirstGeneration == "" {
				result.FirstGeneration = strings.TrimPrefix(timing.TickID, "gen-")
			}
			result.LastGeneration = strings.TrimPrefix(timing.TickID, "gen-")
			l.index(ctx)
		}
	}()
	plans, leases, err := l.passPlansGeneration(ctx, at, &timing.TickID)
	if err != nil {
		return "", err
	}
	answers, counts, err := passAnswers(plans)
	if err != nil {
		return "", err
	}
	timing.DecisionCounts = counts
	defer func() {
		if timing.TickID == "" {
			return
		}
		if recordErr := l.observePass(timing, answers); recordErr != nil && err == nil {
			err = recordErr
		}
	}()
	var holds []Decision
	// Watch currently idle offered lanes. A sibling legitimately started later
	// is removed from both status comparisons when this run's bracket closes.
	watched := []string{}
	busy := map[string]bool{}
	for _, p := range plans {
		if p.decision.RunRequest != nil {
			busy[p.decision.LaneID] = true
		}
	}
	for _, lane := range l.cfg.Lanes {
		if !busy[lane] {
			watched = append(watched, lane)
		}
	}
	stale := false
	for _, p := range plans {
		d := p.decision
		d.PassID = timing.TickID
		p.decision = d
		if d.Action == ActionHold {
			holds = append(holds, d)
		}
		if l.active[p.parentRunID] || ((d.Action == ActionSettle || d.Action == ActionJudge) && l.active[d.RunRequest.RunID]) {
			continue
		}
		ok, work, err := l.applyPlanGeneration(ctx, p, mainSHA, at, &timing.TickID)
		if err != nil {
			return "", err
		}
		if !ok {
			if d.Action != ActionHold {
				stale = true
			}
			continue
		}
		timing.Applied++
		if d.RunRequest != nil {
			request := *d.RunRequest
			request.CorrelationID = timing.TickID
			d.RunRequest = &request
		}
		switch d.Action {
		case ActionDispatch, ActionRetry:
			start := time.Now()
			base := mainSHA
			if work != nil {
				base = work.BaseSHA
			}
			staged, err := l.stageDispatches(ctx, []Decision{d}, base, result)
			if err != nil {
				return "", err
			}
			timing.StageMS += time.Since(start).Milliseconds()
			if len(staged) == 0 {
				if err := l.cancelStaging(ctx, d); err != nil {
					return "", err
				}
				continue
			}
			l.launchRun(ctx, d, at, watched, result)
		case ActionSettle, ActionJudge:
			l.launchExamination(ctx, d, work, at, timing, watched)
		}
	}
	if timing.Applied > 0 || timing.TickID != "" {
		l.waitingDetail = ""
		return "", nil
	}
	if len(l.active) > 0 {
		l.waitingDetail = ""
		return l.waitForTrigger(ctx, leases, at, result)
	}
	if stale {
		return "", nil
	}
	before := l.rollingRuns(result)
	if err := l.maybeRolling(ctx, mainSHA, true, result); err != nil {
		return "", err
	}
	if l.rollingRuns(result) != before {
		return "", nil
	}
	return l.planNothing(ctx, holds, leases, at, result)
}

// launchExamination runs the claimed settlement or judgment independently;
// the completion is a wake trigger and measurement, not its durable outcome.
func (l *Loop) launchExamination(ctx context.Context, d Decision, work *pendingWork, at time.Time, passTiming *PassTiming, watched []string) {
	l.active[d.RunRequest.RunID] = true
	timing := PassTiming{TickID: passTiming.TickID, AtMS: passTiming.AtMS, GateBudgetMS: passTiming.GateBudgetMS}
	go func() {
		local := &LoopResult{}
		completion := runCompletion{id: d.RunRequest.RunID, result: local, timing: &timing}
		if d.Action == ActionSettle {
			s, err := store.OpenRead(ctx, l.cfg.Store)
			var rec *run.Record
			if err == nil {
				err = s.ReadTx(ctx, func(r *store.Reader) error {
					var err error
					rec, err = terminalRecord(r, d.RunRequest.RunID)
					return err
				})
				s.Close()
			}
			if err == nil && rec == nil {
				err = fmt.Errorf("run %s lost its terminal record", d.RunRequest.RunID)
			}
			if err == nil {
				l.countSpend(local, *rec)
				completion.outcome = l.settle(ctx, work.Decision, *rec, logTail(rec.ArtifactRefs), work.BaseSHA, d.RunRequest.CorrelationID, at, local, &timing, watched, true)
			} else {
				completion.outcome.err = err
			}
		} else {
			c := *work.Candidate
			c.TickID = timing.TickID
			var stop string
			var landed bool
			var err error
			if work.RepairHold != nil {
				// Retry the retained repair, not another gate and land of the same
				// original candidate. The original conflict remains the evidence.
				stop, landed, err = l.conflictFix(ctx, c, work.RepairHold.Conflict, local, &timing)
			} else if work.DirtyHold != nil {
				stop, landed, err = l.landCandidate(ctx, c, local, &timing)
			} else {
				stop, landed, err = l.verify(ctx, &c, local, &timing)
			}
			retainLane := errors.Is(err, errItemFaultHeld) || errors.Is(err, errRepairHeld) || errors.Is(err, errRepairPending) || errors.Is(err, errDirtyHeld)
			if retainLane {
				err = nil
			}
			if err != nil || stop != "" {
				l.pinQuietly(ctx, c.RunID, c.HeadSHA)
			}
			if err == nil && stop == "" && !retainLane {
				if landed {
					l.abandon(ctx, c.LaneID, "", "")
				} else {
					l.abandon(ctx, c.LaneID, c.RunID, c.HeadSHA)
				}
				err = l.markExamined(ctx, work.Decision)
			}
			completion.outcome = laneOutcome{stop, err}
		}
		if err := l.releaseExamination(context.WithoutCancel(ctx), d); err != nil && completion.outcome.err == nil {
			completion.outcome.err = err
		}
		completion.detail = local.Detail
		l.completions <- completion
	}()
}

// Results cross the completion channel only after the worker stops writing them.
// The pass alone accumulates them, so running and judging different lanes never
// race on the loop's result or require the planning goroutine to wait for a sibling.
func mergeExamination(dst, src *LoopResult) {
	dst.Dispatched += src.Dispatched
	dst.Landed += src.Landed
	dst.TrunkAcquitted += src.TrunkAcquitted
	dst.ConflictFixes += src.ConflictFixes
	dst.ConflictExhausted += src.ConflictExhausted
	dst.Rejected += src.Rejected
	dst.Candidates += src.Candidates
	dst.InfraFailures += src.InfraFailures
	dst.SpendUSD += src.SpendUSD
	dst.Tokens += src.Tokens
	dst.SpendUnknown += src.SpendUnknown
	dst.CostUnknown += src.CostUnknown
	if dst.SpendUnknownExample == "" {
		dst.SpendUnknownExample = src.SpendUnknownExample
	}
	if dst.CostUnknownExample == "" {
		dst.CostUnknownExample = src.CostUnknownExample
	}
	if dst.RefusedSteps == nil {
		dst.RefusedSteps = map[string]int{}
	}
	for step, count := range src.RefusedSteps {
		dst.RefusedSteps[step] += count
	}
	for _, id := range src.RefusedItems {
		seen := false
		for _, existing := range dst.RefusedItems {
			if id == existing {
				seen = true
				break
			}
		}
		if !seen {
			dst.RefusedItems = append(dst.RefusedItems, id)
		}
	}
}

// liveForks includes unfinished examinations and live leases, not only this
// plan's dispatches. Allocate events retain the item link for foreign runs before
// their terminal runs row exists. Examination keeps the fork during lease-free gaps.
func liveForks(r *store.Reader, at time.Time) ([]plannedFork, error) {
	held, err := readLanes(r, at)
	if err != nil {
		return nil, err
	}
	items, err := readItems(r)
	if err != nil {
		return nil, err
	}
	forks := map[string]string{}
	for _, item := range items {
		forks[item.ItemID] = forkOf(item.Allowed)
	}
	events, err := r.Events(0)
	if err != nil {
		return nil, err
	}
	owners := map[string]struct{ run, item string }{}
	for _, e := range events {
		if e.Kind != run.AllocateKind {
			continue
		}
		var claim struct {
			LaneID string `json:"lane_id"`
			RunID  string `json:"run_id"`
			ItemID string `json:"item_id"`
		}
		if err := json.Unmarshal(e.Payload, &claim); err != nil {
			return nil, err
		}
		owners[claim.LaneID] = struct{ run, item string }{claim.RunID, claim.ItemID}
	}
	var out []plannedFork
	works, err := readPending(r)
	if err != nil {
		return nil, err
	}
	for id, work := range works {
		if !work.Done && forks[id] != "" {
			out = append(out, plannedFork{id, forks[id]})
		}
	}
	for lane, owner := range owners {
		if !held[lane] {
			continue
		}
		var current sql.NullString
		if err := r.QueryRow(`SELECT run_id FROM lanes WHERE lane_id = ?`, lane).Scan(&current); err != nil {
			return nil, err
		}
		if current.Valid && current.String == owner.run && forks[owner.item] != "" {
			out = append(out, plannedFork{owner.item, forks[owner.item]})
		}
	}
	return out, nil
}

// A sibling started after the bracket opened has its own writer and bracket.
// Ignore its status in both snapshots rather than convicting that authorized work.
func (l *Loop) closeAsyncBracket(ctx context.Context, d Decision, at time.Time, before *Boundary, result *LoopResult, watched []string, epochs map[string]int) (string, error) {
	worktree := l.lanePath(d.LaneID)
	after, err := l.snapshot(ctx, []string{worktree}, watched)
	if err != nil {
		return "", err
	}
	l.mu.Lock()
	for _, lane := range watched {
		if epochs[lane] == l.laneChanges[lane] {
			continue
		}
		path := l.lanePath(lane)
		for _, snapshot := range []*Boundary{before, after} {
			for i := range snapshot.Worktrees {
				if snapshot.Worktrees[i].Path == path {
					snapshot.Worktrees[i].Status = nil
				}
			}
		}
	}
	l.mu.Unlock()
	return l.finishRunBracket(ctx, worktree, *d.RunRequest, d.RunRequest.CorrelationID, at, before, after, result)
}
