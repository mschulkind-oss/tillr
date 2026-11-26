package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	tillrrun "github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainFixerHeldNewAttemptRule(t *testing.T) {
	const ruleID = "drain-fixer-held-new-attempt"
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
	if rule.Scope != "core" || rule.Severity != "warning" || rule.Delivery.Receiver != "foreground" ||
		rule.When.Pattern != watch.PredicateMatch || rule.When.Window != 24*time.Hour {
		t.Fatalf("unexpected %s configuration: %+v", ruleID, rule)
	}

	ctx := context.Background()
	findings, err := watch.Lint(ctx, watch.LintOptions{
		Root:      "../..",
		RulesPath: "rules",
	})
	if err != nil {
		t.Fatal(err)
	}
	for _, f := range findings {
		if strings.Contains(f.Source, "drain-fixer-held-new-attempt.toml") {
			t.Fatalf("unexpected lint finding on %s: %s", ruleID, f.String())
		}
	}

	scratchDir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(scratchDir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 23, 9, 55, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	appendEvent := func(kind, objType, objID string, payload map[string]any, at time.Time) {
		t.Helper()
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       kind,
				ObjectType: objType,
				ObjectID:   &objID,
				At:         at,
				Payload:    raw,
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

	pendingSubjects := func() []string {
		t.Helper()
		notices := pending()
		out := make([]string, len(notices))
		for i, n := range notices {
			out[i] = n.Bundle.Subject
		}
		slices.Sort(out)
		return out
	}

	// Empty store stays quiet.
	if got := pendingSubjects(); len(got) != 0 {
		t.Fatalf("empty store opened notices: %v", got)
	}

	// 1. Ordinary run.allocate without a prior drain.fixer-attached stays quiet.
	appendEvent(tillrrun.AllocateKind, "run", "run-item-plain-a2", map[string]any{
		"item_id": "item-plain", "run_id": "run-item-plain-a2", "lane_id": "lane-1", "seat": "build",
	}, now)
	if got := pendingSubjects(); len(got) != 0 {
		t.Fatalf("ordinary run.allocate opened notices: %v", got)
	}

	// 2. Fixer/defect attachment with kept_candidate: false never arms.
	appendEvent(drain.FixerAttachedKind, "run", "fixer-main-111111111111", map[string]any{
		"item_id": "fixer-main-111111111111", "fixer_item": "fixer-main-111111111111", "kept_candidate": false,
	}, now)
	appendEvent(tillrrun.AllocateKind, "run", "run-fixer-main-111111111111-a2", map[string]any{
		"item_id": "fixer-main-111111111111", "run_id": "run-fixer-main-111111111111-a2", "lane_id": "lane-1", "seat": "build",
	}, now)
	if got := pendingSubjects(); len(got) != 0 {
		t.Fatalf("kept_candidate:false attachment opened notices: %v", got)
	}

	// 3. Re-gated after hold via gate.started -> legitimate retry stays quiet.
	appendEvent(drain.FixerAttachedKind, "run", "run-item-gated-start-a1", map[string]any{
		"item_id": "item-gated-start", "run_id": "run-item-gated-start-a1", "lane_id": "lane-1",
		"fixer_item": "fixer-main-111111111111", "kept_candidate": true,
	}, now)
	now = now.Add(2 * time.Hour)
	appendEvent(drain.GateStartedKind, "run", "run-item-gated-start-a1", map[string]any{
		"item_id": "item-gated-start", "run_id": "run-item-gated-start-a1", "lane_id": "lane-1",
	}, now)
	now = now.Add(5 * time.Minute)
	appendEvent(tillrrun.AllocateKind, "run", "run-item-gated-start-a2", map[string]any{
		"item_id": "item-gated-start", "run_id": "run-item-gated-start-a2", "lane_id": "lane-1", "seat": "build",
	}, now)
	if got := pendingSubjects(); len(got) != 0 {
		t.Fatalf("post-gate.started retry opened notices: %v", got)
	}

	// 4. Re-gated after hold via gate.verdict -> legitimate retry stays quiet.
	appendEvent(drain.FixerAttachedKind, "run", "run-item-gated-verdict-a1", map[string]any{
		"item_id": "item-gated-verdict", "run_id": "run-item-gated-verdict-a1", "lane_id": "lane-1",
		"fixer_item": "fixer-main-111111111111", "kept_candidate": true,
	}, now)
	now = now.Add(time.Hour)
	appendEvent(drain.GateVerdictKind, "run", "run-item-gated-verdict-a1", map[string]any{
		"item_id": "item-gated-verdict", "run_id": "run-item-gated-verdict-a1", "lane_id": "lane-1", "result": "failed",
	}, now)
	now = now.Add(5 * time.Minute)
	appendEvent(tillrrun.AllocateKind, "run", "run-item-gated-verdict-a2-n1", map[string]any{
		"item_id": "item-gated-verdict", "run_id": "run-item-gated-verdict-a2-n1", "lane_id": "lane-1", "seat": "plan",
	}, now)
	if got := pendingSubjects(); len(got) != 0 {
		t.Fatalf("post-gate.verdict retry opened notices: %v", got)
	}

	// 5. Pre-gate rebase -cfix, review/verify -fresh, and design runs stay quiet while armed.
	appendEvent(drain.FixerAttachedKind, "run", "run-item-rebased-a1", map[string]any{
		"item_id": "item-rebased", "run_id": "run-item-rebased-a1", "lane_id": "lane-1",
		"fixer_item": "fixer-main-111111111111", "kept_candidate": true,
	}, now)
	now = now.Add(30 * time.Minute)
	appendEvent(tillrrun.AllocateKind, "run", "run-item-rebased-a1-cfix", map[string]any{
		"item_id": "item-rebased", "run_id": "run-item-rebased-a1-cfix", "lane_id": "lane-1", "seat": "build",
	}, now)
	appendEvent(tillrrun.AllocateKind, "run", "run-item-rebased-a1-fresh", map[string]any{
		"item_id": "item-rebased", "run_id": "run-item-rebased-a1-fresh", "lane_id": "lane-1", "seat": "review",
	}, now)
	appendEvent(tillrrun.AllocateKind, "run", "run-item-rebased-a1-verify-fresh", map[string]any{
		"item_id": "item-rebased", "run_id": "run-item-rebased-a1-verify-fresh", "lane_id": "lane-1", "seat": "verify",
	}, now)
	appendEvent(tillrrun.AllocateKind, "run", "run-item-rebased-a2", map[string]any{
		"item_id": "item-rebased", "run_id": "run-item-rebased-a2", "lane_id": "lane-1", "seat": "design",
	}, now)
	if got := pendingSubjects(); len(got) != 0 {
		t.Fatalf("pre-gate rebase/judge/design runs opened notices: %v", got)
	}

	// 6. 2026-11-23 incident shape: 3h 58m hold -> new attempt run-item-request-b70a54f954-a2-n1 fires,
	// and stays open even after the new attempt reaches its own gate.started and gate.verdict.
	appendEvent(drain.FixerAttachedKind, "run", "run-item-request-b70a54f954-a1-n1", map[string]any{
		"item_id": "item-request-b70a54f954", "run_id": "run-item-request-b70a54f954-a1-n1", "lane_id": "lane-1",
		"fixer_item": "fixer-main-111111111111", "kept_candidate": true,
	}, now)
	now = now.Add(3*time.Hour + 58*time.Minute)
	appendEvent(tillrrun.AllocateKind, "run", "run-item-request-b70a54f954-a2-n1", map[string]any{
		"item_id": "item-request-b70a54f954", "run_id": "run-item-request-b70a54f954-a2-n1", "lane_id": "lane-2", "seat": "plan",
	}, now)
	opened := pending()
	if len(opened) != 1 || opened[0].Bundle.Subject != "item-request-b70a54f954" {
		t.Fatalf("expected 1 open notice for item-request-b70a54f954, got %+v", opened)
	}
	if !strings.Contains(opened[0].Bundle.Guidance.Summary, "run-item-request-b70a54f954-a2-n1") ||
		!strings.Contains(opened[0].Bundle.Guidance.Summary, "lane-2") {
		t.Fatalf("unexpected summary: %q", opened[0].Bundle.Guidance.Summary)
	}

	// Subsequent gate.started and gate.verdict of the wasted attempt must NOT clear the alert.
	now = now.Add(10 * time.Minute)
	appendEvent(drain.GateStartedKind, "run", "run-item-request-b70a54f954-a2-n1", map[string]any{
		"item_id": "item-request-b70a54f954", "run_id": "run-item-request-b70a54f954-a2-n1", "lane_id": "lane-2",
	}, now)
	now = now.Add(2 * time.Minute)
	appendEvent(drain.GateVerdictKind, "run", "run-item-request-b70a54f954-a2-n1", map[string]any{
		"item_id": "item-request-b70a54f954", "run_id": "run-item-request-b70a54f954-a2-n1", "lane_id": "lane-2", "result": "passed",
	}, now)
	if got := pendingSubjects(); !slices.Equal(got, []string{"item-request-b70a54f954"}) {
		t.Fatalf("alert did not stay open across new attempt's gate events: got %v", got)
	}

	// 7. Non-workflow attempt run_id (run-...-a2) also fires when bypassing re-gate.
	appendEvent(drain.FixerAttachedKind, "run", "run-item-simple-a1", map[string]any{
		"item_id": "item-simple", "run_id": "run-item-simple-a1", "lane_id": "lane-1",
		"fixer_item": "fixer-main-111111111111", "kept_candidate": true,
	}, now)
	now = now.Add(15 * time.Minute)
	appendEvent(tillrrun.AllocateKind, "run", "run-item-simple-a2", map[string]any{
		"item_id": "item-simple", "run_id": "run-item-simple-a2", "lane_id": "lane-3", "seat": "build",
	}, now)
	if got := pendingSubjects(); !slices.Equal(got, []string{"item-request-b70a54f954", "item-simple"}) {
		t.Fatalf("expected both item-request-b70a54f954 and item-simple open, got %v", got)
	}
}
