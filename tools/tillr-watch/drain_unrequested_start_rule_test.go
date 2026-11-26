package main

import (
	"bytes"
	"context"
	"os"
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
	const (
		id       = "drain-unrequested-start"
		rulePath = "rules/drain-unrequested-start.toml"
	)
	rawRule, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatal(err)
	}
	fileRules, err := watch.LoadRuleDocument(rulePath, rawRule)
	if err != nil {
		t.Fatal(err)
	}
	if len(fileRules) != 1 || fileRules[0].ID != id {
		t.Fatalf("loaded rules from %s = %+v, want single %s rule", rulePath, fileRules, id)
	}

	rules, err := watch.LoadRules("rules")
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
	for _, loaded := range []watch.Rule{fileRules[0], rule} {
		for _, text := range append([]string{loaded.Guidance.Summary}, append(slices.Clone(loaded.Guidance.Check), loaded.Guidance.Do...)...) {
			if strings.Contains(strings.ToLower(text), "expected") {
				t.Fatalf("%s guidance still calls benign starts expected: %q", id, text)
			}
		}
	}
	if !strings.Contains(rule.Guidance.Summary, "ended without a recorded stop or restart request") {
		t.Fatalf("%s summary does not state the unrecorded stop/restart fault: %q", id, rule.Guidance.Summary)
	}
	joinedCheck := strings.Join(rule.Guidance.Check, " ")
	for _, want := range []string{
		"keeper: loop exited",
		"deliberate first launch",
		".tillr/loop/drain-",
		"loop: stopped by the operator",
	} {
		if !strings.Contains(joinedCheck, want) {
			t.Fatalf("%s guidance.check missing %q: %q", id, want, joinedCheck)
		}
	}
	joinedDo := strings.Join(rule.Guidance.Do, " ")
	if !strings.Contains(joinedDo, "ended without a recorded stop or restart request") {
		t.Fatalf("%s guidance.do missing fault confirmation directive: %q", id, joinedDo)
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
