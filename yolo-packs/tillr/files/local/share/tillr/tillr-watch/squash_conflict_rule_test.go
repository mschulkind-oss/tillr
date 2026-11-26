package main

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestLandSquashConflictRule(t *testing.T) {
	const ruleID = "land-squash-conflict"
	rules, err := watch.LoadRules("rules")
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
	scratchDir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(scratchDir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	seqNum := 0
	appendLanding := func(itemID, result, reason string, conflicts []string, at time.Time) {
		t.Helper()
		seqNum++
		body, err := json.Marshal(land.Landing{
			LandingID:     fmt.Sprintf("landing_%s_%d", itemID, seqNum),
			ItemID:        itemID,
			RunID:         fmt.Sprintf("run-%s-%d", itemID, seqNum),
			VerdictID:     fmt.Sprintf("verdict-%d", seqNum),
			Result:        result,
			Reason:        reason,
			BaseSHA:       "1111111111111111111111111111111111111111",
			HeadSHA:       "2222222222222222222222222222222222222222",
			TrunkBefore:   "3333333333333333333333333333333333333333",
			TrunkAfter:    "3333333333333333333333333333333333333333",
			ConflictPaths: conflicts,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       land.LandingKind,
				ObjectType: "item",
				ObjectID:   &itemID,
				At:         at,
				Payload:    body,
			})
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

	// Empty store stays quiet.
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("empty store opened notices: %+v", notices)
	}

	// Unrelated events, landed/refused landings, and expired squash conflicts stay quiet.
	runID := "run-unrelated"
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       drain.RefusalKind,
			ObjectType: "run",
			ObjectID:   &runID,
			At:         now,
			Payload:    []byte(`{"item_id":"item-other","step":"gate"}`),
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	appendLanding("item-clean", land.ResultLanded, "", nil, now)
	appendLanding("item-refused", land.ResultRefused, "trunk-worktree-uncommitted", []string{"dirty.go"}, now)
	appendLanding("item-expired", land.ResultSquashConflict, "", []string{"CONFLICT (content): Merge conflict in old.go"}, now.Add(-2*time.Hour))
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("non-conflict, unrelated, or expired events opened notices: %+v", notices)
	}

	// A squash-conflict landing opens one foreground notice for that item.
	appendLanding("item-a", land.ResultSquashConflict, "", []string{"CONFLICT (content): Merge conflict in base.txt"}, now)
	first := pending()
	if len(first) != 1 || first[0].Bundle.RuleID != ruleID || first[0].Bundle.Subject != "item-a" || first[0].Bundle.Severity != "warning" {
		t.Fatalf("want one warning notice for item-a after squash conflict, got %+v", first)
	}
	guidance := first[0].Bundle.Guidance
	checkText := strings.Join(guidance.Check, " ")
	doText := strings.Join(guidance.Do, " ")
	if !strings.Contains(guidance.Summary, "item-a") ||
		!strings.Contains(strings.ToLower(guidance.Summary), "squash conflict") ||
		!strings.Contains(checkText, land.LandingKind) ||
		!strings.Contains(checkText, "conflict_paths") ||
		!strings.Contains(checkText, "tillr drain why item-a") ||
		!strings.Contains(doText, "item-a") {
		t.Fatalf("unexpected guidance on squash-conflict notice: %+v", guidance)
	}

	// A repeat squash conflict (or a refused retry) on item-a keeps the same episode,
	// while a squash conflict on item-b opens a separate episode.
	now = now.Add(4 * time.Minute)
	appendLanding("item-a", land.ResultSquashConflict, "", []string{"CONFLICT (content): Merge conflict in base.txt"}, now)
	appendLanding("item-a", land.ResultRefused, "trunk-worktree-uncommitted", []string{"base.txt"}, now.Add(30*time.Second))
	appendLanding("item-b", land.ResultSquashConflict, "", []string{"CONFLICT (content): Merge conflict in other.txt"}, now)
	now = now.Add(time.Minute)
	both := pending()
	bySubject := map[string]watch.Notice{}
	for _, n := range both {
		bySubject[n.Bundle.Subject] = n
	}
	if len(both) != 2 || bySubject["item-a"].AlertID != first[0].AlertID || bySubject["item-b"].AlertID == "" {
		t.Fatalf("want retained alert for item-a and separate alert for item-b, got %+v", both)
	}

	// Once item-a's conflict repair lands cleanly, item-a's episode resolves while item-b remains open.
	now = now.Add(5 * time.Minute)
	appendLanding("item-a", land.ResultLanded, "", nil, now)
	afterLanded := pending()
	if len(afterLanded) != 1 || afterLanded[0].Bundle.Subject != "item-b" {
		t.Fatalf("want item-a resolved after clean landing and item-b still open, got %+v", afterLanded)
	}

	// A later squash conflict on item-a opens a fresh episode with a new AlertID.
	now = now.Add(2 * time.Minute)
	appendLanding("item-a", land.ResultSquashConflict, "", []string{"CONFLICT (content): Merge conflict in second.txt"}, now)
	reopened := pending()
	bySubject = map[string]watch.Notice{}
	for _, n := range reopened {
		bySubject[n.Bundle.Subject] = n
	}
	if len(reopened) != 2 || bySubject["item-a"].AlertID == "" || bySubject["item-a"].AlertID == first[0].AlertID {
		t.Fatalf("later squash conflict after landing should open a new episode: first=%s, got=%+v", first[0].AlertID, reopened)
	}

	// Episodes that do not land resolve once their squash conflicts age past the 1h window.
	now = now.Add(62 * time.Minute)
	if aged := pending(); len(aged) != 0 {
		t.Fatalf("episodes should resolve once squash conflicts age past the window, got %+v", aged)
	}

	// Evaluating all command-eligible rules in config/tillr/watch on one squash-conflict
	// event opens only land-squash-conflict.
	allStore, err := store.Open(ctx, filepath.Join(scratchDir, "all-rules.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { allStore.Close() })
	allEval := watch.Evaluator{Store: allStore, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := allEval.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}
	itemC := "item-c"
	body, err := json.Marshal(land.Landing{
		LandingID:     "landing_item-c_1",
		ItemID:        itemC,
		RunID:         "run-item-c-1",
		VerdictID:     "verdict-c-1",
		Result:        land.ResultSquashConflict,
		ConflictPaths: []string{"CONFLICT (content): Merge conflict in c.go"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := allStore.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: land.LandingKind, ObjectType: "item", ObjectID: &itemC, At: now, Payload: body})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	skipped, err := allEval.Evaluate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if skipped[ruleID] != 0 {
		t.Fatalf("expected 0 skipped events for %s, got %+v", ruleID, skipped)
	}
	allNotices, err := allEval.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(allNotices) != 1 || allNotices[0].Bundle.RuleID != ruleID || allNotices[0].Bundle.Subject != itemC {
		t.Fatalf("want only %s for %s across config/tillr/watch, got %+v", ruleID, itemC, allNotices)
	}
}
