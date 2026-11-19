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
	if !ok {
		request, _, _ = l.currentPolicy().NextRequest(node.ItemID, node.LaneID, node.RunRequest.RunID+"-failover", rec.Backend, node.Seat)
		if request == nil {
			return node, false, Usagef("workflow seat %s has no backend route", node.Seat)
		}
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

func (l *Loop) parallelInterrupted(node Decision, rec run.Record) (Decision, bool, error) {
	policy := l.currentPolicy()
	if policy == nil {
		return node, false, Usagef("workflow seat %s has no backend route", node.Seat)
	}
	request := policy.request(node.ItemID, node.LaneID, node.RunRequest.RunID+"-interrupted", rec.Backend)
	if request == nil {
		return node, false, Usagef("workflow seat %s has no backend %s", node.Seat, rec.Backend)
	}
	request.ReadOnlyGroup = node.RunRequest.ReadOnlyGroup
	request.NodeID = node.Workflow.Stages[node.NodeIndex].ID
	request.WorkingDir = node.RunRequest.WorkingDir
	request.TimeoutMS = node.Workflow.Stages[node.NodeIndex].TimeoutMS
	request.CorrelationID = node.RunRequest.CorrelationID
	node.RunRequest = request
	node.WorkflowRestart = true
	return node, true, nil
}

func (l *Loop) advanceParallelRetry(ctx context.Context, work *pendingWork, nodes []Decision, reason string) error {
	d := nodes[0]
	d.Parallel = nodes[1:]
	d.WorkflowRestart = true
	d.Action, d.Reason = ActionDispatch, reason
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
