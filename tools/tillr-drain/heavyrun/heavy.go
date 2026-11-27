package heavyrun

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr-drain/heavy"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/store"
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
	// Lock is the loop lock of Root's repository. A proof moves the shared green pointer,
	// so Run refuses without it, and refuses from a linked worktree whatever it holds.
	Lock  *LoopLock
	Judge func(context.Context, string, string) ([]HeavyCheck, error)
	Fix   func(context.Context, string, []HeavyCheck) error
	// Revert is the automatic remedy for a red the bisection attributed to one landing
	// (graph design Section 8.2, OQ-GP1). It is called before Fix for each such landing,
	// with a replay that judges one check on any commit, and answers whether the landing
	// was reverted. A landing it reverted gets no fixer; one it could not revert gets the
	// fixer, with the reason on its checks. Nil files fixers only.
	Revert func(ctx context.Context, tip, landing string, checks []HeavyCheck, replay HeavyReplay) (RevertOutcome, error)
	Report func(HeavyRun)
	// Progress receives one line as each step of a run ends: each check's verdict on the
	// tip, the start of a culprit search, and each replay it makes with its outcome or the
	// reason it gave no verdict. Report fires once, when the whole run ends; Progress is
	// what an operator reads while a long search is still going.
	Progress func(string)
	// Now observes the run; injected judges can use this same clock for check timings.
	Now func() time.Time
	// BeforeTip takes ownership at the next tip boundary for a pending build replacement.
	BeforeTip func(context.Context) error
	ReuseRed  bool
	// WithWorktreeRegistryLock coordinates git worktree registry mutations with concurrent landers.
	WithWorktreeRegistryLock func(ctx context.Context, repo string, fn func() error) error
	// UnlockRegisteredWorktree unlocks a stale locked worktree registration before removal.
	UnlockRegisteredWorktree func(ctx context.Context, repo, common, work string) (string, time.Time, bool, error)
	// OnUnrecorded is called when a trunk commit in base..tip has no landing record.
	OnUnrecorded func(ctx context.Context, sha string)
	// DeferSettlement leaves refs/tillr/green update and heavy.json publication to the
	// caller (used by --tillr-heavy-proof so Loop owns authoritative settlement).
	DeferSettlement bool
}

// HeavyReplay judges one check on one commit, in the runner's own checkout and under
// its lock: red is true for a failing verdict, and anything that is not a verdict is
// an error.
type HeavyReplay func(ctx context.Context, sha, checkID string) (red bool, err error)

// RevertOutcome is what the automatic revert did with one attributed landing.
type RevertOutcome struct {
	// Reverted is the trunk commit the revert landed as; "" when it did not land.
	Reverted string
	// Dropped is why the revert was dropped without landing because the current trunk
	// tip already passes the failing checks; no fixer is needed.
	Dropped string
	// Refused is why the revert path ended "not possible", in words a fixer's brief can
	// carry. It is "" when Reverted or Dropped is set.
	Refused string
	// StillRed is set when a failing check still fails on the tip with the landing
	// reverted: the attribution did not explain the red and must be searched again.
	StillRed bool
	// StillRedCheck, when one of several attributed checks refutes the revert, names
	// that check so only its attribution is disproved.
	StillRedCheck string
}

func withRegistryLockOrDefault(ctx context.Context, repo string, withLock func(context.Context, string, func() error) error, fn func() error) error {
	if withLock != nil {
		return withLock(ctx, repo, fn)
	}
	common, err := heavy.GitOutput(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	common = strings.TrimSpace(common)
	unlockLand, err := acquireDefaultRegistryLock(ctx, filepath.Join(common, "land.lock"))
	if err != nil {
		return err
	}
	defer unlockLand()
	unlockRegistry, err := acquireDefaultRegistryLock(ctx, filepath.Join(common, "worktree-registry.lock"))
	if err != nil {
		return err
	}
	defer unlockRegistry()
	return fn()
}

func withWorktreeListLock(ctx context.Context, repo string, fn func() error) error {
	common, err := heavy.GitOutput(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return err
	}
	unlock, err := acquireDefaultRegistryLock(ctx, filepath.Join(strings.TrimSpace(common), "worktree-registry.lock"))
	if err != nil {
		return err
	}
	defer unlock()
	return fn()
}

func acquireDefaultRegistryLock(ctx context.Context, path string) (func(), error) {
	lock, err := land.OpenLock(path)
	if err != nil {
		return nil, Externalf("opening worktree registry coordination: %v", err)
	}
	if _, err := lock.Acquire(ctx, 0); err != nil {
		return nil, Externalf("waiting for worktree registry coordination: %v", err)
	}
	return lock.Release, nil
}

func HeavyDirectory(ctx context.Context, root string) (string, error) {
	dir, err := heavy.Directory(ctx, root)
	if err == nil && CommonGuard != nil {
		err = CommonGuard(filepath.Dir(dir))
	}
	return dir, err
}
func PublishHeavy(dir string, run HeavyRun) error {
	return PublishHeavyJSON(dir, "heavy.json", run)
}

func startRunningHeavy(dir, base, tip string, started time.Time) (func(), error) {
	if err := PublishHeavyJSON(dir, heavy.RunningFile, heavy.HeavyRunning{BaseSHA: base, TipSHA: tip, StartedAt: started.UTC()}); err != nil {
		return nil, err
	}
	lock, err := os.OpenFile(filepath.Join(dir, heavy.RunningLockFile), os.O_CREATE|os.O_RDWR, 0644)
	if err != nil {
		_ = os.Remove(filepath.Join(dir, heavy.RunningFile))
		return nil, err
	}
	if err := syscall.Flock(int(lock.Fd()), syscall.LOCK_EX); err != nil {
		lock.Close()
		_ = os.Remove(filepath.Join(dir, heavy.RunningFile))
		return nil, err
	}
	clearPriorUnknownHeavy(dir)
	return sync.OnceFunc(func() {
		_ = syscall.Flock(int(lock.Fd()), syscall.LOCK_UN)
		_ = lock.Close()
		_ = os.Remove(filepath.Join(dir, heavy.RunningFile))
	}), nil
}

func clearPriorUnknownHeavy(dir string) {
	path := filepath.Join(dir, "heavy.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return
	}
	var run HeavyRun
	if json.Unmarshal(raw, &run) != nil {
		return
	}
	if run.Result == "unknown" {
		_ = os.Remove(path)
	}
}
func (r HeavyRunner) Run(ctx context.Context) (HeavyOutcome, error) {
	out, err := r.RunAttempt(ctx, "")
	return out.HeavyOutcome, err
}

type HeavyAttempt struct {
	HeavyOutcome
	// PendingTip names the proof that has not produced a verdict yet. The
	// trigger supplies a fallback for errors before the runner can read trunk.
	PendingTip string
}

func (r HeavyRunner) RunAttempt(ctx context.Context, requested string) (out HeavyAttempt, err error) {
	out.PendingTip = requested
	if r.Judge == nil || r.Work == "" {
		return out, Usagef("heavy runner needs a judge and a worktree path")
	}
	if err = r.authorize(ctx); err != nil {
		return out, err
	}
	if r.Trunk == "" {
		r.Trunk = DefaultTrunkRef
	}
	if r.Now == nil {
		r.Now = time.Now
	}
	dir, err := HeavyDirectory(ctx, r.Root)
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
	if r.Progress != nil {
		ctx = context.WithValue(ctx, HeavyProgressReportKey{}, r.Progress)
	}
	ctx = context.WithValue(ctx, HeavyOwnershipKey{}, HeavyOwnership{Lock: lock, Owner: owner, Guard: filepath.Join(dir, HeavyCleanupGuard)})
	var activeStop func()
	defer func() {
		if activeStop != nil {
			activeStop()
		}
	}()
	for {
		if err = ctx.Err(); err != nil {
			return out, err
		}
		if r.BeforeTip != nil {
			if err = r.BeforeTip(ctx); err != nil {
				return out, err
			}
		}
		tip, e := heavy.GitOutput(ctx, r.Root, "rev-parse", r.Trunk+"^{commit}")
		if e != nil {
			return out, e
		}
		out.PendingTip = tip
		base, e := heavy.Green(ctx, r.Root)
		if e != nil {
			return out, e
		}
		run := HeavyRun{BaseSHA: base, TipSHA: tip, Result: "unknown", Checks: []HeavyCheck{}}
		unconfirmed, e := RecoverHeavyCleanupGuardsWithLock(ctx, r.Root, dir, r.WithWorktreeRegistryLock)
		if e != nil {
			return out, e
		}
		if unconfirmed != "" {
			run.Detail = "prior cleanup is unconfirmed; checkout retained; " + unconfirmed
			if pe := PublishHeavy(dir, run); pe != nil {
				return out, pe
			}
			out.Runs = append(out.Runs, run)
			return out, &heavyCleanupUnconfirmed{detail: run.Detail}
		}
		if tip == base {
			return out, nil
		}
		if base != "" {
			if _, e = heavy.GitOutput(ctx, r.Root, "merge-base", "--is-ancestor", base, tip); e != nil {
				run.Detail = fmt.Sprintf("%s %s is not an ancestor of tip %s; trunk rewritten", GreenRef, base, tip)
				if pe := PublishHeavy(dir, run); pe != nil {
					return out, pe
				}
				return out, Externalf("%s", run.Detail)
			}
		}
		if r.ReuseRed {
			if prior, ok := CompletedHeavyRed(dir, base, tip); ok {
				// The previous image already published the verdict and filed its remedy.
				r.progressf("loop: heavy reused the completed red proof at %s\n", ShortSHA(tip))
				out.Runs = append(out.Runs, prior)
				out.PendingTip = ""
				switch err := r.requireCurrentTip(ctx, tip); {
				case errors.Is(err, errHeavyTipMoved):
					continue
				default:
					return out, err
				}
			}
		}
		progress, e := LoadHeavyProgress(ctx, r.Root, dir, tip)
		if e != nil {
			return out, e
		}
		if err = ctx.Err(); err != nil {
			return out, err
		}
		started := r.Now()
		stopRunning, e := startRunningHeavy(dir, base, tip, started)
		if e != nil {
			return out, e
		}
		activeStop = stopRunning
		attemptCtx, storeProgress := r.StartStoreProgress(ctx, tip)
		run, e = r.judgeTip(context.WithValue(attemptCtx, HeavyProgressKey{}, progress), run)
		run.ReusedChecks = progress.reused
		if ctx.Err() != nil {
			stopRunning()
			storeProgress.Finish("cancelled")
			return out, ctx.Err()
		}
		if e == nil && run.Result == "green" {
			unrecorded, unrecErr := r.firstUnrecordedCommit(ctx, base, tip)
			if unrecErr != nil {
				e = Externalf("checking landing records for %s..%s: %v", ShortSHA(base), ShortSHA(tip), unrecErr)
				run.Result = "unknown"
			} else if unrecorded != "" {
				e = Externalf("trunk commit %s was not made by a landing; %s stays", unrecorded, GreenRef)
				run.Result = "unknown"
				if r.OnUnrecorded != nil {
					r.OnUnrecorded(ctx, unrecorded)
				}
			}
		}
		if e == nil && run.Result == "green" {
			current, curErr := heavy.GitOutput(ctx, r.Root, "rev-parse", r.Trunk+"^{commit}")
			if curErr != nil {
				e = Externalf("reading %s before advancing %s: %v", r.Trunk, GreenRef, curErr)
				run.Result = "unknown"
			} else if onTrunk, ancErr := fixer.TrunkAncestor(ctx, r.Root, tip, current); ancErr != nil {
				e = Externalf("checking whether proved tip %s is on %s %s: %v", tip, r.Trunk, current, ancErr)
				run.Result = "unknown"
			} else if !onTrunk {
				e = Externalf("proved tip %s left the trunk (trunk is now %s); %s stays", tip, current, GreenRef)
				run.Result = "unknown"
			} else if !r.DeferSettlement {
				old := base
				if old == "" {
					old = strings.Repeat("0", len(tip))
				}
				// Publish evidence by commit before moving the pointer. Cancellation or
				// a refused compare-and-swap cannot replace the old pointer's time.
				passed := []string{}
				for _, check := range run.Checks {
					if ClassifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.HeavyCheckWord, Word: check.Result}).Outcome == checkresult.Pass {
						passed = append(passed, check.CheckID)
					}
				}
				e = PublishHeavyJSON(dir, "green-"+tip+".json", heavy.GreenProof{Commit: tip, ProvedAt: r.Now().UTC(), Passed: passed})
				if e == nil {
					_, e = heavy.GitOutput(ctx, r.Root, "update-ref", GreenRef, tip, old)
				}
				if e != nil {
					e = Externalf("green compare-and-swap refused: %v", e)
					run.Result = "unknown"
				}
			}
		}
		run.DurationMS = r.Now().Sub(started).Milliseconds()
		if e != nil {
			run.Detail = e.Error()
		}
		if !r.DeferSettlement {
			if pe := PublishHeavy(dir, run); pe != nil {
				stopRunning()
				endedOutcome := heavyEndedOutcome(ctx, run.Result, pe)
				storeProgress.Finish(endedOutcome, heavyEndedFailingChecks(endedOutcome, run.Checks)...)
				return out, pe
			}
		}
		stopRunning()
		out.Runs = append(out.Runs, run)
		if e == nil && run.Result != "unknown" {
			out.PendingTip = ""
		}
		if r.Report != nil {
			r.Report(run)
		}
		endedOutcome := heavyEndedOutcome(ctx, run.Result, e)
		storeProgress.Finish(endedOutcome, heavyEndedFailingChecks(endedOutcome, run.Checks)...)
		if e != nil {
			return out, e
		}
		if r.DeferSettlement {
			return out, nil
		}
		newest, e := heavy.GitOutput(ctx, r.Root, "rev-parse", r.Trunk+"^{commit}")
		if e != nil {
			return out, e
		}
		if newest == tip {
			return out, nil
		}
		// Landings while the lock is held owe one run, on the newest tip, not a run per landing.
		out.PendingTip = newest
	}
}

func heavyEndedFailingChecks(endedOutcome string, checks []HeavyCheck) []string {
	if endedOutcome != "red" {
		return nil
	}
	seen := make(map[string]struct{}, len(checks))
	var failing []string
	for _, check := range checks {
		if check.CheckID == "" {
			continue
		}
		if ClassifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.HeavyCheckWord, Word: check.Result}).Outcome != checkresult.Fail {
			continue
		}
		if _, ok := seen[check.CheckID]; ok {
			continue
		}
		seen[check.CheckID] = struct{}{}
		failing = append(failing, check.CheckID)
	}
	return failing
}

type proofWorktreeEntry struct {
	path     string
	prunable bool
	locked   bool
}

func sameWorktreePath(a, b string) bool {
	abs := func(p string) string {
		if resolved, err := filepath.Abs(p); err == nil {
			return resolved
		}
		return filepath.Clean(p)
	}
	if abs(a) == abs(b) {
		return true
	}
	ra, errA := filepath.EvalSymlinks(abs(a))
	rb, errB := filepath.EvalSymlinks(abs(b))
	return errA == nil && errB == nil && ra == rb
}

func findProofWorktree(ctx context.Context, repo, work string) (proofWorktreeEntry, bool, error) {
	var out string
	err := withWorktreeListLock(ctx, repo, func() error {
		var err error
		out, err = heavy.GitOutput(ctx, repo, "worktree", "list", "--porcelain")
		return err
	})
	if err != nil {
		return proofWorktreeEntry{}, false, err
	}
	var current *proofWorktreeEntry
	match := func(wt *proofWorktreeEntry) bool {
		return wt != nil && sameWorktreePath(wt.path, work)
	}
	for _, line := range strings.Split(out, "\n") {
		if line == "" {
			if match(current) {
				return *current, true, nil
			}
			current = nil
			continue
		}
		key, rest, _ := strings.Cut(line, " ")
		switch key {
		case "worktree":
			if match(current) {
				return *current, true, nil
			}
			current = &proofWorktreeEntry{path: rest}
		case "prunable":
			if current != nil {
				current.prunable = true
			}
		case "locked":
			if current != nil {
				current.locked = true
			}
		}
	}
	if match(current) {
		return *current, true, nil
	}
	return proofWorktreeEntry{}, false, nil
}

func worktreeInProgress(gitDir string) bool {
	for _, name := range []string{"MERGE_HEAD", "CHERRY_PICK_HEAD", "REVERT_HEAD", "BISECT_LOG", "rebase-merge", "rebase-apply"} {
		if _, err := os.Stat(filepath.Join(gitDir, name)); err == nil {
			return true
		}
	}
	return false
}

// tryReuseProofWorktree moves an already-registered, non-prunable, unlocked
// proof worktree to a detached checkout of tip in place, removes all untracked
// and ignored files, and verifies HEAD and cleanliness before returning true.
// Any failure returns false so the caller falls through to the remove-and-add
// recovery path.
func tryReuseProofWorktree(ctx context.Context, work, tip string) bool {
	info, err := os.Stat(filepath.Join(work, ".git"))
	if err != nil || info.IsDir() {
		return false
	}
	gitDir, err := heavy.GitOutput(ctx, work, "rev-parse", "--path-format=absolute", "--git-dir")
	if err != nil {
		return false
	}
	if worktreeInProgress(gitDir) {
		for _, sub := range [][]string{{"merge", "--abort"}, {"rebase", "--abort"}, {"cherry-pick", "--abort"}, {"revert", "--abort"}} {
			_, _ = heavy.GitOutput(ctx, work, sub...)
		}
	}
	wantTip, err := heavy.GitOutput(ctx, work, "rev-parse", tip+"^{commit}")
	if err != nil {
		return false
	}
	if _, err := heavy.GitOutput(ctx, work, "clean", "-ffdx"); err != nil {
		return false
	}
	if _, err := heavy.GitOutput(ctx, work, "checkout", "--force", "--detach", wantTip); err != nil {
		return false
	}
	if _, err := heavy.GitOutput(ctx, work, "reset", "--hard", wantTip); err != nil {
		return false
	}
	if _, err := heavy.GitOutput(ctx, work, "clean", "-ffdx"); err != nil {
		return false
	}
	head, err := heavy.GitOutput(ctx, work, "rev-parse", "HEAD")
	if err != nil || head != wantTip {
		return false
	}
	branch, err := heavy.GitOutput(ctx, work, "branch", "--show-current")
	if err != nil || branch != "" {
		return false
	}
	status, err := heavy.GitOutput(ctx, work, "--no-optional-locks", "status", "--porcelain", "--untracked-files=all", "--ignored")
	if err != nil || status != "" {
		return false
	}
	return true
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
	reused := false
	if entry, found, err := findProofWorktree(ctx, r.Root, work); err == nil && found && !entry.prunable && !entry.locked {
		reused = tryReuseProofWorktree(ctx, r.Work, tip)
	}
	if !reused {
		if err := ctx.Err(); err != nil {
			return err
		}
		// The shared lock proves no other heavy run owns this path. Create the
		// worktree for the first time, or recover a missing, prunable, locked,
		// or broken registration without pruning any other worktree registrations.
		if err := withRegistryLockOrDefault(ctx, r.Root, r.WithWorktreeRegistryLock, func() error {
			if err := r.unlockProofWorktree(ctx, work); err != nil {
				return err
			}
			_, _ = heavy.GitOutput(ctx, r.Root, "worktree", "remove", "--force", r.Work)
			if err := os.RemoveAll(r.Work); err != nil {
				return err
			}
			_, err := heavy.GitOutput(ctx, r.Root, "worktree", "add", "--detach", r.Work, tip)
			return err
		}); err != nil {
			return err
		}
	}
	return fn(r.Work)
}

// unlockProofWorktree runs under the registry lock. Git can still have a
// registration when the checkout's .git file is gone, so find it by the
// administrative directory's backpointer rather than through the checkout.
func (r HeavyRunner) unlockProofWorktree(ctx context.Context, work string) error {
	unlock := r.UnlockRegisteredWorktree
	if unlock == nil {
		unlock = unlockRegisteredWorktreeDefault
	}
	common := ""
	if r.Lock != nil {
		common = r.Lock.common
	}
	reason, lockedAt, unlocked, err := unlock(ctx, r.Root, common, work)
	if err != nil || !unlocked {
		return err
	}
	r.progressf("loop: heavy unlocked proof worktree %q (reason %q, lock mtime %s)\n",
		work, reason, lockedAt.UTC().Format(time.RFC3339Nano))
	return nil
}

func unlockRegisteredWorktreeDefault(ctx context.Context, repo, common, work string) (string, time.Time, bool, error) {
	if common == "" {
		resolved, err := heavy.GitOutput(ctx, repo, "rev-parse", "--path-format=absolute", "--git-common-dir")
		if err != nil {
			return "", time.Time{}, false, err
		}
		common = strings.TrimSpace(resolved)
	}
	absWork, err := filepath.Abs(work)
	if err != nil {
		return "", time.Time{}, false, err
	}
	registry := filepath.Join(common, "worktrees")
	entries, err := os.ReadDir(registry)
	if os.IsNotExist(err) {
		return "", time.Time{}, false, nil
	}
	if err != nil {
		return "", time.Time{}, false, err
	}
	target := filepath.Join(absWork, ".git")
	canonical := target
	if parent, err := filepath.EvalSymlinks(filepath.Dir(absWork)); err == nil {
		canonical = filepath.Join(parent, filepath.Base(absWork), ".git")
	} else if !os.IsNotExist(err) {
		return "", time.Time{}, false, err
	}
	for _, entry := range entries {
		if !entry.IsDir() {
			continue
		}
		admin := filepath.Join(registry, entry.Name())
		lock := filepath.Join(admin, "locked")
		info, statErr := os.Stat(lock)
		if os.IsNotExist(statErr) {
			continue
		}
		backpointer, readErr := os.ReadFile(filepath.Join(admin, "gitdir"))
		if os.IsNotExist(readErr) {
			continue
		}
		if readErr != nil {
			return "", time.Time{}, false, readErr
		}
		gitfile := strings.TrimSuffix(string(backpointer), "\n")
		if !filepath.IsAbs(gitfile) {
			gitfile = filepath.Join(admin, gitfile)
		}
		path := filepath.Clean(gitfile)
		if path != canonical && path != target {
			continue
		}
		if statErr != nil {
			return "", time.Time{}, false, statErr
		}
		reason, err := os.ReadFile(lock)
		if err != nil {
			return "", time.Time{}, false, err
		}
		if _, err := heavy.GitOutput(ctx, repo, "worktree", "unlock", filepath.Dir(path)); err != nil {
			return "", time.Time{}, false, err
		}
		return strings.TrimSpace(string(reason)), info.ModTime(), true, nil
	}
	return "", time.Time{}, false, nil
}

func (r HeavyRunner) judgeTip(ctx context.Context, run HeavyRun) (HeavyRun, error) {
	early := r.StartEarly(ctx, run)
	err := r.inTree(ctx, run.TipSHA, func(dir string) error {
		storeProgress, _ := ctx.Value(heavyStoreProgressKey{}).(*heavyStoreProgress)
		wholeBefore := storeProgress.WholeStepCount()
		checks, err := r.Judge(context.WithValue(ctx, HeavyCheckEndedKey{}, early.Offer), dir, "")
		if err != nil {
			return err
		}
		if len(checks) == 0 {
			// An empty declaration proves nothing: calling it green would move the pointer over
			// every landing it never judged.
			return Externalf("heavy proof judged no checks: the gate program is empty, so there is nothing to prove; %s stays", GreenRef)
		}
		if storeProgress.WholeStepCount() == wholeBefore {
			var reused []string
			if p, _ := ctx.Value(HeavyProgressKey{}).(*HeavyProgress); p != nil {
				reused = p.reused
			}
			for _, check := range checks {
				if !slices.Contains(reused, check.CheckID) {
					storeProgress.step(ctx, check.CheckID, check.Result, true)
				}
			}
		}
		run.Checks = checks
		run.Result = checkresult.Aggregate(checkresult.HeavyOrder, nil).Detail
		classifiedChecks := make([]checkresult.Result, 0, len(run.Checks))
		for i := range run.Checks {
			check := &run.Checks[i]
			if check.BudgetMS > 0 && check.DurationMS > check.BudgetMS {
				check.OverBudgetMS = check.DurationMS - check.BudgetMS
			}
			classified := ClassifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.HeavyCheckWord, Word: check.Result})
			if classified.Outcome == checkresult.Unknown && classified.Detail == checkresult.DetailStalled {
				check.Stalls = 1
				retry, err := r.Judge(ctx, dir, check.CheckID)
				if err != nil {
					return err
				}
				if len(retry) != 1 || retry[0].CheckID != check.CheckID {
					return Externalf("stall retry for %s gave no verdict", check.CheckID)
				}
				HeavyStoreStepProgress(ctx, retry[0].CheckID, retry[0].Result)
				retryClassified := ClassifyCheck(checkresult.Observation{Check: retry[0].CheckID, Source: checkresult.HeavyCheckWord, Word: retry[0].Result})
				if retryClassified.Outcome == checkresult.Unknown && retryClassified.Detail == checkresult.DetailStalled {
					check.Stalls = 2
					check.Result = "fail"
					check.Detail = "second stall on the same tip"
					classified = ClassifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.HeavyCheckWord, Word: check.Result})
				} else {
					// A green retry does not erase the first stall, but it is not a failing verdict.
					original := *check
					*check = retry[0]
					check.Stalls = 1
					check.Detail = "first stall; retry: " + check.Result
					check.DurationMS += original.DurationMS
					classified = retryClassified
				}
			}
			// A completed retry is proof too. Persist it before another retry
			// can be interrupted, just like the initial individual checks.
			if err := SaveHeavyCheck(ctx, *check); err != nil {
				return err
			}
			if check.BudgetMS > 0 && check.DurationMS > check.BudgetMS {
				check.OverBudgetMS = check.DurationMS - check.BudgetMS
			}
			classifiedChecks = append(classifiedChecks, classified)
			run.Result = checkresult.Aggregate(checkresult.HeavyOrder, classifiedChecks).Detail
		}
		return nil
	})
	// Recovery owns a second checkout: it ends before the run does, whatever the proof answered.
	if earlyErr := early.Wait(); err == nil {
		err = earlyErr
	}
	// A pass breaks this check's red episode even when another check is still red.
	if observeErr := r.ForgetGreenAttributions(ctx, run.Checks); observeErr != nil {
		return run, observeErr
	}
	if err != nil {
		return run, err
	}
	byLanding := map[string][]HeavyCheck{}
	order := []string{}
	// Only checks with a single attributed, recorded culprit can authorize a
	// revert. Even checks sharing a landing must keep that decision separate.
	bisected := map[string]bool{}
	for i := range run.Checks {
		check := &run.Checks[i]
		if ClassifyCheck(checkresult.Observation{Check: check.CheckID, Source: checkresult.HeavyCheckWord, Word: check.Result}).Outcome != checkresult.Fail {
			continue
		}
		against := ""
		if a, ok := early.Handled[check.CheckID]; ok && check.FirstRedLanding == "" {
			// Recovery already ran for this check while the proof continued.
			check.FirstRedLanding, check.GateFault, check.Detail = a.check.FirstRedLanding, a.check.GateFault, a.check.Detail
			if check.LogPath == "" && a.check.LogPath != "" {
				check.LogPath = a.check.LogPath
			}
			if check.LogRef == "" && a.check.LogRef != "" {
				check.LogRef = a.check.LogRef
			}
			check.RevertedBy, check.RevertRefused = a.check.RevertedBy, a.check.RevertRefused
			against = a.against
			switch {
			case check.RevertedBy != "":
				continue
			case a.dropped:
				continue
			case !a.offered && a.bisected:
				bisected[check.CheckID] = true
			}
		} else {
			var found bool
			var err error
			against, found, err = r.attribute(ctx, run, check)
			if err != nil {
				return run, err
			}
			if found {
				bisected[check.CheckID] = true
			}
		}
		if len(byLanding[against]) == 0 {
			order = append(order, against)
		}
		byLanding[against] = append(byLanding[against], *check)
	}
	order, err = r.revert(ctx, &run, order, byLanding, bisected, early.reverted)
	if err != nil {
		return run, err
	}
	for _, check := range run.Checks {
		if check.RevertedBy != "" {
			if recErr := fixer.RecordTrunkRepair(ctx, r.Root, check.RevertedBy, "reverted "+ShortSHA(check.FirstRedLanding)); recErr != nil {
				r.progressf("loop: heavy %s trunk repair at %s not recorded: %v\n", check.CheckID, ShortSHA(check.RevertedBy), recErr)
			}
		}
	}
	if r.Fix != nil {
		order, err = r.RetainRedRepairs(ctx, &run, order, byLanding)
		if err != nil {
			return run, err
		}
		order = coalesceChecksByFailure(order, byLanding)
		for _, sha := range order {
			if err := r.Fix(ctx, sha, byLanding[sha]); err != nil {
				return run, err
			}
		}
	}
	for i := range run.Checks {
		check := &run.Checks[i]
		if check.RevertedBy == "" && check.RevertRefused != "" && check.FirstRedLanding != "" {
			check.Detail = JoinDetail(check.Detail, "automatic revert not possible: "+HeavyBrief(check.RevertRefused))
		}
	}
	return run, nil
}

func (r HeavyRunner) attribute(ctx context.Context, run HeavyRun, check *HeavyCheck) (against string, found bool, err error) {
	// The sha a fixer is filed against: the first red landing when attribution
	// names one, otherwise the tip. A red with no named landing still gets a mover
	// (graph design, Section 8.2: the mechanic for an unattributed red); it is
	// never left holding landings while nobody acts.
	if check.Stalls >= 2 || check.GoTimedOut {
		check.FirstRedLanding = ""
		return run.TipSHA, false, nil // Keep the known failure without replaying it to name a culprit.
	}
	against = check.FirstRedLanding
	if check.FirstRedLanding != "" {
		reason := "first red landing was already set before attribution"
		r.progressf("loop: heavy %s red from %s: %s; not reverting automatically, filing a fixer\n", check.CheckID, ShortSHA(check.FirstRedLanding), reason)
		check.Detail = JoinDetail(check.Detail, "not reverted automatically: "+reason)
	} else {
		red, fault, reason, err := r.FindFirstRed(ctx, run, *check)
		switch {
		case err != nil && ctx.Err() == nil && !CleanupLost(err):
			r.progressf("loop: heavy %s culprit search gave no verdict, because %v; the fixer is filed against the tip\n", check.CheckID, err)
		case err == nil && fault:
			r.progressf("loop: heavy %s culprit search names no commit (%s); the fixer is filed against the tip\n", check.CheckID, reason)
		case err == nil:
			r.progressf("loop: heavy %s first red commit %s\n", check.CheckID, ShortSHA(red))
		}
		if err != nil {
			// A stop or a lost cleanup is the run's own fault, not attribution's.
			if ctx.Err() != nil || CleanupLost(err) {
				return "", false, err
			}
			check.Detail = JoinDetail(check.Detail, "attribution failed, so no landing is named and the fixer is filed against the tip: "+err.Error())
		}
		check.FirstRedLanding = red
		check.GateFault = fault
		if fault {
			if run.BaseSHA == "" {
				check.Detail = "no proved baseline; attribution unavailable"
			} else {
				check.Detail = "cannot establish a single red commit: " + reason + "; attribution unavailable"
			}
			check.Detail += "; needs=" + strings.Join(check.Needs, ",") + " tools=" + strings.Join(check.Tools, ",")
			check.Detail += "; fixer filed against the tip"
		}
		against = red
		if against == "" {
			against = run.TipSHA
		} else if !fault {
			if strings.HasPrefix(check.CheckID, GoTestPrefix) {
				others, err := r.additionalGoCulprits(ctx, run, *check, red)
				if ctx.Err() != nil {
					return "", false, ctx.Err()
				}
				if CleanupLost(err) {
					return "", false, err
				}
				var reasons []string
				if len(others) > 0 {
					culprits := append([]string{red}, others...)
					reasons = append(reasons, "multiple first-red commits for this test: "+strings.Join(culprits, ", "))
				}
				if err != nil {
					reasons = append(reasons, "additional attribution gave no verdict: "+HeavyBrief(err.Error()))
				}
				if len(reasons) > 0 {
					reason := strings.Join(reasons, "; ")
					r.progressf("loop: heavy %s red from %s: %s; not reverting a single landing, filing a fixer against the tip\n", check.CheckID, ShortSHA(red), reason)
					check.Detail = JoinDetail(check.Detail, reason+"; not reverting a single landing; fixer filed against the tip")
					return run.TipSHA, false, nil
				}
			}
			recorded, err := r.HasLandingRecord(ctx, red)
			if err != nil {
				return "", false, err
			}
			if recorded {
				found = true
			} else if m, rebased, err := RebasedLanding(ctx, r.Root, r.Store, red, run.TipSHA); err != nil && ctx.Err() != nil {
				return "", false, err
			} else if rebased {
				note := RebaseNote(red, m)
				r.progressf("loop: heavy %s first red commit %s: %s; filing a fixer against it\n", check.CheckID, ShortSHA(red), note)
				check.Detail = JoinDetail(check.Detail, note)
			} else {
				r.progressf("loop: heavy %s first red commit %s has no landing record; not reverting automatically, filing a fixer against it\n", check.CheckID, ShortSHA(red))
				check.Detail = JoinDetail(check.Detail, "commit "+ShortSHA(red)+" has no landing record; not reverted automatically")
			}
		}
	}
	if check.FirstRedLanding == "" {
		check.Detail += "; attribution unavailable; red batch range " + run.BaseSHA + ".." + run.TipSHA
	}
	return against, found, nil
}

// revert offers each bisected landing to Revert before any fixer is filed, and answers
// the landings that still need one. A reverted landing leaves the list and its checks
// name the revert. A refused revert keeps its fixer, and the reason rides on its checks
// into the fixer's brief. A failed inverse triggers fresh attribution before a
// fixer is filed.
func (r HeavyRunner) revert(ctx context.Context, run *HeavyRun, order []string, byLanding map[string][]HeavyCheck, bisected map[string]bool, reverted map[string]string) ([]string, error) {
	if r.Revert == nil {
		for _, sha := range order {
			for _, check := range byLanding[sha] {
				if !bisected[check.CheckID] || check.FirstRedLanding == "" {
					continue
				}
				reason := "automatic revert is disabled"
				r.progressf("loop: heavy %s red from %s: %s; filing a fixer\n", check.CheckID, ShortSHA(check.FirstRedLanding), reason)
				for i := range run.Checks {
					if run.Checks[i].CheckID == check.CheckID {
						run.Checks[i].Detail = JoinDetail(run.Checks[i].Detail, "not reverted automatically: "+reason)
					}
				}
			}
		}
		return order, nil
	}
	remaining := []string{}
	type movedRepair struct {
		sha   string
		check HeavyCheck
	}
	var moved []movedRepair
	for _, sha := range order {
		var eligible, keep []HeavyCheck
		for _, check := range byLanding[sha] {
			if bisected[check.CheckID] {
				eligible = append(eligible, check)
			} else {
				keep = append(keep, check)
			}
		}
		if len(eligible) > 0 {
			out := RevertOutcome{Reverted: reverted[sha]}
			if out.Reverted == "" {
				var err error
				if out, err = r.offerRevert(ctx, run.TipSHA, sha, eligible); err != nil {
					return nil, err
				}
			}
			for _, check := range eligible {
				against, err := r.resolveRevert(ctx, *run, &check, sha, out)
				if err != nil {
					return nil, err
				}
				for i := range run.Checks {
					if run.Checks[i].CheckID == check.CheckID {
						run.Checks[i] = check
					}
				}
				switch {
				case check.RevertedBy != "":
				case out.Dropped != "":
				case against != sha:
					moved = append(moved, movedRepair{against, check})
				default:
					keep = append(keep, check)
				}
			}
		}
		byLanding[sha] = keep
		if len(keep) > 0 {
			remaining = append(remaining, sha)
		}
	}
	for _, repair := range moved {
		if len(byLanding[repair.sha]) == 0 {
			remaining = append(remaining, repair.sha)
		}
		byLanding[repair.sha] = append(byLanding[repair.sha], repair.check)
	}
	return remaining, nil
}

func (r HeavyRunner) noteRevertRefused(check *HeavyCheck) {
	if check.RevertedBy != "" || check.RevertRefused == "" || check.FirstRedLanding == "" {
		return
	}
	reason := HeavyBrief(check.RevertRefused)
	r.progressf("loop: heavy %s red from %s: automatic revert not possible: %s; filing a fixer\n", check.CheckID, ShortSHA(check.FirstRedLanding), reason)
}

// A failed inverse refutes the landing as the sole explanation for this red.
// Discard the remembered result and search with fresh focused verdicts. Only a
// different attribution can authorize another revert; otherwise repair the tip.
func (r HeavyRunner) resolveRevert(ctx context.Context, run HeavyRun, check *HeavyCheck, landing string, out RevertOutcome) (string, error) {
	check.RevertedBy, check.RevertRefused = out.Reverted, out.Refused
	if out.Dropped != "" {
		check.Detail = JoinDetail(check.Detail, "automatic revert dropped: "+HeavyBrief(out.Dropped))
		return landing, nil
	}
	if !out.StillRed || out.StillRedCheck != "" && out.StillRedCheck != check.CheckID {
		r.noteRevertRefused(check)
		return landing, nil
	}
	discard := func(sha, reason string) error {
		check.FirstRedLanding, check.RevertRefused = "", ""
		check.Detail = JoinDetail(check.Detail, "Automatic revert of "+ShortSHA(sha)+" for "+check.CheckID+" was not possible: "+reason)
		return r.forgetHeavyAttribution(ctx, check.CheckID)
	}
	if err := discard(landing, out.Refused); err != nil {
		return "", err
	}
	r.progressf("loop: heavy %s discarded attribution to %s after a red revert; searching again\n", check.CheckID, ShortSHA(landing))
	// Keep original failure evidence separate from the attribution notes. Do
	// not memoize the retry's attribution until a later proof sees it again.
	retry := *check
	retry.Detail = ""
	fresh := context.WithValue(ctx, freshHeavyAttributionKey{}, true)
	against, found, err := r.attribute(fresh, run, &retry)
	if err != nil {
		return "", err
	}
	check.FirstRedLanding, check.GateFault = retry.FirstRedLanding, retry.GateFault
	check.Detail = JoinDetail(check.Detail, retry.Detail)
	if against == landing || check.FirstRedLanding == landing {
		check.FirstRedLanding = ""
		return run.TipSHA, nil
	}
	if found {
		revised, err := r.offerRevert(ctx, run.TipSHA, against, []HeavyCheck{*check})
		if err != nil {
			return "", err
		}
		if revised.StillRed {
			return run.TipSHA, discard(against, revised.Refused)
		}
		check.RevertedBy, check.RevertRefused = revised.Reverted, revised.Refused
		r.noteRevertRefused(check)
	}
	return against, nil
}

// offerRevert asks Revert about one landing. A revert that failed for its own reasons is
// a refusal; only a stop or a lost cleanup is an error.
func (r HeavyRunner) offerRevert(ctx context.Context, tip, sha string, checks []HeavyCheck) (RevertOutcome, error) {
	if prior, err := RevertedBy(ctx, r.Store, sha); err != nil {
		return RevertOutcome{}, err
	} else if prior != "" {
		return RevertOutcome{Reverted: prior}, nil
	}
	if err := r.requireCurrentTip(ctx, tip); err != nil {
		if errors.Is(err, errHeavyTipMoved) {
			return RevertOutcome{Refused: "trunk advanced beyond the proved commit"}, nil
		}
		return RevertOutcome{}, err
	}
	out, err := r.Revert(ctx, tip, sha, checks, r.one)
	if err != nil {
		if ctx.Err() != nil || CleanupLost(err) {
			return out, err
		}
		out = RevertOutcome{Refused: "the revert could not be attempted: " + err.Error()}
	}
	if out.Reverted == "" && out.Dropped == "" && out.Refused == "" {
		out.Refused = "the revert answered neither a landing nor a reason"
	}
	return out, nil
}
func (r HeavyRunner) one(ctx context.Context, sha, id string) (bool, error) {
	red, _, err := r.OneLogged(ctx, sha, id)
	return red, err
}

// OneLogged is one that also answers the file holding the replayed check's output when
// it was red and the judge kept one.
func (r HeavyRunner) OneLogged(ctx context.Context, sha, id string, removed ...string) (bool, string, error) {
	ctx = context.WithValue(ctx, HeavyProgressKey{}, (*HeavyProgress)(nil))
	var red bool
	var logPath string
	err := r.inTree(ctx, sha, func(dir string) error {
		for i := len(removed) - 1; i >= 0; i-- {
			commit := removed[i]
			parents, err := heavy.GitOutput(ctx, dir, "rev-list", "--parents", "-n", "1", commit)
			if err != nil {
				return err
			}
			args := []string{"revert", "--no-commit"}
			if len(strings.Fields(parents)) > 2 {
				args = append(args, "-m", "1")
			}
			if _, err := heavy.GitOutput(ctx, dir, append(args, commit)...); err != nil {
				return err
			}
		}
		if len(removed) > 0 {
			ctx = context.WithValue(ctx, HeavyInverseKey{}, true)
		}
		checks, err := r.Judge(ctx, dir, id)
		if err != nil {
			return err
		}
		if len(checks) != 1 || checks[0].CheckID != id {
			return Externalf("attribution for %s at %s gave no verdict", id, sha)
		}
		classified := ClassifyCheck(checkresult.Observation{Check: checks[0].CheckID, Source: checkresult.HeavyCheckWord, Word: checks[0].Result})
		switch classified.Outcome {
		case checkresult.Pass:
		case checkresult.Fail:
			red = true
			logPath = checks[0].LogPath
			if logPath == "" {
				logPath = checks[0].LogRef
			}
		default:
			// The judge's own words are the reason; without them the fault is unreadable.
			if detail := strings.TrimSpace(checks[0].Detail); detail != "" {
				return Externalf("attribution for %s at %s is %s, not a verdict: %s", id, sha, checks[0].Result, detail)
			}
			return Externalf("attribution for %s at %s is %s, not a verdict", id, sha, checks[0].Result)
		}
		return nil
	})
	if err == nil {
		outcome := "pass"
		if red {
			outcome = "fail"
		}
		HeavyStoreStepProgress(ctx, id, outcome)
	} else if ctx.Err() == nil && !CleanupLost(err) {
		HeavyStoreStepProgress(ctx, id, "error")
	}
	return red, logPath, err
}
func (r HeavyRunner) progressf(format string, args ...any) {
	if r.Progress != nil {
		r.Progress(fmt.Sprintf(format, args...))
	}
}

func heavyFirstParentCommits(ctx context.Context, root, base, tip string) ([]string, error) {
	raw, err := heavy.GitOutput(ctx, root, "rev-list", "--first-parent", "--reverse", base+".."+tip)
	if err != nil {
		return nil, err
	}
	return strings.Fields(raw), nil
}
func heavyCommitChangedPaths(ctx context.Context, root, sha string) ([]string, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "diff-tree", "--no-commit-id", "--name-only", "--no-renames", "-r", "-z", "--first-parent", sha)
	raw, err := cmd.Output()
	if err != nil {
		return nil, Externalf("reading changed paths for %s: %v", sha, err)
	}
	if len(raw) == 0 {
		return nil, nil
	}
	return strings.Split(strings.TrimSuffix(string(raw), "\x00"), "\x00"), nil
}
func commitTouchesReads(ctx context.Context, root, sha string, reads []string) (bool, error) {
	paths, err := heavyCommitChangedPaths(ctx, root, sha)
	if err != nil {
		return false, err
	}
	if len(paths) == 0 {
		return false, nil
	}
	effective := reads
	if len(effective) == 0 {
		effective = []string{"."}
	}
	for _, p := range paths {
		if fixer.ReadsCoverPath(effective, p) {
			return true, nil
		}
	}
	return false, nil
}
func (r HeavyRunner) firstUnrecordedCommit(ctx context.Context, base, tip string) (string, error) {
	if r.Store == "" || base == "" || tip == "" || base == tip {
		return "", nil
	}
	raw, err := heavy.GitOutput(ctx, r.Root, "rev-list", "--first-parent", "--reverse", base+".."+tip)
	if err != nil {
		return "", err
	}
	for _, sha := range strings.Fields(raw) {
		has, err := r.HasLandingRecord(ctx, sha)
		if err != nil {
			return "", err
		}
		if !has {
			return sha, nil
		}
	}
	return "", nil
}

func (r HeavyRunner) HasLandingRecord(ctx context.Context, sha string) (bool, error) {
	if r.Store == "" {
		return true, nil
	}
	s, err := store.OpenRead(ctx, r.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()
	var found bool
	err = s.ReadTx(ctx, func(reader *store.Reader) error {
		var one int
		shas, err := fixer.LandingShas(reader, sha)
		if err != nil {
			return err
		}
		in, args := heavy.InList(shas)
		err = reader.QueryRow(`SELECT 1 FROM landings WHERE trunk_after IN `+in+` AND result = 'landed' LIMIT 1`, args...).Scan(&one)
		if errors.Is(err, sql.ErrNoRows) || err != nil && strings.Contains(err.Error(), "no such table") {
			return nil
		}
		found = err == nil
		return err
	})
	return found, err
}

// replay is one attribution replay with its outcome on the progress log: what was
// replayed where and why, how long it took, and its verdict or why it gave none.
func (r HeavyRunner) Replay(ctx context.Context, sha, id, why string) (bool, error) {
	// A commit's tree never changes, so a verdict already reached for (sha, id) holds across
	// a restart that cut the culprit search short. The heavy lock owns the file.
	fresh, _ := ctx.Value(freshHeavyAttributionKey{}).(bool)
	if dir, derr := HeavyDirectory(ctx, r.Root); !fresh && derr == nil {
		if saved, ok := ReadHeavyReplay(dir, sha, id); ok {
			r.progressf("loop: heavy %s reused replay at %s (%s): %s\n", id, ShortSHA(sha), why, map[bool]string{true: "red", false: "green"}[saved])
			return saved, nil
		}
	}
	now := r.Now
	if now == nil {
		now = time.Now
	}
	started := now()
	r.progressf("loop: heavy %s replaying at %s (%s)\n", id, ShortSHA(sha), why)
	red, logPath, err := r.OneLogged(ctx, sha, id)
	if err == nil {
		if dir, derr := HeavyDirectory(ctx, r.Root); derr == nil {
			if serr := SaveHeavyReplay(dir, sha, id, red); serr != nil {
				r.progressf("loop: heavy %s replay at %s not saved: %v\n", id, ShortSHA(sha), serr)
			}
		}
	}
	took := now().Sub(started).Round(time.Second)
	switch {
	case err != nil && ctx.Err() != nil:
	case err != nil:
		r.progressf("loop: heavy %s at %s: no verdict after %s, because %v\n", id, ShortSHA(sha), took, err)
	case red:
		switch {
		case logPath != "":
			r.progressf("loop: heavy %s at %s: red after %s; output: %s\n", id, ShortSHA(sha), took, logPath)
		default:
			r.progressf("loop: heavy %s at %s: red after %s\n", id, ShortSHA(sha), took)
		}
	default:
		r.progressf("loop: heavy %s at %s: green after %s\n", id, ShortSHA(sha), took)
	}
	return red, err
}

func (r HeavyRunner) firstRed(ctx context.Context, base, tip, id string, reads []string) (string, bool, string, error) {
	return r.SearchFirstRed(ctx, base, tip, reads, func(sha, why string) (bool, error) {
		return r.Replay(ctx, sha, id, why)
	})
}

func (r HeavyRunner) SearchFirstRed(ctx context.Context, base, tip string, reads []string, judge func(sha, why string) (bool, error)) (string, bool, string, error) {
	verdicts := map[string]bool{}
	replay := func(sha, why string) (bool, error) {
		if red, ok := verdicts[sha]; ok {
			return red, nil
		}
		red, err := judge(sha, why)
		if err == nil {
			verdicts[sha] = red
		}
		return red, err
	}
	if base == "" {
		return "", true, "no proved baseline", nil
	} // no proved baseline: never accuse a commit without one
	allCommits, err := heavyFirstParentCommits(ctx, r.Root, base, tip)
	if err != nil {
		return "", false, "", err
	}
	commits := make([]string, 0, len(allCommits))
	for _, c := range allCommits {
		touches, err := commitTouchesReads(ctx, r.Root, c, reads)
		if err != nil {
			return "", false, "", err
		}
		if touches {
			commits = append(commits, c)
		}
	}
	if len(commits) == 0 {
		return "", true, fmt.Sprintf("no commit in %s..%s touches the check's declared reads", ShortSHA(base), ShortSHA(tip)), nil
	}
	red, err := replay(base, "the green base")
	if err != nil {
		return "", false, "", err
	}
	if red {
		return "", true, "red at the green base", nil
	}
	// The search needs a red upper bound. A later green commit means this
	// range cannot explain the tip, even if an earlier commit was red.
	red, err = replay(commits[len(commits)-1], fmt.Sprintf("the last of %d candidate commits since green", len(commits)))
	if err != nil {
		return "", false, "", err
	}
	if !red {
		return "", true, "the last candidate commit touching the check's reads is green", nil
	}
	// The filtered bracket is only a shortcut: a green candidate is not
	// necessarily the next candidate's parent. Never blame the latter until
	// the real parent has a passing verdict.
	for {
		candidate, err := bisectHeavyRed(commits, replay)
		if err != nil {
			return "", false, "", err
		}
		parent, err := heavy.GitOutput(ctx, r.Root, "rev-parse", candidate+"^1")
		if err != nil {
			return "", false, "", err
		}
		red, err := replay(parent, "the first parent of candidate "+ShortSHA(candidate))
		if err != nil {
			return "", false, "", err
		}
		if !red {
			return candidate, false, "", nil
		}
		// The watches missed a red parent. Search the full first-parent prefix
		// ending at that parent, whose red verdict is already established.
		i := slices.Index(allCommits, parent)
		if i < 0 {
			return "", true, "red parent lies outside the first-parent range", nil
		}
		commits = allCommits[:i+1]
	}
}

// bisectHeavyRed takes a contiguous (or watch-filtered) sequence with an
// established green predecessor and red last commit.
func bisectHeavyRed(commits []string, replay func(sha, why string) (bool, error)) (string, error) {
	low, high := 0, len(commits)-1
	for low < high {
		mid := (low + high) / 2
		red, err := replay(commits[mid], fmt.Sprintf("commit %d of %d, between %d and %d", mid+1, len(commits), low+1, high+1))
		if err != nil {
			return "", err
		}
		if red {
			high = mid
		} else {
			low = mid + 1
		}
	}
	return commits[low], nil
}

func ReadHeavyStatus(ctx context.Context, root, storePath string) (HeavyStatus, error) {
	return heavy.ReadStatus(ctx, root, storePath)
}

func ReadHeavyStatusForBranch(ctx context.Context, root, storePath, trunkBranch string) (HeavyStatus, error) {
	return heavy.ReadStatusForBranch(ctx, root, storePath, trunkBranch)
}

// additionalGoCulprits searches for a later first-red while removing the earlier
// culprits, newest inverse first. Inverses live only in the detached proof tree;
// these outcomes must never enter the ordinary, commit-keyed replay cache.
func (r HeavyRunner) additionalGoCulprits(ctx context.Context, run HeavyRun, check HeavyCheck, first string) ([]string, error) {
	if first == run.TipSHA {
		return nil, nil // The ordinary revert replay will check this inverse.
	}
	var other []string
	removed := []string{first}
	for {
		replay := func(sha, why string) (bool, error) {
			r.progressf("loop: heavy %s replaying at %s without %s (%s)\n", check.CheckID, ShortSHA(sha), strings.Join(removed, ", "), why)
			red, _, err := r.OneLogged(ctx, sha, check.CheckID, removed...)
			return red, err
		}
		red, err := replay(run.TipSHA, "checking whether the known culprits explain the tip")
		if err != nil || !red {
			return other, err
		}
		base := removed[len(removed)-1]
		if base == run.TipSHA {
			return other, fmt.Errorf("test still red after removing %s", strings.Join(removed, ", "))
		}
		next, fault, reason, err := r.SearchFirstRed(ctx, base, run.TipSHA, check.Reads, replay)
		if err != nil {
			return other, err
		}
		if fault {
			return other, fmt.Errorf("cannot explain residual red after removing %s: %s", strings.Join(removed, ", "), reason)
		}
		other = append(other, next)
		removed = append(removed, next)
	}
}
