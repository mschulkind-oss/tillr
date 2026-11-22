package main

import (
	"context"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainUnrequestedStartRule(t *testing.T) {
	const id = "drain-unrequested-start"
	rules, err := watch.LoadRules("../../config/tillr/watch")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == id })
	if i == -1 {
		t.Fatalf("missing %s rule", id)
	}
	for _, tc := range []struct {
		name, payload string
		want          int
	}{
		{"no request", `{"role":"drain"}`, 1},
		{"requested", `{"role":"drain","restart_request":"restart-drain-1"}`, 0},
		{"view start", `{"role":"view"}`, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			ctx := context.Background()
			s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			now := time.Date(2026, 11, 22, 3, 38, 0, 0, time.UTC)
			e := watch.Evaluator{Store: s, Rules: rules[i : i+1], Clock: func() time.Time { return now }}
			if _, err := e.Evaluate(ctx); err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Append(event.Event{Kind: restart.BuildKind, ObjectType: "build", At: now, Payload: []byte(tc.payload)})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := e.Evaluate(ctx); err != nil {
				t.Fatal(err)
			}
			notices, err := e.Pending(ctx, "foreground")
			if err != nil {
				t.Fatal(err)
			}
			if len(notices) != tc.want {
				t.Fatalf("got %d notices, want %d: %+v", len(notices), tc.want, notices)
			}
		})
	}
}
