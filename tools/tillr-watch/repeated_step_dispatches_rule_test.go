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

	_ "github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainRepeatedStepDispatchesRule(t *testing.T) {
	const (
		ruleID       = "drain-repeated-step-dispatches"
		allocateKind = "run.allocate"
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

	now := time.Date(2026, 11, 22, 7, 5, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	allocate := func(runID, itemID, node, laneID string, at time.Time) {
		t.Helper()
		payload := map[string]any{
			"run_id":     runID,
			"backend":    "pi-verify",
			"seat":       "verify",
			"workflow":   "planned-build@v1",
			"lane_id":    laneID,
			"started_at": at.UTC().Format(time.RFC3339),
		}
		if itemID != "" {
			payload["item_id"] = itemID
		}
		if node != "" {
			payload["node"] = node
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		lane := laneID
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       allocateKind,
				ObjectType: "lane",
				ObjectID:   &lane,
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

	// An item whose steps each run once within ten minutes stays quiet, even with six steps in the window.
	for i, step := range []string{"plan", "work/piece-1", "work/piece-2", "work", "verify", "judge"} {
		allocate(fmt.Sprintf("run-item-once-a1-n%d", i+1), "item-once", step, "lane-1", now.Add(-time.Duration(6-i)*time.Minute))
	}
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("item whose steps each run once opened notices: %+v", notices)
	}

	// An expired fifth run older than ten minutes, four runs of the same step in the window,
	// and four runs of the same step on another item all stay quiet.
	allocate("run-item-loop-a0-n3", "item-loop", "verify", "express-1", now.Add(-11*time.Minute))
	for i := 1; i <= 4; i++ {
		at := now.Add(-time.Duration(10-i) * time.Minute)
		allocate(fmt.Sprintf("run-item-loop-a%d-n3", i), "item-loop", "verify", "express-1", at)
		allocate(fmt.Sprintf("run-item-other-a%d-n3", i), "item-other", "verify", "express-1", at)
	}
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("fewer than five runs per (item, step) in ten minutes opened notices: %+v", notices)
	}

	// The fifth run of the same step of the same item within ten minutes opens one alert
	// naming the item, the step, the lane, the count, and the newest run log in that lane.
	newestRun := "run-item-loop-a5-n3"
	allocate(newestRun, "item-loop", "verify", "express-1", now.Add(-30*time.Second))
	first := pending()
	if len(first) != 1 || first[0].Bundle.RuleID != ruleID || first[0].Bundle.Subject != "item-loop/verify" || first[0].Bundle.Severity != "warning" {
		t.Fatalf("want one warning notice for item-loop/verify, got %+v", first)
	}
	guidance := first[0].Bundle.Guidance
	checkText := strings.Join(guidance.Check, " ")
	doText := strings.Join(guidance.Do, " ")
	allGuidance := guidance.Summary + "\n" + checkText + "\n" + doText
	for _, want := range []string{"item-loop", "verify", "express-1", "5"} {
		if !strings.Contains(guidance.Summary, want) {
			t.Fatalf("summary %q does not name %q", guidance.Summary, want)
		}
	}
	if !strings.Contains(checkText, ".tillr/lanes/express-1/"+newestRun+".log") ||
		!strings.Contains(strings.ToLower(checkText), "refusal") ||
		!strings.Contains(allGuidance, "express-1") {
		t.Fatalf("guidance does not tell reader to open newest run log in express-1 for adapter refusal line: %+v", guidance)
	}

	// Further runs of the same step inside the window keep the same single episode open.
	allocate("run-item-loop-a6-n3", "item-loop", "verify", "express-1", now)
	repeated := pending()
	if len(repeated) != 1 || repeated[0].AlertID != first[0].AlertID {
		t.Fatalf("sixth run inside window should keep one episode: first=%+v repeated=%+v", first, repeated)
	}

	// Once fewer than five runs remain within the ten-minute window, the episode resolves.
	now = now.Add(8 * time.Minute)
	if aged := pending(); len(aged) != 0 {
		t.Fatalf("episode should resolve once fewer than five runs remain in the 10m window, got %+v", aged)
	}

	// Evaluating all command-eligible rules in config/tillr/watch on five run.allocate
	// starts of one item step in ten minutes opens only drain-repeated-step-dispatches.
	allStore, err := store.Open(ctx, filepath.Join(t.TempDir(), "all-rules.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { allStore.Close() })
	allEval := watch.Evaluator{Store: allStore, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := allEval.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	stepRuns := []string{
		"run-item-all-a1-n3",
		"run-item-all-a1-n3-failover",
		"run-item-all-a1-n3-002-failover",
		"run-item-all-a1-n3-003-failover",
		"run-item-all-a1-n3-004-failover",
	}
	for i, runID := range stepRuns {
		at := now.Add(-time.Duration(len(stepRuns)-1-i) * time.Minute)
		body, err := json.Marshal(map[string]any{
			"run_id":     runID,
			"item_id":    "item-all",
			"backend":    "pi-verify",
			"seat":       "verify",
			"workflow":   "planned-build@v1",
			"node":       "verify",
			"lane_id":    "express-1",
			"started_at": at.UTC().Format(time.RFC3339),
		})
		if err != nil {
			t.Fatal(err)
		}
		lane := "express-1"
		if err := allStore.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: allocateKind, ObjectType: "lane", ObjectID: &lane, At: at, Payload: body})
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
	if len(allNotices) != 1 || allNotices[0].Bundle.RuleID != ruleID || allNotices[0].Bundle.Subject != "item-all/verify" {
		t.Fatalf("want only %s for item-all/verify across config/tillr/watch, got %+v", ruleID, allNotices)
	}
}
