package drain

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func buildTillrItemForTest(t *testing.T) string {
	t.Helper()
	binary := filepath.Join(scratchUnderDurable(t), "tillr-item")
	build := exec.Command("go", "build", "-o", binary, ".")
	build.Dir = "../../tillr-item"
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build item binary: %v\n%s", err, out)
	}
	return binary
}

func configureDesignAndPlanPolicy(l *Loop) {
	p := l.currentPolicy()
	if p == nil {
		return
	}
	if p.Seats == nil {
		p.Seats = map[string]string{}
	}
	if p.SeatLists == nil {
		p.SeatLists = map[string][]string{}
	}
	for _, seat := range []string{"design", "plan", "build", "verify"} {
		if p.Seats[seat] == "" {
			p.Seats[seat] = p.DefaultBackend
		}
		if len(p.SeatLists[seat]) == 0 {
			p.SeatLists[seat] = []string{p.Seats[seat]}
		}
	}
}

func writeVantageStub(t *testing.T, indexJSON string) string {
	t.Helper()
	indexScript := filepath.Join(scratchUnderDurable(t), "vantage-stub.sh")
	writeForkFile(t, indexScript, "#!/bin/sh\ncat <<'EOF'\n"+indexJSON+"\nEOF\n")
	if err := os.Chmod(indexScript, 0o755); err != nil {
		t.Fatal(err)
	}
	return indexScript
}

func copyPlannedBuildWorkflow(t *testing.T, root string) {
	t.Helper()
	raw, err := os.ReadFile("../../../config/tillr/workflows/planned-build@v1.json")
	if err != nil {
		t.Fatalf("read planned-build@v1.json: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(root, "config/tillr/workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "config/tillr/workflows/planned-build@v1.json"), string(raw))
}

// TestMirroredDesignZeroQuestionsDispatchesRegardlessOfStageAndHoldsInIcebox verifies OQ-QM2:
// an unparked mirrored design with zero open questions dispatches whatever its stage or status,
// while the same design linked only under ## Icebox is held.
func TestMirroredDesignZeroQuestionsDispatchesRegardlessOfStageAndHoldsInIcebox(t *testing.T) {
	f, l := passFixture(t)
	// Offer enough lanes so every ready design can dispatch in one pass.
	l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3", "lane-4"}
	configureDesignAndPlanPolicy(l)
	copyPlannedBuildWorkflow(t, f.root)

	indexJSON := `{
		"index": {
			"config": {
				"stages": {"SKETCH": "open", "DESIGN": "open", "DECIDED": "ready", "BUILT": "built", "CURRENT": "done"}
			},
			"documents": [
				{"path": "docs/design/sketch-draft.md", "status": "draft", "stage": "SKETCH", "questions": []},
				{"path": "docs/design/design-review.md", "status": "in-review", "stage": "DESIGN", "questions": []},
				{"path": "docs/design/unstaged.md", "status": "draft", "stage": "", "questions": []},
				{"path": "docs/design/upstream.md", "status": "in-review", "stage": "DESIGN", "questions": [{"id": "OQ-UP1", "state": "open", "title": "Upstream open"}]},
				{"path": "docs/design/dep-oq.md", "status": "in-review", "stage": "DESIGN", "dependsOn": [{"raw": "./upstream.md#OQ-UP1", "target": "docs/design/upstream.md", "fragment": "OQ-UP1", "line": 4}], "questions": []},
				{"path": "docs/design/parked.md", "status": "draft", "stage": "SKETCH", "questions": []}
			]
		}
	}`
	indexScript := writeVantageStub(t, indexJSON)
	l.cfg.QuestionsCmd = []string{indexScript}

	if err := os.MkdirAll(filepath.Join(f.root, "docs/design"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "docs/roadmap.md"), strings.Join([]string{
		"# Roadmap",
		"- [Sketch Draft](design/sketch-draft.md)",
		"- [Design Review](design/design-review.md)",
		"- [Unstaged](design/unstaged.md)",
		"- [Dep OQ](design/dep-oq.md)",
		"## Icebox",
		"- [Parked](design/parked.md)",
	}, "\n"))
	writeForkFile(t, filepath.Join(f.root, "docs/design/sketch-draft.md"), "---\nstatus: draft\nstage: SKETCH\n---\n# Sketch Draft\n")
	writeForkFile(t, filepath.Join(f.root, "docs/design/design-review.md"), "---\nstatus: in-review\nstage: DESIGN\n---\n# Design Review\n")
	writeForkFile(t, filepath.Join(f.root, "docs/design/unstaged.md"), "---\nstatus: draft\n---\n# Unstaged\n")
	writeForkFile(t, filepath.Join(f.root, "docs/design/upstream.md"), "---\nstage: DESIGN\n---\n# Upstream\n")
	writeForkFile(t, filepath.Join(f.root, "docs/design/dep-oq.md"), "---\nstage: DESIGN\ndepends-on:\n  - ./upstream.md#OQ-UP1\n---\n# Dep OQ\n")
	writeForkFile(t, filepath.Join(f.root, "docs/design/parked.md"), "---\nstatus: draft\nstage: SKETCH\n---\n# Parked\n")
	runGit(t, f.root, "add", "docs", "config")
	runGit(t, f.root, "commit", "-qm", "oq-qm2 fixture")

	binary := buildTillrItemForTest(t)
	steps := &ProcessSteps{
		Root:         f.root,
		Store:        f.storePath,
		ItemBin:      binary,
		QuestionsCmd: []string{indexScript},
		Now:          l.cfg.Now,
	}
	outcome, err := steps.Sync(context.Background())
	if err != nil || outcome.Created != 5 || len(outcome.Findings) != 0 {
		t.Fatalf("sync: %+v / %v", outcome, err)
	}

	for _, id := range []string{
		"item-design-design-sketch-draft",
		"item-design-design-design-review",
		"item-design-design-unstaged",
	} {
		d := decisionFor(t, l, id).decision
		if d.Action != ActionDispatch {
			t.Fatalf("%s decision = %s / %s, want %s", id, d.Action, d.Reason, ActionDispatch)
		}
	}

	if d := decisionFor(t, l, "item-design-design-dep-oq").decision; d.Action != ActionHold || d.Reason != ReasonQuestionOpen || d.QuestionID != "OQ-UP1" {
		t.Fatalf("dep-oq decision = %+v, want hold / %s (OQ-UP1)", d, ReasonQuestionOpen)
	}
	if d := decisionFor(t, l, "item-design-design-parked").decision; d.Action != ActionHold || d.Reason != ReasonBlocked {
		t.Fatalf("parked decision = %+v, want hold / %s", d, ReasonBlocked)
	}

	// Move sketch-draft.md out of the spine and under ## Icebox only: the same design is now held.
	writeForkFile(t, filepath.Join(f.root, "docs/roadmap.md"), strings.Join([]string{
		"# Roadmap",
		"- [Design Review](design/design-review.md)",
		"- [Unstaged](design/unstaged.md)",
		"- [Dep OQ](design/dep-oq.md)",
		"## Icebox",
		"- [Sketch Draft](design/sketch-draft.md)",
		"- [Parked](design/parked.md)",
	}, "\n"))
	runGit(t, f.root, "add", "docs/roadmap.md")
	runGit(t, f.root, "commit", "-qm", "park sketch-draft in icebox")

	if _, err := steps.Sync(context.Background()); err != nil {
		t.Fatalf("re-sync after parking: %v", err)
	}
	if d := decisionFor(t, l, "item-design-design-sketch-draft").decision; d.Action != ActionHold || d.Reason != ReasonBlocked {
		t.Fatalf("parked sketch-draft decision = %+v, want hold / %s", d, ReasonBlocked)
	}
}

type mirroredPlanSteps struct {
	*workflowSteps
	mode        string // "multi-commit" or "no-change"
	plans       int
	works       int
	verifies    int
	gates       int
	lands       int
	planBrief   string
	verifyBrief string
}

func (s *mirroredPlanSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.gates++
	if s.mode == "multi-commit" {
		s.t.Fatal("gate must not run when plan holds multi-commit design")
	}
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *mirroredPlanSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.lands++
	if s.mode == "multi-commit" {
		s.t.Fatal("land must not run when plan holds multi-commit design")
	}
	// Apply the candidate's commit onto the test trunk checkout (mirroring workflowSteps.Land).
	runGit(s.t, s.f.root, "fetch", c.Worktree, c.HeadSHA)
	runGit(s.t, s.f.root, "merge", "--ff-only", "FETCH_HEAD")
	return LandOutcome{Result: LandLanded, TrunkAfter: runGit(s.t, s.f.root, "rev-parse", "HEAD")}, nil
}

func (s *mirroredPlanSteps) Run(_ context.Context, req run.Request) (run.Record, string, error) {
	laneDir := filepath.Join(s.f.laneRoot, req.LaneID)
	target := filepath.Join(laneDir, WorktreeDir)
	briefBytes, err := os.ReadFile(filepath.Join(laneDir, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	brief := string(briefBytes)

	switch req.NodeID {
	case "plan":
		s.plans++
		s.planBrief = brief
		if s.mode == "multi-commit" {
			report := "OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: needs-companion-plan-slice: design requires three separate commits\nRESULT: blocked\n"
			return s.exitedRecord(req, report), report, nil
		}
		report := "CAUSE: design requirements already implemented by prior commits\nEVIDENCE: checked HEAD against every ledger decision\nWHY_NO_CHANGE: design is already satisfied on HEAD\nRESULT: no_change\n"
		return s.exitedRecord(req, report), report, nil

	case "work":
		s.works++
		s.t.Fatalf("work must not run in mode %q", s.mode)
		return s.exitedRecord(req, "RESULT: done\n"), "RESULT: done\n", nil

	case "verify":
		s.verifies++
		s.verifyBrief = brief
		head := runGit(s.t, target, "rev-parse", "HEAD")
		tree := runGit(s.t, target, "rev-parse", head+"^{tree}")
		var verdicts []item.JudgeEntry
		fixtureRead(s.t, s.f, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT criterion_id,text,claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, req.ItemID)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id, text, class string
				if err := rows.Scan(&id, &text, &class); err != nil {
					return err
				}
				verdicts = append(verdicts, item.JudgeEntry{
					CriterionID:   id,
					CriterionHash: item.CriterionHash(text),
					Verdict:       item.VerdictDone,
					ClaimClass:    class,
					Reason:        "independently verified design is already satisfied on HEAD",
					Evidence:      []string{"node:plan", "cmd:check-head"},
				})
			}
			return rows.Err()
		})
		raw, err := json.Marshal(item.JudgeOutput{
			EvaluatedHead: head,
			EvaluatedTree: tree,
			Outcome:       "pass",
			Verdicts:      verdicts,
		})
		if err != nil {
			return run.Record{}, "", err
		}
		outPath := filepath.Join(run.DurableDir(laneDir, req.RunID), "eval.output")
		if err := os.MkdirAll(filepath.Dir(outPath), 0o755); err != nil {
			return run.Record{}, "", err
		}
		if err := os.WriteFile(outPath, raw, 0o644); err != nil {
			return run.Record{}, "", err
		}
		return s.exitedRecord(req, "RESULT: done\n"), "RESULT: done\n", nil
	}
	return s.exitedRecord(req, "RESULT: done\n"), "RESULT: done\n", nil
}

func (s *mirroredPlanSteps) exitedRecord(req run.Request, report string) run.Record {
	code := 0
	var refs []run.ArtifactRef
	if report != "" {
		laneDir := filepath.Join(s.f.laneRoot, req.LaneID)
		path := filepath.Join(run.DurableDir(laneDir, req.RunID), "report.md")
		writeForkFile(s.t, path, report)
		refs = append(refs, run.ArtifactRef{Kind: "report", Ref: path})
	}
	rec := run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		State:        run.StateExited,
		ExitCode:     &code,
		StartedAt:    "2026-11-27T00:00:00Z",
		FinishedAt:   "2026-11-27T00:00:00Z",
		ArtifactRefs: refs,
	}
	st, err := store.Open(context.Background(), s.f.storePath, true)
	if err != nil {
		s.t.Fatal(err)
	}
	defer st.Close()
	if err := st.WriteTx(context.Background(), func(tx *store.Tx) error { return applyTerminal(tx, rec) }); err != nil {
		s.t.Fatal(err)
	}
	return rec
}

func setupMirroredAcceptedDesignFixture(t *testing.T, docName string) (refusalLoopFixture, *Loop, *workflowSteps, string) {
	t.Helper()
	f, l, base := plannedFixture(t)
	configureDesignAndPlanPolicy(l)
	copyPlannedBuildWorkflow(t, f.root)

	// Close the default seeded items so only our mirrored design item is active.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id IN ('item-a','item-b')`)
		return err
	})

	docPath := "docs/design/" + docName + ".md"
	indexJSON := `{
		"index": {
			"config": {
				"stages": {"SKETCH": "open", "DESIGN": "open", "DECIDED": "ready", "BUILT": "built", "CURRENT": "done"}
			},
			"documents": [
				{"path": "` + docPath + `", "status": "accepted", "stage": "DECIDED", "questions": []}
			]
		}
	}`
	indexScript := writeVantageStub(t, indexJSON)
	l.cfg.QuestionsCmd = []string{indexScript}

	if err := os.MkdirAll(filepath.Join(f.root, "docs/design"), 0o700); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "docs/roadmap.md"), "# Roadmap\n- [Accepted Design](design/"+docName+".md)\n")
	writeForkFile(t, filepath.Join(f.root, docPath), "---\ntitle: \"Accepted Design\"\nstatus: accepted\nstage: DECIDED\n---\n# Accepted Design\n")
	runGit(t, f.root, "add", "docs", "config")
	runGit(t, f.root, "commit", "-qm", "accepted design fixture")

	binary := buildTillrItemForTest(t)
	syncSteps := &ProcessSteps{
		Root:         f.root,
		Store:        f.storePath,
		ItemBin:      binary,
		QuestionsCmd: []string{indexScript},
		Now:          l.cfg.Now,
	}
	outcome, err := syncSteps.Sync(context.Background())
	if err != nil || outcome.Created != 1 || len(outcome.Findings) != 0 {
		t.Fatalf("sync: %+v / %v", outcome, err)
	}
	return f, l, base, "item-design-design-" + docName
}

// TestMirroredAcceptedDesignDispatchesPlannedBuildAndHoldsMultiCommitWithNothingCommitted
// verifies OQ-QM3: a planning item for an accepted design dispatches planned-build@v1,
// and when plan finds the design needs several commits it holds with nothing committed.
func TestMirroredAcceptedDesignDispatchesPlannedBuildAndHoldsMultiCommitWithNothingCommitted(t *testing.T) {
	f, l, base, id := setupMirroredAcceptedDesignFixture(t, "multi-commit")

	d := decisionFor(t, l, id).decision
	if d.Action != ActionDispatch {
		t.Fatalf("%s decision = %s / %s, want %s", id, d.Action, d.Reason, ActionDispatch)
	}
	if d.SelectedWorkflow != "planned-build@v1" {
		t.Fatalf("%s workflow = %q, want planned-build@v1", id, d.SelectedWorkflow)
	}
	if d.RunRequest == nil || d.RunRequest.NodeID != "plan" {
		t.Fatalf("%s run request = %+v, want plan node", id, d.RunRequest)
	}

	steps := &mirroredPlanSteps{workflowSteps: base, mode: "multi-commit"}
	l.cfg.Steps = steps
	trunkBefore := runGit(t, f.root, "rev-parse", TrunkRef)

	result := &LoopResult{}
	graphPass(t, l, result)
	graphPass(t, l, result)

	if trunkAfter := runGit(t, f.root, "rev-parse", TrunkRef); trunkAfter != trunkBefore {
		t.Fatalf("trunk moved when plan held multi-commit design: %s -> %s", trunkBefore, trunkAfter)
	}
	if steps.plans != 1 || steps.works != 0 || steps.gates != 0 || steps.lands != 0 {
		t.Fatalf("counts = plans:%d works:%d gates:%d lands:%d, want 1/0/0/0", steps.plans, steps.works, steps.gates, steps.lands)
	}
	if !strings.Contains(steps.planBrief, ReasonNeedsCompanionPlanSlice) {
		t.Fatalf("plan brief missing %s instructions:\n%s", ReasonNeedsCompanionPlanSlice, steps.planBrief)
	}

	held := decisionFor(t, l, id).decision
	if held.Action != ActionHold || held.Reason != ReasonNeedsCompanionPlanSlice {
		t.Fatalf("after multi-commit plan, decision = %s / %s, want hold / %s", held.Action, held.Reason, ReasonNeedsCompanionPlanSlice)
	}
}

// TestMirroredAcceptedDesignNoChangeStampsBuiltWithoutRebuilding verifies OQ-QM3:
// the plan step's instructions say a design already satisfied on HEAD ends no_change
// and is stamped BUILT, and that verified outcome stamps stage: BUILT without running work.
func TestMirroredAcceptedDesignNoChangeStampsBuiltWithoutRebuilding(t *testing.T) {
	f, l, base, id := setupMirroredAcceptedDesignFixture(t, "already-built")
	steps := &mirroredPlanSteps{workflowSteps: base, mode: "no-change"}
	l.cfg.Steps = steps

	result := &LoopResult{}
	for i := 0; i < 20 && itemStateInStore(t, f, id) == "in-queue"; i++ {
		graphPass(t, l, result)
	}

	if steps.plans != 1 || steps.works != 0 || steps.verifies != 1 || steps.gates != 1 || steps.lands != 1 {
		var detail string
		fixtureRead(t, f, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT kind, payload FROM events WHERE object_id=? ORDER BY seq DESC LIMIT 5`, id)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var kind, payload string
				if err := rows.Scan(&kind, &payload); err != nil {
					return err
				}
				detail += kind + "=" + payload + "; "
			}
			return rows.Err()
		})
		t.Fatalf("counts = plans:%d works:%d verifies:%d gates:%d lands:%d, want 1/0/1/1/1 (events=%s)",
			steps.plans, steps.works, steps.verifies, steps.gates, steps.lands, detail)
	}
	if !strings.Contains(steps.planBrief, "RESULT: no_change") || !strings.Contains(steps.planBrief, "stage: BUILT") {
		t.Fatalf("plan brief missing no_change + BUILT instructions:\n%s", steps.planBrief)
	}

	docBytes, err := os.ReadFile(filepath.Join(f.root, "docs/design/already-built.md"))
	if err != nil {
		t.Fatalf("read stamped design doc: %v", err)
	}
	if !strings.Contains(string(docBytes), "stage: BUILT") {
		t.Fatalf("design doc not stamped stage: BUILT:\n%s", string(docBytes))
	}

	var state, closeReason string
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT state, COALESCE(close_reason, '') FROM items WHERE item_id=?`, id).Scan(&state, &closeReason)
	})
	if state != "closed" || !strings.HasPrefix(closeReason, "no_change: ") {
		t.Fatalf("item state/close_reason = %q / %q, want closed / no_change: ...", state, closeReason)
	}
}
