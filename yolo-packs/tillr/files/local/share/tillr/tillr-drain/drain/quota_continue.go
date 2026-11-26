package drain

import (
	"context"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/policy"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// Quota pauses are durable exceptions to the restart rule that resumes every
// paused run on the next pass. Only single-worker runs participate here.
func quotaSingleRun(d Decision) bool {
	return d.RunRequest != nil && d.Workflow == nil && len(d.Parallel) == 0 && d.BlockConflict == nil && d.RunRequest.Backend != "command"
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
		if err := policy.RecordQuotaPeerPause(tx, d.RunRequest.RunID, d.ItemID, d.LaneID, backend, pauseTimeout, l.cfg.Now()); err != nil {
			return err
		}
	}
	return nil
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
