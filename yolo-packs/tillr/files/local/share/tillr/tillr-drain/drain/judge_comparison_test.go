package drain

import (
	"context"
	"encoding/json"
	"path/filepath"
	"reflect"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestJudgeComparisonRecordsFatesExaminedAndSignalsWithoutRouting(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	ctx := context.Background()
	c.Evaluator = &run.Request{RunID: "eval-1", ItemID: c.ItemID, LaneID: c.LaneID, NodeID: "verify"}
	c.Judge = &JudgeReceipt{Request: run.Request{RunID: "eval-1", ItemID: c.ItemID, LaneID: c.LaneID, Backend: "reviewer"}}
	tree1 := runGit(t, c.Worktree, "rev-parse", c.HeadSHA+"^{tree}")

	output1 := item.JudgeOutput{
		EvaluatedHead: c.HeadSHA,
		EvaluatedTree: tree1,
		Outcome:       "fail",
		Verdicts: []item.JudgeEntry{
			{
				CriterionID:   "c1",
				CriterionHash: item.CriterionHash("the first criterion of item-a"),
				Verdict:       item.VerdictNotDone,
				ClaimClass:    item.ClaimVerification,
				Reason:        "missing validation in candidate.txt",
				Evidence:      []string{"file:candidate.txt:1"},
				Findings:      item.VerdictFindings{{Kind: "gap", Case: "Empty input is not validated.", Paths: []string{"file:candidate.txt:1"}}},
			},
		},
		Examined: &item.VerdictExamined{Whole: false, UnexaminedPaths: []string{"file:unexamined.go"}},
	}
	res1 := &LoopResult{}
	if err := l.settleJudgeFailure(ctx, c, output1.Verdicts[0], output1, item.JudgeOptions{Tree: tree1}, res1); err != nil {
		t.Fatal(err)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		var payload []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind='drain.refusal' AND object_id=?`, c.RunID).Scan(&payload); err != nil {
			return err
		}
		var refusal refusalPayload
		if err := json.Unmarshal(payload, &refusal); err != nil {
			return err
		}
		if refusal.VerifyFeedback == nil || refusal.VerifyFeedback.Examined == nil || refusal.VerifyFeedback.Examined.Whole {
			t.Fatalf("first refusal missing partial examined: %s", payload)
		}
		if refusal.VerdictComparison == nil || !refusal.VerdictComparison.HasUnexaminedPaths || refusal.VerdictComparison.NewFindingAfterFix || refusal.VerdictComparison.RepairGrowing {
			t.Fatalf("unexpected first refusal comparison: %+v", refusal.VerdictComparison)
		}
		return nil
	})

	// Second refusal: candidate grows by adding extra.go, earlier finding is marked fixed,
	// and a new finding is reported at file:extra.go:2 along with an unexamined path.
	// S2, S3, and S4 all hold, yet routing stays unchanged (no split call or hold).
	writeForkFile(t, filepath.Join(c.Worktree, "extra.go"), "package main\nfunc extra() {}\nfunc more() {}\n")
	runGit(t, c.Worktree, "add", "extra.go")
	runGit(t, c.Worktree, "commit", "-qm", "add extra")

	c2 := c
	c2.RunID = c.RunID + "-retry"
	c2.HeadSHA = runGit(t, c.Worktree, "rev-parse", "HEAD")
	c2.Evaluator = &run.Request{RunID: "eval-2", ItemID: c.ItemID, LaneID: c.LaneID, NodeID: "verify"}
	c2.Judge = &JudgeReceipt{Request: run.Request{RunID: "eval-2", ItemID: c.ItemID, LaneID: c.LaneID, Backend: "reviewer"}}
	tree2 := runGit(t, c.Worktree, "rev-parse", c2.HeadSHA+"^{tree}")

	fates := item.EarlierFindingFates{
		{CriterionID: "c1", Finding: "Empty input is not validated.", Paths: []string{"file:candidate.txt:1"}, Fate: "fixed"},
	}
	output2 := item.JudgeOutput{
		EvaluatedHead: c2.HeadSHA,
		EvaluatedTree: tree2,
		Outcome:       "fail",
		Verdicts: []item.JudgeEntry{
			{
				CriterionID:   "c1",
				CriterionHash: item.CriterionHash("the first criterion of item-a"),
				Verdict:       item.VerdictNotDone,
				ClaimClass:    item.ClaimVerification,
				Reason:        "new issue in extra.go",
				Evidence:      []string{"file:extra.go:2"},
				Findings:      item.VerdictFindings{{Kind: "regression", Case: "Retry fails in extra.go.", Paths: []string{"file:extra.go:2"}}},
			},
		},
		EarlierFindingFates: fates,
		Examined:            &item.VerdictExamined{Whole: false, UnexaminedPaths: []string{"file:other.go"}},
	}
	res2 := &LoopResult{}
	if err := l.settleJudgeFailure(ctx, c2, output2.Verdicts[0], output2, item.JudgeOptions{Tree: tree2}, res2); err != nil {
		t.Fatal(err)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		var faults, items int
		if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.fault'`).Scan(&faults); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&items); err != nil {
			return err
		}
		if faults != 0 || items != 2 {
			t.Fatalf("advisory S2/S3/S4 must not hold or split: faults=%d items=%d", faults, items)
		}
		var payload []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind='drain.refusal' AND object_id=?`, c2.RunID).Scan(&payload); err != nil {
			return err
		}
		var refusal refusalPayload
		if err := json.Unmarshal(payload, &refusal); err != nil {
			return err
		}
		if refusal.VerifyFeedback == nil || !reflect.DeepEqual(refusal.VerifyFeedback.EarlierFindingFates, fates) {
			t.Fatalf("second refusal lost earlier_finding_fates: %s", payload)
		}
		comp := refusal.VerdictComparison
		if comp == nil {
			t.Fatalf("second refusal missing verdict_comparison: %s", payload)
		}
		if comp.PreviousRunID != c.RunID || !comp.AllEarlierFixed || !comp.NewFindingAtNewPlace || !comp.NewFindingAfterFix || !comp.RepairGrowing || !comp.HasUnexaminedPaths {
			t.Fatalf("unexpected verdict_comparison: %+v", comp)
		}
		if comp.FilesDelta == nil || *comp.FilesDelta <= 0 || comp.AddedLinesDelta == nil || *comp.AddedLinesDelta <= 0 {
			t.Fatalf("expected positive file and line deltas, got files=%v lines=%v", comp.FilesDelta, comp.AddedLinesDelta)
		}
		return nil
	})
}
