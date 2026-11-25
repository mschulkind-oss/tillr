package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestGateRefusalOnConflictRepairStagesBuildOnCandidateCommit(t *testing.T) {
	ctx := context.Background()
	f, l, _ := plannedFixture(t)
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.Stage = &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      l.cfg.Now,
	}

	// Load planned-build@v1 workflow just as the loop does for attempt 1 (do-it-now on plan).
	wf, err := loadWorkflow(f.root, "planned-build@v1", l.cfg.Policy)
	if err != nil {
		t.Fatal(err)
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)

	// Record the original plan do-it-now run (attempt 2, node 1) in the store.
	origRunID := "run-item-a-a2-n1"
	origDecision := Decision{
		ItemID: "item-a", LaneID: "lane-1", Attempt: 2,
		Action: ActionDispatch, Reason: ReasonReady, Seat: "plan",
		SelectedWorkflow: "planned-build@v1", Workflow: wf, NodeIndex: 0, NodeBaseSHA: base,
		RunRequest: &run.Request{ItemID: "item-a", LaneID: "lane-1", RunID: origRunID, Backend: "fixture", WorkingDir: WorktreeDir},
	}
	// Record the conflict-repair run (-cfix-cfix) exactly as conflictFix does:
	// Workflow is nil, Seat is inherited from the original plan run ("plan").
	cfixRunID := origRunID + ConflictFixSuffix + ConflictFixSuffix
	cfixDecision := Decision{
		ItemID: "item-a", LaneID: "lane-1", Attempt: 4,
		Action: ActionDispatch, Reason: ReasonConflictFix, Seat: "plan",
		SelectedWorkflow: "planned-build@v1",
		RunRequest:       &run.Request{ItemID: "item-a", LaneID: "lane-1", RunID: cfixRunID, Backend: "fixture", WorkingDir: WorktreeDir},
	}

	// Materialize lane-1 with the conflict-repair candidate commit, and advance trunk to a
	// newer proved commit so the candidate commit is clearly distinct from trunk.
	lane1Worktree := l.laneTarget("lane-1")
	runGit(t, lane1Worktree, "reset", "--hard", "-q", base)
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), BaseSHAFile), base+"\n")
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), ItemBaseSHAFile), base+"\n")
	if err := os.MkdirAll(filepath.Join(lane1Worktree, "tools/item-a"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane1Worktree, "tools/item-a/work.txt"), "conflict-repaired work\n")
	runGit(t, lane1Worktree, "add", "tools/item-a/work.txt")
	runGit(t, lane1Worktree, "commit", "-qm", "conflict-repaired work")
	candHead := runGit(t, lane1Worktree, "rev-parse", "HEAD")

	// Advance trunk so proved trunk != base and != candHead.
	provedTrunk := conflictTestCommit(t, f.root, "other.txt", "trunk moved\n", "trunk move")

	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, rec := range []struct {
			id string
			d  Decision
		}{
			{origRunID, origDecision},
			{cfixRunID, cfixDecision},
		} {
			if err := appendEffect(tx, dispatchStartedKind, rec.id, &pendingWork{Decision: rec.d, BaseSHA: base, Done: true}, l.cfg.Now()); err != nil {
				return err
			}
			r := run.Record{RunID: rec.id, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture", State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-25T07:30:00Z", FinishedAt: "2026-11-25T07:35:00Z"}
			if err := applyTerminal(tx, r); err != nil {
				return err
			}
		}
		// plan passed via do-it-now; work and verify were skipped.
		for _, nr := range []struct {
			node, runID, outcome string
			quick                int
		}{
			{"plan", origRunID, "passed", 1},
			{"work", "run-item-a-a2-n2", "skipped", 0},
			{"verify", "run-item-a-a2-n3", "skipped", 0},
		} {
			if _, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome,quick_way) VALUES('item-a',2,?,?,?,?)`, nr.node, nr.runID, nr.outcome, nr.quick); err != nil {
				return err
			}
		}
		return nil
	})

	// Write a failing gate verdict for the -cfix-cfix run.
	const refusalWords = "test binary was ended by SIGQUIT"
	if err := os.MkdirAll(RunDir(f.root, cfixRunID), 0755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(RunDir(f.root, cfixRunID), "go-test.log")
	writeForkFile(t, logPath, refusalWords+"\n")
	writeForkFile(t, filepath.Join(RunDir(f.root, cfixRunID), "gate.verdict.json"), mustJSON(map[string]any{
		"payload": map[string]any{
			"checks": []map[string]any{
				{"check_id": "go-test", "result": "fail", "log_ref": logPath},
			},
		},
	}))

	// Record the gate refusal on the -cfix-cfix candidate.
	cand := Candidate{
		ItemID: "item-a", LaneID: "lane-1", RunID: cfixRunID,
		Worktree: lane1Worktree, BaseSHA: base, HeadSHA: candHead,
		RefusalDetail: refusalWords,
		Request:       cfixDecision.RunRequest,
	}
	if err := l.recordRefusal(ctx, cand, RefusalGate); err != nil {
		t.Fatalf("recordRefusal: %v", err)
	}
	if stop := l.abandon(ctx, "lane-1", cfixRunID, candHead); stop != "" {
		t.Fatalf("abandon: %s", stop)
	}
	if err := l.markExamined(ctx, cfixDecision); err != nil {
		t.Fatalf("markExamined: %v", err)
	}

	// Offer only lane-2 (cold lane, session missing/unavailable) to match the observed incident.
	l.cfg.Lanes = []string{"lane-2"}
	var p plannedApplication
	for _, candPlan := range mustPlan(t, l) {
		if candPlan.decision.ItemID == "item-a" {
			p = candPlan
			break
		}
	}
	if p.decision.ItemID != "item-a" {
		t.Fatal("missing plan decision for item-a")
	}
	if p.decision.Action != ActionDispatch || p.decision.Reason != ReasonReturned {
		t.Fatalf("decision = %s/%s, want dispatch/%s", p.decision.Action, p.decision.Reason, ReasonReturned)
	}
	if p.decision.Workflow == nil || p.decision.NodeIndex < 0 || p.decision.NodeIndex >= len(p.decision.Workflow.Stages) {
		t.Fatalf("missing workflow on returned gate decision: %+v", p.decision)
	}
	stage := p.decision.Workflow.Stages[p.decision.NodeIndex]
	if stage.ID != "work" || stage.Seat != "build" {
		t.Fatalf("staged node = %s (seat %s), want work (seat build); must not restart at plan", stage.ID, stage.Seat)
	}
	if p.decision.NodeBaseSHA != candHead {
		t.Fatalf("planned NodeBaseSHA = %s, want refused candidate %s (not proved trunk %s)", p.decision.NodeBaseSHA, candHead, provedTrunk)
	}

	// Apply and stage on lane-2.
	d := applyReturnedTurn(t, l, p, provedTrunk)

	// 1. Staged worktree HEAD must be the refused candidate commit, with the item's work intact.
	lane2Worktree := l.laneTarget("lane-2")
	if got := runGit(t, lane2Worktree, "rev-parse", "HEAD"); got != candHead {
		t.Fatalf("lane-2 HEAD = %s, want refused candidate %s (proved trunk %s)", got, candHead, provedTrunk)
	}
	body, err := os.ReadFile(filepath.Join(lane2Worktree, "tools/item-a/work.txt"))
	if err != nil || strings.TrimSpace(string(body)) != "conflict-repaired work" {
		t.Fatalf("candidate work missing on lane-2: %v %q", err, string(body))
	}
	// Item base stays the original base so the whole item still diffs against its base.
	if got := l.laneFile("lane-2", BaseSHAFile); got != base {
		t.Fatalf("lane-2 base_sha = %s, want %s", got, base)
	}

	// 2. Brief must contain the gate's refusal word for word.
	brief, err := os.ReadFile(filepath.Join(l.laneDirPath("lane-2"), BriefFile))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(brief), refusalWords) || !strings.Contains(string(brief), "go-test") {
		t.Fatalf("brief missing gate refusal words:\n%s", string(brief))
	}

	// 3. drain.started must name the candidate commit it was staged on.
	fixtureRead(t, f, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, dispatchStartedKind, d.RunRequest.RunID).Scan(&raw); err != nil {
			return err
		}
		var started pendingWork
		if err := json.Unmarshal(raw, &started); err != nil {
			return err
		}
		if started.Decision.NodeBaseSHA != candHead {
			t.Errorf("drain.started decision.node_base_sha = %s, want refused candidate %s", started.Decision.NodeBaseSHA, candHead)
		}
		return nil
	})
}

func TestGateRefusalOnExpandedPiecesStagesWholeBuildOnCandidate(t *testing.T) {
	ctx := context.Background()
	f, l, _ := plannedFixture(t)
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.Stage = &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      l.cfg.Now,
	}

	base := runGit(t, f.root, "rev-parse", TrunkRef)
	wf := &Workflow{
		Name: "planned-build", Version: 1,
		Stages: []WorkflowStage{
			{ID: "plan", Kind: "agent", Seat: "plan", Access: "read_only"},
			{ID: "work/piece-one", Parent: "work", Kind: "agent", Seat: "edit", Access: "write", WriteScope: WorkflowScope{Paths: []string{"tools/item-a/one.txt"}}, Prompt: "write one"},
			{ID: "verify", Kind: "agent", Seat: "verify", Access: "read_only"},
		},
		Edges: []WorkflowEdge{
			{From: "plan", To: "work/piece-one"},
			{From: "work/piece-one", To: "verify"},
		},
		Exit: &WorkflowExit{RequiresNodes: []string{"plan", "work/piece-one", "verify"}, EvaluatorSlot: "verify"},
	}

	lane1Worktree := l.laneTarget("lane-1")
	runGit(t, lane1Worktree, "reset", "--hard", "-q", base)
	writeForkFile(t, filepath.Join(l.laneDirPath("lane-1"), BaseSHAFile), base+"\n")
	if err := os.MkdirAll(filepath.Join(lane1Worktree, "tools/item-a"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(lane1Worktree, "tools/item-a/one.txt"), "piece one\n")
	runGit(t, lane1Worktree, "add", "tools/item-a/one.txt")
	runGit(t, lane1Worktree, "commit", "-qm", "piece one")
	candHead := runGit(t, lane1Worktree, "rev-parse", "HEAD")

	writerRunID := "run-item-a-a1-n2"
	verifyRunID := "run-item-a-a1-n3"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		dWriter := Decision{
			ItemID: "item-a", LaneID: "lane-1", Attempt: 1,
			Action: ActionDispatch, Reason: ReasonReady, Seat: "edit",
			SelectedWorkflow: "planned-build@v1", Workflow: wf, NodeIndex: 1, NodeBaseSHA: base,
			RunRequest: &run.Request{ItemID: "item-a", LaneID: "lane-1", RunID: writerRunID, Backend: "fixture", WorkingDir: WorktreeDir},
		}
		dVerify := Decision{
			ItemID: "item-a", LaneID: "lane-1", Attempt: 1,
			Action: ActionDispatch, Reason: "workflow-next-node", Seat: "verify",
			SelectedWorkflow: "planned-build@v1", Workflow: wf, NodeIndex: 2, NodeBaseSHA: candHead,
			RunRequest: &run.Request{ItemID: "item-a", LaneID: "lane-1", RunID: verifyRunID, Backend: "reviewer", WorkingDir: WorktreeDir},
		}
		for _, rec := range []struct {
			id string
			d  Decision
		}{{writerRunID, dWriter}, {verifyRunID, dVerify}} {
			if err := appendEffect(tx, dispatchStartedKind, rec.id, &pendingWork{Decision: rec.d, BaseSHA: base, Done: true}, l.cfg.Now()); err != nil {
				return err
			}
			r := run.Record{RunID: rec.id, ItemID: "item-a", LaneID: "lane-1", Backend: rec.d.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-25T07:30:00Z", FinishedAt: "2026-11-25T07:35:00Z"}
			if err := applyTerminal(tx, r); err != nil {
				return err
			}
		}
		for _, nr := range []struct{ node, runID string }{{"plan", "run-item-a-a1-n1"}, {"work/piece-one", writerRunID}, {"verify", verifyRunID}} {
			if _, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES('item-a',1,?,?,'passed')`, nr.node, nr.runID); err != nil {
				return err
			}
		}
		return nil
	})

	// Simulate lane released for replay during gate failure settlement, so captureWorkerSession returns nil.
	const refusalWords = "pack-sync check failed on tools/item-a"
	cand := Candidate{
		ItemID: "item-a", LaneID: "lane-1", RunID: verifyRunID,
		Worktree: lane1Worktree, BaseSHA: base, HeadSHA: candHead,
		RefusalDetail: refusalWords,
		Writers:       []JudgeWriter{{NodeID: "work/piece-one", RunID: writerRunID, Backend: "fixture"}},
		replayRelease: &replayLaneRelease{released: true},
	}
	if err := l.recordRefusal(ctx, cand, RefusalGate); err != nil {
		t.Fatalf("recordRefusal: %v", err)
	}
	if err := l.markExamined(ctx, Decision{ItemID: "item-a", LaneID: "lane-1", RunRequest: &run.Request{RunID: verifyRunID}}); err != nil {
		t.Fatalf("markExamined: %v", err)
	}

	l.cfg.Lanes = []string{"lane-2"}
	var p plannedApplication
	for _, candPlan := range mustPlan(t, l) {
		if candPlan.decision.ItemID == "item-a" {
			p = candPlan
			break
		}
	}
	if p.decision.Workflow == nil {
		t.Fatalf("expected retained workflow on gate refusal: %+v", p.decision)
	}
	stage := p.decision.Workflow.Stages[p.decision.NodeIndex]
	if stage.ID != verifyReturnWorkStage || stage.Seat != "build" || stage.WriteScope.Paths != nil {
		t.Fatalf("staged node = %+v, want unscoped build stage %q", stage, verifyReturnWorkStage)
	}
	d := applyReturnedTurn(t, l, p, base)
	if got := runGit(t, l.laneTarget("lane-2"), "rev-parse", "HEAD"); got != candHead {
		t.Fatalf("lane-2 HEAD = %s, want candidate %s", got, candHead)
	}
	if d.NodeBaseSHA != candHead {
		t.Fatalf("NodeBaseSHA = %s, want candidate %s", d.NodeBaseSHA, candHead)
	}
}

func TestGateRefusalNonWorkflowSessionMissingStagesOnCandidate(t *testing.T) {
	f, l, _, base := returnedSessionFixture(t, RefusalGate)
	target := l.laneTarget("lane-1")
	candHead := runGit(t, target, "rev-parse", "HEAD")
	if candHead == base {
		t.Fatal("fixture did not create a candidate commit")
	}
	if err := os.Remove(filepath.Join(l.laneDirPath("lane-1"), SessionIDFile)); err != nil {
		t.Fatal(err)
	}
	p := mustPlan(t, l)[0]
	if p.decision.WorkerSessionReason != "session-missing" || p.decision.RunRequest.SessionTurn {
		t.Fatalf("expected fresh session-missing return, got %+v", p.decision)
	}
	if p.decision.NodeBaseSHA != candHead {
		t.Fatalf("planned NodeBaseSHA = %s, want refused candidate %s", p.decision.NodeBaseSHA, candHead)
	}
	d := applyReturnedTurn(t, l, p, base)
	if got := runGit(t, target, "rev-parse", "HEAD"); got != candHead {
		t.Fatalf("lane-1 HEAD = %s, want refused candidate %s (base %s)", got, candHead, base)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind=? AND object_id=? ORDER BY seq DESC LIMIT 1`, dispatchStartedKind, d.RunRequest.RunID).Scan(&raw); err != nil {
			return err
		}
		var started pendingWork
		if err := json.Unmarshal(raw, &started); err != nil {
			return err
		}
		if started.Decision.NodeBaseSHA != candHead {
			t.Errorf("drain.started decision.node_base_sha = %s, want refused candidate %s", started.Decision.NodeBaseSHA, candHead)
		}
		return nil
	})
}
