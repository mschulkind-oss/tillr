package drain

import (
	"context"
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
			live := false
			for _, node := range parallelNodes(w.Decision) {
				faces, err := liveRunFaces(l.cfg.Store, node.RunRequest.RunID, here)
				if err != nil {
					return err
				}
				live = live || len(faces) > 0
			}
			if w.Candidate != nil || !live {
				if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND run_id=?`, run.LaneStateIdle, w.Decision.LaneID, id); err != nil {
					return err
				}
			}
		}
		return nil
	})
}
