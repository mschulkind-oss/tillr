package drain

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"
)

// startHeavy is a trigger, not a gate on dispatch or a synchronous landing step.
// The shared flock excludes replacement loops; this mutex coalesces local triggers.
func (l *Loop) startHeavy(ctx context.Context, tip string) error {
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
	l.heavyWG.Add(1)
	go func() {
		defer l.heavyWG.Done()
		for {
			l.heavyMu.Lock()
			requested := l.heavyWanted
			l.heavyMu.Unlock()
			runner := HeavyRunner{Root: l.cfg.Root, Store: l.cfg.Store, Work: filepath.Join(l.cfg.LaneRoot, ".rolling", "worktree"), Trunk: TrunkRef, Judge: step.JudgeHeavy, Report: func(run HeavyRun) {
				l.reportf("loop: heavy run %s at %s; green base %s\n", run.Result, shortSHA(run.TipSHA), shortSHA(run.BaseSHA))
				for _, check := range run.Checks {
					if check.OverBudgetMS > 0 {
						l.reportf("loop: heavy %s over budget: measured %d ms, declared %d ms; keeps %s verdict\n", check.CheckID, check.DurationMS, check.BudgetMS, check.Result)
					}
					if check.Result == "fail" {
						l.reportf("loop: heavy %s first red landing %s; %s\n", check.CheckID, shortSHA(check.FirstRedLanding), check.Detail)
					}
					if check.Result == "unavailable" {
						l.reportf("loop: heavy %s unavailable; %s\n", check.CheckID, check.Detail)
					}
				}
			}, Fix: func(ctx context.Context, sha string, checks []HeavyCheck) error {
				ids := []string{}
				watches := map[string][]string{}
				for _, check := range checks {
					ids = append(ids, check.CheckID)
					watches[check.CheckID] = check.Reads
				}
				if l.gatePolicy.AutoRevert {
					return Externalf("heavy %s is red at %s; automatic revert is not implemented; pointer held", strings.Join(ids, ","), sha)
				}
				paths := l.rangePaths(ctx, sha+"^", sha)
				_, err := l.fileTrunkFixer(ctx, sha, ids, GateOutcome{SuspectPaths: paths, CheckWatches: watches})
				return err
			}}
			out, err := runner.Run(ctx)
			if err != nil && ctx.Err() == nil {
				l.reportf("loop: heavy runner fault: %v\n", err)
			}
			l.heavyMu.Lock()
			attempted := l.heavyWanted
			if len(out.Runs) > 0 {
				attempted = out.Runs[len(out.Runs)-1].TipSHA
			}
			// Losing the lock is not a judgment and must not consume this tip's trigger.
			if !out.Busy {
				l.heavyAttempted = attempted
			}
			// A trigger arriving after the runner's final tip read still owes work. Do not
			// drop it in the narrow interval between that read and releasing local ownership.
			if ctx.Err() == nil && !out.Busy && err == nil && l.heavyWanted != requested && l.heavyWanted != attempted {
				l.heavyMu.Unlock()
				continue
			}
			l.heavyRunning = false
			l.heavyMu.Unlock()
			return
		}
	}()
	return nil
}
func (l *Loop) heavyInFlight() bool {
	l.heavyMu.Lock()
	defer l.heavyMu.Unlock()
	return l.heavyRunning
}
func heavyStatusLine(status HeavyStatus) string {
	return fmt.Sprintf("green=%s landings_since=%d heavy=%s unavailable=%s", shortSHA(status.GreenCommit), status.LandingsSince, status.Result, strings.Join(heavyIDs(status.UnavailableChecks), ","))
}
func heavyIDs(checks []HeavyCheck) []string {
	ids := []string{}
	for _, check := range checks {
		ids = append(ids, check.CheckID)
	}
	return ids
}
