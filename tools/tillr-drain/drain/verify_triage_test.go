package drain

import (
	"context"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestVerifyFeedbackKeepsOnlyUnansweredFindings(t *testing.T) {
	l, _, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
		return &verifyReturnSteps{workflowSteps: w}
	})
	ctx := context.Background()
	old := &verifyFeedback{Findings: []verifyFindingRecord{
		{Criterion: "c1", Reason: "answered", Snapshot: "same"},
		{Criterion: "c2", Reason: "repeat\nReproduce: run a check", Snapshot: "same"},
	}}
	if err := appendJudgment(ctx, f.storePath, RefusalKind, "run", "earlier", refusalPayload{
		ItemID: "item-a", Step: RefusalVerifyNotDone, VerifyFeedback: old,
	}, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	current := &verifyFeedback{Passed: []string{"c1"}, Findings: []verifyFindingRecord{
		{Criterion: "c2", Reason: old.Findings[1].Reason, Snapshot: "same"},
		{Criterion: "c3", Reason: "new", Snapshot: "another"},
	}}
	check := func(wantRepeat string) {
		t.Helper()
		fixtureRead(t, f, func(r *store.Reader) error {
			detail, repeat, err := collectVerifyFeedback(r, "item-a", "latest", "writer", current)
			if err == nil && (detail != old.Findings[1].text()+"\nc3: new" || repeat != wantRepeat) {
				t.Fatalf("detail=%q repeat=%q, wanted only c2 and c3, repeat=%q", detail, repeat, wantRepeat)
			}
			return err
		})
	}
	check("") // A verifier cannot convict a writer who was not given this finding.
	if err := appendJudgment(ctx, f.storePath, dispatchStartedKind, "run", "writer", pendingWork{
		Decision: Decision{Returned: &ReturnedEnding{Cause: RefusalVerifyNotDone, RunID: "earlier", Detail: old.Findings[1].text()}},
	}, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	check(old.Findings[1].text()) // The additional new finding neither causes nor masks the repeat.
	current.Findings[0].Snapshot = "changed"
	check("")
}

func TestCountedVerifyHoldIsReleasedAfterUpgrade(t *testing.T) {
	l, s, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
		return &verifyReturnSteps{workflowSteps: w, verdict: verifyFinding}
	})
	runVerifyReturn(t, f, l, s, func() bool { return s.verifies == 1 })
	fixtureWrite(t, f, func(tx *store.Tx) error {
		// The historical payload, as recorded by the old count rule.
		return l.recordItemFaultTx(tx, ItemFault{ItemID: "item-a", RunID: s.verifiedRun, LaneID: "lane-1",
			Reason: reasonVerifyTriage, Detail: "Three verifier refusals: old evidence"})
	})
	runVerifyReturn(t, f, l, s, func() bool { return s.lands == 1 })
	if n := storeCount(t, f, `SELECT COUNT(*) FROM events WHERE kind=? AND object_id='item-a'`, itemFaultClearedKind); n != 1 {
		t.Fatalf("count-only holds cleared: %d, want 1", n)
	}
}

func TestVerifyFindingSnapshotUsesCitedContent(t *testing.T) {
	f, l, _ := plannedFixture(t)
	ctx := context.Background()
	commit := func(path, text string) string {
		writeForkFile(t, f.root+"/"+path, text)
		runGit(t, f.root, "add", path)
		runGit(t, f.root, "commit", "-qm", "change "+path)
		return runGit(t, f.root, "rev-parse", "HEAD^{tree}")
	}
	a := commit("cited.txt", "original")
	b := commit("unrelated.txt", "unrelated change")
	c := commit("cited.txt", "possible repair")
	snapshot := func(tree string, evidence ...string) string {
		return l.verifyFindingSnapshot(ctx, Candidate{Worktree: f.root}, tree, evidence)
	}
	first := snapshot(a, "file:cited.txt:1")
	if first == "" || first != snapshot(b, "file:cited.txt:2") || first == snapshot(c, "path:./cited.txt") {
		t.Fatal("snapshot did not follow the cited blob across unrelated and relevant changes")
	}
	if first := snapshot(a, "node:work"); first == "" || first == snapshot(b, "node:work") {
		t.Fatal("unscoped evidence did not compare the whole candidate tree")
	}
}

// Assert the historical JSON can still be loaded without a structured snapshot.
func TestLegacyVerifyFeedbackIsReturnedVerbatim(t *testing.T) {
	l, _, f := verifyReturnFixture(t, false, func(w *workflowSteps) *verifyReturnSteps {
		return &verifyReturnSteps{workflowSteps: w}
	})
	detail := "c1: old finding\nReproduce: old command"
	if err := appendJudgment(context.Background(), f.storePath, RefusalKind, "run", "legacy", refusalPayload{
		ItemID: "item-a", Step: RefusalVerifyNotDone, Detail: detail,
	}, l.cfg.Now()); err != nil {
		t.Fatal(err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		got, repeat, err := collectVerifyFeedback(r, "item-a", "new", "writer", &verifyFeedback{})
		if err == nil && (got != detail || repeat != "") {
			t.Fatalf("detail=%q repeat=%q", got, repeat)
		}
		return err
	})
}
