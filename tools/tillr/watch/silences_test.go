package watch

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func seedSilenceItem(t *testing.T, s *store.Store, itemID, state string) {
	t.Helper()
	seedItemsTable(t, s)
	watchSQL(t, s, `INSERT OR REPLACE INTO items(item_id, state, event_seq) VALUES (?, ?, 1)`, itemID, state)
}

func TestSilenceVariableScopedQuietGapAndOtherBindingWakes(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	s := watchStore(t)
	r := Rule{
		ID:       "pkg-red",
		Scope:    "core",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"watch-test.fact"}},
		When: When{
			Pattern:   PredicateMatch,
			Match:     mustExpr(t, `event.step == "fail"`),
			ClearWhen: mustExpr(t, `event.step == "clear"`),
			GroupBy:   "detail.package",
			Threshold: 1,
			Window:    time.Hour,
		},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now, `{"step":"fail","detail":{"package":"tools/flaky"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := e.Pending(ctx, "foreground")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, err=%v", pending, err)
	}
	alertID := pending[0].AlertID
	if err := e.Shown(ctx, "foreground", pending[0].Seq); err != nil {
		t.Fatal(err)
	}
	if err := e.Seen(ctx, "foreground", pending[0].Seq); err != nil {
		t.Fatal(err)
	}

	until := "1h"
	sil, err := e.Silence(ctx, SilenceOptions{
		AlertID:  alertID,
		By:       "operator-session",
		ItemID:   "item-flaky",
		Reason:   "flaky package under repair",
		Until:    &until,
		Cause:    "known race in flaky package",
		Evidence: "event seq 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if sil.VarsJSON != `{"package":"tools/flaky"}` {
		t.Fatalf("VarsJSON = %q, want {\"package\":\"tools/flaky\"}", sil.VarsJSON)
	}

	// Quiet gap: condition clears, episode 1 resolves.
	now = now.Add(time.Minute)
	watchFact(t, s, now, `{"step":"clear","detail":{"package":"tools/flaky"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var ep1State, ep1Reason string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT state, resolved_reason FROM alert_episodes WHERE alert_id=?`, alertID).Scan(&ep1State, &ep1Reason)
	}); err != nil {
		t.Fatal(err)
	}
	if ep1State != "resolved" || ep1Reason != "condition-cleared" {
		t.Fatalf("ep1 = (%s, %s), want (resolved, condition-cleared)", ep1State, ep1Reason)
	}

	// Condition fires again for silenced package -> opens directly as silenced + settled, wakes nobody.
	now = now.Add(time.Minute)
	watchFact(t, s, now, `{"step":"fail","detail":{"package":"tools/flaky"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if got, err := e.Pending(ctx, "foreground"); err != nil || len(got) != 0 {
		t.Fatalf("silenced re-fire woke Pending: %+v (err=%v)", got, err)
	}
	var ep2State, ep2Settle, ep2Outcome, ep2SilID string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT state, settlement_state, outcome, silence_id FROM alert_episodes WHERE rule_id='pkg-red' AND episode_num=2`).
			Scan(&ep2State, &ep2Settle, &ep2Outcome, &ep2SilID)
	}); err != nil {
		t.Fatal(err)
	}
	if ep2State != "silenced" || ep2Settle != "settled" || ep2Outcome != "silence" || ep2SilID != sil.SilenceID {
		t.Fatalf("ep2 = (%s, %s, %s, %s)", ep2State, ep2Settle, ep2Outcome, ep2SilID)
	}

	// Another binding of the same rule opens as open and wakes Pending.
	now = now.Add(time.Minute)
	watchFact(t, s, now, `{"step":"fail","detail":{"package":"tools/other"}}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	got, err := e.Pending(ctx, "foreground")
	if err != nil || len(got) != 1 || got[0].Bundle.Subject != "tools/other" {
		t.Fatalf("other binding pending = %+v (err=%v)", got, err)
	}
}

func TestSilenceCompositePartialAndGlobalRules(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	s := watchStore(t)
	compRule := Rule{
		ID:       "comp-rule",
		Scope:    "core",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"watch-test.fact"}},
		When: When{
			Pattern:     PredicateMatch,
			GroupByList: []string{"lane_id", "reason"},
			Threshold:   1,
			Window:      time.Hour,
		},
		Delivery: Delivery{Receiver: "foreground"},
	}
	globalRule := Rule{
		ID:       "global-rule",
		Scope:    "core",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"watch-test.fact"}},
		When: When{
			Pattern:   PredicateMatch,
			Match:     mustExpr(t, `event.step == "global-fail"`),
			GroupBy:   "global",
			Threshold: 1,
			Window:    time.Hour,
		},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Store: s, Rules: []Rule{compRule, globalRule}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}

	until := "1h"
	// Missing --var on parameterized rule -> ErrInvalidSilenceBinding.
	if _, err := e.Silence(ctx, SilenceOptions{
		RuleID:   "comp-rule",
		By:       "operator",
		ItemID:   "item-1",
		Reason:   "partial",
		Until:    &until,
		Cause:    "known",
		Evidence: "seq 1",
	}); !errors.Is(err, ErrInvalidSilenceBinding) {
		t.Fatalf("missing var err = %v, want ErrInvalidSilenceBinding", err)
	}
	// Unknown --var on parameterized rule -> ErrInvalidSilenceBinding.
	if _, err := e.Silence(ctx, SilenceOptions{
		RuleID:   "comp-rule",
		Vars:     map[string]string{"bogus": "x"},
		By:       "operator",
		ItemID:   "item-1",
		Reason:   "partial",
		Until:    &until,
		Cause:    "known",
		Evidence: "seq 1",
	}); !errors.Is(err, ErrInvalidSilenceBinding) {
		t.Fatalf("unknown var err = %v, want ErrInvalidSilenceBinding", err)
	}
	// Passing --var on global rule -> ErrInvalidSilenceBinding.
	if _, err := e.Silence(ctx, SilenceOptions{
		RuleID:   "global-rule",
		Vars:     map[string]string{"lane_id": "lane-1"},
		By:       "operator",
		ItemID:   "item-1",
		Reason:   "global",
		Until:    &until,
		Cause:    "known",
		Evidence: "seq 1",
	}); !errors.Is(err, ErrInvalidSilenceBinding) {
		t.Fatalf("global with var err = %v, want ErrInvalidSilenceBinding", err)
	}

	// Partial binding on composite rule silences any reason for lane-1.
	if _, err := e.Silence(ctx, SilenceOptions{
		RuleID:   "comp-rule",
		Vars:     map[string]string{"lane_id": "lane-1"},
		By:       "operator",
		ItemID:   "item-1",
		Reason:   "lane-1 maintenance",
		Until:    &until,
		Cause:    "maintenance",
		Evidence: "seq 1",
	}); err != nil {
		t.Fatal(err)
	}
	// Global rule silenced with vars_json = "{}".
	gSil, err := e.Silence(ctx, SilenceOptions{
		RuleID:   "global-rule",
		By:       "operator",
		ItemID:   "item-2",
		Reason:   "global maintenance",
		Until:    &until,
		Cause:    "maintenance",
		Evidence: "seq 1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if gSil.VarsJSON != "{}" {
		t.Fatalf("global VarsJSON = %q, want {}", gSil.VarsJSON)
	}

	watchFact(t, s, now, `{"lane_id":"lane-1","reason":"busy"}`)
	watchFact(t, s, now, `{"lane_id":"lane-1","reason":"timeout"}`)
	watchFact(t, s, now, `{"lane_id":"lane-2","reason":"busy","step":"global-fail"}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	// Only comp-rule for lane-2/busy should wake Pending; lane-1/* and global-rule are silenced.
	if len(pending) != 1 || pending[0].Bundle.RuleID != "comp-rule" || pending[0].Bundle.Subject != "lane-2/busy" {
		t.Fatalf("pending = %+v, want only comp-rule lane-2/busy", pending)
	}
}

func TestSilencePreconditionsAndItemVerification(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	s := watchStore(t)
	seedSilenceItem(t, s, "item-open", "in-queue")
	seedSilenceItem(t, s, "item-closed", "closed")
	seedSilenceItem(t, s, "item-landed", "landed")

	r := Rule{
		ID:       "r1",
		Scope:    "core",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"watch-test.fact"}},
		When:     When{Pattern: PredicateMatch, GroupBy: "lane_id", Threshold: 1, Window: time.Hour},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	watchFact(t, s, now, `{"lane_id":"lane-1"}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	pending, err := e.Pending(ctx, "foreground")
	if err != nil || len(pending) != 1 {
		t.Fatalf("pending = %+v, err=%v", pending, err)
	}
	id := pending[0].AlertID
	until := "1h"

	// Missing investigation -> ErrInvestigationRequired.
	if _, err := e.Silence(ctx, SilenceOptions{
		AlertID: id, By: "operator", ItemID: "item-open", Reason: "r", Until: &until,
	}); !errors.Is(err, ErrInvestigationRequired) {
		t.Fatalf("missing investigation err = %v, want ErrInvestigationRequired", err)
	}
	// Triage actor refused.
	if _, err := e.Silence(ctx, SilenceOptions{
		AlertID: id, By: "tillr-triage", ItemID: "item-open", Reason: "r", Until: &until, Cause: "c", Evidence: "e",
	}); err == nil {
		t.Fatal("expected tillr-triage actor to be refused")
	}
	// Missing / closed / landed item refused.
	for _, badItem := range []string{"item-missing", "item-closed", "item-landed"} {
		if _, err := e.Silence(ctx, SilenceOptions{
			AlertID: id, By: "operator", ItemID: badItem, Reason: "r", Until: &until, Cause: "c", Evidence: "e",
		}); err == nil {
			t.Fatalf("expected item %s to be refused", badItem)
		}
	}
	// --until-change refused without fingerprint.
	if _, err := e.Silence(ctx, SilenceOptions{
		AlertID: id, By: "operator", ItemID: "item-open", Reason: "r", UntilChange: true, Cause: "c", Evidence: "e",
	}); err == nil {
		t.Fatal("expected --until-change without fingerprint to be refused")
	}

	// Valid silence succeeds, duplicate refused.
	if _, err := e.Silence(ctx, SilenceOptions{
		AlertID: id, By: "operator", ItemID: "item-open", Reason: "r", Until: &until, Cause: "c", Evidence: "e",
	}); err != nil {
		t.Fatal(err)
	}
	if _, err := e.Silence(ctx, SilenceOptions{
		RuleID: "r1", Vars: map[string]string{"lane_id": "lane-1"}, By: "operator", ItemID: "item-open", Reason: "r", Until: &until, Cause: "c", Evidence: "e",
	}); err == nil {
		t.Fatal("expected duplicate active silence to be refused")
	}
}

func TestSilenceAutoLiftOnItemLandedAndClosedAndStandaloneSource(t *testing.T) {
	ctx := context.Background()
	now := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)
	s := watchStore(t)
	seedSilenceItem(t, s, "item-open", "in-queue")

	r := Rule{
		ID:       "r1",
		Scope:    "core",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"watch-test.fact"}},
		When: When{
			Pattern:   PredicateMatch,
			Match:     mustExpr(t, `event.step == "fail"`),
			ClearWhen: mustExpr(t, `event.step == "clear"`),
			GroupBy:   "lane_id",
			Threshold: 1,
			Window:    time.Hour,
		},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	until := "2h"
	sil, err := e.Silence(ctx, SilenceOptions{
		RuleID: "r1", Vars: map[string]string{"lane_id": "lane-1"},
		By: "operator", ItemID: "item-open", Reason: "fixing", Until: &until, Cause: "known", Evidence: "seq 1",
	})
	if err != nil {
		t.Fatal(err)
	}

	// Land the item while no episode is firing, then fire in a later pass -> opens with cause="silence-item-landed".
	now = now.Add(time.Minute)
	seedSilenceItem(t, s, "item-open", "landed")
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	lifted, err := e.ListSilences(ctx, true)
	if err != nil || len(lifted) != 1 || lifted[0].LiftedReason != "item-landed" || lifted[0].LiftedBy != "evaluator" {
		t.Fatalf("lifted silences = %+v (err=%v)", lifted, err)
	}
	var liftedEvCount int
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT COUNT(*) FROM events WHERE kind='watch.silence.lifted' AND object_id=?`, sil.SilenceID).Scan(&liftedEvCount)
	}); err != nil {
		t.Fatal(err)
	}
	if liftedEvCount != 1 {
		t.Fatalf("watch.silence.lifted count = %d, want 1", liftedEvCount)
	}

	now = now.Add(time.Minute)
	watchFact(t, s, now, `{"step":"fail","lane_id":"lane-1"}`)
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	var openCause string
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT json_extract(payload, '$.cause') FROM events WHERE kind='watch.alert.opened' ORDER BY seq DESC LIMIT 1`).Scan(&openCause)
	}); err != nil {
		t.Fatal(err)
	}
	if openCause != "silence-item-landed" {
		t.Fatalf("opened cause = %q, want silence-item-landed", openCause)
	}

	// Standalone ObservationSource without items accepts non-empty external ref and supports ListSilences + LiftSilence.
	src := &memoryObservationSource{projections: map[string]any{}}
	rSrc := r
	rSrc.Scope = "*"
	eSrc := Evaluator{Source: src, Rules: []Rule{rSrc}, Clock: func() time.Time { return now }}
	if _, err := eSrc.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	src.append("watch-test.fact", now, map[string]any{"step": "fail", "lane_id": "lane-2"})
	if _, err := eSrc.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	srcPending, err := eSrc.Pending(ctx, "foreground")
	if err != nil || len(srcPending) != 1 {
		t.Fatalf("srcPending = %+v, err=%v", srcPending, err)
	}
	srcSil, err := eSrc.Silence(ctx, SilenceOptions{
		AlertID: srcPending[0].AlertID, By: "operator", ItemID: "EXT-123", Reason: "external", Until: &until, Cause: "known", Evidence: "log",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := eSrc.LiftSilence(ctx, srcSil.SilenceID, "operator", "done early"); err != nil {
		t.Fatal(err)
	}
	srcPendingAfter, err := eSrc.Pending(ctx, "foreground")
	if err != nil || len(srcPendingAfter) != 1 || srcPendingAfter[0].AlertID != srcPending[0].AlertID {
		t.Fatalf("expected reopened alert after LiftSilence, got %+v (err=%v)", srcPendingAfter, err)
	}
}
