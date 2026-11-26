package item_test

import (
	"context"
	"encoding/json"
	"path/filepath"
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
