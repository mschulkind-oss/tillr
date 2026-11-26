package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	_ "github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-land/land"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestDrainItemFifthAttemptRule(t *testing.T) {
	const (
		ruleID       = "drain-item-fifth-attempt"
		stepRuleID   = "drain-repeated-step-dispatches"
		allocateKind = "run.allocate"
		landingKind  = "land.landing"
	)

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
	for _, text := range append([]string{rule.Guidance.Summary}, append(slices.Clone(rule.Guidance.Check), rule.Guidance.Do...)...) {
		if strings.Contains(strings.ToLower(text), "expected") {
			t.Fatalf("%s guidance contains 'expected': %q", ruleID, text)
		}
	}

	scratchDir := t.TempDir()
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(scratchDir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 23, 2, 0, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: []watch.Rule{rule}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	appendAllocateTo := func(st *store.Store, runID, itemID, node, laneID string, at time.Time) {
		t.Helper()
		payload := map[string]any{
			"run_id":     runID,
			"item_id":    itemID,
			"backend":    "pi-plan",
			"lane_id":    laneID,
			"started_at": at.UTC().Format(time.RFC3339),
		}
		if node != "" {
			payload["seat"] = "plan"
			payload["workflow"] = "planned-build@v1"
			payload["node"] = node
		}
		body, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		lane := laneID
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       allocateKind,
				ObjectType: "lane",
				ObjectID:   &lane,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	appendAllocate := func(runID, itemID, node, laneID string, at time.Time) {
		t.Helper()
		appendAllocateTo(s, runID, itemID, node, laneID, at)
	}
	appendLandedTo := func(st *store.Store, itemID, runID string, at time.Time) {
		t.Helper()
		body, err := json.Marshal(map[string]any{
			"landing_id":   "landing_" + itemID + "_1",
			"item_id":      itemID,
			"run_id":       runID,
			"verdict_id":   "verdict_" + runID,
			"result":       land.ResultLanded,
			"base_sha":     "1111111111111111111111111111111111111111",
			"head_sha":     "2222222222222222222222222222222222222222",
			"trunk_before": "1111111111111111111111111111111111111111",
			"trunk_after":  "3333333333333333333333333333333333333333",
		})
		if err != nil {
			t.Fatal(err)
		}
		objID := "landing_" + itemID + "_1"
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{
				Kind:       landingKind,
				ObjectType: "landing",
				ObjectID:   &objID,
				At:         at,
				Payload:    body,
			})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}
	setItemState := func(st *store.Store, itemID, state string) {
		t.Helper()
		if err := st.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(
				`INSERT INTO items (item_id, title, state, updated_at) VALUES (?, ?, ?, ?)
				 ON CONFLICT(item_id) DO UPDATE SET state = excluded.state, updated_at = excluded.updated_at`,
				itemID, itemID, state, now.UnixMilli(),
			)
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

	if notices := pending(); len(notices) != 0 {
		t.Fatalf("empty store opened notices: %+v", notices)
	}

	// 1. Attempts 1..4 stay quiet across workflow nodes, graph revisions, restarts,
	// judge/cfix suffixes, non-workflow runs, and item IDs that themselves contain -a5.
	const incidentItem = "item-request-f47a9d41f7"
	for attempt := 1; attempt <= 4; attempt++ {
		if attempt > 1 {
			now = now.Add(4 * time.Hour)
		}
		appendAllocate(fmt.Sprintf("run-%s-a%d-n1", incidentItem, attempt), incidentItem, "plan", "lane-3", now)
		appendAllocate(fmt.Sprintf("run-%s-a%d-n2-g1-0123abcd", incidentItem, attempt), incidentItem, "work/piece-1", "lane-3", now.Add(5*time.Minute))
		appendAllocate(fmt.Sprintf("run-%s-a%d-n2-g1-0123abcd-restart", incidentItem, attempt), incidentItem, "work/piece-1", "lane-3", now.Add(10*time.Minute))
		appendAllocate(fmt.Sprintf("run-%s-a%d-n2-g1-0123abcd-002-failover", incidentItem, attempt), incidentItem, "work/piece-1", "lane-3", now.Add(11*time.Minute))
		appendAllocate(fmt.Sprintf("run-%s-a%d-n3-judge", incidentItem, attempt), incidentItem, "verify", "lane-3", now.Add(15*time.Minute))
		appendAllocate(fmt.Sprintf("run-%s-a%d-n3-cfix", incidentItem, attempt), incidentItem, "verify", "lane-3", now.Add(20*time.Minute))
		appendAllocate(fmt.Sprintf("run-item-plain-a%d", attempt), "item-plain", "", "lane-2", now.Add(25*time.Minute))
		appendAllocate(fmt.Sprintf("run-item-a5-a%d-n1", attempt), "item-a5", "plan", "lane-4", now.Add(30*time.Minute))
		appendAllocate(fmt.Sprintf("run-item-a5-fix-a%d-n1", attempt), "item-a5-fix", "plan", "lane-4", now.Add(35*time.Minute))
		now = now.Add(40 * time.Minute)
		if notices := pending(); len(notices) != 0 {
			t.Fatalf("attempt %d (< 5) should stay quiet, got %+v", attempt, notices)
		}
	}

	// 2. Starting the 5th attempt hours/days after earlier attempts opens one warning
	// alert naming the item, run ID, lane, and guidance to read why each previous attempt ended.
	now = now.Add(18 * time.Hour)
	fifthRun := fmt.Sprintf("run-%s-a5-n1", incidentItem)
	appendAllocate(fifthRun, incidentItem, "plan", "lane-5", now)
	first := pending()
	if len(first) != 1 || first[0].Bundle.RuleID != ruleID || first[0].Bundle.Subject != incidentItem || first[0].Bundle.Severity != "warning" {
		t.Fatalf("want one warning notice for %s on 5th attempt, got %+v", incidentItem, first)
	}
	guidance := first[0].Bundle.Guidance
	checkText := strings.Join(guidance.Check, " ")
	doText := strings.Join(guidance.Do, " ")
	allGuidanceLower := strings.ToLower(guidance.Summary + "\n" + checkText + "\n" + doText)
	for _, want := range []string{incidentItem, fifthRun, "lane-5"} {
		if !strings.Contains(guidance.Summary, want) {
			t.Fatalf("summary %q does not name %q", guidance.Summary, want)
		}
	}
	if !strings.Contains(checkText, "tillr drain why "+incidentItem) ||
		!strings.Contains(allGuidanceLower, "previous attempt") ||
		!strings.Contains(allGuidanceLower, "ended") {
		t.Fatalf("guidance does not tell reader how to read why each previous attempt ended: %+v", guidance)
	}

	// 3. Later nodes within attempt 5 and further attempts up to attempt 26 (even >24h later)
	// keep the single open episode without opening duplicate notices.
	now = now.Add(10 * time.Minute)
	appendAllocate(fmt.Sprintf("run-%s-a5-n2", incidentItem), incidentItem, "work", "lane-5", now)
	for attempt := 6; attempt <= 26; attempt++ {
		now = now.Add(2 * time.Hour)
		appendAllocate(fmt.Sprintf("run-%s-a%d-n1", incidentItem, attempt), incidentItem, "plan", "lane-5", now)
	}
	continued := pending()
	if len(continued) != 1 || continued[0].AlertID != first[0].AlertID {
		t.Fatalf("attempts 6..26 should keep one episode open: first=%+v continued=%+v", first, continued)
	}

	// 4. Non-workflow a5, node-reusing a5-n2, and long truncated-slug item IDs also fire,
	// and land.landing == "landed" or terminal item state resolves their episodes.
	longItem := "item-doc-" + strings.Repeat("very-long-slug-segment-", 3) + "tail"
	sum := sha256.Sum256([]byte(longItem))
	digest := hex.EncodeToString(sum[:8])
	keep := 56 - 1 - len(digest)
	longSlug := strings.TrimRight(longItem[:keep], "-") + "-" + digest

	setItemState(s, "item-reused", "in-queue")
	setItemState(s, longItem, "in-queue")
	setItemState(s, "item-already-closed", "closed")

	now = now.Add(time.Minute)
	appendAllocate("run-item-plain-a5", "item-plain", "", "lane-2", now)
	appendAllocate("run-item-reused-a5-n2", "item-reused", "work", "lane-6", now)
	appendAllocate("run-"+longSlug+"-a5-n1", longItem, "plan", "lane-7", now)
	appendAllocate("run-item-already-closed-a5-n1", "item-already-closed", "plan", "lane-8", now)

	multi := pending()
	gotSubjects := make([]string, 0, len(multi))
	for _, n := range multi {
		gotSubjects = append(gotSubjects, n.Bundle.Subject)
	}
	slices.Sort(gotSubjects)
	wantSubjects := []string{incidentItem, longItem, "item-plain", "item-reused"}
	slices.Sort(wantSubjects)
	if !slices.Equal(gotSubjects, wantSubjects) {
		t.Fatalf("open subjects = %v, want %v (closed item must stay suppressed)", gotSubjects, wantSubjects)
	}

	// Landing incidentItem via land.landing clears its episode; closing item-reused and
	// landing longItem in items resolve their episodes with item-closed / item-landed.
	now = now.Add(time.Minute)
	appendLandedTo(s, incidentItem, fmt.Sprintf("run-%s-a26-n1", incidentItem), now)
	setItemState(s, "item-reused", "closed")
	setItemState(s, longItem, "landed")

	afterResolve := pending()
	if len(afterResolve) != 1 || afterResolve[0].Bundle.Subject != "item-plain" {
		t.Fatalf("want only item-plain still open after landing/closing others, got %+v", afterResolve)
	}

	// 5. All-rules evaluation across config/tillr/watch:
	// (a) Five rapid node restarts inside attempt 1 open only drain-repeated-step-dispatches.
	// (b) Five attempts spread 3 hours apart open only drain-item-fifth-attempt.
	allStore, err := store.Open(ctx, filepath.Join(scratchDir, "all-rules.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { allStore.Close() })
	allNow := time.Date(2026, 11, 23, 8, 0, 0, 0, time.UTC)
	allEval := watch.Evaluator{Store: allStore, Rules: rules, Clock: func() time.Time { return allNow }}
	if _, err := allEval.Evaluate(ctx); err != nil {
		t.Fatal(err)
	}

	rapidIDs := []string{
		"run-item-rapid-a1-n3",
		"run-item-rapid-a1-n3-failover",
		"run-item-rapid-a1-n3-002-failover",
		"run-item-rapid-a1-n3-003-failover",
		"run-item-rapid-a1-n3-004-failover",
	}
	for i, rid := range rapidIDs {
		appendAllocateTo(allStore, rid, "item-rapid", "verify", "express-1", allNow.Add(-time.Duration(5-i)*time.Minute))
	}
	for attempt := 1; attempt <= 5; attempt++ {
		if attempt > 1 {
			allNow = allNow.Add(3 * time.Hour)
		}
		appendAllocateTo(allStore, fmt.Sprintf("run-item-spread-a%d-n1", attempt), "item-spread", "plan", "lane-1", allNow)
	}
	// At allNow (+12h), item-rapid's 10m window has aged out while item-spread's 5th attempt just started.
	skipped, err := allEval.Evaluate(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if skipped[ruleID] != 0 || skipped[stepRuleID] != 0 {
		t.Fatalf("expected 0 skipped events, got %+v", skipped)
	}
	spreadNotices, err := allEval.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(spreadNotices) != 1 || spreadNotices[0].Bundle.RuleID != ruleID || spreadNotices[0].Bundle.Subject != "item-spread" {
		t.Fatalf("want only %s for item-spread across config/tillr/watch, got %+v", ruleID, spreadNotices)
	}

	// 6. tillr watch lint passes on the repository with all backend policies.
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
