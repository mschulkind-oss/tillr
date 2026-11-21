package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func heldWorkflowCandidate(t *testing.T) (refusalLoopFixture, *Loop, Candidate) {
	t.Helper()
	f, l, c := heldCandidateFixture(t)
	w := pendingForTest(t, f, c.ItemID)
	w.Decision.Workflow = &Workflow{
		Stages: []WorkflowStage{{ID: "build", Outputs: map[string]string{"plan": "text"}}, {ID: "finish"}},
		Edges:  []WorkflowEdge{{From: "build", FromOutput: "plan", To: "finish", ToInput: "plan"}},
	}
	w.Decision.NodeIndex, w.Decision.NodeBaseSHA = 1, c.HeadSHA
	w.ParentRunID = c.RunID + "-build"
	writeForkFile(t, filepath.Join(l.laneDirPath(c.LaneID), BaseSHAFile), c.HeadSHA+"\n")
	writeForkFile(t, filepath.Join(c.Worktree, "candidate.txt"), "finished candidate\n")
	runGit(t, c.Worktree, "commit", "-qam", "finish workflow candidate")
	c.HeadSHA = runGit(t, c.Worktree, "rev-parse", "HEAD")
	w.Candidate = &c
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, nodeAdvancedKind, c.RunID, w, l.cfg.Now())
	})
	return f, l, c
}

func releaseForRestage(t *testing.T, f refusalLoopFixture, l *Loop, c Candidate) {
	t.Helper()
	ctx := context.Background()
	if err := l.holdItemFault(ctx, c, StopGateHarnessError, "gate returned no verdict"); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if w := pendingForTest(t, f, c.ItemID); !w.LaneReleased {
		t.Fatalf("lane not released: %s", w.LaneKeepReason)
	}
}

func assignHeldForTest(t *testing.T, f refusalLoopFixture, l *Loop, c Candidate) *pendingWork {
	t.Helper()
	w := pendingForTest(t, f, c.ItemID)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		d := w.Decision
		d.LaneID = "lane-2"
		return l.assignPinnedCandidate(tx, w, &d, l.cfg.Now())
	})
	return pendingForTest(t, f, c.ItemID)
}

func TestHeldWorkflowCandidateSurvivesLaneReuse(t *testing.T) {
	f, l, c := heldWorkflowCandidate(t)
	original := pendingForTest(t, f, c.ItemID)
	artifact := filepath.Join(nodeOutputsDir(l.cfg.Root, original.ParentRunID), "plan")
	if err := os.MkdirAll(filepath.Dir(artifact), 0700); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, artifact, "accepted build output\n")
	producer := original.Decision
	producer.NodeIndex = 0
	req := *producer.RunRequest
	req.RunID, producer.RunRequest = original.ParentRunID, &req
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES (?,?,'build',?,'running')`, c.ItemID, producer.Attempt, req.RunID)
		return err
	})
	outputs, err := checkNodeOutputs(l.cfg.Root, producer)
	if err != nil {
		t.Fatal(err)
	}
	if err := l.acceptNodeOutputs(context.Background(), producer, outputs); err != nil {
		t.Fatal(err)
	}
	lane := l.laneDirPath(c.LaneID)
	writeForkFile(t, filepath.Join(lane, ItemFenceFile), "candidate.txt\nother.txt\n")
	writeForkFile(t, filepath.Join(lane, FenceFile), "candidate.txt\n")
	releaseForRestage(t, f, l, c)
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.BaseSHA {
		t.Fatalf("first lane still holds candidate: %s", got)
	}
	writeForkFile(t, filepath.Join(lane, FenceFile), "sibling.txt\n")
	w := assignHeldForTest(t, f, l, c)
	if err := l.stagePinnedCandidate(context.Background(), w); err != nil {
		t.Fatal(err)
	}
	if w.Decision.Workflow == nil || w.ParentRunID != original.ParentRunID || w.Decision.NodeIndex != 1 || w.Candidate.HeadSHA != c.HeadSHA {
		t.Fatalf("workflow identity lost: %+v", w)
	}
	inputs, err := l.nodeInputPaths(context.Background(), w.Decision)
	if err != nil || len(inputs["plan"]) != 1 || inputs["plan"][0].Path != artifact {
		t.Fatalf("accepted input no longer resolves after reassignment: %v (%v)", inputs, err)
	}
	for path, expected := range map[string]string{
		artifact: "accepted build output\n",
		filepath.Join(w.Candidate.Worktree, "candidate.txt"):           "finished candidate\n",
		filepath.Join(l.laneDirPath(w.Decision.LaneID), BaseSHAFile):   original.Decision.NodeBaseSHA + "\n",
		filepath.Join(l.laneDirPath(w.Decision.LaneID), FenceFile):     "candidate.txt\n",
		filepath.Join(l.laneDirPath(w.Decision.LaneID), ItemFenceFile): "candidate.txt\nother.txt\n",
	} {
		if raw, err := os.ReadFile(path); err != nil || string(raw) != expected {
			t.Errorf("%s: %q (%v), want %q", path, raw, err, expected)
		}
	}
}

func TestHeldWorkflowNeverReleasesUnderUnsettledMember(t *testing.T) {
	f, l, c := heldWorkflowCandidate(t)
	ctx := context.Background()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO read_only_runs(run_id,group_id,item_id,lane_id) VALUES (?,?,?,?)`, c.RunID+"-reader", c.RunID, c.ItemID, c.LaneID)
		return err
	})
	if err := l.holdItemFault(ctx, c, StopGateHarnessError, "gate returned no verdict"); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	w := pendingForTest(t, f, c.ItemID)
	if w.LaneReleased || !strings.Contains(w.LaneKeepReason, "unsettled") {
		t.Fatalf("live member did not keep lane: released=%v, reason=%q", w.LaneReleased, w.LaneKeepReason)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
		t.Fatalf("live member's checkout changed to %s", got)
	}
	if w.ReleasePending != "" {
		t.Fatal("recorded a reset intent while the member still owned its claim")
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE run_id=?`, c.RunID+"-reader")
		return err
	})
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if !pendingForTest(t, f, c.ItemID).LaneReleased {
		t.Fatal("hold still keeps the lane after the member settled")
	}
}

func TestHeldWorkflowKeepsUnrecordedHead(t *testing.T) {
	f, l, c := heldWorkflowCandidate(t)
	runGit(t, c.Worktree, "commit", "--allow-empty", "-qm", "unrecorded head")
	head := runGit(t, c.Worktree, "rev-parse", "HEAD")
	ctx := context.Background()
	if err := l.holdItemFault(ctx, c, StopGateHarnessError, "gate returned no verdict"); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	w := pendingForTest(t, f, c.ItemID)
	if w.LaneReleased || w.ReleasePending != "" || w.LaneKeepReason != "unrecorded lane HEAD needs inspection" {
		t.Fatalf("unexpected head did not keep lane: %+v", w)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != head {
		t.Fatalf("unrecorded head was reset to %s", got)
	}
}

func TestPreCandidateHoldExplainsWhyItKeepsLane(t *testing.T) {
	f, l, c := heldWorkflowCandidate(t)
	w := pendingForTest(t, f, c.ItemID)
	w.Candidate = nil
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffect(tx, nodeAdvancedKind, c.RunID, w, l.cfg.Now())
	})
	ctx := context.Background()
	if err := l.holdItemFault(ctx, c, StopGateHarnessError, "hold before settlement"); err != nil {
		t.Fatal(err)
	}
	if err := l.releaseIdleHeldCandidates(ctx); err != nil {
		t.Fatal(err)
	}
	if err := l.readStore(ctx, func(r *store.Reader) error {
		states, err := PendingLaneStates(r)
		if s := states[c.ItemID]; s.Status != "kept" || !strings.Contains(s.Reason, "no recorded candidate") {
			t.Errorf("hold lacks a specific explanation: %+v", s)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := runGit(t, c.Worktree, "rev-parse", "HEAD"); got != c.HeadSHA {
		t.Fatalf("pre-candidate lane changed to %s", got)
	}
}
