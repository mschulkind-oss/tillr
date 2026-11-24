package main

import (
	"bytes"
	"context"
	"path/filepath"
	"slices"
	"strings"
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
	rule := rules[i]
	if reason := rule.UnevaluatedReason(); reason != "" {
		t.Fatalf("%s unevaluated: %s", id, reason)
	}
	for _, text := range append([]string{rule.Guidance.Summary}, append(slices.Clone(rule.Guidance.Check), rule.Guidance.Do...)...) {
		if strings.Contains(strings.ToLower(text), "expected") {
			t.Fatalf("%s guidance still calls benign starts expected: %q", id, text)
		}
	}
	var lintOut, lintErr bytes.Buffer
	if code := run(t.Context(), []string{"lint", "../.."}, &lintOut, &lintErr); code != 0 {
		t.Fatalf("tillr watch lint failed (%d): stdout=%q stderr=%q", code, lintOut.String(), lintErr.String())
	}

	for _, tc := range []struct {
		name, payload string
		want          int
	}{
		{"no request", `{"role":"drain"}`, 1},
		{"requested", `{"role":"drain","restart_request":"restart-drain-1"}`, 0},
		{"stopped", `{"role":"drain","stop_request":"stop-drain-1"}`, 0},
		{"initial", `{"role":"drain","initial":true}`, 0},
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
			results, err := e.EvaluateRules(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if len(results) != 1 || results[0].Diagnostic() != "" || results[0].Skipped != 0 {
				t.Fatalf("unexpected evaluation results: %+v", results)
			}
			notices, err := e.Pending(ctx, "foreground")
			if err != nil {
				t.Fatal(err)
			}
			if len(notices) != tc.want {
				t.Fatalf("got %d notices, want %d: %+v", len(notices), tc.want, notices)
			}
			for _, n := range notices {
				g := n.Bundle.Guidance
				if strings.Contains(strings.ToLower(g.Summary+" "+strings.Join(g.Check, " ")+" "+strings.Join(g.Do, " ")), "expected") {
					t.Fatalf("notice guidance still mentions expected: %+v", g)
				}
			}
		})
	}
}
