package drain

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// conflictingFixer puts a fixer commit on lane-1 and a conflicting repair on the trunk,
// the shape seen live on 2026-11-18: another commit repaired the trunk by removing the
// region the fixer edited.
func conflictingFixer(t *testing.T) (refusalLoopFixture, *Loop, Candidate, string, *strings.Builder) {
	t.Helper()
	f, l, fixer, red := fixerRoutingFixture(t)
	report := &strings.Builder{}
	l.cfg.Report = report
	lane := l.laneTarget("lane-1")
	runGit(t, lane, "reset", "-q", "--hard", red)
	writeForkFile(t, filepath.Join(lane, "base.txt"), "the fixer's edit\n")
	runGit(t, lane, "commit", "-qam", "fixer edits base")
	head := runGit(t, lane, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(f.root, "base.txt"), "the trunk's repair\n")
	runGit(t, f.root, "commit", "-qam", "repair lands")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	c := Candidate{ItemID: fixer, LaneID: "lane-1", RunID: "run-fixer-1", Worktree: lane, BaseSHA: red, HeadSHA: head}
	return f, l, c, tip, report
}

// assertLaneClean fails when a lane is mid-rebase, carries changes, or left its head.
func assertLaneClean(t *testing.T, dir, head string) {
	t.Helper()
	if got := runGit(t, dir, "status", "--porcelain"); got != "" {
		t.Fatalf("lane is not clean: %s", got)
	}
	for _, name := range []string{"rebase-merge", "rebase-apply"} {
		path := runGit(t, dir, "rev-parse", "--git-path", name)
		if !filepath.IsAbs(path) {
			path = filepath.Join(dir, path)
		}
		if _, err := os.Stat(path); err == nil {
			t.Fatalf("lane left mid-rebase (%s exists)", name)
		}
	}
	if head != "" {
		if got := runGit(t, dir, "rev-parse", "HEAD"); got != head {
			t.Fatalf("lane head %s, want %s", got, head)
		}
	}
}

// A fixer whose pre-landing rebase conflicts is aborted in the same step, with the lane
// back on the fixer's head. When the fixer's named check already passes on the tip, the
// fixer is stale: it is resolved elsewhere, its waiters are released, and the run settles
// as one whose item is terminal — not a loop fault.
func TestAFixerWhoseRebaseConflictsIsResolvedWhenItsCheckPassesOnTheTip(t *testing.T) {
	ctx := context.Background()
	f, l, c, tip, report := conflictingFixer(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.SetState(ctx, tx, "item-spare", StateBlocked, l.cfg.Now(), item.WaitingOn{Waiter: "item:" + c.ItemID, Reason: RefusalTrunkAlreadyRed + ": go-tool/unit"})
		return err
	})
	writeTipProgress(t, l, tip, nil, map[string]time.Time{"go-tool/unit": l.cfg.Now().UTC().Add(time.Minute)})

	_, err := l.preRebaseFixer(ctx, c)
	conflict := asFixerRebaseConflict(err)
	if conflict == nil {
		t.Fatalf("a conflicting fixer rebase answered %v", err)
	}
	assertLaneClean(t, c.Worktree, c.HeadSHA)
	if got := conflictPaths(conflict.Reports); len(got) != 1 || got[0] != "base.txt" {
		t.Fatalf("conflict paths %v", conflict.Reports)
	}

	stop, landed, err := l.fixerRebaseConflicted(ctx, c, conflict, &LoopResult{}, &PassTiming{})
	if asItemTerminal(err) == nil || stop != "" || landed {
		t.Fatalf("a stale fixer did not settle as terminal: %q/%v/%v", stop, landed, err)
	}
	if got := fixerState(t, f, c.ItemID); got != StateResolvedElsewhere {
		t.Fatalf("stale fixer is %s", got)
	}
	if err := l.releaseTrunkAcquittals(ctx); err != nil {
		t.Fatal(err)
	}
	if got := fixerState(t, f, "item-spare"); got != StateInQueue {
		t.Fatalf("the stale fixer's waiter was not released: %s", got)
	}
	assertLaneClean(t, c.Worktree, c.HeadSHA)
	if out := report.String(); !strings.Contains(out, c.ItemID+" resolved: every check the fixer names (go-tool/unit) passes on the trunk tip "+tip) {
		t.Fatalf("log lacks the resolution: %s", out)
	}
}

// When the fixer's check is not shown green on the tip, its rebase conflict is a landing
// conflict like any other (conflictFix): recorded with its paths for the producer, under
// the evidence key the identical-repeat rule compares, and never a loop stop.
func TestAFixerWhoseRebaseConflictsIsALandingConflictWhenStillNeeded(t *testing.T) {
	ctx := context.Background()
	f, l, c, _, report := conflictingFixer(t)
	for i := 0; i < 2; i++ { // the second pass finds the same conflict, again without a fault
		_, err := l.preRebaseFixer(ctx, c)
		conflict := asFixerRebaseConflict(err)
		if conflict == nil {
			t.Fatalf("a conflicting fixer rebase answered %v", err)
		}
		assertLaneClean(t, c.Worktree, c.HeadSHA)
		stop, landed, err := l.fixerRebaseConflicted(ctx, c, conflict, &LoopResult{}, &PassTiming{})
		if err != nil || stop != "" || landed {
			t.Fatalf("attempt %d: a fixer conflict stopped the loop: %q/%v/%v", i, stop, landed, err)
		}
	}
	if got := fixerState(t, f, c.ItemID); got == StateResolvedElsewhere {
		t.Fatal("a fixer with no tip proof was resolved")
	}
	var conflicts int
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_id=? AND payload LIKE '%"evidence":"conflict:paths=base.txt,hunks=%'`, RefusalKind, c.RunID).Scan(&conflicts)
	})
	if conflicts == 0 {
		t.Fatalf("no conflict keyed on base.txt was recorded for the producer: %s", report.String())
	}
}

// A lane whose run's item is already terminal and whose state cannot be preserved — here
// left mid-rebase with unmerged paths, as on 2026-11-18 — is not a loop fault: what it held
// is recorded and discarded, the run pin retired, and the lane reset and returned to rotation.
func TestATerminalItemsUnpreservableLaneIsDiscardedNotAFault(t *testing.T) {
	ctx := context.Background()
	f, l, d, rec := savedFixture(t)
	lane := l.laneTarget(d.LaneID)
	runGit(t, lane, "checkout", "-q", "--", ".")
	writeForkFile(t, filepath.Join(lane, "base.txt"), "lane edit\n")
	runGit(t, lane, "commit", "-qam", "lane edit")
	laneHead := runGit(t, lane, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(f.root, "base.txt"), "trunk edit\n")
	runGit(t, f.root, "commit", "-qam", "trunk edit")
	cmd := exec.Command("git", "-C", lane, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "rebase", TrunkRef)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("fixture rebase did not conflict: %s", out)
	}
	if got := runGit(t, lane, "status", "--porcelain"); !strings.Contains(got, "UU base.txt") {
		t.Fatalf("fixture lane not mid-rebase with an unmerged path: %s", got)
	}
	// An unmerged index is preservable since 2026-11-18 (preserved_index_test.go). What
	// cannot be preserved is dirty work an earlier saved ref for this run does not cover:
	// replaying would reset it away.
	ref := preservedPrefix + rec.RunID
	tree := runGit(t, f.root, "rev-parse", TrunkRef+"^{tree}")
	saved := runGit(t, f.root, "-c", "user.name=t", "-c", "user.email=t@example.invalid", "commit-tree", tree, "-m", `{"run_id":"`+rec.RunID+`","ref":"`+ref+`"}`)
	runGit(t, f.root, "update-ref", ref, saved)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.SetStateWithReceipt(ctx, tx, d.ItemID, StateResolvedElsewhere, l.cfg.Now(), item.MoveReceipt{ResolvesVia: "doc:docs/elsewhere.md"}, item.WaitingOn{})
		return err
	})
	report := &strings.Builder{}
	l.cfg.Report = report

	p := mustPlan(t, l)[0]
	ok, work, err := l.applyPlan(ctx, p, runGit(t, f.root, "rev-parse", TrunkRef), l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim examination: %v/%v", ok, err)
	}
	l.launchExamination(ctx, p.decision, work, l.cfg.Now(), &PassTiming{}, nil)
	done := <-l.completions
	l.examinations.Wait()
	if done.outcome.err != nil || done.outcome.stop != "" {
		t.Fatalf("a terminal item's unpreservable lane stopped the loop: %q/%v\n%s", done.outcome.stop, done.outcome.err, report.String())
	}
	var result LoopResult
	if stop, err := l.takeCompletion(done, &result); stop != "" || err != nil {
		t.Fatalf("the loop stopped: %q/%v", stop, err)
	}
	assertLaneClean(t, lane, "")
	if got := runGit(t, lane, "rev-parse", "HEAD"); got != runGit(t, f.root, "rev-parse", TrunkRef) {
		t.Fatalf("lane not reset to the trunk: %s", got)
	}
	if got := runGit(t, f.root, "rev-parse", pinRef(rec.RunID)); got != laneHead {
		t.Fatalf("terminal item's run pin = %s, want unlanded head %s", got, laneHead)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var payload string
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=?`, laneDiscardedKind, rec.RunID).Scan(&payload); err != nil {
			t.Fatalf("no record of what was discarded: %v", err)
		}
		if !strings.Contains(payload, "UU base.txt") {
			t.Fatalf("the discard record lacks the unmerged path: %s", payload)
		}
		var state string
		if err := r.QueryRow(`SELECT state FROM lanes WHERE lane_id = ?`, d.LaneID).Scan(&state); err != nil {
			return err
		}
		if state != "idle" {
			t.Fatalf("lane not returned to rotation: %s", state)
		}
		return nil
	})
}

// A settlement step that cannot return a stop carries a retained lane as an error; the
// completion turns it back into the lane's stop, and only a store failure stays a fault.
func TestARetainedLaneFromASettlementStepIsTheLanesStop(t *testing.T) {
	if got := retainedOutcome(laneOutcome{err: retainedAfter("lane-3", stopLaneRetained)}); got.err != nil || got.stop != stopLaneRetained {
		t.Fatalf("a retained lane stayed an error: %+v", got)
	}
	fault := retainedAfter("lane-3", StopFault)
	var retained *laneRetainedError
	if errors.As(fault, &retained) {
		t.Fatal("a hold that could not be recorded was treated as the lane's condition")
	}
	if got := retainedOutcome(laneOutcome{err: fault}); got.err == nil {
		t.Fatal("a store failure was dropped")
	}
}
