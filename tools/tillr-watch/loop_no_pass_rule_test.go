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
	"github.com/mschulkind-oss/tillr-drain/restart"
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
		notices, err := e.Pending(ctx, "operator-session")
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

func TestDrainHostDeathRelaunchQuietsBothRules(t *testing.T) {
	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	var selected []watch.Rule
	for _, r := range rules {
		if r.ID == "drain-loop-no-pass" || r.ID == "drain-unrequested-start" {
			selected = append(selected, r)
		}
	}
	if len(selected) != 2 {
		t.Fatalf("loaded %d rules, want 2 (drain-loop-no-pass and drain-unrequested-start)", len(selected))
	}

	ctx := context.Background()
	t0 := time.Date(2026, 11, 27, 8, 20, 0, 0, time.UTC)

	appendPass := func(t *testing.T, s *store.Store, sessionID string, at time.Time) {
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

	appendBuild := func(t *testing.T, s *store.Store, at time.Time, payload string) {
		t.Helper()
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       restart.BuildKind,
				ObjectType: "build",
				At:         at,
				Payload:    []byte(payload),
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	t.Run("host death relaunch opens neither alert", func(t *testing.T) {
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })

		now := t0
		e := watch.Evaluator{Store: s, Rules: selected, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		// Prior container recorded a pass at t0, then died with the container.
		appendPass(t, s, "sess-prev", t0)
		// 9 minutes later the container restarts and the new loop records session_relaunch=true
		// before its first drain.loop-pass.
		relaunchAt := t0.Add(9 * time.Minute)
		appendBuild(t, s, relaunchAt, `{"role":"drain","session_relaunch":true,"prior_domain":"boot-1:pid:[100]"}`)

		now = relaunchAt.Add(time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		for _, receiver := range []string{"operator-session", "triage"} {
			notices, err := e.Pending(ctx, receiver)
			if err != nil {
				t.Fatal(err)
			}
			if len(notices) != 0 {
				t.Fatalf("receiver %q: expected 0 notices after host-death relaunch, got %+v", receiver, notices)
			}
		}

		// Once the new loop records a pass, a later 5m silence still opens drain-loop-no-pass.
		newPassAt := relaunchAt.Add(5 * time.Second)
		appendPass(t, s, "sess-new", newPassAt)
		now = newPassAt.Add(5 * time.Minute)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "operator-session")
		if err != nil {
			t.Fatal(err)
		}
		if len(notices) != 1 || notices[0].Bundle.RuleID != "drain-loop-no-pass" {
			t.Fatalf("expected drain-loop-no-pass after subsequent silence, got %+v", notices)
		}
	})

	t.Run("loop ended by itself without stop record opens both alerts", func(t *testing.T) {
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })

		now := t0
		e := watch.Evaluator{Store: s, Rules: selected, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		// Loop recorded a pass at t0, then ended by itself in the same domain (no stop record).
		appendPass(t, s, "sess-same", t0)
		// 6 minutes later a replacement loop starts with no stop/restart/relaunch provenance.
		restartAt := t0.Add(6 * time.Minute)
		appendBuild(t, s, restartAt, `{"role":"drain"}`)

		now = restartAt.Add(time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		triageNotices, err := e.Pending(ctx, "triage")
		if err != nil {
			t.Fatal(err)
		}
		if len(triageNotices) != 1 || triageNotices[0].Bundle.RuleID != "drain-unrequested-start" {
			t.Fatalf("expected drain-unrequested-start notice, got %+v", triageNotices)
		}
		opNotices, err := e.Pending(ctx, "operator-session")
		if err != nil {
			t.Fatal(err)
		}
		if len(opNotices) != 1 || opNotices[0].Bundle.RuleID != "drain-loop-no-pass" {
			t.Fatalf("expected drain-loop-no-pass notice, got %+v", opNotices)
		}
	})

	t.Run("view build-started does not clear drain-loop-no-pass", func(t *testing.T) {
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })

		now := t0
		e := watch.Evaluator{Store: s, Rules: selected, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		appendPass(t, s, "sess-1", t0)
		appendBuild(t, s, t0.Add(6*time.Minute), `{"role":"view","session_relaunch":true}`)

		now = t0.Add(6*time.Minute + time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		opNotices, err := e.Pending(ctx, "operator-session")
		if err != nil {
			t.Fatal(err)
		}
		if len(opNotices) != 1 || opNotices[0].Bundle.RuleID != "drain-loop-no-pass" {
			t.Fatalf("expected drain-loop-no-pass after view build-started, got %+v", opNotices)
		}
	})
}
