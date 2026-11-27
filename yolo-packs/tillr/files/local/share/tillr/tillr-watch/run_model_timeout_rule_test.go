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

func TestRunModelTimeoutRule(t *testing.T) {
	const (
		ruleID      = "run-model-timeout"
		timeoutKind = "run.model-timeout"
		finishKind  = "run.finish"
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

	now := time.Date(2026, 11, 22, 10, 0, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	appendTimeout := func(runID, itemID, laneID, errText string, count int, at time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"run_id":  runID,
			"item_id": itemID,
			"lane_id": laneID,
			"backend": "pi-designer",
			"node":    "work",
			"error":   errText,
			"count":   count,
		})
		if err != nil {
			t.Fatal(err)
		}
		id := runID
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       timeoutKind,
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
	appendFinish := func(runID, state string, at time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"run_id": runID,
			"state":  state,
		})
		if err != nil {
			t.Fatal(err)
		}
		id := runID
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       finishKind,
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
		notices, err := e.Pending(ctx, "tillr-triage")
		if err != nil {
			t.Fatal(err)
		}
		return notices
	}

	if notices := pending(); len(notices) != 0 {
		t.Fatalf("empty store opened notices: %+v", notices)
	}

	// A single timeout in one run (and a single timeout in a second run) stays quiet.
	appendTimeout("run-a", "item-a", "lane-6", "terminated: Body Timeout Error (UND_ERR_BODY_TIMEOUT)", 1, now.Add(-10*time.Minute))
	appendTimeout("run-b", "item-b", "lane-2", "Request timed out.: This operation was aborted (20)", 1, now.Add(-8*time.Minute))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("single timeout per run opened notices: %+v", notices)
	}

	// The second timeout in run-a opens one warning alert naming run, lane, count and error.
	secondErr := "Request timed out.: This operation was aborted (20)"
	appendTimeout("run-a", "item-a", "lane-6", secondErr, 2, now.Add(-5*time.Minute))
	first := pending()
	if len(first) != 1 || first[0].Bundle.RuleID != ruleID || first[0].Bundle.Subject != "run-a" || first[0].Bundle.Severity != "warning" {
		t.Fatalf("want one warning notice for run-a, got %+v", first)
	}
	guidance := first[0].Bundle.Guidance
	for _, want := range []string{"run-a", "lane-6", "2", secondErr} {
		if !strings.Contains(guidance.Summary, want) {
			t.Fatalf("summary %q does not name %q", guidance.Summary, want)
		}
	}
	checkText := strings.Join(guidance.Check, " ")
	if !strings.Contains(checkText, ".tillr/items/lane-6/run-a.log") {
		t.Fatalf("check guidance %q does not name lane run log", checkText)
	}

	// A third timeout in run-a keeps the same single episode open.
	appendTimeout("run-a", "item-a", "lane-6", secondErr, 3, now.Add(-2*time.Minute))
	repeated := pending()
	if len(repeated) != 1 || repeated[0].AlertID != first[0].AlertID {
		t.Fatalf("third timeout should keep one episode: first=%+v repeated=%+v", first, repeated)
	}

	// A pause finish (state == "paused") keeps the episode open across restart.
	appendFinish("run-a", "paused", now.Add(-1*time.Minute))
	paused := pending()
	if len(paused) != 1 || paused[0].AlertID != first[0].AlertID {
		t.Fatalf("paused finish should keep episode open: %+v", paused)
	}

	// A terminal finish resolves the episode.
	appendFinish("run-a", "failed", now)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if st, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil }); err != nil || len(st.Alerts) != 0 {
		t.Fatalf("terminal finish should resolve episode, got %+v (err=%v)", st.Alerts, err)
	}

	// Evaluating all command-eligible rules in config/tillr/watch on two run.model-timeout
	// events in one run opens only run-model-timeout.
	allStore, err := store.Open(ctx, filepath.Join(t.TempDir(), "all-rules.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { allStore.Close() })
	allEval := watch.Evaluator{Store: allStore, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := allEval.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 2; i++ {
		body, err := json.Marshal(map[string]any{
			"run_id":  "run-all",
			"item_id": "item-all",
			"lane_id": "lane-6",
			"backend": "pi-designer",
			"node":    "work",
			"error":   "terminated: Body Timeout Error (UND_ERR_BODY_TIMEOUT)",
			"count":   i,
		})
		if err != nil {
			t.Fatal(err)
		}
		id := "run-all"
		if err := allStore.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: timeoutKind, ObjectType: "run", ObjectID: &id, At: now.Add(-time.Duration(3-i) * time.Minute), Payload: body})
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
	allNotices, err := allEval.Pending(ctx, "tillr-triage")
	if err != nil {
		t.Fatal(err)
	}
	if len(allNotices) != 1 || allNotices[0].Bundle.RuleID != ruleID || allNotices[0].Bundle.Subject != "run-all" {
		t.Fatalf("want only %s for run-all across config/tillr/watch, got %+v", ruleID, allNotices)
	}
}
