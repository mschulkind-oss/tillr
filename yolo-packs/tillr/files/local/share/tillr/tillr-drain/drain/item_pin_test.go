package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

func TestSettleRunMovesItemPinRef(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	if err := l.stageLanes(ctx); err != nil {
		t.Fatal(err)
	}

	settleCommit := func(content, msg string) (runID, head string) {
		t.Helper()
		base := runGit(t, f.root, "rev-parse", TrunkRef)
		p := mustPlan(t, l)[0]
		ok, _, err := l.applyPlan(ctx, p, base, l.cfg.Now())
		if err != nil || !ok {
			t.Fatalf("applyPlan: %v/%v", ok, err)
		}
		work := pendingForTest(t, f, "item-a")
		if _, err := l.stageDispatches(ctx, []Decision{work.Decision}, base, &LoopResult{}); err != nil {
			t.Fatal(err)
		}
		wt := l.lanePath("lane-1")
		if err := os.MkdirAll(filepath.Join(wt, "tools/item-a"), 0755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(wt, "tools/item-a/out.txt"), content)
		runGit(t, wt, "add", ".")
		runGit(t, wt, "commit", "-qm", msg)
		head = runGit(t, wt, "rev-parse", "HEAD")
		runID = work.Decision.RunRequest.RunID
		rec := run.Record{
			RunID:      runID,
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    work.Decision.RunRequest.Backend,
			State:      run.StateExited,
			ExitCode:   countPtr(0),
			StartedAt:  "2026-11-20T00:00:00Z",
			FinishedAt: "2026-11-20T00:00:01Z",
		}
		outcome := l.settle(ctx, work.Decision, rec, "RESULT: done\n", base, "tick-1", l.cfg.Now(), &LoopResult{}, nil, nil, true)
		if outcome.err != nil || outcome.stop != "" {
			t.Fatalf("settle: %+v", outcome)
		}
		return runID, head
	}

	run1, head1 := settleCommit("first\n", "first candidate")
	if got := runGit(t, f.root, "rev-parse", "--verify", pinRef(run1)+"^{commit}"); got != head1 {
		t.Fatalf("first run pin = %s, want %s", got, head1)
	}
	if got := runGit(t, f.root, "rev-parse", "--verify", itemPinRef("item-a")+"^{commit}"); got != head1 {
		t.Fatalf("item pin after first run = %s, want %s", got, head1)
	}

	// Simulate a later pinned run of the same item moving its branch head.
	wt := l.lanePath("lane-1")
	writeForkFile(t, filepath.Join(wt, "tools/item-a/out.txt"), "second\n")
	runGit(t, wt, "add", ".")
	runGit(t, wt, "commit", "-qm", "second candidate")
	head2 := runGit(t, wt, "rev-parse", "HEAD")
	if head2 == head1 {
		t.Fatal("second candidate did not advance HEAD")
	}
	if err := l.pinLane(ctx, "lane-1", "item-a", "run-item-a-a2-n1", head2); err != nil {
		t.Fatalf("second pinLane: %v", err)
	}
	if got := runGit(t, f.root, "rev-parse", "--verify", pinRef("run-item-a-a2-n1")+"^{commit}"); got != head2 {
		t.Fatalf("second run pin = %s, want %s", got, head2)
	}
	if got := runGit(t, f.root, "rev-parse", "--verify", itemPinRef("item-a")+"^{commit}"); got != head2 {
		t.Fatalf("item pin after second run = %s, want %s", got, head2)
	}
	if got := runGit(t, f.root, "rev-parse", "--verify", pinRef(run1)+"^{commit}"); got != head1 {
		t.Fatalf("first run pin moved to %s, want %s", got, head1)
	}
}

func TestRefSweepRetainsItemPinRef(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})

	trunk := strings.TrimSpace(runGit(t, f.root, "rev-parse", TrunkRef))
	longItem := "item-long-" + strings.Repeat("x", maxRefComponent)
	if err := writeItemPin(ctx, l.git, f.root, "item-a", trunk); err != nil {
		t.Fatalf("writeItemPin normal: %v", err)
	}
	if err := writeItemPin(ctx, l.git, f.root, longItem, trunk); err != nil {
		t.Fatalf("writeItemPin long: %v", err)
	}

	refs, err := SweepRefs(ctx, RefSweepRequest{Root: f.root, Store: f.storePath, At: l.cfg.Now()})
	if err != nil {
		t.Fatalf("SweepRefs: %v", err)
	}
	wantRefs := []string{itemPinRef("item-a"), itemPinRef(longItem)}
	byRef := map[string]RefDisposition{}
	for _, r := range refs {
		byRef[r.Ref] = r
	}
	for _, want := range wantRefs {
		got, ok := byRef[want]
		if !ok {
			t.Fatalf("SweepRefs did not report %s; got %+v", want, refs)
		}
		if got.Remove {
			t.Errorf("SweepRefs marked %s for removal (%q)", want, got.Reason)
		}
		if !strings.Contains(got.Reason, "retained") {
			t.Errorf("SweepRefs reason for %s = %q, want retained explanation", want, got.Reason)
		}
		if sha := strings.TrimSpace(runGit(t, f.root, "rev-parse", "--verify", want+"^{commit}")); sha != trunk {
			t.Errorf("%s after sweep = %s, want %s", want, sha, trunk)
		}
	}
}

func TestRunBoundaryExcusesItemPinMove(t *testing.T) {
	own := Worktree{Path: "/lanes/own", Head: "base"}
	longRef := itemPinRef("item/" + strings.Repeat("z", maxRefComponent))
	before := &Boundary{
		Repo:      "/repo",
		Targets:   []string{own.Path},
		Refs:      map[string]string{itemPinRef("item-a"): "old-sha"},
		Worktrees: []Worktree{own},
	}
	after := &Boundary{
		Repo:    before.Repo,
		Targets: before.Targets,
		Refs: map[string]string{
			itemPinRef("item-a"): "new-sha",
			longRef:              "created-sha",
		},
		Worktrees: before.Worktrees,
	}
	convicted, observed, err := CompareRunObserved(before, after)
	if err != nil || len(convicted) != 0 || len(observed) != 0 {
		t.Fatalf("item pin moves were not excused: convicted=%v observed=%v err=%v", convicted, observed, err)
	}
}

func TestStagerRetainsCommitReachableOnlyFromItemPin(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})

	trunk := strings.TrimSpace(runGit(t, f.root, "rev-parse", TrunkRef))
	offNormal := strings.TrimSpace(runGit(t, f.root, "commit-tree", trunk+"^{tree}", "-p", trunk, "-m", "only item pin"))
	offLong := strings.TrimSpace(runGit(t, f.root, "commit-tree", trunk+"^{tree}", "-p", trunk, "-m", "only long item pin"))
	longItem := "item/unsafe-" + strings.Repeat("q", maxRefComponent)

	if err := writeItemPin(ctx, l.git, f.root, "item-a", offNormal); err != nil {
		t.Fatalf("writeItemPin normal: %v", err)
	}
	if err := writeItemPin(ctx, l.git, f.root, longItem, offLong); err != nil {
		t.Fatalf("writeItemPin long: %v", err)
	}

	stager := &LaneStager{Root: f.root, LaneRoot: f.laneRoot}
	for _, tc := range []struct {
		name string
		sha  string
		ref  string
	}{
		{"normal", offNormal, itemPinRef("item-a")},
		{"long", offLong, itemPinRef(longItem)},
	} {
		t.Run(tc.name, func(t *testing.T) {
			refs, err := stager.retainingRunPins(ctx, f.root, tc.sha)
			if err != nil {
				t.Fatalf("retainingRunPins: %v", err)
			}
			if len(refs) != 1 || refs[0] != tc.ref {
				t.Fatalf("retainingRunPins = %v, want [%s]", refs, tc.ref)
			}
			clearable, err := stager.clearableLaneCopy(ctx, "lane-1", "item-other", tc.sha, "")
			if err != nil {
				t.Fatalf("clearableLaneCopy: %v", err)
			}
			if !clearable {
				t.Fatalf("clearableLaneCopy = false for commit retained only by %s", tc.ref)
			}
		})
	}
}
