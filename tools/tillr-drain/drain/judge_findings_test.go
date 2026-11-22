package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestJudgeBriefRequestsClassifiedFindings(t *testing.T) {
	_, l, c := heldCandidateFixture(t)
	receipt := JudgeReceipt{Request: run.Request{RunID: "findings-judge"}, Options: item.JudgeOptions{Head: c.HeadSHA}}
	if err := l.stageJudge(context.Background(), c, &receipt, "judge-session"); err != nil {
		t.Fatal(err)
	}
	raw, err := os.ReadFile(filepath.Join(l.laneDirPath(c.LaneID), BriefFile))
	if err != nil {
		t.Fatal(err)
	}
	brief := strings.Join(strings.Fields(string(raw)), " ")
	for _, want := range []string{
		"Each not-done entry must include a non-empty findings array",
		"kind (gap, regression, or unasked-change)",
		"case (one sentence)",
		"paths (file:<repo-relative-path> or file:<repo-relative-path>:<line> locators)",
		"A gap means the named case behaves as on the base, without meeting the request and without becoming worse.",
		"A regression means the named case behaves worse than on the base, or something that passes on the base fails.",
		"An unasked-change changes something no criterion needs.",
		"Give evidence for each classification in reason and evidence; for a gap, compare the named case with the base and show it behaves the same.",
		"If a finding cannot be classified, report it as a regression.",
	} {
		if !strings.Contains(brief, want) {
			t.Errorf("judge brief lacks %q", want)
		}
	}
}

// A compatibility review holds before the gate and records the findings on its
// refusal, not in criterion state or a separate item.verdict.
func TestJudgeClassifiedFindingsKeepExistingDisposition(t *testing.T) {
	for _, kind := range []string{"", "gap", "regression", "unasked-change"} {
		t.Run(kind, func(t *testing.T) {
			f, l, steps := judgeRouteFixture(t, "not-done")
			entry := item.JudgeEntry{
				CriterionID: "c1", CriterionHash: item.CriterionHash("the first criterion of item-a"),
				Verdict: item.VerdictNotDone, ClaimClass: item.ClaimVerification,
				Reason: "the case is not covered", Evidence: []string{"file:tools/item-a/result.txt:1"},
			}
			if kind != "" {
				entry.Findings = item.VerdictFindings{{Kind: kind, Case: "The case is not covered.", Paths: []string{"file:tools/item-a/result.txt:1"}}}
			}
			steps.entries = []item.JudgeEntry{entry}
			result := &LoopResult{}
			for i := 0; i < 3; i++ {
				if _, err := l.pass(context.Background(), result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, result)
			}
			if !reflect.DeepEqual(steps.order, []string{"worker", "judge"}) {
				t.Fatalf("unexpected route: %v", steps.order)
			}
			d := decisionFor(t, l, "item-a").decision
			if d.Action != ActionHold || d.Reason != ReasonEvalFailed {
				t.Fatalf("not-done disposition changed: %+v", d)
			}
			fixtureRead(t, f, func(r *store.Reader) error {
				var verdicts, items int
				if err := r.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='item.verdict'`).Scan(&verdicts); err != nil {
					return err
				}
				if err := r.QueryRow(`SELECT COUNT(*) FROM items`).Scan(&items); err != nil {
					return err
				}
				if verdicts != 0 || items != 2 {
					t.Fatalf("unexpected verdicts or split items: %d, %d", verdicts, items)
				}
				var payload []byte
				if err := r.QueryRow(`SELECT payload FROM events WHERE kind='drain.refusal' AND object_id=?`, steps.runs[1].RunID).Scan(&payload); err != nil {
					return err
				}
				var refusal refusalPayload
				if err := json.Unmarshal(payload, &refusal); err != nil {
					return err
				}
				var want *verifyFeedback
				if kind != "" {
					want = &verifyFeedback{Findings: []verifyFindingRecord{{Criterion: entry.CriterionID, Reason: entry.Reason, Findings: entry.Findings}}}
				}
				if refusal.Step != ReasonEvalFailed || refusal.Detail != entry.Reason || !reflect.DeepEqual(refusal.VerifyFeedback, want) {
					t.Fatalf("compatibility refusal differs: %s", payload)
				}
				return nil
			})
		})
	}
}
