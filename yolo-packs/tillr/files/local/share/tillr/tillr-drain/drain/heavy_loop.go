package drain

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr/checkresult"
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
		for {
			l.heavyMu.Lock()
			requested := l.heavyWanted
			l.heavyMu.Unlock()
			runner := HeavyRunner{Root: l.cfg.Root, Store: l.cfg.Store, Work: filepath.Join(l.cfg.LaneRoot, ".rolling", "worktree"), Trunk: TrunkRef, Judge: func(jctx context.Context, dir, only string) ([]HeavyCheck, error) {
				checks, err := step.JudgeHeavy(jctx, dir, only)
				l.sweepDisabledMarkers(jctx)
				return checks, err
			}, Report: func(run HeavyRun) {
				l.reportf("loop: heavy run %s at %s; green base %s\n", run.Result, shortSHA(run.TipSHA), shortSHA(run.BaseSHA))
				if run.Result == "green" {
					l.offerGreenAdvance(heavyCtx, run.TipSHA)
				}
				if len(run.ReusedChecks) > 0 {
					l.reportf("loop: heavy %s reused checks=%s\n", shortSHA(run.TipSHA), strings.Join(run.ReusedChecks, ","))
				}
				landings, countErr := heavy.Landings(heavyCtx, l.cfg.Root, l.cfg.Store, run.BaseSHA, run.TipSHA)
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
			}, Fix: func(ctx context.Context, sha string, checks []HeavyCheck) error {
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
					// An unattributed red's brief says why no landing is named, in the
					// runner's own words, with whatever output the judge recorded.
					if check.FirstRedLanding == "" && strings.TrimSpace(check.Detail) != "" {
						notes = append(notes, "No landing is named for "+check.CheckID+" ("+heavyBrief(check.Detail)+"); start from the tip and run the check there")
					} else if strings.HasPrefix(check.CheckID, goTestPrefix) && check.Detail != "" {
						notes = append(notes, check.CheckID+": "+check.Detail)
					}
					// The failed check's full output is on disk; the brief names the file and
					// quotes its end, so the fixer does not rediscover an intermittent failure.
					if check.LogPath != "" {
						notes = append(notes, "Full output of "+check.CheckID+" is in "+check.LogPath+"; its failing lines and end: "+heavyBrief(legacyLogTail(check.LogPath)))
					}
					// A fixer filed after the automatic revert ended "not possible" says why,
					// so its worker does not try the same revert again.
					if check.RevertRefused != "" {
						notes = append(notes, "Automatic revert of "+shortSHA(check.FirstRedLanding)+" for "+check.CheckID+" was not possible: "+heavyBrief(check.RevertRefused))
					}
				}
				// Attribution names the culprit, but repair permissions cover every
				// commit since the last proved green through the current tip.
				green, err := heavyGreen(ctx, l.cfg.Root)
				if err != nil {
					return err
				}
				tip, err := heavyGitOutput(ctx, l.cfg.Root, "rev-parse", TrunkRef)
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
			runner.BeforeReplay = func(bctx context.Context) error {
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
					l.heavyMu.Lock()
					l.heavyProofActive = false
					l.heavyMu.Unlock()
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
	if _, waitCh := l.restartWaitState(); waitCh != nil && !l.restartActionDeferred() {
		return false
	}
	l.heavyMu.Lock()
	l.heavyProofActive = true
	l.heavyMu.Unlock()
	return true
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
