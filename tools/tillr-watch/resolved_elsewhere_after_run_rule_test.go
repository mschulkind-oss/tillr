package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	tillrrun "github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestResolvedElsewhereAfterRunRule(t *testing.T) {
	const ruleID = "drain-resolved-elsewhere-after-run"

	allRules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(allRules, func(r watch.Rule) bool { return r.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule in config/tillr/watch", ruleID)
	}
	rule := allRules[idx]
	if rule.Scope != "core" || rule.Severity != "warning" || !rule.Enabled ||
		rule.Delivery.Receiver != "foreground" ||
		len(rule.Delivery.AlsoNotify) != 0 ||
		!slices.Equal(rule.Reads.Events, []string{tillrrun.AllocateKind, item.UpdateKind}) ||
		!slices.Equal(rule.Reads.Projections, []string{"items"}) ||
		rule.When.GroupBy != "item_id" ||
		rule.When.Pattern != watch.PredicateMatch ||
		rule.When.Window != 24*time.Hour ||
		!rule.When.KeepOnTerminalItem {
		t.Fatalf("unexpected %s configuration: %+v", ruleID, rule)
	}
	for _, text := range append([]string{rule.Guidance.Summary}, append(slices.Clone(rule.Guidance.Check), rule.Guidance.Do...)...) {
		if strings.Contains(strings.ToLower(text), "expected") {
			t.Fatalf("%s guidance calls a firing alarm expected: %q", ruleID, text)
		}
	}

	seedItem := func(ctx context.Context, t *testing.T, st *store.Store, itemID, state string, at time.Time) {
		t.Helper()
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(
				`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, ?, ?, ?)
				 ON CONFLICT(item_id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`,
				itemID, itemID, state, at.UnixMilli(),
			)
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	appendRunAllocate := func(ctx context.Context, t *testing.T, st *store.Store, runID, itemID, laneID, seat string, at time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"run_id":     runID,
			"item_id":    itemID,
			"lane_id":    laneID,
			"backend":    "pi",
			"seat":       seat,
			"node":       seat,
			"started_at": at.UTC().Format(time.RFC3339),
		})
		if err != nil {
			t.Fatal(err)
		}
		id := laneID
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       tillrrun.AllocateKind,
				ObjectType: "lane",
				ObjectID:   &id,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	appendItemUpdate := func(ctx context.Context, t *testing.T, st *store.Store, itemID, state, resolvesVia, reason string, at time.Time) {
		t.Helper()
		seedItem(ctx, t, st, itemID, state, at)
		payload := map[string]any{
			"item_id":    itemID,
			"title":      itemID,
			"state":      state,
			"updated_at": at.UTC().Format(time.RFC3339),
		}
		if resolvesVia != "" {
			payload["resolves_via"] = resolvesVia
		}
		if reason != "" {
			payload["lifecycle_reason"] = reason
			payload["lifecycle_by"] = "tillr-drain"
			payload["lifecycle_action"] = "state"
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		id := itemID
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       item.UpdateKind,
				ObjectType: "item",
				ObjectID:   &id,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	t0 := time.Date(2026, 11, 23, 17, 35, 0, 0, time.UTC)

	newHarness := func(t *testing.T, rules []watch.Rule) (context.Context, *store.Store, *watch.Evaluator, *time.Time) {
		t.Helper()
		ctx := context.Background()
		st, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { st.Close() })
		now := t0
		e := &watch.Evaluator{Store: st, Root: t.TempDir(), Rules: rules, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		return ctx, st, e, &now
	}

	t.Run("resolved elsewhere after worker runs opens, stays open, and clears if reopened", func(t *testing.T) {
		ctx, st, e, now := newHarness(t, []watch.Rule{rule})
		const (
			itemID      = "item-request-3a0ae4fd31"
			resolvesVia = "commit:3ed0291d91"
		)
		seedItem(ctx, t, st, itemID, "in-queue", t0)

		appendRunAllocate(ctx, t, st, "run-item-request-3a0ae4fd31-a1-n1", itemID, "lane-1", "plan", t0.Add(time.Minute))
		appendItemUpdate(ctx, t, st, itemID, "blocked", "", "", t0.Add(2*time.Minute))
		appendRunAllocate(ctx, t, st, "run-item-request-3a0ae4fd31-a2-n1", itemID, "lane-2", "build", t0.Add(3*time.Minute))
		appendItemUpdate(ctx, t, st, itemID, "resolved-elsewhere", resolvesVia, "already fixed on base by commit 3ed0291d91", t0.Add(5*time.Minute))

		*now = t0.Add(6 * time.Minute)
		res, err := e.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 || res[0].Skipped != 0 {
			t.Fatalf("eval results = %+v, want Skipped=0", res)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		if len(notices) != 1 || notices[0].Bundle.Subject != itemID {
			t.Fatalf("pending notices = %+v, want 1 for %s", notices, itemID)
		}
		summary := notices[0].Bundle.Guidance.Summary
		for _, want := range []string{itemID, resolvesVia} {
			if !strings.Contains(summary, want) {
				t.Fatalf("summary %q missing %q", summary, want)
			}
		}

		// Subsequent evaluation keeps the single episode open even though items.state == "resolved-elsewhere".
		*now = t0.Add(15 * time.Minute)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		stSnap, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(stSnap.Alerts) != 1 || stSnap.Alerts[0].State != "open" || stSnap.Alerts[0].Subject != itemID {
			t.Fatalf("status alerts after re-eval = %+v, want 1 open alert for %s", stSnap.Alerts, itemID)
		}

		// Reopening the item clears the alert because item.state is no longer resolved-elsewhere.
		appendItemUpdate(ctx, t, st, itemID, "in-queue", "", "reopened", t0.Add(20*time.Minute))
		*now = t0.Add(21 * time.Minute)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		stSnap, err = e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(stSnap.Alerts) != 0 {
			t.Fatalf("alerts after reopen = %+v, want 0", stSnap.Alerts)
		}
	})

	t.Run("resolved without prior run, normal landings, cross-item events, and reverse order stay quiet", func(t *testing.T) {
		ctx, st, e, now := newHarness(t, []watch.Rule{rule})

		// Resolved elsewhere with no prior worker run.
		seedItem(ctx, t, st, "item-no-run", "in-queue", t0)
		appendItemUpdate(ctx, t, st, "item-no-run", "resolved-elsewhere", "commit:1111111", "fixed before dispatch", t0.Add(time.Minute))

		// Worker ran, then item landed or closed normally.
		seedItem(ctx, t, st, "item-landed", "in-queue", t0)
		appendRunAllocate(ctx, t, st, "run-item-landed-a1-n1", "item-landed", "lane-1", "build", t0.Add(2*time.Minute))
		appendItemUpdate(ctx, t, st, "item-landed", "landed", "", "", t0.Add(3*time.Minute))

		seedItem(ctx, t, st, "item-closed", "in-queue", t0)
		appendRunAllocate(ctx, t, st, "run-item-closed-a1-n1", "item-closed", "lane-2", "build", t0.Add(4*time.Minute))
		appendItemUpdate(ctx, t, st, "item-closed", "closed", "", "superseded", t0.Add(5*time.Minute))

		// Run on item-a must not arm item-b.
		seedItem(ctx, t, st, "item-a", "in-queue", t0)
		seedItem(ctx, t, st, "item-b", "in-queue", t0)
		appendRunAllocate(ctx, t, st, "run-item-a-a1-n1", "item-a", "lane-3", "build", t0.Add(6*time.Minute))
		appendItemUpdate(ctx, t, st, "item-b", "resolved-elsewhere", "commit:2222222", "fixed elsewhere", t0.Add(7*time.Minute))

		// Reverse order: resolved-elsewhere before run.allocate.
		seedItem(ctx, t, st, "item-reverse", "in-queue", t0)
		appendItemUpdate(ctx, t, st, "item-reverse", "resolved-elsewhere", "commit:3333333", "already done", t0.Add(8*time.Minute))
		appendRunAllocate(ctx, t, st, "run-item-reverse-a1-n1", "item-reverse", "lane-4", "build", t0.Add(9*time.Minute))

		*now = t0.Add(10 * time.Minute)
		res, err := e.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(res) != 1 || res[0].Skipped != 0 {
			t.Fatalf("eval results = %+v, want Skipped=0", res)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		if len(notices) != 0 {
			t.Fatalf("unexpected alerts: %+v", notices)
		}
	})

	t.Run("all config rules evaluate cleanly together", func(t *testing.T) {
		ctx, st, e, now := newHarness(t, allRules)
		const itemID = "item-request-all-rules"
		seedItem(ctx, t, st, itemID, "in-queue", t0)
		appendRunAllocate(ctx, t, st, "run-item-request-all-rules-a1-n1", itemID, "lane-1", "build", t0.Add(time.Minute))
		appendItemUpdate(ctx, t, st, itemID, "resolved-elsewhere", "commit:3ed0291d91", "already fixed on base", t0.Add(2*time.Minute))

		*now = t0.Add(5 * time.Minute)
		results, err := e.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, res := range results {
			if res.ID == ruleID && res.Diagnostic() != "" {
				t.Fatalf("unexpected diagnostic on %s: %+v", res.ID, res)
			}
			if res.Skipped != 0 {
				t.Fatalf("unexpected skipped events on %s: %+v", res.ID, res)
			}
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		if len(notices) != 1 || notices[0].Bundle.RuleID != ruleID || notices[0].Bundle.Subject != itemID {
			t.Fatalf("want only %s for %s across config/tillr/watch, got %+v", ruleID, itemID, notices)
		}
	})

	root := filepath.Clean(filepath.Join("..", ".."))
	policies, err := filepath.Glob(filepath.Join(root, "config", "tillr", "backend-policy*.json"))
	if err != nil || len(policies) == 0 {
		t.Fatalf("glob policies: %v (len=%d)", err, len(policies))
	}
	slices.Sort(policies)
	lintArgs := []string{"lint", root}
	for _, p := range policies {
		lintArgs = append(lintArgs, "--policy", p)
	}
	var lintOut, lintErr bytes.Buffer
	if code := run(t.Context(), lintArgs, &lintOut, &lintErr); code != 0 || lintErr.Len() != 0 {
		t.Fatalf("tillr watch lint failed: code=%d stdout=%q stderr=%q", code, lintOut.String(), lintErr.String())
	}
}
