package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	_ "github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestGateRepeatedStallRule(t *testing.T) {
	const (
		ruleID      = "gate-repeated-stall"
		verdictKind = "gate.verdict"
	)
	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule", ruleID)
	}
	rule := rules[idx]
	if reason := rule.UnevaluatedReason(); reason != "" {
		t.Fatalf("rule %s is unevaluated: %s", ruleID, reason)
	}

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 22, 14, 5, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	recordVerdict := func(runID, treeSHA, result string, at time.Time) {
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
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
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

	if notices := pending(); len(notices) != 0 {
		t.Fatalf("empty store opened notices: %+v", notices)
	}

	const treeA = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	const treeB = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"

	// Two stalls on treeA and two stalls on treeB stay quiet (threshold is 3 per tree_sha).
	recordVerdict("run-a-1", treeA, "stalled", now.Add(-10*time.Minute))
	recordVerdict("run-a-1", treeA, "stalled", now.Add(-8*time.Minute))
	recordVerdict("run-b-1", treeB, "stalled", now.Add(-7*time.Minute))
	recordVerdict("run-b-1", treeB, "stalled", now.Add(-6*time.Minute))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("two stalls per tree opened notices: %+v", notices)
	}

	// Non-stalled verdicts on treeB do not count toward stalling.
	recordVerdict("run-b-2", treeB, "fail", now.Add(-5*time.Minute))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("non-stalled verdict opened notices: %+v", notices)
	}

	// A third stalled verdict on treeA opens one warning alert naming treeA and count 3.
	recordVerdict("run-a-1", treeA, "stalled", now.Add(-4*time.Minute))
	first := pending()
	if len(first) != 1 || first[0].Bundle.RuleID != ruleID || first[0].Bundle.Subject != treeA || first[0].Bundle.Severity != "warning" {
		t.Fatalf("want one warning notice for %s on third stall, got %+v", treeA, first)
	}
	if !strings.Contains(first[0].Bundle.Guidance.Summary, treeA) || !strings.Contains(first[0].Bundle.Guidance.Summary, "3") {
		t.Fatalf("summary %q does not name tree and count 3", first[0].Bundle.Guidance.Summary)
	}

	// A fourth stall on treeA keeps the same episode open.
	recordVerdict("run-a-1", treeA, "stalled", now.Add(-2*time.Minute))
	repeated := pending()
	if len(repeated) != 1 || repeated[0].AlertID != first[0].AlertID {
		t.Fatalf("fourth stall should keep one episode: first=%+v repeated=%+v", first, repeated)
	}

	// A non-stalled verdict on treeA clears the episode.
	recordVerdict("run-a-1", treeA, "pass", now.Add(-1*time.Minute))
	if cleared := pending(); len(cleared) != 0 {
		t.Fatalf("non-stalled verdict should clear episode, got %+v", cleared)
	}

	// Evaluating all command-eligible rules in config/tillr/watch on three stalled
	// gate.verdict events for one tree_sha opens per-tree gate-repeated-stall and
	// drain-wide gate-drain-stalls, and nothing else.
	allStore, err := store.Open(ctx, filepath.Join(t.TempDir(), "all-rules.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { allStore.Close() })
	allEval := watch.Evaluator{Store: allStore, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := allEval.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	const treeC = "cccccccccccccccccccccccccccccccccccccccc"
	for i := 1; i <= 3; i++ {
		at := now.Add(-time.Duration(4-i) * time.Minute)
		body, err := json.Marshal(map[string]any{
			"verdict_id": "verdict-all",
			"tree_sha":   treeC,
			"result":     "stalled",
		})
		if err != nil {
			t.Fatal(err)
		}
		id := "run-all-a1"
		if err := allStore.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: verdictKind, ObjectType: "run", ObjectID: &id, At: at, Payload: body})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	skipped, err := allEval.Evaluate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if skipped[ruleID] != 0 {
		t.Fatalf("expected 0 skipped events for %s, got %+v", ruleID, skipped)
	}
	allNotices, err := allEval.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	gotByRule := map[string]string{}
	for _, n := range allNotices {
		gotByRule[n.Bundle.RuleID] = n.Bundle.Subject
	}
	if len(allNotices) != 2 || gotByRule[ruleID] != treeC || gotByRule["gate-drain-stalls"] != "global" {
		t.Fatalf("want %s (%s) and gate-drain-stalls (global) across config/tillr/watch, got %+v", ruleID, treeC, allNotices)
	}
}
