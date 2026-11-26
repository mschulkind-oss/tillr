package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func TestAlertItemLinksTableAndBackfill(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy.sqlite3")

	// Create a store first, then drop alert_item_links and seed legacy episodes & events
	// to simulate an existing store from before Slice 6.
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	t0 := time.Date(2026, 11, 25, 10, 0, 0, 0, time.UTC)
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`DROP TABLE alert_item_links`); err != nil {
			return err
		}
		// 1. Episode with tracked event + later fix-fault settle event on same item.
		if _, err := tx.Exec(`INSERT INTO alert_episodes (alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json, tracking_item_id, settlement_state, outcome)
			VALUES ('alrt-1', 'r1', 's1', 1, 'open', 'high', 'fp1', 1, ?, 1, ?, '{}', 'item-1', 'settled', 'fix-fault')`,
			t0.UnixMilli(), t0.UnixMilli()); err != nil {
			return err
		}
		// 2. Episode with tracking_item_id only (no event).
		if _, err := tx.Exec(`INSERT INTO alert_episodes (alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json, tracking_item_id)
			VALUES ('alrt-2', 'r2', 's2', 1, 'resolved', 'low', 'fp2', 2, ?, 2, ?, '{}', 'item-2')`,
			t0.Add(time.Minute).UnixMilli(), t0.Add(time.Minute).UnixMilli()); err != nil {
			return err
		}
		// 3. Episode with fix-fault followed by silence event on same item (fix-fault must win).
		if _, err := tx.Exec(`INSERT INTO alert_episodes (alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json)
			VALUES ('alrt-3', 'r3', 's3', 1, 'silenced', 'medium', 'fp3', 3, ?, 3, ?, '{}')`,
			t0.Add(2*time.Minute).UnixMilli(), t0.Add(2*time.Minute).UnixMilli()); err != nil {
			return err
		}

		appendEv := func(kind, alertID string, at time.Time, payload map[string]any) error {
			raw, _ := json.Marshal(payload)
			_, err := tx.Append(event.Event{Kind: kind, At: at, ObjectType: "alert", ObjectID: &alertID, Payload: raw})
			return err
		}
		if err := appendEv(trackedKind, "alrt-1", t0, map[string]any{"alert_id": "alrt-1", "item_id": "item-1", "by": "op"}); err != nil {
			return err
		}
		if err := appendEv(settledKind, "alrt-1", t0.Add(time.Second), map[string]any{"alert_id": "alrt-1", "item_id": "item-1", "outcome": "fix-fault", "by": "op"}); err != nil {
			return err
		}
		if err := appendEv(itemLinkedKind, "alrt-1", t0.Add(2*time.Second), map[string]any{"alert_id": "alrt-1", "item_id": "item-extra", "relation": "filed", "by": "owner"}); err != nil {
			return err
		}
		if err := appendEv(settledKind, "alrt-3", t0.Add(3*time.Second), map[string]any{"alert_id": "alrt-3", "item_id": "item-3", "outcome": "fix-rule", "by": "op"}); err != nil {
			return err
		}
		if err := appendEv(silencedKind, "alrt-3", t0.Add(4*time.Second), map[string]any{"alert_id": "alrt-3", "item_id": "item-3", "by": "op", "reason": "noisy"}); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		s.Close()
		t.Fatalf("seeding legacy store: %v", err)
	}
	s.Close()

	// Verify read-only projection works BEFORE alert_item_links exists and does not write.
	rdOnly, err := store.Open(ctx, path, false)
	if err != nil {
		t.Fatalf("Open read-only: %v", err)
	}
	err = rdOnly.ReadTx(ctx, func(rd *store.Reader) error {
		hasLinks, err := tableExists(rd, "alert_item_links")
		if err != nil || hasLinks {
			t.Fatalf("expected alert_item_links to be absent on read-only open, hasLinks=%v err=%v", hasLinks, err)
		}
		items1, err := LinkedItemsForAlert(rd, "alrt-1")
		if err != nil {
			return err
		}
		if len(items1) != 2 || items1[0].ItemID != "item-1" || items1[0].Relation != LinkRelationFixFault || items1[1].ItemID != "item-extra" || items1[1].Relation != LinkRelationFiled {
			t.Fatalf("projected items1 = %+v", items1)
		}
		alerts2, err := LinkedAlertsForItem(rd, "item-2")
		if err != nil {
			return err
		}
		if len(alerts2) != 1 || alerts2[0].AlertID != "alrt-2" || alerts2[0].Relation != LinkRelationTracked {
			t.Fatalf("projected alerts2 = %+v", alerts2)
		}
		items3, err := LinkedItemsForAlert(rd, "alrt-3")
		if err != nil {
			return err
		}
		if len(items3) != 1 || items3[0].Relation != LinkRelationFixRule {
			t.Fatalf("projected items3 = %+v, want fix-rule preserved over silence", items3)
		}
		return nil
	})
	rdOnly.Close()
	if err != nil {
		t.Fatalf("read-only projection failed: %v", err)
	}

	// Now open for writing: migration creates alert_item_links and backfills it.
	s2, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatalf("Open write: %v", err)
	}
	defer s2.Close()
	err = s2.ReadTx(ctx, func(rd *store.Reader) error {
		hasLinks, err := tableExists(rd, "alert_item_links")
		if err != nil || !hasLinks {
			t.Fatalf("expected alert_item_links to exist after write-open, hasLinks=%v err=%v", hasLinks, err)
		}
		var count int
		if err := rd.QueryRow(`SELECT count(*) FROM alert_item_links`).Scan(&count); err != nil {
			return err
		}
		if count != 4 {
			t.Fatalf("backfilled row count = %d, want 4", count)
		}
		alerts1, err := LinkedAlertsForItem(rd, "item-1")
		if err != nil {
			return err
		}
		if len(alerts1) != 1 || alerts1[0].Relation != LinkRelationFixFault {
			t.Fatalf("backfilled alerts1 = %+v", alerts1)
		}
		items3, err := LinkedItemsForAlert(rd, "alrt-3")
		if err != nil {
			return err
		}
		if len(items3) != 1 || items3[0].Relation != LinkRelationFixRule {
			t.Fatalf("backfilled items3 = %+v, want fix-rule", items3)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("verifying backfill failed: %v", err)
	}
}

func TestLinkAlertItemInTxAndReadHelpers(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	t0 := time.Date(2026, 11, 25, 12, 0, 0, 0, time.UTC)

	// Unknown alert returns ErrAlertNotFound.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		return LinkAlertItemInTx(tx, "alrt-missing", "item-1", LinkRelationFiled, "owner", "", t0)
	})
	if !errors.Is(err, ErrAlertNotFound) {
		t.Fatalf("unknown alert err = %v, want ErrAlertNotFound", err)
	}

	// Seed an open alert and a resolved alert.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO alert_episodes (alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json, variables_json, settlement_state)
			VALUES ('alrt-open', 'rule-a', 'subj-1', 1, 'open', 'high', 'fp1', 1, ?, 1, ?, '{}', '{"pkg":"a"}', 'uninvestigated'),
			       ('alrt-res', 'rule-a', 'subj-1', 2, 'resolved', 'high', 'fp2', 2, ?, 2, ?, '{}', '{"pkg":"a"}', 'uninvestigated')`,
			t0.UnixMilli(), t0.UnixMilli(), t0.Add(time.Minute).UnixMilli(), t0.Add(time.Minute).UnixMilli()); err != nil {
			return err
		}
		if err := appendAlert(tx, "alrt-res", openedKind, t0.Add(time.Minute), map[string]any{"alert_id": "alrt-res", "rule_id": "rule-a", "subject_key": "subj-1"}); err != nil {
			return err
		}
		if err := appendAlert(tx, "alrt-res", resolvedKind, t0.Add(2*time.Minute), map[string]any{"alert_id": "alrt-res", "reason": "condition-cleared"}); err != nil {
			return err
		}
		if err := LinkAlertItemInTx(tx, "alrt-res", "item-filed-1", LinkRelationFiled, "owner", "first fix", t0.Add(3*time.Minute)); err != nil {
			return err
		}
		if err := LinkAlertItemInTx(tx, "alrt-res", "item-filed-2", LinkRelationFiled, "operator-session", "follow-up", t0.Add(4*time.Minute)); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seeding & linking failed: %v", err)
	}

	err = s.ReadTx(ctx, func(rd *store.Reader) error {
		// Resolved episode must stay resolved and uninvestigated.
		detail, err := ReadAlertEpisode(rd, "alrt-res")
		if err != nil {
			return err
		}
		if !detail.Found || detail.Episode.State != "resolved" || detail.Episode.SettlementState != "uninvestigated" {
			t.Fatalf("unexpected episode state after LinkAlertItemInTx: %+v", detail.Episode)
		}
		if len(detail.LinkedItems) != 2 || detail.LinkedItems[0].ItemID != "item-filed-1" || detail.LinkedItems[1].ItemID != "item-filed-2" {
			t.Fatalf("LinkedItems = %+v", detail.LinkedItems)
		}
		if len(detail.RelatedEpisodes) != 1 || detail.RelatedEpisodes[0].AlertID != "alrt-open" {
			t.Fatalf("RelatedEpisodes = %+v", detail.RelatedEpisodes)
		}
		if len(detail.Transitions) != 4 {
			t.Fatalf("Transitions count = %d, want 4: %+v", len(detail.Transitions), detail.Transitions)
		}
		if detail.Transitions[0].StateChange != "re-fired" || detail.Transitions[0].Actor != "evaluator" {
			t.Fatalf("transition 0 = %+v", detail.Transitions[0])
		}
		if detail.Transitions[2].StateChange != "item-linked" || detail.Transitions[2].Actor != "owner" {
			t.Fatalf("transition 2 = %+v", detail.Transitions[2])
		}

		ledger, err := ReadAlertEpisodes(rd, AlertFilter{RuleID: "rule-a"})
		if err != nil {
			return err
		}
		if ledger.Absent || len(ledger.Episodes) != 2 {
			t.Fatalf("ledger = %+v", ledger)
		}
		// Unsettled open episode should sort before resolved.
		if ledger.Episodes[0].AlertID != "alrt-open" {
			t.Fatalf("expected alrt-open first, got %s", ledger.Episodes[0].AlertID)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadTx failed: %v", err)
	}
}

func TestReadAlertHelpersOnEmptyStoreWithoutAlertTables(t *testing.T) {
	ctx := context.Background()
	dbPath := filepath.Join(t.TempDir(), "empty.sqlite3")
	raw, err := sql.Open("sqlite", "file:"+dbPath)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`CREATE TABLE items (item_id TEXT PRIMARY KEY);`); err != nil {
		raw.Close()
		t.Fatal(err)
	}
	raw.Close()

	s, err := store.Open(ctx, dbPath, false)
	if err != nil {
		t.Fatalf("Open read-only: %v", err)
	}
	defer s.Close()

	err = s.ReadTx(ctx, func(rd *store.Reader) error {
		alerts, err := LinkedAlertsForItem(rd, "item-1")
		if err != nil || len(alerts) != 0 {
			t.Fatalf("LinkedAlertsForItem = %+v, err=%v", alerts, err)
		}
		items, err := LinkedItemsForAlert(rd, "alrt-1")
		if err != nil || len(items) != 0 {
			t.Fatalf("LinkedItemsForAlert = %+v, err=%v", items, err)
		}
		ledger, err := ReadAlertEpisodes(rd, AlertFilter{})
		if err != nil || !ledger.Absent {
			t.Fatalf("ReadAlertEpisodes = %+v, err=%v", ledger, err)
		}
		detail, err := ReadAlertEpisode(rd, "alrt-1")
		if err != nil || !detail.Absent {
			t.Fatalf("ReadAlertEpisode = %+v, err=%v", detail, err)
		}
		silences, err := ReadActiveSilences(rd)
		if err != nil || len(silences) != 0 {
			t.Fatalf("ReadActiveSilences = %+v, err=%v", silences, err)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadTx failed: %v", err)
	}
}

func TestSettleSilenceAndReopenPreserveAlertItemLinks(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()

	t0 := time.Date(2026, 11, 25, 14, 0, 0, 0, time.UTC)
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS items (item_id TEXT PRIMARY KEY, title TEXT, state TEXT, updated_at INTEGER)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO items (item_id, title, state, updated_at) VALUES
			('item-fix', 'Fix it', 'in-queue', ?),
			('item-sil', 'Silence it', 'in-queue', ?)`, t0.UnixMilli(), t0.UnixMilli()); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO alert_episodes (alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json, variables_json, settlement_state)
			VALUES ('alrt-1', 'rule-a', 'subj-1', 1, 'open', 'high', 'fp1', 1, ?, 1, ?, '{}', '{"pkg":"a"}', 'investigated')`,
			t0.UnixMilli(), t0.UnixMilli()); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	ev := Evaluator{
		Store: s,
		Rules: []Rule{{ID: "rule-a", Severity: "high", When: When{Pattern: PredicateMatch, GroupBy: "pkg"}}},
		Clock: func() time.Time { return t0.Add(time.Minute) },
	}
	if err := ev.Settle(ctx, "alrt-1", SettleOptions{By: "op", Outcome: "fix-fault", ItemID: "item-fix"}); err != nil {
		t.Fatalf("Settle: %v", err)
	}
	// Simulate recurrence clearing tracking_item_id and resetting settlement_state to investigated.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE alert_episodes SET state='open', settlement_state='investigated', tracking_item_id=NULL WHERE alert_id='alrt-1'`)
		return err
	})
	if err != nil {
		t.Fatalf("reopen update failed: %v", err)
	}
	until := "1h"
	if _, err := ev.Silence(ctx, SilenceOptions{AlertID: "alrt-1", By: "op", ItemID: "item-sil", Reason: "mute", Until: &until, Cause: "noisy", Evidence: "log"}); err != nil {
		t.Fatalf("Silence: %v", err)
	}
	// Simulate another recurrence clearing tracking_item_id on alert_episodes.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE alert_episodes SET state='open', settlement_state='investigated', tracking_item_id=NULL WHERE alert_id='alrt-1'`)
		return err
	})
	if err != nil {
		t.Fatalf("second reopen update failed: %v", err)
	}

	err = s.ReadTx(ctx, func(rd *store.Reader) error {
		items, err := LinkedItemsForAlert(rd, "alrt-1")
		if err != nil {
			return err
		}
		if len(items) != 2 {
			t.Fatalf("expected 2 links preserved after recurrence, got %+v", items)
		}
		if items[0].ItemID != "item-fix" || items[0].Relation != LinkRelationFixFault {
			t.Fatalf("item[0] = %+v", items[0])
		}
		if items[1].ItemID != "item-sil" || items[1].Relation != LinkRelationSilence || !items[1].ActiveSilence {
			t.Fatalf("item[1] = %+v", items[1])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadTx failed: %v", err)
	}
}

func TestReadAlertTransitionsResolvedActor(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite3"), true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	t0 := time.Unix(1700000000, 0).UTC()

	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, id := range []string{"alrt-auto", "alrt-manual"} {
			if _, err := tx.Exec(`INSERT INTO alert_episodes (alert_id, rule_id, subject_key, episode_num, state, settlement_state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json)
				VALUES (?, 'rule-a', ?, 1, 'resolved', 'settled', 'medium', 'fp', 1, ?, 1, ?, '{}')`,
				id, "subj-"+id, t0.UnixMilli(), t0.UnixMilli()); err != nil {
				return err
			}
		}
		for _, ev := range []struct {
			id     string
			at     time.Time
			reason string
		}{
			{"alrt-auto", t0.Add(time.Minute), "condition-cleared"},
			{"alrt-manual", t0.Add(2 * time.Minute), "manual"},
		} {
			raw, err := json.Marshal(map[string]any{"alert_id": ev.id, "reason": ev.reason})
			if err != nil {
				return err
			}
			if _, err := tx.Append(event.Event{Kind: resolvedKind, At: ev.at, ObjectType: "alert", ObjectID: &ev.id, Payload: raw}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	err = s.ReadTx(ctx, func(rd *store.Reader) error {
		autoDetail, err := ReadAlertEpisode(rd, "alrt-auto")
		if err != nil {
			return err
		}
		if len(autoDetail.Transitions) != 1 || autoDetail.Transitions[0].Actor != "evaluator" {
			t.Fatalf("auto resolved transitions = %+v, want Actor=evaluator", autoDetail.Transitions)
		}
		manualDetail, err := ReadAlertEpisode(rd, "alrt-manual")
		if err != nil {
			return err
		}
		if len(manualDetail.Transitions) != 1 || manualDetail.Transitions[0].Actor != "" {
			t.Fatalf("manual resolved transitions = %+v, want empty Actor", manualDetail.Transitions)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadTx failed: %v", err)
	}
}

func TestReadAlertEpisodesTriageOrderingAndTransitions(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "state.sqlite3"), true)
	if err != nil {
		t.Fatalf("Open: %v", err)
	}
	defer s.Close()
	t0 := time.Unix(1700000000, 0).UTC()

	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		// 1. Triage-settled episode (newest opened_at, still open condition, settled by triage).
		if _, err := tx.Exec(`INSERT INTO alert_episodes (
			alert_id, rule_id, subject_key, episode_num, state, settlement_state, severity, condition_fingerprint,
			opened_seq, opened_at, last_observed_seq, last_observed_at,
			outcome, item_link_kind, tracking_item_id, settled_by, settled_at,
			triage_state, triage_outcome, triage_outcome_kind, triage_run_id, triage_finding, triaged_at, bundle_json
		) VALUES (
			'alrt-settled', 'rule-a', 'subj-settled', 1, 'open', 'settled', 'warning', 'fp-1',
			10, ?, 10, ?,
			'fix-fault', 'filed', 'item-1', 'tillr-triage', ?,
			'settled', 'filed', 'fix-fault', 'run-triage-1', 'filed defect for go-build stall', ?, '{}'
		)`, t0.Add(3*time.Minute).UnixMilli(), t0.Add(3*time.Minute).UnixMilli(), t0.Add(4*time.Minute).UnixMilli(), t0.Add(4*time.Minute).UnixMilli()); err != nil {
			return err
		}
		// 2. Escalated episode (even though settled against an item, escalated_to + triage_state=escalated must lead).
		if _, err := tx.Exec(`INSERT INTO alert_episodes (
			alert_id, rule_id, subject_key, episode_num, state, settlement_state, severity, condition_fingerprint,
			opened_seq, opened_at, last_observed_seq, last_observed_at,
			outcome, item_link_kind, tracking_item_id, escalated_to,
			triage_state, triage_outcome, triage_outcome_kind, triage_run_id, triage_finding, triaged_at, bundle_json
		) VALUES (
			'alrt-escalated', 'rule-b', 'subj-esc', 1, 'open', 'settled', 'high', 'fp-2',
			5, ?, 6, ?,
			'fix-fault', 'linked', 'item-2', 'operator-session',
			'escalated', 'escalated', 'fix-fault', 'run-triage-2', 'needs manual lane cleanup', ?, '{}'
		)`, t0.Add(1*time.Minute).UnixMilli(), t0.Add(2*time.Minute).UnixMilli(), t0.Add(2*time.Minute).UnixMilli()); err != nil {
			return err
		}
		// 3. Unsettled untriaged episode.
		if _, err := tx.Exec(`INSERT INTO alert_episodes (
			alert_id, rule_id, subject_key, episode_num, state, settlement_state, severity, condition_fingerprint,
			opened_seq, opened_at, last_observed_seq, last_observed_at,
			triage_state, active_triage_run_id, bundle_json
		) VALUES (
			'alrt-unsettled', 'rule-c', 'subj-un', 1, 'open', 'uninvestigated', 'warning', 'fp-3',
			8, ?, 8, ?,
			'triaging', 'run-triage-3', '{}'
		)`, t0.Add(2*time.Minute).UnixMilli(), t0.Add(2*time.Minute).UnixMilli()); err != nil {
			return err
		}

		// Record triage events for alrt-escalated.
		escID := "alrt-escalated"
		for _, rec := range []struct {
			kind    string
			at      time.Time
			payload map[string]any
		}{
			{triageStartedKind, t0.Add(90 * time.Second), map[string]any{"alert_id": escID, "run_id": "run-triage-2"}},
			{triagedKind, t0.Add(2 * time.Minute), map[string]any{"alert_id": escID, "run_id": "run-triage-2", "outcome": "escalated", "outcome_kind": "fix-fault", "item_id": "item-2"}},
			{escalatedKind, t0.Add(2*time.Minute + time.Second), map[string]any{"alert_id": escID, "run_id": "run-triage-2", "outcome": "escalated", "escalate_to": "operator-session", "item_id": "item-2"}},
		} {
			raw, err := json.Marshal(rec.payload)
			if err != nil {
				return err
			}
			if _, err := tx.Append(event.Event{Kind: rec.kind, At: rec.at, ObjectType: "alert", ObjectID: &escID, Payload: raw}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("seed failed: %v", err)
	}

	err = s.ReadTx(ctx, func(rd *store.Reader) error {
		ledger, err := ReadAlertEpisodes(rd, AlertFilter{})
		if err != nil {
			return err
		}
		if len(ledger.Episodes) != 3 {
			t.Fatalf("len(Episodes) = %d, want 3", len(ledger.Episodes))
		}
		// Unsettled and escalated must lead ahead of triage-settled even though settled has newer opened_at.
		if ledger.Episodes[0].AlertID != "alrt-unsettled" || ledger.Episodes[1].AlertID != "alrt-escalated" || ledger.Episodes[2].AlertID != "alrt-settled" {
			t.Fatalf("order = [%s, %s, %s], want [alrt-unsettled, alrt-escalated, alrt-settled]",
				ledger.Episodes[0].AlertID, ledger.Episodes[1].AlertID, ledger.Episodes[2].AlertID)
		}
		if ledger.Episodes[0].ActiveTriageRunID != "run-triage-3" || ledger.Episodes[0].TriageState != "triaging" {
			t.Fatalf("unsettled triage fields = %+v", ledger.Episodes[0])
		}
		if ledger.Episodes[2].TriageRunID != "run-triage-1" || ledger.Episodes[2].TriageOutcome != "filed" || ledger.Episodes[2].TriageOutcomeKind != "fix-fault" || ledger.Episodes[2].TriageFinding == "" || ledger.Episodes[2].TriagedAt == nil {
			t.Fatalf("settled triage fields = %+v", ledger.Episodes[2])
		}

		// Filter by triage state.
		settledOnly, err := ReadAlertEpisodes(rd, AlertFilter{TriageState: "settled"})
		if err != nil {
			return err
		}
		if len(settledOnly.Episodes) != 1 || settledOnly.Episodes[0].AlertID != "alrt-settled" {
			t.Fatalf("TriageState=settled got %+v", settledOnly.Episodes)
		}

		// Check triage transitions on alrt-escalated.
		detail, err := ReadAlertEpisode(rd, "alrt-escalated")
		if err != nil {
			return err
		}
		if len(detail.Transitions) != 3 {
			t.Fatalf("len(Transitions) = %d, want 3: %+v", len(detail.Transitions), detail.Transitions)
		}
		wantChanges := []string{"triage-started", "triaged", "escalated"}
		for i, want := range wantChanges {
			tr := detail.Transitions[i]
			if tr.StateChange != want || tr.Actor != "tillr-triage" {
				t.Fatalf("transition[%d] = %+v, want StateChange=%q Actor=tillr-triage", i, tr, want)
			}
		}
		if !strings.Contains(detail.Transitions[0].Detail, "run-triage-2") {
			t.Fatalf("triage-started detail = %q, want run-triage-2", detail.Transitions[0].Detail)
		}
		if !strings.Contains(detail.Transitions[1].Detail, "escalated fix-fault") || !strings.Contains(detail.Transitions[1].Detail, "item-2") {
			t.Fatalf("triaged detail = %q", detail.Transitions[1].Detail)
		}
		if !strings.Contains(detail.Transitions[2].Detail, "to operator-session") {
			t.Fatalf("escalated detail = %q", detail.Transitions[2].Detail)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadTx failed: %v", err)
	}
}
