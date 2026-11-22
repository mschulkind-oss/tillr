package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/obstacles"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainObstacleHoldRule(t *testing.T) {
	const ruleID = "drain-obstacle-hold"
	rules, err := watch.LoadRules("../../config/tillr/watch")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule", ruleID)
	}

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Date(2026, 11, 21, 22, 0, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: rules[idx : idx+1], Clock: func() time.Time { return now }}
	appendEvent := func(kind, itemID string, payload any) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: kind, ObjectType: "item", ObjectID: &itemID, At: now, Payload: body})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	pending := func() []watch.Notice {
		t.Helper()
		if _, err := e.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		return notices
	}
	hold := func(itemID, runID, kind string, fields map[string]string) {
		t.Helper()
		appendEvent(obstacles.HeldKind, itemID, obstacles.NewHold(itemID, runID, kind, fields, nil, "", nil))
	}

	appendEvent(obstacles.ClearedKind, "item-c", obstacles.ClearPayload{ItemID: "item-c", RunID: "run-c", Reason: "retry"})
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("a clear without a hold opened notices: %+v", notices)
	}

	question := map[string]string{"QUESTION": "Which option should the drain use?"}
	hold("item-a", "run-a", "owner", question)
	first := pending()
	if len(first) != 1 || first[0].Bundle.Subject != "item-a" {
		t.Fatalf("want one notice for item-a after its first hold, got %+v", first)
	}
	guidance := first[0].Bundle.Guidance
	if !strings.Contains(strings.Join(guidance.Check, " "), "tillr drain why item-a") ||
		!strings.Contains(strings.Join(guidance.Do, " "), "tillr drain retry --item item-a") {
		t.Fatalf("notice lacks hold investigation or retry guidance: %+v", guidance)
	}

	hold("item-a", "run-a2", "owner", question)
	hold("item-b", "run-b", "trunk", map[string]string{"CHECKS": "verify"})
	notices := pending()
	counts := map[string]int{}
	for _, notice := range notices {
		counts[notice.Bundle.Subject]++
		if notice.Bundle.Subject == "item-a" && notice.AlertID != first[0].AlertID {
			t.Fatalf("repeat hold replaced item-a's notice: first %+v, now %+v", first[0], notice)
		}
	}
	if len(notices) != 2 || counts["item-a"] != 1 || counts["item-b"] != 1 {
		t.Fatalf("want one notice per held item, got %+v", notices)
	}
}
