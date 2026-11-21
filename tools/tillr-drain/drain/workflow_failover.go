package drain

import (
	"context"
	"fmt"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// A machinery fault retries this node, not the graph or a fail-outcome branch.
// Restart staging preserves the faulted run and restores this node's start head,
// retaining all earlier nodes' commits and the graph's original base.
func (l *Loop) retryWorkflowNode(ctx context.Context, d Decision, base string, rec run.Record, result *LoopResult) error {
	if !l.nonLanding {
		head := l.headOf(ctx, d.LaneID)
		if err := l.preserveWork(ctx, d.LaneID, rec.RunID, "infrastructure failure before failover"); err != nil {
			if stop := l.retainUnpreserved(ctx, d.LaneID, rec.RunID, head, err); stop != "" {
				return retainedAfter(d.LaneID, stop)
			}
		}
		if head != "" {
			l.pinQuietly(ctx, rec.RunID, head)
		}
	}
	if err := l.recordBackendWindow(ctx, rec); err != nil {
		return err
	}
	l.mu.Lock()
	result.InfraFailures++
	l.infraStreak++
	l.mu.Unlock()
	request, ok, err := l.nextAvailableRequest(ctx, d.ItemID, d.LaneID, markRunID(d.RunRequest.RunID, "failover"), rec.Backend, d.Seat)
	if err != nil {
		return err
	}
	if !ok {
		// Persist a ready continuation even when the whole route is closed.
		// Planning holds it until a recorded reopening, retaining this node/base.
		request, _ = l.currentPolicy().NextRequest(d.ItemID, d.LaneID, markRunID(d.RunRequest.RunID, "failover"), rec.Backend, d.Seat)
		if request == nil {
			return Usagef("workflow seat %s has no backend route", d.Seat)
		}
	}
	request.NodeID = d.Workflow.Stages[d.NodeIndex].ID
	request.WorkingDir = d.RunRequest.WorkingDir
	stampDecisionRequest(d, request)
	d.RunRequest = request
	d.NodeInfraFailures++
	d.WorkflowRestart = true
	d.Action, d.Reason = ActionDispatch, "workflow-node-failover"
	if d.Workflow.Stages[d.NodeIndex].TimeoutMS > 0 {
		request.TimeoutMS = d.Workflow.Stages[d.NodeIndex].TimeoutMS
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		changed, err := tx.Exec(`UPDATE node_runs SET outcome='running' WHERE item_id=? AND attempt=? AND node_id=?`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID)
		if err != nil {
			return err
		}
		n, err := changed.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("lost workflow node during failover")
		}
		return appendEffect(tx, nodeAdvancedKind, request.RunID, pendingWork{Decision: d, BaseSHA: base, Ready: true, ParentRunID: rec.RunID}, l.cfg.Now().UTC())
	})
}

func (l *Loop) restartInterruptedNode(ctx context.Context, d Decision, base string, rec run.Record) error {
	return l.restartWorkflowNode(ctx, d, base, rec, "interrupted", "run interrupted by signal")
}

func (l *Loop) restartWorkflowNode(ctx context.Context, d Decision, base string, rec run.Record, reason, preserveReason string) error {
	if !l.nonLanding {
		head := l.headOf(ctx, d.LaneID)
		if err := l.preserveWork(ctx, d.LaneID, rec.RunID, preserveReason); err != nil {
			if stop := l.retainUnpreserved(ctx, d.LaneID, rec.RunID, head, err); stop != "" {
				return retainedAfter(d.LaneID, stop)
			}
		}
		if head != "" {
			l.pinQuietly(ctx, rec.RunID, head)
		}
	}
	policy := l.currentPolicy()
	if policy == nil {
		return Usagef("workflow seat %s has no backend route", d.Seat)
	}
	request := policy.request(d.ItemID, d.LaneID, markRunID(d.RunRequest.RunID, reason), rec.Backend)
	if request == nil {
		return Usagef("workflow seat %s has no backend %s", d.Seat, rec.Backend)
	}
	request.NodeID = d.Workflow.Stages[d.NodeIndex].ID
	request.WorkingDir = d.RunRequest.WorkingDir
	stampDecisionRequest(d, request)
	d.RunRequest = request
	d.WorkflowRestart = true
	d.Action, d.Reason = ActionDispatch, "workflow-node-"+reason
	if d.Workflow.Stages[d.NodeIndex].TimeoutMS > 0 {
		request.TimeoutMS = d.Workflow.Stages[d.NodeIndex].TimeoutMS
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		changed, err := tx.Exec(`UPDATE node_runs SET outcome='running' WHERE item_id=? AND attempt=? AND node_id=?`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID)
		if err != nil {
			return err
		}
		n, err := changed.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("lost workflow node during %s restart", reason)
		}
		return appendEffect(tx, nodeAdvancedKind, request.RunID, pendingWork{Decision: d, BaseSHA: base, Ready: true, ParentRunID: rec.RunID}, l.cfg.Now().UTC())
	})
}
