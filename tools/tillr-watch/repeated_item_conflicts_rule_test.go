package main

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
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

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	t0 := time.Date(2026, 11, 21, 10, 0, 0, 0, time.UTC)
	now := t0
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}

	setItemState := func(st *store.Store, itemID, state string) {
		t.Helper()
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(
				`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, ?, ?, ?)
				 ON CONFLICT(item_id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`,
				itemID, itemID, state, now.UnixMilli(),
			)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	n := 0
	conflictIn := func(st *store.Store, itemID, conflictID, status string, at time.Time) {
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
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
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
	conflict := func(itemID, conflictID, status string, at time.Time) {
		t.Helper()
		conflictIn(s, itemID, conflictID, status, at)
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

	setItemState(s, "item-open", "in-queue")
	setItemState(s, "item-blocked", "blocked")
	setItemState(s, "item-closed", "closed")
	setItemState(s, "item-landed", "landed")
	setItemState(s, "item-resolved", "resolved-elsewhere")

	// One raised item.conflict event in the window on an open item (plus an unrelated event) stays quiet.
	conflict("item-open", "conflict-open-0", "raised", now)
	refusalRun := "run-refusal"
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       drain.RefusalKind,
			ObjectType: "run",
			ObjectID:   &refusalRun,
			At:         now,
			Payload:    []byte(`{"item_id":"item-open","step":"verify-not-done"}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("one raised item.conflict event in the window should stay quiet, got %+v", notices)
	}

	// After conflict-open-0 ages past 24h, one new raised conflict on item-open and
	// one on item-blocked stay quiet, even after item-open's conflict transitions to
	// awaiting, answered, and closed when given to a designer.
	now = t0.Add(25 * time.Hour)
	conflict("item-open", "conflict-open-1", "raised", now)
	conflict("item-blocked", "conflict-blocked-1", "raised", now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("one in-window raised item.conflict event per open item should stay quiet, got %+v", notices)
	}

	now = t0.Add(25*time.Hour + time.Minute)
	conflict("item-open", "conflict-open-1", "awaiting", now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("one conflict going raised then awaiting should stay quiet, got %+v", notices)
	}

	now = t0.Add(25*time.Hour + 2*time.Minute)
	conflict("item-open", "conflict-open-1", "answered", now)
	conflict("item-open", "conflict-open-1", "closed", now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("status updates on a single conflict should stay quiet, got %+v", notices)
	}

	// With two in-window raised item.conflict events each, open item-open opens one
	// foreground notice while closed, landed, resolved-elsewhere, and missing items
	// at the same raised conflict count stay quiet.
	now = t0.Add(25*time.Hour + 3*time.Minute)
	conflict("item-open", "conflict-open-2", "raised", now)
	for _, subject := range []string{"item-closed", "item-landed", "item-resolved", "item-missing"} {
		conflict(subject, "conflict-"+subject+"-1", "raised", now.Add(-time.Minute))
		conflict(subject, "conflict-"+subject+"-2", "raised", now)
	}
	notices := pending()
	if len(notices) != 1 || notices[0].Bundle.RuleID != ruleID || notices[0].Bundle.Subject != "item-open" {
		t.Fatalf("want one notice only for open item-open while closed/finished items stay quiet, got %+v", notices)
	}
	var episodeCount int
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT COUNT(*) FROM alert_episodes`).Scan(&episodeCount)
	}); err != nil || episodeCount != 1 {
		t.Fatalf("want exactly 1 episode stored (no closed/finished episodes), got %d (err=%v)", episodeCount, err)
	}
	guidance := notices[0].Bundle.Guidance
	checkText := strings.Join(guidance.Check, " ")
	doText := strings.Join(guidance.Do, " ")
	allGuidance := strings.ToLower(checkText + " " + doText)
	if !strings.Contains(guidance.Summary, "item-open") ||
		!strings.Contains(guidance.Summary, "2") ||
		!strings.Contains(checkText, "tillr drain why item-open") ||
		!strings.Contains(allGuidance, "obstacle text") ||
		!strings.Contains(allGuidance, "request's own words") ||
		!strings.Contains(allGuidance, "design document") ||
		!strings.Contains(allGuidance, "state a cause it cannot establish") {
		t.Fatalf("notice lacks subject, count, or request-words guidance: %+v", guidance)
	}

	// A third raised item.conflict event in the window keeps the single open alert
	// episode on item-open, while a third raised event on item-closed still stays quiet.
	now = t0.Add(26*time.Hour + 3*time.Minute)
	conflict("item-open", "conflict-open-3", "raised", now)
	conflict("item-closed", "conflict-closed-3", "raised", now)
	if again := pending(); len(again) != 1 || again[0].AlertID != notices[0].AlertID {
		t.Fatalf("third raised item.conflict event should keep the same alert open, got %+v", again)
	}

	// A blocked item is also open: a second in-window raised item.conflict event opens
	// its alert, and closing or landing an open item resolves its episode immediately.
	now = now.Add(time.Minute)
	conflict("item-blocked", "conflict-blocked-2", "raised", now)
	both := pending()
	if len(both) != 2 || both[0].AlertID != notices[0].AlertID || both[1].Bundle.Subject != "item-blocked" {
		t.Fatalf("want open notices for item-open and item-blocked, got %+v", both)
	}

	now = now.Add(time.Minute)
	setItemState(s, "item-blocked", "closed")
	afterClose := pending()
	if len(afterClose) != 1 || afterClose[0].AlertID != notices[0].AlertID {
		t.Fatalf("closing item-blocked should resolve its alert while keeping item-open, got %+v", afterClose)
	}
	var blockedState, blockedReason string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT state, resolved_reason FROM alert_episodes WHERE alert_id = ?`, both[1].AlertID).Scan(&blockedState, &blockedReason)
	}); err != nil || blockedState != "resolved" || blockedReason != "condition-cleared" {
		t.Fatalf("want item-blocked episode resolved/condition-cleared, got state=%q reason=%q err=%v", blockedState, blockedReason, err)
	}

	now = now.Add(time.Minute)
	setItemState(s, "item-open", "landed")
	if afterLand := pending(); len(afterLand) != 0 {
		t.Fatalf("landing item-open should resolve its alert, got %+v", afterLand)
	}

	// An open item whose raised conflicts age past 24h also resolves once fewer than
	// two raised item.conflict events remain inside the 24h window.
	setItemState(s, "item-aging", "in-queue")
	tAging := now
	conflict("item-aging", "conflict-aging-1", "raised", tAging)
	now = tAging.Add(30 * time.Minute)
	conflict("item-aging", "conflict-aging-2", "raised", now)
	if agingOpen := pending(); len(agingOpen) != 1 || agingOpen[0].Bundle.Subject != "item-aging" {
		t.Fatalf("want one notice for open item-aging, got %+v", agingOpen)
	}
	now = tAging.Add(24*time.Hour + time.Minute)
	if aged := pending(); len(aged) != 0 {
		t.Fatalf("alert should resolve once fewer than two raised item.conflict events remain in 24h, got %+v", aged)
	}

	// On a charter where charter.closed is outside charter.finished (such as recorded-verdict),
	// closed items still stay quiet alongside finished items while open items alert.
	verdictStore, err := store.Open(ctx, filepath.Join(t.TempDir(), "verdict.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { verdictStore.Close() })
	defBytes, err := os.ReadFile("../tillr/examples/recorded-verdict/definition.json")
	if err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(defBytes)
	defRecord, err := json.Marshal(map[string]any{
		"definition": "recorded-verdict",
		"version":    1,
		"content":    string(defBytes),
		"sha256":     fmt.Sprintf("%x", sum),
		"default":    false,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := verdictStore.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta(key, value) VALUES ('store.definition', ?)`, string(defRecord))
		return err
	}); err != nil {
		t.Fatal(err)
	}
	setItemState(verdictStore, "rv-open", "in-queue")
	setItemState(verdictStore, "rv-closed", "closed")
	setItemState(verdictStore, "rv-confirmed", "confirmed")
	for _, subject := range []string{"rv-open", "rv-closed", "rv-confirmed"} {
		conflictIn(verdictStore, subject, "c-"+subject+"-1", "raised", now.Add(-time.Minute))
		conflictIn(verdictStore, subject, "c-"+subject+"-2", "raised", now)
	}
	verdictEval := watch.Evaluator{Store: verdictStore, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	if results, err := verdictEval.EvaluateRules(ctx); err != nil || len(results) != 1 || results[0].Diagnostic() != "" {
		t.Fatalf("recorded-verdict evaluation results: %+v, err=%v", results, err)
	}
	verdictNotices, err := verdictEval.Pending(ctx, "foreground")
	if err != nil || len(verdictNotices) != 1 || verdictNotices[0].Bundle.Subject != "rv-open" {
		t.Fatalf("want only rv-open to alert on recorded-verdict charter, got %+v (err=%v)", verdictNotices, err)
	}
}
