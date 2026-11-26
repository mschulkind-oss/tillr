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

	_ "github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainLaneKeptRule(t *testing.T) {
	const (
		ruleID   = "drain-lane-kept"
		rulePath = "rules/drain-lane-kept.toml"
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
	for _, required := range []string{"sqlite3", "2026-11-24", "691.3", "1136.0", "1425.3", "15m"} {
		if !strings.Contains(comment, required) {
			t.Fatalf("rule comment in %s must state command, date, and figures (missing %q):\n%s", rulePath, required, comment)
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
		!slices.Equal(rule.Reads.Events, []string{"drain.lane-kept", "drain.lane-released", "run.release"}) ||
		rule.When.GroupBy != "lane_id" ||
		rule.When.Pattern != watch.DurationWithoutEvent ||
		rule.When.ThresholdDuration != 15*time.Minute ||
		rule.When.Window != 24*time.Hour {
		t.Fatalf("unexpected %s configuration: %+v", ruleID, rule)
	}
	for _, text := range append([]string{rule.Guidance.Summary}, append(slices.Clone(rule.Guidance.Check), rule.Guidance.Do...)...) {
		if strings.Contains(strings.ToLower(text), "expected") {
			t.Fatalf("%s guidance calls a firing alarm expected: %q", ruleID, text)
		}
	}

	var lintOut, lintErr bytes.Buffer
	if code := run(t.Context(), []string{"lint", "../.."}, &lintOut, &lintErr); code != 0 {
		t.Fatalf("tillr watch lint failed (%d): stdout=%q stderr=%q", code, lintOut.String(), lintErr.String())
	}

	newHarness := func(t *testing.T, rules []watch.Rule, start time.Time) (context.Context, *store.Store, *time.Time, func() []watch.Notice) {
		t.Helper()
		ctx := context.Background()
		storePath := filepath.Join(t.TempDir(), "state.db")
		s, err := store.Open(ctx, storePath, true)
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

	appendLaneKept := func(ctx context.Context, s *store.Store, at time.Time, lane, itemID, runID, reason string) {
		t.Helper()
		body, err := json.Marshal(map[string]string{
			"lane_id": lane,
			"item_id": itemID,
			"run_id":  runID,
			"reason":  reason,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       "drain.lane-kept",
				ObjectType: "lane",
				ObjectID:   &lane,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	appendDrainLaneReleased := func(ctx context.Context, s *store.Store, at time.Time, runID, lane, cause string) {
		t.Helper()
		body, err := json.Marshal(map[string]string{
			"lane_id": lane,
			"cause":   cause,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       "drain.lane-released",
				ObjectType: "run",
				ObjectID:   &runID,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	appendRunRelease := func(ctx context.Context, s *store.Store, at time.Time, lane, runID, result string) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"lane_id":     lane,
			"run_id":      runID,
			"released_at": at.UTC().Format(time.RFC3339),
			"result":      result,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       "run.release",
				ObjectType: "lane",
				ObjectID:   &lane,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	t0 := time.Date(2026, 11, 23, 9, 34, 10, 0, time.UTC)

	t.Run("released within threshold stays quiet", func(t *testing.T) {
		ctx, s, now, pending := newHarness(t, []watch.Rule{rule}, t0)

		appendLaneKept(ctx, s, *now, "lane-4", "item-a", "run-item-a-a1-n1", "lane has an unfinished run run-other-a1")
		*now = t0.Add(11*time.Minute + 31*time.Second)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("keep for 11m31s (< 15m) opened notices: %+v", notices)
		}
		appendDrainLaneReleased(ctx, s, *now, "run-item-a-a1-n1", "lane-4", "examined")

		*now = t0.Add(15 * time.Minute)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("lane released via drain.lane-released opened notices at 15m: %+v", notices)
		}

		// Also verify run.release with result=="released" clears within the duration.
		t1 := t0.Add(20 * time.Minute)
		*now = t1
		appendLaneKept(ctx, s, *now, "lane-2", "item-b", "run-item-b-a1", "lane has an unfinished resume run-old")
		*now = t1.Add(2*time.Minute + 41*time.Second)
		appendRunRelease(ctx, s, *now, "lane-2", "run-item-b-a1", "released")

		*now = t1.Add(30 * time.Minute)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("lane released via run.release opened notices at 30m: %+v", notices)
		}
	})

	t.Run("long keep fires past threshold, isolates by lane, and resolves on release", func(t *testing.T) {
		ctx, s, now, pending := newHarness(t, []watch.Rule{rule}, t0)

		const (
			itemID = "item-sample-keep"
			runID  = "run-item-sample-keep-a1-n2"
			reason = "lane may have a live run in another process domain"
		)
		appendLaneKept(ctx, s, *now, "lane-1", itemID, runID, reason)

		// A refused run.release or a release on another lane must not clear lane-1's keep.
		*now = t0.Add(5 * time.Minute)
		appendRunRelease(ctx, s, *now, "lane-1", runID, "refused")
		appendDrainLaneReleased(ctx, s, *now, "run-other-a1", "lane-2", "examined")

		*now = t0.Add(15*time.Minute - time.Second)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("want quiet before 15m threshold, got %+v", notices)
		}

		*now = t0.Add(15 * time.Minute)
		first := pending()
		if len(first) != 1 || first[0].Bundle.RuleID != ruleID || first[0].Bundle.Subject != "lane-1" {
			t.Fatalf("want one %s notice for lane-1 at 15m, got %+v", ruleID, first)
		}
		for _, want := range []string{"lane-1", "15m", itemID, runID, reason} {
			if !strings.Contains(first[0].Bundle.Guidance.Summary, want) {
				t.Fatalf("summary %q missing %q", first[0].Bundle.Guidance.Summary, want)
			}
		}

		// Still open on the same episode at 20m while unreleased.
		*now = t0.Add(20 * time.Minute)
		later := pending()
		if len(later) != 1 || later[0].AlertID != first[0].AlertID {
			t.Fatalf("want same open episode at 20m, got %+v", later)
		}

		// Releasing lane-1 resolves the open episode.
		*now = t0.Add(23*time.Minute + 45*time.Second)
		appendDrainLaneReleased(ctx, s, *now, runID, "lane-1", "examined")
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("releasing lane-1 should resolve %s, got %+v", ruleID, notices)
		}
	})

	t.Run("across all config rules on 15m un-released lane keep", func(t *testing.T) {
		ctx, s, now, pending := newHarness(t, allRules, t0)
		appendLaneKept(ctx, s, *now, "lane-1", "item-sample", "run-item-sample-a1-n2", "lane may have a live run in another process domain")
		*now = t0.Add(15 * time.Minute)
		notices := pending()
		if len(notices) != 1 || notices[0].Bundle.RuleID != ruleID || notices[0].Bundle.Subject != "lane-1" {
			t.Fatalf("want only %s for lane-1 across config/tillr/watch, got %+v", ruleID, notices)
		}
	})
}
