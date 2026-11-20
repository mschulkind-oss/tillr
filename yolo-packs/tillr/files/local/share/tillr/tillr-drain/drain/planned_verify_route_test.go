package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type plannedVerifySteps struct {
	*workflowSteps
	blocked  bool
	plans    int
	verifies int
}

func (s *plannedVerifySteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	brief, err := os.ReadFile(filepath.Join(s.f.laneRoot, req.LaneID, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	report := "RESULT: done\n"
	switch req.NodeID {
	case "plan":
		s.plans++
		if !strings.Contains(string(brief), "cheap models") || !strings.Contains(string(brief), "QUESTION:") {
			s.t.Errorf("plan brief missing guidance or question: %s", brief)
		}
		if s.blocked {
			report = "OBSTACLE: owner\nQUESTION: Which design should be used?\nRESULT: blocked\n"
		} else {
			if s.plans > 1 && !strings.Contains(string(brief), "c2: uncovered:") {
				s.t.Errorf("returned brief lacks c2: %s", brief)
			}
			proposalArtifact(s.t, s.f.root, req.RunID, pieceProposal([]map[string]any{piece("one", "build", "write"), piece("two", "edit", "write")}))
		}
	case "work/piece-one", "work/piece-two":
		if err := os.MkdirAll(filepath.Join(target, "tools/item-a"), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-a", strings.TrimPrefix(req.NodeID, "work/piece-")+".txt"), req.RunID)
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "build piece")
	case "verify":
		s.verifies++
		for _, want := range []string{"uncovered:", "node:<id>", "candidate.diff", "Node work/piece-one:", "Node work/piece-two:"} {
			if !strings.Contains(string(brief), want) {
				s.t.Errorf("verify brief lacks %q: %s", want, brief)
			}
		}
		for _, node := range []string{"work/piece-one", "work/piece-two"} {
			for i := len(s.runs) - 1; i >= 0; i-- {
				r := s.runs[i]
				if r.NodeID == node {
					if !strings.Contains(string(brief), nodeOutputsDir(s.f.root, r.RunID)) {
						s.t.Errorf("missing output directory for %s", node)
					}
					break
				}
			}
		}
		head := runGit(s.t, target, "rev-parse", "HEAD")
		reason := "uncovered: no piece"
		if s.verifies > 1 {
			reason = "uncovered: differently worded"
		}
		v := item.JudgeOutput{EvaluatedHead: head, EvaluatedTree: runGit(s.t, target, "rev-parse", head+"^{tree}"), Outcome: "fail", Verdicts: []item.JudgeEntry{
			{CriterionID: "c1", CriterionHash: item.CriterionHash("the first criterion of item-a"), Verdict: "done", ClaimClass: item.ClaimVerification, Reason: "covered", Evidence: []string{"node:work/piece-one"}},
			{CriterionID: "c2", CriterionHash: item.CriterionHash("second criterion"), Verdict: "not-done", ClaimClass: item.ClaimVerification, Reason: reason, Evidence: []string{"candidate.diff"}},
		}}
		raw, _ := json.Marshal(v)
		writeForkFile(s.t, filepath.Join(run.DurableDir(filepath.Join(s.f.laneRoot, req.LaneID), req.RunID), "eval.output"), string(raw))
	default:
		return run.Record{}, "", fmt.Errorf("unexpected node %s", req.NodeID)
	}
	path := filepath.Join(run.DurableDir(filepath.Join(s.f.laneRoot, req.LaneID), req.RunID), "report.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, path, report)
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func TestPlannedVerifyReturnsAndRepeats(t *testing.T) {
	f, l, base := plannedFixture(t)
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config/tillr/plan-guidance.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/plan-guidance.md"), string(raw))
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_criteria(item_id,criterion_id,text,state,claim_class) VALUES('item-a','c2','second criterion','not-done','verification-only')`)
		return err
	})
	steps := &plannedVerifySteps{workflowSteps: base}
	l.cfg.Policy.Seats["design"] = "fixture"
	l.cfg.Steps = steps
	result := &LoopResult{}
	for i := 0; i < 25 && steps.verifies < 1; i++ {
		graphPass(t, l, result)
	}
	if steps.verifies != 1 {
		t.Fatalf("first verifier did not run: %+v", steps.runs)
	}
	var found bool
	for _, p := range mustPlan(t, l) {
		d := p.decision
		if d.ItemID == "item-a" && d.Action == ActionDispatch && d.Reason == ReasonReturned && d.Workflow.Stages[d.NodeIndex].ID == "plan" {
			found = true
		}
	}
	if !found {
		t.Fatalf("no returned plan: %+v", mustPlan(t, l))
	}
	for i := 0; i < 25 && steps.verifies < 2; i++ {
		graphPass(t, l, result)
	}
	if steps.verifies != 2 {
		t.Fatalf("second verifier did not run: %+v", steps.runs)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id='item-a' AND json_extract(payload,'$.waiting_on')='design'`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("design conflicts: %d", n)
		}
		return nil
	})
	for _, p := range mustPlan(t, l) {
		if p.decision.ItemID == "item-a" && p.decision.Action == ActionDispatch && p.decision.Seat == "plan" {
			t.Fatal("identical repeat dispatched a third plan")
		}
	}
	if steps.plans != 2 || steps.gates != 0 {
		t.Fatalf("plans=%d gates=%d", steps.plans, steps.gates)
	}
}

func TestPlannedBlockedRoutesDesign(t *testing.T) {
	f, l, base := plannedFixture(t)
	writeForkFile(t, filepath.Join(f.root, "config/tillr/plan-guidance.md"), "cheap models")
	steps := &plannedVerifySteps{workflowSteps: base, blocked: true}
	l.cfg.Steps = steps
	result := &LoopResult{}
	for i := 0; i < 5 && steps.plans < 1; i++ {
		graphPass(t, l, result)
	}
	graphPass(t, l, result)
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM graph_revisions`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("revisions=%d", n)
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.conflict' AND object_id='item-a' AND json_extract(payload,'$.waiting_on')='design'`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("design conflicts=%d", n)
		}
		conflicts, err := item.ReadConflicts(r)
		if err != nil {
			return err
		}
		found := false
		for _, c := range conflicts {
			if c.ItemID == "item-a" && c.WaitingOn == "design" && strings.Contains(c.Text, "Which design should be used?") {
				found = true
			}
		}
		if !found {
			t.Errorf("design conflict missing planner's question: %+v", conflicts)
		}
		return nil
	})
}

type plannedRequestDesignSteps struct {
	*workflowSteps
	reqID      string
	words      string
	addBlock   bool
	order      []string
	plans      int
	designs    int
	workNodeID string
	designRun  string
}

func (s *plannedRequestDesignSteps) Gate(_ context.Context, _ Candidate) (GateOutcome, error) {
	s.gates++
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *plannedRequestDesignSteps) Land(_ context.Context, c Candidate) (LandOutcome, error) {
	runGit(s.t, s.f.root, "merge", "--squash", c.HeadSHA)
	runGit(s.t, s.f.root, "commit", "-qm", "land "+c.RunID)
	fixtureLanding(s.t, s.f.storePath, c)
	return LandOutcome{Result: LandLanded}, nil
}

func (s *plannedRequestDesignSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.runs = append(s.runs, req)
	laneDir := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(laneDir, WorktreeDir)
	briefBytes, err := os.ReadFile(filepath.Join(laneDir, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	brief := string(briefBytes)
	report := "RESULT: done\n"
	switch {
	case req.NodeID == "plan":
		s.order = append(s.order, "plan")
		s.plans++
		if s.plans == 1 {
			for _, want := range []string{
				"amend or overturn a decision recorded in a design document's ledger",
				"OBSTACLE: definition",
				s.words,
			} {
				if !strings.Contains(brief, want) {
					s.t.Errorf("plan brief missing %q: %s", want, brief)
				}
			}
			report = "OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: docs/design/example.md D-EX1 records batch limit is 10; request overturns it to 25\nRESULT: blocked\n"
		} else {
			doc, err := os.ReadFile(filepath.Join(target, "docs/design/example.md"))
			if err != nil {
				return run.Record{}, "", err
			}
			if !strings.Contains(string(doc), "25") {
				s.t.Errorf("returned plan worktree docs/design/example.md does not contain 25: %s", doc)
			}
			p := map[string]any{
				"id":          "limit",
				"seat":        "build",
				"access":      "write",
				"prompt":      "raise batch limit to 25",
				"write_scope": []string{"tools/item-req/"},
			}
			proposalArtifact(s.t, s.f.root, req.RunID, pieceProposal([]map[string]any{p}))
		}
	case req.NodeID == "":
		s.order = append(s.order, "design")
		s.designs++
		s.designRun = req.RunID
		fence, err := os.ReadFile(filepath.Join(laneDir, FenceFile))
		if err != nil {
			return run.Record{}, "", err
		}
		if string(fence) != "docs/\n" {
			s.t.Errorf("design fence = %q, want %q", string(fence), "docs/\n")
		}
		rederived, err := rederiveFence(ctx, ResumeConfig{Root: s.f.root, Store: s.f.storePath, LaneRoot: s.f.laneRoot}, resumeRun{ItemID: req.ItemID, LaneID: req.LaneID}, laneDir, req.RunID)
		if err != nil {
			return run.Record{}, "", err
		}
		if !reflect.DeepEqual(rederived, []string{"docs/"}) {
			s.t.Errorf("rederiveFence = %v, want [docs/]", rederived)
		}
		if strings.Contains(brief, "Amend only that item's block") {
			s.t.Errorf("design brief unexpectedly contains %q: %s", "Amend only that item's block", brief)
		}
		for _, want := range []string{"docs/", "conflict.json", s.words} {
			if !strings.Contains(brief, want) {
				s.t.Errorf("design brief missing %q: %s", want, brief)
			}
		}
		content := "# Example Design\n\n## Decision Ledger\n\n| ID | Decision |\n|---|---|\n| D-EX1 | batch limit is 25 |\n"
		if s.addBlock {
			content += "\n" + "<!-- " + "tillr: item id=\"item-x\" -->\n"
		}
		if err := os.MkdirAll(filepath.Join(target, "docs/design"), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "docs/design/example.md"), content)
		runGit(s.t, target, "add", "docs/design/example.md")
		runGit(s.t, target, "commit", "-qm", "update D-EX1 to 25")
	case strings.HasPrefix(req.NodeID, "work/piece-"):
		s.order = append(s.order, req.NodeID)
		s.workNodeID = req.NodeID
		if err := os.MkdirAll(filepath.Join(target, "tools/item-req"), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/item-req/limit.go"), "package itemreq\n\nconst BatchLimit = 25\n")
		runGit(s.t, target, "add", "tools/item-req/limit.go")
		runGit(s.t, target, "commit", "-qm", "raise batch limit to 25")
	case req.NodeID == "verify":
		s.order = append(s.order, "verify")
		head := runGit(s.t, target, "rev-parse", "HEAD")
		tree := runGit(s.t, target, "rev-parse", head+"^{tree}")
		var verdicts []item.JudgeEntry
		fixtureRead(s.t, s.f, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT criterion_id, text, claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, req.ItemID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var cid, text, cc string
				if err := rows.Scan(&cid, &text, &cc); err != nil {
					return err
				}
				verdicts = append(verdicts, item.JudgeEntry{
					CriterionID:   cid,
					CriterionHash: item.CriterionHash(text),
					Verdict:       "done",
					ClaimClass:    cc,
					Reason:        "covered",
					Evidence:      []string{"node:" + s.workNodeID},
				})
			}
			return rows.Err()
		})
		if len(verdicts) == 0 {
			s.t.Fatalf("no item_criteria rows for %s", req.ItemID)
		}
		v := item.JudgeOutput{EvaluatedHead: head, EvaluatedTree: tree, Outcome: "pass", Verdicts: verdicts}
		raw, err := json.Marshal(v)
		if err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(run.DurableDir(laneDir, req.RunID), "eval.output"), string(raw))
	default:
		return run.Record{}, "", fmt.Errorf("unexpected node %s", req.NodeID)
	}
	path := filepath.Join(run.DurableDir(laneDir, req.RunID), "report.md")
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		return run.Record{}, "", err
	}
	writeForkFile(s.t, path, report)
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
	st, err := store.Open(ctx, s.f.storePath, true)
	if err != nil {
		return rec, report, err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, err
}

func plannedRequestDesignFixture(t *testing.T, addBlock bool) (refusalLoopFixture, *Loop, *plannedRequestDesignSteps, string) {
	t.Helper()
	f, l, base := plannedFixture(t)
	l.cfg.Policy.Seats["design"] = "designer"
	l.cfg.Policy.Backends["designer"] = Backend{Adapter: "fixture", Model: "design-model"}
	raw, err := os.ReadFile(filepath.Join("..", "..", "..", "config/tillr/plan-guidance.md"))
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/plan-guidance.md"), string(raw))
	if err := os.MkdirAll(filepath.Join(f.root, "docs/design"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "docs/design/example.md"), "# Example Design\n\n## Decision Ledger\n\n| ID | Decision |\n|---|---|\n| D-EX1 | batch limit is 10 |\n")
	runGit(t, f.root, "add", "docs/design/example.md")
	runGit(t, f.root, "commit", "-qm", "design doc")
	preDesign := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", GreenRef, preDesign)

	words := []byte("Overturn D-EX1 in docs/design/example.md and raise the batch limit to 25 in tools/item-req/.\n")
	var reqID string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id='item-a'`); err != nil {
			return err
		}
		env, err := item.FileRequest(context.Background(), tx, item.RequestFiling{
			Words:       words,
			Title:       "Raise batch limit to 25",
			Seat:        "build",
			Workflow:    "planned-build@v1",
			Paths:       []string{"tools/item-req/"},
			Destination: item.Destination{To: "first"},
			At:          time.Date(2026, 11, 15, 0, 0, 0, 0, time.UTC),
			By:          "owner",
			Via:         "cli",
		})
		reqID = env.ID
		return err
	})
	fixtureRead(t, f, func(r *store.Reader) error {
		var bodyRef string
		if err := r.QueryRow(`SELECT COALESCE(body_ref,'') FROM items WHERE item_id=?`, reqID).Scan(&bodyRef); err != nil {
			return err
		}
		if bodyRef != "" {
			t.Fatalf("body_ref=%q, want empty", bodyRef)
		}
		gotWords, err := item.RequestWords(r, reqID)
		if err != nil {
			return err
		}
		if string(gotWords) != string(words) {
			t.Fatalf("request_words=%q, want %q", gotWords, words)
		}
		return nil
	})
	steps := &plannedRequestDesignSteps{
		workflowSteps: base,
		reqID:         reqID,
		words:         string(words),
		addBlock:      addBlock,
	}
	l.cfg.Steps = steps
	return f, l, steps, preDesign
}

func TestPlannedRequestDesignAmendmentRoutesDesignBeforeBuild(t *testing.T) {
	f, l, steps, preDesign := plannedRequestDesignFixture(t, false)
	result := &LoopResult{}

	// Run 1: plan node ends with OBSTACLE: definition.
	for i := 0; i < 5 && steps.plans < 1; i++ {
		graphPass(t, l, result)
	}
	graphPass(t, l, result)
	if steps.plans != 1 || len(steps.runs) != 1 {
		t.Fatalf("after Run 1: plans=%d runs=%d", steps.plans, len(steps.runs))
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT COUNT(*) FROM graph_revisions`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("graph_revisions=%d, want 0", n)
		}
		conflicts, err := item.ReadConflicts(r)
		if err != nil {
			return err
		}
		c, ok := conflicts[steps.reqID]
		if !ok || c.Status != "raised" || c.WaitingOn != "design" {
			t.Errorf("conflict = %+v (ok=%v), want raised waiting_on=design", c, ok)
		}
		return nil
	})
	var designDispatch bool
	for _, p := range mustPlan(t, l) {
		d := p.decision
		if d.ItemID == steps.reqID && d.Action == ActionDispatch {
			if d.Seat != "design" || d.RunRequest == nil || d.RunRequest.Backend != "designer" || d.BlockConflict == nil || d.Workflow != nil {
				t.Fatalf("unexpected dispatch after Run 1: %+v", d)
			}
			designDispatch = true
		}
	}
	if !designDispatch {
		t.Fatalf("no design dispatch after Run 1: %+v", mustPlan(t, l))
	}

	// Run 2: design BlockConflict run updates docs/design/example.md, gates and lands.
	for i := 0; i < 5 && steps.designs < 1; i++ {
		graphPass(t, l, result)
	}
	graphPass(t, l, result)
	if steps.designs != 1 {
		t.Fatalf("design run did not execute: order=%v", steps.order)
	}
	if green := runGit(t, f.root, "rev-parse", GreenRef); green != preDesign {
		t.Fatalf("GreenRef moved from %s to %s", preDesign, green)
	}
	trunkDoc := runGit(t, f.root, "show", TrunkRef+":docs/design/example.md")
	if !strings.Contains(trunkDoc, "25") || len(item.DefinitionBlockIDs("docs/design/example.md", []byte(trunkDoc))) != 0 {
		t.Fatalf("trunk docs/design/example.md = %q", trunkDoc)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		conflicts, err := item.ReadConflicts(r)
		if err != nil {
			return err
		}
		c, ok := conflicts[steps.reqID]
		if !ok || c.Status != "answered" || c.WaitingOn != "build" {
			t.Errorf("conflict after design = %+v (ok=%v), want answered waiting_on=build", c, ok)
		}
		return nil
	})
	var returnDispatch bool
	for _, p := range mustPlan(t, l) {
		d := p.decision
		if d.ItemID == steps.reqID && d.Action == ActionDispatch {
			if d.Reason != ReasonConflictAnswered || d.Workflow == nil || d.Workflow.Stages[d.NodeIndex].ID != "plan" {
				t.Fatalf("unexpected return dispatch: %+v", d)
			}
			returnDispatch = true
		}
	}
	if !returnDispatch {
		t.Fatalf("no return plan dispatch after Run 2: %+v", mustPlan(t, l))
	}

	// Runs 3-5: returned plan -> work/piece-limit -> verify -> landed.
	var state string
	for i := 0; i < 20 && state != StateLanded; i++ {
		graphPass(t, l, result)
		fixtureRead(t, f, func(r *store.Reader) error {
			return r.QueryRow(`SELECT state FROM items WHERE item_id=?`, steps.reqID).Scan(&state)
		})
	}
	if state != StateLanded {
		t.Fatalf("state=%q, want %q; order=%v", state, StateLanded, steps.order)
	}
	wantOrder := []string{"plan", "design", "plan", steps.workNodeID, "verify"}
	if steps.workNodeID == "" || !reflect.DeepEqual(steps.order, wantOrder) {
		t.Fatalf("run order = %v, want %v", steps.order, wantOrder)
	}
}

func TestRequestDesignRunCannotAddItemBlock(t *testing.T) {
	f, l, steps, _ := plannedRequestDesignFixture(t, true)
	result := &LoopResult{}

	for i := 0; i < 5 && steps.plans < 1; i++ {
		graphPass(t, l, result)
	}
	graphPass(t, l, result)
	for i := 0; i < 5 && steps.designs < 1; i++ {
		graphPass(t, l, result)
	}
	graphPass(t, l, result)
	if steps.designs != 1 {
		t.Fatalf("design run did not execute: order=%v", steps.order)
	}
	if steps.gates != 0 {
		t.Fatalf("gates=%d, want 0 (refused before gate)", steps.gates)
	}
	p := refusalOf(t, f, steps.designRun)
	if p.Step != RefusalFence || !strings.Contains(p.Detail, RequestBlockRefusal+":") {
		t.Fatalf("refusal = %+v, want step=%s detail containing %q", p, RefusalFence, RequestBlockRefusal+":")
	}
}

func TestDocumentItemDesignObstacleIncludesTargetDesignDocInFence(t *testing.T) {
	f := newRefusalLoopFixture(t, "item-a")
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET body_ref='briefs/item-a.md' WHERE item_id='item-a'`)
		return err
	})
	recorded := stagedItem{
		ItemID:       "item-a",
		BodyRef:      "briefs/item-a.md",
		AllowedPaths: []string{"tools/item-a/"},
	}
	c := item.Conflict{
		ConflictID: "c-1",
		ItemID:     "item-a",
		BodyRef:    "briefs/item-a.md",
		WaitingOn:  "design",
		Status:     "raised",
		Text:       "OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: docs/design/example.md D-EX1 contradicts item-a\n",
	}
	got := blockConflictPaths(recorded, c)
	want := []string{"briefs/item-a.md", "docs/design/example.md"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("blockConflictPaths = %v, want %v", got, want)
	}
	s := &LaneStager{
		Root:     f.root,
		Store:    f.storePath,
		LaneRoot: f.laneRoot,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
	}
	spec := StageSpec{
		ItemID:        "item-a",
		LaneID:        "lane-1",
		RunID:         "run-item-a-a1",
		Backend:       "fixture",
		WorkingDir:    WorktreeDir,
		BaseSHA:       runGit(t, f.root, "rev-parse", TrunkRef),
		BlockConflict: &c,
	}
	if err := s.Stage(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	fence, err := os.ReadFile(filepath.Join(l.laneDirPath("lane-1"), FenceFile))
	if err != nil {
		t.Fatal(err)
	}
	if string(fence) != fenceText(want) {
		t.Fatalf("staged fence.txt = %q, want %q", string(fence), fenceText(want))
	}
}
