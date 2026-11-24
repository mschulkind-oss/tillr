package main

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestGateDrainStallsRule(t *testing.T) {
	const (
		ruleID      = "gate-drain-stalls"
		verdictKind = "gate.verdict"
		rulePath    = "../../config/tillr/watch/gate-drain-stalls.toml"
	)
	rawRule, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("read %s: %v", rulePath, err)
	}
	var commentLines []string
	for _, line := range strings.Split(string(rawRule), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			commentLines = append(commentLines, trimmed)
		}
	}
	comment := strings.Join(commentLines, "\n")
	for _, required := range []string{"sqlite3", "2026-11-24", "19", "11"} {
		if !strings.Contains(comment, required) {
			t.Fatalf("rule comment in %s must state command, date, and figures (missing %q):\n%s", rulePath, required, comment)
		}
	}

	rules, err := watch.LoadRules("../../config/tillr/watch")
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
		rule.When.GroupBy != "global" ||
		rule.When.Pattern != watch.CountInWindow ||
		rule.When.Threshold != 3 ||
		rule.When.Window != 4*time.Hour ||
		rule.When.ClearWhen != nil {
		t.Fatalf("unexpected %s configuration: %+v", ruleID, rule)
	}

	ctx := context.Background()
	scratchDir := t.TempDir()

	s, err := store.Open(ctx, filepath.Join(scratchDir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 22, 23, 43, 0, 0, time.UTC)
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
		treeOld  = "0000000000000000000000000000000000000000"
		treeA    = "8806db219a2b0000000000000000000000000001"
		treeB    = "a368cb731d9e0000000000000000000000000002"
		treeC    = "86a800f48a170000000000000000000000000003"
		treeD    = "89fb1b3e991c0000000000000000000000000004"
		treePass = "ffffffffffffffffffffffffffffffffffffffff"
	)

	// Empty store and expired stalls outside the 4h window stay quiet.
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("empty store opened notices: %+v", notices)
	}
	appendVerdict(s, "run-expired", treeOld, "stalled", now.Add(-4*time.Hour-time.Minute))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("expired stall outside 4h window opened notices: %+v", notices)
	}

	// Below threshold (1 and 2 stalls on different trees within 4h, with passing and
	// failing verdicts on other candidates in between) stays quiet.
	appendVerdict(s, "run-cdd89c77e9-a1-n2", treeA, "stalled", now.Add(-69*time.Minute))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("1 stalled verdict opened notice: %+v", notices)
	}
	appendVerdict(s, "run-pass-between-1", treePass, "pass", now.Add(-50*time.Minute))
	appendVerdict(s, "run-eee6c3a261-a2-n1", treeB, "stalled", now.Add(-27*time.Minute))
	appendVerdict(s, "run-fail-between-2", treePass, "fail", now.Add(-10*time.Minute))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("2 stalled verdicts across different trees opened notice: %+v", notices)
	}

	// Third stalled verdict on a third distinct tree within 4h opens one drain-wide
	// warning episode even though another candidate passed in between.
	appendVerdict(s, "run-48e632c45b-a5-n1", treeC, "stalled", now)
	opened := pending()
	if len(opened) != 1 || opened[0].Bundle.RuleID != ruleID || opened[0].Bundle.Subject != "global" || opened[0].Bundle.Severity != "warning" {
		t.Fatalf("want 1 global warning notice at 3rd distinct-tree stall, got %+v", opened)
	}
	guidance := opened[0].Bundle.Guidance
	allGuidance := strings.Join(append([]string{guidance.Summary}, append(slices.Clone(guidance.Check), guidance.Do...)...), " ")
	if !strings.Contains(guidance.Summary, "3") ||
		!strings.Contains(allGuidance, "run-48e632c45b-a5-n1") ||
		!strings.Contains(allGuidance, treeC) ||
		strings.Contains(allGuidance, "{{") ||
		strings.Contains(allGuidance, "unknown") {
		t.Fatalf("guidance must render count, latest run, and tree without raw placeholders or unknown: %+v", guidance)
	}

	// A passing verdict on another candidate does not clear the open drain-wide episode,
	// and a fourth distinct-tree stall 2h37m later updates the same episode in place.
	now = now.Add(10 * time.Minute)
	appendVerdict(s, "run-pass-after", treePass, "pass", now)
	stillOpen := pending()
	if len(stillOpen) != 1 || stillOpen[0].AlertID != opened[0].AlertID {
		t.Fatalf("unrelated passing verdict should not clear drain-wide stall episode, got %+v", stillOpen)
	}

	now = now.Add(147 * time.Minute)
	appendVerdict(s, "run-fixer-main-a1", treeD, "stalled", now)
	retained := pending()
	if len(retained) != 1 || retained[0].AlertID != opened[0].AlertID {
		t.Fatalf("fourth distinct-tree stall within 4h should retain episode %s, got %+v", opened[0].AlertID, retained)
	}

	// Once fewer than 3 stalled verdicts remain in the 4h window, the episode resolves.
	now = now.Add(4*time.Hour + time.Minute)
	if cleared := pending(); len(cleared) != 0 {
		t.Fatalf("expected episode to resolve once stalls age past 4h, got %+v", cleared)
	}

	// Evaluating all rules in config/tillr/watch on three stalls across three distinct
	// trees opens gate-drain-stalls and keeps per-tree gate-repeated-stall quiet.
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
	replayNow := time.Date(2026, 11, 22, 23, 43, 0, 0, time.UTC)
	allEval := watch.Evaluator{Store: allStore, Rules: eligible, Clock: func() time.Time { return replayNow }}
	if _, err := allEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	appendVerdict(allStore, "run-distinct-1", treeA, "stalled", replayNow.Add(-69*time.Minute))
	appendVerdict(allStore, "run-distinct-pass", treePass, "pass", replayNow.Add(-45*time.Minute))
	appendVerdict(allStore, "run-distinct-2", treeB, "stalled", replayNow.Add(-27*time.Minute))
	appendVerdict(allStore, "run-distinct-3", treeC, "stalled", replayNow)
	if _, err := allEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	allNotices, err := allEval.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(allNotices) != 1 || allNotices[0].Bundle.RuleID != ruleID || allNotices[0].Bundle.Subject != "global" {
		t.Fatalf("want only %s (global) on 3 distinct stalled trees across config/tillr/watch, got %+v", ruleID, allNotices)
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
	if code := run(t.Context(), lintArgs, &lintOut, &lintErr); code != 0 || lintOut.Len() != 0 || lintErr.Len() != 0 {
		t.Fatalf("tillr watch lint failed: code=%d stdout=%q stderr=%q", code, lintOut.String(), lintErr.String())
	}
}
