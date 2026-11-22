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

func TestDrainHeavyFaultRule(t *testing.T) {
	const ruleID = "drain-heavy-fault"
	rules, err := watch.LoadRules("../../config/tillr/watch")
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
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	appendEvent := func(kind, objectType, objectID string, at time.Time, payload any) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: kind, ObjectType: objectType, ObjectID: &objectID, At: at, Payload: body})
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
		if len(results) != 1 || results[0].ID != ruleID || results[0].Diagnostic() != "" || results[0].Skipped != 0 {
			t.Fatalf("unexpected evaluation results: %+v", results)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		return notices
	}
	fault := func(tip, detail string, at time.Time) {
		t.Helper()
		appendEvent(drain.HeavyFaultKind, "commit", tip, at, map[string]string{"tip_sha": tip, "detail": detail})
	}

	if notices := pending(); len(notices) != 0 {
		t.Fatalf("empty store opened notices: %+v", notices)
	}
	appendEvent(drain.RevertKind, "landing", "0000000000000000000000000000000000000001", now, map[string]any{
		"landing": "0000000000000000000000000000000000000001",
		"tip":     "0000000000000000000000000000000000000002",
		"checks":  []string{"test"},
		"result":  "reverted",
	})
	fault("aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa", "expired fault", now.Add(-2*time.Hour))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("unrelated or expired events opened notices: %+v", notices)
	}

	const tipA = "1111111111111111111111111111111111111111"
	const tipB = "2222222222222222222222222222222222222222"
	fault(tipA, "worktree reset failed on first attempt", now)
	first := pending()
	if len(first) != 1 || first[0].Bundle.RuleID != ruleID || first[0].Bundle.Subject != tipA {
		t.Fatalf("want one notice for %s after its first fault, got %+v", tipA, first)
	}
	guidance := first[0].Bundle.Guidance
	checkText := strings.Join(guidance.Check, " ")
	doText := strings.Join(guidance.Do, " ")
	if !strings.Contains(guidance.Summary, tipA) ||
		!strings.Contains(strings.ToLower(checkText), "fault text") ||
		!strings.Contains(checkText, ".tillr/lanes/.rolling") ||
		!strings.Contains(doText, tipA) {
		t.Fatalf("notice lacks plain-word guidance for fault text and .tillr/lanes/.rolling: %+v", guidance)
	}

	now = now.Add(time.Minute)
	fault(tipA, "worktree reset failed on retry", now)
	fault(tipB, "suite runner exited before verdict", now)
	notices := pending()
	counts := map[string]int{}
	for _, notice := range notices {
		counts[notice.Bundle.Subject]++
		if notice.Bundle.Subject == tipA && notice.AlertID != first[0].AlertID {
			t.Fatalf("repeat fault on %s replaced its notice: first %+v, now %+v", tipA, first[0], notice)
		}
	}
	if len(notices) != 2 || counts[tipA] != 1 || counts[tipB] != 1 {
		t.Fatalf("want one notice per tip commit, got %+v", notices)
	}

	now = now.Add(62 * time.Minute)
	if aged := pending(); len(aged) != 0 {
		t.Fatalf("episodes should resolve once faults age past the window, got %+v", aged)
	}
}
