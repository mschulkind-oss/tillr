package item_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
	"github.com/mschulkind-oss/tillr/watch"
)

func TestFileRequestWithAlertsAndShow(t *testing.T) {
	ctx := context.Background()
	s := syncStore(t)
	root := scratch(t)
	t0 := time.Date(2026, 11, 25, 16, 0, 0, 0, time.UTC)

	// Seed an open alert and a resolved alert.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO alert_episodes (alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json, settlement_state)
			VALUES ('alrt-open', 'rule-a', 'subj-1', 1, 'open', 'high', 'fp1', 1, ?, 1, ?, '{}', 'uninvestigated'),
			       ('alrt-resolved', 'rule-b', 'subj-2', 1, 'resolved', 'medium', 'fp2', 2, ?, 2, ?, '{}', 'uninvestigated')`,
			t0.UnixMilli(), t0.UnixMilli(), t0.UnixMilli(), t0.UnixMilli())
		return err
	}); err != nil {
		t.Fatalf("seed alerts: %v", err)
	}

	// File request linking both alerts (including the resolved one).
	var itemID string
	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		env, err := item.FileRequest(ctx, tx, item.RequestFiling{
			Words:       []byte("Fix the alert fault.\n"),
			Title:       "Fix alert fault",
			Seat:        "build",
			Workflow:    item.SingleWorkerWorkflow,
			Alerts:      []string{"alrt-resolved", "alrt-open", "alrt-open"},
			Destination: item.Destination{To: "last"},
			At:          t0.Add(time.Minute),
			By:          "owner",
			Via:         "cli",
		})
		if err != nil {
			return err
		}
		var body map[string]any
		if err := json.Unmarshal(env.Payload, &body); err != nil {
			return err
		}
		itemID, _ = body["item_id"].(string)
		return nil
	})
	if err != nil {
		t.Fatalf("FileRequest with alerts: %v", err)
	}
	if itemID == "" {
		t.Fatal("expected non-empty item_id")
	}

	// Idempotent refile with same words and same alert list succeeds.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.FileRequest(ctx, tx, item.RequestFiling{
			Words:       []byte("Fix the alert fault.\n"),
			Title:       "Fix alert fault",
			Seat:        "build",
			Workflow:    item.SingleWorkerWorkflow,
			Alerts:      []string{"alrt-open", "alrt-resolved"},
			Destination: item.Destination{To: "last"},
			At:          t0.Add(2 * time.Minute),
			By:          "owner",
			Via:         "cli",
		})
		return err
	})
	if err != nil {
		t.Fatalf("idempotent refile: %v", err)
	}

	// Refile with different alerts is refused with request-differs.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.FileRequest(ctx, tx, item.RequestFiling{
			Words:       []byte("Fix the alert fault.\n"),
			Title:       "Fix alert fault",
			Seat:        "build",
			Workflow:    item.SingleWorkerWorkflow,
			Alerts:      []string{"alrt-open"},
			Destination: item.Destination{To: "last"},
			At:          t0.Add(2 * time.Minute),
			By:          "owner",
			Via:         "cli",
		})
		return err
	})
	if err == nil {
		t.Fatal("expected request-differs when alert list differs")
	}

	// File a second item linked to alrt-open, then simulate recurrence clearing tracking_item_id.
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.FileRequest(ctx, tx, item.RequestFiling{
			Words:       []byte("Second item for same alert.\n"),
			Title:       "Second item",
			Seat:        "build",
			Workflow:    item.SingleWorkerWorkflow,
			Alerts:      []string{"alrt-open"},
			Destination: item.Destination{To: "last"},
			At:          t0.Add(3 * time.Minute),
			By:          "operator-session",
			Via:         "cli",
		})
		if err != nil {
			return err
		}
		_, err = tx.Exec(`UPDATE alert_episodes SET tracking_item_id = NULL WHERE alert_id = 'alrt-open'`)
		return err
	})
	if err != nil {
		t.Fatalf("second FileRequest: %v", err)
	}

	// Verify resolved alert stayed resolved/uninvestigated, both items survived recurrence, and ShowWithQuestions returns linked_alerts.
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		detail, err := watch.ReadAlertEpisode(r, "alrt-resolved")
		if err != nil {
			return err
		}
		if detail.Episode.State != "resolved" || detail.Episode.SettlementState != "uninvestigated" {
			t.Fatalf("resolved alert mutated: %+v", detail.Episode)
		}
		links, err := watch.LinkedItemsForAlert(r, "alrt-open")
		if err != nil {
			return err
		}
		if len(links) != 2 {
			t.Fatalf("expected 2 links on alrt-open after recurrence, got %+v", links)
		}
		shown, err := item.ShowWithQuestions(r, itemID, root, vantage.Preloaded(root, vantage.ParseIndex([]byte(`{"index":{"documents":[]}}`), "fixture")))
		if err != nil {
			return err
		}
		var payload struct {
			LinkedAlerts []watch.LinkedAlert `json:"linked_alerts"`
		}
		if err := json.Unmarshal(shown.Payload, &payload); err != nil {
			return err
		}
		if len(payload.LinkedAlerts) != 2 {
			t.Fatalf("expected 2 linked_alerts on show, got %+v", payload.LinkedAlerts)
		}
		if payload.LinkedAlerts[0].AlertID != "alrt-open" || payload.LinkedAlerts[1].AlertID != "alrt-resolved" {
			t.Fatalf("unexpected linked_alerts: %+v", payload.LinkedAlerts)
		}
		if payload.LinkedAlerts[0].Relation != watch.LinkRelationFiled || payload.LinkedAlerts[0].LinkedBy != "owner" {
			t.Fatalf("unexpected link metadata: %+v", payload.LinkedAlerts[0])
		}
		return nil
	})
	if err != nil {
		t.Fatalf("ReadTx: %v", err)
	}
}

func TestFileRequestUnknownAlertRollsBackCompletely(t *testing.T) {
	ctx := context.Background()
	s := syncStore(t)
	t0 := time.Date(2026, 11, 25, 17, 0, 0, 0, time.UTC)

	var beforeItems, beforeWords, beforeLinks, beforeEvents int
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		_ = r.QueryRow(`SELECT count(*) FROM items`).Scan(&beforeItems)
		_ = r.QueryRow(`SELECT count(*) FROM request_words`).Scan(&beforeWords)
		_ = r.QueryRow(`SELECT count(*) FROM alert_item_links`).Scan(&beforeLinks)
		_ = r.QueryRow(`SELECT count(*) FROM events`).Scan(&beforeEvents)
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.FileRequest(ctx, tx, item.RequestFiling{
			Words:       []byte("Should roll back.\n"),
			Title:       "Rollback test",
			Seat:        "build",
			Workflow:    item.SingleWorkerWorkflow,
			Alerts:      []string{"alrt-does-not-exist"},
			Destination: item.Destination{To: "last"},
			At:          t0,
			By:          "owner",
			Via:         "cli",
		})
		return err
	})
	if err == nil {
		t.Fatal("expected error for unknown alert")
	}

	var afterItems, afterWords, afterLinks, afterEvents int
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		_ = r.QueryRow(`SELECT count(*) FROM items`).Scan(&afterItems)
		_ = r.QueryRow(`SELECT count(*) FROM request_words`).Scan(&afterWords)
		_ = r.QueryRow(`SELECT count(*) FROM alert_item_links`).Scan(&afterLinks)
		_ = r.QueryRow(`SELECT count(*) FROM events`).Scan(&afterEvents)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if afterItems != beforeItems || afterWords != beforeWords || afterLinks != beforeLinks || afterEvents != beforeEvents {
		t.Fatalf("rollback failed: items %d->%d words %d->%d links %d->%d events %d->%d",
			beforeItems, afterItems, beforeWords, afterWords, beforeLinks, afterLinks, beforeEvents, afterEvents)
	}
}

func TestShowWithQuestionsWithoutWatchTables(t *testing.T) {
	ctx := context.Background()
	s := syncStore(t)
	root := scratch(t)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`DROP TABLE IF EXISTS alert_item_links`); err != nil {
			return err
		}
		if _, err := tx.Exec(`DROP TABLE IF EXISTS alert_episodes`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES ('item-no-watch','No watch','in-queue',0)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	err := s.ReadTx(ctx, func(r *store.Reader) error {
		shown, err := item.ShowWithQuestions(r, "item-no-watch", root, vantage.Preloaded(root, vantage.ParseIndex([]byte(`{"index":{"documents":[]}}`), "fixture")))
		if err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(shown.Payload, &fields); err != nil {
			return err
		}
		if _, ok := fields["linked_alerts"]; ok {
			t.Fatalf("expected linked_alerts omitted when no watch tables exist: %s", shown.Payload)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	_ = filepath.Base(root)
}

func TestFileRequestTriageProvenanceAndDedupKey(t *testing.T) {
	ctx := context.Background()
	s := syncStore(t)
	root := scratch(t)
	t0 := time.Date(2026, 11, 26, 10, 0, 0, 0, time.UTC)

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO alert_episodes (alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json, settlement_state)
			VALUES ('alrt-t1', 'drain-item-fault', 'item-a', 1, 'open', 'high', 'fp1', 1, ?, 1, ?, '{}', 'uninvestigated'),
			       ('alrt-t2', 'drain-item-fault', 'item-b', 1, 'open', 'high', 'fp2', 2, ?, 2, ?, '{}', 'uninvestigated'),
			       ('alrt-t3', 'drain-item-fault', 'item-c', 1, 'open', 'high', 'fp3', 3, ?, 3, ?, '{}', 'uninvestigated')`,
			t0.UnixMilli(), t0.UnixMilli(), t0.UnixMilli(), t0.UnixMilli(), t0.UnixMilli(), t0.UnixMilli())
		return err
	}); err != nil {
		t.Fatalf("seed alerts: %v", err)
	}

	// Malformed dedup_key is rejected.
	for _, bad := range []string{"no-colon", ":cause-only", "rule-only:", "rule:line\nbreak"} {
		err := s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := item.FileRequest(ctx, tx, item.RequestFiling{
				Words:       []byte("Bad dedup key.\n"),
				Title:       "Bad dedup key",
				Seat:        "build",
				Workflow:    item.SingleWorkerWorkflow,
				FiledBy:     "tillr-triage",
				DedupKey:    bad,
				Destination: item.Destination{To: "last"},
				At:          t0,
				Via:         "cli",
			})
			return err
		})
		if rej, ok := item.RejectionOf(err); !ok || rej.Rule != "dedup-key-invalid" {
			t.Fatalf("dedup_key %q: expected dedup-key-invalid rejection, got %v", bad, err)
		}
	}

	// First triage filing creates an item with filed_by and dedup_key and links alrt-t1.
	var firstID string
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		env, err := item.FileRequest(ctx, tx, item.RequestFiling{
			Words:       []byte("First diagnosis of stage-failed-x.\n"),
			Title:       "Fix stage-failed-x",
			Seat:        "build",
			Workflow:    item.SingleWorkerWorkflow,
			Alerts:      []string{"alrt-t1"},
			FiledBy:     "tillr-triage",
			DedupKey:    "drain-item-fault:stage-failed-x",
			Destination: item.Destination{To: "last"},
			At:          t0.Add(time.Minute),
			Via:         "cli",
		})
		if err != nil {
			return err
		}
		var body struct {
			ItemID       string   `json:"item_id"`
			FiledBy      string   `json:"filed_by"`
			DedupKey     string   `json:"dedup_key"`
			Deduplicated bool     `json:"deduplicated"`
			LinkedAlerts []string `json:"linked_alerts"`
		}
		if err := json.Unmarshal(env.Payload, &body); err != nil {
			return err
		}
		firstID = body.ItemID
		if body.FiledBy != "tillr-triage" || body.DedupKey != "drain-item-fault:stage-failed-x" || body.Deduplicated {
			t.Fatalf("unexpected first response: %+v", body)
		}
		if len(body.LinkedAlerts) != 1 || body.LinkedAlerts[0] != "alrt-t1" {
			t.Fatalf("unexpected first linked_alerts: %+v", body.LinkedAlerts)
		}
		return nil
	}); err != nil {
		t.Fatalf("first triage filing: %v", err)
	}

	// Second triage filing with different words and alert alrt-t2 but same dedup_key lands on firstID.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		env, err := item.FileRequest(ctx, tx, item.RequestFiling{
			Words:       []byte("Second diagnosis of stage-failed-x from another alert.\n"),
			Title:       "Fix stage-failed-x (second alert)",
			Seat:        "build",
			Workflow:    item.SingleWorkerWorkflow,
			Alerts:      []string{"alrt-t2"},
			FiledBy:     "tillr-triage",
			DedupKey:    "drain-item-fault:stage-failed-x",
			Destination: item.Destination{To: "last"},
			At:          t0.Add(2 * time.Minute),
			Via:         "cli",
		})
		if err != nil {
			return err
		}
		var body struct {
			ItemID       string   `json:"item_id"`
			FiledBy      string   `json:"filed_by"`
			DedupKey     string   `json:"dedup_key"`
			Deduplicated bool     `json:"deduplicated"`
			LinkedAlerts []string `json:"linked_alerts"`
		}
		if err := json.Unmarshal(env.Payload, &body); err != nil {
			return err
		}
		if body.ItemID != firstID || !body.Deduplicated || body.FiledBy != "tillr-triage" || body.DedupKey != "drain-item-fault:stage-failed-x" {
			t.Fatalf("unexpected dedup response: %+v (want item_id %s)", body, firstID)
		}
		if len(body.LinkedAlerts) != 2 || body.LinkedAlerts[0] != "alrt-t1" || body.LinkedAlerts[1] != "alrt-t2" {
			t.Fatalf("expected both alert ids on dedup response, got %+v", body.LinkedAlerts)
		}
		return nil
	}); err != nil {
		t.Fatalf("second triage filing: %v", err)
	}

	// Unknown alert on dedup hit rolls back cleanly.
	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.FileRequest(ctx, tx, item.RequestFiling{
			Words:       []byte("Dedup with unknown alert.\n"),
			Title:       "Unknown alert",
			Seat:        "build",
			Workflow:    item.SingleWorkerWorkflow,
			Alerts:      []string{"alrt-nonexistent"},
			FiledBy:     "tillr-triage",
			DedupKey:    "drain-item-fault:stage-failed-x",
			Destination: item.Destination{To: "last"},
			At:          t0.Add(3 * time.Minute),
			Via:         "cli",
		})
		return err
	})
	if rej, ok := item.RejectionOf(err); !ok || rej.Rule != "alert-not-found" {
		t.Fatalf("expected alert-not-found rejection on dedup hit with missing alert, got %v", err)
	}

	// Verify only 1 item exists and ShowWithQuestions exposes filed_by, dedup_key, and both linked_alerts.
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var itemCount int
		if err := r.QueryRow(`SELECT count(*) FROM items`).Scan(&itemCount); err != nil {
			return err
		}
		if itemCount != 1 {
			t.Fatalf("expected 1 item after dedup, got %d", itemCount)
		}
		shown, err := item.ShowWithQuestions(r, firstID, root, vantage.Preloaded(root, vantage.ParseIndex([]byte(`{"index":{"documents":[]}}`), "fixture")))
		if err != nil {
			return err
		}
		var payload struct {
			FiledBy      string              `json:"filed_by"`
			DedupKey     string              `json:"dedup_key"`
			LinkedAlerts []watch.LinkedAlert `json:"linked_alerts"`
		}
		if err := json.Unmarshal(shown.Payload, &payload); err != nil {
			return err
		}
		if payload.FiledBy != "tillr-triage" || payload.DedupKey != "drain-item-fault:stage-failed-x" {
			t.Fatalf("show provenance = (%q, %q), want (tillr-triage, drain-item-fault:stage-failed-x)", payload.FiledBy, payload.DedupKey)
		}
		if len(payload.LinkedAlerts) != 2 || payload.LinkedAlerts[0].AlertID != "alrt-t1" || payload.LinkedAlerts[1].AlertID != "alrt-t2" {
			t.Fatalf("show linked_alerts = %+v, want [alrt-t1, alrt-t2]", payload.LinkedAlerts)
		}
		if payload.LinkedAlerts[0].LinkedBy != "tillr-triage" || payload.LinkedAlerts[1].LinkedBy != "tillr-triage" {
			t.Fatalf("expected linked_by=tillr-triage on both alerts, got %+v", payload.LinkedAlerts)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify show: %v", err)
	}

	// Once firstID is closed, a new filing with the same dedup_key creates a new item.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, firstID, "superseded", "owner", t0.Add(4*time.Minute))
		return err
	}); err != nil {
		t.Fatalf("close first item: %v", err)
	}
	var secondID string
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		env, err := item.FileRequest(ctx, tx, item.RequestFiling{
			Words:       []byte("Recurred after close: stage-failed-x.\n"),
			Title:       "Fix stage-failed-x recurrence",
			Seat:        "build",
			Workflow:    item.SingleWorkerWorkflow,
			Alerts:      []string{"alrt-t3"},
			FiledBy:     "tillr-triage",
			DedupKey:    "drain-item-fault:stage-failed-x",
			Destination: item.Destination{To: "last"},
			At:          t0.Add(5 * time.Minute),
			Via:         "cli",
		})
		if err != nil {
			return err
		}
		var body struct {
			ItemID       string `json:"item_id"`
			Deduplicated bool   `json:"deduplicated"`
		}
		if err := json.Unmarshal(env.Payload, &body); err != nil {
			return err
		}
		secondID = body.ItemID
		if body.Deduplicated || secondID == firstID {
			t.Fatalf("expected new item after close, got %+v (firstID=%s)", body, firstID)
		}
		return nil
	}); err != nil {
		t.Fatalf("refile after close: %v", err)
	}
}

func TestFileRequestConcurrentDedupKey(t *testing.T) {
	ctx := context.Background()
	s := syncStore(t)
	root := scratch(t)
	t0 := time.Date(2026, 11, 26, 11, 0, 0, 0, time.UTC)

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO alert_episodes (alert_id, rule_id, subject_key, episode_num, state, severity, condition_fingerprint, opened_seq, opened_at, last_observed_seq, last_observed_at, bundle_json, settlement_state)
			VALUES ('alrt-conc-1', 'drain-item-fault', 'subj-1', 1, 'open', 'high', 'fp1', 1, ?, 1, ?, '{}', 'uninvestigated'),
			       ('alrt-conc-2', 'drain-item-fault', 'subj-2', 1, 'open', 'high', 'fp2', 2, ?, 2, ?, '{}', 'uninvestigated')`,
			t0.UnixMilli(), t0.UnixMilli(), t0.UnixMilli(), t0.UnixMilli())
		return err
	}); err != nil {
		t.Fatalf("seed alerts: %v", err)
	}

	type result struct {
		itemID       string
		deduplicated bool
		err          error
	}
	results := make([]result, 2)
	start := make(chan struct{})
	var wg sync.WaitGroup
	filings := []item.RequestFiling{
		{
			Words:       []byte("Concurrent filing 1 for shared bug.\n"),
			Title:       "Fix shared bug (run 1)",
			Seat:        "build",
			Workflow:    item.SingleWorkerWorkflow,
			Alerts:      []string{"alrt-conc-1"},
			FiledBy:     "tillr-triage",
			DedupKey:    "drain-item-fault:shared-bug",
			Destination: item.Destination{To: "last"},
			At:          t0.Add(time.Second),
			Via:         "cli",
		},
		{
			Words:       []byte("Concurrent filing 2 for shared bug.\n"),
			Title:       "Fix shared bug (run 2)",
			Seat:        "build",
			Workflow:    item.SingleWorkerWorkflow,
			Alerts:      []string{"alrt-conc-2"},
			FiledBy:     "tillr-triage",
			DedupKey:    "drain-item-fault:shared-bug",
			Destination: item.Destination{To: "last"},
			At:          t0.Add(2 * time.Second),
			Via:         "cli",
		},
	}

	for i := range filings {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			<-start
			err := s.WriteTx(ctx, func(tx *store.Tx) error {
				env, err := item.FileRequest(ctx, tx, filings[idx])
				if err != nil {
					return err
				}
				var body struct {
					ItemID       string `json:"item_id"`
					Deduplicated bool   `json:"deduplicated"`
				}
				if err := json.Unmarshal(env.Payload, &body); err != nil {
					return err
				}
				results[idx] = result{itemID: body.ItemID, deduplicated: body.Deduplicated}
				return nil
			})
			results[idx].err = err
		}(i)
	}
	close(start)
	wg.Wait()

	for i, r := range results {
		if r.err != nil {
			t.Fatalf("goroutine %d failed: %v", i, r.err)
		}
		if r.itemID == "" {
			t.Fatalf("goroutine %d returned empty item_id", i)
		}
	}
	if results[0].itemID != results[1].itemID {
		t.Fatalf("concurrent filings created two items: %s vs %s", results[0].itemID, results[1].itemID)
	}
	if results[0].deduplicated == results[1].deduplicated {
		t.Fatalf("expected exactly one deduplicated response, got %+v", results)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var count int
		if err := r.QueryRow(`SELECT count(*) FROM items`).Scan(&count); err != nil {
			return err
		}
		if count != 1 {
			t.Fatalf("expected 1 item row, got %d", count)
		}
		shown, err := item.ShowWithQuestions(r, results[0].itemID, root, vantage.Preloaded(root, vantage.ParseIndex([]byte(`{"index":{"documents":[]}}`), "fixture")))
		if err != nil {
			return err
		}
		var payload struct {
			FiledBy      string              `json:"filed_by"`
			DedupKey     string              `json:"dedup_key"`
			LinkedAlerts []watch.LinkedAlert `json:"linked_alerts"`
		}
		if err := json.Unmarshal(shown.Payload, &payload); err != nil {
			return err
		}
		if len(payload.LinkedAlerts) != 2 {
			t.Fatalf("expected 2 linked_alerts carrying both alert ids, got %+v", payload.LinkedAlerts)
		}
		got := map[string]bool{
			payload.LinkedAlerts[0].AlertID: true,
			payload.LinkedAlerts[1].AlertID: true,
		}
		if !got["alrt-conc-1"] || !got["alrt-conc-2"] {
			t.Fatalf("missing alert id in linked_alerts: %+v", payload.LinkedAlerts)
		}
		return nil
	}); err != nil {
		t.Fatalf("verify concurrent dedup: %v", err)
	}
}

func TestShowWithQuestionsOlderRequestWordsWithoutProvenanceColumns(t *testing.T) {
	ctx := context.Background()
	s := syncStore(t)
	root := scratch(t)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES ('item-old-req','Old req','in-queue',0)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`ALTER TABLE request_words DROP COLUMN filed_by`); err != nil {
			return err
		}
		if _, err := tx.Exec(`ALTER TABLE request_words DROP COLUMN dedup_key`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO request_words(item_id, words_hash, words, input_hash) VALUES ('item-old-req','h1',X'6869','i1')`)
		return err
	}); err != nil {
		t.Fatalf("seed legacy request_words: %v", err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		shown, err := item.ShowWithQuestions(r, "item-old-req", root, vantage.Preloaded(root, vantage.ParseIndex([]byte(`{"index":{"documents":[]}}`), "fixture")))
		if err != nil {
			return err
		}
		var fields map[string]json.RawMessage
		if err := json.Unmarshal(shown.Payload, &fields); err != nil {
			return err
		}
		if _, ok := fields["filed_by"]; ok {
			t.Fatalf("expected filed_by omitted on legacy table: %s", shown.Payload)
		}
		if _, ok := fields["dedup_key"]; ok {
			t.Fatalf("expected dedup_key omitted on legacy table: %s", shown.Payload)
		}
		return nil
	}); err != nil {
		t.Fatalf("ReadTx: %v", err)
	}
}
