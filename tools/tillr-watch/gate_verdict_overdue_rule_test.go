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

func TestGateVerdictOverdueRule(t *testing.T) {
	const (
		ruleID   = "gate-verdict-overdue"
		rulePath = "rules/gate-verdict-overdue.toml"
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
		"fixer-main-1293971a3597",
		"824",
		"gate.verdict.json",
		"duration_ms",
		"99th",
		"3,650 s",
	} {
		if !strings.Contains(comment, required) {
			t.Fatalf("rule comment in %s must state where 3,650 s was measured (missing %q):\n%s", rulePath, required, comment)
		}
	}

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
		!slices.Equal(rule.Reads.Events, []string{drain.GateStartedKind, drain.GateVerdictKind, drain.GateStoppedKind, drain.RefusalKind, "drain.set-aside", "drain.candidate-superseded"}) ||
		rule.When.GroupBy != "object_id" ||
		rule.When.Pattern != watch.DurationWithoutEvent ||
		rule.When.ThresholdDuration != 3650*time.Second ||
		rule.When.Window != 24*time.Hour {
		t.Fatalf("unexpected %s configuration: %+v", ruleID, rule)
	}
	for _, text := range append([]string{rule.Guidance.Summary}, append(slices.Clone(rule.Guidance.Check), rule.Guidance.Do...)...) {
		if strings.Contains(strings.ToLower(text), "expected") {
			t.Fatalf("%s guidance calls a firing alarm expected: %q", ruleID, text)
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

	appendGateVerdict := func(ctx context.Context, t *testing.T, st *store.Store, runID, itemID, laneID, treeSHA, result string, durationMS int64, at time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"verdict_id":  "verdict-" + runID,
			"run_id":      runID,
			"item_id":     itemID,
			"lane_id":     laneID,
			"tree_sha":    treeSHA,
			"result":      result,
			"duration_ms": durationMS,
		})
		if err != nil {
			t.Fatal(err)
		}
		id := runID
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       drain.GateVerdictKind,
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

	newHarness := func(t *testing.T, rules []watch.Rule, start time.Time) (context.Context, *store.Store, *time.Time, func() []watch.Notice) {
		t.Helper()
		ctx := context.Background()
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		now := start
		e := watch.Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
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
				if res.ID == ruleID && (res.Diagnostic() != "" || res.Skipped != 0) {
					t.Fatalf("unexpected %s evaluation result: %+v", ruleID, res)
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

	t0 := time.Date(2026, 11, 23, 9, 13, 0, 0, time.UTC)

	t.Run("gate ending sooner than 3650s stays closed", func(t *testing.T) {
		ctx, st, now, pending := newHarness(t, []watch.Rule{rule}, t0)

		appendGateStarted(ctx, t, st, "run-median-1", "item-median", "lane-1", "aaaa1111", t0)
		*now = t0.Add(56 * time.Second)
		appendGateVerdict(ctx, t, st, "run-median-1", "item-median", "lane-1", "bbbb1111", "pass", 56_000, *now)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("56s gate with verdict opened notices: %+v", notices)
		}

		t1 := t0.Add(2 * time.Minute)
		*now = t1
		appendGateStarted(ctx, t, st, "run-just-under-1", "item-under", "lane-3", "aaaa2222", t1)
		*now = t1.Add(3649 * time.Second)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("gate with no verdict at 3649s (< 3650s) opened notices: %+v", notices)
		}
		appendGateVerdict(ctx, t, st, "run-just-under-1", "item-under", "lane-3", "bbbb2222", "pass", 3_649_000, *now)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("gate finishing at 3649s opened notices: %+v", notices)
		}

		*now = t1.Add(3650 * time.Second)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("already-verdicted gate opened notices at 3650s: %+v", notices)
		}
		*now = t1.Add(88 * time.Minute)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("already-verdicted gate opened notices at 88m: %+v", notices)
		}
	})

	t.Run("gate with no verdict at or past 3650s opens and resolves on verdict", func(t *testing.T) {
		ctx, st, now, pending := newHarness(t, []watch.Rule{rule}, t0)

		const (
			hungRun  = "run-fixer-main-1293971a3597-a1-n1"
			hungItem = "fixer-main-1293971a3597"
			hungLane = "lane-2"
		)
		appendGateStarted(ctx, t, st, hungRun, hungItem, hungLane, "aaaa3333", t0)

		// Another run starting and completing its gate in the meantime must not clear hungRun.
		*now = t0.Add(10 * time.Minute)
		appendGateStarted(ctx, t, st, "run-other-1", "item-other", "lane-4", "aaaa4444", *now)
		*now = t0.Add(11 * time.Minute)
		appendGateVerdict(ctx, t, st, "run-other-1", "item-other", "lane-4", "bbbb4444", "pass", 60_000, *now)

		*now = t0.Add(3649 * time.Second)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("want quiet at 3649s before 3650s threshold, got %+v", notices)
		}

		*now = t0.Add(3650 * time.Second)
		opened := pending()
		if len(opened) != 1 || opened[0].Bundle.RuleID != ruleID || opened[0].Bundle.Subject != hungRun || opened[0].Bundle.Severity != "warning" {
			t.Fatalf("want one warning notice for %s at 3650s, got %+v", hungRun, opened)
		}
		guidance := opened[0].Bundle.Guidance
		allGuidance := strings.Join(append([]string{guidance.Summary}, append(slices.Clone(guidance.Check), guidance.Do...)...), " ")
		if !strings.Contains(guidance.Summary, hungRun) ||
			!strings.Contains(guidance.Summary, hungItem) ||
			!strings.Contains(guidance.Summary, hungLane) ||
			!strings.Contains(guidance.Summary, "60m") ||
			strings.Contains(allGuidance, "{{") ||
			strings.Contains(allGuidance, "unknown") {
			t.Fatalf("guidance must render run, item, lane, and elapsed duration without raw placeholders or unknown: %+v", guidance)
		}

		// At 88 minutes (the 2026-11-23 incident duration), the same episode stays open.
		*now = t0.Add(88 * time.Minute)
		at88m := pending()
		if len(at88m) != 1 || at88m[0].AlertID != opened[0].AlertID || at88m[0].Bundle.Subject != hungRun {
			t.Fatalf("want same open episode %s at 88m, got %+v", opened[0].AlertID, at88m)
		}

		// Recording gate.verdict for hungRun resolves the open episode.
		*now = t0.Add(94 * time.Minute)
		appendGateVerdict(ctx, t, st, hungRun, hungItem, hungLane, "bbbb3333", "fail", 5_640_000, *now)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("gate.verdict for %s should resolve %s, got %+v", hungRun, ruleID, notices)
		}
	})

	t.Run("run whose item is closed, landed, or resolved elsewhere does not fire", func(t *testing.T) {
		ctx, st, now, pending := newHarness(t, []watch.Rule{rule}, t0)

		setItemState := func(itemID, state string) {
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

		setItemState("item-closed", "closed")
		setItemState("item-landed", "landed")
		setItemState("item-resolved", "resolved-elsewhere")
		setItemState("item-open", "in-queue")

		appendGateStarted(ctx, t, st, "run-closed-1", "item-closed", "lane-1", "eeee1111", t0)
		appendGateStarted(ctx, t, st, "run-landed-1", "item-landed", "lane-2", "eeee2222", t0)
		appendGateStarted(ctx, t, st, "run-resolved-1", "item-resolved", "lane-3", "eeee3333", t0)
		appendGateStarted(ctx, t, st, "run-open-1", "item-open", "lane-4", "eeee4444", t0)

		*now = t0.Add(4 * time.Hour)
		opened := pending()
		if len(opened) != 1 || opened[0].Bundle.Subject != "run-open-1" {
			t.Fatalf("want only open item run-open-1 to fire (closed/landed/resolved-elsewhere must not match), got %+v", opened)
		}

		// Closing the open item later resolves its already-open episode.
		setItemState("item-open", "closed")
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("closing item-open should resolve %s for run-open-1, got %+v", ruleID, notices)
		}
	})

	t.Run("set-aside or superseded run does not fire and resolves open episode", func(t *testing.T) {
		ctx, st, now, pending := newHarness(t, []watch.Rule{rule}, t0)

		appendRunEvent := func(kind, runID string, payload any, at time.Time) {
			t.Helper()
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			id := runID
			if err := st.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Append(event.Event{
					Kind:       kind,
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

		// Run 1: gate.started, then operator sets the candidate aside before threshold.
		const (
			setAsideRun  = "run-item-request-a88616fc52-a1-n1"
			setAsideItem = "item-request-a88616fc52"
		)
		appendGateStarted(ctx, t, st, setAsideRun, setAsideItem, "lane-1", "ffff1111", t0)
		*now = t0.Add(5 * time.Minute)
		appendRunEvent("drain.set-aside", setAsideRun, map[string]any{
			"candidate": map[string]any{"item_id": setAsideItem, "run_id": setAsideRun, "lane_id": "lane-1"},
			"ref":       "refs/tillr/runs/" + setAsideRun,
			"why":       "gate program could not start",
		}, *now)

		// Run 2: gate.started, then retry supersedes the held candidate before threshold.
		const (
			supersededRun  = "run-item-superseded-a1-n1"
			supersededItem = "item-superseded"
		)
		appendGateStarted(ctx, t, st, supersededRun, supersededItem, "lane-2", "ffff2222", t0)
		*now = t0.Add(10 * time.Minute)
		appendRunEvent("drain.candidate-superseded", supersededRun, map[string]any{
			"candidate": map[string]any{"item_id": supersededItem, "run_id": supersededRun, "lane_id": "lane-2"},
			"retry_id":  "retry-1",
		}, *now)

		// Run 3: gate.started with no terminal event yet, to ensure other runs' events do not clear it.
		const (
			stillRunningRun  = "run-item-still-running-a1-n1"
			stillRunningItem = "item-still-running"
		)
		appendGateStarted(ctx, t, st, stillRunningRun, stillRunningItem, "lane-3", "ffff3333", t0)

		*now = t0.Add(61 * time.Minute)
		opened := pending()
		if len(opened) != 1 || opened[0].Bundle.Subject != stillRunningRun {
			t.Fatalf("want only %s to fire (set-aside and superseded runs must stay quiet), got %+v", stillRunningRun, opened)
		}

		// Setting aside the open run resolves its already-open episode.
		*now = t0.Add(65 * time.Minute)
		appendRunEvent("drain.set-aside", stillRunningRun, map[string]any{
			"candidate": map[string]any{"item_id": stillRunningItem, "run_id": stillRunningRun, "lane_id": "lane-3"},
			"ref":       "refs/tillr/runs/" + stillRunningRun,
			"why":       "set aside after 65m",
		}, *now)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("drain.set-aside for %s should resolve %s, got %+v", stillRunningRun, ruleID, notices)
		}

		// An open episode on another run also resolves on drain.candidate-superseded.
		const (
			lateSupersededRun  = "run-item-late-superseded-a1-n1"
			lateSupersededItem = "item-late-superseded"
		)
		appendGateStarted(ctx, t, st, lateSupersededRun, lateSupersededItem, "lane-4", "ffff4444", *now)
		*now = (*now).Add(3650 * time.Second)
		openedLate := pending()
		if len(openedLate) != 1 || openedLate[0].Bundle.Subject != lateSupersededRun {
			t.Fatalf("want %s to open at 3650s, got %+v", lateSupersededRun, openedLate)
		}
		*now = (*now).Add(time.Minute)
		appendRunEvent("drain.candidate-superseded", lateSupersededRun, map[string]any{
			"candidate": map[string]any{"item_id": lateSupersededItem, "run_id": lateSupersededRun, "lane_id": "lane-4"},
			"retry_id":  "retry-2",
		}, *now)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("drain.candidate-superseded for %s should resolve %s, got %+v", lateSupersededRun, ruleID, notices)
		}
	})

	t.Run("refused or stopped gate does not fire at 60m and resolves open episode", func(t *testing.T) {
		ctx, st, now, pending := newHarness(t, []watch.Rule{rule}, t0)

		appendRunEvent := func(kind, runID string, payload any, at time.Time) {
			t.Helper()
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			id := runID
			if err := st.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Append(event.Event{
					Kind:       kind,
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

		// Run 1: 2026-11-25 incident shape — gate.started followed by drain.refusal (step=gate) after base replay.
		const (
			refusedRun  = "run-item-request-8721e56757-a5-cfix"
			refusedItem = "item-request-8721e56757"
		)
		appendGateStarted(ctx, t, st, refusedRun, refusedItem, "lane-1", "1111aaaa", t0)
		*now = t0.Add(7*time.Minute + 22*time.Second)
		appendRunEvent(drain.RefusalKind, refusedRun, map[string]any{
			"step":    "gate",
			"item_id": refusedItem,
			"lane_id": "lane-1",
			"detail":  "failing checks: recording a refusal, not a verdict",
		}, *now)

		// Run 2: gate.started followed by gate.stopped (e.g. candidate no longer applies to trunk).
		const (
			conflictStoppedRun  = "run-item-conflict-stopped-a1-n1"
			conflictStoppedItem = "item-conflict-stopped"
		)
		appendGateStarted(ctx, t, st, conflictStoppedRun, conflictStoppedItem, "lane-2", "2222aaaa", t0)
		*now = t0.Add(12 * time.Minute)
		appendRunEvent(drain.GateStoppedKind, conflictStoppedRun, map[string]any{
			"item_id": conflictStoppedItem,
			"run_id":  conflictStoppedRun,
			"lane_id": "lane-2",
			"reason":  "conflict",
		}, *now)

		// Run 3: gate.started followed by gate.stopped at loop stop.
		const (
			loopStoppedRun  = "run-item-loop-stopped-a1-n1"
			loopStoppedItem = "item-loop-stopped"
		)
		appendGateStarted(ctx, t, st, loopStoppedRun, loopStoppedItem, "lane-3", "3333aaaa", t0)
		*now = t0.Add(15 * time.Minute)
		appendRunEvent(drain.GateStoppedKind, loopStoppedRun, map[string]any{
			"item_id": loopStoppedItem,
			"run_id":  loopStoppedRun,
			"lane_id": "lane-3",
			"reason":  "loop-stopped",
		}, *now)

		// Run 4: gate.started with truly no verdict and no ending event must still open at 60m (3650s).
		const (
			overdueRun  = "run-item-truly-overdue-a1-n1"
			overdueItem = "item-truly-overdue"
		)
		appendGateStarted(ctx, t, st, overdueRun, overdueItem, "lane-4", "4444aaaa", t0)

		*now = t0.Add(3650 * time.Second)
		opened := pending()
		if len(opened) != 1 || opened[0].Bundle.Subject != overdueRun {
			t.Fatalf("want only %s to open at 3650s (refused and stopped runs must stay quiet), got %+v", overdueRun, opened)
		}

		// Recording drain.refusal for an already-open episode resolves it.
		*now = t0.Add(65 * time.Minute)
		appendRunEvent(drain.RefusalKind, overdueRun, map[string]any{
			"step":    "gate",
			"item_id": overdueItem,
			"lane_id": "lane-4",
		}, *now)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("drain.refusal for %s should resolve %s, got %+v", overdueRun, ruleID, notices)
		}

		// Recording gate.stopped for an already-open episode resolves it.
		const (
			lateStoppedRun  = "run-item-late-stopped-a1-n1"
			lateStoppedItem = "item-late-stopped"
		)
		appendGateStarted(ctx, t, st, lateStoppedRun, lateStoppedItem, "lane-5", "5555aaaa", *now)
		*now = (*now).Add(3650 * time.Second)
		openedLate := pending()
		if len(openedLate) != 1 || openedLate[0].Bundle.Subject != lateStoppedRun {
			t.Fatalf("want %s to open at 3650s, got %+v", lateStoppedRun, openedLate)
		}
		*now = (*now).Add(time.Minute)
		appendRunEvent(drain.GateStoppedKind, lateStoppedRun, map[string]any{
			"item_id": lateStoppedItem,
			"run_id":  lateStoppedRun,
			"lane_id": "lane-5",
			"reason":  "loop-stopped",
		}, *now)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("gate.stopped for %s should resolve %s, got %+v", lateStoppedRun, ruleID, notices)
		}
	})

	t.Run("across all config rules on 88m gate with no verdict", func(t *testing.T) {
		ctx := context.Background()
		root := t.TempDir()
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "all-rules.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })

		now := t0
		e := watch.Evaluator{Store: s, Root: root, Rules: allRules, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}

		appendGateStarted(ctx, t, s, "run-all-fast", "item-fast", "lane-1", "cccc1111", t0)
		now = t0.Add(56 * time.Second)
		appendGateVerdict(ctx, t, s, "run-all-fast", "item-fast", "lane-1", "dddd1111", "pass", 56_000, now)

		appendGateStarted(ctx, t, s, "run-all-hung", "fixer-main-1293971a3597", "lane-2", "cccc2222", t0)
		now = t0.Add(88 * time.Minute)

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
		if len(notices) != 1 || notices[0].Bundle.RuleID != ruleID || notices[0].Bundle.Subject != "run-all-hung" {
			t.Fatalf("want only %s for run-all-hung across config/tillr/watch, got %+v", ruleID, notices)
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
