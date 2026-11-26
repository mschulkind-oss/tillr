package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestGateFailThenPassRule(t *testing.T) {
	const (
		ruleID      = "gate-fail-then-pass"
		verdictKind = "gate.verdict"
	)

	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule in config/tillr/watch", ruleID)
	}
	rule := rules[idx]
	if reason := rule.UnevaluatedReason(); reason != "" {
		t.Fatalf("rule %s is unevaluated: %s", ruleID, reason)
	}
	if rule.Scope != "core" || rule.Severity != "warning" || !rule.Enabled ||
		rule.Delivery.Receiver != "foreground" ||
		!slices.Equal(rule.Reads.Events, []string{verdictKind}) ||
		rule.When.GroupBy != "tree_sha" ||
		rule.When.Pattern != watch.AlternatingPair ||
		rule.When.Field != "result" ||
		!slices.Equal(rule.When.Pairs, [][2]string{{"fail", "pass"}}) ||
		rule.When.Threshold != 2 ||
		rule.When.Window != 24*time.Hour {
		t.Fatalf("unexpected %s configuration: %+v", ruleID, rule)
	}

	ctx := context.Background()
	scratchDir := t.TempDir()

	s, err := store.Open(ctx, filepath.Join(scratchDir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 23, 14, 45, 0, 0, time.UTC)
	e := watch.Evaluator{
		Store: s,
		Rules: []watch.Rule{rule},
		Clock: func() time.Time { return now },
	}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	appendVerdict := func(st *store.Store, runID, treeSHA, result string, at time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"verdict_id": "verdict-" + runID,
			"tree_sha":   treeSHA,
			"result":     result,
		})
		if err != nil {
			t.Fatal(err)
		}
		id := runID
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       verdictKind,
				ObjectType: "run",
				ObjectID:   &id,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

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

	const (
		treeStableFail  = "1111111111111111111111111111111111111111"
		treeStablePass  = "2222222222222222222222222222222222222222"
		treeInherited   = "3333333333333333333333333333333333333333"
		treeExpired     = "4444444444444444444444444444444444444444"
		treeFlaky       = "5555555555555555555555555555555555555555"
		treePassToFail  = "6666666666666666666666666666666666666666"
		treeAllRulesRun = "7777777777777777777777777777777777777777"
	)

	// Empty store stays quiet.
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("empty store opened notices: %+v", notices)
	}

	// Identical repeated verdicts on the same tree and opposite verdicts on
	// different trees stay quiet.
	appendVerdict(s, "run-fail-1", treeStableFail, "fail", now.Add(-40*time.Minute))
	appendVerdict(s, "run-fail-2", treeStableFail, "fail", now.Add(-35*time.Minute))
	appendVerdict(s, "run-pass-1", treeStablePass, "pass", now.Add(-30*time.Minute))
	appendVerdict(s, "run-pass-2", treeStablePass, "pass", now.Add(-28*time.Minute))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("repeated identical verdicts or distinct trees opened notices: %+v", notices)
	}

	// Promoting a failing verdict to pass-inherited does not fire.
	appendVerdict(s, "run-inh-1", treeInherited, "fail", now.Add(-27*time.Minute))
	appendVerdict(s, "run-inh-2", treeInherited, "pass-inherited", now.Add(-26*time.Minute))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("fail -> pass-inherited opened notices: %+v", notices)
	}

	// A fail outside the 24h window followed by a pass inside it stays quiet.
	appendVerdict(s, "run-exp-1", treeExpired, "fail", now.Add(-24*time.Hour-time.Minute))
	appendVerdict(s, "run-exp-2", treeExpired, "pass", now.Add(-5*time.Minute))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("expired fail + recent pass opened notices: %+v", notices)
	}

	// Fail then pass on the same tree within 25 minutes opens one warning alert.
	appendVerdict(s, "run-flaky-fail", treeFlaky, "fail", now.Add(-25*time.Minute))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("single fail verdict opened notices: %+v", notices)
	}
	appendVerdict(s, "run-flaky-pass", treeFlaky, "pass", now)
	opened := pending()
	if len(opened) != 1 || opened[0].Bundle.RuleID != ruleID || opened[0].Bundle.Subject != treeFlaky || opened[0].Bundle.Severity != "warning" {
		t.Fatalf("want 1 warning notice for %s on fail -> pass, got %+v", treeFlaky, opened)
	}
	guidance := opened[0].Bundle.Guidance
	allGuidance := strings.Join(append([]string{guidance.Summary}, append(slices.Clone(guidance.Check), guidance.Do...)...), " ")
	for _, want := range []string{treeFlaky, "fail", "pass", "2", "25m", "run-flaky-pass"} {
		if !strings.Contains(allGuidance, want) {
			t.Fatalf("guidance missing %q: %+v", want, guidance)
		}
	}
	if strings.Contains(allGuidance, "{{") || strings.Contains(allGuidance, "unknown") {
		t.Fatalf("guidance contains unrendered placeholder or unknown: %+v", guidance)
	}

	// Another pass on the same tree keeps the same episode open without duplicating it.
	now = now.Add(5 * time.Minute)
	appendVerdict(s, "run-flaky-pass-2", treeFlaky, "pass", now)
	retained := pending()
	if len(retained) != 1 || retained[0].AlertID != opened[0].AlertID {
		t.Fatalf("subsequent pass should retain episode %s, got %+v", opened[0].AlertID, retained)
	}

	// Once the failing verdict ages past 24h, the episode resolves.
	now = now.Add(24 * time.Hour)
	if cleared := pending(); len(cleared) != 0 {
		t.Fatalf("expected episode to resolve once failing verdict ages past 24h, got %+v", cleared)
	}

	// Pass then fail on the same tree also opens one warning alert.
	appendVerdict(s, "run-pf-pass", treePassToFail, "pass", now.Add(-10*time.Minute))
	appendVerdict(s, "run-pf-fail", treePassToFail, "fail", now)
	pfOpened := pending()
	if len(pfOpened) != 1 || pfOpened[0].Bundle.RuleID != ruleID || pfOpened[0].Bundle.Subject != treePassToFail {
		t.Fatalf("want 1 warning notice for %s on pass -> fail, got %+v", treePassToFail, pfOpened)
	}

	// Evaluating all command-eligible rules in config/tillr/watch on a fail -> pass
	// pair for one tree_sha opens only gate-fail-then-pass.
	allStore, err := store.Open(ctx, filepath.Join(scratchDir, "all-rules.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { allStore.Close() })
	var eligible []watch.Rule
	for _, r := range rules {
		if r.UnevaluatedReason() == "" {
			eligible = append(eligible, r)
		}
	}
	replayNow := time.Date(2026, 11, 23, 14, 45, 0, 0, time.UTC)
	allEval := watch.Evaluator{Store: allStore, Rules: eligible, Clock: func() time.Time { return replayNow }}
	if _, err := allEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	appendVerdict(allStore, "run-all-fail", treeAllRulesRun, "fail", replayNow.Add(-25*time.Minute))
	appendVerdict(allStore, "run-all-pass", treeAllRulesRun, "pass", replayNow)
	if _, err := allEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	allNotices, err := allEval.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(allNotices) != 1 || allNotices[0].Bundle.RuleID != ruleID || allNotices[0].Bundle.Subject != treeAllRulesRun {
		t.Fatalf("want only %s (%s) across config/tillr/watch, got %+v", ruleID, treeAllRulesRun, allNotices)
	}

	// tillr watch lint passes on the repository with the new rule present.
	root := filepath.Clean(filepath.Join("..", ".."))
	policies, err := filepath.Glob(filepath.Join(root, "config", "tillr", "backend-policy*.json"))
	if err != nil || len(policies) == 0 {
		t.Fatalf("glob policies: %v (len=%d)", err, len(policies))
	}
	slices.Sort(policies)
	lintArgs := []string{"lint", root}
	for _, p := range policies {
		lintArgs = append(lintArgs, "--policy", p)
	}
	var lintOut, lintErr bytes.Buffer
	if code := run(t.Context(), lintArgs, &lintOut, &lintErr); code != 0 || lintErr.Len() != 0 {
		t.Fatalf("tillr watch lint failed: code=%d stdout=%q stderr=%q", code, lintOut.String(), lintErr.String())
	}
}
