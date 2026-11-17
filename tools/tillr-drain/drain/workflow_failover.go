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
	if err := l.recordBackendWindow(ctx, rec); err != nil {
		return err
	}
	l.mu.Lock()
	result.InfraFailures++
	l.infraStreak++
	l.mu.Unlock()
	request, _, ok, err := l.nextAvailableRequest(ctx, d.ItemID, d.LaneID, d.RunRequest.RunID+"-failover", rec.Backend, d.Seat)
	if err != nil {
		return err
	}
	if !ok || d.NodeInfraFailures >= l.cfg.Policy.failovers() {
		if err := l.nodeOutcome(ctx, d, "fault"); err != nil {
			return err
		}
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, l.headOf(ctx, d.LaneID), "workflow node infrastructure exhausted"); stop != "" {
			return Externalf("lane %s retained after preservation failed", d.LaneID)
		}
		return l.markExamined(ctx, d)
	}
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
