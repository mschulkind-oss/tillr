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
	workBrief   string
	verifyBrief string
	gateFn      func(c Candidate) (GateOutcome, error)
	workFn      func(req run.Request, target, brief string) (run.Record, string, error)
}

func (s *mirroredPlanSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	s.gates++
	if s.mode == "multi-commit" {
		s.t.Fatal("gate must not run when plan holds multi-commit design")
	}
	if s.gateFn != nil {
		return s.gateFn(c)
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
	if s.works > 0 {
		fixtureLanding(s.t, s.f.storePath, c)
	}
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
		s.workBrief = brief
		if s.workFn != nil {
			return s.workFn(req, target, brief)
		}
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

func writeDocumentsGateFailure(t *testing.T, root, runID, output string) string {
	t.Helper()
	runDir := RunDir(root, runID)
	logDir := filepath.Join(runDir, "logs")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "documents.log")
	if err := os.WriteFile(logPath, []byte(output), 0o600); err != nil {
		t.Fatal(err)
	}
	verdict := mustJSON(map[string]any{
		"payload": map[string]any{
			"result": "fail",
			"checks": []map[string]any{
				{"check_id": "documents", "result": "fail", "log_ref": logPath},
			},
		},
	})
	if err := os.WriteFile(filepath.Join(runDir, GateVerdictRecord), []byte(verdict), 0o600); err != nil {
		t.Fatal(err)
	}
	return logPath
}

// TestMirroredAcceptedDesignNoChangeStampDocumentFailureReturnsToBuilder verifies
// that when the loop's BUILT stamp fails the gate's documents check, the check's
// output is returned to the item's builder as a gate refusal (no settle-failed
// hold and no retry of the unchanged stamp) so the builder can repair the link
// and land the stamped document.
func TestMirroredAcceptedDesignNoChangeStampDocumentFailureReturnsToBuilder(t *testing.T) {
	f, l, base, id := setupMirroredAcceptedDesignFixture(t, "broken-link")
	docPath := "docs/design/broken-link.md"
	docErr := docPath + ": dead link ../../tools/tillr-drain/drain/seat_escalation.go"

	var steps *mirroredPlanSteps
	var workSawBuilt bool
	var gateLogPath string
	steps = &mirroredPlanSteps{
		workflowSteps: base,
		mode:          "no-change",
		gateFn: func(c Candidate) (GateOutcome, error) {
			if steps.gates == 1 {
				gateLogPath = writeDocumentsGateFailure(t, f.root, c.RunID, docErr+"\n")
				return GateOutcome{
					Outcome:       GateFailed,
					FailingChecks: []string{"documents"},
					CheckLogs:     map[string]string{"documents": gateLogPath},
				}, nil
			}
			return GateOutcome{Outcome: GatePassed}, nil
		},
		workFn: func(req run.Request, target, brief string) (run.Record, string, error) {
			raw, err := os.ReadFile(filepath.Join(target, docPath))
			if err != nil {
				return run.Record{}, "", err
			}
			workSawBuilt = strings.Contains(string(raw), "stage: BUILT")
			repaired := string(raw) + "\nRepaired link.\n"
			if err := os.WriteFile(filepath.Join(target, docPath), []byte(repaired), 0o644); err != nil {
				return run.Record{}, "", err
			}
			runGit(t, target, "add", "--", docPath)
			runGit(t, target, "commit", "-qm", "docs: repair broken link in "+docPath)
			return steps.exitedRecord(req, "RESULT: done\n"), "RESULT: done\n", nil
		},
	}
	l.cfg.Steps = steps

	result := &LoopResult{}
	// Run until the stamp gate fails and the refusal is recorded (before work runs).
	for i := 0; i < 10 && steps.gates == 0; i++ {
		graphPass(t, l, result)
	}
	if steps.gates != 1 {
		t.Fatalf("after first verify, gates = %d, want 1", steps.gates)
	}

	// Must NOT hold on settle-failed.
	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if fault, ok := faults[id]; ok {
			t.Fatalf("unexpected fault hold after stamp documents failure: %+v", fault)
		}
		return nil
	})
	if !hasRefusalStep(t, f, id, RefusalGate) {
		t.Fatalf("expected %s refusal event after stamp documents failure", RefusalGate)
	}

	// Next decision must dispatch the build seat (work) on the stamped commit with the documents output.
	next := decisionFor(t, l, id).decision
	if next.Action != ActionDispatch || next.RunRequest == nil || next.RunRequest.NodeID != "work" {
		t.Fatalf("next decision = %s / %s (req=%+v), want dispatch work", next.Action, next.Reason, next.RunRequest)
	}
	if next.Returned == nil || next.Returned.Cause != RefusalGate {
		t.Fatalf("next returned = %+v, want cause %s", next.Returned, RefusalGate)
	}

	// Finish draining: work repairs the doc, verify passes, gate passes, land succeeds.
	for i := 0; i < 20 && itemStateInStore(t, f, id) == "in-queue"; i++ {
		graphPass(t, l, result)
	}
	if steps.plans != 1 || steps.works != 1 || steps.verifies != 2 || steps.gates != 2 || steps.lands != 1 {
		t.Fatalf("counts = plans:%d works:%d verifies:%d gates:%d lands:%d, want 1/1/2/2/1",
			steps.plans, steps.works, steps.verifies, steps.gates, steps.lands)
	}
	if !workSawBuilt {
		t.Fatalf("work stage did not start from the stamped stage: BUILT commit")
	}
	if !strings.Contains(steps.workBrief, docErr) || !strings.Contains(steps.workBrief, gateLogPath) {
		t.Fatalf("work brief missing documents check output (%q) or log path (%q):\n%s", docErr, gateLogPath, steps.workBrief)
	}
	if state := itemStateInStore(t, f, id); state != "landed" {
		t.Fatalf("final item state = %q, want landed", state)
	}
	docBytes, err := os.ReadFile(filepath.Join(f.root, docPath))
	if err != nil {
		t.Fatalf("read landed design doc: %v", err)
	}
	if !strings.Contains(string(docBytes), "stage: BUILT") || !strings.Contains(string(docBytes), "Repaired link.") {
		t.Fatalf("landed design doc missing BUILT stamp or repair:\n%s", string(docBytes))
	}
}

// TestMirroredAcceptedDesignHeldStampGateSettleFailedMovesOnItsOwn verifies that an
// item already held on settle-failed from a prior BUILT-stamp documents gate failure
// clears its hold automatically, returns the recorded check output to the builder
// without re-running the gate on the unchanged stamp, and dispatches work.
func TestMirroredAcceptedDesignHeldStampGateSettleFailedMovesOnItsOwn(t *testing.T) {
	f, l, base, id := setupMirroredAcceptedDesignFixture(t, "already-held")
	docPath := "docs/design/already-held.md"
	docErr := docPath + ": dead link ../../tools/tillr-drain/drain/seat_escalation.go"

	var logPath string
	var steps *mirroredPlanSteps
	steps = &mirroredPlanSteps{
		workflowSteps: base,
		mode:          "no-change",
		gateFn: func(c Candidate) (GateOutcome, error) {
			if steps.gates == 1 {
				// Simulate the pre-fix settle-failed outcome: gate wrote its failing
				// documents verdict and settlement returned an external error.
				logPath = writeDocumentsGateFailure(t, f.root, c.RunID, docErr+"\n")
				return GateOutcome{}, Externalf("stamping %s BUILT failed gate (fail): documents", docPath)
			}
			t.Fatalf("gate must not re-run on unchanged stamp for %s", c.RunID)
			return GateOutcome{Outcome: GatePassed}, nil
		},
	}
	l.cfg.Steps = steps

	// Drive plan (no_change) -> verify (passes) -> stamp commit -> pre-fix settle-failed hold.
	result := &LoopResult{}
	for i := 0; i < 10 && steps.gates == 0; i++ {
		graphPass(t, l, result)
	}
	if steps.gates != 1 {
		t.Fatalf("gates before recovery = %d, want 1", steps.gates)
	}

	// Confirm the item is genuinely held on settle-failed with the pre-fix detail.
	if held := decisionFor(t, l, id).decision; held.Action != ActionHold || held.Reason != ReasonSettleFailed {
		t.Fatalf("seeded decision = %s / %s, want hold / %s", held.Action, held.Reason, ReasonSettleFailed)
	}

	// Next pass must clear the settle-failed hold automatically (via observeAndSyncTrunk ->
	// releaseStampGateSettleFaults) and settle verify into RefusalGate without re-running Gate.
	graphPass(t, l, result)

	fixtureRead(t, f, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if fault, ok := faults[id]; ok {
			t.Fatalf("settle-failed hold still active after recovery: %+v", fault)
		}
		return nil
	})
	if steps.gates != 1 {
		t.Fatalf("gates after recovery = %d, want 1 (must not retry unchanged stamp)", steps.gates)
	}
	if !hasRefusalStep(t, f, id, RefusalGate) {
		t.Fatalf("expected %s refusal event after recovering held stamp", RefusalGate)
	}

	next := decisionFor(t, l, id).decision
	if next.Action != ActionDispatch || next.RunRequest == nil || next.RunRequest.NodeID != "work" {
		t.Fatalf("next decision after recovery = %s / %s (req=%+v), want dispatch work", next.Action, next.Reason, next.RunRequest)
	}
	if next.Returned == nil || next.Returned.Cause != RefusalGate || !strings.Contains(next.Returned.Detail, docErr) {
		t.Fatalf("next returned = %+v, want RefusalGate containing %q", next.Returned, docErr)
	}
	if len(next.Returned.Checks) != 1 || next.Returned.Checks[0].CheckID != "documents" || next.Returned.Checks[0].LogRef != logPath {
		t.Fatalf("next returned checks = %+v, want documents with log %q", next.Returned.Checks, logPath)
	}
}
