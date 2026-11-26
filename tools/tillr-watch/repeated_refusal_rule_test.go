package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainRepeatedRefusalRule(t *testing.T) {
	const (
		ruleID   = "drain-repeated-refusal"
		ruleFile = "rules/drain-repeated-refusal.toml"
	)

	rawRuleFile, err := os.ReadFile(ruleFile)
	if err != nil {
		t.Fatalf("read %s: %v", ruleFile, err)
	}
	if !strings.Contains(string(rawRuleFile), "RepeatedRefusalThreshold") {
		t.Fatalf("%s must name RepeatedRefusalThreshold as the source of its count", ruleFile)
	}
	fileRules, err := watch.LoadRuleDocument(ruleFile, rawRuleFile)
	if err != nil {
		t.Fatalf("LoadRuleDocument(%s): %v", ruleFile, err)
	}
	if len(fileRules) != 1 || fileRules[0].ID != ruleID {
		t.Fatalf("expected %s to define solely %q, got %+v", ruleFile, ruleID, fileRules)
	}

	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(rules, func(r watch.Rule) bool { return r.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule in config/tillr/watch", ruleID)
	}
	rule := rules[idx]
	if reason := rule.UnevaluatedReason(); reason != "" {
		t.Fatalf("rule %s is unevaluated: %s", ruleID, reason)
	}
	if rule.Scope != "core" || rule.Severity != "warning" || rule.Delivery.Receiver != "foreground" {
		t.Fatalf("unexpected %s metadata: scope=%q severity=%q receiver=%q", ruleID, rule.Scope, rule.Severity, rule.Delivery.Receiver)
	}
	if rule.When.Pattern != watch.ConsecutiveCount ||
		rule.When.ForEach != "refusal_causes" ||
		rule.When.GroupBy != "cause" ||
		!slices.Equal(rule.When.BroadcastEvents, []string{land.LandingKind}) ||
		rule.When.DistinctBy != "item_id" ||
		rule.When.Threshold != int64(drain.RepeatedRefusalThreshold) ||
		rule.When.Window != 24*time.Hour {
		t.Fatalf("unexpected %s when config: %+v (RepeatedRefusalThreshold=%d)", ruleID, rule.When, drain.RepeatedRefusalThreshold)
	}

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 26, 10, 13, 0, 0, time.UTC)

	appendRefusalTo := func(st *store.Store, runID, itemID, step, detail string, causes []string, at time.Time) int64 {
		t.Helper()
		causeObjs := make([]drain.RefusalCause, 0, len(causes))
		for _, c := range causes {
			causeObjs = append(causeObjs, drain.RefusalCause{Cause: c})
		}
		body, err := json.Marshal(map[string]any{
			"step":           step,
			"refusal_causes": causeObjs,
			"item_id":        itemID,
			"lane_id":        "lane-1",
			"tick_id":        "tick-1",
			"detail":         detail,
		})
		if err != nil {
			t.Fatal(err)
		}
		var seq int64
		if err := st.WriteTx(ctx, func(tx *store.Tx) (err error) {
			seq, err = tx.Append(event.Event{
				Kind:       drain.RefusalKind,
				ObjectType: "run",
				ObjectID:   &runID,
				At:         at,
				Payload:    body,
			})
			return
		}); err != nil {
			t.Fatal(err)
		}
		return seq
	}

	landingSeq := 0
	appendLandingTo := func(st *store.Store, itemID, result string, at time.Time) int64 {
		t.Helper()
		landingSeq++
		body, err := json.Marshal(land.Landing{
			LandingID:   fmt.Sprintf("landing_%s_%d", itemID, landingSeq),
			ItemID:      itemID,
			RunID:       fmt.Sprintf("run-%s-%d", itemID, landingSeq),
			VerdictID:   fmt.Sprintf("verdict-%d", landingSeq),
			Result:      result,
			BaseSHA:     "1111111111111111111111111111111111111111",
			HeadSHA:     "2222222222222222222222222222222222222222",
			TrunkBefore: "3333333333333333333333333333333333333333",
			TrunkAfter:  "4444444444444444444444444444444444444444",
		})
		if err != nil {
			t.Fatal(err)
		}
		var seq int64
		if err := st.WriteTx(ctx, func(tx *store.Tx) (err error) {
			seq, err = tx.Append(event.Event{
				Kind:       land.LandingKind,
				ObjectType: "item",
				ObjectID:   &itemID,
				At:         at,
				Payload:    body,
			})
			return
		}); err != nil {
			t.Fatal(err)
		}
		return seq
	}

	// Pre-load history before first evaluation does not fire (D-TW66).
	appendRefusalTo(s, "run-old-1", "item-old-1", drain.RefusalGate, "failing checks: citations", []string{"gate:citations"}, now.Add(-10*time.Minute))
	appendRefusalTo(s, "run-old-2", "item-old-2", drain.RefusalGate, "failing checks: citations", []string{"gate:citations"}, now.Add(-5*time.Minute))

	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	pending := func() []watch.Notice {
		t.Helper()
		results, err := e.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].ID != ruleID || results[0].Diagnostic() != "" || results[0].Skipped != 0 {
			t.Fatalf("unexpected evaluation results: %+v", results)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		return notices
	}

	if notices := pending(); len(notices) != 0 {
		t.Fatalf("pre-load history opened notices: %+v", notices)
	}

	// 1. Multiple refusals on the same item alone stay closed.
	now = now.Add(2 * time.Minute)
	appendRefusalTo(s, "run-same-1", "item-same", drain.RefusalGate, "failing checks: go-affected-suite", []string{"gate:go-affected-suite"}, now)
	now = now.Add(2 * time.Minute)
	appendRefusalTo(s, "run-same-2", "item-same", drain.RefusalGate, "failing checks: go-affected-suite", []string{"gate:go-affected-suite"}, now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("repeated refusals on a single item opened notice: %+v", notices)
	}

	// 2. Excluded refusals (2026-11-24 trunk-already-red + conflict, and 2026-11-25 two merge conflicts on different files) do not fire.
	now = now.Add(2 * time.Minute)
	appendLandingTo(s, "item-reset-0", land.ResultLanded, now)
	now = now.Add(2 * time.Minute)
	appendRefusalTo(s, "run-trunk-red", "item-inc-a", drain.RefusalTrunkAlreadyRed, "trunk already red", []string{}, now)
	now = now.Add(2 * time.Minute)
	appendRefusalTo(s, "run-conflict-1", "item-inc-b", drain.RefusalConflict, "CONFLICT (content): Merge conflict in docs/a.md", []string{}, now)
	now = now.Add(2 * time.Minute)
	appendRefusalTo(s, "run-conflict-2", "item-inc-c", drain.RefusalConflict, "CONFLICT (content): Merge conflict in docs/b.md", []string{}, now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("excluded refusals (trunk-already-red / conflicts) opened notice: %+v", notices)
	}

	// 3. Two refusals with different causes (gate:documents vs gate:go-build) do not fire.
	now = now.Add(2 * time.Minute)
	appendRefusalTo(s, "run-diff-1", "item-diff-a", drain.RefusalGate, "failing checks: documents", []string{"gate:documents"}, now)
	now = now.Add(2 * time.Minute)
	appendRefusalTo(s, "run-diff-2", "item-diff-b", drain.RefusalGate, "failing checks: go-build", []string{"gate:go-build"}, now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("refusals with different causes opened notice: %+v", notices)
	}

	// 4. A landed commit between refusals sharing a cause keeps the alert closed.
	now = now.Add(2 * time.Minute)
	appendLandingTo(s, "item-between-landed", land.ResultLanded, now)
	now = now.Add(2 * time.Minute)
	appendRefusalTo(s, "run-between-1", "item-between-a", drain.RefusalGate, "failing checks: citations, documents", []string{"gate:citations", "gate:documents"}, now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("single refused item after landing opened notice: %+v", notices)
	}

	// 5. Non-landing land.landing outcomes (squash-conflict, refused) do not reset causes and keep Skipped == 0;
	// refusing a second distinct item on a shared cause ("gate:documents") opens the alert for "gate:documents".
	now = now.Add(2 * time.Minute)
	appendLandingTo(s, "item-conflict", land.ResultSquashConflict, now)
	now = now.Add(1 * time.Minute)
	appendLandingTo(s, "item-refused", land.ResultRefused, now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("non-landing land.landing events alone opened notice: %+v", notices)
	}

	now = now.Add(3 * time.Minute)
	appendRefusalTo(s, "run-second-distinct", "item-between-b", drain.RefusalGate, "failing checks: documents", []string{"gate:documents"}, now)
	opened := pending()
	if len(opened) != 1 {
		t.Fatalf("want 1 alert for shared cause gate:documents, got %+v", opened)
	}
	firstAlert := opened[0]
	if firstAlert.Bundle.RuleID != ruleID || firstAlert.Bundle.Subject != "gate:documents" || firstAlert.Bundle.Severity != "warning" {
		t.Fatalf("unexpected alert bundle metadata: %+v", firstAlert.Bundle)
	}
	guidance := firstAlert.Bundle.Guidance
	allText := guidance.Summary + " " + strings.Join(guidance.Check, " ") + " " + strings.Join(guidance.Do, " ")
	if !strings.Contains(guidance.Summary, "gate:documents") ||
		!strings.Contains(guidance.Summary, "2") ||
		!strings.Contains(guidance.Summary, "item-between-a, item-between-b") ||
		!strings.Contains(guidance.Summary, drain.RefusalGate) ||
		!strings.Contains(guidance.Summary, "item-between-b") ||
		!strings.Contains(guidance.Summary, "run-second-distinct") ||
		!strings.Contains(strings.Join(guidance.Check, " "), "failing checks: documents") ||
		strings.Contains(strings.ToLower(allText), "unknown") ||
		strings.Contains(strings.ToLower(allText), "expected") ||
		strings.Contains(strings.ToLower(allText), "normal") {
		t.Fatalf("unexpected guidance text: %+v", guidance)
	}

	// 6. Further refusals on the same cause with no landing update the open episode in place.
	now = now.Add(4 * time.Minute)
	appendRefusalTo(s, "run-third-distinct", "item-between-c", drain.RefusalGate, "failing checks: documents", []string{"gate:documents"}, now)
	now = now.Add(2 * time.Minute)
	latestSeq := appendRefusalTo(s, "run-repeat-b", "item-between-b", drain.RefusalGate, "failing checks: documents", []string{"gate:documents"}, now)
	stillOpen := pending()
	if len(stillOpen) != 1 || stillOpen[0].AlertID != firstAlert.AlertID {
		t.Fatalf("expected single in-place episode %s, got %+v", firstAlert.AlertID, stillOpen)
	}
	var obsCount, lastObsSeq int64
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT observation_count, last_observed_seq FROM alert_episodes WHERE alert_id=?`, firstAlert.AlertID).Scan(&obsCount, &lastObsSeq)
	}); err != nil {
		t.Fatal(err)
	}
	if obsCount < 2 || lastObsSeq != latestSeq {
		t.Fatalf("expected observation_count>=2 and last_observed_seq=%d, got count=%d seq=%d", latestSeq, obsCount, lastObsSeq)
	}

	// 7. A subsequent landed commit resolves the open alert at landedSeq (condition-cleared) with Skipped == 0.
	now = now.Add(3 * time.Minute)
	landedSeq := appendLandingTo(s, "item-recovered", land.ResultLanded, now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("landed commit did not resolve open alert: %+v", notices)
	}
	var state, reason string
	var resolvedSeq int64
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT state, resolved_reason, resolved_seq FROM alert_episodes WHERE alert_id=?`, firstAlert.AlertID).Scan(&state, &reason, &resolvedSeq)
	}); err != nil {
		t.Fatal(err)
	}
	if state != "resolved" || reason != "condition-cleared" || resolvedSeq != landedSeq {
		t.Fatalf("resolved row = (%q, %q, %d), want (resolved, condition-cleared, %d)", state, reason, resolvedSeq, landedSeq)
	}

	// 8. A fresh pair of distinct refused items sharing a cause after that landing opens episode 2.
	now = now.Add(2 * time.Minute)
	appendRefusalTo(s, "run-ep2-a", "item-ep2-a", drain.RefusalVerifyNotDone, "c1: missing test", []string{"verify-not-done"}, now)
	now = now.Add(2 * time.Minute)
	appendRefusalTo(s, "run-ep2-b", "item-ep2-b", drain.RefusalVerifyNotDone, "c2: missing test", []string{"verify-not-done"}, now)
	reopened := pending()
	if len(reopened) != 1 || reopened[0].AlertID == firstAlert.AlertID || reopened[0].Bundle.Subject != "verify-not-done" {
		t.Fatalf("expected new episode for verify-not-done after prior landing resolution, got %+v", reopened)
	}

	// 9. Events lacking refusal_causes are skipped with no fallback (R-TW-DR2).
	legacyStore, err := store.Open(ctx, filepath.Join(t.TempDir(), "legacy.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { legacyStore.Close() })
	legacyEval := watch.Evaluator{Store: legacyStore, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	if _, err := legacyEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	for _, run := range []struct{ id, item string }{{"run-leg-1", "item-leg-1"}, {"run-leg-2", "item-leg-2"}} {
		body, _ := json.Marshal(map[string]any{"step": drain.RefusalGate, "item_id": run.item, "detail": "failing checks: documents"})
		runID := run.id
		if err := legacyStore.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: drain.RefusalKind, ObjectType: "run", ObjectID: &runID, At: now, Payload: body})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	legacyRes, err := legacyEval.EvaluateRules(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(legacyRes) != 1 || legacyRes[0].Skipped != 2 {
		t.Fatalf("expected Skipped == 2 for legacy events lacking refusal_causes, got %+v", legacyRes)
	}
	if legacyNotices, err := legacyEval.Pending(ctx, "foreground"); err != nil || len(legacyNotices) != 0 {
		t.Fatalf("legacy events lacking refusal_causes must not open alerts, got %+v (err=%v)", legacyNotices, err)
	}

	// 10. Evaluate alongside the full rules directory and lint the repository.
	allStore, err := store.Open(ctx, filepath.Join(t.TempDir(), "all.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { allStore.Close() })
	root := filepath.Clean(filepath.Join("..", ".."))
	allEval := watch.Evaluator{Store: allStore, Root: root, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := allEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	now = now.Add(time.Minute)
	appendRefusalTo(allStore, "run-all-1", "item-all-1", drain.RefusalGate, "failing checks: check-1", []string{"gate:check-1"}, now)
	now = now.Add(time.Minute)
	appendRefusalTo(allStore, "run-all-2", "item-all-2", drain.RefusalGate, "failing checks: check-1", []string{"gate:check-1"}, now)
	if _, err := allEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	allNotices, err := allEval.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if !slices.ContainsFunc(allNotices, func(n watch.Notice) bool { return n.Bundle.RuleID == ruleID && n.Bundle.Subject == "gate:check-1" }) {
		t.Fatalf("expected %s notice on gate:check-1 when evaluating full rules directory, got %+v", ruleID, allNotices)
	}

	policies, err := filepath.Glob(filepath.Join(root, "config", "tillr", "backend-policy*.json"))
	if err != nil || len(policies) == 0 {
		t.Fatalf("glob policies: %v (len=%d)", err, len(policies))
	}
	slices.Sort(policies)
	args := []string{"lint", root}
	for _, p := range policies {
		args = append(args, "--policy", p)
	}
	var stdout, stderr bytes.Buffer
	if code := run(t.Context(), args, &stdout, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("tillr watch lint failed: code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
}
