package drain

import (
	"context"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/heavy"
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
				notes := []string{}
				for _, check := range checks {
					ids = append(ids, check.CheckID)
					watches[check.CheckID] = check.Reads
					// An unattributed red's brief says why no landing is named, in the
					// runner's own words, with whatever output the judge recorded.
					if check.FirstRedLanding == "" && strings.TrimSpace(check.Detail) != "" {
						notes = append(notes, "No landing is named for "+check.CheckID+" ("+heavyBrief(check.Detail)+"); start from the tip and run the check there")
					}
				}
				if l.gatePolicy.AutoRevert {
					return Externalf("heavy %s is red at %s; automatic revert is not implemented; pointer held", strings.Join(ids, ","), sha)
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
				_, err = l.fileTrunkFixerNoted(ctx, sha, ids, GateOutcome{SuspectPaths: paths, CheckWatches: watches}, strings.Join(notes, ". "))
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

// heavyBrief keeps a recorded detail readable in one criteria line: whitespace folded,
// and a long output tail cut to its end, where a failure's reason usually is.
func heavyBrief(detail string) string {
	detail = strings.Join(strings.Fields(detail), " ")
	const keep = 1500
	if len(detail) > keep {
		detail = "..." + detail[len(detail)-keep:]
	}
	return detail
}
