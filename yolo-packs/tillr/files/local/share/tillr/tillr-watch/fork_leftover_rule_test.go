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

func TestDrainForkLeftoverRule(t *testing.T) {
	const ruleID = "drain-fork-leftover"

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
		!slices.Equal(rule.Reads.Events, []string{drain.ForkLeftoverKind}) ||
		len(rule.Reads.Projections) != 0 ||
		rule.When.Pattern != watch.PredicateMatch ||
		rule.When.GroupBy != "checkout" ||
		rule.When.Window != 24*time.Hour {
		t.Fatalf("unexpected %s configuration: %+v", ruleID, rule)
	}

	var lintOut, lintErr bytes.Buffer
	if code := run(t.Context(), []string{"lint", "../.."}, &lintOut, &lintErr); code != 0 {
		t.Fatalf("tillr watch lint failed (%d): stdout=%q stderr=%q", code, lintOut.String(), lintErr.String())
	}

	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	e := watch.Evaluator{Store: s, Root: t.TempDir(), Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	appendLeftover := func(at time.Time, payload drain.ForkLeftover) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		runID := payload.RunID
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       drain.ForkLeftoverKind,
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

	appendLeftover(now.Add(-2*time.Minute), drain.ForkLeftover{
		Checkout:         "/workspace/.forks/lanes/lane-8/pi",
		LaneID:           "lane-8",
		RunID:            "run-item-1-a1",
		ItemID:           "item-1",
		Ref:              "refs/heads/patch/pi",
		Holds:            "uncommitted work (2 files: a.txt, b.txt)",
		UncommittedFiles: []string{"a.txt", "b.txt"},
		UncommittedCount: 2,
	})
	appendLeftover(now.Add(-1*time.Minute), drain.ForkLeftover{
		Checkout:       "/workspace/.forks/lanes/lane-9/pi",
		LaneID:         "lane-9",
		RunID:          "run-item-2-a1",
		ItemID:         "item-2",
		Ref:            "refs/heads/patch/pi",
		Holds:          "unretained commits at 9fd1e7f0 (delivery tip 12345678)",
		UnretainedHead: "9fd1e7f011111111111111111111111111111111",
		DeliveryTip:    "1234567800000000000000000000000000000000",
	})

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
	if len(notices) != 2 {
		t.Fatalf("got %d notices, want 2: %+v", len(notices), notices)
	}

	bySubject := map[string]watch.Notice{}
	for _, n := range notices {
		if n.Bundle.RuleID != ruleID {
			t.Fatalf("unexpected rule %q in notice: %+v", n.Bundle.RuleID, n)
		}
		bySubject[n.Bundle.Subject] = n
	}
	uncommitted, ok := bySubject["/workspace/.forks/lanes/lane-8/pi"]
	if !ok {
		t.Fatalf("missing notice for lane-8 checkout: %+v", notices)
	}
	if !strings.Contains(uncommitted.Bundle.Guidance.Summary, "uncommitted work (2 files: a.txt, b.txt)") ||
		!strings.Contains(uncommitted.Bundle.Guidance.Summary, "/workspace/.forks/lanes/lane-8/pi") {
		t.Fatalf("uncommitted summary = %q, want checkout and holds", uncommitted.Bundle.Guidance.Summary)
	}
	unretained, ok := bySubject["/workspace/.forks/lanes/lane-9/pi"]
	if !ok {
		t.Fatalf("missing notice for lane-9 checkout: %+v", notices)
	}
	if !strings.Contains(unretained.Bundle.Guidance.Summary, "unretained commits at 9fd1e7f0") ||
		!strings.Contains(unretained.Bundle.Guidance.Summary, "/workspace/.forks/lanes/lane-9/pi") {
		t.Fatalf("unretained summary = %q, want checkout and holds", unretained.Bundle.Guidance.Summary)
	}
}
