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

func TestDrainPlanOutputUnparseableRule(t *testing.T) {
	const ruleID = "drain-plan-output-unparseable"
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
		t.Fatalf("rule %s is unevaluated: %s", ruleID, reason)
	}

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 22, 12, 39, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}

	appendRefusal := func(runID, itemID, step, detail string) {
		t.Helper()
		body, err := json.Marshal(map[string]string{
			"item_id": itemID,
			"step":    step,
			"detail":  detail,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       drain.RefusalKind,
				ObjectType: "run",
				ObjectID:   &runID,
				At:         now,
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
		notices, err := e.Pending(ctx, "tillr-triage")
		if err != nil {
			t.Fatal(err)
		}
		return notices
	}

	// Unrelated refusals do not open an alert.
	appendRefusal("run-1", "item-other", drain.RefusalNodeOutput, "node-output-missing: first.findings: file missing")
	appendRefusal("run-2", "item-other", drain.RefusalGate, "gate failed")
	if notices := evaluatePending(); len(notices) != 0 {
		t.Fatalf("unrelated refusals opened notices: %+v", notices)
	}

	// An unparseable plan output refusal opens an alert naming the item and parse error.
	const parseErr = "json: cannot unmarshal string into Go value of type drain.deliveryDecision"
	appendRefusal("run-3", "item-a", drain.RefusalPlanOutput, parseErr)
	notices := evaluatePending()
	if len(notices) != 1 {
		t.Fatalf("want 1 notice, got %d: %+v", len(notices), notices)
	}
	n := notices[0]
	if n.Bundle.RuleID != ruleID || n.Bundle.Subject != "item-a" {
		t.Fatalf("unexpected notice bundle: %+v", n.Bundle)
	}
	if !strings.Contains(n.Bundle.Guidance.Summary, "item-a") || !strings.Contains(n.Bundle.Guidance.Summary, parseErr) {
		t.Fatalf("summary must include item and parse error, got: %q", n.Bundle.Guidance.Summary)
	}
}
