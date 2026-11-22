package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainTrunkCoreWorktreeRule(t *testing.T) {
	rules, err := watch.LoadRules("../../config/tillr/watch")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == "drain-trunk-core-worktree" })
	if i < 0 {
		t.Fatal("missing drain-trunk-core-worktree rule")
	}
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	config := "/repo/.git/config"
	e := watch.Evaluator{Store: s, Rules: rules[i : i+1], Clock: func() time.Time { return now }}
	var alertID string
	for range 2 {
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind: "drain.trunk-core-worktree", ObjectType: "repository", ObjectID: &config, At: now,
				Payload: []byte(`{"config_path":"/repo/.git/config","worktree":"/repo/run/worktree"}`),
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
		if _, err := e.Evaluate(ctx); err != nil {
			t.Fatal(err)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil || len(notices) != 1 {
			t.Fatalf("want one foreground notice, got %+v / %v", notices, err)
		}
		n := notices[0]
		if alertID != "" && n.AlertID != alertID {
			t.Fatalf("repeat observation opened a new alert: %s != %s", n.AlertID, alertID)
		}
		alertID = n.AlertID
		guidance := n.Bundle.Guidance
		if n.Bundle.Subject != config || !strings.Contains(strings.Join(guidance.Check, " "), "Save a copy of "+config) ||
			!strings.Contains(strings.Join(guidance.Do, " "), "which run's directory") {
			t.Fatalf("notice lacks config investigation guidance: %+v", n.Bundle)
		}
	}
}
