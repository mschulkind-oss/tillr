package drain

import (
	"context"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

// waitForPath polls until path exists. The path is written by a fixture process with no
// channel back to the test, so the poll is the only way to see it. No wall-clock bound: a
// fixture that never writes it is stopped by the suite's own timeout.
func waitForPath(t *testing.T, path string) {
	t.Helper()
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// TestARealRollingRunDuringASiblingRunConvictsNothing brackets one lane's run the way the
// loop does (Loop.snapshot), and runs the REAL rolling step inside the bracket: its
// detached worktree under <lane root>/.rolling is created after the bracket opened and
// removed before it closed, and a second bracket opens while it exists. On 2026-11-17, at
// five lanes, two runs that made no edits ended inside a rolling window and were both held
// host-boundary-violation for "worktree <lane root>/.rolling/worktree appeared". The loop's
// own scratch worktree is its designed work, never a run's escape.
func TestARealRollingRunDuringASiblingRunConvictsNothing(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	tip := runGit(t, f.root, "rev-parse", "HEAD")
	gate := scratchUnderDurable(t)
	inside, release := filepath.Join(gate, "inside"), filepath.Join(gate, "release")
	program := executable(t, "touch "+inside+"\nwhile [ ! -f "+release+" ]; do sleep 0.01; done\nexit 0")
	steps := &ProcessSteps{Root: f.root, LaneRoot: f.laneRoot, RollingProgram: program}
	ctx := context.Background()
	own := []string{l.lanePath("lane-1")}
	watched := []string{"lane-2"}

	before, err := l.snapshot(ctx, own, watched)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		out, err := steps.Rolling(ctx, "", tip)
		if err == nil && !out.Green {
			err = Internalf("fixture: the rolling program was not green: %+v", out)
		}
		done <- err
	}()
	waitForPath(t, inside)
	during, err := l.snapshot(ctx, own, watched)
	if err != nil {
		t.Fatal(err)
	}
	rolling, err := canonicalDir(filepath.Join(f.laneRoot, ".rolling", "worktree"))
	if err != nil || worktreeFor(during.Worktrees, rolling) == nil {
		t.Fatalf("fixture: the rolling worktree is not registered while the rolling run is in flight: %v", err)
	}
	writeForkFile(t, release, "release\n")
	if err := <-done; err != nil {
		t.Fatal(err)
	}
	after, err := l.snapshot(ctx, own, watched)
	if err != nil {
		t.Fatal(err)
	}
	if worktreeFor(after.Worktrees, rolling) != nil {
		t.Fatal("fixture: the rolling run left its worktree registered")
	}

	for _, bracket := range []struct {
		name          string
		before, after *Boundary
	}{
		{"a run that started before the rolling worktree existed and ended inside it", before, during},
		{"a run that started inside the rolling window and ended after it", during, after},
		{"a run that spanned the whole rolling run", before, after},
	} {
		violations, err := compareAll(bracket.before, bracket.after)
		if err != nil {
			t.Fatal(err)
		}
		if len(violations) != 0 {
			t.Errorf("%s was convicted of the loop's own rolling worktree: %v", bracket.name, violations)
		}
	}
}

// The excuse is the loop's own reserved places and shapes, and nothing wider: a worktree
// that appears anywhere else during a run — beside the reserved directories, under a
// reserved directory at a path the loop never uses, on a branch where the loop only ever
// checks out a detached HEAD, or outside the lane root entirely — is still observed on
// the run's bracket. Since the owner's 2026-11-17 ruling it is information, not a
// conviction (CompareRunObserved); the loop's own places are not even observed.
func TestAWorktreeOutsideTheLoopsReservedPlacesIsObserved(t *testing.T) {
	mainWT := Worktree{Path: "/repo", Head: "aaa", Branch: "refs/heads/main"}
	own := Worktree{Path: "/lanes/lane-1/worktree", Head: "aaa", Branch: "refs/heads/tillr/x/lane-1"}
	for _, wt := range []Worktree{
		{Path: "/lanes/.rollingx/worktree", Head: "aaa"},
		{Path: "/lanes/.rolling/other", Head: "aaa"},
		{Path: "/lanes/.rolling/worktree/nested", Head: "aaa"},
		{Path: "/lanes/.rolling/worktree", Head: "aaa", Branch: "refs/heads/escape"},
		{Path: "/lanes/.replay/abc/deeper", Head: "aaa"},
		{Path: "/lanes/.replay/abc", Head: "aaa", Branch: "refs/heads/escape"},
		{Path: "/lanes/desk-doc/elsewhere", Head: "aaa"},
		{Path: "/lanes/desk-doc/worktree", Head: "aaa", Branch: "refs/heads/escape"},
		{Path: "/lanes/lane-1/durable/run-a1/scratch", Head: "aaa"},
		{Path: "/repo/elsewhere", Head: "aaa"},
		{Path: "/elsewhere/.rolling/worktree", Head: "aaa"},
	} {
		snap := func(worktrees ...Worktree) *Boundary {
			return &Boundary{Repo: "/repo", Targets: []string{own.Path}, LaneRoot: "/lanes", LaneScope: "x",
				Refs: map[string]string{}, Worktrees: worktrees}
		}
		convicted, appeared, err := CompareRunObserved(snap(mainWT, own), snap(mainWT, own, wt))
		if err != nil {
			t.Fatal(err)
		}
		if len(convicted) != 0 || len(appeared) != 1 || appeared[0].Kind != "worktree" || appeared[0].Where != wt.Path || appeared[0].Detail != "appeared" {
			t.Errorf("a worktree appearing at %s (branch %q) answered %v / %v, want the one observation naming it", wt.Path, wt.Branch, convicted, appeared)
		}
		convicted, gone, err := CompareRunObserved(snap(mainWT, own, wt), snap(mainWT, own))
		if err != nil {
			t.Fatal(err)
		}
		if len(convicted) != 0 || len(gone) != 1 || gone[0].Kind != "worktree" || gone[0].Where != wt.Path || gone[0].Detail != "went away" {
			t.Errorf("a worktree going away at %s (branch %q) answered %v / %v, want the one observation naming it", wt.Path, wt.Branch, convicted, gone)
		}
	}
}

// The loop's other scratch worktrees are excused by the same rule as the rolling one: the
// trunk-already-red replay checks a revision out at <lane root>/.replay/<sha>, and a review
// comment's desk lane is staged at <lane root>/desk-<doc>/worktree and removed again by its
// reset. A desk lane is not one of the configured lanes, so the sibling-directory excuse
// never covered it, and its branch — created by its first staging and re-pointed by every
// later one — is the loop's lane branch, not a rogue ref.
func TestTheLoopsReplayAndDeskWorktreesNeverConvict(t *testing.T) {
	mainWT := Worktree{Path: "/repo", Head: "aaa", Branch: "refs/heads/main"}
	own := Worktree{Path: "/lanes/lane-1/worktree", Head: "aaa", Branch: "refs/heads/tillr/x/lane-1"}
	desk := Worktree{Path: "/lanes/desk-doc/worktree", Head: "bbb", Branch: "refs/heads/tillr/x/desk-doc"}
	for _, wt := range []Worktree{
		{Path: "/lanes/.rolling/worktree", Head: "aaa"},
		{Path: "/lanes/.replay/0123456789ab", Head: "aaa"},
		desk,
	} {
		snap := func(refs map[string]string, worktrees ...Worktree) *Boundary {
			return &Boundary{Repo: "/repo", Targets: []string{own.Path}, LaneRoot: "/lanes", LaneScope: "x",
				Refs: refs, Worktrees: worktrees}
		}
		refs := map[string]string{}
		if wt.Branch != "" {
			refs[wt.Branch] = wt.Head
		}
		convicted, appeared, err := CompareRunObserved(snap(map[string]string{}, mainWT, own), snap(refs, mainWT, own, wt))
		if err != nil {
			t.Fatal(err)
		}
		if appeared = append(appeared, convicted...); len(appeared) != 0 {
			t.Errorf("the loop's worktree %s appearing convicted the run: %v", wt.Path, appeared)
		}
		moved := map[string]string{}
		for ref := range refs {
			moved[ref] = "ccc"
		}
		convicted, gone, err := CompareRunObserved(snap(refs, mainWT, own, wt), snap(moved, mainWT, own))
		if err != nil {
			t.Fatal(err)
		}
		if gone = append(gone, convicted...); len(gone) != 0 {
			t.Errorf("the loop's worktree %s going away (and its branch moving) convicted the run: %v", wt.Path, gone)
		}
	}
	// A desk branch of another lane root's scope, or an ordinary lane-root branch nobody
	// has checked out, is not the loop's desk lane and is still observed.
	for _, ref := range []string{"refs/heads/tillr/other/desk-doc", "refs/heads/tillr/x/rogue"} {
		before := &Boundary{Repo: "/repo", Targets: []string{own.Path}, LaneRoot: "/lanes", LaneScope: "x", Refs: map[string]string{}, Worktrees: []Worktree{mainWT, own}}
		after := &Boundary{Repo: "/repo", Targets: []string{own.Path}, LaneRoot: "/lanes", LaneScope: "x", Refs: map[string]string{ref: "ddd"}, Worktrees: []Worktree{mainWT, own}}
		convicted, violations, err := CompareRunObserved(before, after)
		if err != nil {
			t.Fatal(err)
		}
		if len(convicted) != 0 || len(violations) != 1 || violations[0].Kind != "ref" || !strings.Contains(violations[0].Detail, ref) {
			t.Errorf("the ref %s appearing answered %v / %v, want the one ref observation", ref, convicted, violations)
		}
	}
}

// The loop's scratch-worktree steps prune registrations whose directory is already gone
// (`git worktree prune` in detachedWorktree and the stager's clearWorktree). A registration
// that was prunable when the run's bracket opened was gone before the run started, so its
// pruning during the run is not the run's doing.
func TestAPrunableRegistrationPrunedDuringARunDoesNotConvict(t *testing.T) {
	mainWT := Worktree{Path: "/repo", Head: "aaa", Branch: "refs/heads/main"}
	own := Worktree{Path: "/lanes/lane-1/worktree", Head: "aaa", Branch: "refs/heads/tillr/x/lane-1"}
	stale := Worktree{Path: "/somewhere/abandoned", Head: "aaa", Prunable: true}
	snap := func(worktrees ...Worktree) *Boundary {
		return &Boundary{Repo: "/repo", Targets: []string{own.Path}, Refs: map[string]string{}, Worktrees: worktrees}
	}
	violations, err := compareAll(snap(mainWT, own, stale), snap(mainWT, own))
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 0 {
		t.Fatalf("pruning a registration that was already prunable convicted the run: %v", violations)
	}
	live := stale
	live.Prunable = false
	_, observed, err := CompareRunObserved(snap(mainWT, own, live), snap(mainWT, own))
	if err != nil {
		t.Fatal(err)
	}
	if len(observed) != 1 || observed[0].Detail != "went away" {
		t.Fatalf("a live registration that went away answered %v, want the one observation", observed)
	}
}

// A fork-series landing moves refs/heads/patch/<fork> to the squash commit it marked at
// refs/tillr/landed/<run> first. A sibling run in flight across that landing did not move
// the patch branch; the lander did, and its marker names the commit exactly. A patch branch
// moved to anything else is still convicted.
func TestASiblingForkLandingsPatchBranchMoveDoesNotConvict(t *testing.T) {
	mainWT := Worktree{Path: "/repo", Head: "aaa", Branch: "refs/heads/main"}
	own := Worktree{Path: "/lanes/lane-1/worktree", Head: "aaa", Branch: "refs/heads/tillr/x/lane-1"}
	snap := func(refs map[string]string) *Boundary {
		return &Boundary{Repo: "/repo", Targets: []string{own.Path}, Refs: refs, Worktrees: []Worktree{mainWT, own}}
	}
	before := snap(map[string]string{"refs/heads/patch/pi": "p1"})
	landed := snap(map[string]string{"refs/heads/patch/pi": "p2", "refs/tillr/landed/run-item-f-a1": "p2", "refs/tillr/runs/run-item-f-a1": "h1"})
	if violations, err := compareAll(before, landed); err != nil || len(violations) != 0 {
		t.Fatalf("a sibling's fork landing convicted the run: %v %v", violations, err)
	}
	rogue := snap(map[string]string{"refs/heads/patch/pi": "p3", "refs/tillr/landed/run-item-f-a1": "p2"})
	_, violations, err := CompareRunObserved(before, rogue)
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 || !strings.Contains(violations[0].Detail, "refs/heads/patch/pi") {
		t.Fatalf("a patch branch moved to an unmarked commit answered %v, want the one ref observation", violations)
	}
	// A marker that was already there before the bracket opened is not this bracket's landing.
	old := snap(map[string]string{"refs/heads/patch/pi": "p1", "refs/tillr/landed/run-item-f-a1": "p2"})
	_, violations, err = CompareRunObserved(old, snap(map[string]string{"refs/heads/patch/pi": "p2", "refs/tillr/landed/run-item-f-a1": "p2"}))
	if err != nil {
		t.Fatal(err)
	}
	if len(violations) != 1 {
		t.Fatalf("a patch branch moved to a marker older than the bracket answered %v, want the one ref observation", violations)
	}
}

// processAlive answers whether pid is a live (not zombie) process.
func processAlive(pid int) bool {
	start, err := processStart(pid)
	return err == nil && start != ""
}

// A loop stop cancels a rolling run mid-check. The check's whole process tree — the
// program and the test processes it started — must be ended before the rolling worktree is
// removed: on 2026-11-17 a `go test -parallel 48 ./...` kept running for two minutes in the
// deleted worktree after the loop stopped, because the cancellation reached only the program.
func TestAStoppedRollingRunEndsItsWholeProcessTreeBeforeRemovingItsWorktree(t *testing.T) {
	root, tip := committedRoot(t)
	gate := scratchUnderDurable(t)
	inside, pidFile := filepath.Join(gate, "inside"), filepath.Join(gate, "pid")
	program := executable(t, "sleep 300 >/dev/null 2>&1 &\necho $! > "+pidFile+"\ntouch "+inside+"\nwait")
	laneRoot := filepath.Join(root, ".tillr", "lanes")
	steps := &ProcessSteps{Root: root, LaneRoot: laneRoot, RollingProgram: program}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		_, err := steps.Rolling(ctx, "", tip)
		done <- err
	}()
	waitForPath(t, inside)
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	if !processAlive(pid) {
		t.Fatal("fixture: the check's child is not running")
	}
	cancel()
	<-done
	if processAlive(pid) {
		t.Fatalf("the rolling check's child %d outlived the stopped rolling run", pid)
	}
	if _, err := os.Stat(filepath.Join(laneRoot, ".rolling", "worktree")); !os.IsNotExist(err) {
		t.Fatalf("the stopped rolling run left its worktree: %v", err)
	}
}

// A stop that arrives while a candidate is being pinned must not leave the candidate
// unpinned: the pin is the only thing that keeps a refused or conflicted head reachable
// after the lane reset. On 2026-11-17 the stop's cancellation cut off
// `git update-ref refs/tillr/runs/...` with "context canceled".
func TestAPinSurvivesTheStopsCancellation(t *testing.T) {
	f, l := passFixture(t, "item-a")
	head := runGit(t, f.root, "rev-parse", "HEAD")
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := l.pin(ctx, "run-item-a-a1", head); err != nil {
		t.Fatalf("pinning under a canceled context failed: %v", err)
	}
	if got := runGit(t, f.root, "rev-parse", "refs/tillr/runs/run-item-a-a1"); got != head {
		t.Fatalf("the pin is at %q, want %s", got, head)
	}
}

// compareAll answers everything a run's bracket reports, convicted or only observed: an
// excuse for the loop's own work must keep that work out of both.
func compareAll(before, after *Boundary) ([]Violation, error) {
	violations, observed, err := CompareRunObserved(before, after)
	return append(violations, observed...), err
}
