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

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/fixer"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestProvedFixerGatedRule(t *testing.T) {
	const ruleID = "drain-proved-fixer-gated"

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
		!slices.Equal(rule.Delivery.AlsoNotify, []string{"subscribers"}) ||
		!slices.Equal(rule.Reads.Events, []string{fixer.FixerProvedKind, drain.FixerAttachedKind, drain.GateStartedKind}) ||
		!slices.Equal(rule.When.GroupByAny, []string{"item_id", "fixer_item", "object_id"}) ||
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

	appendFixerProved := func(ctx context.Context, t *testing.T, st *store.Store, fixerID, greenSHA string, checks []string, at time.Time) {
		t.Helper()
		body, err := json.Marshal(fixer.FixerProof{
			FixerItem: fixerID,
			Green:     greenSHA,
			ProvedAt:  at.UTC().Format("2006-01-02T15:04:05.000Z"),
			Checks:    checks,
		})
		if err != nil {
			t.Fatal(err)
		}
		id := fixerID
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       fixer.FixerProvedKind,
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

	appendFixerAttached := func(ctx context.Context, t *testing.T, st *store.Store, fixerID string, at time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"fixer_item": fixerID,
			"red_sha":    "deadbeef",
		})
		if err != nil {
			t.Fatal(err)
		}
		id := fixerID
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       drain.FixerAttachedKind,
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

	appendGateStarted := func(ctx context.Context, t *testing.T, st *store.Store, runID, itemID, laneID, baseSHA string, at time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"item_id":  itemID,
			"run_id":   runID,
			"lane_id":  laneID,
			"base_sha": baseSHA,
		})
		if err != nil {
			t.Fatal(err)
		}
		id := runID
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       drain.GateStartedKind,
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

	seedItems := func(ctx context.Context, t *testing.T, st *store.Store, itemID, state string, at time.Time) {
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

	t0 := time.Date(2026, 11, 23, 9, 47, 0, 0, time.UTC)

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

	t.Run("proved before gate on resolved-elsewhere fixer opens and stays open", func(t *testing.T) {
		ctx, st, e, now := newHarness(t, []watch.Rule{rule})
		const (
			fixerID = "fixer-main-1293971a3597"
			runID   = "run-fixer-main-1293971a3597-a1-n1"
			laneID  = "lane-2"
		)
		seedItems(ctx, t, st, fixerID, "resolved-elsewhere", t0)

		appendFixerProved(ctx, t, st, fixerID, "aaaa1111", []string{"go-tool/unit"}, t0.Add(time.Second))
		appendGateStarted(ctx, t, st, runID, fixerID, laneID, "bbbb2222", t0.Add(2*time.Second))

		*now = t0.Add(5 * time.Second)
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
		if len(notices) != 1 || notices[0].Bundle.Subject != fixerID {
			t.Fatalf("pending notices = %+v, want 1 for %s", notices, fixerID)
		}
		summary := notices[0].Bundle.Guidance.Summary
		for _, want := range []string{fixerID, runID, laneID} {
			if !strings.Contains(summary, want) {
				t.Fatalf("summary %q missing %q", summary, want)
			}
		}

		// Subsequent evaluations keep the single episode open even though items.state == "resolved-elsewhere".
		*now = t0.Add(30 * time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		stSnap, err := e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(stSnap.Alerts) != 1 || stSnap.Alerts[0].State != "open" || stSnap.Alerts[0].Subject != fixerID {
			t.Fatalf("status alerts after re-eval = %+v, want 1 open alert for %s", stSnap.Alerts, fixerID)
		}
		firstAlertID := stSnap.Alerts[0].ID

		// Repeat gate.started and a second [drain.fixer-proved, gate.started] inside window deduplicate to 1 episode.
		appendGateStarted(ctx, t, st, runID, fixerID, laneID, "bbbb2222", t0.Add(40*time.Second))
		appendFixerProved(ctx, t, st, fixerID, "cccc3333", []string{"go-tool/unit"}, t0.Add(45*time.Second))
		appendGateStarted(ctx, t, st, "run-fixer-main-1293971a3597-a2-n1", fixerID, laneID, "dddd4444", t0.Add(50*time.Second))
		*now = t0.Add(60 * time.Second)
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		stSnap, err = e.ReadStatus(ctx, func(string) (bool, error) { return false, nil })
		if err != nil {
			t.Fatal(err)
		}
		if len(stSnap.Alerts) != 1 || stSnap.Alerts[0].ID != firstAlertID || stSnap.Alerts[0].State != "open" {
			t.Fatalf("repeat alerts = %+v, want same single open alert %s", stSnap.Alerts, firstAlertID)
		}
	})

	t.Run("gate before proof, re-attached fixer, and ordinary items stay quiet", func(t *testing.T) {
		ctx, st, e, now := newHarness(t, []watch.Rule{rule})

		// [gate.started, drain.fixer-proved]
		appendGateStarted(ctx, t, st, "run-before-1", "fixer-main-before", "lane-1", "1111aaaa", t0.Add(time.Second))
		appendFixerProved(ctx, t, st, "fixer-main-before", "2222bbbb", []string{"go-tool/unit"}, t0.Add(2*time.Second))

		// [drain.fixer-proved, drain.fixer-attached, gate.started]
		appendFixerProved(ctx, t, st, "fixer-main-reattached", "3333cccc", []string{"go-tool/unit"}, t0.Add(3*time.Second))
		appendFixerAttached(ctx, t, st, "fixer-main-reattached", t0.Add(4*time.Second))
		appendGateStarted(ctx, t, st, "run-reattached-1", "fixer-main-reattached", "lane-2", "4444dddd", t0.Add(5*time.Second))

		// Ordinary non-fixer item gate.started
		appendGateStarted(ctx, t, st, "run-ordinary-1", "item-request-ordinary", "lane-3", "5555eeee", t0.Add(6*time.Second))

		*now = t0.Add(10 * time.Second)
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
		const fixerID = "fixer-main-e7ff466d8d4a"
		seedItems(ctx, t, st, fixerID, "resolved-elsewhere", t0)
		appendFixerProved(ctx, t, st, fixerID, "aaaa1111", []string{"go-tool/unit"}, t0.Add(time.Second))
		appendGateStarted(ctx, t, st, "run-all-1", fixerID, "lane-1", "bbbb2222", t0.Add(2*time.Second))

		*now = t0.Add(5 * time.Second)
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
		if len(notices) != 1 || notices[0].Bundle.RuleID != ruleID || notices[0].Bundle.Subject != fixerID {
			t.Fatalf("want only %s for %s across config/tillr/watch, got %+v", ruleID, fixerID, notices)
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
