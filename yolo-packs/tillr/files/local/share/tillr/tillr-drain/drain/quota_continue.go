package drain

import (
	"context"
	"fmt"
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

// resumeQuotaRun preserves the original attempt, lane, staged base and files.
// When the seat still maps to the paused backend, it waits for that backend's
// window and resumes the original session. When the active profile removed that
// backend or moved the seat to an open one, it continues on the seat's current
// backend in a fresh session and records a BackendMovedKind event.
func (l *Loop) resumeQuotaRun(ctx context.Context, d Decision, rec run.Record, tick string, at time.Time, result *LoopResult, watched []string) (run.Record, string, string, string, error) {
	req := *d.RunRequest
	if req.Backend != rec.Backend {
		return run.Record{}, "", "", "", Externalf("quota-paused run %s was recorded on %s, not %s", rec.RunID, rec.Backend, req.Backend)
	}
	if err := l.refreshQuotas(ctx); err != nil {
		return run.Record{}, "", "", "", err
	}
	pol := l.currentPolicy()
	openOld := false
	if pol != nil && policyHasBackend(pol, rec.Backend) {
		var err error
		openOld, err = l.recordedBackendOpen(ctx, rec.Backend)
		if err != nil {
			return run.Record{}, "", "", "", err
		}
	}
	shut := map[string]bool{rec.Backend: !openOld}
	target, move, ok := resolveStepBackend(pol, d.ItemID, d.Seat, rec.Backend, shut)
	if !ok {
		if err := l.holdQuotaRun(ctx, d, rec.Backend); err != nil {
			return run.Record{}, "", "", "", err
		}
		return run.Record{}, "", "", rec.Backend, nil
	}
	if move != nil {
		openNew, err := l.recordedBackendOpen(ctx, target)
		if err != nil {
			return run.Record{}, "", "", "", err
		}
		if !openNew {
			if err := l.holdQuotaRun(ctx, d, rec.Backend); err != nil {
				return run.Record{}, "", "", "", err
			}
			return run.Record{}, "", "", target, nil
		}
		fresh, rebuilt := refreshRestartRequestForBackend(pol, d, &req, target)
		if !rebuilt {
			return run.Record{}, "", "", "", Externalf("quota-paused run %s cannot move to backend %s", rec.RunID, target)
		}
		move.RunID = rec.RunID
		move.NodeID = fresh.NodeID
		if err := recordBackendMove(ctx, l.cfg.Store, *move, l.cfg.Now().UTC()); err != nil {
			return run.Record{}, "", "", "", err
		}
		if err := l.clearQuotaPause(ctx, rec.RunID); err != nil {
			return run.Record{}, "", "", "", err
		}
		fresh.Resume = true
		fresh.FreshSession = true
		fresh.SessionTurn = false
		seatName := d.Seat
		if seatName == "" {
			seatName = "default"
		}
		fresh.ResumePrompt = fmt.Sprintf("The earlier session was paused for quota on %s, and the active profile moved seat %s to %s. Continue the same attempt in a fresh session, preserving existing work.", rec.Backend, seatName, target)
		resumed, log, stop, err := l.runContinuation(ctx, d.LaneID, *fresh, l.lanePath(d.LaneID), rec, tick, at, result, watched, "")
		if err == nil && stop == "" && resumed.RunID == "" && ctx.Err() == nil {
			err = Externalf("quota-paused run %s cannot continue on %s: its lane needs the staged brief", rec.RunID, target)
		}
		return resumed, log, stop, target, err
	}
	if err := l.holdQuotaRun(ctx, d, rec.Backend); err != nil {
		return run.Record{}, "", "", "", err
	}
	if !openOld {
		return run.Record{}, "", "", rec.Backend, nil
	}
	req.Resume = true
	req.FreshSession = false
	req.ResumePrompt = "The backend's quota reopened. Continue the same attempt from where you stopped, preserving existing work."
	resumed, log, stop, err := l.runContinuation(ctx, d.LaneID, req, l.lanePath(d.LaneID), rec, tick, at, result, watched, "")
	if err == nil && stop == "" && resumed.RunID == "" && ctx.Err() == nil {
		err = Externalf("quota-paused run %s cannot continue: its lane needs the staged brief and session-id", rec.RunID)
	}
	return resumed, log, stop, rec.Backend, err
}

// refreshRestartRequestForBackend rebuilds a restarted run's request from
// policy for targetBackend. When targetBackend differs from prev.Backend (or
// its route changed), session-resume flags are dropped so the new backend
// starts a fresh session while keeping the lane's worktree and candidate.
func refreshRestartRequestForBackend(policy *Policy, d Decision, prev *run.Request, targetBackend string) (*run.Request, bool) {
	if policy == nil || prev == nil || targetBackend == "" || !policyHasBackend(policy, targetBackend) {
		return nil, false
	}
	fresh := policy.Request(prev.ItemID, prev.LaneID, prev.RunID, targetBackend)
	fresh.ReadOnlyGroup = prev.ReadOnlyGroup
	if prev.WorkingDir != "" {
		fresh.WorkingDir = prev.WorkingDir
	}
	if d.Workflow != nil && d.NodeIndex >= 0 && d.NodeIndex < len(d.Workflow.Stages) {
		stage := d.Workflow.Stages[d.NodeIndex]
		fresh.NodeID = stage.ID
		if stage.TimeoutMS > 0 {
			fresh.TimeoutMS = stage.TimeoutMS
		}
	} else if prev.NodeID != "" {
		fresh.NodeID = prev.NodeID
	}
	fresh.ScratchEnv = prev.ScratchEnv
	if fresh.Backend == prev.Backend && fresh.Route() == prev.Route() {
		fresh.Resume = prev.Resume
		fresh.SessionTurn = prev.SessionTurn
		fresh.ResumePrompt = prev.ResumePrompt
		fresh.FreshSession = prev.FreshSession
	}
	if d.Workflow != nil {
		stampDecisionRequest(d, fresh)
	} else {
		fresh.Seat = prev.Seat
		fresh.Workflow = prev.Workflow
		fresh.Node = prev.Node
	}
	return fresh, true
}
