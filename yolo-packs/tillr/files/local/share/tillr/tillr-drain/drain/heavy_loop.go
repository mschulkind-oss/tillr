package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr-drain/internal/bgactors"
	"github.com/mschulkind-oss/tillr-drain/internal/gateland"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/notify"
	"github.com/mschulkind-oss/tillr/store"
)

type heavyParentCtxKey struct{}

// startHeavy is a trigger, not a gate on dispatch or a synchronous landing step.
// The shared flock excludes replacement loops; this mutex coalesces local triggers.
// A pending loop restart holds back new trunk proofs until the restart is answered
// so new proofs do not starve exec behind already-running work.
func (l *Loop) startHeavy(ctx context.Context, tip string) error {
	if l.isRestartReplaced() {
		return errRestartPending
	}
	step, ok := l.cfg.Steps.(HeavyStep)
	if !ok {
		return Usagef("after-merge needs a heavy judge; the configured steps have none")
	}
	procSteps, isProc := l.cfg.Steps.(*ProcessSteps)
	l.heavyMu.Lock()
	defer l.heavyMu.Unlock()
	l.heavyWanted = tip
	if l.heavyRunning || l.heavyAttempted == tip {
		return nil
	}
	l.heavyRunning = true
	// Detach from a per-examination context so finishing the landing's examination
	// does not cancel the proof, while still stopping when the loop itself stops.
	heavyCtx := ctx
	if loopCtx := l.getLoopCtx(); loopCtx != nil {
		heavyCtx = loopCtx
	} else if parent, ok := ctx.Value(heavyParentCtxKey{}).(context.Context); ok && parent != nil {
		heavyCtx = parent
	}
	// A heavy goroutine never owns its caller's mechanical-step place.
	heavyCtx = context.WithValue(heavyCtx, restartMechanicalKey{}, nil)
	l.heavyWG.Add(1)
	go func() {
		defer l.heavyWG.Done()
		if isProc {
			l.runDetachedHeavyLoop(heavyCtx, procSteps)
			return
		}
		for {
			l.heavyMu.Lock()
			requested := l.heavyWanted
			l.heavyMu.Unlock()
			runner := HeavyRunner{Root: l.cfg.Root, Store: l.cfg.Store, Work: filepath.Join(l.cfg.LaneRoot, ".rolling", "worktree"), Trunk: l.trunkRef(), Judge: func(jctx context.Context, dir, only string) ([]HeavyCheck, error) {
				checks, err := step.JudgeHeavy(jctx, dir, only)
				l.sweepDisabledMarkers(jctx)
				return checks, err
			}, OnUnrecorded: func(uctx context.Context, sha string) {
				l.recordUnrecordedTrunkCommit(uctx, sha)
			}, Report: func(run HeavyRun) {
				l.reportHeavyRun(heavyCtx, run)
			}, Fix: func(ctx context.Context, sha string, checks []HeavyCheck) error {
				return l.fixHeavyChecks(ctx, sha, checks)
			}}
			runner.Progress = func(line string) { l.reportf("%s", line) }
			runner.ReuseRed = l.heavyReuseRed
			firstTip := true
			runner.BeforeTip = func(bctx context.Context) error {
				if firstTip {
					firstTip = false
					return nil
				}
				stop, err := l.restartStopsNextHeavyProof(bctx)
				if err != nil {
					return err
				}
				if stop {
					return errRestartPending
				}
				return nil
			}
			runner.WithWorktreeRegistryLock = withWorktreeRegistryLock
			runner.UnlockRegisteredWorktree = unlockRegisteredWorktree
			if l.gatePolicy.AutoRevert {
				runner.Revert = l.revertLanding
			}
			out := HeavyAttempt{PendingTip: requested}
			var err error
			hold, busy, holdErr := acquireLoopLock(heavyCtx, l.cfg.Root)
			if holdErr != nil {
				err = holdErr
			} else if busy {
				out.Busy = true
			} else {
				runner.Lock = hold
				if l.beginHeavyProof(heavyCtx) {
					out, err = runner.RunAttempt(heavyCtx, requested)
					hold.Close()
				} else {
					hold.Close()
					err = errRestartPending
				}
			}
			if err != nil && heavyCtx.Err() == nil && !errors.Is(err, errRestartPending) {
				l.reportf("loop: heavy runner fault: %v\n", err)
				if out.PendingTip != "" {
					if eventErr := l.recordHeavyFault(heavyCtx, out.PendingTip, err); eventErr != nil {
						l.reportf("loop: recording heavy runner fault: %v\n", eventErr)
					}
				}
			}
			l.heavyMu.Lock()
			attempted := l.heavyWanted
			if len(out.Runs) > 0 {
				attempted = out.Runs[len(out.Runs)-1].TipSHA
			}
			// Only a completed run consumes this tip's trigger. Losing the lock, waiting on an
			// unconfirmed supervisor, or a fault with no verdict is not a judgment: the next
			// pass retries, so refs/tillr/green is not left unproved by a silenced tip.
			if !out.Busy && err == nil {
				l.heavyAttempted = attempted
			}
			// A trigger arriving after the runner's final tip read still owes work. Do not
			// drop it in the narrow interval between that read and releasing local ownership,
			// unless a pending restart is waiting for this proof to finish.
			if heavyCtx.Err() == nil && !out.Busy && err == nil && l.heavyWanted != requested && l.heavyWanted != attempted {
				l.heavyMu.Unlock()
				if stop, stopErr := l.restartStopsNextHeavyProof(heavyCtx); stopErr == nil && stop {
					l.heavyMu.Lock()
					l.heavyRunning = false
					l.heavyMu.Unlock()
					return
				}
				continue
			}
			l.heavyRunning = false
			l.heavyMu.Unlock()
			return
		}
	}()
	return nil
}

func (l *Loop) restartStopsNextHeavyProof(ctx context.Context) (bool, error) {
	if l.cfg.Restart == nil {
		return false, nil
	}
	for {
		if l.restartReplaced.Load() {
			return true, nil
		}
		pending, err := l.restarterPendingWith(ctx, nil)
		if err != nil || !pending {
			return false, err
		}
		done := l.busyRestartAction()
		if done == nil {
			return true, nil
		}
		select {
		case <-done:
		case <-ctx.Done():
			return false, ctx.Err()
		}
	}
}

// Admission and exec cannot cross: the writer checks heavyProofActive after
// excluding this short reader, not after waiting for the running proof.
func (l *Loop) beginHeavyProof(ctx context.Context) bool {
	if stop, err := l.restartStopsNextHeavyProof(ctx); err != nil || stop {
		return false
	}
	l.restartMechanical.RLock()
	defer l.restartMechanical.RUnlock()
	if l.restartReplaced.Load() {
		return false
	}
	if !l.restartActionDeferred() {
		if _, waitCh := l.restartWaitState(); waitCh != nil {
			return false
		}
		if l.cfg.Restart != nil {
			if pending, err := l.restarterPendingWith(ctx, nil); err != nil || pending {
				return false
			}
		}
	}
	l.heavyMu.Lock()
	defer l.heavyMu.Unlock()
	return true
}

func (l *Loop) reportHeavyRun(ctx context.Context, run HeavyRun) {
	l.reportf("loop: heavy run %s at %s; green base %s\n", run.Result, shortSHA(run.TipSHA), shortSHA(run.BaseSHA))
	if run.Result == "green" {
		l.clearUnrecordedTrunkCommit(ctx)
		l.offerGreenAdvance(ctx, run.TipSHA)
	}
	if len(run.ReusedChecks) > 0 {
		l.reportf("loop: heavy %s reused checks=%s\n", shortSHA(run.TipSHA), strings.Join(run.ReusedChecks, ","))
	}
	landings, countErr := heavy.Landings(ctx, l.cfg.Root, l.cfg.Store, run.BaseSHA, run.TipSHA)
	if countErr != nil {
		l.reportf("loop: landings per heavy run=unavailable: %v\n", countErr)
	} else {
		l.heavyMu.Lock()
		l.heavyLandingCounts = append(l.heavyLandingCounts, len(landings))
		l.heavyMu.Unlock()
		l.reportf("loop: heavy %s landings per heavy run=%d\n", shortSHA(run.TipSHA), len(landings))
	}
	for _, check := range run.Checks {
		if check.OverBudgetMS > 0 {
			l.reportf("loop: heavy %s over budget: measured %d ms, declared %d ms; keeps %s verdict\n", check.CheckID, check.DurationMS, check.BudgetMS, check.Result)
		}
		classified := classifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.HeavyCheckWord, Word: check.Result})
		if classified.Outcome == checkresult.Fail {
			l.reportf("loop: heavy %s first red landing %s; %s\n", check.CheckID, shortSHA(check.FirstRedLanding), check.Detail)
			if check.RevertedBy != "" {
				l.reportf("loop: heavy %s red from %s: reverted by %s\n", check.CheckID, shortSHA(check.FirstRedLanding), shortSHA(check.RevertedBy))
			}
		}
		if classified.Outcome == checkresult.Unavailable {
			l.reportf("loop: heavy %s unavailable; %s\n", check.CheckID, check.Detail)
		}
	}
}

func (l *Loop) fixHeavyChecks(ctx context.Context, sha string, checks []HeavyCheck) error {
	remaining, err := l.linkMatchingOpenFixers(ctx, sha, checks)
	if err != nil {
		return err
	}
	if len(remaining) == 0 {
		return nil
	}
	checks = remaining
	ids := []string{}
	watches := map[string][]string{}
	notes := []string{}
	for _, check := range checks {
		ids = append(ids, check.CheckID)
		watches[check.CheckID] = check.Reads
		if check.FirstRedLanding == "" && strings.TrimSpace(check.Detail) != "" {
			notes = append(notes, "No landing is named for "+check.CheckID+" ("+heavyBrief(check.Detail)+"); start from the tip and run the check there")
		} else if strings.HasPrefix(check.CheckID, goTestPrefix) && check.Detail != "" {
			notes = append(notes, check.CheckID+": "+check.Detail)
		}
		if check.LogPath != "" {
			notes = append(notes, "Full output of "+check.CheckID+" is in "+check.LogPath+"; its failing lines and end: "+heavyBrief(legacyLogTail(check.LogPath)))
		}
		if check.RevertRefused != "" {
			notes = append(notes, "Automatic revert of "+shortSHA(check.FirstRedLanding)+" for "+check.CheckID+" was not possible: "+heavyBrief(check.RevertRefused))
		}
	}
	green, err := heavyGreen(ctx, l.cfg.Root)
	if err != nil {
		return err
	}
	tip, err := heavyGitOutput(ctx, l.cfg.Root, "rev-parse", l.trunkRef())
	if err != nil {
		return err
	}
	paths := l.rangePaths(ctx, green, tip)
	detail := []string{}
	for _, check := range checks {
		if check.FirstRedLanding == "" {
			detail = append(detail, check.Detail)
		}
	}
	_, err = l.fileTrunkFixerNoted(ctx, sha, ids, GateOutcome{SuspectPaths: paths, CheckWatches: watches, RepairDetail: strings.Join(detail, "; "), CheckLogs: l.heavyCheckLogs(tip, checks)}, strings.Join(notes, ". "))
	return err
}

func (l *Loop) heavyInFlight() bool {
	l.heavyMu.Lock()
	defer l.heavyMu.Unlock()
	return l.heavyRunning
}
func heavyStatusLine(status HeavyStatus) string {
	return fmt.Sprintf("green=%s proved_age=%s landings_since=%d heavy=%s unavailable=%s failing=%s", shortSHA(status.GreenCommit), heavy.ProofAge(status, time.Now()), status.LandingsSince, status.Result, strings.Join(heavyIDs(status.UnavailableChecks), ","), strings.Join(heavyIDs(status.FailingChecks), ","))
}
func heavyIDs(checks []HeavyCheck) []string {
	ids := []string{}
	for _, check := range checks {
		ids = append(ids, check.CheckID)
	}
	return ids
}

// heavyCheckLogs binds the proof's evidence to the same IDs used for recovery.
// Some judges return output inline instead of writing a log file.
func (l *Loop) heavyCheckLogs(tip string, checks []HeavyCheck) map[string]string {
	logs := map[string]string{}
	for _, check := range checks {
		for _, ref := range []string{check.LogRef, check.LogPath} {
			if ref != "" && !filepath.IsAbs(ref) && legacyKeptLog(ref, "fail") == "" {
				ref = filepath.Join(l.cfg.Root, ref)
			}
			if legacyKeptLog(ref, "fail") != "" {
				logs[check.CheckID] = ref
				break
			}
		}
		if logs[check.CheckID] != "" {
			continue
		}
		if ref := l.heavyLog(tip, check.CheckID); ref != "" {
			logs[check.CheckID] = ref
			continue
		}
		if l.cfg.LaneRoot == "" || strings.TrimSpace(check.Detail) == "" {
			continue
		}
		dir := filepath.Join(l.cfg.LaneRoot, ".rolling", "logs", tip)
		err := os.MkdirAll(dir, 0755)
		if err == nil {
			var file *os.File
			file, err = os.CreateTemp(dir, "detail-*.log")
			if err == nil {
				_, err = file.WriteString(check.Detail)
				if closeErr := file.Close(); err == nil {
					err = closeErr
				}
				if err == nil {
					logs[check.CheckID] = file.Name()
				} else {
					_ = os.Remove(file.Name())
				}
			}
		}
		if err != nil {
			l.reportf("loop: could not keep heavy output for %s: %v\n", check.CheckID, err)
		}
	}
	return logs
}

// legacyLogTail is the part of a check's log a brief quotes: the last failing lines
// (test names, panics, the runner's own FAIL) and then the log's final lines, in file
// order, each once. It is bounded; the whole file stays on disk.
func legacyLogTail(path string) string {
	file, err := os.Open(path)
	if err != nil {
		return "(the log could not be read: " + err.Error() + ")"
	}
	defer file.Close()
	const window = 64 << 10
	info, err := file.Stat()
	if err != nil {
		return "(the log could not be read: " + err.Error() + ")"
	}
	start := info.Size() - window
	if start < 0 {
		start = 0
	}
	buf := make([]byte, info.Size()-start)
	if _, err := file.ReadAt(buf, start); err != nil && err != io.EOF {
		return "(the log could not be read: " + err.Error() + ")"
	}
	lines := strings.Split(strings.TrimRight(string(buf), "\n"), "\n")
	if start > 0 && len(lines) > 0 {
		lines = lines[1:] // the first line is cut mid-way
	}
	const failing, ending = 15, 5
	keep := map[int]bool{}
	picked := 0
	for i := len(lines) - 1; i >= 0 && picked < failing; i-- {
		if strings.Contains(lines[i], "FAIL") || strings.Contains(lines[i], "panic:") {
			keep[i] = true
			picked++
		}
	}
	for i := max(0, len(lines)-ending); i < len(lines); i++ {
		keep[i] = true
	}
	out := []string{}
	for i, line := range lines {
		if keep[i] {
			out = append(out, line)
		}
	}
	return strings.Join(out, "\n")
}

func (l *Loop) nowTimeUTC() time.Time {
	if l.cfg.Now != nil {
		return l.cfg.Now().UTC()
	}
	return time.Now().UTC()
}

func (l *Loop) recordUnrecordedTrunkCommit(ctx context.Context, commit string) {
	if l.cfg.Store == "" || commit == "" {
		return
	}
	s, err := store.Open(ctx, l.cfg.Store, false)
	if err != nil {
		l.reportf("loop: could not record unrecorded-trunk-commit alarm for %s: %v\n", shortSHA(commit), err)
		return
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var activeCommit string
		err := tx.Reader().QueryRow(`SELECT coalesce(json_extract(payload,'$.commit'), '')
FROM events
WHERE kind = ?
 AND seq > COALESCE((SELECT seq FROM events WHERE kind = ? ORDER BY seq DESC LIMIT 1), 0)
ORDER BY seq DESC LIMIT 1`, alarms.UnrecordedTrunkCommitKind, alarms.UnrecordedTrunkClearedKind).Scan(&activeCommit)
		if err == nil && activeCommit == commit {
			return nil
		}
		payload, err := json.Marshal(map[string]any{"commit": commit})
		if err != nil {
			return err
		}
		id := "trunk"
		_, err = tx.Append(event.Event{
			Kind:       alarms.UnrecordedTrunkCommitKind,
			ObjectType: "trunk",
			ObjectID:   &id,
			At:         l.nowTimeUTC(),
			Payload:    payload,
		})
		return err
	}); err != nil {
		l.reportf("loop: could not record unrecorded-trunk-commit alarm for %s: %v\n", shortSHA(commit), err)
		return
	}
	l.reportf("loop: ALARM %s: trunk commit %s was not made by a landing\n", alarms.UnrecordedTrunkCommit, commit)
}

func (l *Loop) clearUnrecordedTrunkCommit(ctx context.Context) {
	if l.cfg.Store == "" {
		return
	}
	s, err := store.Open(ctx, l.cfg.Store, false)
	if err != nil {
		return
	}
	defer s.Close()
	_ = s.WriteTx(ctx, func(tx *store.Tx) error {
		var active int
		err := tx.Reader().QueryRow(`SELECT COUNT(*)
FROM events
WHERE kind = ?
 AND seq > COALESCE((SELECT seq FROM events WHERE kind = ? ORDER BY seq DESC LIMIT 1), 0)`,
			alarms.UnrecordedTrunkCommitKind, alarms.UnrecordedTrunkClearedKind).Scan(&active)
		if err != nil || active == 0 {
			return err
		}
		id := "trunk"
		_, err = tx.Append(event.Event{
			Kind:       alarms.UnrecordedTrunkClearedKind,
			ObjectType: "trunk",
			ObjectID:   &id,
			At:         l.nowTimeUTC(),
			Payload:    []byte("{}"),
		})
		return err
	})
}

func (l *Loop) firstUnrecordedTrunkCommit(ctx context.Context, base, tip string) (string, error) {
	if l.cfg.Store == "" || base == "" || tip == "" || base == tip {
		return "", nil
	}
	raw, err := rawGit(ctx, l.cfg.Root, nil, "rev-list", "--first-parent", "--reverse", base+".."+tip)
	if err != nil {
		return "", err
	}
	runner := HeavyRunner{Root: l.cfg.Root, Store: l.cfg.Store}
	for _, sha := range strings.Fields(raw) {
		has, err := runner.HasLandingRecord(ctx, sha)
		if err != nil {
			return "", err
		}
		if !has {
			return sha, nil
		}
	}
	return "", nil
}

type detachedHeavyHandle struct {
	dir     string
	face    heavyrun.HeavyProofFace
	preDone *heavyrun.HeavyProofDone
}

func processStepsHeavyCallbacks() heavyrun.HeavyProofCallbacks {
	return heavyrun.HeavyProofCallbacks{
		MakeJudge: func(spec heavyrun.HeavyProofSpec) (func(context.Context, string, string) ([]HeavyCheck, error), error) {
			ps := &ProcessSteps{
				Root:                   spec.Root,
				LaneRoot:               spec.LaneRoot,
				Store:                  spec.Store,
				HeavyGateBin:           spec.HeavyGateBin,
				RollingProgram:         spec.RollingProgram,
				ReplayProgram:          spec.ReplayProgram,
				GateProgram:            spec.GateProgram,
				selfExe:                spec.SelfExe,
				heavyLogSampleInterval: time.Duration(spec.HeavyLogSampleIntervalMS) * time.Millisecond,
			}
			return ps.JudgeHeavy, nil
		},
		WithWorktreeRegistryLock: withWorktreeRegistryLock,
		UnlockRegisteredWorktree: unlockRegisteredWorktree,
	}
}

func (l *Loop) runDetachedHeavyLoop(heavyCtx context.Context, procSteps *ProcessSteps) {
	for {
		l.heavyMu.Lock()
		requested := l.heavyWanted
		l.heavyMu.Unlock()

		handle, attached, err := l.startOrAttachDetachedHeavyProof(heavyCtx, procSteps, requested)
		var done heavyrun.HeavyProofDone
		if err == nil {
			if !attached {
				// Confirm we still want to proceed or wait on the detached supervisor.
			}
			done, err = l.awaitDetachedHeavyProof(heavyCtx, procSteps, handle)
		}
		if err == nil {
			err = l.settleDetachedHeavyProof(heavyCtx, handle.dir, done)
		}
		if err != nil && heavyCtx.Err() == nil && !errors.Is(err, errRestartPending) {
			l.reportf("loop: heavy runner fault: %v\n", err)
			pendingTip := requested
			if done.Attempt.PendingTip != "" {
				pendingTip = done.Attempt.PendingTip
			}
			if pendingTip != "" {
				if eventErr := l.recordHeavyFault(heavyCtx, pendingTip, err); eventErr != nil {
					l.reportf("loop: recording heavy runner fault: %v\n", eventErr)
				}
			}
		}

		l.heavyMu.Lock()
		attempted := l.heavyWanted
		if len(done.Attempt.Runs) > 0 {
			attempted = done.Attempt.Runs[len(done.Attempt.Runs)-1].TipSHA
		}
		if err == nil && !done.Attempt.Busy && done.Error == "" && !done.Interrupted {
			l.heavyAttempted = attempted
		}
		if heavyCtx.Err() == nil && !l.isRestartReplaced() && err == nil && !done.Attempt.Busy && done.Error == "" && !done.Interrupted {
			newest, tipErr := heavy.GitOutput(heavyCtx, l.cfg.Root, "rev-parse", l.trunkRef()+"^{commit}")
			needMore := (l.heavyWanted != requested && l.heavyWanted != attempted) || (tipErr == nil && newest != "" && newest != attempted)
			if needMore {
				l.heavyMu.Unlock()
				if stop, stopErr := l.restartStopsNextHeavyProof(heavyCtx); stopErr == nil && stop {
					l.heavyMu.Lock()
					l.heavyRunning = false
					l.heavyMu.Unlock()
					return
				}
				continue
			}
		}
		l.heavyRunning = false
		l.heavyMu.Unlock()
		return
	}
}

func (l *Loop) startOrAttachDetachedHeavyProof(ctx context.Context, procSteps *ProcessSteps, requested string) (detachedHeavyHandle, bool, error) {
	dir, err := heavyrun.HeavyDirectory(ctx, l.cfg.Root)
	if err != nil {
		return detachedHeavyHandle{}, false, err
	}
	if d, ok := heavyrun.ReadHeavyProofDone(dir); ok {
		return detachedHeavyHandle{dir: dir, preDone: &d}, true, nil
	}
	if f, ok := heavyrun.ReadHeavyProofFace(dir); ok && heavyrun.HeavyProofRunning(f) {
		return detachedHeavyHandle{dir: dir, face: f}, true, nil
	}
	if !l.beginHeavyProof(ctx) {
		return detachedHeavyHandle{}, false, errRestartPending
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return detachedHeavyHandle{}, false, Externalf("creating heavy directory %s: %v", dir, err)
	}
	for _, name := range []string{
		heavyrun.HeavyAttemptFaceFile,
		heavyrun.HeavyAttemptDoneFile,
		heavyrun.HeavyAttemptSpecFile,
		heavyrun.HeavyEarlyRequestFile,
		heavyrun.HeavyEarlyAnswerFile,
	} {
		if err := os.Remove(filepath.Join(dir, name)); err != nil && !os.IsNotExist(err) {
			return detachedHeavyHandle{}, false, Externalf("clearing stale heavy state %s: %v", name, err)
		}
	}
	ownerPID, ownerStartTicks, err := heavyrun.HeavySupervisorIdentity()
	if err != nil {
		return detachedHeavyHandle{}, false, Externalf("reading loop process identity: %v", err)
	}
	spec := heavyrun.HeavyProofSpec{
		Root:                     l.cfg.Root,
		Work:                     filepath.Join(l.cfg.LaneRoot, ".rolling", "worktree"),
		Trunk:                    l.trunkRef(),
		Store:                    l.cfg.Store,
		LaneRoot:                 l.cfg.LaneRoot,
		RequestedTip:             requested,
		ReuseRed:                 l.heavyReuseRed,
		AutoRevert:               l.gatePolicy.AutoRevert,
		OwnerPID:                 ownerPID,
		OwnerStartTicks:          ownerStartTicks,
		HeavyGateBin:             procSteps.HeavyGateBin,
		RollingProgram:           procSteps.RollingProgram,
		ReplayProgram:            procSteps.ReplayProgram,
		GateProgram:              procSteps.GateProgram,
		SelfExe:                  procSteps.selfExe,
		HeavyLogSampleIntervalMS: procSteps.heavyLogSampleInterval.Milliseconds(),
		Dir:                      dir,
		FaceFile:                 filepath.Join(dir, heavyrun.HeavyAttemptFaceFile),
		DoneFile:                 filepath.Join(dir, heavyrun.HeavyAttemptDoneFile),
		EarlyRequestFile:         filepath.Join(dir, heavyrun.HeavyEarlyRequestFile),
		EarlyAnswerFile:          filepath.Join(dir, heavyrun.HeavyEarlyAnswerFile),
	}
	if target, ok := procSteps.priority.NiceTarget(ProcessClassProof); ok {
		spec.NiceTarget = &target
	}
	if err := heavyrun.PublishHeavyJSON(dir, heavyrun.HeavyAttemptSpecFile, spec); err != nil {
		return detachedHeavyHandle{}, false, Externalf("writing heavy proof spec: %v", err)
	}
	specPath := filepath.Join(dir, heavyrun.HeavyAttemptSpecFile)
	self, err := os.Executable()
	if err != nil {
		return detachedHeavyHandle{}, false, Externalf("locating executable for heavy proof supervisor: %v", err)
	}
	readyR, readyW, err := os.Pipe()
	if err != nil {
		return detachedHeavyHandle{}, false, Externalf("creating heavy proof readiness pipe: %v", err)
	}
	cmd := exec.Command(self, "--tillr-heavy-proof", specPath)
	cmd.Dir = l.cfg.Root
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if procSteps.priority.Mechanism() == "cgroup" {
		if err := procSteps.priority.ApplyToCmd(cmd, ProcessClassProof, procSteps.selfExe); err != nil {
			_ = readyW.Close()
			_ = readyR.Close()
			return detachedHeavyHandle{}, false, err
		}
	}
	cmd.ExtraFiles = []*os.File{readyW}
	if err := cmd.Start(); err != nil {
		_ = readyW.Close()
		_ = readyR.Close()
		return detachedHeavyHandle{}, false, Externalf("starting heavy proof supervisor: %v", err)
	}
	_ = readyW.Close()
	go func() { _ = cmd.Wait() }()

	var buf [1]byte
	_, _ = readyR.Read(buf[:])
	_ = readyR.Close()

	if f, ok := heavyrun.ReadHeavyProofFace(dir); ok {
		return detachedHeavyHandle{dir: dir, face: f}, false, nil
	}
	if d, ok := heavyrun.ReadHeavyProofDone(dir); ok {
		return detachedHeavyHandle{dir: dir, preDone: &d}, false, nil
	}
	return detachedHeavyHandle{}, false, Externalf("heavy proof supervisor exited before publishing live face")
}

func (l *Loop) awaitDetachedHeavyProof(ctx context.Context, procSteps *ProcessSteps, handle detachedHeavyHandle) (heavyrun.HeavyProofDone, error) {
	if handle.preDone != nil {
		if ctx.Err() != nil {
			if l.isRestartReplaced() {
				return heavyrun.HeavyProofDone{}, errRestartPending
			}
			return heavyrun.HeavyProofDone{}, ctx.Err()
		}
		return *handle.preDone, nil
	}
	ticker := time.NewTicker(25 * time.Millisecond)
	defer ticker.Stop()
	lastEarlyReqID := ""
	for {
		if l.isRestartReplaced() {
			return heavyrun.HeavyProofDone{}, errRestartPending
		}
		if req, ok := heavyrun.ReadHeavyEarlyRequest(handle.dir); ok && req.RequestID != lastEarlyReqID {
			if ans, ok := heavyrun.ReadHeavyEarlyAnswer(handle.dir); !ok || ans.RequestID != req.RequestID {
				l.handleDetachedHeavyEarly(ctx, procSteps, handle.dir, req)
			}
			lastEarlyReqID = req.RequestID
		}
		if d, ok := heavyrun.ReadHeavyProofDone(handle.dir); ok {
			return d, nil
		}
		if handle.face.PID > 0 && !heavyrun.HeavyProofRunning(handle.face) {
			if d, ok := heavyrun.ReadHeavyProofDone(handle.dir); ok {
				return d, nil
			}
			return heavyrun.HeavyProofDone{}, Externalf("heavy proof supervisor %d exited without writing %s", handle.face.PID, heavyrun.HeavyAttemptDoneFile)
		}
		select {
		case <-ctx.Done():
			if l.isRestartReplaced() {
				return heavyrun.HeavyProofDone{}, errRestartPending
			}
			return heavyrun.HeavyProofDone{}, ctx.Err()
		case <-ticker.C:
		}
	}
}

func (l *Loop) handleDetachedHeavyEarly(ctx context.Context, procSteps *ProcessSteps, dir string, req heavyrun.HeavyEarlyRequest) {
	replayRunner := HeavyRunner{
		Root:  l.cfg.Root,
		Work:  filepath.Join(l.cfg.LaneRoot, ".rolling", "worktree-revert-replay"),
		Trunk: l.trunkRef(),
		Judge: func(jctx context.Context, checkDir, only string) ([]HeavyCheck, error) {
			return procSteps.JudgeHeavy(context.WithValue(jctx, heavyrun.HeavyUnownedKey{}, true), checkDir, only)
		},
		Progress:                 func(line string) { l.reportf("%s", line) },
		Now:                      l.cfg.Now,
		WithWorktreeRegistryLock: withWorktreeRegistryLock,
		UnlockRegisteredWorktree: unlockRegisteredWorktree,
	}
	if replayRunner.Now == nil {
		replayRunner.Now = time.Now
	}
	outcome, err := l.revertLanding(ctx, req.TipSHA, req.LandingSHA, req.Checks, func(rctx context.Context, sha, id string) (bool, error) {
		return replayRunner.Replay(rctx, sha, id, "revert-check")
	})
	if ctx.Err() != nil && l.isRestartReplaced() {
		// Leave the request unanswered so the replacement Loop handles it after exec.
		return
	}
	ans := heavyrun.HeavyEarlyAnswer{
		RequestID:    req.RequestID,
		Outcome:      outcome,
		AnsweredAtMS: time.Now().UnixMilli(),
	}
	if err != nil {
		ans.Error = err.Error()
		ans.CleanupLost = heavyrun.CleanupLost(err)
	}
	_ = heavyrun.WriteHeavyEarlyAnswer(dir, ans)
}

func (l *Loop) settleDetachedHeavyProof(ctx context.Context, dir string, done heavyrun.HeavyProofDone) error {
	if done.Interrupted && len(done.Attempt.Runs) == 0 {
		l.clearDetachedHeavyFiles(dir)
		return context.Canceled
	}
	if done.Attempt.Busy {
		l.clearDetachedHeavyFiles(dir)
		return nil
	}
	if lockErr := l.checkRestartAdmission(ctx, false); lockErr != nil {
		// Leave heavy-attempt.done.json on disk so the post-exec image settles it.
		return lockErr
	}
	sctx := ctx

	hold, busy, holdErr := acquireLoopLock(sctx, l.cfg.Root)
	if holdErr != nil {
		return holdErr
	}
	if busy {
		return nil
	}
	defer hold.Close()

	fixByTip := map[string]heavyrun.HeavyFixPlan{}
	for _, fp := range done.FixPlans {
		fixByTip[fp.TipSHA] = fp
	}

	var settleErr error
	for i := range done.Attempt.Runs {
		run := done.Attempt.Runs[i]
		fp := fixByTip[run.TipSHA]
		if fp.UnrecordedCommit != "" {
			l.recordUnrecordedTrunkCommit(sctx, fp.UnrecordedCommit)
		}
		if run.Result == "green" && done.Error == "" {
			base, err := heavy.Green(sctx, l.cfg.Root)
			if err != nil {
				run.Result = "unknown"
				run.Detail = err.Error()
				settleErr = err
			} else {
				current, curErr := heavy.GitOutput(sctx, l.cfg.Root, "rev-parse", l.trunkRef()+"^{commit}")
				if curErr != nil {
					settleErr = Externalf("reading %s before advancing %s: %v", l.trunkRef(), heavy.GreenRef, curErr)
					run.Result = "unknown"
					run.Detail = settleErr.Error()
				} else if onTrunk, ancErr := fixer.TrunkAncestor(sctx, l.cfg.Root, run.TipSHA, current); ancErr != nil {
					settleErr = Externalf("checking whether proved tip %s is on %s %s: %v", run.TipSHA, l.trunkRef(), current, ancErr)
					run.Result = "unknown"
					run.Detail = settleErr.Error()
				} else if !onTrunk {
					settleErr = Externalf("proved tip %s left the trunk (trunk is now %s); %s stays", run.TipSHA, current, heavy.GreenRef)
					run.Result = "unknown"
					run.Detail = settleErr.Error()
				} else if run.TipSHA != base {
					old := base
					if old == "" {
						old = strings.Repeat("0", len(run.TipSHA))
					}
					passed := []string{}
					for _, check := range run.Checks {
						if classifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.HeavyCheckWord, Word: check.Result}).Outcome == checkresult.Pass {
							passed = append(passed, check.CheckID)
						}
					}
					err = l.withRestartCommit(sctx, func(txCtx context.Context) error {
						if err := heavyrun.PublishHeavyJSON(dir, "green-"+run.TipSHA+".json", heavy.GreenProof{
							Commit:   run.TipSHA,
							ProvedAt: l.nowTimeUTC(),
							Passed:   passed,
						}); err != nil {
							return err
						}
						_, err := heavy.GitOutput(txCtx, l.cfg.Root, "update-ref", heavy.GreenRef, run.TipSHA, old)
						return err
					})
					if err != nil {
						settleErr = Externalf("green compare-and-swap refused: %v", err)
						run.Result = "unknown"
						run.Detail = settleErr.Error()
					} else if l.notifyEnabled() && l.cfg.Store != "" {
						_ = notify.Publish(l.cfg.Store, notify.Frame{
							Topic: notify.TopicProofGreen,
							AtMS:  l.nowTimeUTC().UnixMilli(),
						})
					}
				}
			}
		}
		if pe := heavyrun.PublishHeavy(dir, run); pe != nil && settleErr == nil {
			settleErr = pe
		}
		l.reportHeavyRun(sctx, run)
		for _, sha := range fp.Order {
			if fixErr := l.withRestartCommit(sctx, func(txCtx context.Context) error {
				return l.fixHeavyChecks(txCtx, sha, fp.ByLanding[sha])
			}); fixErr != nil && settleErr == nil {
				settleErr = fixErr
			}
		}
	}
	if settleErr == nil && done.Error != "" {
		settleErr = errors.New(done.Error)
	}
	l.clearDetachedHeavyFiles(dir)
	return settleErr
}

func (l *Loop) clearDetachedHeavyFiles(dir string) {
	for _, name := range []string{
		heavyrun.HeavyAttemptDoneFile,
		heavyrun.HeavyAttemptFaceFile,
		heavyrun.HeavyAttemptSpecFile,
		heavyrun.HeavyEarlyRequestFile,
		heavyrun.HeavyEarlyAnswerFile,
	} {
		_ = os.Remove(filepath.Join(dir, name))
	}
}

func (l *Loop) reclaimDetachedHeavyProof(ctx context.Context) {
	if l.nonLanding || l.isRestartReplaced() || l.cfg.Root == "" || l.heavyInFlight() {
		return
	}
	if _, isProc := l.cfg.Steps.(*ProcessSteps); !isProc {
		return
	}
	dir, err := heavy.Directory(ctx, l.cfg.Root)
	if err != nil {
		return
	}
	if _, ok := heavyrun.ReadHeavyProofDone(dir); ok {
		_ = l.startHeavy(ctx, "")
		return
	}
	if face, ok := heavyrun.ReadHeavyProofFace(dir); ok && heavyrun.HeavyProofRunning(face) {
		_ = l.startHeavy(ctx, face.TipSHA)
	}
}

// startTrunkProofActor runs the trunk observation, landing reconciliation, and
// heavy-proof background actor outside the scheduling pass (D-SN3, Section 5.7),
// woken by trunk.moved / proof.green / resync and reconciled every
// StorePollInterval (15s default).
func (l *Loop) startTrunkProofActor(ctx context.Context, result *LoopResult) func() {
	actorCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	l.actors.Ensure()
	l.mu.Lock()
	l.actors.TrunkProof.Active = true
	timer := l.actors.TrunkProof.Timer
	l.mu.Unlock()

	go func() {
		defer close(done)
		defer func() {
			l.mu.Lock()
			l.actors.TrunkProof.Active = false
			l.mu.Unlock()
		}()
		wait := l.cfg.StorePollInterval
		if wait <= 0 {
			wait = DefaultFallbackPollInterval
		}
		gateland.RunLoop(actorCtx, wait, timer, func() <-chan struct{} {
			if !l.notifyEnabled() {
				return nil
			}
			return l.actors.TrunkProofWakeChan()
		}, func() {
			if actorCtx.Err() != nil || l.isRestartReplaced() {
				return
			}
			if stopped, err := l.stopRequested(actorCtx); stopped || err != nil {
				return
			}
			beforeSeq, _ := l.readEventHeadSeq(actorCtx)
			if err := l.runTrunkProofStep(actorCtx, result, false); err != nil {
				l.reportActorError(actorCtx, "trunk-proof", err)
				return
			}
			afterSeq, _ := l.readEventHeadSeq(actorCtx)
			if afterSeq != beforeSeq {
				l.signalPassWake()
			}
		})
	}()

	return func() {
		cancel()
		<-done
	}
}

func (l *Loop) runTrunkProofStep(ctx context.Context, result *LoopResult, startup bool) error {
	l.actors.TrunkMu.Lock()
	defer l.actors.TrunkMu.Unlock()
	l.actors.TrunkProof.Runs.Add(1)

	if !l.nonLanding {
		l.sweepDisabledMarkers(ctx)
	}
	if err := l.observeTrunkCoreWorktree(ctx); err != nil {
		l.reportf("loop: could not record trunk core.worktree observation: %v\n", err)
	}
	replay, err := l.recoverPendingRemoteSyncReplay(ctx)
	if err == nil && replay != nil {
		l.afterRemoteSyncReplay(ctx, replay, result)
	}
	if err != nil {
		return err
	}
	var endObserve func()
	if !l.actorsActive() {
		endObserve = l.startPassPhase("observe_trunk")
	}
	_, observeErr := l.observeTrunk(ctx, "")
	if endObserve != nil {
		endObserve()
	}
	if observeErr != nil {
		return observeErr
	}
	if err := l.reconcileRewrittenLandings(ctx); err != nil {
		return err
	}
	var mainSHA string
	if !l.nonLanding {
		tip, err := l.revParse(ctx, l.cfg.Root, l.trunkRef())
		if err != nil {
			return err
		}
		mainSHA = tip
		if l.gatePolicy.LandingStrategy == "after-merge" {
			l.offerStartupGreen(ctx)
			if err := l.maybeRolling(ctx, mainSHA, false, result); err != nil {
				return err
			}
		}
		if err := l.reconcileLandingCalls(ctx); err != nil {
			return err
		}
		if err := l.closeStaleRemoteSyncItems(ctx); err != nil {
			return err
		}
		l.maybeRecoverClearedPushHold(ctx)
		if !startup && l.gatePolicy.LandingStrategy != "after-merge" {
			if err := l.maybeRolling(ctx, mainSHA, false, result); err != nil {
				return err
			}
		}
		if err := l.resolveProvedFixers(ctx); err != nil {
			return err
		}
		if err := l.settleResolvedFixerRuns(ctx); err != nil {
			return err
		}
		if err := l.releaseTrunkAcquittals(ctx); err != nil {
			return err
		}
		if err := l.releaseChangedCandidateGateFaults(ctx); err != nil {
			return err
		}
		if err := l.releaseAdvancedGateDigestFaults(ctx); err != nil {
			return err
		}
		if err := l.releaseStaleBaseConflicts(ctx); err != nil {
			return err
		}
		if _, err := l.advanceSplitClosingChecks(ctx, mainSHA, result); err != nil {
			return err
		}
	}

	if changed, err := l.refreshTrunkCache(ctx, mainSHA); err != nil {
		return err
	} else if changed {
		l.signalPassWake()
	}
	return nil
}

func (l *Loop) refreshTrunkCache(ctx context.Context, tip string) (bool, error) {
	if l.nonLanding {
		return l.actors.SetCachedTrunk(bgactors.TrunkCache{Valid: true}), nil
	}
	if tip == "" {
		var err error
		tip, err = l.revParse(ctx, l.cfg.Root, l.trunkRef())
		if err != nil {
			return false, err
		}
	}
	green, err := provedLaneBaseAt(ctx, l.cfg.Root, l.gatePolicy.LaneBase, tip)
	if err != nil {
		// Record tip even if green is temporarily rewritten; observeTrunk /
		// trunk reconciliation handles the rewrite without failing the actor.
		return l.actors.SetCachedTrunk(bgactors.TrunkCache{Tip: tip, Valid: true}), nil
	}
	var unprovedSet map[string]bool
	var blockBases map[string]string
	if green != "" && green != tip {
		if list, err := heavyGitOutput(ctx, l.cfg.Root, "rev-list", green+".."+tip); err == nil {
			fields := strings.Fields(list)
			if len(fields) > 0 {
				unprovedSet = make(map[string]bool, len(fields))
				for _, sha := range fields {
					unprovedSet[sha] = true
				}
			}
		}
		if l.cfg.Store != "" {
			if s, err := store.OpenRead(ctx, l.cfg.Store); err == nil {
				_ = s.ReadTx(ctx, func(r *store.Reader) error {
					rows, err := readItems(r)
					if err != nil {
						return err
					}
					for _, row := range rows {
						if row.State != StateInQueue && row.State != StateBlocked {
							continue
						}
						if IsTrunkFixer(row.ItemID) || IsRemoteSyncItem(row.ItemID) {
							continue
						}
						base, err := blockBaseAt(ctx, r, l.cfg.Root, green, tip, row.ItemID)
						if err == nil && base != "" && base != green {
							if blockBases == nil {
								blockBases = map[string]string{}
							}
							blockBases[row.ItemID] = base
						}
					}
					return nil
				})
				s.Close()
			}
		}
	}
	return l.actors.SetCachedTrunk(bgactors.TrunkCache{
		Tip:         tip,
		Green:       green,
		UnprovedSet: unprovedSet,
		BlockBases:  blockBases,
		Valid:       true,
	}), nil
}
