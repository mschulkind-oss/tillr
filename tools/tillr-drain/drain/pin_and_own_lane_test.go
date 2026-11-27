package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestIntakeLandingOfferPinsCommitImmediately(t *testing.T) {
	ctx := context.Background()
	f, l, _, d := continuationDispatchFixture(t, "set -- first\n"+standInLandingOffer, false)
	req := *d.RunRequest
	base := l.laneBase(d.LaneID)
	head := runGit(t, l.laneTarget(d.LaneID), "rev-parse", "HEAD")

	got, err := l.intakeLandingOffer(ctx, d, run.Record{RunID: req.RunID}, base)
	if err != nil || got != landingOfferSubmitted {
		t.Fatalf("intakeLandingOffer = %v, %v; want submitted, nil", got, err)
	}
	pinned := runGit(t, f.root, "rev-parse", "--verify", "refs/tillr/runs/"+req.RunID+"^{commit}")
	if pinned != head {
		t.Fatalf("pin = %s, want %s", pinned, head)
	}
}

func TestSettleNodePinsAcceptedWriteCommitImmediately(t *testing.T) {
	f, l, steps := restartGraphFixture(t)
	result := &LoopResult{}
	graphPass(t, l, result)
	graphPass(t, l, result)
	if len(steps.runs) != 1 {
		t.Fatalf("runs = %+v, want 1", steps.runs)
	}
	runID := steps.runs[0].RunID
	head := runGit(t, l.laneTarget("lane-1"), "rev-parse", "HEAD")
	pinned := runGit(t, f.root, "rev-parse", "--verify", "refs/tillr/runs/"+runID+"^{commit}")
	if pinned != head {
		t.Fatalf("pin = %s, want %s", pinned, head)
	}
}

func TestSettleDeferJudgePinsCandidateCommitImmediately(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	if err := l.stageLanes(ctx); err != nil {
		t.Fatal(err)
	}
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
	writeForkFile(t, filepath.Join(wt, "tools/item-a/out.txt"), "candidate\n")
	runGit(t, wt, "add", ".")
	runGit(t, wt, "commit", "-qm", "candidate")
	head := runGit(t, wt, "rev-parse", "HEAD")
	rec := run.Record{
		RunID:      work.Decision.RunRequest.RunID,
		ItemID:     "item-a",
		LaneID:     "lane-1",
		Backend:    work.Decision.RunRequest.Backend,
		State:      run.StateExited,
		ExitCode:   countPtr(0),
		StartedAt:  "2026-11-20T00:00:00Z",
		FinishedAt: "2026-11-20T00:00:00Z",
	}
	outcome := l.settle(ctx, work.Decision, rec, "RESULT: done\n", base, "tick-1", l.cfg.Now(), &LoopResult{}, nil, nil, true)
	if outcome.err != nil || outcome.stop != "" {
		t.Fatalf("settle(deferJudge=true): %+v", outcome)
	}
	pinned := runGit(t, f.root, "rev-parse", "--verify", "refs/tillr/runs/"+rec.RunID+"^{commit}")
	if pinned != head {
		t.Fatalf("pin = %s, want %s", pinned, head)
	}
}

func TestClearableLaneCopyProtectsForeignActivePendingWork(t *testing.T) {
	ctx := context.Background()
	f, _ := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "checkout", "-qb", "lane-work")
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "item-a commit")
	sha := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", "refs/tillr/runs/run-item-a-1", sha)
	runGit(t, f.root, "checkout", "-q", TrunkRef)

	// Record an active, unreleased pendingWork for item-a on lane-1.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, dispatchStartedKind, "run-item-a-1", pendingWork{
			Decision: Decision{ItemID: "item-a", LaneID: "lane-1", RunRequest: &run.Request{RunID: "run-item-a-1", ItemID: "item-a", LaneID: "lane-1"}},
			BaseSHA:  base,
		}, time.Now().UTC())
	})

	stager := &LaneStager{Root: f.root, Store: f.storePath}
	// Another item must not clear item-a's live, unreleased lane.
	clearable, err := stager.clearableLaneCopy(ctx, "lane-1", "item-b", sha, "")
	if err != nil {
		t.Fatal(err)
	}
	if clearable {
		t.Fatal("foreign item cleared active unreleased lane")
	}

	// Owning item's own prior run pin is recognized as clearable.
	clearable, err = stager.clearableLaneCopy(ctx, "lane-1", "item-a", sha, "")
	if err != nil {
		t.Fatal(err)
	}
	if !clearable {
		t.Fatal("owning item was refused its own prior run pin")
	}

	// Once item-a's lane is released, another item can clear the pinned lane copy.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, candidateLaneKind, "run-item-a-1", candidateLane{
			Candidate: Candidate{ItemID: "item-a", LaneID: "lane-1", RunID: "run-item-a-1", HeadSHA: sha},
			Released:  true,
		}, time.Now().UTC())
	})
	clearable, err = stager.clearableLaneCopy(ctx, "lane-1", "item-b", sha, "")
	if err != nil {
		t.Fatal(err)
	}
	if !clearable {
		t.Fatal("foreign item could not clear released pinned lane copy")
	}
}

func TestSameItemFreshAttemptRestagesOverOwnUnlandedCommitAfterTrunkMoves(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	if err := l.stageLanes(ctx); err != nil {
		t.Fatal(err)
	}
	oldBase := runGit(t, f.root, "rev-parse", TrunkRef)
	wt := l.lanePath("lane-1")
	if err := os.MkdirAll(filepath.Join(wt, "tools/item-a"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(wt, "tools/item-a/work.txt"), "own candidate\n")
	runGit(t, wt, "add", ".")
	runGit(t, wt, "commit", "-qm", "own unlanded candidate")
	ownHead := runGit(t, wt, "rev-parse", "HEAD")

	// Record candidateReady without a pre-existing git pin to verify restaging pins it.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, candidateReadyKind, "run-item-a-prior", Candidate{
			ItemID:   "item-a",
			LaneID:   "lane-1",
			RunID:    "run-item-a-prior",
			Worktree: wt,
			BaseSHA:  oldBase,
			HeadSHA:  ownHead,
		}, l.cfg.Now())
	})

	// Advance trunk past oldBase.
	writeForkFile(t, filepath.Join(f.root, "trunk-advance.txt"), "moved\n")
	runGit(t, f.root, "add", ".")
	runGit(t, f.root, "commit", "-qm", "advance trunk")
	newBase := runGit(t, f.root, "rev-parse", TrunkRef)

	stager := &LaneStager{Root: l.cfg.Root, LaneRoot: l.cfg.LaneRoot, Store: l.cfg.Store, Now: l.cfg.Now}
	if err := stager.ensureWorktree(ctx, StageSpec{
		LaneID:  "lane-1",
		ItemID:  "item-a",
		RunID:   "run-item-a-a2-n0",
		BaseSHA: newBase,
	}, l.laneDirPath("lane-1"), wt); err != nil {
		t.Fatalf("ensureWorktree refused own candidate commit: %v", err)
	}
	if got := runGit(t, wt, "rev-parse", "HEAD"); got != newBase {
		t.Fatalf("worktree HEAD = %s, want %s", got, newBase)
	}
	if pinned := runGit(t, f.root, "rev-parse", "--verify", "refs/tillr/runs/run-item-a-prior^{commit}"); pinned != ownHead {
		t.Fatalf("prior run pin = %s, want %s", pinned, ownHead)
	}
}

func TestContinuingWorkflowNodeStartsAfterTrunkMovesAndFromPin(t *testing.T) {
	t.Run("same-lane-after-trunk-moves", func(t *testing.T) {
		f, l, steps := restartGraphFixture(t)
		result := &LoopResult{}
		graphPass(t, l, result)
		graphPass(t, l, result)
		work := pendingForTest(t, f, "item-a")
		if work.Decision.NodeIndex != 1 {
			t.Fatalf("node index = %d, want 1", work.Decision.NodeIndex)
		}
		// Advance trunk past work.BaseSHA.
		writeForkFile(t, filepath.Join(f.root, "trunk-advance.txt"), "moved\n")
		runGit(t, f.root, "add", ".")
		runGit(t, f.root, "commit", "-qm", "advance trunk")

		next := replaceGraphLoop(t, f, l, steps)
		graphPass(t, next, result)
		if len(steps.runs) != 2 || steps.runs[1].RunID != "run-item-a-a1-n2" {
			t.Fatalf("successor node did not run after trunk moved: %+v", steps.runs)
		}
	})

	t.Run("restage-from-pin-after-reassignment", func(t *testing.T) {
		ctx := context.Background()
		f, l, steps := restartGraphFixture(t)
		l.cfg.Lanes = []string{"lane-1", "lane-2"}
		if err := l.stageLanes(ctx); err != nil {
			t.Fatal(err)
		}
		result := &LoopResult{}
		graphPass(t, l, result)
		graphPass(t, l, result)
		work := pendingForTest(t, f, "item-a")
		if work.Decision.NodeIndex != 1 {
			t.Fatalf("node index = %d, want 1", work.Decision.NodeIndex)
		}
		node0Head := work.Decision.NodeBaseSHA

		// Simulate lane reassignment: move the pending decision to lane-2 (currently at trunk).
		reassigned := work.Decision
		reassigned.LaneID = "lane-2"
		req := *reassigned.RunRequest
		req.LaneID = "lane-2"
		reassigned.RunRequest = &req

		if err := l.resetLane(ctx, "lane-2"); err != nil {
			t.Fatal(err)
		}
		var reportBuf strings.Builder
		l.cfg.Report = &reportBuf
		trunk := runGit(t, f.root, "rev-parse", TrunkRef)
		staged, err := l.stageDispatches(ctx, []Decision{reassigned}, trunk, result)
		if err != nil {
			t.Fatalf("stageDispatches from pin failed: %v (report=%s)", err, reportBuf.String())
		}
		if len(staged) != 1 || staged[0].LaneID != "lane-2" {
			t.Fatalf("staged = %+v, want lane-2 (report=%s, infra=%d)", staged, reportBuf.String(), result.InfraFailures)
		}
		if got := runGit(t, l.lanePath("lane-2"), "rev-parse", "HEAD"); got != node0Head {
			t.Fatalf("lane-2 HEAD = %s, want %s", got, node0Head)
		}
		rawItemBase, err := os.ReadFile(filepath.Join(l.laneDirPath("lane-2"), ItemBaseSHAFile))
		if err != nil {
			t.Fatal(err)
		}
		if strings.TrimSpace(string(rawItemBase)) != work.BaseSHA {
			t.Fatalf("item_base_sha = %q, want %q", strings.TrimSpace(string(rawItemBase)), work.BaseSHA)
		}
		_ = steps
	})
}

func TestPinLaneDoesNotCrossRepositoryBoundaries(t *testing.T) {
	ctx := context.Background()
	root := scratchUnderDurable(t)
	runGit(t, root, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(root, "a.txt"), "root\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-qm", "root")
	rootHead := runGit(t, root, "rev-parse", "HEAD")

	laneRoot := filepath.Join(root, ".tillr", "lanes")
	fork := filepath.Join(root, ".forks", "lanes", "lane-2", "pi")
	if err := os.MkdirAll(fork, 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, fork, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(fork, "f.txt"), "fork\n")
	runGit(t, fork, "add", ".")
	runGit(t, fork, "commit", "-qm", "fork")
	forkHead := runGit(t, fork, "rev-parse", "HEAD")

	laneDir := filepath.Join(laneRoot, "lane-2")
	if err := os.MkdirAll(laneDir, 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(laneDir, TargetFile), fork+"\n")
	writeForkFile(t, filepath.Join(laneDir, FenceFile), ".forks/lanes/lane-2/pi/\n")
	l := &Loop{cfg: LoopConfig{Root: root, LaneRoot: laneRoot}}
	if l.laneTarget("lane-2") != fork {
		t.Fatalf("lane target = %q, want the fork checkout %q", l.laneTarget("lane-2"), fork)
	}

	if err := l.pinLane(ctx, "lane-1", "item-missing", "run-missing", forkHead); err == nil {
		t.Fatal("an ordinary lane's missing object must still fail the pin")
	}

	if err := l.pinLane(ctx, "lane-2", "item-held", "run-held", rootHead); err == nil {
		t.Fatal("a fork lane must not fall back to pinning a root-only commit")
	}
	if _, err := l.revParse(ctx, root, pinRef("run-held")+"^{commit}"); err == nil {
		t.Fatal("fork lane wrote a pin in the root repository")
	}

	if err := l.pinLane(ctx, "lane-1", "item-plain", "run-plain", rootHead); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, root, "rev-parse", "--verify", pinRef("run-plain")+"^{commit}"); got != rootHead {
		t.Fatalf("ordinary lane pin = %s, want %s", got, rootHead)
	}
	if got := runGit(t, root, "rev-parse", "--verify", itemPinRef("item-plain")+"^{commit}"); got != rootHead {
		t.Fatalf("ordinary lane item pin = %s, want %s", got, rootHead)
	}
}
