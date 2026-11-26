package main

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"slices"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestShippedRuleReceiversMatchAlertTriageDesign(t *testing.T) {
	rules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}

	wantReceiver := map[string]string{
		// Group A (15 rules): settleable inside tillr.
		"fast-gate-run-slow":               "triage",
		"fast-gate-median-slow":            "triage",
		"gate-repeated-stall":              "triage",
		"land-squash-conflict":             "triage",
		"drain-repeated-item-conflicts":    "triage",
		"drain-repeated-verifier-refusals": "triage",
		"drain-plan-output-unparseable":    "triage",
		"drain-repeated-step-dispatches":   "triage",
		"drain-dispatch-declined":          "triage",
		"drain-item-fault":                 "triage",
		"drain-judge-unreadable":           "triage",
		"drain-store-busy":                 "triage",
		"drain-trunk-fixer-filed":          "triage",
		"drain-landing-reverted":           "triage",
		"run-model-timeout":                "triage",
		// Group B (9 rules): diagnose and file inside tillr, then escalate live action.
		"drain-lane-refused":        "triage",
		"drain-trunk-core-worktree": "triage",
		"drain-revert-not-possible": "triage",
		"drain-heavy-fault":         "triage",
		"trunk-proof-lag":           "triage",
		"trunk-proof-stalled":       "triage",
		"drain-obstacle-hold":       "triage",
		"drain-backend-closed":      "triage",
		"drain-unrequested-start":   "triage",
		// Group C (2 rules): loop liveness.
		"drain-loop-no-pass": "operator-session",
		"drain-loop-fault":   "operator-session",
	}

	seen := map[string]bool{}
	for _, r := range rules {
		seen[r.ID] = true
		want, ok := wantReceiver[r.ID]
		if !ok {
			want = "foreground"
		}
		if r.Delivery.Receiver != want {
			t.Errorf("rule %s receiver = %q, want %q", r.ID, r.Delivery.Receiver, want)
		}
	}
	for id := range wantReceiver {
		if !seen[id] {
			t.Errorf("sorted rule %s missing from tools/tillr-watch/rules", id)
		}
	}

	root := filepath.Clean(filepath.Join("..", ".."))
	policies, err := filepath.Glob(filepath.Join(root, "config", "tillr", "backend-policy*.json"))
	if err != nil || len(policies) == 0 {
		t.Fatalf("glob policies: %v (len=%d)", err, len(policies))
	}
	slices.Sort(policies)
	args := []string{"lint", root}
	for _, p := range policies {
		args = append(args, "--policy", p)
	}
	var out, stderr bytes.Buffer
	if code := run(t.Context(), args, &out, &stderr); code != 0 || stderr.Len() != 0 {
		t.Fatalf("tillr watch lint failed: code=%d stdout=%q stderr=%q", code, out.String(), stderr.String())
	}
}

func TestOperatorSessionWakeSurfaceWithCutoverShippedRules(t *testing.T) {
	allRules, err := watch.LoadRules("rules")
	if err != nil {
		t.Fatal(err)
	}
	pick := func(ids ...string) []watch.Rule {
		var out []watch.Rule
		for _, id := range ids {
			idx := slices.IndexFunc(allRules, func(r watch.Rule) bool { return r.ID == id })
			if idx < 0 {
				t.Fatalf("missing shipped rule %s", id)
			}
			out = append(out, allRules[idx])
		}
		return out
	}
	rules := pick("drain-store-busy", "drain-loop-fault")

	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })

	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	e := watch.Evaluator{Store: s, Rules: rules, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	appendBusy := func(op string) {
		t.Helper()
		body, err := json.Marshal(map[string]any{"op": op, "error": "database is locked"})
		if err != nil {
			t.Fatal(err)
		}
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Append(event.Event{Kind: "drain.store-busy", At: now, Payload: body})
			return err
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 1. A newly opened triage rule wakes tillr-triage and stays quiet for operator-session.
	appendBusy("drain-pass")
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	triageNotices, err := e.Pending(ctx, "tillr-triage")
	if err != nil || len(triageNotices) != 1 || triageNotices[0].Bundle.RuleID != "drain-store-busy" {
		t.Fatalf("want 1 drain-store-busy notice for tillr-triage, got %+v (err=%v)", triageNotices, err)
	}
	if opNotices, err := e.Pending(ctx, "operator-session"); err != nil || len(opNotices) != 0 {
		t.Fatalf("untriaged triage alert must not wake operator-session: %+v (err=%v)", opNotices, err)
	}

	// 2. Settling the triage episode keeps operator-session quiet.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO items(item_id, title, state, updated_at, event_seq) VALUES ('item-fix-busy', 'fix store busy', 'in-queue', 1000, 0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	settledID := triageNotices[0].AlertID
	if err := e.ClaimTriage(ctx, settledID, "run-triage-1"); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordTriage(ctx, settledID, watch.RecordTriageOptions{
		RunID:       "run-triage-1",
		Outcome:     "filed",
		OutcomeKind: "fix-fault",
		ItemID:      "item-fix-busy",
		Finding:     "drain-pass contended with concurrent store writer",
	}); err != nil {
		t.Fatal(err)
	}
	if opNotices, err := e.Pending(ctx, "operator-session"); err != nil || len(opNotices) != 0 {
		t.Fatalf("settled triage alert must not wake operator-session: %+v (err=%v)", opNotices, err)
	}

	// 3. Reopening and escalating a triage episode wakes operator-session with the triage header attached.
	now = now.Add(time.Minute)
	appendBusy("land-commit")
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	triageNotices, err = e.Pending(ctx, "tillr-triage")
	if err != nil || len(triageNotices) != 1 || triageNotices[0].Bundle.RuleID != "drain-store-busy" {
		t.Fatalf("want 1 reopened drain-store-busy triage notice, got %+v (err=%v)", triageNotices, err)
	}
	escalatedID := triageNotices[0].AlertID
	if err := e.ClaimTriage(ctx, escalatedID, "run-triage-2"); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordTriage(ctx, escalatedID, watch.RecordTriageOptions{
		RunID:        "run-triage-2",
		Outcome:      "escalated",
		EscalateTo:   "operator-session",
		Finding:      "external process holds SQLite write lock",
		ActionNeeded: "inspect lsof on .tillr/state.db",
	}); err != nil {
		t.Fatal(err)
	}
	opNotices, err := e.Pending(ctx, "operator-session")
	if err != nil || len(opNotices) != 1 || opNotices[0].AlertID != escalatedID || opNotices[0].Bundle.Triage == nil {
		t.Fatalf("expected escalated alert with triage header for operator-session, got %+v (err=%v)", opNotices, err)
	}

	// 4. Loop-liveness alert wakes operator-session directly and never routes to tillr-triage.
	now = now.Add(time.Minute)
	faultBody, err := json.Marshal(map[string]any{"stop": "fault", "detail": "store corrupted"})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{Kind: "drain.loop-ended", At: now, Payload: faultBody})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if triageAfterLoop, err := e.Pending(ctx, "tillr-triage"); err != nil || len(triageAfterLoop) != 0 {
		t.Fatalf("loop-liveness alert must not route to tillr-triage: %+v (err=%v)", triageAfterLoop, err)
	}
	opAfterLoop, err := e.Pending(ctx, "operator-session")
	if err != nil || len(opAfterLoop) != 2 {
		t.Fatalf("expected escalated + loop-liveness notices for operator-session, got %+v (err=%v)", opAfterLoop, err)
	}
	if opAfterLoop[1].Bundle.RuleID != "drain-loop-fault" {
		t.Fatalf("expected second operator-session notice to be drain-loop-fault, got %+v", opAfterLoop[1])
	}
}
