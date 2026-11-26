package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func heldForkFixture(t *testing.T) (refusalLoopFixture, *Loop, Candidate) {
	t.Helper()
	f, l, c := heldCandidateFixture(t)
	runGit(t, c.Worktree, "reset", "--hard", c.BaseSHA)
	const fork = "fixture-fork"
	c.BaseRef = "refs/heads/patch/" + fork
	c.Worktree = filepath.Join(f.root, ".forks", "lanes", c.LaneID, fork)
	if err := os.MkdirAll(c.Worktree, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, c.Worktree, "init", "-b", "main")
	runGit(t, c.Worktree, "commit", "--allow-empty", "-qm", "fork base")
	c.BaseSHA = runGit(t, c.Worktree, "rev-parse", "HEAD")
	runGit(t, c.Worktree, "remote", "add", "upstream", filepath.Join(f.root, "original-upstream"))
	runGit(t, c.Worktree, "update-ref", "refs/remotes/upstream/main", c.BaseSHA)
	runGit(t, c.Worktree, "tag", "fork-base", c.BaseSHA)
	runGit(t, f.root, "fetch", "--no-tags", c.Worktree, "HEAD:"+c.BaseRef)
	writeForkFile(t, filepath.Join(c.Worktree, "candidate.txt"), "fork-only candidate\n")
	runGit(t, c.Worktree, "add", "candidate.txt")
	runGit(t, c.Worktree, "commit", "-qm", "fork candidate")
	c.HeadSHA = runGit(t, c.Worktree, "rev-parse", "HEAD")
	if _, err := l.revParse(context.Background(), f.root, c.HeadSHA+"^{commit}"); err == nil {
		t.Fatal("fixture head is already in root")
	}
	fence := ".forks/lanes/" + c.LaneID + "/" + fork + "/candidate.txt\n"
	for name, body := range map[string]string{
		TargetFile: c.Worktree + "\n", PatchRefFile: c.BaseRef + "\n",
		BaseSHAFile: c.BaseSHA + "\n", ItemBaseSHAFile: c.BaseSHA + "\n",
		FenceFile: fence, ItemFenceFile: fence,
	} {
		writeForkFile(t, filepath.Join(l.laneDirPath(c.LaneID), name), body)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, candidateReadyKind, c.RunID, c, l.cfg.Now())
	})
	return f, l, c
}

func TestHeldForkSurvivesCheckoutReplacement(t *testing.T) {
	f, l, c := heldForkFixture(t)
	releaseForRestage(t, f, l, c)
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.BaseSHA {
		t.Fatalf("old fork checkout still holds candidate: %s", got)
	}
	if err := os.RemoveAll(c.Worktree); err != nil {
		t.Fatal(err)
	}
	work := assignHeldForTest(t, f, l, c)
	for range 2 {
		if err := l.stagePinnedCandidate(context.Background(), work); err != nil {
			t.Fatal(err)
		}
	}
	restored := *work.Candidate
	if restored.Worktree == c.Worktree || l.laneTarget(restored.LaneID) != restored.Worktree || l.lanePatchRef(restored.LaneID) != c.BaseRef {
		t.Fatalf("restored the wrong target: %+v", restored)
	}
	if got := runGit(t, restored.Worktree, "rev-parse", "HEAD", "refs/remotes/upstream/main", "refs/tags/fork-base"); got != strings.Join([]string{c.HeadSHA, c.BaseSHA, c.BaseSHA}, "\n") {
		t.Fatalf("restored fork refs: %s", got)
	}
	if got := runGit(t, restored.Worktree, "remote", "get-url", "upstream"); got != filepath.Join(f.root, "original-upstream") {
		t.Fatalf("restored upstream: %s", got)
	}
	if raw, err := os.ReadFile(filepath.Join(restored.Worktree, "candidate.txt")); err != nil || string(raw) != "fork-only candidate\n" {
		t.Fatalf("restored fork contents: %q (%v)", raw, err)
	}
	if why, err := l.fenceViolation(context.Background(), restored); err != nil || why != "" {
		t.Fatalf("rebound fence: %q (%v)", why, err)
	}
	if got := runGit(t, f.root, "rev-parse", c.BaseRef); got != c.BaseSHA {
		t.Fatalf("delivery ref moved: %s", got)
	}
}

func TestHeldForkKeepsLaneWithLiveWorker(t *testing.T) {
	f, l, c := heldForkFixture(t)
	worker := exec.Command("sleep", "300")
	worker.Dir = c.Worktree
	worker.Env = append(os.Environ(), "TILLR_RUN_ID="+c.RunID+"-member", "TILLR_LOOP_STORE="+l.cfg.Store)
	if err := worker.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = worker.Process.Kill(); _ = worker.Wait() })
	ctx := context.Background()
	if err := l.holdItemFault(ctx, c, StopGateHarnessError, "gate returned no verdict"); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if w := pendingForTest(t, f, c.ItemID); w.LaneReleased || w.ReleasePending != "" || !strings.Contains(w.LaneKeepReason, "live marked process") {
		t.Fatalf("live worker's checkout was not kept: %+v", w)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
		t.Fatalf("live worker's checkout changed to %s", got)
	}
}

func TestInterruptedForkReleaseRequiresOriginalPin(t *testing.T) {
	f, l, c := heldForkFixture(t)
	ctx := context.Background()
	if err := l.pinHeldFork(ctx, c); err != nil {
		t.Fatal(err)
	}
	stage, err := l.saveHeldLaneStage(ctx, c)
	if err != nil {
		t.Fatal(err)
	}
	intent := candidateLane{Candidate: c, Fence: stage.NodeFence, Stage: stage, ResetSHA: runGit(t, f.root, "rev-parse", TrunkRef)}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, candidateReleaseStartedKind, c.RunID, intent, l.cfg.Now())
	})
	runGit(t, c.Worktree, "reset", "--hard", c.BaseSHA)
	runGit(t, f.root, "update-ref", "-d", candidatePinRef(c))
	if err := l.finishReleaseIntent(ctx, c.RunID); err == nil {
		t.Fatal("interrupted release succeeded without its original pin")
	}
	if got := pendingForTest(t, f, c.ItemID); got.LaneReleased || got.ReleasePending == "" || !strings.Contains(got.LaneKeepReason, "cannot be read back") {
		t.Fatalf("interrupted release lost its reservation or reason: %+v", got)
	}
	if err := l.pinnedCandidate(ctx, c); err == nil {
		t.Fatal("recovery recreated its missing pin")
	}
	runGit(t, f.root, "update-ref", candidatePinRef(c), c.HeadSHA)
	if err := l.finishReleaseIntent(ctx, c.RunID); err != nil {
		t.Fatal(err)
	}
	if !pendingForTest(t, f, c.ItemID).LaneReleased {
		t.Fatal("release did not finish after restoring its original pin")
	}
}

func TestHeldForkRestoresUpstreamRefsInAnotherLane(t *testing.T) {
	f, l, c := heldForkFixture(t)
	runGit(t, c.Worktree, "update-ref", "refs/remotes/upstream/stable", c.BaseSHA)
	releaseForRestage(t, f, l, c)

	destWorktree := filepath.Join(f.root, ".forks", "lanes", "lane-2", "fixture-fork")
	if err := os.MkdirAll(destWorktree, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, destWorktree, "init", "-b", "main")
	runGit(t, destWorktree, "fetch", "--no-tags", f.root, c.BaseRef)
	runGit(t, destWorktree, "checkout", "-q", "-B", "main", "FETCH_HEAD")
	runGit(t, destWorktree, "remote", "add", "upstream", filepath.Join(f.root, "original-upstream"))
	// Simulate newer upstream fetches in lane-2 than when lane-1 held the candidate.
	runGit(t, destWorktree, "commit", "--allow-empty", "-qm", "newer upstream main")
	newerMain := runGit(t, destWorktree, "rev-parse", "HEAD")
	runGit(t, destWorktree, "update-ref", "refs/remotes/upstream/main", newerMain)
	runGit(t, destWorktree, "commit", "--allow-empty", "-qm", "newer upstream stable")
	newerStable := runGit(t, destWorktree, "rev-parse", "HEAD")
	runGit(t, destWorktree, "update-ref", "refs/remotes/upstream/stable", newerStable)
	runGit(t, destWorktree, "reset", "--hard", c.BaseSHA)

	work := assignHeldForTest(t, f, l, c)
	if err := l.stagePinnedCandidate(context.Background(), work); err != nil {
		t.Fatalf("stagePinnedCandidate refused lane with newer upstream refs: %v", err)
	}
	restored := *work.Candidate
	if restored.Worktree != destWorktree {
		t.Fatalf("restored worktree = %q, want %q", restored.Worktree, destWorktree)
	}
	got := runGit(t, restored.Worktree, "rev-parse", "HEAD", "refs/remotes/upstream/main", "refs/remotes/upstream/stable", "refs/tags/fork-base")
	want := strings.Join([]string{c.HeadSHA, c.BaseSHA, c.BaseSHA, c.BaseSHA}, "\n")
	if got != want {
		t.Fatalf("restored fork refs = %q, want %q", got, want)
	}
	if raw, err := os.ReadFile(filepath.Join(restored.Worktree, "candidate.txt")); err != nil || string(raw) != "fork-only candidate\n" {
		t.Fatalf("restored fork contents: %q (%v)", raw, err)
	}
	if why, err := l.fenceViolation(context.Background(), restored); err != nil || why != "" {
		t.Fatalf("rebound fence: %q (%v)", why, err)
	}
}

func TestHeldForkStillRefusesConflictingTagInAnotherLane(t *testing.T) {
	f, l, c := heldForkFixture(t)
	releaseForRestage(t, f, l, c)

	destWorktree := filepath.Join(f.root, ".forks", "lanes", "lane-2", "fixture-fork")
	if err := os.MkdirAll(destWorktree, 0755); err != nil {
		t.Fatal(err)
	}
	runGit(t, destWorktree, "init", "-b", "main")
	runGit(t, destWorktree, "fetch", "--no-tags", f.root, c.BaseRef)
	runGit(t, destWorktree, "checkout", "-q", "-B", "main", "FETCH_HEAD")
	runGit(t, destWorktree, "remote", "add", "upstream", filepath.Join(f.root, "original-upstream"))
	runGit(t, destWorktree, "commit", "--allow-empty", "-qm", "other tag target")
	otherTag := runGit(t, destWorktree, "rev-parse", "HEAD")
	runGit(t, destWorktree, "tag", "fork-base", otherTag)
	runGit(t, destWorktree, "reset", "--hard", c.BaseSHA)

	work := assignHeldForTest(t, f, l, c)
	err := l.stagePinnedCandidate(context.Background(), work)
	refusal := asLaneRefusal(err)
	if refusal == nil || refusal.Reason != ReasonLaneUnlandedChange || !strings.Contains(refusal.Msg, "has different upstream inputs") {
		t.Fatalf("stagePinnedCandidate err = %v, want lane refusal for different upstream inputs", err)
	}
}
