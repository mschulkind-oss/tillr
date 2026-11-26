package watch

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func watchFactKind(t *testing.T, s *store.Store, at time.Time, kind, payload string) int64 {
	t.Helper()
	var seq int64
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var err error
		seq, err = tx.Append(event.Event{Kind: kind, At: at, Payload: []byte(payload)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	return seq
}

func seedItemsTable(t *testing.T, s *store.Store) {
	t.Helper()
	watchSQL(t, s, `CREATE TABLE IF NOT EXISTS items(item_id TEXT PRIMARY KEY, state TEXT, seat TEXT, waiting_on TEXT, event_seq INTEGER NOT NULL DEFAULT 0)`)
}

func TestInvestigateValidationStatesAndRefusals(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)
	seedItemsTable(t, s)
	watchSQL(t, s, `INSERT INTO items(item_id, state, event_seq) VALUES ('item-open', 'in-queue', 0)`)

	r := Rule{
		ID:       "inv-rule",
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
	var alertID string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE state='open'`, &alertID)

	// Refuses blank/invalid fields.
	for _, tc := range []struct {
		name, by, cause, ev string
	}{
		{"blank by", " ", "bad cache", "seq 1"},
		{"blank cause", "op", " ", "seq 1"},
		{"unknown cause", "op", " UnKnOwN ", "seq 1"},
		{"blank evidence", "op", "bad cache", " \t "},
	} {
		if err := e.Investigate(ctx, alertID, tc.by, tc.cause, tc.ev); err == nil {
			t.Fatalf("%s: expected error, got nil", tc.name)
		}
	}

	// Ack moves settlement_state from uninvestigated -> investigating.
	if err := e.Ack(ctx, alertID, "op", nil); err != nil {
		t.Fatalf("Ack: %v", err)
	}
	var st, sst string
	lifecycleRead(t, s, `SELECT state, settlement_state FROM alert_episodes WHERE alert_id='`+alertID+`'`, &st, &sst)
	if st != "acknowledged" || sst != "investigating" {
		t.Fatalf("after Ack: state=%q settlement_state=%q, want acknowledged/investigating", st, sst)
	}

	// Auto-resolution leaves settlement_state untouched (still investigating).
	now = now.Add(time.Minute)
	register(t, "watch-test.clear")
	watchFactKind(t, s, now, "watch-test.clear", `{}`)
	evaluateLifecycle(t, &e)
	lifecycleRead(t, s, `SELECT state, settlement_state FROM alert_episodes WHERE alert_id='`+alertID+`'`, &st, &sst)
	if st != "resolved" || sst != "investigating" {
		t.Fatalf("after auto-resolve: state=%q settlement_state=%q, want resolved/investigating", st, sst)
	}

	// Investigate works on resolved episodes.
	if err := e.Investigate(ctx, alertID, "op", "stale cache key", "event seq 2 showed miss"); err != nil {
		t.Fatalf("Investigate on resolved episode: %v", err)
	}
	var invBy, invCause, invEv string
	lifecycleRead(t, s, `SELECT settlement_state, investigated_by, investigation_cause, investigation_evidence FROM alert_episodes WHERE alert_id='`+alertID+`'`, &sst, &invBy, &invCause, &invEv)
	if sst != "investigated" || invBy != "op" || invCause != "stale cache key" || invEv != "event seq 2 showed miss" {
		t.Fatalf("after Investigate: sst=%q by=%q cause=%q ev=%q", sst, invBy, invCause, invEv)
	}

	// Settle works on resolved+investigated episode; afterwards Investigate is refused.
	if err := e.Settle(ctx, alertID, SettleOptions{By: "op", Outcome: "fix-fault", ItemID: "item-open", LinkKind: "filed"}); err != nil {
		t.Fatalf("Settle on resolved episode: %v", err)
	}
	if err := e.Investigate(ctx, alertID, "op", "another", "ev"); err == nil {
		t.Fatal("expected Investigate on settled episode to fail")
	}
}

func TestSettleValidationItemVerificationAndIdempotency(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)
	seedItemsTable(t, s)

	r := Rule{
		ID:       "settle-val",
		Scope:    "core",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"watch-test.fact"}},
		When:     When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	evaluateLifecycle(t, &e)

	obsSeq := watchFact(t, s, now, `{}`)
	evaluateLifecycle(t, &e)
	var alertID string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE state='open'`, &alertID)

	// Seed items: open, closed, stale-finished, newly-finished.
	watchSQL(t, s, `INSERT INTO items(item_id, state, event_seq) VALUES
		('item-open', 'in-queue', 0),
		('item-closed', 'closed', 999999),
		('item-stale-landed', 'landed', ?),
		('item-fresh-landed', 'landed', ?)`, obsSeq, obsSeq+10)

	// Uninvestigated settle returns ErrInvestigationRequired and mutates nothing.
	err := e.Settle(ctx, alertID, SettleOptions{By: "op", Outcome: "fix-fault", ItemID: "item-open"})
	if !errors.Is(err, ErrInvestigationRequired) {
		t.Fatalf("uninvestigated Settle err = %v, want ErrInvestigationRequired", err)
	}
	var sst string
	lifecycleRead(t, s, `SELECT settlement_state FROM alert_episodes WHERE alert_id='`+alertID+`'`, &sst)
	if sst != "uninvestigated" {
		t.Fatalf("settlement_state mutated on ErrInvestigationRequired: %q", sst)
	}

	// Refuses invalid outcome ('expected', 'silence', ''), invalid link_kind, missing/closed/stale-finished items.
	if err := e.Investigate(ctx, alertID, "op", "root cause", "log line 42"); err != nil {
		t.Fatalf("Investigate: %v", err)
	}
	for _, bad := range []struct {
		name string
		opts SettleOptions
		want string
	}{
		{"expected outcome", SettleOptions{By: "op", Outcome: "expected", ItemID: "item-open"}, "fix-rule or fix-fault"},
		{"silence outcome", SettleOptions{By: "op", Outcome: "silence", ItemID: "item-open"}, "fix-rule or fix-fault"},
		{"bad link kind", SettleOptions{By: "op", Outcome: "fix-rule", ItemID: "item-open", LinkKind: "other"}, "filed or linked"},
		{"missing item", SettleOptions{By: "op", Outcome: "fix-fault", ItemID: "item-missing"}, "not found"},
		{"closed item", SettleOptions{By: "op", Outcome: "fix-fault", ItemID: "item-closed"}, "closed"},
		{"stale finished item", SettleOptions{By: "op", Outcome: "fix-fault", ItemID: "item-stale-landed"}, "file a new item"},
	} {
		err := e.Settle(ctx, alertID, bad.opts)
		if err == nil || !strings.Contains(err.Error(), bad.want) {
			t.Fatalf("%s: err = %v, want substring %q", bad.name, err, bad.want)
		}
	}

	// Newly-finished item (seq > last_observed_seq) succeeds.
	if err := e.Settle(ctx, alertID, SettleOptions{By: "op", Outcome: "fix-rule", ItemID: "item-fresh-landed", LinkKind: "linked"}); err != nil {
		t.Fatalf("Settle with newly-finished item: %v", err)
	}

	// Idempotent repeat with same (by, outcome, item) succeeds without extra event.
	if err := e.Settle(ctx, alertID, SettleOptions{By: "op", Outcome: "fix-rule", ItemID: "item-fresh-landed"}); err != nil {
		t.Fatalf("idempotent Settle: %v", err)
	}
	var settledEvCount int
	lifecycleRead(t, s, `SELECT COUNT(*) FROM events WHERE kind='watch.alert.settled' AND object_id='`+alertID+`'`, &settledEvCount)
	if settledEvCount != 1 {
		t.Fatalf("settled event count = %d, want 1", settledEvCount)
	}

	// Conflicting settle fails naming who settled it and how.
	err = e.Settle(ctx, alertID, SettleOptions{By: "other", Outcome: "fix-fault", ItemID: "item-open"})
	if err == nil || !strings.Contains(err.Error(), "op") || !strings.Contains(err.Error(), "fix-rule") || !strings.Contains(err.Error(), "item-fresh-landed") {
		t.Fatalf("conflicting Settle err = %v, want actor/outcome/item", err)
	}
}

func TestSettleSingleInstanceWatermarkAndReopenOpenVsTerminalItem(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)
	seedItemsTable(t, s)
	watchSQL(t, s, `INSERT INTO items(item_id, state, event_seq) VALUES ('item-open', 'in-queue', 0), ('item-done', 'in-queue', 0)`)

	r := Rule{
		ID:       "reopen-rule",
		Scope:    "core",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"watch-test.fact"}},
		When:     When{Pattern: PredicateMatch, GroupBy: "subject", Threshold: 1, Window: time.Hour},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	evaluateLifecycle(t, &e)

	watchFact(t, s, now, `{"subject":"s-open"}`)
	watchFact(t, s, now, `{"subject":"s-done"}`)
	evaluateLifecycle(t, &e)

	var idOpen, idDone string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE subject_key='s-open'`, &idOpen)
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE subject_key='s-done'`, &idDone)

	// Inline cause & evidence on Settle.
	if err := e.Settle(ctx, idOpen, SettleOptions{By: "op", Outcome: "fix-fault", ItemID: "item-open", Cause: "c1", Evidence: "e1"}); err != nil {
		t.Fatalf("Settle open: %v", err)
	}
	if err := e.Settle(ctx, idDone, SettleOptions{By: "op", Outcome: "fix-fault", ItemID: "item-done", Cause: "c2", Evidence: "e2"}); err != nil {
		t.Fatalf("Settle done: %v", err)
	}

	// Re-reading same settled window does not reopen.
	evaluateLifecycle(t, &e)
	var reopenCount int
	lifecycleRead(t, s, `SELECT COUNT(*) FROM events WHERE kind='watch.alert.reopened'`, &reopenCount)
	if reopenCount != 0 {
		t.Fatalf("expected 0 reopens on same settled window, got %d", reopenCount)
	}

	// item-done lands, then new instances arrive for both subjects.
	watchSQL(t, s, `UPDATE items SET state='landed', event_seq=999 WHERE item_id='item-done'`)
	now = now.Add(time.Minute)
	watchFact(t, s, now, `{"subject":"s-open"}`)
	watchFact(t, s, now, `{"subject":"s-done"}`)
	evaluateLifecycle(t, &e)

	// s-open: item-open is still open -> keeps tracking_item_id, increments reopen_count_for_item, resets settlement_state.
	var stOpen, sstOpen, trkOpen string
	var reopensOpen int64
	lifecycleRead(t, s, `SELECT state, settlement_state, tracking_item_id, reopen_count_for_item FROM alert_episodes WHERE alert_id='`+idOpen+`'`, &stOpen, &sstOpen, &trkOpen, &reopensOpen)
	if stOpen != "open" || sstOpen != "uninvestigated" || trkOpen != "item-open" || reopensOpen != 1 {
		t.Fatalf("s-open after reopen: state=%q sst=%q trk=%q reopens=%d", stOpen, sstOpen, trkOpen, reopensOpen)
	}

	// s-done: item-done landed -> reopens with cause recurred-after-item-landed, clears tracking_item_id.
	var stDone, sstDone string
	var trkDone sql.NullString
	lifecycleRead(t, s, `SELECT state, settlement_state, tracking_item_id FROM alert_episodes WHERE alert_id='`+idDone+`'`, &stDone, &sstDone, &trkDone)
	if stDone != "open" || sstDone != "uninvestigated" || trkDone.Valid {
		t.Fatalf("s-done after reopen: state=%q sst=%q trk=%+v", stDone, sstDone, trkDone)
	}
	var reopenCause string
	lifecycleRead(t, s, `SELECT json_extract(payload,'$.cause') FROM events WHERE kind='watch.alert.reopened' AND object_id='`+idDone+`'`, &reopenCause)
	if reopenCause != "recurred-after-item-landed" {
		t.Fatalf("s-done reopen cause = %q, want recurred-after-item-landed", reopenCause)
	}
}

func TestAutoResolutionLeavesSettlementUnsettledAndMigrationMarksPreExistingResolved(t *testing.T) {
	register(t, "watch-test.fact")
	register(t, "watch-test.clear")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)

	// Create a legacy store with alert_episodes lacking settlement columns.
	path := filepath.Join(t.TempDir(), "legacy.sqlite3")
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`
		CREATE TABLE meta (key TEXT PRIMARY KEY, value TEXT NOT NULL);
		CREATE TABLE schema_migrations (version INTEGER PRIMARY KEY, applied_at INTEGER NOT NULL);
		INSERT INTO schema_migrations(version, applied_at) VALUES (6, 1000);
		CREATE TABLE events (
			seq INTEGER PRIMARY KEY AUTOINCREMENT, ts INTEGER NOT NULL, kind TEXT NOT NULL,
			seat TEXT, object_type TEXT, object_id TEXT, revision INTEGER, payload TEXT NOT NULL
		);
		CREATE TABLE alert_episodes (
			alert_id TEXT PRIMARY KEY, rule_id TEXT NOT NULL, subject_key TEXT NOT NULL,
			episode_num INTEGER NOT NULL, state TEXT NOT NULL, severity TEXT NOT NULL,
			condition_fingerprint TEXT NOT NULL, opened_seq INTEGER NOT NULL, opened_at INTEGER NOT NULL,
			last_observed_seq INTEGER NOT NULL, last_observed_at INTEGER NOT NULL,
			observation_count INTEGER NOT NULL DEFAULT 1, acked_by TEXT, acked_note TEXT, acked_at INTEGER,
			silence_until_ms INTEGER, silence_on_fingerprint TEXT, active_triage_run_id TEXT,
			resolved_seq INTEGER, resolved_at INTEGER, resolved_reason TEXT, bundle_json TEXT NOT NULL
		);
		INSERT INTO alert_episodes(alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, resolved_seq, resolved_at, resolved_reason, bundle_json)
		VALUES
			('alrt-old-resolved', 'r1', 's1', 1, 'resolved', 'warning', 'fp1', 1, 1000, 1, 1000, 2, 2000, 'condition-cleared', '{}'),
			('alrt-old-open', 'r1', 's2', 1, 'open', 'warning', 'fp2', 3, 3000, 3, 3000, NULL, NULL, NULL, '{}');
	`); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	s, err := store.OpenMigrated(ctx, path)
	if err != nil {
		t.Fatalf("OpenMigrated: %v", err)
	}
	defer s.Close()

	var oldResSST, oldOpenSST string
	lifecycleRead(t, s, `SELECT settlement_state FROM alert_episodes WHERE alert_id='alrt-old-resolved'`, &oldResSST)
	lifecycleRead(t, s, `SELECT settlement_state FROM alert_episodes WHERE alert_id='alrt-old-open'`, &oldOpenSST)
	if oldResSST != "settled" || oldOpenSST != "uninvestigated" {
		t.Fatalf("migration backfill: old-resolved=%q old-open=%q, want settled/uninvestigated", oldResSST, oldOpenSST)
	}

	// Newly auto-resolved episode stays uninvestigated.
	r := Rule{
		ID:       "auto-res",
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
	now = now.Add(time.Minute)
	watchFactKind(t, s, now, "watch-test.clear", `{}`)
	evaluateLifecycle(t, &e)

	var newResState, newResSST string
	lifecycleRead(t, s, `SELECT state, settlement_state FROM alert_episodes WHERE rule_id='auto-res'`, &newResState, &newResSST)
	if newResState != "resolved" || newResSST != "uninvestigated" {
		t.Fatalf("newly auto-resolved: state=%q sst=%q, want resolved/uninvestigated", newResState, newResSST)
	}
}

func TestSourceStandaloneSettleAndReopen(t *testing.T) {
	ctx := context.Background()
	curNow := time.Unix(1700000000, 0)
	src := &memoryObservationSource{projections: map[string]any{}}
	r := Rule{
		ID:       "src-settle",
		Scope:    "*",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"gate.done"}},
		When:     When{Pattern: PredicateMatch, GroupBy: "detail.check", Match: mustExpr(t, `event.kind == "gate.done"`), Threshold: 1, Window: time.Hour},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Source: src, Rules: []Rule{r}, Clock: func() time.Time { return curNow }}
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	curNow = curNow.Add(time.Minute)
	src.append("gate.done", curNow, map[string]any{"detail": map[string]any{"check": "lint"}})
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	notices, err := e.Pending(ctx, "foreground")
	if err != nil || len(notices) != 1 {
		t.Fatalf("Pending = %+v (%v)", notices, err)
	}
	alertID := notices[0].AlertID

	// Standalone Source without items/charter projections accepts external item ref (D-AW10).
	if err := e.Settle(ctx, alertID, SettleOptions{By: "op", Outcome: "fix-fault", ItemID: "EXT-123", Cause: "flaky lint", Evidence: "seq 1"}); err != nil {
		t.Fatalf("standalone Source Settle: %v", err)
	}
	// Re-reading same window does not reopen.
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if ep := e.mem.findAlertLocked(alertID); ep.state != "open" || ep.settlementState != "settled" {
		t.Fatalf("unexpected state after same window: state=%q sst=%q", ep.state, ep.settlementState)
	}
	// New instance reopens.
	curNow = curNow.Add(time.Minute)
	src.append("gate.done", curNow, map[string]any{"detail": map[string]any{"check": "lint"}})
	if _, err := e.EvaluateRules(ctx); err != nil {
		t.Fatal(err)
	}
	if ep := e.mem.findAlertLocked(alertID); ep.settlementState != "uninvestigated" {
		t.Fatalf("expected uninvestigated after reopen, got %q", ep.settlementState)
	}
}

func TestSettleLandedItemRelativeToAlertOpened(t *testing.T) {
	register(t, "watch-test.fact")
	ctx := context.Background()
	now := time.Unix(1700000000, 0)
	s := watchStore(t)
	seedItemsTable(t, s)

	r := Rule{
		ID:       "settle-landed-seq",
		Scope:    "core",
		Enabled:  true,
		Severity: "warning",
		Reads:    Reads{Events: []string{"watch-test.fact"}},
		When:     When{Pattern: PredicateMatch, GroupBy: "global", Threshold: 1, Window: time.Hour},
		Delivery: Delivery{Receiver: "foreground"},
	}
	e := Evaluator{Store: s, Rules: []Rule{r}, Clock: func() time.Time { return now }}
	evaluateLifecycle(t, &e)

	// Open the alert at openSeq, then observe a later fact so last_observed_seq > itemSeq > opened_seq.
	openSeq := watchFact(t, s, now, `{}`)
	evaluateLifecycle(t, &e)

	var alertID string
	lifecycleRead(t, s, `SELECT alert_id FROM alert_episodes WHERE state='open'`, &alertID)

	now = now.Add(time.Minute)
	laterSeq := watchFact(t, s, now, `{}`)
	if laterSeq <= openSeq+1 {
		// Ensure there is room between openSeq and laterSeq, or bump last_observed_seq directly.
		laterSeq = openSeq + 10
	}
	evaluateLifecycle(t, &e)
	watchSQL(t, s, `UPDATE alert_episodes SET last_observed_seq=? WHERE alert_id=?`, openSeq+20, alertID)

	watchSQL(t, s, `INSERT INTO items(item_id, state, event_seq) VALUES
		('item-before-open', 'landed', ?),
		('item-after-open', 'landed', ?)`, openSeq-1, openSeq+10)

	if err := e.Investigate(ctx, alertID, "op", "root cause", "observed in log"); err != nil {
		t.Fatalf("Investigate: %v", err)
	}

	// Refuses an item that landed before the alert opened, with a message that says why.
	err := e.Settle(ctx, alertID, SettleOptions{By: "op", Outcome: "fix-fault", ItemID: "item-before-open"})
	if err == nil || !strings.Contains(err.Error(), "before alert opened") {
		t.Fatalf("Settle with item landed before alert opened: err = %v, want substring %q", err, "before alert opened")
	}

	// Accepts an item that landed after the alert opened (even when seq <= last_observed_seq) and records it as the fix.
	if err := e.Settle(ctx, alertID, SettleOptions{By: "op", Outcome: "fix-fault", ItemID: "item-after-open", LinkKind: "linked"}); err != nil {
		t.Fatalf("Settle with item landed after alert opened: %v", err)
	}

	var sst, outcome, trkID string
	lifecycleRead(t, s, `SELECT settlement_state, outcome, tracking_item_id FROM alert_episodes WHERE alert_id='`+alertID+`'`, &sst, &outcome, &trkID)
	if sst != "settled" || outcome != "fix-fault" || trkID != "item-after-open" {
		t.Fatalf("settled episode = (%q, %q, %q), want (settled, fix-fault, item-after-open)", sst, outcome, trkID)
	}
}
