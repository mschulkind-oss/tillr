package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainRepeatedItemConflictsRule(t *testing.T) {
	const ruleID = "drain-repeated-item-conflicts"
	rules, err := watch.LoadRules("../../config/tillr/watch")
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

	t0 := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	now := t0
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}

	n := 0
	conflict := func(itemID, conflictID, status string, at time.Time) {
		t.Helper()
		n++
		payload := map[string]any{
			"conflict_id":      conflictID,
			"item_id":          itemID,
			"run_id":           fmt.Sprintf("run-%d", n),
			"text":             "OBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: request conflicts with design",
			"blocked_criteria": []string{"c1"},
			"waiting_on":       "design",
			"status":           status,
		}
		if status == "awaiting" {
			payload["design_run_id"] = fmt.Sprintf("design-%d", n)
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       "item.conflict",
				ObjectType: "item",
				ObjectID:   &itemID,
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

	// One item.conflict event in the window (plus an unrelated event) stays quiet.
	conflict("item-a", "conflict-a-0", "raised", now)
	refusalRun := "run-refusal"
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       drain.RefusalKind,
			ObjectType: "run",
			ObjectID:   &refusalRun,
			At:         now,
			Payload:    []byte(`{"item_id":"item-a","step":"verify-not-done"}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("one item.conflict event in the window should stay quiet, got %+v", notices)
	}

	// After conflict-a-0 ages past 24h, one new raised conflict on item-a and
	// one on item-b stay quiet, even after item-a's conflict transitions to
	// awaiting (and later answered and closed) when given to a designer.
	now = t0.Add(25 * time.Hour)
	conflict("item-a", "conflict-a-1", "raised", now)
	conflict("item-b", "conflict-b-1", "raised", now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("one in-window raised item.conflict event per item should stay quiet, got %+v", notices)
	}

	now = t0.Add(25*time.Hour + time.Minute)
	conflict("item-a", "conflict-a-1", "awaiting", now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("one conflict going raised then awaiting should stay quiet, got %+v", notices)
	}

	now = t0.Add(25*time.Hour + 2*time.Minute)
	conflict("item-a", "conflict-a-1", "answered", now)
	conflict("item-a", "conflict-a-1", "closed", now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("status updates on a single conflict should stay quiet, got %+v", notices)
	}

	// A second raised conflict in the window for item-a opens one foreground notice.
	now = t0.Add(25*time.Hour + 3*time.Minute)
	conflict("item-a", "conflict-a-2", "raised", now)
	notices := pending()
	if len(notices) != 1 || notices[0].Bundle.RuleID != ruleID || notices[0].Bundle.Subject != "item-a" {
		t.Fatalf("want one notice for item-a at two raised item.conflict events, got %+v", notices)
	}
	guidance := notices[0].Bundle.Guidance
	checkText := strings.Join(guidance.Check, " ")
	doText := strings.Join(guidance.Do, " ")
	allGuidance := strings.ToLower(checkText + " " + doText)
	if !strings.Contains(guidance.Summary, "item-a") ||
		!strings.Contains(guidance.Summary, "2") ||
		!strings.Contains(checkText, "tillr drain why item-a") ||
		!strings.Contains(allGuidance, "obstacle text") ||
		!strings.Contains(allGuidance, "request's own words") ||
		!strings.Contains(allGuidance, "design document") ||
		!strings.Contains(allGuidance, "state a cause it cannot establish") {
		t.Fatalf("notice lacks subject, count, or request-words guidance: %+v", guidance)
	}

	// A third raised item.conflict event in the window keeps the single open alert episode.
	now = t0.Add(26*time.Hour + 3*time.Minute)
	conflict("item-a", "conflict-a-3", "raised", now)
	if again := pending(); len(again) != 1 || again[0].AlertID != notices[0].AlertID {
		t.Fatalf("third raised item.conflict event should keep the same alert open, got %+v", again)
	}

	// Once fewer than two raised item.conflict events remain inside the 24h window, the alert resolves.
	now = t0.Add(49*time.Hour + 30*time.Minute)
	if aged := pending(); len(aged) != 0 {
		t.Fatalf("alert should resolve once fewer than two raised item.conflict events remain in 24h, got %+v", aged)
	}
}
