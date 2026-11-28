package drain

import (
	"context"
	"fmt"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// newRepeatedCall is the record of one repeating-call ending and its route.
func newRepeatedCall(d Decision, rec run.Record) RepeatedCall {
	rc := RepeatedCall{RunID: rec.RunID, ItemID: d.ItemID, Backend: rec.Backend}
	if rec.RepeatingCall != nil {
		rc.Tool, rc.Args, rc.ArgsDigest, rc.Streak = rec.RepeatingCall.Tool, rec.RepeatingCall.Args, rec.RepeatingCall.ArgsDigest, rec.RepeatingCall.Streak
		rc.Action, rc.Shape = rec.RepeatingCall.Action, rec.RepeatingCall.Shape
	}
	if d.Workflow != nil && d.NodeIndex < len(d.Workflow.Stages) {
		rc.NodeID = d.Workflow.Stages[d.NodeIndex].ID
	}
	recovered := (rec.LoopRecovery != nil && rec.LoopRecovery.Count > 0) ||
		(rec.RepeatingCall != nil && rec.RepeatingCall.Action == "ended")
	rc.Route, rc.NextBackend, rc.Backends = repeatingRoute(d.RepeatedCall, rec.Backend, recovered)
	return rc
}

// recordRepeatedCall appends the ending's event. It does no I/O beyond the store.
func (l *Loop) recordRepeatedCall(ctx context.Context, rc RepeatedCall) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		return appendEffect(tx, repeatingCallKind, rc.RunID, rc, l.cfg.Now().UTC())
	})
}

// settleRepeatingCall settles an item-level run (no workflow) that pi ended for a
// repeating call: it records the ending and its route and abandons the lane, preserving
// and pinning the run's work exactly as an interrupted run's is. The next pass's planner
// acts on the recorded route (planner.repeatedDecision).
func (l *Loop) settleRepeatingCall(ctx context.Context, d Decision, rec run.Record, lane string) laneOutcome {
	rc := newRepeatedCall(d, rec)
	if err := l.recordRepeatedCall(ctx, rc); err != nil {
		return laneOutcome{err: err}
	}
	l.reportf("loop: %s %s; route %s %s\n", rec.RunID, rc.Describe(), rc.Route, rc.NextBackend)
	return laneOutcome{stop: l.abandon(ctx, lane, rec.RunID, l.headOf(ctx, lane), rc.Describe())}
}

// restartRepeatingNode settles a workflow node pi ended for a repeating call: the same
// node is dispatched again, on its lane and base, in a fresh session on the same backend
// or the node is recorded `repeating-call` so the planner holds the
// item (commandNodeHold). It charges nothing to the backend: a
// model that loops is not machinery that failed.
func (l *Loop) restartRepeatingNode(ctx context.Context, d Decision, base string, rec run.Record) error {
	policy := l.currentPolicy()
	rc := newRepeatedCall(d, rec)
	var head string
	if !l.nonLanding {
		head = l.headOf(ctx, d.LaneID)
		if err := l.preserveWork(ctx, d.LaneID, rec.RunID, rc.Describe()); err != nil {
			if stop := l.retainUnpreserved(ctx, d.LaneID, rec.RunID, head, err); stop != "" {
				return retainedAfter(d.LaneID, stop)
			}
		}
		if err := l.pinLane(ctx, d.LaneID, d.ItemID, rec.RunID, head); err != nil {
			return err
		}
	}
	l.reportf("loop: %s %s; route %s %s\n", rec.RunID, rc.Describe(), rc.Route, rc.NextBackend)
	if rc.Route == repeatRouteHold {
		s, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return err
		}
		defer s.Close()
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			if err := appendEffect(tx, repeatingCallKind, rc.RunID, rc, l.cfg.Now().UTC()); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE node_runs SET outcome=? WHERE run_id=?`, ReasonRepeatingCall, rec.RunID)
			return err
		}); err != nil {
			return err
		}
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, rc.Describe()); stop != "" {
			return retainedAfter(d.LaneID, stop)
		}
		return l.markExamined(ctx, d)
	}
	if policy == nil {
		return Usagef("workflow seat %s has no backend route", d.Seat)
	}
	request := policy.Request(d.ItemID, d.LaneID, markRunID(d.RunRequest.RunID, "fresh"), rc.NextBackend)
	request.NodeID = d.Workflow.Stages[d.NodeIndex].ID
	request.WorkingDir = d.RunRequest.WorkingDir
	if d.Workflow.Stages[d.NodeIndex].TimeoutMS > 0 {
		request.TimeoutMS = d.Workflow.Stages[d.NodeIndex].TimeoutMS
	}
	stampDecisionRequest(d, request)
	d.RunRequest = request
	d.WorkflowRestart = true
	d.UnreadFooter = ""
	d.UnreadReportRestart = false
	d.InfrastructureFrom = ""
	d.Returned, d.WorkerSessionReason = nil, ""
	d.Action, d.Reason = ActionDispatch, ReasonRepeatingCallFresh
	answered := rc
	d.RepeatedCall = &answered
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := appendEffect(tx, repeatingCallKind, rc.RunID, rc, l.cfg.Now().UTC()); err != nil {
			return err
		}
		changed, err := tx.Exec(`UPDATE node_runs SET outcome='running' WHERE item_id=? AND attempt=? AND node_id=?`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID)
		if err != nil {
			return err
		}
		if n, err := changed.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("lost workflow node during a repeating-call restart")
		}
		return appendEffect(tx, nodeAdvancedKind, request.RunID, pendingWork{Decision: d, BaseSHA: base, Ready: true, ParentRunID: rec.RunID}, l.cfg.Now().UTC())
	})
}
