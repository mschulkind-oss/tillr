package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
)

const GreenRef = heavy.GreenRef

type HeavyCheck = heavy.HeavyCheck
type HeavyRun = heavy.HeavyRun
type HeavyOutcome = heavy.HeavyOutcome
type HeavyStatus = heavy.HeavyStatus

// HeavyRunner holds the lock through the run and attribution. Judge runs in a detached
// worktree; an empty only asks for the entire heavy tier, otherwise exactly that check.
// Landings is read from the store when named, never inferred from the rolling watermark.
type HeavyRunner struct {
	Root, Work, Trunk, Store string
	Judge                    func(context.Context, string, string) ([]HeavyCheck, error)
	Fix                      func(context.Context, string, []HeavyCheck) error
	Report                   func(HeavyRun)
	// Now observes the run; injected judges can use this same clock for check timings.
	Now func() time.Time
}

func heavyGitOutput(ctx context.Context, root string, args ...string) (string, error) {
	return heavy.GitOutput(ctx, root, args...)
}
func heavyDirectory(ctx context.Context, root string) (string, error) {
	return heavy.Directory(ctx, root)
}
func heavyGreen(ctx context.Context, root string) (string, error) { return heavy.Green(ctx, root) }
func publishHeavy(dir string, run HeavyRun) error {
	return publishHeavyJSON(dir, "heavy.json", run)
}
func (r HeavyRunner) Run(ctx context.Context) (out HeavyOutcome, err error) {
	if r.Judge == nil || r.Work == "" {
		return out, Usagef("heavy runner needs a judge and a worktree path")
	}
	if r.Trunk == "" {
		r.Trunk = TrunkRef
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	dir, err := heavyDirectory(ctx, r.Root)
	if err != nil {
		return out, err
	}
	if err = os.MkdirAll(dir, 0755); err != nil {
		return out, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, "heavy.lock"), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		return out, err
	}
	defer lock.Close()
	if err = syscall.Flock(int(lock.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		if err == syscall.EWOULDBLOCK || err == syscall.EAGAIN {
			out.Busy = true
			return out, nil
		}
		return out, err
	}
	// Closing the last inherited descriptor releases ownership. Explicit LOCK_UN
	// would also release it under a surviving supervisor or declaration child.
	owner, life, err := os.Pipe()
	if err != nil {
		return out, err
	}
	defer owner.Close()
	defer life.Close()
	ctx = context.WithValue(ctx, heavyOwnershipKey{}, heavyOwnership{Lock: lock, Owner: owner, Guard: filepath.Join(dir, heavyCleanupGuard)})
	for {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		tip, e := heavyGitOutput(ctx, r.Root, "rev-parse", r.Trunk+"^{commit}")
		if e != nil {
			return out, e
		}
		base, e := heavyGreen(ctx, r.Root)
		if e != nil {
			return out, e
		}
		run := HeavyRun{BaseSHA: base, TipSHA: tip, Result: "unknown", Checks: []HeavyCheck{}}
		guard := filepath.Join(dir, heavyCleanupGuard)
		if _, guardErr := os.Stat(guard); guardErr == nil || !os.IsNotExist(guardErr) {
			run.Detail = "prior cleanup is unconfirmed; checkout retained; independent process recovery must inspect and clear " + guard
			if pe := publishHeavy(dir, run); pe != nil {
				return out, pe
			}
			out.Runs = append(out.Runs, run)
			return out, &heavyCleanupUnconfirmed{detail: run.Detail}
		}
		if tip == base {
			return out, nil
		}
		if base != "" {
			if _, e = heavyGitOutput(ctx, r.Root, "merge-base", "--is-ancestor", base, tip); e != nil {
				run.Detail = fmt.Sprintf("%s %s is not an ancestor of tip %s; trunk rewritten", GreenRef, base, tip)
				if pe := publishHeavy(dir, run); pe != nil {
					return out, pe
				}
				return out, Externalf("%s", run.Detail)
			}
		}
		progress, e := loadHeavyProgress(dir, tip)
		if e != nil {
			return out, e
		}
		started := r.Now()
		run, e = r.judgeTip(context.WithValue(ctx, heavyProgressKey{}, progress), run)
		run.ReusedChecks = progress.reused
		if e == nil && run.Result == "green" {
			old := base
			if old == "" {
				old = strings.Repeat("0", len(tip))
			}
			// Publish evidence by commit before moving the pointer. Cancellation or
			// a refused compare-and-swap cannot replace the old pointer's time.
			passed := []string{}
			for _, check := range run.Checks {
				if check.Result == "pass" {
					passed = append(passed, check.CheckID)
				}
			}
			e = publishHeavyJSON(dir, "green-"+tip+".json", heavy.GreenProof{Commit: tip, ProvedAt: r.Now().UTC(), Passed: passed})
			if e == nil {
				_, e = heavyGitOutput(ctx, r.Root, "update-ref", GreenRef, tip, old)
			}
			if e != nil {
				e = Externalf("green compare-and-swap refused: %v", e)
				run.Result = "unknown"
			}
		}
		run.DurationMS = r.Now().Sub(started).Milliseconds()
		if e != nil {
			run.Detail = e.Error()
		}
		if pe := publishHeavy(dir, run); pe != nil {
			return out, pe
		}
		out.Runs = append(out.Runs, run)
		if r.Report != nil {
			r.Report(run)
		}
		if e != nil {
			return out, e
		}
		newest, e := heavyGitOutput(ctx, r.Root, "rev-parse", r.Trunk+"^{commit}")
		if e != nil {
			return out, e
		}
		if newest == tip {
			return out, nil
		}
		// Landings while the lock is held owe one run, on the newest tip, not a run per landing.
	}
}
func (r HeavyRunner) inTree(ctx context.Context, tip string, fn func(string) error) error {
	if err := os.MkdirAll(filepath.Dir(r.Work), 0755); err != nil {
		return err
	}
	root, err := filepath.Abs(r.Root)
	if err != nil {
		return err
	}
	work, err := filepath.Abs(r.Work)
	if err != nil {
		return err
	}
	if root == work {
		return Usagef("heavy worktree must not be the main checkout")
	}
	// The shared lock proves no other heavy run owns this path. Recover a tree
	// left by a killed runner without pruning any other worktree registrations.
	if err := withWorktreeRegistryLock(ctx, r.Root, func() error {
		_, _ = heavyGitOutput(ctx, r.Root, "worktree", "remove", "--force", r.Work)
		if err := os.RemoveAll(r.Work); err != nil {
			return err
		}
		_, err := heavyGitOutput(ctx, r.Root, "worktree", "add", "--detach", r.Work, tip)
		return err
	}); err != nil {
		return err
	}
	err = fn(r.Work)
	if cleanupLost(err) {
		return err
	}
	// Cancellation must not prevent cleanup after the judge's process group has ended.
	cleanupErr := withWorktreeRegistryLock(context.Background(), r.Root, func() error {
		_, err := heavyGitOutput(context.Background(), r.Root, "worktree", "remove", "--force", r.Work)
		return err
	})
	if err != nil {
		return err
	}
	return cleanupErr
}
func (r HeavyRunner) judgeTip(ctx context.Context, run HeavyRun) (HeavyRun, error) {
	err := r.inTree(ctx, run.TipSHA, func(dir string) error {
		checks, err := r.Judge(ctx, dir, "")
		if err != nil {
			return err
		}
		run.Checks = checks
		run.Result = "green"
		for i := range run.Checks {
			check := &run.Checks[i]
			if check.BudgetMS > 0 && check.DurationMS > check.BudgetMS {
				check.OverBudgetMS = check.DurationMS - check.BudgetMS
			}
			if check.Result == "stalled" {
				check.Stalls = 1
				retry, err := r.Judge(ctx, dir, check.CheckID)
				if err != nil {
					return err
				}
				if len(retry) != 1 || retry[0].CheckID != check.CheckID {
					return Externalf("stall retry for %s gave no verdict", check.CheckID)
				}
				if retry[0].Result == "stalled" {
					check.Stalls = 2
					check.Result = "fail"
					check.FirstRedLanding = run.TipSHA
					check.Detail = "second stall on the same tip"
				} else {
					// A green retry does not erase the first stall, but it is not a failing verdict.
					original := *check
					*check = retry[0]
					check.Stalls = 1
					check.Detail = "first stall; retry: " + check.Result
					check.DurationMS += original.DurationMS
				}
			}
			// A completed retry is proof too. Persist it before another retry
			// can be interrupted, just like the initial individual checks.
			if err := saveHeavyCheck(ctx, *check); err != nil {
				return err
			}
			if check.BudgetMS > 0 && check.DurationMS > check.BudgetMS {
				check.OverBudgetMS = check.DurationMS - check.BudgetMS
			}
			switch check.Result {
			case "fail":
				if run.Result != "unknown" {
					run.Result = "red"
				}
			case "pass", "unavailable":
			default:
				run.Result = "unknown"
			}
		}
		return nil
	})
	if err != nil {
		return run, err
	}
	byLanding := map[string][]HeavyCheck{}
	order := []string{}
	for i := range run.Checks {
		check := &run.Checks[i]
		if check.Result != "fail" {
			continue
		}
		// The sha a fixer is filed against: the first red landing when attribution
		// names one, otherwise the tip. A red with no named landing still gets a mover
		// (graph design, Section 8.2: the mechanic for an unattributed red); it is
		// never left holding landings while nobody acts.
		against := check.FirstRedLanding
		if check.FirstRedLanding == "" {
			red, fault, err := r.firstRed(ctx, run.BaseSHA, run.TipSHA, check.CheckID)
			if err != nil {
				// A stop or a lost cleanup is the run's own fault, not attribution's.
				if ctx.Err() != nil || cleanupLost(err) {
					return run, err
				}
				check.Detail = joinDetail(check.Detail, "attribution failed, so no landing is named and the fixer is filed against the tip: "+err.Error())
			}
			check.FirstRedLanding = red
			check.GateFault = fault
			if fault {
				if run.BaseSHA == "" {
					check.Detail = "no proved baseline; attribution unavailable"
				} else {
					check.Detail = "cannot establish a single red landing: baseline or recorded landing range does not explain the red tip"
				}
				check.Detail += "; needs=" + strings.Join(check.Needs, ",") + " tools=" + strings.Join(check.Tools, ",")
				check.Detail += "; fixer filed against the tip"
			}
			against = red
			if against == "" {
				against = run.TipSHA
			}
		}
		if check.FirstRedLanding == "" {
			check.Detail += "; attribution unavailable; red batch range " + run.BaseSHA + ".." + run.TipSHA
		}
		if len(byLanding[against]) == 0 {
			order = append(order, against)
		}
		byLanding[against] = append(byLanding[against], *check)
	}
	if r.Fix != nil {
		for _, sha := range order {
			if err := r.Fix(ctx, sha, byLanding[sha]); err != nil {
				return run, err
			}
		}
	}
	return run, nil
}
func (r HeavyRunner) one(ctx context.Context, sha, id string) (bool, error) {
	ctx = context.WithValue(ctx, heavyProgressKey{}, (*heavyProgress)(nil))
	var red bool
	err := r.inTree(ctx, sha, func(dir string) error {
		checks, err := r.Judge(ctx, dir, id)
		if err != nil {
			return err
		}
		if len(checks) != 1 || checks[0].CheckID != id {
			return Externalf("attribution for %s at %s gave no verdict", id, sha)
		}
		switch checks[0].Result {
		case "pass":
		case "fail":
			red = true
		default:
			// The judge's own words are the reason; without them the fault is unreadable.
			if detail := strings.TrimSpace(checks[0].Detail); detail != "" {
				return Externalf("attribution for %s at %s is %s, not a verdict: %s", id, sha, checks[0].Result, detail)
			}
			return Externalf("attribution for %s at %s is %s, not a verdict", id, sha, checks[0].Result)
		}
		return nil
	})
	return red, err
}
func joinDetail(old, add string) string {
	if old = strings.TrimSpace(old); old == "" {
		return add
	}
	return old + "; " + add
}
func heavyLandings(ctx context.Context, root, storePath, base, tip string) ([]string, error) {
	return heavy.Landings(ctx, root, storePath, base, tip)
}
func (r HeavyRunner) firstRed(ctx context.Context, base, tip, id string) (string, bool, error) {
	if base == "" {
		return "", true, nil
	} // no proved baseline: never accuse a landing without one
	red, err := r.one(ctx, base, id)
	if err != nil {
		return "", false, err
	}
	if red {
		return "", true, nil
	}
	commits, err := heavyLandings(ctx, r.Root, r.Store, base, tip)
	if err != nil {
		return "", false, err
	}
	if len(commits) == 0 {
		return "", true, nil
	}
	// The search needs a red upper bound. A later green landing means this
	// range cannot explain the tip, even if an earlier landing was red.
	red, err = r.one(ctx, commits[len(commits)-1], id)
	if err != nil {
		return "", false, err
	}
	if !red {
		return "", true, nil
	}
	low, high := 0, len(commits)-1
	for low < high {
		mid := (low + high) / 2
		red, err := r.one(ctx, commits[mid], id)
		if err != nil {
			return "", false, err
		}
		if red {
			high = mid
		} else {
			low = mid + 1
		}
	}
	return commits[low], false, nil
}

func ReadHeavyStatus(ctx context.Context, root, storePath string) (HeavyStatus, error) {
	return heavy.ReadStatus(ctx, root, storePath)
}
