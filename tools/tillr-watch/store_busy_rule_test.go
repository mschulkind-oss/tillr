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

func TestDrainStoreBusyRule(t *testing.T) {
	const ruleID = "drain-store-busy"
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
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: rules[idx : idx+1], Clock: func() time.Time { return now }}

	if _, err := e.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 0 {
		t.Fatalf("expected no notice before any %s event, got %+v", drain.StoreBusyKind, notices)
	}

	body, err := json.Marshal(drain.StoreBusyFact{
		Operation: "session-heartbeat",
		WaitedMS:  5000,
		SessionID: "sess-1",
		At:        now.Format(time.RFC3339Nano),
	})
	if err != nil {
		t.Fatal(err)
	}
	sessionID := "sess-1"
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       drain.StoreBusyKind,
			ObjectType: "session",
			ObjectID:   &sessionID,
			At:         now,
			Payload:    body,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if _, err := e.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err = e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 {
		t.Fatalf("expected 1 notice after %s event, got %+v", drain.StoreBusyKind, notices)
	}
	checkText := strings.Join(notices[0].Bundle.Guidance.Check, " ")
	if !strings.Contains(checkText, "/proc/locks") || !strings.Contains(checkText, "-shm") {
		t.Fatalf("guidance.check lacks /proc/locks or -shm instruction: %+v", notices[0].Bundle.Guidance)
	}
}
