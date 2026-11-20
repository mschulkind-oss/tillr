package drain

import (
	"context"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Reserve replacement memberships before interrupting the old readers. A finish
// can release a read-only group's lease only when all its memberships settled;
// therefore the item never lends its lane between the old and new sessions.
func (l *Loop) claimBuildReaders(ctx context.Context, tx *store.Tx, w *pendingWork, facts map[string]Decision, hash string) (*pendingWork, error) {
	nodes := parallelNodes(w.Decision)
	changed := false
	for i, node := range nodes {
		req := node.RunRequest
		rec, err := terminalRecord(tx.Reader(), req.RunID)
		if err != nil {
			return nil, err
		}
		if rec != nil {
			continue
		}
		if f := facts[req.RunID]; f.Inputs != nil && f.Inputs.TillrBuild == hash {
			continue
		}
		old := req.RunID
		next := *req
		next.RunID = markRunID(next.RunID, "restart")
		next.Resume, next.FreshSession = false, false
		node.RunRequest = &next
		node.BuildRestartFrom = old
		node.WorkflowRestart = true
		inputs := RunInputs{}
		if f := facts[old]; f.Inputs != nil {
			inputs = *f.Inputs
		} else if node.Inputs != nil {
			inputs = *node.Inputs
		}
		inputs.TillrBuild = hash
		node.Inputs = &inputs
		if _, err := run.ClaimLease(ctx, tx, next, node.LaneID, l.cfg.Now()); err != nil {
			return nil, err
		}
		if err := startNode(tx, node); err != nil {
			return nil, err
		}
		if err := appendEffect(tx, inputRecordedKind, next.RunID, node, l.cfg.Now()); err != nil {
			return nil, err
		}
		nodes[i] = node
		changed = true
	}
	if !changed {
		return nil, nil
	}
	generation, err := allocateGeneration(tx)
	if err != nil {
		return nil, err
	}
	for i := range nodes {
		req := *nodes[i].RunRequest
		req.CorrelationID = generation
		nodes[i].RunRequest = &req
		nodes[i].PassID = generation
	}
	if err := appendEffect(tx, loopOrphanedKind, w.Decision.RunRequest.RunID, struct{}{}, l.cfg.Now()); err != nil {
		return nil, err
	}
	copy := *w
	copy.Decision = nodes[0]
	copy.Decision.Parallel = nodes[1:]
	copy.Decision.WorkflowRestart = true
	copy.Orphaned, copy.Examining = false, false
	copy.Owner, err = currentLoopOwner()
	if err != nil {
		return nil, err
	}
	if err := appendEffect(tx, dispatchStartedKind, copy.Decision.RunRequest.RunID, &copy, l.cfg.Now()); err != nil {
		return nil, err
	}
	return &copy, nil
}

func (l *Loop) awaitBuildReader(ctx context.Context, id string) error {
	for {
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return err
		}
		var rec *run.Record
		err = s.ReadTx(ctx, func(r *store.Reader) error { var err error; rec, err = terminalRecord(r, id); return err })
		s.Close()
		if err != nil {
			return err
		}
		here, err := currentLoopOwner()
		if err != nil {
			return err
		}
		live, err := liveRunWorkers(l.cfg.Store, id, here)
		if err != nil {
			return err
		}
		if len(live) == 0 {
			// A killed face can fail before writing run.finish. Its reserved replacement
			// still owns the group; settle only the vanished old membership, and end any
			// non-terminal (e.g. paused) runs row for this replaced reader.
			if rec == nil || !isTerminalRunState(rec.State) {
				db, err := store.Open(ctx, l.cfg.Store, true)
				if err != nil {
					return err
				}
				err = db.WriteTx(ctx, func(tx *store.Tx) error {
					if hasReadOnly, err := tableExists(tx.Reader(), "read_only_runs"); err != nil {
						return err
					} else if hasReadOnly {
						if _, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE run_id=?`, id); err != nil {
							return err
						}
					}
					if rec != nil && !isTerminalRunState(rec.State) {
						return endNonTerminalRunRow(ctx, tx, id, "superseded by build reader restart", true, l.cfg.Now().UTC())
					}
					return nil
				})
				db.Close()
				if err != nil {
					return err
				}
			}
			return nil
		}
		if !waitFor(ctx, 50*time.Millisecond) {
			return ctx.Err()
		}
	}
}
