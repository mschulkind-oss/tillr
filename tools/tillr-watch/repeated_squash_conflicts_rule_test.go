package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestLandRepeatedSquashConflictsRule(t *testing.T) {
	const ruleID = "land-repeated-squash-conflicts"
	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	idx := slices.IndexFunc(rules, func(rule watch.Rule) bool { return rule.ID == ruleID })
	if idx < 0 {
		t.Fatalf("missing %s rule in config/tillr/watch", ruleID)
	}
	rule := rules[idx]
	if reason := rule.UnevaluatedReason(); reason != "" {
		t.Fatalf("rule %s is unevaluated: %s", ruleID, reason)
	}
	if rule.Scope != "core" || rule.Severity != "warning" || rule.Delivery.Receiver != "foreground" ||
		rule.When.Pattern != watch.ConsecutiveCount || rule.When.Threshold != 3 || rule.When.Window != 24*time.Hour {
		t.Fatalf("unexpected %s configuration: %+v", ruleID, rule)
	}

	ctx := context.Background()
	scratchDir := t.TempDir()
	s, err := store.Open(ctx, filepath.Join(scratchDir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 22, 16, 37, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	seqNum := 0
	appendLandingPairTo := func(st *store.Store, itemID, result, reason, headSHA, trunkBefore string, conflicts []string, at time.Time) {
		t.Helper()
		seqNum++
		if headSHA == "" {
			headSHA = fmt.Sprintf("22222222222222222222222222222222%08x", seqNum)
		}
		if trunkBefore == "" {
			trunkBefore = fmt.Sprintf("33333333333333333333333333333333%08x", seqNum)
		}
		body, err := json.Marshal(land.Landing{
			LandingID:     fmt.Sprintf("landing_%s_%d", itemID, seqNum),
			ItemID:        itemID,
			RunID:         fmt.Sprintf("run-%s-%d", itemID, seqNum),
			VerdictID:     fmt.Sprintf("verdict-%d", seqNum),
			Result:        result,
			Reason:        reason,
			BaseSHA:       "1111111111111111111111111111111111111111",
			HeadSHA:       headSHA,
			TrunkBefore:   trunkBefore,
			TrunkAfter:    trunkBefore,
			ConflictPaths: conflicts,
		})
		if err != nil {
			t.Fatal(err)
		}
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
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
	appendLandingTo := func(st *store.Store, itemID, result, reason string, conflicts []string, at time.Time) {
		appendLandingPairTo(st, itemID, result, reason, "", "", conflicts, at)
	}
	appendLanding := func(itemID, result, reason string, conflicts []string, at time.Time) {
		appendLandingTo(s, itemID, result, reason, conflicts, at)
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

	// 1. One and two squash conflicts stay quiet (no alert).
	appendLanding("item-a", land.ResultSquashConflict, "", []string{"CONFLICT (content): Merge conflict in docs/design/tillr-watch.md"}, now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("1 squash conflict opened notice: %+v", notices)
	}
	now = now.Add(12 * time.Minute)
	appendLanding("item-a", land.ResultSquashConflict, "", []string{"CONFLICT (content): Merge conflict in docs/design/tillr-watch.md"}, now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("2 squash conflicts opened notice: %+v", notices)
	}

	// 2. A landing in between resets the consecutive count, so four squash conflicts split
	// 2-before-landed and 2-after-landed open no alert.
	appendLanding("item-between", land.ResultSquashConflict, "", []string{"CONFLICT (content): Merge conflict in shared.go"}, now.Add(-10*time.Minute))
	appendLanding("item-between", land.ResultSquashConflict, "", []string{"CONFLICT (content): Merge conflict in shared.go"}, now.Add(-5*time.Minute))
	appendLanding("item-between", land.ResultLanded, "", nil, now.Add(-3*time.Minute))
	appendLanding("item-between", land.ResultSquashConflict, "", []string{"CONFLICT (content): Merge conflict in shared.go"}, now.Add(-2*time.Minute))
	appendLanding("item-between", land.ResultSquashConflict, "", []string{"CONFLICT (content): Merge conflict in shared.go"}, now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("squash conflicts separated by a landed event opened notice: %+v", notices)
	}

	// 3. A transient non-landing refusal (result == "refused") does not reset item-a's 2-conflict streak.
	now = now.Add(5 * time.Minute)
	appendLanding("item-a", land.ResultRefused, "trunk-worktree-uncommitted", []string{"dirty.go"}, now)
	if notices := pending(); len(notices) != 0 {
		t.Fatalf("refused landing after 2 conflicts opened notice: %+v", notices)
	}

	// 4. The third consecutive squash conflict on item-a opens one alert naming item, count, and conflicting paths.
	now = now.Add(8 * time.Minute)
	thirdPaths := []string{
		"CONFLICT (content): Merge conflict in docs/design/tillr-watch.md",
		"CONFLICT (content): Merge conflict in tools/tillr/watch/bundle.go",
	}
	wantPaths := strings.Join(thirdPaths, ", ")
	appendLanding("item-a", land.ResultSquashConflict, "", thirdPaths, now)
	opened := pending()
	if len(opened) != 1 || opened[0].Bundle.RuleID != ruleID || opened[0].Bundle.Subject != "item-a" || opened[0].Bundle.Severity != "warning" {
		t.Fatalf("want 1 warning notice for item-a at 3rd squash conflict, got %+v", opened)
	}
	guidance := opened[0].Bundle.Guidance
	checkText := strings.Join(guidance.Check, " ")
	doText := strings.Join(guidance.Do, " ")
	if !strings.Contains(guidance.Summary, "item-a") ||
		!strings.Contains(guidance.Summary, "3") ||
		!strings.Contains(guidance.Summary, wantPaths) ||
		strings.Contains(guidance.Summary, "unknown") ||
		!strings.Contains(checkText, "item-a") ||
		!strings.Contains(checkText, wantPaths) ||
		!strings.Contains(doText, wantPaths) {
		t.Fatalf("guidance must name item, count, and conflicting paths without unknown: %+v", guidance)
	}

	// 5. A fourth consecutive squash conflict on item-a updates the same episode in place,
	// while three consecutive conflicts on item-b open an independent episode.
	now = now.Add(21 * time.Minute)
	appendLanding("item-a", land.ResultSquashConflict, "", thirdPaths, now)
	appendLanding("item-b", land.ResultSquashConflict, "", []string{"other.go"}, now.Add(-4*time.Minute))
	appendLanding("item-b", land.ResultSquashConflict, "", []string{"other.go"}, now.Add(-2*time.Minute))
	appendLanding("item-b", land.ResultSquashConflict, "", []string{"other.go"}, now)
	both := pending()
	bySubject := map[string]watch.Notice{}
	for _, n := range both {
		bySubject[n.Bundle.Subject] = n
	}
	if len(both) != 2 || bySubject["item-a"].AlertID != opened[0].AlertID || bySubject["item-b"].AlertID == "" {
		t.Fatalf("want retained alert for item-a and independent alert for item-b, got %+v", both)
	}
	unattached := func(string) (bool, error) { return false, nil }
	status, err := e.ReadStatus(ctx, unattached)
	if err != nil {
		t.Fatal(err)
	}
	for _, a := range status.Alerts {
		if a.Subject == "item-a" && a.Observed != 2 {
			t.Fatalf("expected item-a episode Observed=2 after 4th conflict, got %+v", a)
		}
	}

	// 6. Repeating one candidate head against one trunk tip does not fire the rule;
	// only conflicts that follow a repair (new head_sha) or a trunk move (new trunk_before) count.
	const (
		sameHead     = "aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
		sameTrunk    = "bbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbbb"
		movedTrunk   = "cccccccccccccccccccccccccccccccccccccccc"
		repairedHead = "dddddddddddddddddddddddddddddddddddddddd"
	)
	for i := 0; i < 5; i++ {
		now = now.Add(time.Second)
		appendLandingPairTo(s, "item-repeat", land.ResultSquashConflict, "", sameHead, sameTrunk, []string{"shared.go"}, now)
	}
	for _, n := range pending() {
		if n.Bundle.Subject == "item-repeat" {
			t.Fatalf("repeated same (head_sha, trunk_before) fired alert: %+v", n)
		}
	}
	// A conflict after a trunk move (2nd distinct pair) stays below threshold 3.
	now = now.Add(time.Second)
	appendLandingPairTo(s, "item-repeat", land.ResultSquashConflict, "", sameHead, movedTrunk, []string{"shared.go"}, now)
	for _, n := range pending() {
		if n.Bundle.Subject == "item-repeat" {
			t.Fatalf("2 distinct (head_sha, trunk_before) pairs fired alert: %+v", n)
		}
	}
	// A conflict after a repair (3rd distinct pair) reaches threshold 3 and fires.
	now = now.Add(time.Second)
	appendLandingPairTo(s, "item-repeat", land.ResultSquashConflict, "", repairedHead, movedTrunk, []string{"shared.go"}, now)
	repeatFound := false
	for _, n := range pending() {
		if n.Bundle.Subject == "item-repeat" {
			repeatFound = true
		}
	}
	if !repeatFound {
		t.Fatalf("expected item-repeat to fire after 3 distinct (head_sha, trunk_before) pairs")
	}
	now = now.Add(time.Second)
	appendLanding("item-repeat", land.ResultLanded, "", nil, now)

	// 7. A clean landing on item-a resolves item-a's episode while item-b remains open.
	now = now.Add(10 * time.Minute)
	appendLanding("item-a", land.ResultLanded, "", nil, now)
	afterLanded := pending()
	if len(afterLanded) != 1 || afterLanded[0].Bundle.Subject != "item-b" {
		t.Fatalf("want item-a resolved on landed and item-b still open, got %+v", afterLanded)
	}

	// 7. Worked replay of item-request-a386b7eb1b's 2026-11-22 timeline across all rules in config/tillr/watch.
	allStore, err := store.Open(ctx, filepath.Join(scratchDir, "all-rules.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { allStore.Close() })
	var eligible []watch.Rule
	for _, r := range rules {
		if r.UnevaluatedReason() == "" {
			eligible = append(eligible, r)
		}
	}
	replayNow := time.Date(2026, 11, 22, 16, 37, 0, 0, time.UTC)
	allEval := watch.Evaluator{Store: allStore, Rules: eligible, Clock: func() time.Time { return replayNow }}
	if _, err := allEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	const incidentItem = "item-request-a386b7eb1b"
	incidentPaths := []string{"CONFLICT (content): Merge conflict in docs/design/tillr-watch.md"}
	evalAndConfirm := func(owner string) []watch.Notice {
		t.Helper()
		res, err := allEval.EvaluateRules(ctx)
		if err != nil {
			t.Fatal(err)
		}
		for _, r := range res {
			if (r.ID == ruleID || r.ID == "land-squash-conflict") && (r.Diagnostic() != "" || r.Skipped != 0) {
				t.Fatalf("unexpected rule result: %+v", r)
			}
		}
		notices, err := allEval.Pending(ctx, owner)
		if err != nil {
			t.Fatal(err)
		}
		if len(notices) > 0 {
			through := notices[len(notices)-1].Seq
			if err := allEval.Shown(ctx, owner, through); err != nil {
				t.Fatal(err)
			}
			if err := allEval.Seen(ctx, owner, through); err != nil {
				t.Fatal(err)
			}
		}
		return notices
	}

	// 11:37 AM ET (conflict #1): opens land-squash-conflict only (routed to triage); foreground stays quiet.
	appendLandingTo(allStore, incidentItem, land.ResultSquashConflict, "", incidentPaths, replayNow)
	n1 := evalAndConfirm("tillr-triage")
	if len(n1) != 1 || n1[0].Bundle.RuleID != "land-squash-conflict" {
		t.Fatalf("conflict #1: want only land-squash-conflict for tillr-triage, got %+v", n1)
	}
	if fg1 := evalAndConfirm("foreground"); len(fg1) != 0 {
		t.Fatalf("conflict #1: want 0 foreground notices, got %+v", fg1)
	}

	// 11:49 AM ET (conflict #2): no new foreground wake.
	replayNow = replayNow.Add(12 * time.Minute)
	appendLandingTo(allStore, incidentItem, land.ResultSquashConflict, "", incidentPaths, replayNow)
	if n2 := evalAndConfirm("foreground"); len(n2) != 0 {
		t.Fatalf("conflict #2: want 0 new notices, got %+v", n2)
	}

	// 12:02 PM ET (conflict #3): wakes foreground with land-repeated-squash-conflicts.
	replayNow = replayNow.Add(13 * time.Minute)
	appendLandingTo(allStore, incidentItem, land.ResultSquashConflict, "", incidentPaths, replayNow)
	n3 := evalAndConfirm("foreground")
	if len(n3) != 1 || n3[0].Bundle.RuleID != ruleID || n3[0].Bundle.Subject != incidentItem ||
		!strings.Contains(n3[0].Bundle.Guidance.Summary, incidentItem) ||
		!strings.Contains(n3[0].Bundle.Guidance.Summary, "3") ||
		!strings.Contains(n3[0].Bundle.Guidance.Summary, incidentPaths[0]) {
		t.Fatalf("conflict #3: want %s notice with item, count 3, and path, got %+v", ruleID, n3)
	}

	// 12:23 PM ET (conflict #4): counts in place without opening a duplicate notice.
	replayNow = replayNow.Add(21 * time.Minute)
	appendLandingTo(allStore, incidentItem, land.ResultSquashConflict, "", incidentPaths, replayNow)
	if n4 := evalAndConfirm("foreground"); len(n4) != 0 {
		t.Fatalf("conflict #4: want 0 duplicate notices, got %+v", n4)
	}

	// Subsequent clean landing resolves both open alerts.
	replayNow = replayNow.Add(10 * time.Minute)
	appendLandingTo(allStore, incidentItem, land.ResultLanded, "", nil, replayNow)
	if _, err := allEval.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	st, err := allEval.ReadStatus(ctx, unattached)
	if err != nil {
		t.Fatal(err)
	}
	if len(st.Alerts) != 0 {
		t.Fatalf("expected all alerts resolved after landed, got %+v", st.Alerts)
	}

	// 8. tillr watch lint passes on the repository with the new rule present.
	root := filepath.Clean(filepath.Join("..", ".."))
	policies, err := filepath.Glob(filepath.Join(root, "config", "tillr", "backend-policy*.json"))
	if err != nil || len(policies) == 0 {
		t.Fatalf("glob policies: %v (len=%d)", err, len(policies))
	}
	slices.Sort(policies)
	lintArgs := []string{"lint", root}
	for _, p := range policies {
		lintArgs = append(lintArgs, "--policy", p)
	}
	var lintOut, lintErr bytes.Buffer
	if code := run(t.Context(), lintArgs, &lintOut, &lintErr); code != 0 || lintErr.Len() != 0 {
		t.Fatalf("tillr watch lint failed: code=%d stdout=%q stderr=%q", code, lintOut.String(), lintErr.String())
	}
}
