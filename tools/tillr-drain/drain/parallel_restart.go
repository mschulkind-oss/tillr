package drain

import (
	"context"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// restartParallel retains every finished result, including work failures. Only
// executions interrupted by the operator or reclaimed after owner death restart.
func restartParallel(r *store.Reader, d Decision) (Decision, bool, error) {
	nodes := parallelNodes(d)
	restarted := false
	for i, node := range nodes {
		rec, err := terminalRecord(r, node.RunRequest.RunID)
		if err != nil {
			return d, false, err
		}
		if rec != nil && rec.EndCause != run.EndSignal && rec.EndCause != run.EndLeaseReclaimed && rec.EndCause != run.EndRestart && rec.State != run.StatePaused && ClassifyAttempt(*rec, "") != AttemptUnjudged {
			continue
		}
		req := *node.RunRequest
		req.RunID = markRunID(req.RunID, "restart")
		node.RunRequest = &req
		node.WorkflowRestart = true
		nodes[i] = node
		restarted = true
	}
	if !restarted {
		return d, false, nil
	}
	d = nodes[0]
	d.Parallel = nodes[1:]
	d.WorkflowRestart = true
	return d, true, nil
}

func (l *Loop) reapParallel(ctx context.Context, tx *store.Tx, work *pendingWork, at reapEvidence) (bool, error) {
	d := work.Decision
	if len(d.Parallel) == 0 {
		return false, nil
	}
	for _, node := range parallelNodes(d) {
		if at.live[node.RunRequest.RunID] {
			return true, nil
		}
	}
	incomplete := false
	for _, node := range parallelNodes(d) {
		rec, err := terminalRecord(tx.Reader(), node.RunRequest.RunID)
		if err != nil {
			return true, err
		}
		incomplete = incomplete || rec == nil || rec.EndCause == run.EndSignal || rec.EndCause == run.EndLeaseReclaimed || rec.EndCause == run.EndRestart
	}
	if incomplete {
		if _, err := run.ReleaseLaneInTx(ctx, tx, d.LaneID, "read-only group owner died", l.cfg.Now()); err != nil {
			return true, err
		}
		for _, node := range parallelNodes(d) {
			if _, err := tx.Exec(`UPDATE node_runs SET outcome = 'interrupted' WHERE run_id = ? AND outcome = 'running'`, node.RunRequest.RunID); err != nil {
				return true, err
			}
		}
		return true, appendEffect(tx, loopOrphanedKind, d.RunRequest.RunID, struct{}{}, l.cfg.Now())
	}
	return true, nil
}
