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

type itemDirRecordingSteps struct {
	*workflowSteps
	laneRoot     string
	laneIDs      []string
	scratchDirs  []string
	worktreeSeen []bool
}

func (s *itemDirRecordingSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.laneIDs = append(s.laneIDs, req.LaneID)
	wt := filepath.Join(s.laneRoot, req.LaneID, WorktreeDir)
	_, statErr := os.Stat(wt)
	s.worktreeSeen = append(s.worktreeSeen, statErr == nil)
	scratchDir := run.DurableDir(filepath.Join(s.laneRoot, req.LaneID), req.RunID)
	if err := os.MkdirAll(scratchDir, 0o755); err != nil {
		return run.Record{}, "", err
	}
	if err := os.WriteFile(filepath.Join(scratchDir, "marker.txt"), []byte(req.RunID+"\n"), 0o644); err != nil {
		return run.Record{}, "", err
	}
	s.scratchDirs = append(s.scratchDirs, scratchDir)
	return s.workflowSteps.Run(ctx, req)
}

func TestItemDirectoryTwoSequentialNodesCreateOnFirstUseAndSettle(t *testing.T) {
	f, l, ws := workflowLoopFixture(t, "")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
		return err
	})
	l.cfg.Lanes = []string{"item-a"}
	rec := &itemDirRecordingSteps{workflowSteps: ws, laneRoot: f.laneRoot}
	l.cfg.Steps = rec

	if err := l.stageLandingLanes(context.Background()); err != nil {
		t.Fatalf("stageLandingLanes: %v", err)
	}
	wantWT := filepath.Join(f.laneRoot, "item-a", WorktreeDir)
	if _, err := os.Stat(wantWT); !os.IsNotExist(err) {
		t.Fatalf("item worktree %s existed before first use (err=%v)", wantWT, err)
	}

	result := &LoopResult{}
	for i := 0; i < 6 && result.Landed == 0; i++ {
		graphPass(t, l, result)
	}
	if result.Landed != 1 || result.Rejected != 0 || result.InfraFailures != 0 {
		t.Fatalf("item-directory workflow did not settle and land cleanly: %+v", result)
	}
	if len(ws.runs) != 2 {
		t.Fatalf("runs = %d, want 2 sequential nodes: %+v", len(ws.runs), ws.runs)
	}
	for i, lane := range rec.laneIDs {
		if lane != "item-a" {
			t.Fatalf("node %d lane id = %q, want item-a", i, lane)
		}
		if !rec.worktreeSeen[i] {
			t.Fatalf("node %d did not see worktree at %s", i, wantWT)
		}
	}
	if rec.scratchDirs[0] == rec.scratchDirs[1] {
		t.Fatalf("both nodes shared one scratch dir %q", rec.scratchDirs[0])
	}
	wantScratchPrefix := filepath.Join(f.laneRoot, "item-a", "scratch") + string(filepath.Separator)
	for i, dir := range rec.scratchDirs {
		if !strings.HasPrefix(dir, wantScratchPrefix) {
			t.Fatalf("node %d scratch dir = %q, want prefix %q", i, dir, wantScratchPrefix)
		}
		if _, err := os.Stat(filepath.Join(dir, "marker.txt")); err != nil {
			t.Fatalf("node %d scratch marker missing in %s: %v", i, dir, err)
		}
	}
	for i, r := range ws.runs {
		brief, err := os.ReadFile(filepath.Join(RunDir(f.root, r.RunID), BriefFile))
		if err != nil {
			t.Fatalf("reading run %d brief: %v", i, err)
		}
		if !strings.Contains(string(brief), wantWT) {
			t.Fatalf("run %d brief does not name worktree %s", i, wantWT)
		}
		if !strings.Contains(string(brief), rec.scratchDirs[i]) {
			t.Fatalf("run %d brief does not name scratch dir %s", i, rec.scratchDirs[i])
		}
	}
}

func TestItemDirectoryStageAtPinnedHeadOrTrunk(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 1, "item-a")
	trunk := runGit(t, f.root, "rev-parse", "HEAD")

	// Create a pinned commit off trunk without moving main.
	tmpWT := filepath.Join(t.TempDir(), "pin-src")
	runGit(t, f.root, "worktree", "add", "--detach", tmpWT, trunk)
	if err := os.MkdirAll(filepath.Join(tmpWT, "tools", "item-a"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(tmpWT, "tools/item-a/pinned.txt"), "pinned\n")
	runGit(t, tmpWT, "add", "tools/item-a/pinned.txt")
	runGit(t, tmpWT, "commit", "-qm", "pinned commit")
	pinned := runGit(t, tmpWT, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", "refs/tillr/runs/run-item-a-a1", pinned)
	runGit(t, f.root, "worktree", "remove", "--force", tmpWT)

	stager := &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
	}

	// Without a pinned head, staging creates the worktree at trunk.
	if err := stager.Stage(context.Background(), StageSpec{
		ItemID:     "item-a",
		LaneID:     "item-a",
		RunID:      "run-item-a-trunk",
		Backend:    "fixture",
		WorkingDir: WorktreeDir,
		BaseSHA:    trunk,
	}); err != nil {
		t.Fatalf("Stage at trunk: %v", err)
	}
	wt := filepath.Join(f.laneRoot, "item-a", WorktreeDir)
	if got := runGit(t, wt, "rev-parse", "HEAD"); got != trunk {
		t.Fatalf("trunk worktree HEAD = %s, want %s", got, trunk)
	}

	// With a pinned head, staging materializes that commit in the item worktree.
	if err := stager.Stage(context.Background(), StageSpec{
		ItemID:          "item-a",
		LaneID:          "item-a",
		RunID:           "run-item-a-pinned",
		Backend:         "fixture",
		WorkingDir:      WorktreeDir,
		BaseSHA:         trunk,
		KeptHeadSHA:     pinned,
		KeptItemBaseSHA: trunk,
	}); err != nil {
		t.Fatalf("Stage at pinned head: %v", err)
	}
	if got := runGit(t, wt, "rev-parse", "HEAD"); got != pinned {
		t.Fatalf("pinned worktree HEAD = %s, want %s", got, pinned)
	}
}

func TestItemDirectoryKilledRunIsPreservedAndReplacementRunsInSameDirectory(t *testing.T) {
	f, l, restart := restartGraphFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-b'`)
		return err
	})
	l.cfg.Lanes = []string{"item-a"}
	l.cfg.Policy.Backends["alternate"] = Backend{Adapter: "fixture"}
	l.cfg.Policy.SeatLists = map[string][]string{"build": {"fixture", "alternate"}}
	steps := &dirtyWorkflowContinuationSteps{
		restartWorkflowSteps: restart,
		cases: []dirtyContinuationCase{
			{endCause: run.EndSignal},
		},
	}
	l.cfg.Steps = steps

	if err := l.stageLandingLanes(context.Background()); err != nil {
		t.Fatalf("stageLandingLanes: %v", err)
	}
	wantWT := filepath.Join(f.laneRoot, "item-a", WorktreeDir)
	if _, err := os.Stat(wantWT); !os.IsNotExist(err) {
		t.Fatalf("item worktree %s existed before first dispatch (err=%v)", wantWT, err)
	}

	result := &LoopResult{}
	for i := 0; i < 3; i++ {
		graphPass(t, l, result)
	}
	if result.Rejected != 0 || result.InfraFailures != 0 || result.RefusedSteps[RefusalFence] != 0 {
		t.Fatalf("killed run in item directory refused or failed: %+v", result)
	}
	if len(steps.runs) < 2 || steps.runs[0].RunID != "run-item-a-a1-n1" || steps.runs[1].RunID != "run-item-a-a1-n1-interrupted" {
		t.Fatalf("runs = %+v, want run-item-a-a1-n1 then run-item-a-a1-n1-interrupted", steps.runs)
	}
	for i := 0; i < 2; i++ {
		if steps.runs[i].LaneID != "item-a" {
			t.Fatalf("run %d lane = %q, want item-a", i, steps.runs[i].LaneID)
		}
	}
	ref := preservedPrefix + "run-item-a-a1-n1"
	want := steps.preservedSeen["run-item-a-a1-n1"]
	if got := runGit(t, f.root, "show", ref+":base.txt"); got != want {
		t.Fatalf("preserved %s:base.txt = %q, want %q", ref, got, want)
	}
	if got := runGit(t, f.root, "show", ref+":tools/item-a/uncommitted-run-item-a-a1-n1.txt"); got != want {
		t.Fatalf("preserved untracked = %q, want %q", got, want)
	}
	if !strings.Contains(steps.briefs[1], "Earlier work exists at "+ref) {
		t.Fatalf("replacement brief missing preserved ref %s:\n%s", ref, steps.briefs[1])
	}

	for i := 0; i < 6 && result.Landed == 0; i++ {
		graphPass(t, l, result)
	}
	if result.Landed != 1 || result.Rejected != 0 {
		t.Fatalf("replacement in item directory did not finish landing: %+v", result)
	}
}

func TestItemDirectoryFastStartClaimUnderLiveScale(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	l.cfg.LiveScale = true
	l.cfg.Lanes = []string{"lane-1"}

	v, s, e := claimFixture(t, l)
	// Retarget the proposed dispatch to the item's own directory, which is
	// absent from the live pool list l.cfg.Lanes = ["lane-1"].
	e.decision.LaneID = "item-a"
	e.decision.RunRequest.LaneID = "item-a"

	ok, work, gen, _, err := l.claimFastStart(ctx, v, s, e, l.cfg.Now())
	if err != nil || !ok || work == nil || gen == "" {
		t.Fatalf("claimFastStart for item directory under LiveScale = (ok=%v work=%+v gen=%q err=%v), want claimed", ok, work, gen, err)
	}
	if work.Decision.LaneID != "item-a" {
		t.Fatalf("claimed lane = %q, want item-a", work.Decision.LaneID)
	}

	// A pool lane absent from l.cfg.Lanes must still be refused under LiveScale.
	_, l2 := passFixture(t, "item-b")
	l2.cfg.LiveScale = true
	l2.cfg.Lanes = []string{"lane-1"}
	v2, s2, e2 := claimFixture(t, l2)
	e2.decision.LaneID = "lane-9"
	e2.decision.RunRequest.LaneID = "lane-9"
	ok2, work2, _, _, err2 := l2.claimFastStart(ctx, v2, s2, e2, l2.cfg.Now())
	if err2 != nil || ok2 || work2 != nil {
		t.Fatalf("claimFastStart for unoffered pool lane under LiveScale = (ok=%v work=%+v err=%v), want refused", ok2, work2, err2)
	}
	_ = f
}

func TestItemDirectoryStageRefusesUnsafeAndPoolShapedItemIDs(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 1, "item-a")
	trunk := runGit(t, f.root, "rev-parse", "HEAD")
	laneRoot := filepath.Join(t.TempDir(), "lanes")
	if err := os.MkdirAll(laneRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	stager := &LaneStager{
		Root:     f.root,
		LaneRoot: laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
	}

	unsafeCases := []string{"", ".", "..", "../escape", "a/b", `a\b`, ".hidden"}
	for _, id := range unsafeCases {
		err := stager.Stage(context.Background(), StageSpec{
			ItemID:     id,
			LaneID:     id,
			RunID:      "run-unsafe",
			Backend:    "fixture",
			WorkingDir: WorktreeDir,
			BaseSHA:    trunk,
		})
		if err == nil {
			t.Fatalf("Stage(%q) succeeded, want refusal for unsafe path segment", id)
		}
		if !strings.Contains(err.Error(), "not a safe single path segment") {
			t.Fatalf("Stage(%q) error = %q, want reason naming unsafe single path segment", id, err.Error())
		}
	}

	poolShapedCases := []string{"lane-1", "express-1", "desk-doc"}
	for _, id := range poolShapedCases {
		err := stager.Stage(context.Background(), StageSpec{
			ItemID:     id,
			LaneID:     id,
			RunID:      "run-pool-shaped",
			Backend:    "fixture",
			WorkingDir: WorktreeDir,
			BaseSHA:    trunk,
		})
		if err == nil {
			t.Fatalf("Stage(%q) succeeded, want refusal for pool/desk lane shape", id)
		}
		if !strings.Contains(err.Error(), "pool or desk lane name") {
			t.Fatalf("Stage(%q) error = %q, want reason naming pool or desk lane name", id, err.Error())
		}
	}

	entries, err := os.ReadDir(laneRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("staging refused item ids created entries under laneRoot: %+v", entries)
	}
	if _, err := os.Stat(filepath.Join(filepath.Dir(laneRoot), "escape")); !os.IsNotExist(err) {
		t.Fatalf("staging ../escape created sibling directory")
	}
}

func TestItemDirectoryIdleEpochsOrdinaryAgentsWhyAndForkCheckout(t *testing.T) {
	t.Run("idle_lane_epochs", func(t *testing.T) {
		f := newRefusalLoopFixtureWithLanes(t, 1, "item-a")
		l := f.loop(t, &recordingSteps{}, stubStager{})
		l.cfg.Lanes = []string{"lane-1"}

		// Before the item worktree exists, idleLaneEpochs does not include item-a.
		if _, ok := l.idleLaneEpochs()["item-a"]; ok {
			t.Fatalf("idleLaneEpochs included non-existent item-a")
		}
		// Once the item worktree directory exists, idleLaneEpochs tracks it even
		// though item-a is not in l.cfg.Lanes.
		if err := os.MkdirAll(l.lanePath("item-a"), 0o755); err != nil {
			t.Fatal(err)
		}
		before := l.idleLaneEpochs()
		if _, ok := before["item-a"]; !ok {
			t.Fatalf("idleLaneEpochs missing existing item-a: %v", before)
		}
		l.noteLaneWrite("item-a")
		after := l.idleLaneEpochs()
		if after["item-a"] != before["item-a"]+1 {
			t.Fatalf("item-a epoch after write = %d, want %d", after["item-a"], before["item-a"]+1)
		}
	})

	t.Run("collect_active_ordinary_agents", func(t *testing.T) {
		f, l := passFixture(t, "item-a", "item-b")
		at := l.cfg.Now()
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := run.ClaimLease(context.Background(), tx, run.Request{RunID: "run-a", ItemID: "item-a", Backend: "fixture"}, "item-a", at); err != nil {
				return err
			}
			_, err := run.ClaimLease(context.Background(), tx, run.Request{RunID: "run-b", ItemID: "item-b", Backend: "fixture"}, "item-b", at)
			return err
		})
		var active map[string]string
		fixtureRead(t, f, func(r *store.Reader) error {
			var err error
			active, err = collectActiveOrdinaryAgents(r, at, nil, nil, map[string]bool{"item-b": true})
			return err
		})
		if active["run-a"] != "item-a" {
			t.Fatalf("ordinary item in item-a not counted: %v", active)
		}
		if _, ok := active["run-b"]; ok {
			t.Fatalf("express item in item-b was counted as ordinary agent: %v", active)
		}
	})

	t.Run("why_describes_item_lease", func(t *testing.T) {
		f, l := passFixture(t, "item-a")
		at := l.cfg.Now()
		fixtureWrite(t, f, func(tx *store.Tx) error {
			_, err := run.ClaimLease(context.Background(), tx, run.Request{RunID: "run-item-a-a1", ItemID: "item-a", Backend: "fixture", TimeoutMS: 60_000}, "item-a", at)
			return err
		})
		offered := []string{"lane-1"}
		var answer WhyAnswer
		fixtureRead(t, f, func(r *store.Reader) error {
			var err error
			answer, err = Why(r, Pass{Now: at.Format(time.RFC3339), AvailableLanes: &offered, StorePath: f.storePath}, l.cfg.Policy, nil)
			return err
		})
		if answer.Summary.LanesBusy != 1 || answer.Summary.LanesWorkers != 1 {
			t.Fatalf("why summary = %+v, want LanesBusy=1 LanesWorkers=1", answer.Summary)
		}
		if len(answer.Draining) != 0 {
			t.Fatalf("why reported item directory as draining: %+v", answer.Draining)
		}
	})

	t.Run("fork_checkout_under_item_id", func(t *testing.T) {
		f := newRefusalLoopFixtureWithLanes(t, 1, "item-a")
		l := f.loop(t, &recordingSteps{}, stubStager{})
		fork := checkoutForDelivery(f.root, "item-a", deliveryDecision{Kind: "fork", Fork: "pi"})
		wantRel := ".forks/lanes/item-a/pi"
		if fork.Path != wantRel {
			t.Fatalf("fork.Path = %q, want %q", fork.Path, wantRel)
		}
		laneDir := l.laneDirPath("item-a")
		if err := os.MkdirAll(laneDir, 0o755); err != nil {
			t.Fatal(err)
		}
		writeForkFile(t, filepath.Join(laneDir, TargetFile), fork.Checkout+"\n")
		writeForkFile(t, filepath.Join(laneDir, ItemFenceFile), fork.Path+"/\n")
		if got := l.laneTarget("item-a"); got != fork.Checkout {
			t.Fatalf("laneTarget(item-a) = %q, want %q", got, fork.Checkout)
		}
	})

	t.Run("recheck_lane_children_restores_item_dir", func(t *testing.T) {
		f := newRefusalLoopFixtureWithLanes(t, 1, "item-a")
		l := f.loop(t, &recordingSteps{}, stubStager{})
		l.cfg.Lanes = []string{"lane-1"}
		if err := l.holdLaneChildren(context.Background(), "item-a", []int{999999}, "run-item-a-a1"); err != nil {
			t.Fatalf("holdLaneChildren: %v", err)
		}
		if !l.hasLaneCondition("item-a", ReasonLaneChildren) {
			t.Fatalf("expected item-a to have ReasonLaneChildren condition")
		}
		if err := l.recheckLaneChildren(context.Background()); err != nil {
			t.Fatalf("recheckLaneChildren: %v", err)
		}
		if l.hasLaneCondition("item-a", ReasonLaneChildren) {
			t.Fatalf("expected recheckLaneChildren to restore item-a once no live children remain")
		}
	})
}
