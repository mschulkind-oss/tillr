package main

import (
	"context"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	_ "github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainPiBuildStaleRule(t *testing.T) {
	rules, err := watch.LoadRules("../../config/tillr/watch")
	if err != nil {
		t.Fatal(err)
	}
	i := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == "drain-pi-build-stale" })
	if i < 0 {
		t.Fatal("missing drain-pi-build-stale rule")
	}
	if reason := rules[i].UnevaluatedReason(); reason != "" {
		t.Fatalf("rule drain-pi-build-stale is unevaluated: %s", reason)
	}
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	now := time.Date(2026, 11, 22, 13, 0, 0, 0, time.UTC)
	tip := "54175817b6000000000000000000000000000000"
	e := watch.Evaluator{Store: s, Rules: rules[i : i+1], Clock: func() time.Time { return now }}
	// Establish the rule baseline before seeding facts (watch judges events from first load onward).
	if _, err := e.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}

	var alertID string
	for _, payload := range []string{
		`{"stale_key":"24c7e4f89e08","wanted_key":"54175817b600","tip":"` + tip + `","reason":"failed","detail":"build-pi exited 2"}`,
		`{"stale_key":"24c7e4f89e08","wanted_key":"54175817b600","tip":"` + tip + `","reason":"skipped","detail":"no [roll] command configured"}`,
	} {
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind: "drain.pi-build-stale", ObjectType: "commit", ObjectID: &tip, At: now,
				Payload: []byte(payload),
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
			t.Fatalf("repeat stale pi build opened a new alert: %s != %s", n.AlertID, alertID)
		}
		alertID = n.AlertID
		g := n.Bundle.Guidance
		if !strings.Contains(g.Summary, "24c7e4f89e08") || !strings.Contains(g.Summary, "54175817b600") {
			t.Fatalf("summary lacks stale/wanted keys: %q", g.Summary)
		}
		if !strings.Contains(strings.Join(g.Do, " "), "54175817b600") {
			t.Fatalf("guidance do lacks wanted key: %+v", g.Do)
		}
	}

	// A subsequent drain.pi-build-current event clears the alert.
	now = now.Add(time.Minute)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind: "drain.pi-build-current", ObjectType: "commit", ObjectID: &tip, At: now,
			Payload: []byte(`{"key":"54175817b600","tip":"` + tip + `"}`),
		})
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
	if len(notices) != 0 {
		t.Fatalf("expected alert to clear after drain.pi-build-current, got %+v", notices)
	}
}
