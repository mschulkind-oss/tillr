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

func TestFastGateSlowRules(t *testing.T) {
	rules, err := watch.LoadRules("../../config/tillr/watch")
	if err != nil {
		t.Fatal(err)
	}
	findRule := func(id, group string, window time.Duration, clear bool) watch.Rule {
		t.Helper()
		idx := slices.IndexFunc(rules, func(r watch.Rule) bool { return r.ID == id })
		if idx < 0 {
			t.Fatalf("missing rule %s", id)
		}
		r := rules[idx]
		if r.Scope != "core" || r.Severity != "warning" || !r.Enabled ||
			!slices.Equal(r.Reads.Events, []string{drain.FastGateTimingKind}) || len(r.Reads.Projections) != 0 ||
			r.When.Pattern != watch.PredicateMatch || r.When.GroupBy != group || r.When.Window != window ||
			r.When.Match == nil || (r.When.ClearWhen != nil) != clear || r.Delivery.Receiver != "foreground" {
			t.Fatalf("unexpected configuration for %s: %+v", id, r)
		}
		return r
	}
	runRule := findRule("fast-gate-run-slow", "slowest_check", time.Hour, false)
	medianRule := findRule("fast-gate-median-slow", "global", 24*time.Hour, true)

	fixture := func(t *testing.T, rule watch.Rule) (func(time.Time, map[string]any), func() []watch.Notice, time.Time) {
		t.Helper()
		ctx := context.Background()
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
		e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
		n := 0
		appendTiming := func(at time.Time, payload map[string]any) {
			t.Helper()
			n++
			run := fmt.Sprintf("run-%d", n)
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Append(event.Event{Kind: drain.FastGateTimingKind, ObjectType: "run", ObjectID: &run, At: at, Payload: body})
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
			if len(results) != 1 || results[0].Diagnostic() != "" {
				t.Fatalf("evaluation diagnostic: %+v", results)
			}
			notices, err := e.Pending(ctx, "foreground")
			if err != nil {
				t.Fatal(err)
			}
			return notices
		}
		return appendTiming, pending, now
	}

	t.Run("run", func(t *testing.T) {
		appendTiming, pending, now := fixture(t, runRule)
		appendTiming(now.Add(-2*time.Hour), map[string]any{"wall_ms": 61000, "slowest_check": "old-check"})
		appendTiming(now, map[string]any{"wall_ms": 59000, "slowest_check": "quiet-check"})
		appendTiming(now, map[string]any{"wall_ms": 60000, "slowest_check": "boundary-check"})
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("59s, 60s boundary, and out-of-window runs should not alert: %+v", notices)
		}
		appendTiming(now, map[string]any{"wall_ms": 61000, "slowest_check": "go-test:example"})
		notices := pending()
		if len(notices) != 1 || notices[0].Bundle.Subject != "go-test:example" {
			t.Fatalf("expected named slowest check: %+v", notices)
		}
		guidance := notices[0].Bundle.Guidance
		if !strings.Contains(guidance.Summary, "go-test:example") || !strings.Contains(guidance.Summary, "longer than 60 seconds") ||
			!strings.Contains(strings.Join(guidance.Check, " "), "gate.timing.jsonl") ||
			!strings.Contains(strings.Join(guidance.Check, " "), "unmeasured") {
			t.Fatalf("missing rendered check or investigation guidance: %+v", guidance)
		}
		firstID := notices[0].AlertID
		appendTiming(now, map[string]any{"wall_ms": 60001, "slowest_check": "go-test:example"})
		appendTiming(now, map[string]any{"wall_ms": 59000, "slowest_check": "go-test:example"})
		if again := pending(); len(again) != 1 || again[0].AlertID != firstID {
			t.Fatalf("repeat slow run or faster run should retain the episode: %+v", again)
		}
		appendTiming(now, map[string]any{"wall_ms": 60001, "slowest_check": "unmeasured"})
		notices = pending()
		idx := slices.IndexFunc(notices, func(n watch.Notice) bool { return n.Bundle.Subject == "unmeasured" })
		if len(notices) != 2 || idx < 0 {
			t.Fatalf("expected a separate unmeasured alert: %+v", notices)
		}
		guidance = notices[idx].Bundle.Guidance
		if !strings.Contains(guidance.Summary, "unmeasured") || !strings.Contains(strings.Join(guidance.Check, " "), "no check") {
			t.Fatalf("unmeasured fallback not explained: %+v", guidance)
		}
	})

	t.Run("median", func(t *testing.T) {
		appendTiming, pending, now := fixture(t, medianRule)
		appendTiming(now.Add(-25*time.Hour), map[string]any{"wall_ms": 1, "slowest_check": "x", "median20_wall_ms": 90001})
		appendTiming(now, map[string]any{"wall_ms": 1, "slowest_check": "x"})
		appendTiming(now, map[string]any{"wall_ms": 1, "slowest_check": "x", "median20_wall_ms": 89999})
		appendTiming(now, map[string]any{"wall_ms": 1, "slowest_check": "x", "median20_wall_ms": 90000})
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("absent, boundary, or out-of-window median alerted: %+v", notices)
		}
		appendTiming(now, map[string]any{"wall_ms": 1, "slowest_check": "x", "median20_wall_ms": 90001})
		notices := pending()
		if len(notices) != 1 || notices[0].Bundle.Subject != "global" {
			t.Fatalf("expected median alert: %+v", notices)
		}
		firstID := notices[0].AlertID
		check := strings.Join(notices[0].Bundle.Guidance.Check, " ")
		if !strings.Contains(check, drain.FastGateTimingKind) || !strings.Contains(check, "per-run") {
			t.Fatalf("median guidance does not point to timing events and per-run alerts: %s", check)
		}
		appendTiming(now, map[string]any{"wall_ms": 1, "slowest_check": "x"})
		if notices := pending(); len(notices) != 1 || notices[0].AlertID != firstID {
			t.Fatalf("absent median must not clear the alert: %+v", notices)
		}
		appendTiming(now, map[string]any{"wall_ms": 1, "slowest_check": "x", "median20_wall_ms": 90000})
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("median at threshold should resolve: %+v", notices)
		}
		appendTiming(now, map[string]any{"wall_ms": 1, "slowest_check": "x", "median20_wall_ms": 90001})
		notices = pending()
		if len(notices) != 1 || notices[0].AlertID == firstID {
			t.Fatalf("subsequent rise should open a new episode: %+v", notices)
		}
	})
}
