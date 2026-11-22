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

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainDispatchDeclinedRule(t *testing.T) {
	const ruleID = "drain-dispatch-declined"
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
	now := time.Date(2026, 11, 21, 22, 30, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	n := 0
	decline := func(item, reason string, at time.Time) {
		t.Helper()
		n++
		body, err := json.Marshal(map[string]string{
			"decision_id": fmt.Sprintf("dec-%d", n),
			"lane_id":     "lane-1",
			"run_id":      fmt.Sprintf("run-%d", n),
			"reason":      reason,
			"waiting_on":  "lane-1",
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       "drain.dispatch-declined",
				ObjectType: "item",
				ObjectID:   &item,
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

	// One event outside the ten-minute window and two inside stay quiet for item-a,
	// even with concurrent declines on another item.
	decline("item-a", "item or lane state changed since planning", now.Add(-11*time.Minute))
	decline("item-a", "item or lane state changed since planning", now.Add(-6*time.Minute))
	decline("item-a", "item or lane state changed since planning", now.Add(-2*time.Minute))
	decline("item-b", "lane-1 is still finishing previous run", now.Add(-3*time.Minute))
	decline("item-b", "lane-1 is still finishing previous run", now.Add(-1*time.Minute))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("two declines in ten minutes for one item should stay quiet, got %+v", notices)
	}

	// The third decline in ten minutes for item-a opens one foreground alert for item-a.
	decline("item-a", "item or lane state changed since planning", now)
	notices := pending()
	if len(notices) != 1 || notices[0].Bundle.RuleID != ruleID || notices[0].Bundle.Subject != "item-a" {
		t.Fatalf("want one notice for item-a at three declines, got %+v", notices)
	}
	guidance := notices[0].Bundle.Guidance
	allGuidance := strings.ToLower(strings.Join(append(slices.Clone(guidance.Check), guidance.Do...), " "))
	if !strings.Contains(guidance.Summary, "item-a") ||
		!strings.Contains(guidance.Summary, "3") ||
		!strings.Contains(allGuidance, "tillr drain why item-a") ||
		!strings.Contains(allGuidance, "decline reason") {
		t.Fatalf("notice lacks subject, count, or guidance to run tillr drain why and read the decline reason: %+v", guidance)
	}

	// A fourth decline in the same window keeps the single open alert episode.
	decline("item-a", "item or lane state changed since planning", now)
	if again := pending(); len(again) != 1 || again[0].AlertID != notices[0].AlertID {
		t.Fatalf("fourth decline should keep the same alert open, got %+v", again)
	}

	// Once fewer than three declines remain in the ten-minute window, the alert resolves.
	now = now.Add(9 * time.Minute)
	if aged := pending(); len(aged) != 0 {
		t.Fatalf("alert should resolve once fewer than three declines remain in ten minutes, got %+v", aged)
	}
}
