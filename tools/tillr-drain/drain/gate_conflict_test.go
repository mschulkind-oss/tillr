package drain

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type conflictGateSteps struct {
	recordingSteps
	db       *store.Store
	root     string
	now      func() time.Time
	trees    map[string]string
	mu       sync.Mutex
	gateRuns []string
	landRuns []string
	onGate   func(ctx context.Context, c Candidate) (GateOutcome, error)
}

func (s *conflictGateSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.mu.Lock()
	s.gateRuns = append(s.gateRuns, c.RunID)
	s.mu.Unlock()
	if s.onGate != nil {
		return s.onGate(ctx, c)
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *conflictGateSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.mu.Lock()
	s.landRuns = append(s.landRuns, c.RunID)
	tree := s.trees[c.RunID]
	s.mu.Unlock()
	if tree == "" {
		out, err := exec.CommandContext(ctx, "git", "-C", c.Worktree, "rev-parse", c.HeadSHA+"^{tree}").Output()
		if err != nil {
			return LandOutcome{}, err
		}
		tree = strings.TrimSpace(string(out))
	}
	trunk, err := land.OpenTrunk(s.root)
	if err != nil {
		return LandOutcome{}, err
	}
	out, err := land.Land(ctx, s.db, trunk, land.LandingRequest{
		ItemID: c.ItemID, RunID: c.RunID, BaseRef: TrunkRef, BaseSHA: c.BaseSHA, HeadSHA: c.HeadSHA,
		ObservedTrunk: c.ObservedTrunk, At: s.now(),
		Verdict: land.Verdict{VerdictID: c.RunID + "-gate", BaseSHA: c.BaseSHA, TreeSHA: tree, Result: land.VerdictPass},
	})
	if err != nil {
		return LandOutcome{}, err
	}
	if out.Result == land.ResultSquashConflict {
		return LandOutcome{Result: LandSquashConflict, ConflictPaths: out.ConflictPaths}, nil
	}
	if !out.Landed() {
		return LandOutcome{Result: LandRefused, Reason: out.Reason}, nil
	}
	return LandOutcome{Result: LandLanded, TrunkAfter: out.TrunkAfter}, nil
}

type syncCaptureStager struct {
	mu    sync.Mutex
	specs []StageSpec
}

func (s *syncCaptureStager) Stage(_ context.Context, spec StageSpec) error {
	s.mu.Lock()
	s.specs = append(s.specs, spec)
	s.mu.Unlock()
	return errors.New("sync capture stager: stop after recording the spec")
}

func (s *syncCaptureStager) snapshot() []StageSpec {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]StageSpec(nil), s.specs...)
}

func worktreeCount(t *testing.T, root string) int {
	t.Helper()
	out := runGit(t, root, "worktree", "list", "--porcelain")
	count := 0
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "worktree ") {
			count++
		}
	}
	return count
}

// TestConflictingCandidateGoesToConflictRepairBeforeGateStarts verifies that a
// candidate whose squash no longer applies to the trunk tip is sent to conflict
// repair before any gate starts, without creating a worktree.
func TestConflictingCandidateGoesToConflictRepairBeforeGateStarts(t *testing.T) {
	l, c, _, stager := conflictBaseFixture(t, true)
	var gateStarted atomic.Int32
	steps := &conflictGateSteps{
		root: l.cfg.Root,
		now:  l.cfg.Now,
		onGate: func(_ context.Context, _ Candidate) (GateOutcome, error) {
			gateStarted.Add(1)
			return GateOutcome{Outcome: GatePassed}, nil
		},
	}
	l.cfg.Steps = steps
	worktreesBefore := worktreeCount(t, l.cfg.Root)

	result := &LoopResult{}
	_, landed, err := l.verify(context.Background(), &c, result, &PassTiming{})
	if err != nil && !errors.Is(err, errRepairHeld) {
		t.Fatalf("verify failed: %v", err)
	}
	if landed {
		t.Fatal("conflicting candidate should not land")
	}
	if got := gateStarted.Load(); got != 0 {
		t.Fatalf("gate started %d times, want 0", got)
	}
	if got := worktreeCount(t, l.cfg.Root); got != worktreesBefore {
		t.Fatalf("worktree count changed from %d to %d", worktreesBefore, got)
	}
	if len(stager.specs) != 1 {
		t.Fatalf("staged %d conflict-fix specs, want 1", len(stager.specs))
	}
	spec := stager.specs[0]
	if spec.Conflict == nil {
		t.Fatal("conflict-fix spec missing Conflict brief")
	}
	if !slices.Equal(spec.Conflict.Paths, []string{"shared.txt"}) {
		t.Fatalf("conflict paths = %v, want [shared.txt]", spec.Conflict.Paths)
	}
	if !strings.Contains(spec.Conflict.Markers, "<<<<<<<") {
		t.Fatalf("conflict markers missing merge markers:\n%s", spec.Conflict.Markers)
	}
}

// TestRunningGateStopsImmediatelyWhenTrunkLandingConflicts verifies that when a
// landing moves the trunk while sibling gates are running, a candidate whose
// squash no longer applies has its gate stopped immediately and goes to conflict
// repair, while a disjoint sibling's gate keeps running and lands.
func TestRunningGateStopsImmediatelyWhenTrunkLandingConflicts(t *testing.T) {
	items := []string{"item-winner", "item-loser", "item-disjoint"}
	lanes := []string{"lane-1", "lane-2", "lane-3"}
	f := newRefusalLoopFixtureWithLanes(t, len(lanes), items...)
	writeForkFile(t, filepath.Join(f.root, ".git", "info", "exclude"), ".tillr/\n")
	writeForkFile(t, filepath.Join(f.root, "shared.txt"), "initial\n")
	writeForkFile(t, filepath.Join(f.root, "other.txt"), "initial\n")
	runGit(t, f.root, "add", "shared.txt", "other.txt")
	runGit(t, f.root, "commit", "-qm", "seed shared and other")
	base := runGit(t, f.root, "rev-parse", TrunkRef)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	db, err := store.Open(ctx, f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	entered := make(chan string, len(items))
	loserCanceled := make(chan struct{})
	releaseDisjoint := make(chan struct{})
	var disjointCanceled atomic.Bool

	steps := &conflictGateSteps{
		db:    db,
		root:  f.root,
		trees: map[string]string{},
		onGate: func(gctx context.Context, c Candidate) (GateOutcome, error) {
			entered <- c.ItemID
			switch c.ItemID {
			case "item-loser":
				// Never released by the test: only trunk-move cancellation stops it.
				<-gctx.Done()
				close(loserCanceled)
				return GateOutcome{}, gctx.Err()
			case "item-disjoint":
				select {
				case <-releaseDisjoint:
					return GateOutcome{Outcome: GatePassed}, nil
				case <-gctx.Done():
					disjointCanceled.Store(true)
					return GateOutcome{}, gctx.Err()
				}
			default:
				return GateOutcome{Outcome: GatePassed}, nil
			}
		},
	}
	stager := &syncCaptureStager{}
	var report bytes.Buffer
	l, err := NewLoop(LoopConfig{
		Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot,
		Lanes: lanes, Steps: steps, Stage: stager, Report: &report,
		Policy: &Policy{DefaultBackend: "fixture", Backends: map[string]Backend{"fixture": {Adapter: "fixture"}}},
		Now:    func() time.Time { return time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}
	defer func() {
		cancel()
		l.examinations.Wait()
		if t.Failed() {
			t.Log(report.String())
		}
	}()
	steps.now = l.cfg.Now

	type laneSetup struct {
		item, lane, file, content string
	}
	setups := []laneSetup{
		{"item-winner", "lane-1", "shared.txt", "winner change\n"},
		{"item-loser", "lane-2", "shared.txt", "loser change\n"},
		{"item-disjoint", "lane-3", "other.txt", "disjoint change\n"},
	}
	works := make([]pendingWork, len(setups))
	for i, s := range setups {
		wt := l.lanePath(s.lane)
		runGit(t, wt, "reset", "--hard", base)
		writeForkFile(t, filepath.Join(wt, s.file), s.content)
		writeForkFile(t, filepath.Join(l.laneDirPath(s.lane), FenceFile), s.file+"\n")
		runGit(t, wt, "add", s.file)
		runGit(t, wt, "commit", "-qm", "test: "+s.item)
		head := runGit(t, wt, "rev-parse", "HEAD")
		runID := "run-" + s.item
		req := &run.Request{RunID: runID, ItemID: s.item, LaneID: s.lane, Backend: "fixture"}
		cand := &Candidate{ItemID: s.item, RunID: runID, LaneID: s.lane, Worktree: wt, BaseSHA: base, HeadSHA: head, Request: req}
		steps.trees[runID] = runGit(t, wt, "rev-parse", "HEAD^{tree}")
		works[i] = pendingWork{
			Decision:  Decision{Action: ActionJudge, ItemID: s.item, LaneID: s.lane, RunRequest: req},
			Candidate: cand,
			BaseSHA:   base,
		}
	}

	// Start loser and disjoint gates first and wait until both are running.
	l.launchExamination(ctx, works[1].Decision, &works[1], l.cfg.Now(), &PassTiming{}, nil)
	l.launchExamination(ctx, works[2].Decision, &works[2], l.cfg.Now(), &PassTiming{}, nil)
	guard := time.NewTimer(hangGuard)
	defer guard.Stop()
	seen := map[string]bool{}
	for len(seen) < 2 {
		select {
		case id := <-entered:
			seen[id] = true
		case <-guard.C:
			t.Fatalf("timed out waiting for loser and disjoint gates to start; saw %v", seen)
		}
	}

	// Now launch winner, which passes its gate and lands on shared.txt.
	l.launchExamination(ctx, works[0].Decision, &works[0], l.cfg.Now(), &PassTiming{}, nil)

	// Wait for loser's gate to be canceled by winner's landing.
	select {
	case <-loserCanceled:
	case <-guard.C:
		t.Fatal("loser gate was not stopped when conflicting trunk landing moved main")
	}

	// Release disjoint gate; it must still be running and land cleanly.
	close(releaseDisjoint)

	completed := map[string]runCompletion{}
	for len(completed) < 3 {
		select {
		case done := <-l.completions:
			delete(l.active, done.id)
			completed[done.id] = done
		case <-guard.C:
			t.Fatalf("timed out waiting for examinations to finish; completed %d/3", len(completed))
		}
	}

	if disjointCanceled.Load() {
		t.Fatal("disjoint sibling gate was canceled even though its squash still applied")
	}
	if w := completed["run-item-winner"]; w.outcome.err != nil || w.result.Landed != 1 {
		t.Fatalf("winner did not land: %+v", w)
	}
	if d := completed["run-item-disjoint"]; d.outcome.err != nil || d.result.Landed != 1 {
		t.Fatalf("disjoint sibling did not land: %+v", d)
	}
	if lo := completed["run-item-loser"]; lo.outcome.err != nil || lo.result.Landed != 0 {
		t.Fatalf("loser unexpected outcome: %+v", lo)
	}

	specs := stager.snapshot()
	if len(specs) != 1 || specs[0].ItemID != "item-loser" || specs[0].Conflict == nil {
		t.Fatalf("expected one conflict-fix spec for item-loser, got %+v", specs)
	}
	if !slices.Equal(specs[0].Conflict.Paths, []string{"shared.txt"}) {
		t.Fatalf("conflict paths = %v, want [shared.txt]", specs[0].Conflict.Paths)
	}
}

// TestDetachedGateStopsWhenTrunkLandingConflicts verifies that a real detached
// gate supervisor process is stopped promptly when a trunk landing invalidates
// its candidate's squash.
func TestDetachedGateStopsWhenTrunkLandingConflicts(t *testing.T) {
	l, c, _, stager := conflictBaseFixture(t, false)
	// conflictBaseFixture(false) moved trunk on other.txt, so c still applies before gate.
	gateScript := filepath.Join(t.TempDir(), "slow-gate.sh")
	startedFile := filepath.Join(t.TempDir(), "gate-started")
	script := fmt.Sprintf("#!/bin/sh\necho started > %q\nexec sleep 60\n", startedFile)
	if err := os.WriteFile(gateScript, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	l.cfg.Steps = &ProcessSteps{
		Root:        l.cfg.Root,
		Store:       l.cfg.Store,
		GateProgram: gateScript,
	}

	doneCh := make(chan error, 1)
	go func() {
		_, _, err := l.verify(context.Background(), &c, &LoopResult{}, &PassTiming{})
		doneCh <- err
	}()

	// Wait for the detached gate script to actually start.
	deadline := time.Now().Add(hangGuard)
	for {
		if _, err := os.Stat(startedFile); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("detached gate script did not start in time")
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Now move trunk with a conflicting change on shared.txt and notify active gates.
	writeForkFile(t, filepath.Join(l.cfg.Root, "shared.txt"), "conflicting trunk change\n")
	runGit(t, l.cfg.Root, "add", "shared.txt")
	runGit(t, l.cfg.Root, "commit", "-qm", "conflicting trunk landing")
	newTip := runGit(t, l.cfg.Root, "rev-parse", TrunkRef)
	l.checkActiveGatesOnTrunkMove(context.Background(), newTip)

	select {
	case err := <-doneCh:
		if err != nil && !errors.Is(err, errRepairHeld) {
			t.Fatalf("verify returned error: %v", err)
		}
	case <-time.After(hangGuard):
		t.Fatal("detached gate did not stop promptly on conflicting trunk move")
	}
	if len(stager.specs) != 1 || stager.specs[0].Conflict == nil {
		t.Fatalf("expected one conflict-fix spec, got %+v", stager.specs)
	}
	if !slices.Equal(stager.specs[0].Conflict.Paths, []string{"shared.txt"}) {
		t.Fatalf("conflict paths = %v, want [shared.txt]", stager.specs[0].Conflict.Paths)
	}
}

// TestTrunkSquashCheckRunsOneGitProcessPerCandidatePerTrunkMove verifies that
// checking active gates on a trunk move spawns exactly one git process per
// candidate per trunk move and starts no worktree.
func TestTrunkSquashCheckRunsOneGitProcessPerCandidatePerTrunkMove(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 2, "item-1", "item-2")
	writeForkFile(t, filepath.Join(f.root, "a.txt"), "base\n")
	writeForkFile(t, filepath.Join(f.root, "b.txt"), "base\n")
	runGit(t, f.root, "add", "a.txt", "b.txt")
	runGit(t, f.root, "commit", "-qm", "seed")
	base := runGit(t, f.root, "rev-parse", TrunkRef)

	l, err := NewLoop(LoopConfig{
		Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot,
		Lanes:  []string{"lane-1", "lane-2"},
		Steps:  &recordingSteps{},
		Stage:  &captureStager{},
		Policy: &Policy{DefaultBackend: "fixture", Backends: map[string]Backend{"fixture": {Adapter: "fixture"}}},
		Now:    func() time.Time { return time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC) },
	})
	if err != nil {
		t.Fatal(err)
	}

	makeCand := func(itemID, lane, file, content string) Candidate {
		wt := l.lanePath(lane)
		runGit(t, wt, "reset", "--hard", base)
		writeForkFile(t, filepath.Join(wt, file), content)
		runGit(t, wt, "add", file)
		runGit(t, wt, "commit", "-qm", itemID)
		return Candidate{
			ItemID: itemID, RunID: "run-" + itemID, LaneID: lane,
			Worktree: wt, BaseSHA: base, HeadSHA: runGit(t, wt, "rev-parse", "HEAD"),
		}
	}
	c1 := makeCand("item-1", "lane-1", "a.txt", "cand1\n")
	c2 := makeCand("item-2", "lane-2", "b.txt", "cand2\n")

	ctx1, cancel1 := context.WithCancel(context.Background())
	defer cancel1()
	ctx2, cancel2 := context.WithCancel(context.Background())
	defer cancel2()
	g1, conflicts1, _, err := l.prepareActiveGate(ctx1, c1, cancel1)
	if err != nil || len(conflicts1) != 0 || g1 == nil {
		t.Fatalf("prepareActiveGate c1: g=%v conflicts=%v err=%v", g1, conflicts1, err)
	}
	defer l.unregisterActiveGate(g1)
	g2, conflicts2, _, err := l.prepareActiveGate(ctx2, c2, cancel2)
	if err != nil || len(conflicts2) != 0 || g2 == nil {
		t.Fatalf("prepareActiveGate c2: g=%v conflicts=%v err=%v", g2, conflicts2, err)
	}
	defer l.unregisterActiveGate(g2)

	// Advance trunk on a.txt (conflicts with c1, clean for c2).
	writeForkFile(t, filepath.Join(f.root, "a.txt"), "trunk\n")
	runGit(t, f.root, "add", "a.txt")
	runGit(t, f.root, "commit", "-qm", "trunk move")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)

	// Wrap git in PATH to count exact git process invocations during the check.
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	binDir := t.TempDir()
	logFile := filepath.Join(binDir, "git.log")
	wrapper := fmt.Sprintf("#!/bin/sh\nprintf '%%s\\n' \"$*\" >> %q\nexec %q \"$@\"\n", logFile, realGit)
	if err := os.WriteFile(filepath.Join(binDir, "git"), []byte(wrapper), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))

	l.checkActiveGatesOnTrunkMove(context.Background(), tip)
	// Duplicate notification for the same tip must spawn zero additional processes.
	l.checkActiveGatesOnTrunkMove(context.Background(), tip)

	raw, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("reading git log: %v", err)
	}
	var lines []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			lines = append(lines, line)
		}
	}
	if len(lines) != 2 {
		t.Fatalf("expected 2 git processes (1 per candidate per trunk move), got %d:\n%s", len(lines), raw)
	}
	for _, line := range lines {
		if !strings.Contains(line, "merge-tree --write-tree") {
			t.Fatalf("unexpected git command: %s", line)
		}
		if strings.Contains(line, "worktree") {
			t.Fatalf("squash check must not touch worktrees: %s", line)
		}
	}
	if ctx1.Err() == nil {
		t.Fatal("conflicting candidate c1 gate was not canceled")
	}
	if ctx2.Err() != nil {
		t.Fatal("non-conflicting candidate c2 gate was canceled")
	}
}
