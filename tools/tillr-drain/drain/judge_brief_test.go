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
			payload := refusalPayload{ItemID: itemID, Step: step, Detail: detail}
			if step == ReasonEvalFailed {
				// Only one of this refusal's criteria had classifications. History must
				// keep the complete detail, not just the classified subset.
				payload.VerifyFeedback = &verifyFeedback{Findings: []verifyFindingRecord{{Criterion: "c2", Reason: "another compatibility finding", Findings: item.VerdictFindings{
					{Kind: "gap", Case: "The case is missing.", Paths: []string{"file:candidate.txt"}},
				}}}}
			}
			return appendEffect(tx, RefusalKind, id, payload, l.cfg.Now())
		})
	}
	const heading = "## Earlier verifier refusals"
	brief := stage()
	if strings.Contains(brief, heading) {
		t.Fatal("history should not appear before any candidate refusal")
	}
	first := `c1: first finding\nc2: preserves \u007b\u007bLITERAL}}`
	record("first", c.ItemID, RefusalVerifyNotDone, "c1: first finding\nc2: preserves {{LITERAL}}")
	record("other-item", "item-b", RefusalVerifyNotDone, "not this item")
	record("gate", c.ItemID, RefusalGate, "not a verifier finding")
	record("unreadable", c.ItemID, ReasonJudgeUnreadable, "not a valid verdict")
	brief = stage()
	for _, want := range []string{
		heading, first,
		"State all remaining in-scope findings at once in this verdict, including multiple findings under the same criterion.",
		`examined: {"whole": true}`,
		"earlier_finding_fates: when earlier verifier refusals are shown below",
	} {
		if !strings.Contains(strings.Join(strings.Fields(brief), " "), want) {
			t.Errorf("brief after 1 refusal lacks %q:\n%s", want, brief)
		}
	}
	if !strings.Contains(brief, "Changed paths ("+c.BaseSHA+".."+c.HeadSHA+"):\ncandidate.txt\n") {
		t.Fatalf("judge did not receive the pinned changed paths:\n%s", brief)
	}
	candDir := filepath.Join(run.DurableDir(l.laneDirPath(c.LaneID), receipt.Request.RunID), "cand")
	wantCloneCmd := `git clone --shared --no-checkout . "` + candDir + `" && git -C "` + candDir + `" checkout --detach ` + c.HeadSHA
	for _, want := range []string{
		wantCloneCmd,
		"Do not export GIT_DIR or GIT_WORK_TREE",
		"Name any changed path that the item's words do not account for.",
		"A refusal must rest on the item's words or criteria or a regression this change introduces in existing behavior.",
		"Report defects outside that scope as proposed follow-ups, not refusals.",
		"If only the owner can decide whether a finding is in scope, use a blocked verdict with waiter owner and state the question and options.",
	} {
		if !strings.Contains(strings.Join(strings.Fields(brief), " "), want) {
			t.Errorf("judge brief lacks %q", want)
		}
	}
	cloneCmd := exec.Command("sh", "-c", wantCloneCmd)
	cloneCmd.Dir = c.Worktree
	if out, err := cloneCmd.CombinedOutput(); err != nil {
		t.Fatalf("recommended candidate clone command failed: %v\n%s", err, out)
	}
	gotHead, err := exec.Command("git", "-C", candDir, "rev-parse", "HEAD").Output()
	if err != nil {
		t.Fatalf("git rev-parse HEAD in cloned candidate: %v", err)
	}
	if strings.TrimSpace(string(gotHead)) != c.HeadSHA {
		t.Fatalf("cloned candidate HEAD = %q, want %q", strings.TrimSpace(string(gotHead)), c.HeadSHA)
	}

	// History is item-wide, even when an operator retry begins a new triage cycle.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return appendEffectOn(tx, verifyTriageResetKind, "item", c.ItemID, struct{}{}, l.cfg.Now())
	})
	record("second", c.ItemID, ReasonEvalFailed, "c1: compatibility judge finding\nc2: another compatibility finding")
	brief = stage()
	for _, want := range []string{
		heading, first, `c1: compatibility judge finding\nc2: another compatibility finding`,
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

type judgeBriefShapeSteps struct {
	*judgeRouteSteps
	judgeCalls   int
	judgeResumed bool
	briefJSON    string
}

func extractBriefJSONTemplate(t *testing.T, brief string) string {
	t.Helper()
	const fence = "```json\n"
	start := strings.Index(brief, fence)
	if start < 0 {
		t.Fatalf("judge brief missing fenced json verdict example:\n%s", brief)
	}
	rest := brief[start+len(fence):]
	end := strings.Index(rest, "\n```")
	if end < 0 {
		t.Fatalf("judge brief has unclosed json fence:\n%s", brief)
	}
	return rest[:end]
}

func (s *judgeBriefShapeSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if req.Node != "judge" && !strings.HasSuffix(req.RunID, "-judge") && req.NodeID != "evaluate" && !strings.HasSuffix(req.NodeID, "/evaluate") {
		return s.judgeRouteSteps.Run(ctx, req)
	}
	s.judgeCalls++
	if req.Resume {
		s.judgeResumed = true
	}
	briefBytes, err := os.ReadFile(filepath.Join(s.f.laneRoot, req.LaneID, BriefFile))
	if err != nil {
		return run.Record{}, "", err
	}
	brief := string(briefBytes)
	for _, want := range []string{
		`unknown keys such as "criteria" are rejected by the validator`,
		`Do not include actor, judged_actor, criteria, or any other unlisted key; unknown fields and duplicate keys are rejected.`,
	} {
		if !strings.Contains(strings.Join(strings.Fields(brief), " "), want) {
			s.t.Errorf("judge brief lacks %q", want)
		}
	}
	// Fill only the placeholder values in the brief's own JSON template; keep every
	// JSON field name verbatim so any brief/reader field mismatch fails immediately.
	tmpl := extractBriefJSONTemplate(s.t, brief)
	filled := strings.NewReplacer(
		"<criterion_id>", "c1",
		"<criterion_hash>", item.CriterionHash("the first criterion of item-a"),
		"<claim_class>", item.ClaimVerification,
		"<reason>", "verified against candidate diff",
	).Replace(tmpl)
	s.briefJSON = filled

	// Let the base stub record the run, then overwrite eval.output with the
	// verbatim brief-shaped JSON.
	rec, log, err := s.judgeRouteSteps.Run(ctx, req)
	if err != nil {
		return rec, log, err
	}
	evalPath := filepath.Join(run.DurableDir(filepath.Join(s.f.laneRoot, req.LaneID), req.RunID), "eval.output")
	writeForkFile(s.t, evalPath, filled)
	return rec, log, nil
}

func TestJudgeBriefVerdictShapeAcceptedOnFirstTry(t *testing.T) {
	f, l, baseSteps := judgeRouteFixture(t, "pass")
	steps := &judgeBriefShapeSteps{judgeRouteSteps: baseSteps}
	l.cfg.Steps = steps

	result := &LoopResult{}
	for i := 0; i < 10; i++ {
		if _, err := l.pass(context.Background(), result); err != nil {
			t.Fatal(err)
		}
		drainOwned(t, l, result)
	}
	if steps.judgeCalls != 1 {
		t.Fatalf("judgeCalls = %d, want 1 (verdict should be read without a resumed judge session)", steps.judgeCalls)
	}
	if steps.judgeResumed {
		t.Fatal("judge session was unexpectedly resumed")
	}
	if result.Rejected != 0 || len(result.RefusedSteps) != 0 {
		t.Fatalf("unexpected refusals: %+v", result)
	}
	if steps.briefJSON == "" {
		t.Fatal("judge never produced a verdict from the brief template")
	}

	// Verify the reader accepts the brief's shape directly and still rejects an
	// unlisted "criteria" key.
	var obj map[string]any
	if err := json.Unmarshal([]byte(steps.briefJSON), &obj); err != nil {
		t.Fatalf("brief template did not produce valid JSON: %v\n%s", err, steps.briefJSON)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var state string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id='item-a'`).Scan(&state); err != nil {
			return err
		}
		if state != StateLanded {
			t.Errorf("item state = %s, want %s", state, StateLanded)
		}
		binding, err := item.ReadEvaluatorBinding(r, "item-a")
		if err != nil {
			return err
		}
		opts := item.JudgeOptions{
			EvaluatorBinding: binding,
			ItemID:           "item-a",
			Head:             obj["evaluated_head"].(string),
			Tree:             obj["evaluated_tree"].(string),
			Actor:            "run=judge-check;session=judge-session;route=reviewer",
			JudgedActor:      "run=worker-check;session=worker-session",
			At:               l.cfg.Now(),
		}
		checked, err := item.CheckJudge(context.Background(), r, []byte(steps.briefJSON), opts)
		if err != nil {
			t.Fatalf("CheckJudge rejected verdict in brief shape: %v\n%s", err, steps.briefJSON)
		}
		if checked.Outcome != "pass" || len(checked.Output.Verdicts) != 1 {
			t.Fatalf("checked = %+v, want pass with 1 verdict", checked)
		}
		obj["criteria"] = obj["verdicts"]
		withCriteria, err := json.Marshal(obj)
		if err != nil {
			return err
		}
		if _, err := item.CheckJudge(context.Background(), r, withCriteria, opts); err == nil || !strings.Contains(err.Error(), `unknown field "criteria"`) {
			t.Fatalf("CheckJudge with extra criteria field = %v, want unknown field \"criteria\" refusal", err)
		}
		return nil
	})
}
