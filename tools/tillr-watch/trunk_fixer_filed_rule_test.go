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

func TestDrainTrunkFixerFiledRule(t *testing.T) {
	const ruleID = "drain-trunk-fixer-filed"
	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule", ruleID)
	}
	rule := rules[idx]
	if reason := rule.UnevaluatedReason(); reason != "" {
		t.Fatalf("%s must evaluate under the landed engine, got unevaluated=%q", ruleID, reason)
	}
	if rule.Scope != "core" || rule.Severity != "warning" || rule.Delivery.Receiver != "foreground" {
		t.Fatalf("unexpected %s metadata: scope=%q severity=%q receiver=%q", ruleID, rule.Scope, rule.Severity, rule.Delivery.Receiver)
	}

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 21, 19, 30, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	pending := func() []watch.Notice {
		t.Helper()
		results, err := e.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].ID != ruleID || results[0].Diagnostic() != "" || results[0].Skipped != 0 {
			t.Fatalf("want clean %s evaluation, got %+v", ruleID, results)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		return notices
	}
	appendItemAdd := func(itemID, title, rank, seat string, at time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"item_id":       itemID,
			"title":         title,
			"state":         "in-queue",
			"rank":          rank,
			"allowed_paths": []string{"**"},
			"seat":          seat,
			"criteria": []map[string]any{
				{"criterion_id": "trunk-green", "text": "the full gate is green", "state": "not-done"},
			},
		})
		if err != nil {
			t.Fatal(err)
		}
		id := itemID
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: "item.add", ObjectType: "item", ObjectID: &id, At: at, Payload: body})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	if notices := pending(); len(notices) != 0 {
		t.Fatalf("empty store opened notices: %+v", notices)
	}

	// Non-fixer item additions and an expired trunk fixer stay quiet without skipping events.
	appendItemAdd("item-ordinary", "ordinary work", "100", "build", now.Add(-10*time.Minute))
	appendItemAdd("item-request-7b2ea39afd", "filed request", "100", "plan", now.Add(-8*time.Minute))
	appendItemAdd("defect-go-test", "repair defect", "000", "build", now.Add(-6*time.Minute))
	appendItemAdd("item-fixer-main-note", "non-prefix mention", "100", "docs", now.Add(-4*time.Minute))
	appendItemAdd(drain.TrunkFixerID("000000000000deadbeef"), "fix the broken trunk at 000000000000", "000", "build", now.Add(-2*time.Hour))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("non-fixer and expired item.add events should stay quiet, got %+v", notices)
	}

	fixerOne := drain.TrunkFixerID("9083abc6f62ef057bb4eb15f9b94ab3c052a77ec")
	appendItemAdd(fixerOne, "fix the broken trunk at 9083abc6f62e", "000", "build", now.Add(-5*time.Minute))
	first := pending()
	if len(first) != 1 || first[0].Bundle.RuleID != ruleID || first[0].Bundle.Subject != fixerOne || first[0].Bundle.Severity != "warning" {
		t.Fatalf("want one warning notice for %s, got %+v", fixerOne, first)
	}
	guidance := first[0].Bundle.Guidance
	checkText := strings.ToLower(strings.Join(guidance.Check, " "))
	doText := strings.Join(guidance.Do, " ")
	if !strings.Contains(guidance.Summary, fixerOne) ||
		!strings.Contains(checkText, "heavy") ||
		!strings.Contains(checkText, "failing checks") ||
		!strings.Contains(doText, fixerOne) {
		t.Fatalf("guidance lacks heavy-run failing-checks pointer or fixer id: %+v", guidance)
	}

	// Re-evaluating inside the window keeps the single open alert; a second fixer opens its own.
	if again := pending(); len(again) != 1 || again[0].AlertID != first[0].AlertID {
		t.Fatalf("repeat evaluation inside window must keep the same alert open: first=%+v again=%+v", first, again)
	}
	fixerTwo := drain.TrunkFixerID("1111222233334444555566667777888899990000")
	appendItemAdd(fixerTwo, "fix the broken trunk at 111122223333", "000", "build", now.Add(-1*time.Minute))
	both := pending()
	if len(both) != 2 {
		t.Fatalf("want two open notices across two filed trunk fixers, got %+v", both)
	}

	now = now.Add(61 * time.Minute)
	if aged := pending(); len(aged) != 0 {
		t.Fatalf("alerts should resolve once item.add events age past window, got %+v", aged)
	}
}
