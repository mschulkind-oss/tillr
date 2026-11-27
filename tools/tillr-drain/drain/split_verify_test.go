package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func splitVerifyFixture(t *testing.T) (refusalLoopFixture, *Loop, string, map[string]string) {
	t.Helper()
	f, l, _ := judgeRouteFixture(t, "done")
	ctx := context.Background()
	at := time.Date(2026, 11, 27, 7, 0, 0, 0, time.UTC)

	var parentID string
	var partIDs map[string]string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		// Close the default fixture item so the split parts are the only queued items.
		if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id IN ('item-a','item-b')`); err != nil {
			return err
		}
		req := item.RequestFiling{
			Words:       []byte("Handle both alpha and beta cases cleanly."),
			Title:       "Handle alpha and beta",
			Destination: item.Destination{To: "last"},
			At:          at,
			By:          "owner",
			Via:         "cli",
		}
		env, err := item.FileRequest(ctx, tx, req)
		if err != nil {
			return err
		}
		var body struct {
			ItemID string `json:"item_id"`
		}
		if err := json.Unmarshal(env.Payload, &body); err != nil {
			return err
		}
		parentID = body.ItemID

		reqTrue := true
		stmt := item.SplitStatement{
			Parts: []item.SplitPart{
				{
					Label:    "alpha",
					Title:    "Handle alpha",
					Share:    "Handle the alpha case.",
					Leaves:   map[string]string{"beta": "Handle the beta case."},
					Required: &reqTrue,
				},
				{
					Label:    "beta",
					Title:    "Handle beta",
					Share:    "Handle the beta case.",
					Leaves:   map[string]string{"alpha": "Handle the alpha case."},
					Follows:  []string{"alpha"},
					Required: &reqTrue,
				},
			},
			Coverage: map[string][]item.SplitCoverage{},
		}
		partIDs, err = item.FileSplit(ctx, tx, parentID, stmt, at.Add(time.Minute))
		if err != nil {
			return err
		}
		for _, id := range []string{partIDs["alpha"], partIDs["beta"]} {
			if _, err := tx.Exec(`UPDATE items SET workflow='', judge_mode='agent' WHERE item_id=?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	return f, l, parentID, partIDs
}

func partVerdicts(t *testing.T, f refusalLoopFixture, itemID, verdict string, findings item.VerdictFindings) []item.JudgeEntry {
	t.Helper()
	var entries []item.JudgeEntry
	fixtureRead(t, f, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT criterion_id, text, claim_class FROM item_criteria WHERE item_id=? ORDER BY criterion_id`, itemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, text, class string
			if err := rows.Scan(&id, &text, &class); err != nil {
				return err
			}
			v := item.VerdictDone
			var fs item.VerdictFindings
			if id == "c1" && verdict != item.VerdictDone {
				v = verdict
				fs = findings
			}
			entries = append(entries, item.JudgeEntry{
				CriterionID:   id,
				CriterionHash: item.CriterionHash(text),
				Verdict:       v,
				ClaimClass:    class,
				Reason:        "checked " + id,
				Evidence:      []string{"diff:candidate.diff"},
				Findings:      fs,
			})
		}
		return rows.Err()
	})
	return entries
}

type splitPartJudgeSteps struct {
	*workflowSteps
	f         refusalLoopFixture
	responses func(req run.Request, call int, head, tree string) item.JudgeOutput
	judgeCall int
	judgeReqs []run.Request
}

func (s *splitPartJudgeSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	target := filepath.Join(s.f.laneRoot, req.LaneID, "worktree")
	reportPath := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	if req.Node != "judge" && req.NodeID != "evaluate" {
		if err := os.MkdirAll(filepath.Join(target, "tools", "split"), 0755); err != nil {
			s.t.Fatal(err)
		}
		writeForkFile(s.t, filepath.Join(target, "tools/split/part.txt"), req.ItemID+"-"+req.RunID+"\n")
		runGit(s.t, target, "add", ".")
		runGit(s.t, target, "commit", "-qm", "build "+req.ItemID)
		writeForkFile(s.t, reportPath, "RESULT: done\n")
		rec := run.Record{
			RunID:        req.RunID,
			ItemID:       req.ItemID,
			LaneID:       req.LaneID,
			Backend:      req.Backend,
			State:        run.StateExited,
			ExitCode:     countPtr(0),
			StartedAt:    "2026-11-27T07:05:00Z",
			FinishedAt:   "2026-11-27T07:05:01Z",
			ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}},
		}
		fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec, "RESULT: done\n", nil
	}
	s.judgeCall++
	s.judgeReqs = append(s.judgeReqs, req)
	head := runGit(s.t, target, "rev-parse", "HEAD")
	tree := runGit(s.t, target, "rev-parse", "HEAD^{tree}")
	out := s.responses(req, s.judgeCall, head, tree)
	raw, err := json.Marshal(out)
	if err != nil {
		s.t.Fatal(err)
	}
	evalPath := filepath.Join(run.DurableDir(filepath.Join(s.f.laneRoot, req.LaneID), req.RunID), "eval.output")
	if err := os.MkdirAll(filepath.Dir(evalPath), 0755); err != nil {
		s.t.Fatal(err)
	}
	if err := os.WriteFile(evalPath, raw, 0644); err != nil {
		s.t.Fatal(err)
	}
	writeForkFile(s.t, reportPath, "RESULT: done\n")
	rec := run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		State:        run.StateExited,
		ExitCode:     countPtr(0),
		StartedAt:    "2026-11-27T07:05:00Z",
		FinishedAt:   "2026-11-27T07:05:01Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}},
	}
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "RESULT: done\n", nil
}

func (s *splitPartJudgeSteps) Gate(_ context.Context, _ Candidate) (GateOutcome, error) {
	return GateOutcome{Outcome: GatePassed}, nil
}

func TestSplitPartJudgeBriefIncludesAndNonPartOmits(t *testing.T) {
	_, l, c := heldCandidateFixture(t)
	receipt := JudgeReceipt{Request: run.Request{RunID: "non-part-judge"}, Options: item.JudgeOptions{Head: c.HeadSHA}}
	if err := l.stageJudge(context.Background(), c, &receipt, "judge-session"); err != nil {
		t.Fatal(err)
	}
	nonPartRaw, err := os.ReadFile(filepath.Join(l.laneDirPath(c.LaneID), BriefFile))
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(nonPartRaw), "## Split part verification") {
		t.Fatalf("non-part judge brief unexpectedly included split part verification:\n%s", string(nonPartRaw))
	}

	f, sl, parentID, partIDs := splitVerifyFixture(t)
	alphaID := partIDs["alpha"]
	betaID := partIDs["beta"]
	partCand := c
	partCand.ItemID = alphaID
	partReceipt := JudgeReceipt{Request: run.Request{RunID: "part-judge"}, Options: item.JudgeOptions{Head: c.HeadSHA}}
	if err := sl.stageJudge(context.Background(), partCand, &partReceipt, "judge-session"); err != nil {
		t.Fatal(err)
	}
	partRaw, err := os.ReadFile(filepath.Join(sl.laneDirPath(partCand.LaneID), BriefFile))
	if err != nil {
		t.Fatal(err)
	}
	brief := string(partRaw)
	for _, want := range []string{
		"## Split part verification",
		"Parent item: " + parentID,
		"This part: alpha — Handle the alpha case.",
		"### Cases left to siblings",
		"beta (" + betaID + "): Handle the beta case.",
		"### Sibling parts",
		"beta (" + betaID + ", Handle beta): Handle the beta case.",
		"Rule P6: gaps vs. regressions",
		`"sibling_gaps"`,
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("part judge brief lacks %q\n%s", want, brief)
		}
	}
	_ = f
}

func TestSplitPartPassWithSiblingGapLandsAndStagesSibling(t *testing.T) {
	f, l, parentID, partIDs := splitVerifyFixture(t)
	alphaID := partIDs["alpha"]
	betaID := partIDs["beta"]

	steps := &splitPartJudgeSteps{
		workflowSteps: l.cfg.Steps.(*judgeRouteSteps).workflowSteps,
		f:             f,
		responses: func(req run.Request, call int, head, tree string) item.JudgeOutput {
			return item.JudgeOutput{
				EvaluatedHead: head,
				EvaluatedTree: tree,
				Outcome:       "pass",
				Verdicts:      partVerdicts(t, f, req.ItemID, item.VerdictDone, nil),
				SiblingGaps: item.SiblingGaps{{
					Sibling:  "beta",
					Case:     "Beta input falls back to base behavior.",
					Paths:    []string{"file:pkg/beta.go:10"},
					Evidence: []string{"diff:candidate.diff"},
				}},
			}
		},
	}
	l.cfg.Steps = steps

	result := &LoopResult{}
	for i := 0; i < 4; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		var alphaState string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, alphaID).Scan(&alphaState); err != nil {
			return err
		}
		if alphaState != "landed" {
			t.Fatalf("alpha state = %q, want landed", alphaState)
		}
		parentGaps, err := item.SplitGapsForParent(r, parentID)
		if err != nil {
			return err
		}
		sibGaps, err := item.SplitGapsForSibling(r, betaID)
		if err != nil {
			return err
		}
		if len(parentGaps) != 1 || len(sibGaps) != 1 {
			t.Fatalf("parentGaps=%d, sibGaps=%d, want 1 each", len(parentGaps), len(sibGaps))
		}
		var evCount int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind=? AND object_type='item' AND object_id=?`, item.SplitGapKind, parentID).Scan(&evCount); err != nil {
			return err
		}
		if evCount != 1 {
			t.Fatalf("item.split-gap events on parent = %d, want 1", evCount)
		}
		return nil
	})

	// Staging the sibling's worker brief includes the recorded sibling gaps before the byte-counted suffix.
	stager := &LaneStager{
		Root:     f.root,
		LaneRoot: f.laneRoot,
		Store:    f.storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
	}
	baseSHA := runGit(t, f.root, "rev-parse", "HEAD")
	spec := StageSpec{
		Policy:     l.currentPolicy(),
		ItemID:     betaID,
		LaneID:     "lane-1",
		RunID:      "run-beta-1",
		Backend:    "worker",
		WorkingDir: WorktreeDir,
		BaseSHA:    baseSHA,
	}
	if err := stager.Stage(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	workerBriefRaw, err := os.ReadFile(filepath.Join(f.laneRoot, "lane-1", BriefFile))
	if err != nil {
		t.Fatal(err)
	}
	workerBrief := string(workerBriefRaw)
	gapHeading := "## Recorded gaps from sibling parts"
	suffixHeading := "## Saved request words (verbatim;"
	gapIdx := strings.Index(workerBrief, gapHeading)
	suffixIdx := strings.Index(workerBrief, suffixHeading)
	if gapIdx == -1 || suffixIdx == -1 || gapIdx >= suffixIdx {
		t.Fatalf("worker brief missing gap section before request words suffix (gapIdx=%d, suffixIdx=%d):\n%s", gapIdx, suffixIdx, workerBrief)
	}
	if !strings.Contains(workerBrief, "Beta input falls back to base behavior.") {
		t.Fatalf("worker brief missing gap case:\n%s", workerBrief)
	}

	// Staging the sibling's judge brief also includes the recorded sibling gaps.
	wt := filepath.Join(f.laneRoot, "lane-1", "worktree")
	head := runGit(t, wt, "rev-parse", "HEAD")
	betaCand := Candidate{
		ItemID:   betaID,
		LaneID:   "lane-1",
		RunID:    "run-beta-1",
		Worktree: wt,
		BaseSHA:  head,
		HeadSHA:  head,
	}
	betaReceipt := JudgeReceipt{Request: run.Request{RunID: "judge-beta-1"}, Options: item.JudgeOptions{Head: head}}
	if err := l.stageJudge(context.Background(), betaCand, &betaReceipt, "judge-session"); err != nil {
		t.Fatal(err)
	}
	judgeBriefRaw, err := os.ReadFile(filepath.Join(l.laneDirPath("lane-1"), BriefFile))
	if err != nil {
		t.Fatal(err)
	}
	judgeBrief := string(judgeBriefRaw)
	if !strings.Contains(judgeBrief, gapHeading) || !strings.Contains(judgeBrief, "Beta input falls back to base behavior.") {
		t.Fatalf("sibling judge brief missing recorded sibling gaps:\n%s", judgeBrief)
	}
}

func TestSplitPartRegressionStillRefusedWithoutSplitCall(t *testing.T) {
	f, l, parentID, partIDs := splitVerifyFixture(t)
	alphaID := partIDs["alpha"]

	steps := &splitPartJudgeSteps{
		workflowSteps: l.cfg.Steps.(*judgeRouteSteps).workflowSteps,
		f:             f,
		responses: func(req run.Request, call int, head, tree string) item.JudgeOutput {
			return item.JudgeOutput{
				EvaluatedHead: head,
				EvaluatedTree: tree,
				Outcome:       "fail",
				Verdicts: partVerdicts(t, f, req.ItemID, item.VerdictNotDone, item.VerdictFindings{{
					Kind:  "regression",
					Case:  "Alpha breaks existing handling.",
					Paths: []string{"file:pkg/alpha.go:5"},
				}}),
			}
		},
	}
	l.cfg.Steps = steps

	result := &LoopResult{}
	for i := 0; i < 2; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	d := decisionFor(t, l, alphaID).decision
	if d.Action != ActionHold {
		t.Fatalf("regression on part disposition = %+v, want hold", d)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var refusalStep string
		if err := r.QueryRow(`SELECT json_extract(payload, '$.step') FROM events WHERE kind='drain.refusal' AND json_extract(payload, '$.item_id')=? ORDER BY seq DESC LIMIT 1`, alphaID).Scan(&refusalStep); err != nil {
			return err
		}
		if refusalStep != ReasonEvalFailed {
			t.Fatalf("refusal step = %q, want %q", refusalStep, ReasonEvalFailed)
		}
		var alphaState string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, alphaID).Scan(&alphaState); err != nil {
			return err
		}
		if alphaState == "landed" {
			t.Fatalf("regressed part should not land")
		}
		gaps, err := item.SplitGapsForParent(r, parentID)
		if err != nil {
			return err
		}
		if len(gaps) != 0 {
			t.Fatalf("unexpected gaps recorded on regression: %+v", gaps)
		}
		return nil
	})
}

func TestJudgeSiblingGapFormRejectionResumesAndPasses(t *testing.T) {
	f, l, _, partIDs := splitVerifyFixture(t)
	alphaID := partIDs["alpha"]

	steps := &splitPartJudgeSteps{
		workflowSteps: l.cfg.Steps.(*judgeRouteSteps).workflowSteps,
		f:             f,
		responses: func(req run.Request, call int, head, tree string) item.JudgeOutput {
			target := "unknown-sibling"
			if call == 2 {
				target = "beta"
			}
			return item.JudgeOutput{
				EvaluatedHead: head,
				EvaluatedTree: tree,
				Outcome:       "pass",
				Verdicts:      partVerdicts(t, f, req.ItemID, item.VerdictDone, nil),
				SiblingGaps: item.SiblingGaps{{
					Sibling:  target,
					Case:     "Beta input falls back to base behavior.",
					Paths:    []string{"file:pkg/beta.go:10"},
					Evidence: []string{"diff:candidate.diff"},
				}},
			}
		},
	}
	l.cfg.Steps = steps

	result := &LoopResult{}
	for i := 0; i < 4; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
		var alphaState string
		fixtureRead(t, f, func(r *store.Reader) error {
			return r.QueryRow(`SELECT state FROM items WHERE item_id=?`, alphaID).Scan(&alphaState)
		})
		if alphaState == "landed" {
			break
		}
	}
	var alphaJudges []run.Request
	for _, req := range steps.judgeReqs {
		if req.ItemID == alphaID {
			alphaJudges = append(alphaJudges, req)
		}
	}
	if len(alphaJudges) != 2 {
		t.Fatalf("alpha judge calls = %d, want 2 (form-rejection resume then pass)", len(alphaJudges))
	}
	if alphaJudges[1].ResumePrompt == "" {
		t.Fatalf("second judge call did not resume session: %+v", alphaJudges[1])
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var alphaState string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, alphaID).Scan(&alphaState); err != nil {
			return err
		}
		if alphaState != "landed" {
			t.Fatalf("alpha state = %q, want landed after resume", alphaState)
		}
		return nil
	})
}
