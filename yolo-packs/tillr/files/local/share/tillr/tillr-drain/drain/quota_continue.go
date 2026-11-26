package drain

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// Quota pauses are durable exceptions to the restart rule that resumes every
// paused run on the next pass. Only single-worker runs participate here.
func quotaSingleRun(d Decision) bool {
	return d.RunRequest != nil && d.Workflow == nil && len(d.Parallel) == 0 && d.BlockConflict == nil && d.RunRequest.Backend != "command"
}

func quotaPauseBackend(r *store.Reader, id string) (string, error) {
	var backend string
	err := r.QueryRow(`SELECT backend FROM quota_pauses WHERE run_id=?`, id).Scan(&backend)
	if err == sql.ErrNoRows || (err != nil && strings.Contains(err.Error(), "no such table: quota_pauses")) {
		return "", nil
	}
	return backend, err
}

func (l *Loop) quotaInterrupted(ctx context.Context, id string) (bool, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()
	backend := ""
	err = s.ReadTx(ctx, func(r *store.Reader) error { var err error; backend, err = quotaPauseBackend(r, id); return err })
	return backend != "", err
}

// Called in the refusal-window transaction, so every peer's reason survives a
// loop restart along with its pause request. Reserves/probe errors never call it.
func (l *Loop) pauseQuotaPeers(tx *store.Tx, backend, refusing string) error {
	pauseTimeout := l.postLandingSettings().Roll.PauseTimeoutSeconds
	if pauseTimeout <= 0 {
		if cfg, err := settings.Read(l.cfg.Root); err == nil {
			pauseTimeout = cfg.Roll.PauseTimeoutSeconds
		} else {
			pauseTimeout = 120
		}
	}
	works, err := readPending(tx.Reader())
	if err != nil {
		return err
	}
	for _, work := range works {
		d := work.Decision
		if work.Done || !quotaSingleRun(d) || d.RunRequest.Backend != backend || d.RunRequest.RunID == refusing {
			continue
		}
		rec, err := terminalRecord(tx.Reader(), d.RunRequest.RunID)
		if err != nil {
			return err
		}
		if rec != nil {
			// A resume retains the preceding terminal row until it finishes.
			// Only a later finish, not that old row, establishes natural completion.
			var latest string
			err := tx.Reader().QueryRow(`SELECT kind FROM events WHERE object_type='run' AND object_id=? AND kind IN (?,?) ORDER BY seq DESC LIMIT 1`, d.RunRequest.RunID, run.ResumeKind, run.FinishKind).Scan(&latest)
			if err != nil && err != sql.ErrNoRows {
				return err
			}
			if latest != run.ResumeKind {
				continue
			}
		}
		var live int
		if err := tx.Reader().QueryRow(`SELECT count(*) FROM lanes WHERE run_id=? AND state=?`, d.RunRequest.RunID, run.LaneStateLeased).Scan(&live); err != nil {
			return err
		}
		if live != 1 {
			continue
		}
		inserted, err := tx.Exec(`INSERT INTO quota_pauses(run_id,item_id,lane_id,backend,state,paused_at) VALUES(?,?,?,?,'requested',?) ON CONFLICT(run_id) DO NOTHING`, d.RunRequest.RunID, d.ItemID, d.LaneID, backend, l.cfg.Now().UnixMilli())
		if err != nil {
			return err
		}
		n, err := inserted.RowsAffected()
		if err != nil {
			return err
		}
		if n == 0 {
			continue
		}
		if err := appendEffect(tx, run.PauseRequestKind, d.RunRequest.RunID, map[string]int64{"timeout_ms": pauseTimeout * 1000}, l.cfg.Now()); err != nil {
			return err
		}
	}
	return nil
}

func (l *Loop) holdQuotaRun(ctx context.Context, d Decision, backend string) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO quota_pauses(run_id,item_id,lane_id,backend,state,paused_at) VALUES(?,?,?,?,'waiting',?) ON CONFLICT(run_id) DO UPDATE SET state='waiting'`, d.RunRequest.RunID, d.ItemID, d.LaneID, backend, l.cfg.Now().UnixMilli())
		return err
	})
}

func (l *Loop) clearQuotaPause(ctx context.Context, id string) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error { _, err := tx.Exec(`DELETE FROM quota_pauses WHERE run_id=?`, id); return err })
}

// resumeQuotaRun preserves the original backend, session, attempt, lane, staged
// base and files. The row remains until the executor's resume claim commits.
func (l *Loop) resumeQuotaRun(ctx context.Context, d Decision, rec run.Record, tick string, at time.Time, result *LoopResult, watched []string) (run.Record, string, string, error) {
	req := *d.RunRequest
	if req.Backend != rec.Backend {
		return run.Record{}, "", "", Externalf("quota-paused run %s was recorded on %s, not %s", rec.RunID, rec.Backend, req.Backend)
	}
	if err := l.holdQuotaRun(ctx, d, rec.Backend); err != nil {
		return run.Record{}, "", "", err
	}
	if err := l.refreshQuotas(ctx); err != nil {
		return run.Record{}, "", "", err
	}
	open, err := l.recordedBackendOpen(ctx, req.Backend)
	if err != nil || !open {
		return run.Record{}, "", "", err
	}
	req.Resume = true
	req.FreshSession = false
	req.ResumePrompt = "The backend's quota reopened. Continue the same attempt from where you stopped, preserving existing work."
	resumed, log, stop, err := l.runContinuation(ctx, d.LaneID, req, l.lanePath(d.LaneID), rec, tick, at, result, watched, "")
	if err == nil && stop == "" && resumed.RunID == "" && ctx.Err() == nil {
		err = Externalf("quota-paused run %s cannot continue: its lane needs the staged brief and session-id", rec.RunID)
	}
	return resumed, log, stop, err
}
