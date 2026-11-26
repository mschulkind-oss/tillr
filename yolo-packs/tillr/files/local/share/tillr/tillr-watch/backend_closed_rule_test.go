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

func TestDrainBackendClosedRule(t *testing.T) {
	const (
		ruleID       = "drain-backend-closed"
		outcomeKind  = "drain.backend-outcome"
		probeKind    = "drain.backend-probe"
		finishKind   = "run.finish"
		primaryBack  = "pi"
		secondaryBck = "claude"
	)

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
	if rule.Scope != "core" || rule.Severity != "high" || rule.Delivery.Receiver != "foreground" ||
		!slices.Equal(rule.Delivery.AlsoNotify, []string{"subscribers"}) {
		t.Fatalf("unexpected %s metadata: scope=%q severity=%q receiver=%q also_notify=%v",
			ruleID, rule.Scope, rule.Severity, rule.Delivery.Receiver, rule.Delivery.AlsoNotify)
	}

	ctx := context.Background()
	scratchDir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(scratchDir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 22, 16, 30, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}

	var terminalSeq int64
	appendOutcome := func(backend, class string, closes bool, cause string, includeCause bool, at time.Time) {
		t.Helper()
		terminalSeq++
		payload := map[string]any{
			"terminal_seq": terminalSeq,
			"class":        class,
			"closes":       closes,
		}
		if includeCause {
			payload["cause"] = cause
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		b := backend
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       outcomeKind,
				ObjectType: "run",
				ObjectID:   &b,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	appendProbe := func(backend string, success bool, at time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"success": success})
		if err != nil {
			t.Fatal(err)
		}
		b := backend
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       probeKind,
				ObjectType: "run",
				ObjectID:   &b,
				At:         at,
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

	// 1. Empty store and non-closing outcomes or probes before any closure stay quiet.
	if notices := evaluatePending(); len(notices) != 0 {
		t.Fatalf("empty store opened notices: %+v", notices)
	}
	appendOutcome(primaryBack, "answered", false, "", false, now)
	appendProbe(primaryBack, true, now)
	appendProbe(primaryBack, false, now)
	if notices := evaluatePending(); len(notices) != 0 {
		t.Fatalf("non-closing outcome or probes before closure opened notices: %+v", notices)
	}

	// 2. Closing drain.backend-outcome opens a high-severity foreground (and subscribers)
	// episode naming the backend, the run, and the reason text; omitting cause renders "unknown".
	firstCause := "run run-item-1-a1 on pi (exited), normal-exit, exit 1: auth token expired"
	appendOutcome(primaryBack, "infrastructure", true, firstCause, true, now)
	appendOutcome(secondaryBck, "infrastructure", true, "", false, now)

	first := evaluatePending()
	if len(first) != 2 {
		t.Fatalf("want 2 foreground notices for pi and claude, got %d: %+v", len(first), first)
	}
	subNotices, err := e.Pending(ctx, "subscribers")
	if err != nil {
		t.Fatal(err)
	}
	if len(subNotices) != 2 {
		t.Fatalf("want 2 subscriber notices, got %d: %+v", len(subNotices), subNotices)
	}

	piFirst, ok := findNotice(first, primaryBack)
	if !ok || piFirst.Bundle.Severity != "high" {
		t.Fatalf("missing high-severity notice for %s in %+v", primaryBack, first)
	}
	g := piFirst.Bundle.Guidance
	checkText := strings.Join(g.Check, " ")
	doText := strings.Join(g.Do, " ")
	if !strings.Contains(g.Summary, primaryBack) ||
		!strings.Contains(g.Summary, "run-item-1-a1") ||
		!strings.Contains(g.Summary, "auth token expired") {
		t.Fatalf("summary missing backend, run, or reason text: %q", g.Summary)
	}
	if !strings.Contains(checkText, primaryBack) ||
		!strings.Contains(checkText, "run-item-1-a1") ||
		!strings.Contains(checkText, "auth token expired") {
		t.Fatalf("check guidance missing backend, run, or reason text: %v", g.Check)
	}
	if !strings.Contains(doText, "tillr drain probe-backend") || !strings.Contains(doText, "--backend "+primaryBack) {
		t.Fatalf("do guidance missing probe-backend command for %s: %v", primaryBack, g.Do)
	}

	claudeFirst, ok := findNotice(first, secondaryBck)
	if !ok || claudeFirst.AlertID == piFirst.AlertID {
		t.Fatalf("expected distinct notice for %s, got %+v", secondaryBck, first)
	}
	if !strings.Contains(claudeFirst.Bundle.Guidance.Summary, secondaryBck) ||
		!strings.Contains(claudeFirst.Bundle.Guidance.Summary, "unknown") {
		t.Fatalf("expected missing cause to render unknown on %s: %q", secondaryBck, claudeFirst.Bundle.Guidance.Summary)
	}

	// 3. Repeat closing outcomes, failed probes, interrupted sibling outcomes, and >24h
	// elapsed wall time all keep the single episode open with the same AlertID.
	now = now.Add(5 * time.Minute)
	appendOutcome(primaryBack, "infrastructure", true, "run run-item-1-a2 on pi (exited), normal-exit, exit 1: auth token expired", true, now)
	appendProbe(primaryBack, false, now.Add(time.Minute))
	appendOutcome(primaryBack, "interrupted", false, "", false, now.Add(2*time.Minute))
	now = now.Add(26 * time.Hour)

	stillOpen := evaluatePending()
	if len(stillOpen) != 2 {
		t.Fatalf("episodes should remain open across repeats, failed probe, interrupted run, and >24h, got %+v", stillOpen)
	}
	if got, ok := findNotice(stillOpen, primaryBack); !ok || got.AlertID != piFirst.AlertID {
		t.Fatalf("pi AlertID changed while still closed: got %+v, want %q", got, piFirst.AlertID)
	}
	if got, ok := findNotice(stillOpen, secondaryBck); !ok || got.AlertID != claudeFirst.AlertID {
		t.Fatalf("claude AlertID changed while still closed: got %+v, want %q", got, claudeFirst.AlertID)
	}

	// 4. Reopening pi via drain.backend-probe (success == true) resolves pi's episode as
	// condition-cleared while leaving claude open.
	now = now.Add(5 * time.Minute)
	appendProbe(primaryBack, true, now)
	afterProbeReopen := evaluatePending()
	if len(afterProbeReopen) != 1 {
		t.Fatalf("want only %s remaining open after %s probe succeeded, got %+v", secondaryBck, primaryBack, afterProbeReopen)
	}
	if got, ok := findNotice(afterProbeReopen, secondaryBck); !ok || got.AlertID != claudeFirst.AlertID {
		t.Fatalf("expected %s to remain open, got %+v", secondaryBck, afterProbeReopen)
	}
	var state, resolvedReason string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(
			`SELECT state, COALESCE(resolved_reason, '') FROM alert_episodes WHERE alert_id = ?`,
			piFirst.AlertID,
		).Scan(&state, &resolvedReason)
	}); err != nil {
		t.Fatal(err)
	}
	if state != "resolved" || resolvedReason != "condition-cleared" {
		t.Fatalf("first pi episode state=%q resolved_reason=%q, want resolved/condition-cleared", state, resolvedReason)
	}

	// 5. A second close on pi opens a fresh episode with a new AlertID naming the second
	// closing run and reason, and a non-interrupted non-closing drain.backend-outcome clears it.
	now = now.Add(75 * time.Minute)
	secondCause := "run run-item-2-a1 on pi (exited), normal-exit, exit 1: upstream gateway timeout"
	appendOutcome(primaryBack, "infrastructure", true, secondCause, true, now)
	afterSecondClose := evaluatePending()
	if len(afterSecondClose) != 2 {
		t.Fatalf("want 2 open episodes after second %s close, got %+v", primaryBack, afterSecondClose)
	}
	piSecond, ok := findNotice(afterSecondClose, primaryBack)
	if !ok || piSecond.AlertID == "" || piSecond.AlertID == piFirst.AlertID {
		t.Fatalf("second %s close did not open a fresh episode: first=%q second=%+v", primaryBack, piFirst.AlertID, piSecond)
	}
	if !strings.Contains(piSecond.Bundle.Guidance.Summary, primaryBack) ||
		!strings.Contains(piSecond.Bundle.Guidance.Summary, "run-item-2-a1") ||
		!strings.Contains(piSecond.Bundle.Guidance.Summary, "upstream gateway timeout") {
		t.Fatalf("second episode summary missing backend, run, or reason: %q", piSecond.Bundle.Guidance.Summary)
	}

	now = now.Add(10 * time.Minute)
	appendOutcome(primaryBack, "answered", false, "", false, now)
	afterOutcomeReopen := evaluatePending()
	if len(afterOutcomeReopen) != 1 {
		t.Fatalf("want %s resolved after non-interrupted non-closing outcome, got %+v", primaryBack, afterOutcomeReopen)
	}
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(
			`SELECT state, COALESCE(resolved_reason, '') FROM alert_episodes WHERE alert_id = ?`,
			piSecond.AlertID,
		).Scan(&state, &resolvedReason)
	}); err != nil {
		t.Fatal(err)
	}
	if state != "resolved" || resolvedReason != "condition-cleared" {
		t.Fatalf("second pi episode state=%q resolved_reason=%q, want resolved/condition-cleared", state, resolvedReason)
	}

	// 6. End-to-end across all rules in config/tillr/watch using real drain.RecordBackendOutcomes
	// and drain.ProbeBackend on a scratch store: closing opens only drain-backend-closed,
	// a failing probe keeps it open, and a passing probe clears it.
	allStorePath := filepath.Join(scratchDir, "all-rules.db")
	allStore, err := store.Open(ctx, allStorePath, true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { allStore.Close() })

	allEval := watch.Evaluator{Store: allStore, Rules: rules}
	if _, err := allEval.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}

	exitCode := 1
	finishBody, err := json.Marshal(map[string]any{
		"run_id":        "run-item-real-a1",
		"item_id":       "item-real",
		"lane_id":       "lane-1",
		"backend":       primaryBack,
		"state":         "exited",
		"end_cause":     "normal-exit",
		"exit_code":     exitCode,
		"stderr_tail":   "provider rate socket closed",
		"started_at":    "2026-11-22T16:29:00Z",
		"finished_at":   "2026-11-22T16:30:00Z",
		"artifact_refs": []any{},
		"spend_status":  "unavailable",
	})
	if err != nil {
		t.Fatal(err)
	}
	runObjID := "run-item-real-a1"
	if err := allStore.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       finishKind,
			ObjectType: "run",
			ObjectID:   &runObjID,
			At:         time.Now().UTC(),
			Payload:    finishBody,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := drain.RecordBackendOutcomes(ctx, allStorePath); err != nil {
		t.Fatal(err)
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
	if len(allNotices) != 1 || allNotices[0].Bundle.RuleID != ruleID || allNotices[0].Bundle.Subject != primaryBack {
		t.Fatalf("want only %s for %s across config/tillr/watch, got %+v", ruleID, primaryBack, allNotices)
	}
	if !strings.Contains(allNotices[0].Bundle.Guidance.Summary, "run-item-real-a1") ||
		!strings.Contains(allNotices[0].Bundle.Guidance.Summary, "provider rate socket closed") {
		t.Fatalf("real RecordBackendOutcomes notice missing run or stderr reason: %q", allNotices[0].Bundle.Guidance.Summary)
	}

	if err := drain.ProbeBackend(ctx, allStorePath, scratchDir, primaryBack, []string{"/bin/false"}); err == nil {
		t.Fatal("expected /bin/false probe to return an error")
	}
	if _, err := allEval.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	stillPending, err := allEval.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(stillPending) != 1 || stillPending[0].AlertID != allNotices[0].AlertID {
		t.Fatalf("failed ProbeBackend should keep episode open: got %+v", stillPending)
	}

	if err := drain.ProbeBackend(ctx, allStorePath, scratchDir, primaryBack, []string{"/bin/true"}); err != nil {
		t.Fatalf("passing ProbeBackend failed: %v", err)
	}
	if _, err := allEval.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	clearedNotices, err := allEval.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(clearedNotices) != 0 {
		t.Fatalf("passing ProbeBackend should resolve %s episode, got %+v", ruleID, clearedNotices)
	}
}
