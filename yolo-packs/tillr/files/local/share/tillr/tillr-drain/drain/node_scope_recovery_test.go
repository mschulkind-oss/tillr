package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestNodeScopeResumeKeepsSavedScope(t *testing.T) {
	f, l, _ := workflowLoopFixture(t, "")
	raw := `{"name":"scout-then-build","version":1,"stages":[{"id":"one","kind":"agent","seat":"build","access":"write","write_scope":["tools/item-a/one/"]}],"edges":[]}`
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), raw)
	p := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ok, work, err := l.applyPlan(context.Background(), p, base, l.cfg.Now())
	if err != nil || !ok {
		t.Fatalf("claim: %v / %v", ok, err)
	}
	d := work.Decision
	if err := l.cfg.Stage.Stage(context.Background(), StageSpec{LaneID: d.LaneID, ItemID: d.ItemID, RunID: d.RunRequest.RunID, Backend: d.RunRequest.Backend, WorkingDir: WorktreeDir, BaseSHA: base, Node: &d.Workflow.Stages[0]}); err != nil {
		t.Fatal(err)
	}
	// The saved graph, not an edited workflow file, owns the resumed node.
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/scout-then-build@v1.json"), workflowFixture)
	fence, err := rederiveFence(context.Background(), ResumeConfig{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot}, resumeRun{ItemID: d.ItemID, LaneID: d.LaneID}, l.laneDirPath(d.LaneID), d.RunRequest.RunID)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Join(fence, ",") != "tools/item-a/one/" {
		t.Errorf("resume widened node fence: %v", fence)
	}
	for _, dir := range []string{l.laneDirPath(d.LaneID), RunDir(f.root, d.RunRequest.RunID)} {
		body, err := os.ReadFile(filepath.Join(dir, FenceFile))
		if err != nil || string(body) != "tools/item-a/one/\n" {
			t.Errorf("resumed node fence %s: %s / %v", dir, body, err)
		}
	}
	target := l.laneTarget(d.LaneID)
	if err := os.MkdirAll(filepath.Join(target, "tools/item-a/two"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(target, "tools/item-a/two/escaped.txt"), "escape\n")
	runGit(t, target, "add", ".")
	runGit(t, target, "commit", "-qm", "escaped node")
	c := Candidate{LaneID: d.LaneID, Worktree: target, BaseSHA: base, HeadSHA: runGit(t, target, "rev-parse", "HEAD")}
	escape, err := l.fenceViolationAt(context.Background(), c, filepath.Join(l.laneDirPath(d.LaneID), FenceFile))
	if err != nil || !strings.Contains(escape, "tools/item-a/two/escaped.txt") {
		t.Fatalf("resumed node escape admitted: %q / %v", escape, err)
	}
	// An owner narrowing the item cannot authorize the saved node to widen it.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET allowed_paths='["tools/item-a/two/"]' WHERE item_id='item-a'`)
		return err
	})
	_, err = rederiveFence(context.Background(), ResumeConfig{Root: f.root, Store: f.storePath, LaneRoot: f.laneRoot}, resumeRun{ItemID: d.ItemID, LaneID: d.LaneID}, l.laneDirPath(d.LaneID), d.RunRequest.RunID)
	if err == nil || !strings.Contains(err.Error(), ReasonNodeScopeOutsideFence) {
		t.Fatalf("resumed node widened edited item fence: %v", err)
	}
}

func TestNodeScopeHeldCandidateRestoresItemAuthority(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	lane := l.laneDirPath(c.LaneID)
	// The last node's scope cannot replace the whole candidate's fence.
	writeForkFile(t, filepath.Join(lane, ItemFenceFile), "candidate.txt\n")
	writeForkFile(t, filepath.Join(lane, FenceFile), "last-node-only.txt\n")
	examineHeldCandidate(t, l)
	st := openFixture(t, f.storePath)
	var work *pendingWork
	if err := st.ReadTx(context.Background(), func(r *store.Reader) error {
		works, err := readPending(r)
		work = works[c.ItemID]
		return err
	}); err != nil {
		t.Fatal(err)
	}
	st.Close()
	if work.HeldFence != "candidate.txt\n" {
		t.Errorf("saved last node fence: %q", work.HeldFence)
	}
	// Emulate metadata left by a sibling that occupied the released lane.
	writeForkFile(t, filepath.Join(lane, ItemFenceFile), "sibling-only.txt\n")
	writeForkFile(t, filepath.Join(lane, ItemBaseSHAFile), "stale-sibling-base\n")
	work.HeldFence = "candidate.txt\n"
	if err := l.stagePinnedCandidate(context.Background(), work); err != nil {
		t.Fatal(err)
	}
	if l.laneBase(c.LaneID) != c.BaseSHA {
		t.Errorf("candidate uses sibling base: %s", l.laneBase(c.LaneID))
	}
	if escape, err := l.fenceViolation(context.Background(), c); err != nil || escape != "" {
		t.Fatalf("candidate uses sibling fence: %q / %v", escape, err)
	}
}
