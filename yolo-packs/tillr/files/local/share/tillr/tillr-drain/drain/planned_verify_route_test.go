package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

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
