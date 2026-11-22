package main

import (
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainItemFaultRule(t *testing.T) {
	rules, err := watch.LoadRules("../../config/tillr/watch")
	if err != nil {
		t.Fatal(err)
	}
	if len(rules) != 1 || rules[0].ID != "drain-item-fault" {
		t.Fatalf("want only drain-item-fault rule, got %+v", rules)
	}

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	appendEvent := func(kind, item string, payload any) {
		t.Helper()
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: kind, ObjectType: "item", ObjectID: &item, At: now, Payload: body})
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

	fault := func(item, detail string) {
		t.Helper()
		appendEvent(faults.HeldKind, item, faults.Hold{ItemID: item, Reason: "decision-collision", Detail: detail})
	}

	appendEvent("drain.refusal", "item-a", map[string]string{"item_id": "item-a", "reason": "test refusal"})
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("non-fault event opened notices: %+v", notices)
	}
	fault("item-a", "first collision")
	first := pending()
	if len(first) != 1 || first[0].Bundle.Subject != "item-a" {
		t.Fatalf("want one notice for item-a after its first fault, got %+v", first)
	}
	guidance := first[0].Bundle.Guidance
	if !strings.Contains(strings.Join(guidance.Check, " "), "tillr drain why item-a") ||
		!strings.Contains(strings.Join(guidance.Do, " "), "fault detail") {
		t.Fatalf("notice lacks fault investigation guidance: %+v", guidance)
	}
	fault("item-a", "second collision")
	fault("item-b", "another item collided")
	notices := pending()
	counts := map[string]int{}
	for _, notice := range notices {
		counts[notice.Bundle.Subject]++
		if notice.Bundle.Subject == "item-a" && notice.AlertID != first[0].AlertID {
			t.Fatalf("repeat fault replaced item-a's notice: first %+v, now %+v", first[0], notice)
		}
	}
	if len(notices) != 2 || counts["item-a"] != 1 || counts["item-b"] != 1 {
		t.Fatalf("want one notice per item, got %+v", notices)
	}
}
