package drain

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestTriageQuestionAfterLandingWaitsOnOwnerWithText(t *testing.T) {
	f, l := triageFixture(t)
	d := findTriagePlan(t, l, "item-request").decision
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	target := l.laneTarget(d.LaneID)
	runGit(t, target, "reset", "--hard", base)
	body := triageBlock + strings.ReplaceAll(triageBlock, "item-declared", "item-request") + `
## Open questions

1. 💬 **OQ-1: Which behavior should the requested feature support?**

   <!-- vantage: question id=OQ-1 -->

   The stored request does not identify the behavior to build.
`
	writeForkFile(t, filepath.Join(target, "docs/request.md"), body)
	runGit(t, target, "add", "docs")
	runGit(t, target, "commit", "-qm", "triage owner question")
	c := Candidate{ItemID: d.ItemID, RunID: d.RunRequest.RunID, Worktree: target, BaseSHA: base, HeadSHA: runGit(t, target, "rev-parse", "HEAD"), BlockConflict: d.BlockConflict}
	if detail, err := l.validateTriage(context.Background(), c); err != nil || detail != "" {
		t.Fatalf("question declaration refused: %s %v", detail, err)
	}
	runGit(t, f.root, "merge", "--ff-only", c.HeadSHA)
	if err := l.answerBlock(context.Background(), c); err != nil {
		t.Fatal(err)
	}
	l.questions = &VantageQuestions{Root: f.root, Command: []string{"sh", "-c", `printf '%s' '{"index":{"documents":[{"path":"docs/request.md","questions":[{"id":"OQ-1","state":"open","title":"Which behavior should the requested feature support?"}]}]}}'`}}
	next := findTriagePlan(t, l, d.ItemID).decision
	if next.Reason != ReasonQuestionOpen || next.RunRequest != nil {
		t.Fatalf("question did not hold: %+v", next)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		answer, err := Why(r, Pass{AvailableLanes: &l.cfg.Lanes}, l.cfg.Policy, l.questions)
		if err != nil {
			return err
		}
		for _, row := range answer.Rows {
			if row.ItemID == d.ItemID && (!strings.Contains(row.Mover, "owner") || !strings.Contains(row.Why, "Which behavior")) {
				t.Errorf("question/mover invisible: %+v", row)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestThinTriageRequestCanReachTheDesigner(t *testing.T) {
	_, l := triageFixture(t)
	d := findTriagePlan(t, l, "item-request").decision
	recorded := stagedItem{ItemID: d.ItemID, BodyRef: d.BlockConflict.BodyRef, AllowedPaths: []string{"docs/request.md"}}
	values, err := briefValuesFor(recorded, StageSpec{RunID: d.RunRequest.RunID, BlockConflict: d.BlockConflict}, "target", "lane worktree", "base", "scratch", l.cfg.Now())
	if err != nil {
		t.Fatal(err)
	}
	for _, v := range values {
		if v.placeholder == "CRITERION_1" && !strings.Contains(v.text, "None stored") {
			t.Fatalf("invented criteria: %s", v.text)
		}
	}
}
