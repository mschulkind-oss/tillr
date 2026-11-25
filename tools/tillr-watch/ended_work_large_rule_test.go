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
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainEndedWorkLargeRule(t *testing.T) {
	const (
		ruleID    = "drain-ended-work-large"
		limitByte = 1048576
	)

	allRules, err := watch.LoadRules("../../config/tillr/watch")
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
		!slices.Equal(rule.Reads.Events, []string{"drain.ended-work-saved"}) ||
		len(rule.Reads.Projections) != 0 ||
		rule.When.Pattern != watch.PredicateMatch ||
		rule.When.GroupBy != "ref" ||
		rule.When.Match == nil ||
		rule.When.Window != 24*time.Hour {
		t.Fatalf("unexpected %s configuration: %+v", ruleID, rule)
	}

	var lintOut, lintErr bytes.Buffer
	if code := run(t.Context(), []string{"lint", "../.."}, &lintOut, &lintErr); code != 0 {
		t.Fatalf("tillr watch lint failed (%d): stdout=%q stderr=%q", code, lintOut.String(), lintErr.String())
	}

	newHarness := func(t *testing.T, rules []watch.Rule, now time.Time) (context.Context, *store.Store, func(time.Time, drain.EndedWorkSaved), func() []watch.Notice) {
		t.Helper()
		ctx := context.Background()
		s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { s.Close() })
		e := watch.Evaluator{Store: s, Root: t.TempDir(), Rules: rules, Clock: func() time.Time { return now }}
		if _, err := e.EvaluateRules(ctx); err != nil {
			t.Fatal(err)
		}
		appendSaved := func(at time.Time, payload drain.EndedWorkSaved) {
			t.Helper()
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			runID := payload.RunID
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Append(event.Event{
					Kind:       "drain.ended-work-saved",
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
		return ctx, s, appendSaved, pending
	}

	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	ctx, s, appendSaved, pending := newHarness(t, allRules, now)

	// Record the ended item as closed in items so we also verify the alert is not auto-closed
	// as a terminal item-subject alert.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, ?, 'closed', ?)`,
			"item-closed-large", "Closed item with large save", now.UnixMilli(),
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// Small scratch save, exact boundary save, and out-of-window large save must not alert.
	appendSaved(now, drain.EndedWorkSaved{
		RunID:      "run-small-a1-n1",
		ItemID:     "item-small",
		ItemState:  "closed",
		LaneID:     "lane-1",
		Ref:        "refs/tillr/ended/run-small-a1-n1",
		Repository: "/repo",
		Reason:     "ended-item-lane",
		KeptFiles:  1,
		KeptBytes:  2,
	})
	appendSaved(now, drain.EndedWorkSaved{
		RunID:      "run-boundary-a1-n1",
		ItemID:     "item-boundary",
		ItemState:  "closed",
		LaneID:     "lane-2",
		Ref:        "refs/tillr/ended/run-boundary-a1-n1",
		Repository: "/repo",
		Reason:     "ended-item-lane",
		KeptFiles:  10,
		KeptBytes:  limitByte,
	})
	appendSaved(now.Add(-25*time.Hour), drain.EndedWorkSaved{
		RunID:      "run-old-large-a1-n1",
		ItemID:     "item-old",
		ItemState:  "closed",
		LaneID:     "lane-3",
		Ref:        "refs/tillr/ended/run-old-large-a1-n1",
		Repository: "/repo",
		Reason:     "ended-item-lane",
		KeptFiles:  7190,
		KeptBytes:  limitByte + 1,
	})
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("small, boundary, and out-of-window saves must not alert: %+v", notices)
	}

	// Oversized save over the 1 MiB limit must alert across config/tillr/watch.
	const largeRef = "refs/tillr/ended/run-large-a1-n1"
	appendSaved(now, drain.EndedWorkSaved{
		RunID:      "run-large-a1-n1",
		ItemID:     "item-closed-large",
		ItemState:  "closed",
		LaneID:     "lane-4",
		Ref:        largeRef,
		Repository: "/repo",
		Reason:     "ended-item-lane",
		KeptFiles:  7190,
		KeptBytes:  limitByte + 1,
	})
	notices := pending()
	if len(notices) != 1 || notices[0].Bundle.RuleID != ruleID || notices[0].Bundle.Subject != largeRef {
		t.Fatalf("want only %s for %s across config/tillr/watch, got %+v", ruleID, largeRef, notices)
	}
	summary := notices[0].Bundle.Guidance.Summary
	for _, want := range []string{largeRef, "7190", "1048577", "item-closed-large", "lane-4"} {
		if !strings.Contains(summary, want) {
			t.Fatalf("summary missing %q: %q", want, summary)
		}
	}
}
