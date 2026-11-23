package drain

import (
	"context"
	"database/sql"
	"fmt"
	"syscall"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func (l *Loop) interruptBuildRun(id string) error {
	here, err := currentLoopOwner()
	if err != nil {
		return err
	}
	workers, err := liveRunWorkers(l.cfg.Store, id, here)
	if err != nil {
		return err
	}
	for _, worker := range workers {
		dead, err := ownerDead(&worker.LoopOwner, here)
		if err != nil {
			return err
		}
		if !dead {
			if err := syscall.Kill(worker.PID, syscall.SIGTERM); err != nil && !processGone(err) {
				return fmt.Errorf("interrupt %s: %w", id, err)
			}
		}
	}
	return nil
}

// Exec keeps its PID and start time. Explicitly relinquish examination ownership:
// the next image cannot infer that the old examiner died from /proc alone.
func (l *Loop) releaseRestartExaminations(ctx context.Context) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	here, err := currentLoopOwner()
	if err != nil {
		return err
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		for _, w := range works {
			owner := w.Examiner
			if w.Examining && owner == nil {
				owner = w.Owner
			}
			if w.Decision.RunRequest == nil || owner == nil || *owner != *here {
				continue
			}
			id := w.Decision.RunRequest.RunID
			if err := appendEffect(tx, examinationReleasedKind, id, struct{}{}, l.cfg.Now()); err != nil {
				return err
			}
			if err := appendEffect(tx, examinationOwnerKind, id, (*LoopOwner)(nil), l.cfg.Now()); err != nil {
				return err
			}
			keepLease := false
			if w.Candidate != nil && !w.LaneReleased {
				dir := detachedGateDir(l.cfg.Root, l.cfg.Store, w.Candidate.Worktree, id)
				liveGate, doneGate := hasDetachedGateInDir(dir, *w.Candidate, false)
				keepLease = liveGate || doneGate
			} else {
				for _, node := range parallelNodes(w.Decision) {
					faces, err := liveRunFaces(l.cfg.Store, node.RunRequest.RunID, here)
					if err != nil {
						return err
					}
					if len(faces) > 0 {
						keepLease = true
						break
					}
				}
			}
			if !keepLease {
				if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND run_id=?`, run.LaneStateIdle, w.Decision.LaneID, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}

func (l *Loop) isRunActive(runID string) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.active[runID]
}

func (l *Loop) getLoopCtx() context.Context {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.loopCtx
}

func (l *Loop) reclaimDetachedGateExaminations(ctx context.Context) error {
	return l.reclaimDetachedGateExaminationsWith(ctx, false, nil)
}

func (l *Loop) reclaimDetachedGateExaminationsWith(ctx context.Context, stopChecked bool, scope *pollReaderScope) error {
	if l.cfg.Store == "" {
		return nil
	}
	if operatorStopped(ctx) {
		return nil
	}
	if !stopChecked {
		if stopped, err := l.stopRequestedWith(ctx, scope); err != nil {
			return err
		} else if stopped {
			return nil
		}
	}
	leased := map[string]string{}
	var works map[string]*pendingWork
	err := l.withPollReader(ctx, scope, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT lane_id, run_id FROM lanes WHERE state=? AND leased_until IS NULL AND run_id IS NOT NULL`, run.LaneStateLeased)
		if err != nil {
			return err
		}
		for rows.Next() {
			var laneID, runID string
			if err := rows.Scan(&laneID, &runID); err != nil {
				rows.Close()
				return err
			}
			if !l.isRunActive(runID) {
				leased[laneID] = runID
			}
		}
		if err := rows.Close(); err != nil {
			return err
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(leased) == 0 {
			return nil
		}
		works, err = readPending(r)
		return err
	})
	if err != nil || len(leased) == 0 {
		return err
	}

	var ws *store.Store
	defer func() {
		if ws != nil {
			ws.Close()
		}
	}()
	here, err := currentLoopOwner()
	if err != nil {
		return err
	}
	for _, w := range works {
		if w.Done || w.Candidate == nil || w.Examining || w.Examiner != nil || w.Decision.RunRequest == nil {
			continue
		}
		id := w.Decision.RunRequest.RunID
		if leased[w.Decision.LaneID] != id {
			continue
		}
		c, recErr := l.recoverRetirement(ctx, *w.Candidate)
		if recErr != nil {
			if ctx.Err() != nil {
				return ctx.Err()
			}
			c = *w.Candidate
		}
		dir := detachedGateDir(l.cfg.Root, l.cfg.Store, c.Worktree, id)
		live, done := hasDetachedGateInDir(dir, c, true)
		if ws == nil {
			ws, err = store.Open(ctx, l.cfg.Store, true)
			if err != nil {
				return err
			}
		}
		now := l.cfg.Now().UTC()
		if live || done {
			var (
				claimed       bool
				judgeDecision Decision
				claimedWork   *pendingWork
				passTiming    *PassTiming
			)
			err = ws.WriteTx(ctx, func(tx *store.Tx) error {
				pending, err := readPending(tx.Reader())
				if err != nil {
					return err
				}
				w2 := pending[w.Decision.ItemID]
				if w2 == nil || w2.Done || w2.Candidate == nil || w2.Examining || w2.Examiner != nil || w2.Decision.RunRequest == nil || w2.Decision.RunRequest.RunID != id || w2.Decision.LaneID != w.Decision.LaneID {
					return nil
				}
				var state string
				var runID, leasedUntil sql.NullString
				if err := tx.QueryRow(`SELECT state, run_id, leased_until FROM lanes WHERE lane_id=?`, w2.Decision.LaneID).Scan(&state, &runID, &leasedUntil); err != nil {
					if err == sql.ErrNoRows {
						return nil
					}
					return err
				}
				if state != run.LaneStateLeased || !runID.Valid || runID.String != id || leasedUntil.Valid {
					return nil
				}
				tickID, err := allocateGeneration(tx)
				if err != nil {
					return err
				}
				judgeDecision = w2.Decision
				judgeDecision.DecisionID = fmt.Sprintf("decision-%s-reclaim-%s", w2.Decision.ItemID, tickID)
				judgeDecision.Action = ActionJudge
				judgeDecision.PassID = tickID
				req := *judgeDecision.RunRequest
				req.CorrelationID = tickID
				judgeDecision.RunRequest = &req
				kind := examinationOwnerKind
				if w2.Decision.Workflow != nil {
					kind = examinationClaimedKind
				}
				if err := appendEffect(tx, kind, id, here, now); err != nil {
					return err
				}
				if err := recordPassDecision(tx, judgeDecision, now); err != nil {
					return err
				}
				copyWork := *w2
				copyWork.Decision = judgeDecision
				copyWork.Examining = true
				copyWork.Examiner = here
				claimedWork = &copyWork
				passTiming = &PassTiming{
					TickID:         tickID,
					AtMS:           now.UnixMilli(),
					EndMS:          now.UnixMilli(),
					GateBudgetMS:   l.gateBudget.Milliseconds(),
					DecisionCounts: map[string]int{ActionJudge: 1},
					Applied:        1,
				}
				claimed = true
				return nil
			})
			if err != nil {
				return err
			}
			if claimed {
				if err := recordPassTiming(context.WithoutCancel(ctx), l.cfg.Store, *passTiming); err != nil {
					return err
				}
				launchCtx := ctx
				if loopCtx := l.getLoopCtx(); loopCtx != nil {
					launchCtx = loopCtx
				}
				l.launchExamination(launchCtx, judgeDecision, claimedWork, now, passTiming, nil)
			}
		} else {
			err = ws.WriteTx(ctx, func(tx *store.Tx) error {
				pending, err := readPending(tx.Reader())
				if err != nil {
					return err
				}
				w2 := pending[w.Decision.ItemID]
				if w2 == nil || w2.Done || w2.Candidate == nil || w2.Examining || w2.Examiner != nil || w2.Decision.RunRequest == nil || w2.Decision.RunRequest.RunID != id || w2.Decision.LaneID != w.Decision.LaneID {
					return nil
				}
				var state string
				var runID, leasedUntil sql.NullString
				if err := tx.QueryRow(`SELECT state, run_id, leased_until FROM lanes WHERE lane_id=?`, w2.Decision.LaneID).Scan(&state, &runID, &leasedUntil); err != nil {
					if err == sql.ErrNoRows {
						return nil
					}
					return err
				}
				if state != run.LaneStateLeased || !runID.Valid || runID.String != id || leasedUntil.Valid {
					return nil
				}
				if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND run_id=? AND leased_until IS NULL`, run.LaneStateIdle, w2.Decision.LaneID, id); err != nil {
					return err
				}
				return appendEffect(tx, laneReleasedKind, id, laneReleased{LaneID: w2.Decision.LaneID, Cause: laneReleaseReap}, now)
			})
			if err != nil {
				return err
			}
		}
	}
	return nil
}
