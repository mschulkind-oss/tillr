package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestCompatibilityJudgeRetainsAllRefusalFindings(t *testing.T) {
	f, l, s := judgeRouteFixture(t, "not-done")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_criteria(item_id,criterion_id,text,state,claim_class) VALUES('item-a','c2','second criterion','not-done','verification-only')`)
		return err
	})
	s.entries = []item.JudgeEntry{
		{CriterionID: "c2", CriterionHash: item.CriterionHash("second criterion"), Verdict: item.VerdictNotDone, ClaimClass: item.ClaimVerification, Reason: "second finding", Evidence: []string{"diff:candidate.diff"}},
		{CriterionID: "c1", CriterionHash: item.CriterionHash("the first criterion of item-a"), Verdict: item.VerdictNotDone, ClaimClass: item.ClaimVerification, Reason: "first finding", Evidence: []string{"diff:candidate.diff"}},
	}
	result := &LoopResult{}
	for i := 0; i < 3; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		detail, err := judgeFailureDetail(r, "item-a", ReasonEvalFailed)
		if err == nil && detail != "c1: first finding\nc2: second finding" {
			t.Fatalf("refusal detail = %q, want both findings", detail)
		}
		return err
	})
}

func TestJudgeBriefScopeAndRefusalHistory(t *testing.T) {
	f, l, c := heldCandidateFixture(t)
	ctx := context.Background()
	receipt := JudgeReceipt{Request: run.Request{RunID: "next-judge"}, Options: item.JudgeOptions{Head: c.HeadSHA}}
	path := filepath.Join(l.laneDirPath(c.LaneID), BriefFile)
	stage := func() string {
		t.Helper()
		if err := l.stageJudge(ctx, c, &receipt, "judge-session"); err != nil {
			t.Fatal(err)
		}
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(raw)
	}
	record := func(id, itemID, step, detail string) {
		t.Helper()
		fixtureWrite(t, f, func(tx *store.Tx) error {
			return appendEffect(tx, RefusalKind, id, refusalPayload{ItemID: itemID, Step: step, Detail: detail}, l.cfg.Now())
		})
	}
	first := `c1: first finding\nc2: preserves \u007b\u007bLITERAL}}`
	record("first", c.ItemID, RefusalVerifyNotDone, "c1: first finding\nc2: preserves {{LITERAL}}")
	record("other-item", "item-b", RefusalVerifyNotDone, "not this item")
	record("gate", c.ItemID, RefusalGate, "not a verifier finding")
	record("unreadable", c.ItemID, ReasonJudgeUnreadable, "not a valid verdict")
	const heading = "## Earlier verifier refusals"
	brief := stage()
	if strings.Contains(brief, heading) || strings.Contains(brief, "first finding") {
		t.Fatal("history should not appear after only one candidate refusal")
	}
	for _, want := range []string{
		"A refusal must rest on the item's words or criteria or a regression this change introduces in existing behavior.",
		"Report defects outside that scope as proposed follow-ups, not refusals.",
		"If only the owner can decide whether a finding is in scope, use a blocked verdict with waiter owner and state the question and options.",
	} {
		if !strings.Contains(strings.Join(strings.Fields(brief), " "), want) {
			t.Errorf("judge brief lacks %q", want)
		}
	}

	// History is item-wide, even when an operator retry begins a new triage cycle.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffectOn(tx, verifyTriageResetKind, "item", c.ItemID, struct{}{}, l.cfg.Now())
	})
	record("second", c.ItemID, ReasonEvalFailed, "c1: compatibility judge finding")
	brief = stage()
	for _, want := range []string{
		heading, first, "c1: compatibility judge finding",
		"State all remaining in-scope findings at once in this verdict, including multiple findings under the same criterion.",
	} {
		if !strings.Contains(strings.Join(strings.Fields(brief), " "), want) {
			t.Errorf("next verifier lacks %q:\n%s", want, brief)
		}
	}
	record("third", c.ItemID, RefusalVerifyNotDone, "c1: third finding")
	record("fourth", c.ItemID, RefusalVerifyNotDone, "c1: fourth finding")
	brief = stage()
	offset := 0
	for _, want := range []string{first, "c1: compatibility judge finding", "c1: third finding", "c1: fourth finding"} {
		i := strings.Index(brief[offset:], want)
		if i < 0 {
			t.Fatalf("missing or out-of-order finding %q:\n%s", want, brief)
		}
		offset += i + len(want)
	}
	for _, unwanted := range []string{"not this item", "not a verifier finding", "not a valid verdict"} {
		if strings.Contains(brief, unwanted) {
			t.Errorf("judge brief includes unrelated detail %q", unwanted)
		}
	}
	preflight, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "preflight.sh"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sh", "-c", `PROG=fixture; . "$1"; preflight_brief "$2"`, "fixture", preflight, path)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("adapter preflight: %v: %s", err, out)
	}
}
