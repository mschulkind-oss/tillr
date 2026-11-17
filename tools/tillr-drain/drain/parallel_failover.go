package drain

import (
	"context"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Retry only the reader whose harness failed. Finished siblings keep both their
// run identities and their checked outputs, and are not sent to a backend again.
func (l *Loop) parallelFailover(ctx context.Context, node Decision, rec run.Record, result *LoopResult) (Decision, bool, error) {
	if err := l.recordBackendWindow(ctx, rec); err != nil {
		return node, false, err
	}
	l.mu.Lock()
	result.InfraFailures++
	l.infraStreak++
	l.mu.Unlock()
	request, _, ok, err := l.nextAvailableRequest(ctx, node.ItemID, node.LaneID, node.RunRequest.RunID+"-failover", rec.Backend, node.Seat)
	if err != nil {
		return node, false, err
	}
	if !ok || node.NodeInfraFailures >= l.cfg.Policy.failovers() {
		return node, false, nil
	}
	request.ReadOnlyGroup = node.RunRequest.ReadOnlyGroup
	request.NodeID = node.Workflow.Stages[node.NodeIndex].ID
	request.TimeoutMS = node.Workflow.Stages[node.NodeIndex].TimeoutMS
	request.CorrelationID = node.RunRequest.CorrelationID
	node.RunRequest = request
	node.NodeInfraFailures++
	node.WorkflowRestart = true
	return node, true, nil
}

func (l *Loop) advanceParallelRetry(ctx context.Context, work *pendingWork, nodes []Decision) error {
	d := nodes[0]
	d.Parallel = nodes[1:]
	d.WorkflowRestart = true
	d.Action, d.Reason = ActionDispatch, "workflow-node-failover"
	next := pendingWork{Decision: d, BaseSHA: work.BaseSHA, Ready: true, ParentRunID: work.Decision.RunRequest.RunID, LoopID: work.LoopID}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		return appendEffect(tx, nodeAdvancedKind, d.RunRequest.RunID, next, l.cfg.Now())
	})
}
