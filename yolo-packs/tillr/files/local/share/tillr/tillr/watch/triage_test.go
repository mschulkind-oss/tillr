package watch

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestTriageClaimSerializationOpenAndAutoResolved(t *testing.T) {
	register(t, "watch-test.fact", "watch-test.clear")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)

	r := Rule{
		ID:       "triage-claim-rule",
		Scope:    "core",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"watch-test.fact", "watch-test.clear"}},
		When:     When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour, ClearWhen: mustExpr(t, `event.kind == "watch-test.clear"`)},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	evaluateLifecycle(t, &e)

	// Open episode.
	watchFact(t, s, now, `{}`)
	evaluateLifecycle(t, &e)
	var openID string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE state='open'`, &openID)

	// Concurrent ClaimTriage race: exactly one winner.
	const workers = 8
	var wg sync.WaitGroup
	var wins atomic.Int32
	var already atomic.Int32
	start := make(chan struct{})
	for i := 0; i < workers; i++ {
		wg.Add(1)
		runID := fmt.Sprintf("run-triage-%d", i)
		go func() {
			defer wg.Done()
			<-start
			err := e.ClaimTriage(ctx, openID, runID)
			if err == nil {
				wins.Add(1)
				return
			}
			if errors.Is(err, ErrTriageAlreadyClaimed) {
				already.Add(1)
				return
			}
			t.Errorf("unexpected ClaimTriage error: %v", err)
		}()
	}
	close(start)
	wg.Wait()
	if wins.Load() != 1 || already.Load() != workers-1 {
		t.Fatalf("expected 1 win and %d already-claimed, got wins=%d already=%d", workers-1, wins.Load(), already.Load())
	}

	var startedCount int
	lifecycleRead(t, s, `SELECT COUNT(*) FROM events WHERE kind='watch.alert.triage-started' AND object_id='`+openID+`'`, &startedCount)
	if startedCount != 1 {
		t.Fatalf("expected 1 triage-started event, got %d", startedCount)
	}

	// Open a second episode and auto-resolve it before claiming.
	now = now.Add(time.Minute)
	watchFactKind(t, s, now, "watch-test.clear", `{}`)
	evaluateLifecycle(t, &e)

	now = now.Add(time.Minute)
	watchFact(t, s, now, `{}`)
	evaluateLifecycle(t, &e)
	var autoID string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE state='open'`, &autoID)

	now = now.Add(time.Minute)
	watchFactKind(t, s, now, "watch-test.clear", `{}`)
	evaluateLifecycle(t, &e)

	var resolvedState, resolvedSettle string
	lifecycleRead(t, s, `SELECT state, settlement_state FROM alert_episodes WHERE alert_id='`+autoID+`'`, &resolvedState, &resolvedSettle)
	if resolvedState != "resolved" || resolvedSettle != "uninvestigated" {
		t.Fatalf("expected resolved/uninvestigated, got %s/%s", resolvedState, resolvedSettle)
	}

	// Auto-resolved uninvestigated episode is eligible for ClaimTriage; second claim is refused.
	if err := e.ClaimTriage(ctx, autoID, "run-auto-1"); err != nil {
		t.Fatalf("ClaimTriage on auto-resolved episode: %v", err)
	}
	if err := e.ClaimTriage(ctx, autoID, "run-auto-2"); !errors.Is(err, ErrTriageAlreadyClaimed) {
		t.Fatalf("expected ErrTriageAlreadyClaimed on second claim, got %v", err)
	}
}

func TestTriageRefusesExpectedAndRecordsThreeOutcomes(t *testing.T) {
	register(t, "watch-test.fact", "watch-test.clear")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)
	seedItemsTable(t, s)
	watchSQL(t, s, `INSERT INTO items(item_id, state, event_seq) VALUES ('item-1', 'in-queue', 0), ('item-2', 'in-queue', 0)`)

	r := Rule{
		ID:       "triage-record-rule",
		Scope:    "core",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"watch-test.fact", "watch-test.clear"}},
		When:     When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour, ClearWhen: mustExpr(t, `event.kind == "watch-test.clear"`)},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	evaluateLifecycle(t, &e)

	watchFact(t, s, now, `{}`)
	evaluateLifecycle(t, &e)
	var alertID string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE state='open'`, &alertID)

	if err := e.ClaimTriage(ctx, alertID, "run-1"); err != nil {
		t.Fatal(err)
	}

	// Refusal of 'expected' with zero store mutation.
	var seqBefore int64
	lifecycleRead(t, s, `SELECT COALESCE(MAX(seq), 0) FROM events`, &seqBefore)
	err := e.RecordTriage(ctx, alertID, RecordTriageOptions{
		RunID:   "run-1",
		Outcome: "expected",
		Finding: "benign operation",
	})
	if !errors.Is(err, ErrInvalidTriageOutcome) {
		t.Fatalf("expected ErrInvalidTriageOutcome for expected, got %v", err)
	}
	var seqAfter int64
	lifecycleRead(t, s, `SELECT COALESCE(MAX(seq), 0) FROM events`, &seqAfter)
	if seqAfter != seqBefore {
		t.Fatalf("expected zero events appended on refused expected outcome, got %d -> %d", seqBefore, seqAfter)
	}
	var triageState string
	lifecycleRead(t, s, `SELECT triage_state FROM alert_episodes WHERE alert_id='`+alertID+`'`, &triageState)
	if triageState != "triaging" {
		t.Fatalf("expected triage_state to remain triaging, got %q", triageState)
	}

	// 1. Outcome: filed
	if err := e.RecordTriage(ctx, alertID, RecordTriageOptions{
		RunID:       "run-1",
		Outcome:     "filed",
		OutcomeKind: "fix-fault",
		ItemID:      "item-1",
		Finding:     "seq 1 shows fault",
	}); err != nil {
		t.Fatalf("RecordTriage filed: %v", err)
	}
	var sst, itemID, linkKind, tOutcome string
	lifecycleRead(t, s, `SELECT settlement_state, tracking_item_id, item_link_kind, triage_outcome FROM alert_episodes WHERE alert_id='`+alertID+`'`, &sst, &itemID, &linkKind, &tOutcome)
	if sst != "settled" || itemID != "item-1" || linkKind != "filed" || tOutcome != "filed" {
		t.Fatalf("unexpected filed state: sst=%s item=%s linkKind=%s tOutcome=%s", sst, itemID, linkKind, tOutcome)
	}
	// Settled episode must not wake Pending.
	notices, err := e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 0 {
		t.Fatalf("expected 0 pending notices for triage-settled episode, got %d", len(notices))
	}

	// 2. Outcome: linked on an auto-resolved episode (must stay resolved).
	now = now.Add(time.Minute)
	watchFactKind(t, s, now, "watch-test.clear", `{}`)
	evaluateLifecycle(t, &e)
	now = now.Add(time.Minute)
	watchFact(t, s, now, `{}`)
	evaluateLifecycle(t, &e)
	var autoID string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE state='open'`, &autoID)
	now = now.Add(time.Minute)
	watchFactKind(t, s, now, "watch-test.clear", `{}`)
	evaluateLifecycle(t, &e)

	if err := e.ClaimTriage(ctx, autoID, "run-2"); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordTriage(ctx, autoID, RecordTriageOptions{
		RunID:       "run-2",
		Outcome:     "linked",
		OutcomeKind: "fix-rule",
		ItemID:      "item-2",
		Finding:     "matches item-2 predicate gap",
	}); err != nil {
		t.Fatalf("RecordTriage linked: %v", err)
	}
	var epState string
	lifecycleRead(t, s, `SELECT state, settlement_state, triage_state FROM alert_episodes WHERE alert_id='`+autoID+`'`, &epState, &sst, &triageState)
	if epState != "resolved" || sst != "settled" || triageState != "settled" {
		t.Fatalf("expected resolved/settled/settled, got %s/%s/%s", epState, sst, triageState)
	}

	// 3. Outcome: escalated to operator-session on an auto-resolved episode -> wakes Pending with Bundle.Triage!
	now = now.Add(time.Minute)
	watchFact(t, s, now, `{}`)
	evaluateLifecycle(t, &e)
	var escID string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE state='open'`, &escID)
	now = now.Add(time.Minute)
	watchFactKind(t, s, now, "watch-test.clear", `{}`)
	evaluateLifecycle(t, &e)

	if err := e.ClaimTriage(ctx, escID, "run-3"); err != nil {
		t.Fatal(err)
	}
	// While triaging, Pending must not wake.
	notices, err = e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 0 {
		t.Fatalf("expected 0 pending notices while triaging, got %d", len(notices))
	}

	if err := e.RecordTriage(ctx, escID, RecordTriageOptions{
		RunID:        "run-3",
		Outcome:      "escalated",
		EscalateTo:   "operator-session",
		Finding:      "two plausible causes",
		ActionNeeded: "inspect lane-1 logs",
	}); err != nil {
		t.Fatalf("RecordTriage escalated: %v", err)
	}
	notices, err = e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 1 || notices[0].AlertID != escID {
		t.Fatalf("expected 1 escalated notice for %s, got %+v", escID, notices)
	}
	if notices[0].Bundle.Triage == nil || notices[0].Bundle.Triage.RunID != "run-3" || notices[0].Bundle.Triage.ActionNeeded != "inspect lane-1 logs" {
		t.Fatalf("expected Bundle.Triage header on escalated notice, got %+v", notices[0].Bundle.Triage)
	}

	// 4. Outcome: escalated to owner -> does NOT wake Pending for operating session.
	now = now.Add(time.Minute)
	watchFact(t, s, now, `{}`)
	evaluateLifecycle(t, &e)
	var ownerID string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE state='open'`, &ownerID)
	if err := e.ClaimTriage(ctx, ownerID, "run-4"); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordTriage(ctx, ownerID, RecordTriageOptions{
		RunID:      "run-4",
		Outcome:    "escalated",
		EscalateTo: "owner",
		Finding:    "needs policy ruling",
		Question:   "Keep or drop rule?",
		Options:    []string{"keep", "drop"},
	}); err != nil {
		t.Fatalf("RecordTriage owner: %v", err)
	}
	notices, err = e.Pending(ctx, "foreground")
	if err != nil {
		t.Fatal(err)
	}
	for _, n := range notices {
		if n.AlertID == ownerID {
			t.Fatalf("owner escalation %s must not wake operating session Pending", ownerID)
		}
	}
}

func TestTriageBundleHeaderSurvivesTruncation(t *testing.T) {
	largeEvents := make([]observation, 40)
	for i := range largeEvents {
		largeEvents[i] = observation{
			Seq:  int64(i + 1),
			TS:   1700000000000 + int64(i)*1000,
			Kind: fmt.Sprintf("watch-test.fact.%0300d", i),
		}
	}
	b := Bundle{
		RuleID:   "big-rule",
		Subject:  "global",
		Severity: "warning",
		Triage: &TriageHeader{
			RunID:        "run-trunc",
			Outcome:      "escalated",
			EscalatedTo:  "operator-session",
			ItemID:       "item-99",
			Finding:      "important triage finding",
			ActionNeeded: "clear stale lock",
		},
		Guidance: bundleGuidance{
			Summary: "long summary",
			Check:   []string{"check 1", "check 2"},
			Do:      []string{"do 1", "do 2"},
		},
		Events: largeEvents,
	}
	raw, err := b.encode()
	if err != nil {
		t.Fatal(err)
	}
	if len(raw) > bundleBudget {
		t.Fatalf("encoded bundle %d exceeds budget %d", len(raw), bundleBudget)
	}
	var decoded Bundle
	if err := json.Unmarshal(raw, &decoded); err != nil {
		t.Fatal(err)
	}
	if decoded.Omitted.Events == 0 {
		t.Fatalf("expected some events to be omitted")
	}
	if decoded.Triage == nil || decoded.Triage.Finding != "important triage finding" || decoded.Triage.ActionNeeded != "clear stale lock" {
		t.Fatalf("expected TriageHeader to survive truncation intact, got %+v", decoded.Triage)
	}
}

func TestTriageReopenResetsStateAndStatusRecent(t *testing.T) {
	register(t, "watch-test.fact", "watch-test.clear")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)
	seedItemsTable(t, s)
	watchSQL(t, s, `INSERT INTO items(item_id, state, event_seq) VALUES ('item-open', 'in-queue', 0)`)

	r := Rule{
		ID:       "triage-reopen-rule",
		Scope:    "core",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"watch-test.fact", "watch-test.clear"}},
		When:     When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour, ClearWhen: mustExpr(t, `event.kind == "watch-test.clear"`)},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	evaluateLifecycle(t, &e)

	// Open, claim, record filed, and resolve within 5m.
	watchFact(t, s, now, `{}`)
	evaluateLifecycle(t, &e)
	var alertID string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE state='open'`, &alertID)

	now = now.Add(time.Minute)
	if err := e.ClaimTriage(ctx, alertID, "run-r1"); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordTriage(ctx, alertID, RecordTriageOptions{
		RunID:       "run-r1",
		Outcome:     "filed",
		OutcomeKind: "fix-fault",
		ItemID:      "item-open",
		Finding:     "filed item-open",
	}); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Minute)
	watchFactKind(t, s, now, "watch-test.clear", `{}`)
	evaluateLifecycle(t, &e)

	// Status --recent 10m includes the resolved triaged episode; --recent 30s excludes it.
	now = now.Add(time.Minute)
	stRecent, err := e.ReadStatusWithOptions(ctx, func(string) (bool, error) { return false, nil }, StatusOptions{Recent: 10 * time.Minute})
	if err != nil {
		t.Fatal(err)
	}
	if len(stRecent.Alerts) != 1 || stRecent.Alerts[0].ID != alertID {
		t.Fatalf("expected 1 recent alert %s, got %+v", alertID, stRecent.Alerts)
	}
	if stRecent.Alerts[0].Triage == nil || stRecent.Alerts[0].Triage.State != "settled" || stRecent.Alerts[0].Triage.Outcome != "filed" {
		t.Fatalf("expected triage status on alert, got %+v", stRecent.Alerts[0].Triage)
	}
	if stRecent.Summary.Triage.Settled != 1 {
		t.Fatalf("expected Triage.Settled=1, got %+v", stRecent.Summary.Triage)
	}

	stNarrow, err := e.ReadStatusWithOptions(ctx, func(string) (bool, error) { return false, nil }, StatusOptions{Recent: 30 * time.Second})
	if err != nil {
		t.Fatal(err)
	}
	if len(stNarrow.Alerts) != 0 {
		t.Fatalf("expected 0 alerts in 30s window, got %d", len(stNarrow.Alerts))
	}

	// Open a second episode, settle it via triage while still open, then emit a newer fact
	// to trigger reopenSettledOpenItem and verify triage_state resets to untriaged.
	now = now.Add(time.Minute)
	watchFact(t, s, now, `{}`)
	evaluateLifecycle(t, &e)
	var openID string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE state='open'`, &openID)

	now = now.Add(time.Minute)
	if err := e.ClaimTriage(ctx, openID, "run-r2"); err != nil {
		t.Fatal(err)
	}
	if err := e.RecordTriage(ctx, openID, RecordTriageOptions{
		RunID:       "run-r2",
		Outcome:     "linked",
		OutcomeKind: "fix-fault",
		ItemID:      "item-open",
		Finding:     "linked item-open",
	}); err != nil {
		t.Fatal(err)
	}

	now = now.Add(time.Minute)
	watchFact(t, s, now, `{}`)
	evaluateLifecycle(t, &e)
	var reopenedTriageState string
	lifecycleRead(t, s, `SELECT triage_state FROM alert_episodes WHERE alert_id='`+openID+`'`, &reopenedTriageState)
	if reopenedTriageState != "untriaged" {
		t.Fatalf("expected reopened triage_state=untriaged, got %q", reopenedTriageState)
	}
}

func TestRecordTriageFailureAndHelpers(t *testing.T) {
	register(t, "watch-test.fact", "watch-test.clear")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)
	seedItemsTable(t, s)
	watchSQL(t, s, `INSERT INTO items(item_id, state, event_seq) VALUES ('item-fail', 'in-queue', 0)`)

	r := Rule{
		ID:       "triage-fail-rule",
		Scope:    "core",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"watch-test.fact", "watch-test.clear"}},
		When:     When{Pattern: PredicateMatch, GroupBy: "subject", Threshold: 1, Window: time.Hour, ClearWhen: mustExpr(t, `event.kind == "watch-test.clear"`)},
		Delivery: Delivery{Receiver: "triage"},
	}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	evaluateLifecycle(t, &e)

	// Open two episodes: first on s1 (auto-resolved), second on s2 (open).
	watchFact(t, s, now, `{"subject":"s1"}`)
	evaluateLifecycle(t, &e)
	var id1 string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE subject_key='s1'`, &id1)

	now = now.Add(time.Minute)
	watchFactKind(t, s, now, "watch-test.clear", `{"subject":"s1"}`)
	evaluateLifecycle(t, &e)

	now = now.Add(time.Minute)
	watchFact(t, s, now, `{"subject":"s2"}`)
	evaluateLifecycle(t, &e)
	var id2 string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE subject_key='s2'`, &id2)

	// ReadEligibleTriageEpisodes returns both ordered by opened_seq ASC.
	var eligible []EligibleTriageEpisode
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		var err error
		eligible, err = ReadEligibleTriageEpisodes(rd, map[string]bool{"triage-fail-rule": true})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(eligible) != 2 || eligible[0].AlertID != id1 || eligible[1].AlertID != id2 {
		t.Fatalf("expected [%s, %s], got %+v", id1, id2, eligible)
	}
	if eligible[0].State != "resolved" || eligible[1].State != "open" {
		t.Fatalf("expected resolved then open, got %s then %s", eligible[0].State, eligible[1].State)
	}

	// Claim id1 and fail with an attached defect item.
	if err := e.ClaimTriage(ctx, id1, "run-fail-1"); err != nil {
		t.Fatal(err)
	}
	var claims []ActiveTriageClaim
	if err := s.ReadTx(ctx, func(rd *store.Reader) error {
		var err error
		claims, err = ReadActiveTriageClaims(rd)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(claims) != 1 || claims[0].AlertID != id1 || claims[0].ActiveRunID != "run-fail-1" {
		t.Fatalf("unexpected active claims: %+v", claims)
	}

	if err := e.RecordTriageFailure(ctx, id1, TriageFailureOptions{
		RunID:        "run-fail-1",
		Finding:      "runner exited 1",
		ActionNeeded: "inspect triage run-fail-1",
		ItemID:       "item-fail",
		OutcomeKind:  "fix-fault",
	}); err != nil {
		t.Fatalf("RecordTriageFailure with item: %v", err)
	}

	var tState, tOutcome, escTo, trk, active string
	lifecycleRead(t, s, `SELECT triage_state, triage_outcome, escalated_to, tracking_item_id, COALESCE(active_triage_run_id, '') FROM alert_episodes WHERE alert_id='`+id1+`'`,
		&tState, &tOutcome, &escTo, &trk, &active)
	if tState != "escalated" || tOutcome != "triage-failed" || escTo != "operator-session" || trk != "item-fail" || active != "" {
		t.Fatalf("unexpected state after RecordTriageFailure: state=%s outcome=%s esc=%s trk=%s active=%q", tState, tOutcome, escTo, trk, active)
	}

	// Fail id2 without an item and without a run id (closed backend).
	if err := e.RecordTriageFailure(ctx, id2, TriageFailureOptions{
		Finding:      "triage backend pi-scout is closed",
		ActionNeeded: "reopen pi-scout",
	}); err != nil {
		t.Fatalf("RecordTriageFailure without item: %v", err)
	}

	// Both failures wake operator-session with Bundle.Triage.Outcome == "triage-failed".
	notices, err := e.Pending(ctx, "operator-session")
	if err != nil {
		t.Fatal(err)
	}
	if len(notices) != 2 {
		t.Fatalf("expected 2 notices for operator-session, got %d", len(notices))
	}
	for _, n := range notices {
		if n.Bundle.Triage == nil || n.Bundle.Triage.Outcome != "triage-failed" || n.Bundle.Triage.EscalatedTo != "operator-session" {
			t.Fatalf("expected triage-failed header on notice %s, got %+v", n.AlertID, n.Bundle.Triage)
		}
	}
}
