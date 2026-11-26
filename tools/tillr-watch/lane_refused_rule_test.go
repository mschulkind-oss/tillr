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

func TestDrainLaneRefusedRule(t *testing.T) {
	const ruleID = "drain-lane-refused"
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

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}

	appendRefused := func(lane, reason, detail, leftBy string, includeLeftBy bool) {
		t.Helper()
		payload := map[string]string{
			"lane_id": lane,
			"reason":  reason,
			"detail":  detail,
			"remedy":  "clean the lane by hand",
		}
		if includeLeftBy {
			payload["left_by"] = leftBy
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       "drain.lane-refused",
				ObjectType: "lane",
				ObjectID:   &lane,
				At:         now,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	appendRestored := func(lane string) {
		t.Helper()
		body, err := json.Marshal(map[string]string{"lane_id": lane})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       "drain.lane-restored",
				ObjectType: "lane",
				ObjectID:   &lane,
				At:         now,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	evaluatePending := func() []watch.Notice {
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

	findNotice := func(notices []watch.Notice, subject string) (watch.Notice, bool) {
		for _, n := range notices {
			if n.Bundle.RuleID == ruleID && n.Bundle.Subject == subject {
				return n, true
			}
		}
		return watch.Notice{}, false
	}

	// 1. drain.lane-restored before any refusal opens no alert.
	appendRestored("lane-0")
	if notices := evaluatePending(); len(notices) != 0 {
		t.Fatalf("expected no alert after drain.lane-restored before any refusal, got %+v", notices)
	}

	// 2. All four reasons open one alert per (lane_id, reason) with distinct AlertIDs,
	// including two different reasons on the same lane and across different lanes.
	appendRefused("lane-1", "lane-holds-uncommitted-work", "M dirty.go", "run-1 (item-1)", true)
	appendRefused("lane-1", "lane-holds-unlanded-change", "HEAD ahead of base", "run-2 (item-2)", true)
	appendRefused("lane-2", "lane-branch-held", "branch checked out elsewhere", "", false) // left_by absent -> "unknown"
	appendRefused("lane-3", "lane-run-children-live", "pid 42 alive", "", true)            // left_by empty -> "unknown"

	first := evaluatePending()
	if len(first) != 4 {
		t.Fatalf("want 4 notices across 4 (lane_id, reason) pairs, got %d: %+v", len(first), first)
	}

	// Check subscribers delivery is also populated.
	subNotices, err := e.Pending(ctx, "subscribers")
	if err != nil {
		t.Fatal(err)
	}
	if len(subNotices) != 4 {
		t.Fatalf("want 4 subscriber notices, got %d: %+v", len(subNotices), subNotices)
	}

	alertIDs := map[string]string{}
	for _, tc := range []struct {
		subject string
		lane    string
		reason  string
		detail  string
		leftBy  string
	}{
		{"lane-1/lane-holds-uncommitted-work", "lane-1", "lane-holds-uncommitted-work", "M dirty.go", "run-1 (item-1)"},
		{"lane-1/lane-holds-unlanded-change", "lane-1", "lane-holds-unlanded-change", "HEAD ahead of base", "run-2 (item-2)"},
		{"lane-2/lane-branch-held", "lane-2", "lane-branch-held", "branch checked out elsewhere", "unknown"},
		{"lane-3/lane-run-children-live", "lane-3", "lane-run-children-live", "pid 42 alive", "unknown"},
	} {
		n, ok := findNotice(first, tc.subject)
		if !ok {
			t.Fatalf("missing notice for subject %q in %+v", tc.subject, first)
		}
		alertIDs[tc.subject] = n.AlertID
		g := n.Bundle.Guidance
		if !strings.Contains(g.Summary, tc.lane) || !strings.Contains(g.Summary, tc.reason) || !strings.Contains(g.Summary, tc.leftBy) {
			t.Fatalf("summary for %s missing lane/reason/left_by: %q", tc.subject, g.Summary)
		}
		allCheck := strings.Join(g.Check, " ")
		if !strings.Contains(allCheck, tc.lane) || !strings.Contains(allCheck, tc.detail) || !strings.Contains(allCheck, tc.leftBy) {
			t.Fatalf("check guidance for %s missing lane/detail/left_by: %v", tc.subject, g.Check)
		}
		allDo := strings.Join(g.Do, " ")
		if !strings.Contains(allDo, tc.lane) || !strings.Contains(strings.ToLower(allDo), "root cause") {
			t.Fatalf("do guidance for %s missing lane or root cause: %v", tc.subject, g.Do)
		}
	}

	// Distinct AlertIDs across all four episodes.
	seenIDs := map[string]bool{}
	for _, id := range alertIDs {
		if seenIDs[id] {
			t.Fatalf("duplicate AlertID %q across distinct (lane_id, reason) episodes: %+v", id, alertIDs)
		}
		seenIDs[id] = true
	}

	// 3. Repeated identical events do not open duplicate alerts or replace AlertIDs.
	now = now.Add(5 * time.Minute)
	appendRefused("lane-1", "lane-holds-uncommitted-work", "M dirty.go", "run-1 (item-1)", true)
	again := evaluatePending()
	if len(again) != 4 {
		t.Fatalf("repeated identical event changed notice count to %d: %+v", len(again), again)
	}
	for subject, wantID := range alertIDs {
		n, ok := findNotice(again, subject)
		if !ok || n.AlertID != wantID {
			t.Fatalf("subject %q AlertID changed on repeat: got %+v, want %q", subject, n, wantID)
		}
	}

	// 4. Episodes stay open across elapsed wall time (hours later) without auto-resolving.
	now = now.Add(6 * time.Hour)
	hoursLater := evaluatePending()
	if len(hoursLater) != 4 {
		t.Fatalf("episodes should remain open hours later without drain.lane-restored, got %d: %+v", len(hoursLater), hoursLater)
	}
	for subject, wantID := range alertIDs {
		n, ok := findNotice(hoursLater, subject)
		if !ok || n.AlertID != wantID {
			t.Fatalf("subject %q AlertID changed after elapsed time: got %+v, want %q", subject, n, wantID)
		}
	}

	// 5. Appending drain.lane-restored for lane-1 resolves both active episodes for lane-1
	// as condition-cleared while leaving lane-2 and lane-3 open.
	now = now.Add(10 * time.Minute)
	appendRestored("lane-1")
	afterRestore := evaluatePending()
	if len(afterRestore) != 2 {
		t.Fatalf("want 2 remaining notices after restoring lane-1, got %d: %+v", len(afterRestore), afterRestore)
	}
	for _, kept := range []string{"lane-2/lane-branch-held", "lane-3/lane-run-children-live"} {
		n, ok := findNotice(afterRestore, kept)
		if !ok || n.AlertID != alertIDs[kept] {
			t.Fatalf("expected %q to remain open with AlertID %q, got %+v", kept, alertIDs[kept], n)
		}
	}
	for _, cleared := range []string{"lane-1/lane-holds-uncommitted-work", "lane-1/lane-holds-unlanded-change"} {
		var state, resolvedReason string
		if err := s.ReadTx(ctx, func(rd *store.Reader) error {
			return rd.QueryRow(
				`SELECT state, COALESCE(resolved_reason, '') FROM alert_episodes WHERE rule_id = ? AND subject_key = ?`,
				ruleID, cleared,
			).Scan(&state, &resolvedReason)
		}); err != nil {
			t.Fatal(err)
		}
		if state != "resolved" || resolvedReason != "condition-cleared" {
			t.Fatalf("episode %q state=%q resolved_reason=%q, want resolved/condition-cleared", cleared, state, resolvedReason)
		}
	}
}

func TestDrainLaneRefusedAlertTextNamesCurrentCause(t *testing.T) {
	const ruleID = "drain-lane-refused"
	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule", ruleID)
	}
	rule := rules[idx]

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}

	appendRefused := func(detail, leftBy string) {
		t.Helper()
		lane := "lane-2"
		body, err := json.Marshal(map[string]string{
			"lane_id": lane,
			"reason":  "lane-holds-unlanded-change",
			"detail":  detail,
			"remedy":  "inspect the fork checkout before clearing it",
			"left_by": leftBy,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       "drain.lane-refused",
				ObjectType: "lane",
				ObjectID:   &lane,
				At:         now,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	appendRefused("fork checkout /repo/.forks/lanes/lane-2/f has an undelivered HEAD", "run-1 (item-1)")
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	first, err := e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(first) != 1 {
		t.Fatalf("want 1 notice, got %d: %+v", len(first), first)
	}
	alertID := first[0].AlertID
	if !strings.Contains(strings.Join(first[0].Bundle.Guidance.Check, " "), "has an undelivered HEAD") {
		t.Fatalf("first guidance check = %v, want undelivered HEAD", first[0].Bundle.Guidance.Check)
	}

	// A newer refusal for the same (lane_id, reason) with a different cause must
	// keep the same episode/AlertID and update the alert text to the current cause.
	now = now.Add(2 * time.Minute)
	appendRefused("fork checkout /repo/.forks/lanes/lane-2/f has different upstream inputs", "run-2 (item-2)")
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(second) != 1 {
		t.Fatalf("want 1 notice after updated cause, got %d: %+v", len(second), second)
	}
	if second[0].AlertID != alertID {
		t.Fatalf("AlertID changed from %q to %q on updated cause", alertID, second[0].AlertID)
	}
	checkText := strings.Join(second[0].Bundle.Guidance.Check, " ")
	if !strings.Contains(checkText, "has different upstream inputs") {
		t.Fatalf("updated guidance check = %v, want current cause", second[0].Bundle.Guidance.Check)
	}
	if strings.Contains(checkText, "has an undelivered HEAD") {
		t.Fatalf("updated guidance check still names earlier cause: %v", second[0].Bundle.Guidance.Check)
	}
	if !strings.Contains(second[0].Bundle.Guidance.Summary, "run-2 (item-2)") {
		t.Fatalf("updated guidance summary = %q, want run-2 (item-2)", second[0].Bundle.Guidance.Summary)
	}
}
