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
		if rec != nil && rec.State == run.StatePaused {
			// A sibling can need replay while this reader has a saved session.
			continue
		}
		if rec != nil && rec.EndCause != run.EndSignal && rec.EndCause != run.EndLeaseReclaimed && rec.EndCause != run.EndRestart && ClassifyAttempt(*rec, "") != AttemptUnjudged {
			continue
		}
		req := *node.RunRequest
		req.RunID = markRunID(req.RunID, "restart")
		node.RunRequest = &req
		node.WorkflowRestart = true
		node.UnreadFooter = ""
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
		if at.Live[node.RunRequest.RunID] {
			return true, nil
		}
	}
	var interrupted []string
	strandedResume := false
	for _, node := range parallelNodes(d) {
		id := node.RunRequest.RunID
		rec, err := terminalRecord(tx.Reader(), id)
		if err != nil {
			return true, err
		}
		if rec != nil && rec.State == run.StatePaused {
			active, err := runHasActiveResume(tx.Reader(), id)
			if err != nil {
				return true, err
			}
			strandedResume = strandedResume || active
			continue
		}
		if rec == nil || rec.EndCause == run.EndSignal || rec.EndCause == run.EndLeaseReclaimed || rec.EndCause == run.EndRestart {
			interrupted = append(interrupted, id)
		}
	}
	if (len(interrupted) > 0 && !work.Orphaned) || strandedResume {
		// Release only this group's lease. A stranded resume can use its last
		// paused record again; it is not an interrupted node to replace.
		var leased bool
		if err := tx.Reader().QueryRow(`SELECT EXISTS(SELECT 1 FROM lanes WHERE lane_id=? AND run_id=? AND state=?)`, d.LaneID, d.RunRequest.ReadOnlyGroup, run.LaneStateLeased).Scan(&leased); err != nil {
			return true, err
		}
		if leased {
			if _, err := run.ReleaseLaneInTx(ctx, tx, d.LaneID, "read-only group owner died", l.cfg.Now()); err != nil {
				return true, err
			}
		}
	}
	if len(interrupted) > 0 && !work.Orphaned {
		for _, id := range interrupted {
			if _, err := tx.Exec(`UPDATE node_runs SET outcome = 'interrupted' WHERE run_id = ? AND outcome = 'running'`, id); err != nil {
				return true, err
			}
			if at.Reclaimed != nil {
				*at.Reclaimed = append(*at.Reclaimed, "loop: restarting reader run "+id+" on "+d.LaneID+": its run face vanished without a saved pause\n")
			}
		}
		return true, appendEffect(tx, loopOrphanedKind, d.RunRequest.RunID, struct{}{}, l.cfg.Now())
	}
	return true, nil
}
