package main

import (
	"bytes"
	"context"
	"encoding/json"
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

func TestDrainBaseReplayRules(t *testing.T) {
	const (
		overlapID = "drain-base-replay-overlap"
		slowID    = "drain-base-replay-slow"
		rulePath  = "rules/drain-base-replay.toml"
	)

	rawRule, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatalf("read %s: %v", rulePath, err)
	}
	var commentLines []string
	for _, line := range strings.Split(string(rawRule), "\n") {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "#") {
			commentLines = append(commentLines, trimmed)
		}
	}
	comment := strings.Join(commentLines, "\n")
	for _, required := range []string{
		"2026-11-23",
		"01:48 ET",
		"2026-11-24",
		"124",
		"92",
		"45.5m",
		"50m",
	} {
		if !strings.Contains(comment, required) {
			t.Fatalf("rule comment in %s must state the measured replay baseline (missing %q):\n%s", rulePath, required, comment)
		}
	}

	allRules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	overlapIdx := slices.IndexFunc(allRules, func(r watch.Rule) bool { return r.ID == overlapID })
	if overlapIdx < 0 {
		t.Fatalf("missing %s rule in config/tillr/watch", overlapID)
	}
	slowIdx := slices.IndexFunc(allRules, func(r watch.Rule) bool { return r.ID == slowID })
	if slowIdx < 0 {
		t.Fatalf("missing %s rule in config/tillr/watch", slowID)
	}
	overlapRule := allRules[overlapIdx]
	slowRule := allRules[slowIdx]

	wantEvents := []string{drain.BaseReplayStartedKind, drain.BaseReplayEndedKind}
	if overlapRule.Scope != "core" || overlapRule.Severity != "warning" || !overlapRule.Enabled ||
		overlapRule.Delivery.Receiver != "foreground" ||
		!slices.Equal(overlapRule.Delivery.AlsoNotify, []string{"subscribers"}) ||
		!slices.Equal(overlapRule.Reads.Events, wantEvents) ||
		overlapRule.When.GroupBy != "global" ||
		overlapRule.When.Pattern != watch.PredicateMatch ||
		overlapRule.When.Window != 0 {
		t.Fatalf("unexpected %s configuration: %+v", overlapID, overlapRule)
	}
	if slowRule.Scope != "core" || slowRule.Severity != "warning" || !slowRule.Enabled ||
		slowRule.Delivery.Receiver != "foreground" ||
		!slices.Equal(slowRule.Delivery.AlsoNotify, []string{"subscribers"}) ||
		!slices.Equal(slowRule.Reads.Events, wantEvents) ||
		!slices.Equal(slowRule.When.GroupByList, []string{"run_id", "base_sha"}) ||
		slowRule.When.Pattern != watch.DurationWithoutEvent ||
		slowRule.When.ThresholdDuration != 50*time.Minute ||
		slowRule.When.Window != 24*time.Hour {
		t.Fatalf("unexpected %s configuration: %+v", slowID, slowRule)
	}

	for _, rule := range []watch.Rule{overlapRule, slowRule} {
		for _, text := range append([]string{rule.Guidance.Summary}, append(slices.Clone(rule.Guidance.Check), rule.Guidance.Do...)...) {
			if strings.Contains(strings.ToLower(text), "expected") {
				t.Fatalf("%s guidance calls a firing alarm expected: %q", rule.ID, text)
			}
		}
	}

	appendStarted := func(ctx context.Context, t *testing.T, st *store.Store, runID, itemID, baseSHA string, checks []string, activeReplays int, at time.Time) {
		t.Helper()
		body, err := json.Marshal(drain.BaseReplayStartedPayload{
			BaseSHA:       baseSHA,
			ItemID:        itemID,
			RunID:         runID,
			Checks:        checks,
			ActiveReplays: activeReplays,
		})
		if err != nil {
			t.Fatal(err)
		}
		objectType, objectID := "run", runID
		if objectID == "" {
			objectType, objectID = "commit", baseSHA
		}
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       drain.BaseReplayStartedKind,
				ObjectType: objectType,
				ObjectID:   &objectID,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	appendEnded := func(ctx context.Context, t *testing.T, st *store.Store, runID, itemID, baseSHA string, checks []string, outcome string, elapsedMS int64, activeReplays int, at time.Time) {
		t.Helper()
		body, err := json.Marshal(drain.BaseReplayEndedPayload{
			BaseSHA:       baseSHA,
			ItemID:        itemID,
			RunID:         runID,
			Checks:        checks,
			Outcome:       outcome,
			ElapsedMS:     elapsedMS,
			ActiveReplays: activeReplays,
		})
		if err != nil {
			t.Fatal(err)
		}
		objectType, objectID := "run", runID
		if objectID == "" {
			objectType, objectID = "commit", baseSHA
		}
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       drain.BaseReplayEndedKind,
				ObjectType: objectType,
				ObjectID:   &objectID,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	newHarness := func(t *testing.T, rules []watch.Rule, start time.Time) (context.Context, *store.Store, *time.Time, func() []watch.Notice) {
		t.Helper()
		ctx := context.Background()
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		now := start
		e := watch.Evaluator{Store: s, Root: t.TempDir(), Rules: rules, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		pending := func() []watch.Notice {
			t.Helper()
			results, err := e.EvaluateRules(ctx)
			if err != nil {
				t.Fatal(err)
			}
			for _, res := range results {
				if (res.ID == overlapID || res.ID == slowID) && res.Diagnostic() != "" {
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
			return notices
		}
		return ctx, s, &now, pending
	}

	t0 := time.Date(2026, 11, 24, 12, 0, 0, 0, time.UTC)
	bothRules := []watch.Rule{overlapRule, slowRule}

	t.Run("solo replay under 50m stays quiet", func(t *testing.T) {
		ctx, st, now, pending := newHarness(t, bothRules, t0)

		appendStarted(ctx, t, st, "run-solo", "item-1", "aaaa1111", []string{"unit"}, 1, *now)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("solo start should not alert, got %+v", notices)
		}

		*now = t0.Add(45*time.Minute + 30*time.Second)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("solo replay at 45.5m should not alert, got %+v", notices)
		}

		appendEnded(ctx, t, st, "run-solo", "item-1", "aaaa1111", []string{"unit"}, "green", int64((45*time.Minute+30*time.Second)/time.Millisecond), 0, *now)
		*now = t0.Add(60 * time.Minute)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("finished solo replay should stay quiet, got %+v", notices)
		}
	})

	t.Run("overlap opens at 2, updates through 4, stays open at 2, resolves at 1, and reopens on next overlap", func(t *testing.T) {
		ctx, st, now, pending := newHarness(t, []watch.Rule{overlapRule}, t0)

		appendStarted(ctx, t, st, "run-1", "item-1", "aaaa1111", []string{"unit"}, 1, *now)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("first replay should not alert, got %+v", notices)
		}

		*now = now.Add(time.Minute)
		appendStarted(ctx, t, st, "run-2", "item-2", "aaaa1111", []string{"unit"}, 2, *now)
		opened := pending()
		if len(opened) != 1 || opened[0].Bundle.RuleID != overlapID {
			t.Fatalf("want 1 %s notice on active_replays=2, got %+v", overlapID, opened)
		}
		firstEpisode := opened[0].AlertID
		if !strings.Contains(opened[0].Bundle.Guidance.Summary, "Multiple base replays (2)") ||
			!strings.Contains(opened[0].Bundle.Guidance.Summary, "run-2") {
			t.Fatalf("unexpected overlap summary: %q", opened[0].Bundle.Guidance.Summary)
		}

		*now = now.Add(time.Minute)
		appendStarted(ctx, t, st, "run-3", "item-3", "bbbb2222", []string{"gate"}, 3, *now)
		*now = now.Add(time.Minute)
		appendStarted(ctx, t, st, "run-4", "item-4", "cccc3333", []string{"gate"}, 4, *now)
		updated := pending()
		if len(updated) != 1 || updated[0].AlertID != firstEpisode {
			t.Fatalf("want same episode %s updated in place through active_replays=4, got %+v", firstEpisode, updated)
		}

		// Ending down to 3 and then 2 must keep the overlap episode open.
		*now = now.Add(time.Minute)
		appendEnded(ctx, t, st, "run-4", "item-4", "cccc3333", []string{"gate"}, "green", 60_000, 3, *now)
		if notices := pending(); len(notices) != 1 || notices[0].AlertID != firstEpisode {
			t.Fatalf("want episode %s still open at active_replays=3, got %+v", firstEpisode, notices)
		}
		*now = now.Add(time.Minute)
		appendEnded(ctx, t, st, "run-3", "item-3", "bbbb2222", []string{"gate"}, "green", 180_000, 2, *now)
		if notices := pending(); len(notices) != 1 || notices[0].AlertID != firstEpisode {
			t.Fatalf("want episode %s still open at active_replays=2, got %+v", firstEpisode, notices)
		}

		// Ending down to 1 resolves the overlap episode.
		*now = now.Add(time.Minute)
		appendEnded(ctx, t, st, "run-2", "item-2", "aaaa1111", []string{"unit"}, "green", 300_000, 1, *now)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("want overlap resolved at active_replays=1, got %+v", notices)
		}

		// Starting R5 while R1 is still running opens a fresh overlap episode.
		*now = now.Add(time.Minute)
		appendStarted(ctx, t, st, "run-5", "item-5", "dddd4444", []string{"unit"}, 2, *now)
		reopened := pending()
		if len(reopened) != 1 || reopened[0].AlertID == firstEpisode {
			t.Fatalf("want new episode on second overlap (prev %s), got %+v", firstEpisode, reopened)
		}

		// Self-heal if a crashed loop's next solo start reports active_replays == 1.
		*now = now.Add(time.Minute)
		appendStarted(ctx, t, st, "run-after-crash", "item-6", "eeee5555", []string{"unit"}, 1, *now)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("want crash-recovery solo start (active_replays=1) to resolve overlap, got %+v", notices)
		}
	})

	t.Run("slow stays quiet at 49m, fires at 50m per (run_id, base_sha) without sibling masking, and resolves on end", func(t *testing.T) {
		ctx, st, now, pending := newHarness(t, []watch.Rule{slowRule}, t0)

		appendStarted(ctx, t, st, "run-slow", "item-slow", "aaaa1111", []string{"go-affected"}, 1, t0)
		// Fast sibling replay on the same base_sha starts and finishes at 2m.
		*now = t0.Add(time.Minute)
		appendStarted(ctx, t, st, "run-fast", "item-fast", "aaaa1111", []string{"unit"}, 2, *now)
		*now = t0.Add(2 * time.Minute)
		appendEnded(ctx, t, st, "run-fast", "item-fast", "aaaa1111", []string{"unit"}, "green", 60_000, 1, *now)

		// Commit-level replay with empty run_id also tracks cleanly (skipped == 0).
		appendStarted(ctx, t, st, "", "", "bbbb2222", []string{"heavy"}, 2, t0.Add(5*time.Minute))

		*now = t0.Add(49 * time.Minute)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("49m replay should not alert, got %+v", notices)
		}

		*now = t0.Add(50 * time.Minute)
		opened := pending()
		if len(opened) != 1 || opened[0].Bundle.RuleID != slowID || opened[0].Bundle.Subject != "run-slow/aaaa1111" {
			t.Fatalf("want 1 %s notice for run-slow/aaaa1111 at 50m, got %+v", slowID, opened)
		}

		// Commit-level replay crosses 50m at t0+55m while run-slow is still open.
		*now = t0.Add(55 * time.Minute)
		both := pending()
		if len(both) != 2 {
			t.Fatalf("want 2 slow notices at t0+55m, got %+v", both)
		}

		// Ending run-slow resolves only run-slow's episode.
		appendEnded(ctx, t, st, "run-slow", "item-slow", "aaaa1111", []string{"go-affected"}, "red", int64((55*time.Minute)/time.Millisecond), 1, *now)
		afterOneEnd := pending()
		if len(afterOneEnd) != 1 || afterOneEnd[0].Bundle.Subject != "/bbbb2222" {
			t.Fatalf("want only commit-level /bbbb2222 open after run-slow ends, got %+v", afterOneEnd)
		}

		appendEnded(ctx, t, st, "", "", "bbbb2222", []string{"heavy"}, "green", int64((50*time.Minute)/time.Millisecond), 0, *now)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("want all slow episodes resolved after matching ends, got %+v", notices)
		}
	})

	t.Run("across all config rules on 87m overlapping replay", func(t *testing.T) {
		ctx, st, now, pending := newHarness(t, allRules, t0)

		appendStarted(ctx, t, st, "run-r1", "item-r1", "aaaa1111", []string{"go-affected"}, 1, t0)
		*now = t0.Add(15 * time.Minute)
		appendStarted(ctx, t, st, "run-r2", "item-r2", "aaaa1111", []string{"go-affected"}, 2, *now)
		*now = t0.Add(87 * time.Minute)

		notices := pending()
		var gotIDs []string
		for _, n := range notices {
			gotIDs = append(gotIDs, n.Bundle.RuleID)
		}
		slices.Sort(gotIDs)
		wantIDs := []string{overlapID, slowID, slowID}
		if !slices.Equal(gotIDs, wantIDs) {
			t.Fatalf("want rules %v across config/tillr/watch, got %v (%+v)", wantIDs, gotIDs, notices)
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
