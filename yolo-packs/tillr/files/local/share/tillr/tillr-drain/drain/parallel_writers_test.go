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

func TestParallelWritersClaimPinAndJoin(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Policy = &Policy{
		Seats: map[string]string{"build": "fixture", "verify": "fixture"},
		Backends: map[string]Backend{
			"fixture": {Adapter: "true", WorkingDir: WorktreeDir},
		},
	}

	wf := &Workflow{
		Name:    "parallel-test",
		Version: 1,
		Stages: []WorkflowStage{
			{ID: "work/p1", Kind: "agent", Seat: "build", Access: "write", WriteScope: WorkflowScope{Paths: []string{"docs/"}}},
			{ID: "work/p2", Kind: "agent", Seat: "build", Access: "write", WriteScope: WorkflowScope{Paths: []string{"tests/"}}},
			{ID: "work/join", Kind: "join", Access: "write"},
			{ID: "verify", Kind: "agent", Seat: "verify", Access: "read_only"},
		},
		Edges: []WorkflowEdge{
			{From: "work/p1", To: "work/join"},
			{From: "work/p2", To: "work/join"},
			{From: "work/join", To: "verify"},
		},
		Exit: &WorkflowExit{RequiresNodes: []string{"work/join", "verify"}, EvaluatorSlot: "verify"},
	}

	base := runGit(t, f.root, "rev-parse", TrunkRef)
	d := Decision{
		ItemID:    "item-a",
		LaneID:    "lane-1",
		Attempt:   1,
		Workflow:  wf,
		NodeIndex: 0,
		RunRequest: &run.Request{
			RunID:      "run-item-a-a1-n1",
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    "fixture",
			WorkingDir: WorktreeDir,
		},
	}
	stampDecisionRequest(d, d.RunRequest)
	if !strings.HasPrefix(d.RunRequest.WorkingDir, "nodes/") {
		t.Fatalf("expected node working_dir, got %q", d.RunRequest.WorkingDir)
	}

	// With 2 lanes, claimParallelWithWorks claims both work/p1 and work/p2.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if err := l.claimParallelWithWorks(tx, &d, nil, nil, nil); err != nil {
			return err
		}
		if err := startNode(tx, d); err != nil {
			return err
		}
		return appendEffect(tx, dispatchStartedKind, d.RunRequest.RunID, &pendingWork{Decision: d, BaseSHA: base}, time.Now().UTC())
	})
	if len(d.Parallel) != 1 || d.Parallel[0].RunRequest.NodeID != "work/p2" {
		t.Fatalf("expected work/p2 in d.Parallel, got %+v", d.Parallel)
	}

	// Stage both writers into their isolated node checkouts.
	ctx := context.Background()
	stager := &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
	}
	l.cfg.Stage = stager
	spec0 := StageSpec{Policy: l.cfg.Policy, Node: &wf.Stages[0], LaneID: "lane-1", ItemID: "item-a", RunID: d.RunRequest.RunID, Backend: "fixture", BaseSHA: base, WorkingDir: d.RunRequest.WorkingDir}
	if err := stager.Stage(ctx, spec0); err != nil {
		t.Fatalf("stage primary writer: %v", err)
	}
	d, err := l.stageParallel(ctx, d, base)
	if err != nil {
		t.Fatalf("stageParallel: %v", err)
	}

	wt1 := l.nodeTarget(d)
	wt2 := l.nodeTarget(d.Parallel[0])
	if wt1 == wt2 || !strings.Contains(wt1, "/nodes/") || !strings.Contains(wt2, "/nodes/") {
		t.Fatalf("expected distinct node worktrees, got %q and %q", wt1, wt2)
	}

	// Each writer commits in its own checkout without touching the primary worktree.
	if err := os.MkdirAll(filepath.Dir(filepath.Join(wt1, "docs", "a.md")), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(wt1, "docs", "a.md"), "# A\n")
	runGit(t, wt1, "add", ".")
	runGit(t, wt1, "commit", "-qm", "feat: add docs/a.md")
	sha1 := runGit(t, wt1, "rev-parse", "HEAD")

	if err := os.MkdirAll(filepath.Dir(filepath.Join(wt2, "tests", "b.txt")), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(wt2, "tests", "b.txt"), "b\n")
	runGit(t, wt2, "add", ".")
	runGit(t, wt2, "commit", "-qm", "feat: add tests/b.txt")
	sha2 := runGit(t, wt2, "rev-parse", "HEAD")

	if err := l.pinNodeCommit(ctx, "lane-1", "item-a", "work/p1", d.RunRequest.RunID, sha1); err != nil {
		t.Fatalf("pinNodeCommit p1: %v", err)
	}
	if err := l.pinNodeCommit(ctx, "lane-1", "item-a", "work/p2", d.Parallel[0].RunRequest.RunID, sha2); err != nil {
		t.Fatalf("pinNodeCommit p2: %v", err)
	}

	// Run join stage in-process.
	joinDec := Decision{
		ItemID:    "item-a",
		LaneID:    "lane-1",
		Attempt:   1,
		Workflow:  wf,
		NodeIndex: 2,
		RunRequest: &run.Request{
			RunID:      "run-item-a-a1-n3",
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    "command",
			WorkingDir: WorktreeDir,
		},
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return startNode(tx, joinDec)
	})
	rec, log, err := l.runJoinStage(ctx, joinDec, time.Now().UTC(), &LoopResult{})
	if err != nil {
		t.Fatalf("runJoinStage: %v", err)
	}
	if rec.Backend != "command" || !strings.Contains(log, "RESULT: done") {
		t.Fatalf("unexpected join rec/log: %+v / %q", rec, log)
	}

	primaryWT := l.laneTarget("lane-1")
	joinedHead := runGit(t, primaryWT, "rev-parse", "HEAD")
	if joinedHead == base {
		t.Fatal("expected primary worktree HEAD to advance after join")
	}
	if got := runGit(t, primaryWT, "show", joinedHead+":docs/a.md"); got != "# A" {
		t.Fatalf("docs/a.md = %q, want # A", got)
	}
	if got := runGit(t, primaryWT, "show", joinedHead+":tests/b.txt"); got != "b" {
		t.Fatalf("tests/b.txt = %q, want b", got)
	}
	// NodePinRefs remain while per-node checkouts are deleted.
	if got := runGit(t, f.root, "rev-parse", nodePinRef("item-a", "work/p1")); got != sha1 {
		t.Fatalf("NodePinRef p1 = %s, want %s", got, sha1)
	}
	if _, err := os.Stat(wt1); !os.IsNotExist(err) {
		t.Fatalf("expected node checkout %q to be deleted after join", wt1)
	}

	// Verify evaluatorCandidate skips kind: "join".
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE node_runs SET outcome='passed' WHERE run_id=?`, joinDec.RunRequest.RunID)
		return err
	})
	verifyDec := Decision{
		ItemID:    "item-a",
		LaneID:    "lane-1",
		Attempt:   1,
		Workflow:  wf,
		NodeIndex: 3,
		RunRequest: &run.Request{
			RunID:      "run-item-a-a1-n4",
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    "fixture",
			WorkingDir: WorktreeDir,
		},
	}
	c, err := l.evaluatorCandidate(ctx, verifyDec)
	if err != nil {
		t.Fatalf("evaluatorCandidate: %v", err)
	}
	for _, w := range c.Writers {
		if w.NodeID == "work/join" {
			t.Fatalf("evaluatorCandidate should skip kind: join, got %+v", c.Writers)
		}
	}
}

func TestJoinConflictSplicesReconcileAndEscalatesRepeat(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Policy = &Policy{
		Seats: map[string]string{"build": "fixture", "verify": "fixture"},
		Backends: map[string]Backend{
			"fixture": {Adapter: "true", WorkingDir: WorktreeDir},
		},
	}

	wf := &Workflow{
		Name:    "parallel-conflict",
		Version: 1,
		Stages: []WorkflowStage{
			{ID: "work/p1", Kind: "agent", Seat: "build", Access: "write", WriteScope: WorkflowScope{Paths: []string{"shared.txt"}}},
			{ID: "work/p2", Kind: "agent", Seat: "build", Access: "write", WriteScope: WorkflowScope{Paths: []string{"shared.txt", "extra.txt"}}},
			{ID: "work/join", Kind: "join", Access: "write"},
			{ID: "verify", Kind: "agent", Seat: "verify", Access: "read_only"},
		},
		Edges: []WorkflowEdge{
			{From: "work/p1", To: "work/join"},
			{From: "work/p2", To: "work/join"},
			{From: "work/join", To: "verify"},
		},
		Exit: &WorkflowExit{RequiresNodes: []string{"work/join", "verify"}, EvaluatorSlot: "verify"},
	}

	base := runGit(t, f.root, "rev-parse", TrunkRef)
	ctx := context.Background()

	// Create two conflicting commits and pin them at NodePinRef.
	wt1 := nodeWorktreePath(f.laneRoot, "item-a", "work/p1")
	runGit(t, f.root, "worktree", "add", "-f", "-B", "tillr/test-nodes/item-a/p1", wt1, base)
	writeForkFile(t, filepath.Join(wt1, "shared.txt"), "from p1\n")
	runGit(t, wt1, "add", ".")
	runGit(t, wt1, "commit", "-qm", "feat: p1")
	sha1 := runGit(t, wt1, "rev-parse", "HEAD")
	if err := l.pinNodeCommit(ctx, "lane-1", "item-a", "work/p1", "run-p1", sha1); err != nil {
		t.Fatal(err)
	}

	wt2 := nodeWorktreePath(f.laneRoot, "item-a", "work/p2")
	runGit(t, f.root, "worktree", "add", "-f", "-B", "tillr/test-nodes/item-a/p2", wt2, base)
	writeForkFile(t, filepath.Join(wt2, "shared.txt"), "from p2\n")
	runGit(t, wt2, "add", ".")
	runGit(t, wt2, "commit", "-qm", "feat: p2")
	sha2 := runGit(t, wt2, "rev-parse", "HEAD")
	if err := l.pinNodeCommit(ctx, "lane-1", "item-a", "work/p2", "run-p2", sha2); err != nil {
		t.Fatal(err)
	}

	joinDec := Decision{
		ItemID:    "item-a",
		LaneID:    "lane-1",
		Attempt:   1,
		Workflow:  wf,
		NodeIndex: 2,
		RunRequest: &run.Request{
			RunID:      "run-item-a-a1-n3",
			ItemID:     "item-a",
			LaneID:     "lane-1",
			Backend:    "command",
			WorkingDir: WorktreeDir,
		},
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return startNode(tx, joinDec)
	})

	_, log, err := l.runJoinStage(ctx, joinDec, time.Now().UTC(), &LoopResult{})
	if err != nil {
		t.Fatalf("runJoinStage conflict: %v", err)
	}
	if !strings.Contains(log, "conflict across 2 writers on shared.txt") {
		t.Fatalf("unexpected conflict log: %q", log)
	}

	// Verify node_runs.outcome = 'handled-fail' and graph_revisions spliced work/join-reconcile.
	var outcome, target string
	var splicedWF *Workflow
	if err := l.readStore(ctx, func(r *store.Reader) error {
		if err := r.QueryRow(`SELECT outcome FROM node_runs WHERE run_id=?`, joinDec.RunRequest.RunID).Scan(&outcome); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT target FROM graph_revisions WHERE item_id='item-a' ORDER BY rowid DESC LIMIT 1`).Scan(&target); err != nil {
			return err
		}
		works, err := readPending(r)
		if err != nil {
			return err
		}
		if w := works["item-a"]; w != nil {
			splicedWF = w.Decision.Workflow
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if outcome != "handled-fail" || target != "work/join-reconcile" || splicedWF == nil {
		t.Fatalf("outcome=%q target=%q splicedWF=%v", outcome, target, splicedWF)
	}

	// Identical repeat conflict escalates to design (item.conflict).
	joinDec2 := joinDec
	joinDec2.Attempt = 2
	joinDec2.RunRequest = &run.Request{RunID: "run-item-a-a2-n3-repeat", ItemID: "item-a", LaneID: "lane-1", Backend: "command", WorkingDir: WorktreeDir}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return startNode(tx, joinDec2)
	})
	if _, _, err := l.runJoinStage(ctx, joinDec2, time.Now().UTC(), &LoopResult{}); err != nil {
		t.Fatalf("repeat runJoinStage conflict: %v", err)
	}
	var conflictCount int
	if err := l.readStore(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND json_extract(payload,'$.conflict_id')=?`, "conflict-"+joinDec2.RunRequest.RunID).Scan(&conflictCount)
	}); err != nil {
		t.Fatal(err)
	}
	if conflictCount != 1 {
		t.Fatalf("expected 1 design conflict on identical repeat, got %d", conflictCount)
	}
}
