package drain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavy"
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
}

// HeavyReplay judges one check on one commit, in the runner's own checkout and under
// its lock: red is true for a failing verdict, and anything that is not a verdict is
// an error.
type HeavyReplay func(ctx context.Context, sha, checkID string) (red bool, err error)

// RevertOutcome is what the automatic revert did with one attributed landing.
type RevertOutcome struct {
	// Reverted is the trunk commit the revert landed as; "" when it did not land.
	Reverted string
	// Refused is why the revert path ended "not possible", in words a fixer's brief can
	// carry. It is "" exactly when Reverted is set.
	Refused string
	// StillRed is set when a failing check still fails on the tip with the landing
	// reverted: the attribution did not explain the red, so the fixer starts from the tip.
	StillRed bool
}

func heavyGitOutput(ctx context.Context, root string, args ...string) (string, error) {
	return gather(ctx, gitKey("trimmed output", root, args), func(ctx context.Context) (string, error) { return heavy.GitOutput(ctx, root, args...) })
}
func heavyDirectory(ctx context.Context, root string) (string, error) {
	dir, err := heavy.Directory(ctx, root)
	if err == nil && heavyCommonGuard != nil {
		err = heavyCommonGuard(filepath.Dir(dir))
	}
	return dir, err
}
func heavyGreen(ctx context.Context, root string) (string, error) {
	return gather(ctx, "green pointer in "+root, func(ctx context.Context) (string, error) { return heavy.Green(ctx, root) })
}
func publishHeavy(dir string, run HeavyRun) error {
	return publishHeavyJSON(dir, "heavy.json", run)
}
func (r HeavyRunner) Run(ctx context.Context) (out HeavyOutcome, err error) {
	if r.Judge == nil || r.Work == "" {
		return out, Usagef("heavy runner needs a judge and a worktree path")
	}
	if err = r.authorize(ctx); err != nil {
		return out, err
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
	if r.Progress != nil {
		ctx = context.WithValue(ctx, heavyProgressReportKey{}, r.Progress)
	}
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
		unconfirmed, e := recoverHeavyCleanupGuards(ctx, r.Root, dir)
		if e != nil {
			return out, e
		}
		if unconfirmed != "" {
			run.Detail = "prior cleanup is unconfirmed; checkout retained; " + unconfirmed
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
		progress, e := loadHeavyProgress(ctx, r.Root, dir, tip)
		if e != nil {
			return out, e
		}
		started := r.Now()
		run, e = r.judgeTip(context.WithValue(ctx, heavyProgressKey{}, progress), run)
		run.ReusedChecks = progress.reused
		if ctx.Err() != nil {
			return out, ctx.Err()
		}
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
	early := r.startEarly(ctx, run)
	err := r.inTree(ctx, run.TipSHA, func(dir string) error {
		checks, err := r.Judge(context.WithValue(ctx, heavyCheckEndedKey{}, early.offer), dir, "")
		if err != nil {
			return err
		}
		if len(checks) == 0 {
			// An empty declaration proves nothing: calling it green would move the pointer over
			// every landing it never judged.
			return Externalf("heavy proof judged no checks: the gate program is empty, so there is nothing to prove; %s stays", GreenRef)
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
	// Recovery owns a second checkout: it ends before the run does, whatever the proof answered.
	if earlyErr := early.wait(); err == nil {
		err = earlyErr
	}
	if err != nil {
		return run, err
	}
	byLanding := map[string][]HeavyCheck{}
	order := []string{}
	// bisected names the landings the bisection itself found: only those may be
	// reverted. A judge's own FirstRedLanding (a second stall names the tip) is a place
	// to start a fixer, not an attribution.
	bisected := map[string]bool{}
	for i := range run.Checks {
		check := &run.Checks[i]
		if check.Result != "fail" {
			continue
		}
		against := ""
		if a, ok := early.handled[check.CheckID]; ok && check.FirstRedLanding == "" {
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
			case a.offered && a.stillRed && against != run.TipSHA:
				against = run.TipSHA
			case !a.offered && a.bisected:
				bisected[against] = true
			}
		} else {
			var found bool
			var err error
			against, found, err = r.attribute(ctx, run, check)
			if err != nil {
				return run, err
			}
			if found {
				bisected[against] = true
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
	if r.Fix != nil {
		for _, sha := range order {
			if err := r.Fix(ctx, sha, byLanding[sha]); err != nil {
				return run, err
			}
		}
	}
	return run, nil
}

func (r HeavyRunner) attribute(ctx context.Context, run HeavyRun, check *HeavyCheck) (against string, found bool, err error) {
	// The sha a fixer is filed against: the first red landing when attribution
	// names one, otherwise the tip. A red with no named landing still gets a mover
	// (graph design, Section 8.2: the mechanic for an unattributed red); it is
	// never left holding landings while nobody acts.
	against = check.FirstRedLanding
	if check.FirstRedLanding == "" {
		r.progressf("loop: heavy %s failed at tip %s; searching for the first red commit since green %s\n", check.CheckID, shortSHA(run.TipSHA), shortSHA(run.BaseSHA))
		red, fault, reason, err := r.firstRed(ctx, run.BaseSHA, run.TipSHA, check.CheckID, check.Reads)
		switch {
		case err != nil && ctx.Err() == nil && !cleanupLost(err):
			r.progressf("loop: heavy %s culprit search gave no verdict, because %v; the fixer is filed against the tip\n", check.CheckID, err)
		case err == nil && fault:
			r.progressf("loop: heavy %s culprit search names no commit (%s); the fixer is filed against the tip\n", check.CheckID, reason)
		case err == nil:
			r.progressf("loop: heavy %s first red commit %s\n", check.CheckID, shortSHA(red))
		}
		if err != nil {
			// A stop or a lost cleanup is the run's own fault, not attribution's.
			if ctx.Err() != nil || cleanupLost(err) {
				return "", false, err
			}
			check.Detail = joinDetail(check.Detail, "attribution failed, so no landing is named and the fixer is filed against the tip: "+err.Error())
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
			recorded, err := r.hasLandingRecord(ctx, red)
			if err != nil {
				return "", false, err
			}
			if recorded {
				found = true
			} else if m, rebased, err := rebasedLanding(ctx, r.Root, r.Store, red, run.TipSHA); err != nil && ctx.Err() != nil {
				return "", false, err
			} else if rebased {
				note := rebaseNote(red, m)
				r.progressf("loop: heavy %s first red commit %s: %s; filing a fixer against it\n", check.CheckID, shortSHA(red), note)
				check.Detail = joinDetail(check.Detail, note)
			} else {
				r.progressf("loop: heavy %s first red commit %s has no landing record; not reverting automatically, filing a fixer against it\n", check.CheckID, shortSHA(red))
				check.Detail = joinDetail(check.Detail, "commit "+shortSHA(red)+" has no landing record; not reverted automatically")
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
// into the fixer's brief; when the check stayed red with the landing reverted, its
// fixer moves to the tip, because the landing did not explain the red. A revert that
// failed for its own reasons is a refusal too: the red keeps a mover either way.
func (r HeavyRunner) revert(ctx context.Context, run *HeavyRun, order []string, byLanding map[string][]HeavyCheck, bisected map[string]bool, reverted map[string]string) ([]string, error) {
	if r.Revert == nil {
		return order, nil
	}
	remaining := []string{}
	moved := []HeavyCheck{}
	for _, sha := range order {
		if !bisected[sha] {
			remaining = append(remaining, sha)
			continue
		}
		out := RevertOutcome{Reverted: reverted[sha]}
		if out.Reverted == "" {
			var err error
			if out, err = r.offerRevert(ctx, run.TipSHA, sha, byLanding[sha]); err != nil {
				return nil, err
			}
		}
		for i := range byLanding[sha] {
			byLanding[sha][i].RevertedBy, byLanding[sha][i].RevertRefused = out.Reverted, out.Refused
		}
		for i := range run.Checks {
			if run.Checks[i].Result == "fail" && run.Checks[i].FirstRedLanding == sha {
				run.Checks[i].RevertedBy, run.Checks[i].RevertRefused = out.Reverted, out.Refused
			}
		}
		switch {
		case out.Reverted != "":
		case out.StillRed && sha != run.TipSHA:
			moved = append(moved, byLanding[sha]...)
		default:
			remaining = append(remaining, sha)
		}
	}
	if len(moved) > 0 {
		if len(byLanding[run.TipSHA]) == 0 {
			remaining = append(remaining, run.TipSHA)
		}
		byLanding[run.TipSHA] = append(byLanding[run.TipSHA], moved...)
	}
	return remaining, nil
}

// offerRevert asks Revert about one landing. A revert that failed for its own reasons is
// a refusal; only a stop or a lost cleanup is an error.
func (r HeavyRunner) offerRevert(ctx context.Context, tip, sha string, checks []HeavyCheck) (RevertOutcome, error) {
	out, err := r.Revert(ctx, tip, sha, checks, r.one)
	if err != nil {
		if ctx.Err() != nil || cleanupLost(err) {
			return out, err
		}
		out = RevertOutcome{Refused: "the revert could not be attempted: " + err.Error()}
	}
	if out.Reverted == "" && out.Refused == "" {
		out.Refused = "the revert answered neither a landing nor a reason"
	}
	return out, nil
}
func (r HeavyRunner) one(ctx context.Context, sha, id string) (bool, error) {
	red, _, err := r.oneLogged(ctx, sha, id)
	return red, err
}

// oneLogged is one that also answers the file holding the replayed check's output when
// it was red and the judge kept one.
func (r HeavyRunner) oneLogged(ctx context.Context, sha, id string) (bool, string, error) {
	ctx = context.WithValue(ctx, heavyProgressKey{}, (*heavyProgress)(nil))
	var red bool
	var logPath string
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
	return red, logPath, err
}
func (r HeavyRunner) progressf(format string, args ...any) {
	if r.Progress != nil {
		r.Progress(fmt.Sprintf(format, args...))
	}
}

// heavyProgressReportKey carries the runner's Progress into the judge, so a judge that
// runs checks one at a time reports each verdict as it ends.
type heavyProgressReportKey struct{}

func heavyProgressReport(ctx context.Context, format string, args ...any) {
	if report, ok := ctx.Value(heavyProgressReportKey{}).(func(string)); ok && report != nil {
		report(fmt.Sprintf(format, args...))
	}
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
func heavyFirstParentCommits(ctx context.Context, root, base, tip string) ([]string, error) {
	raw, err := heavyGitOutput(ctx, root, "rev-list", "--first-parent", "--reverse", base+".."+tip)
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
		if readsCoverPath(effective, p) {
			return true, nil
		}
	}
	return false, nil
}
func (r HeavyRunner) hasLandingRecord(ctx context.Context, sha string) (bool, error) {
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
		shas, err := landingShas(reader, sha)
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
func (r HeavyRunner) replay(ctx context.Context, sha, id, why string) (bool, error) {
	// A commit's tree never changes, so a verdict already reached for (sha, id) holds across
	// a restart that cut the culprit search short. The heavy lock owns the file.
	if dir, derr := heavyDirectory(ctx, r.Root); derr == nil {
		if saved, ok := readHeavyReplay(dir, sha, id); ok {
			r.progressf("loop: heavy %s reused replay at %s (%s): %s\n", id, shortSHA(sha), why, map[bool]string{true: "red", false: "green"}[saved])
			return saved, nil
		}
	}
	started := r.Now()
	r.progressf("loop: heavy %s replaying at %s (%s)\n", id, shortSHA(sha), why)
	red, logPath, err := r.oneLogged(ctx, sha, id)
	if err == nil {
		if dir, derr := heavyDirectory(ctx, r.Root); derr == nil {
			if serr := saveHeavyReplay(dir, sha, id, red); serr != nil {
				r.progressf("loop: heavy %s replay at %s not saved: %v\n", id, shortSHA(sha), serr)
			}
		}
	}
	took := r.Now().Sub(started).Round(time.Second)
	switch {
	case err != nil && ctx.Err() != nil:
	case err != nil:
		r.progressf("loop: heavy %s at %s: no verdict after %s, because %v\n", id, shortSHA(sha), took, err)
	case red:
		switch {
		case logPath != "":
			r.progressf("loop: heavy %s at %s: red after %s; output: %s\n", id, shortSHA(sha), took, logPath)
		default:
			r.progressf("loop: heavy %s at %s: red after %s\n", id, shortSHA(sha), took)
		}
	default:
		r.progressf("loop: heavy %s at %s: green after %s\n", id, shortSHA(sha), took)
	}
	return red, err
}

func (r HeavyRunner) firstRed(ctx context.Context, base, tip, id string, reads []string) (string, bool, string, error) {
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
		return "", true, fmt.Sprintf("no commit in %s..%s touches the check's declared reads", shortSHA(base), shortSHA(tip)), nil
	}
	red, err := r.replay(ctx, base, id, "the green base")
	if err != nil {
		return "", false, "", err
	}
	if red {
		return "", true, "red at the green base", nil
	}
	// The search needs a red upper bound. A later green commit means this
	// range cannot explain the tip, even if an earlier commit was red.
	red, err = r.replay(ctx, commits[len(commits)-1], id, fmt.Sprintf("the last of %d candidate commits since green", len(commits)))
	if err != nil {
		return "", false, "", err
	}
	if !red {
		return "", true, "the last candidate commit touching the check's reads is green", nil
	}
	low, high := 0, len(commits)-1
	for low < high {
		mid := (low + high) / 2
		red, err := r.replay(ctx, commits[mid], id, fmt.Sprintf("commit %d of %d, between %d and %d", mid+1, len(commits), low+1, high+1))
		if err != nil {
			return "", false, "", err
		}
		if red {
			high = mid
		} else {
			low = mid + 1
		}
	}
	return commits[low], false, "", nil
}

func ReadHeavyStatus(ctx context.Context, root, storePath string) (HeavyStatus, error) {
	return heavy.ReadStatus(ctx, root, storePath)
}
