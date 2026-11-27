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

func TestParseSplitStatementDonorAndCarvingFields(t *testing.T) {
	valid := []byte(`{
		"donor": {"run_id": "r1", "ref": "refs/tillr/runs/r1", "sha": "abc123", "base_sha": "base000"},
		"parts": [
			{"label": "a", "title": "Part A", "share": "Deliver A", "takes": "whole", "required": true},
			{"label": "b", "title": "Part B", "share": "Deliver B", "takes": "named", "paths": ["tools/b/b.go"], "shared_files": ["tools/shared.go"], "inherited_findings": ["c5 [gap] (B missing): tools/b/b.go"], "required": true}
		],
		"coverage": {"c5": [{"clause": "A works;", "part": "a"}, {"clause": "B works.", "part": "b"}]}
	}`)
	st, err := parseSplitStatement(valid)
	if err != nil {
		t.Fatalf("parseSplitStatement valid failed: %v", err)
	}
	if st.Donor == nil || st.Donor.SHA != "abc123" || st.Donor.BaseSHA != "base000" {
		t.Fatalf("Donor = %+v", st.Donor)
	}
	if st.Parts[0].Takes != item.SplitTakesWhole || st.Parts[1].Takes != item.SplitTakesNamed || len(st.Parts[1].Paths) != 1 || len(st.Parts[1].SharedFiles) != 1 || len(st.Parts[1].InheritedFindings) != 1 {
		t.Fatalf("Parts = %+v", st.Parts)
	}

	// Reject unknown donor key.
	badDonorKey := []byte(`{
		"donor": {"sha": "abc123", "extra": "nope"},
		"parts": [
			{"label": "a", "title": "Part A", "share": "Deliver A", "required": true},
			{"label": "b", "title": "Part B", "share": "Deliver B", "required": true}
		],
		"coverage": {}
	}`)
	if _, err := parseSplitStatement(badDonorKey); err == nil {
		t.Fatalf("expected error for unknown donor field")
	}

	// Reject donor on a part.
	partDonor := []byte(`{
		"parts": [
			{"label": "a", "title": "Part A", "share": "Deliver A", "donor": {"sha": "x"}, "required": true},
			{"label": "b", "title": "Part B", "share": "Deliver B", "required": true}
		],
		"coverage": {}
	}`)
	if _, err := parseSplitStatement(partDonor); err == nil {
		t.Fatalf("expected error for donor on part")
	}
}

func TestSplitPartCarvingAndJudgeBriefs(t *testing.T) {
	donor := &item.SplitDonor{
		RunID:   "run-donor-9",
		Ref:     "refs/tillr/runs/run-donor-9",
		SHA:     "donorhead999",
		BaseSHA: "donorbase000",
	}

	namedInfo := &item.SplitPartInfo{
		ParentID: "parent-1",
		Donor:    donor,
		Part: item.SplitPart{
			Label:             "carve",
			Share:             "Deliver feature Y",
			Takes:             item.SplitTakesNamed,
			Paths:             []string{"tools/y/y.go", "tools/y/y_test.go"},
			SharedFiles:       []string{"tools/shared/reg.go"},
			InheritedFindings: []string{"c5 [regression] (Y panicked): tools/y/y.go"},
		},
		Siblings: map[string]item.SplitSibling{
			"rest": {Label: "rest", ItemID: "part-rest", Title: "Rest", Share: "Rest"},
		},
	}

	carveBrief := splitPartCarvingBrief(namedInfo)
	for _, want := range []string{
		"## Carving from the donor",
		"ref=refs/tillr/runs/run-donor-9",
		"sha=donorhead999",
		"base=donorbase000",
		"tools/y/y.go",
		"tools/shared/reg.go",
		"c5 [regression] (Y panicked): tools/y/y.go",
		"just pack-sync",
	} {
		if !strings.Contains(carveBrief, want) {
			t.Errorf("carveBrief missing %q:\n%s", want, carveBrief)
		}
	}

	judgeCarve := splitPartJudgeBrief(namedInfo)
	for _, want := range []string{
		"### Donor and carving instructions",
		"Instructed to take: named paths from the donor",
		"path: tools/y/y.go",
		"shared_file: tools/shared/reg.go",
		"c5 [regression] (Y panicked): tools/y/y.go",
		"No verdict or gate result is inherited from the donor (D-SP8)",
		"inspect both the donor and the part's diff",
	} {
		if !strings.Contains(judgeCarve, want) {
			t.Errorf("judgeCarve missing %q:\n%s", want, judgeCarve)
		}
	}

	nothingInfo := &item.SplitPartInfo{
		ParentID: "parent-1",
		Donor:    donor,
		Part: item.SplitPart{
			Label:             "fresh",
			Share:             "Deliver missing case Z",
			Takes:             item.SplitTakesNothing,
			InheritedFindings: []string{"c5 [gap] (Z missing): tools/z/z.go"},
		},
	}
	nothingBrief := splitPartCarvingBrief(nothingInfo)
	if !strings.Contains(nothingBrief, "## Inherited findings from the refused candidate") || !strings.Contains(nothingBrief, "c5 [gap] (Z missing): tools/z/z.go") {
		t.Fatalf("nothingBrief unexpected:\n%s", nothingBrief)
	}
}

func setupSplitTakesWholeFixture(t *testing.T) (refusalLoopFixture, *Loop, *judgeRouteSteps, string, string, string, string) {
	t.Helper()
	f, l, steps := judgeRouteFixture(t, "done")
	rawWF, err := os.ReadFile("../../../config/tillr/workflows/planned-build@v1.json")
	if err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "config/tillr/workflows/planned-build@v1.json"), string(rawWF))
	runGit(t, f.root, "add", "config/tillr/workflows/planned-build@v1.json")
	runGit(t, f.root, "commit", "-qm", "add planned-build@v1")
	l.cfg.Policy.Seats["verify"] = "reviewer"
	l.cfg.Policy.Seats["plan"] = "reviewer"
	base := runGit(t, f.root, "rev-parse", "HEAD")
	ctx := context.Background()
	at := time.Date(2026, 11, 27, 8, 0, 0, 0, time.UTC)

	// Create a donor commit on a temporary branch and pin it.
	donorWork := filepath.Join(t.TempDir(), "donor-work")
	runGit(t, f.root, "worktree", "add", "--detach", donorWork, base)
	if err := os.WriteFile(filepath.Join(donorWork, "donor_feature.txt"), []byte("donor work\n"), 0644); err != nil {
		t.Fatal(err)
	}
	runGit(t, donorWork, "add", "donor_feature.txt")
	runGit(t, donorWork, "commit", "-m", "donor work")
	donorSHA := runGit(t, donorWork, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", "refs/tillr/runs/run-donor-whole", donorSHA)
	runGit(t, f.root, "worktree", "remove", "--force", donorWork)

	var parentID string
	var partIDs map[string]string
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='closed' WHERE item_id IN ('item-a','item-b')`); err != nil {
			return err
		}
		env, err := item.FileRequest(ctx, tx, item.RequestFiling{
			Words:       []byte("Deliver donor feature and follow-up."),
			Title:       "Donor and follow-up",
			Destination: item.Destination{To: "last"},
			At:          at,
			By:          "owner",
			Via:         "cli",
		})
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
			Donor: &item.SplitDonor{
				RunID:   "run-donor-whole",
				Ref:     "refs/tillr/runs/run-donor-whole",
				SHA:     donorSHA,
				BaseSHA: base,
			},
			Parts: []item.SplitPart{
				{
					Label:    "whole",
					Title:    "Take donor whole",
					Share:    "Deliver donor feature.",
					Leaves:   map[string]string{"rest": "Follow-up"},
					Takes:    item.SplitTakesWhole,
					Required: &reqTrue,
				},
				{
					Label:    "rest",
					Title:    "Follow-up",
					Share:    "Deliver follow-up.",
					Follows:  []string{"whole"},
					Takes:    item.SplitTakesNothing,
					Required: &reqTrue,
				},
			},
			Coverage: map[string][]item.SplitCoverage{},
		}
		partIDs, err = item.FileSplit(ctx, tx, parentID, stmt, at.Add(time.Minute))
		if err != nil {
			return err
		}
		for _, id := range []string{partIDs["whole"], partIDs["rest"]} {
			if _, err := tx.Exec(`UPDATE items SET workflow='planned-build@v1', judge_mode='agent' WHERE item_id=?`, id); err != nil {
				return err
			}
		}
		return nil
	})
	return f, l, steps, parentID, partIDs["whole"], base, donorSHA
}

type splitCarvingSteps struct {
	*workflowSteps
	f         refusalLoopFixture
	responses func(req run.Request, call int, head, tree string) item.JudgeOutput
	judgeCall int
	workCalls map[string]int
	trace     []string
}

func (s *splitCarvingSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.trace = append(s.trace, req.ItemID+" "+req.RunID+" node="+req.Node+" nodeID="+req.NodeID)
	target := filepath.Join(s.f.laneRoot, req.LaneID, "worktree")
	reportPath := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	if req.Node != "judge" && req.NodeID != "evaluate" && req.NodeID != "verify" {
		if s.workCalls == nil {
			s.workCalls = map[string]int{}
		}
		s.workCalls[req.ItemID]++
		writeForkFile(s.t, filepath.Join(target, "donor_feature.txt"), "repaired "+req.RunID+"\n")
		runGit(s.t, target, "add", "donor_feature.txt")
		runGit(s.t, target, "commit", "-qm", "repair "+req.ItemID)
		writeForkFile(s.t, reportPath, "RESULT: done\n")
		rec := run.Record{
			RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
			State: run.StateExited, ExitCode: countPtr(0),
			StartedAt: "2026-11-27T08:05:00Z", FinishedAt: "2026-11-27T08:05:01Z",
			ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}},
		}
		fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		return rec, "RESULT: done\n", nil
	}
	s.judgeCall++
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
		RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, ExitCode: countPtr(0),
		StartedAt: "2026-11-27T08:05:00Z", FinishedAt: "2026-11-27T08:05:01Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}},
	}
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "RESULT: done\n", nil
}

func (s *splitCarvingSteps) Gate(_ context.Context, _ Candidate) (GateOutcome, error) {
	return GateOutcome{Outcome: GatePassed}, nil
}

func TestSplitPartTakesWholePassesStraightToLand(t *testing.T) {
	f, l, baseSteps, _, wholeID, _, donorSHA := setupSplitTakesWholeFixture(t)
	ctx := context.Background()

	var judgeCalls int
	var evaluatedHead string
	steps := &splitCarvingSteps{
		workflowSteps: baseSteps.workflowSteps,
		f:             f,
		responses: func(req run.Request, call int, head, tree string) item.JudgeOutput {
			judgeCalls++
			evaluatedHead = head
			return item.JudgeOutput{
				EvaluatedHead: head,
				EvaluatedTree: tree,
				Outcome:       "pass",
				Verdicts:      partVerdicts(t, f, req.ItemID, item.VerdictDone, nil),
			}
		},
	}
	l.cfg.Steps = steps

	result := &LoopResult{}
	for i := 0; i < 4; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}

	if judgeCalls != 1 {
		t.Fatalf("judgeCalls = %d, want 1", judgeCalls)
	}
	if steps.workCalls[wholeID] != 0 {
		t.Fatalf("workCalls[%s] = %d, want 0 (no build step)\ntrace:\n%s", wholeID, steps.workCalls[wholeID], strings.Join(steps.trace, "\n"))
	}
	if evaluatedHead != donorSHA {
		t.Fatalf("evaluatedHead = %s, want donorSHA %s", evaluatedHead, donorSHA)
	}

	var state string
	outcomes := map[string]string{}
	fixtureRead(t, f, func(r *store.Reader) error {
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, wholeID).Scan(&state); err != nil {
			return err
		}
		rows, err := r.Query(`SELECT node_id, outcome FROM node_runs WHERE item_id=? AND attempt=1`, wholeID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var nid, out string
			if err := rows.Scan(&nid, &out); err != nil {
				return err
			}
			outcomes[nid] = out
		}
		return rows.Err()
	})
	if state != StateLanded {
		var evs []string
		fixtureRead(t, f, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT kind, object_id, payload FROM events ORDER BY seq`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var k, oid, p string
				rows.Scan(&k, &oid, &p)
				if strings.Contains(k, "refusal") || oid == wholeID || strings.Contains(oid, wholeID) {
					evs = append(evs, k+" ("+oid+"): "+p)
				}
			}
			return nil
		})
		t.Fatalf("whole part state = %q, want %q; outcomes=%+v\nevents:\n%s", state, StateLanded, outcomes, strings.Join(evs, "\n"))
	}
	if outcomes["plan"] != "skipped" || outcomes["work"] != "skipped" || outcomes["verify"] != "passed" {
		t.Fatalf("outcomes = %+v, want plan=skipped, work=skipped, verify=passed", outcomes)
	}
}

func TestSplitPartTakesWholeRefusalReturnsToBuildAndPasses(t *testing.T) {
	f, l, baseSteps, _, wholeID, _, donorSHA := setupSplitTakesWholeFixture(t)
	ctx := context.Background()

	var judgeCalls int
	var firstEvaluatedHead string
	steps := &splitCarvingSteps{
		workflowSteps: baseSteps.workflowSteps,
		f:             f,
		responses: func(req run.Request, call int, head, tree string) item.JudgeOutput {
			judgeCalls++
			if judgeCalls == 1 {
				firstEvaluatedHead = head
				return item.JudgeOutput{
					EvaluatedHead: head,
					EvaluatedTree: tree,
					Outcome:       "fail",
					Verdicts: partVerdicts(t, f, req.ItemID, item.VerdictNotDone, item.VerdictFindings{
						{Kind: "regression", Case: "donor broke edge case", Paths: []string{"file:donor_feature.txt"}},
					}),
				}
			}
			return item.JudgeOutput{
				EvaluatedHead: head,
				EvaluatedTree: tree,
				Outcome:       "pass",
				Verdicts:      partVerdicts(t, f, req.ItemID, item.VerdictDone, nil),
			}
		},
	}
	l.cfg.Steps = steps

	result := &LoopResult{}
	for i := 0; i < 12; i++ {
		if _, err := l.pass(ctx, result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
		var st string
		fixtureRead(t, f, func(r *store.Reader) error {
			return r.QueryRow(`SELECT state FROM items WHERE item_id=?`, wholeID).Scan(&st)
		})
		if st == StateLanded {
			break
		}
	}

	if judgeCalls != 2 {
		var evs []string
		fixtureRead(t, f, func(r *store.Reader) error {
			rows, err := r.Query(`SELECT kind, object_id, payload FROM events ORDER BY seq`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var k, oid, p string
				rows.Scan(&k, &oid, &p)
				if strings.Contains(k, "refusal") || strings.Contains(k, "hold") || oid == wholeID || strings.Contains(oid, wholeID) {
					evs = append(evs, k+" ("+oid+"): "+p)
				}
			}
			return nil
		})
		t.Fatalf("judgeCalls = %d, want 2\ntrace:\n%s\nevents:\n%s", judgeCalls, strings.Join(steps.trace, "\n"), strings.Join(evs, "\n"))
	}
	if steps.workCalls[wholeID] != 1 {
		t.Fatalf("workCalls[%s] = %d, want 1", wholeID, steps.workCalls[wholeID])
	}
	if firstEvaluatedHead != donorSHA {
		t.Fatalf("firstEvaluatedHead = %s, want donorSHA %s", firstEvaluatedHead, donorSHA)
	}

	var state string
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(`SELECT state FROM items WHERE item_id=?`, wholeID).Scan(&state)
	})
	if state != StateLanded {
		t.Fatalf("whole part state = %q, want %q", state, StateLanded)
	}
}
