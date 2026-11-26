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
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainLoopNoPassRule(t *testing.T) {
	const ruleID = "drain-loop-no-pass"
	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule", ruleID)
	}

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	t0 := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	now := t0
	e := watch.Evaluator{
		Store: s,
		Rules: rules[idx : idx+1],
		Clock: func() time.Time { return now },
	}

	appendPass := func(sessionID string, at time.Time) {
		t.Helper()
		body, err := json.Marshal(drain.LoopPassFact{SessionID: sessionID})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       drain.LoopPassKind,
				ObjectType: "session",
				ObjectID:   &sessionID,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	pendingAt := func(at time.Time) []watch.Notice {
		t.Helper()
		now = at
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		return notices
	}

	// a) Before any drain.loop-pass event, 0 pending notices.
	if notices := pendingAt(t0); len(notices) != 0 {
		t.Fatalf("expected 0 notices before any %s event, got %+v", drain.LoopPassKind, notices)
	}

	// b) Append a drain.loop-pass event at t0; at t0 + 4m, 0 pending notices.
	appendPass("sess-1", t0)
	if notices := pendingAt(t0.Add(4 * time.Minute)); len(notices) != 0 {
		t.Fatalf("expected 0 notices at 4m after %s, got %+v", drain.LoopPassKind, notices)
	}

	// c) At t0 + 5m, 1 pending notice for drain-loop-no-pass.
	notices := pendingAt(t0.Add(5 * time.Minute))
	if len(notices) != 1 {
		t.Fatalf("expected 1 notice at 5m, got %+v", notices)
	}
	first := notices[0]
	if first.Bundle.RuleID != ruleID {
		t.Fatalf("unexpected rule ID: bundle=%s", first.Bundle.RuleID)
	}
	if first.Bundle.Subject != "global" {
		t.Fatalf("want subject global, got %q", first.Bundle.Subject)
	}
	if first.Bundle.Severity != "warning" {
		t.Fatalf("want severity warning, got %q", first.Bundle.Severity)
	}
	if !strings.Contains(first.Bundle.Guidance.Summary, "5m") {
		t.Fatalf("summary lacks 5m elapsed: %q", first.Bundle.Guidance.Summary)
	}
	checkText := strings.ToLower(strings.Join(first.Bundle.Guidance.Check, " "))
	if !strings.Contains(checkText, "alive") || !strings.Contains(checkText, "log") {
		t.Fatalf("guidance.check lacks process liveness or log instruction: %+v", first.Bundle.Guidance.Check)
	}
	if len(first.Bundle.Guidance.Do) == 0 {
		t.Fatalf("expected non-empty guidance.do")
	}

	// d) At t0 + 6m, same single AlertID (still 1 pending notice).
	notices = pendingAt(t0.Add(6 * time.Minute))
	if len(notices) != 1 {
		t.Fatalf("expected 1 notice at 6m, got %+v", notices)
	}
	if notices[0].AlertID != first.AlertID {
		t.Fatalf("expected same AlertID %q at 6m, got %q", first.AlertID, notices[0].AlertID)
	}

	// e) Newer drain.loop-pass at t0 + 6m1s resolves the episode at t0 + 6m2s.
	appendPass("sess-2", t0.Add(6*time.Minute+time.Second))
	if notices := pendingAt(t0.Add(6*time.Minute + 2*time.Second)); len(notices) != 0 {
		t.Fatalf("expected 0 pending notices after newer %s, got %+v", drain.LoopPassKind, notices)
	}
	var state string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT state FROM alert_episodes WHERE alert_id = ?`, first.AlertID).Scan(&state)
	}); err != nil {
		t.Fatal(err)
	}
	if state != "resolved" {
		t.Fatalf("expected episode %q to be resolved, got %q", first.AlertID, state)
	}
}
