package main

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
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
	idx := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == "drain-item-fault" })
	if idx < 0 {
		t.Fatal("missing drain-item-fault rule")
	}
	rules = rules[idx : idx+1]

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

func TestDrainRepeatedVerifierRefusalsRule(t *testing.T) {
	const ruleID = "drain-repeated-verifier-refusals"
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
	n := 0
	refuse := func(item, step string, at time.Time) {
		t.Helper()
		n++
		run := fmt.Sprintf("run-%d", n)
		body, err := json.Marshal(map[string]string{"item_id": item, "step": step})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: drain.RefusalKind, ObjectType: "run", ObjectID: &run, At: at, Payload: body})
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

	refuse("item-a", drain.RefusalVerifyNotDone, now.Add(-25*time.Hour))
	for i := 4; i > 0; i-- {
		refuse("item-a", drain.RefusalVerifyNotDone, now.Add(-time.Duration(i)*time.Hour))
	}
	refuse("item-b", drain.RefusalVerifyNotDone, now)
	refuse("item-a", drain.RefusalGate, now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("four verifier refusals in the window for one item should stay quiet, got %+v", notices)
	}

	refuse("item-a", drain.RefusalVerifyNotDone, now)
	notices := pending()
	if len(notices) != 1 || notices[0].Bundle.Subject != "item-a" {
		t.Fatalf("want one notice for item-a at five verifier refusals, got %+v", notices)
	}
	guidance := notices[0].Bundle.Guidance
	if !strings.Contains(guidance.Summary, "5 times") ||
		!strings.Contains(strings.Join(guidance.Check, " "), "new each time") ||
		!strings.Contains(strings.Join(guidance.Do, " "), "split the item") {
		t.Fatalf("notice lacks count or guidance on new findings and splitting: %+v", guidance)
	}

	refuse("item-a", drain.RefusalVerifyNotDone, now)
	if again := pending(); len(again) != 1 || again[0].AlertID != notices[0].AlertID {
		t.Fatalf("sixth refusal should keep the same alert open, got %+v", again)
	}
}

func TestTrunkProofLagRule(t *testing.T) {
	const ruleID = "trunk-proof-lag"
	rules, err := watch.LoadRules("../../config/tillr/watch")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule", ruleID)
	}
	rule := rules[idx]
	ctx := context.Background()
	root := t.TempDir()
	now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)

	// Do not let the parent lane's Git context redirect fixture writes.
	var env []string
	for _, entry := range os.Environ() {
		if !strings.HasPrefix(entry, "GIT_") {
			env = append(env, entry)
		}
	}
	env = append(env,
		"GIT_AUTHOR_NAME=Watch Fixture", "GIT_AUTHOR_EMAIL=watch@example.invalid",
		"GIT_COMMITTER_NAME=Watch Fixture", "GIT_COMMITTER_EMAIL=watch@example.invalid",
		"GIT_AUTHOR_DATE=2026-01-01T12:00:00+0000", "GIT_COMMITTER_DATE=2026-01-01T12:00:00+0000",
	)
	git := func(input string, args ...string) string {
		t.Helper()
		cmd := exec.CommandContext(ctx, "git", append([]string{"-C", root, "--git-dir=" + filepath.Join(root, ".git")}, args...)...)
		cmd.Env = env
		cmd.Stdin = strings.NewReader(input)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("fixture git %v: %v: %s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("", "init", "--quiet", "--initial-branch=main")
	tree := git("", "mktree")
	head := git("base", "commit-tree", tree)
	git("", "update-ref", "refs/heads/main", head)
	git("", "update-ref", "refs/tillr/green", head)
	advance := func() {
		t.Helper()
		head = git("advance", "commit-tree", tree, "-p", head)
		git("", "update-ref", "refs/heads/main", head)
	}

	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	e := watch.Evaluator{Store: s, Root: root, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	pending := func() []watch.Notice {
		t.Helper()
		results, err := e.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		if len(results) != 1 || results[0].ID != rule.ID || results[0].Diagnostic() != "" {
			t.Fatalf("want readable trunk-proof-lag evaluation, got %+v", results)
		}
		notices, err := e.Pending(ctx, "foreground")
		if err != nil {
			t.Fatal(err)
		}
		return notices
	}

	advance()
	advance()
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("two commits ahead opened notices: %+v", notices)
	}
	advance()
	first := pending()
	if len(first) != 1 {
		t.Fatalf("want one foreground notice at three commits ahead, got %+v", first)
	}
	if advice := strings.Join(first[0].Bundle.Guidance.Do, " "); !strings.Contains(strings.ToLower(advice), "heavy runner") {
		t.Fatalf("notice advice does not mention the heavy runner: %+v", first[0].Bundle.Guidance)
	}
	advance()
	if notices := pending(); len(notices) != 1 || notices[0].AlertID != first[0].AlertID {
		t.Fatalf("repeat evaluation must retain the single alert ID: first %+v, now %+v", first, notices)
	}
}

func TestDrainRevertRules(t *testing.T) {
	rules, err := watch.LoadRules("../../config/tillr/watch")
	if err != nil {
		t.Fatal(err)
	}
	for _, tc := range []struct{ result, rule string }{
		{"reverted", "drain-landing-reverted"},
		{"not-possible", "drain-revert-not-possible"},
	} {
		t.Run(tc.result, func(t *testing.T) {
			ctx := context.Background()
			s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() { s.Close() })
			now := time.Date(2026, 1, 1, 12, 0, 0, 0, time.UTC)
			landing := "abc123"
			// A refusal may have no item_id when the drain cannot find a landing record.
			payload := map[string]any{"landing": landing, "tip": "def456", "checks": []string{"test"}, "result": tc.result}
			if tc.result == "reverted" {
				payload["item_id"] = "item-a"
				payload["revert"] = "fed321"
			} else {
				payload["reason"] = "no landing record names commit " + landing
			}
			body, err := json.Marshal(payload)
			if err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(ctx, func(tx *store.Tx) error {
				_, err := tx.Append(event.Event{Kind: drain.RevertKind, ObjectType: "landing", ObjectID: &landing, At: now, Payload: body})
				return err
			}); err != nil {
				t.Fatal(err)
			}
			e := watch.Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
			if _, err := e.Evaluate(ctx); err != nil {
				t.Fatal(err)
			}
			notices, err := e.Pending(ctx, "foreground")
			if err != nil {
				t.Fatal(err)
			}
			if len(notices) != 1 || notices[0].Bundle.RuleID != tc.rule || notices[0].Bundle.Subject != landing {
				t.Fatalf("want only %s for landing %s, got %+v", tc.rule, landing, notices)
			}
		})
	}
}
