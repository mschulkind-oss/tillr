package runhistory

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	_ "github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

func openRunHistoryTestStore(t *testing.T) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), filepath.Join(t.TempDir(), "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { s.Close() })
	return s
}

func TestReadRepeatedCallsForRuns(t *testing.T) {
	ctx := context.Background()
	s := openRunHistoryTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	appendRC := func(tx *store.Tx, runID string, rc RepeatedCall) error {
		body, err := json.Marshal(rc)
		if err != nil {
			return err
		}
		id := runID
		_, err = tx.Append(event.Event{
			Kind:       RepeatingCallKind,
			ObjectType: "run",
			ObjectID:   &id,
			Payload:    body,
			At:         at,
		})
		return err
	}

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := appendRC(tx, "run-open-1", RepeatedCall{ItemID: "item-open", Tool: "bash", Route: "failover", NextBackend: "claude", Streak: 5}); err != nil {
			return err
		}
		if err := appendRC(tx, "run-open-2", RepeatedCall{ItemID: "item-open", Tool: "read", Route: RepeatActionFreshAttempt, Streak: 3}); err != nil {
			return err
		}
		return appendRC(tx, "run-closed-1", RepeatedCall{ItemID: "item-closed", Tool: "bash", Route: RepeatActionHold, Streak: 4})
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		empty, err := ReadRepeatedCallsForRuns(r, nil)
		if err != nil || len(empty) != 0 {
			t.Fatalf("empty = %v, %v", empty, err)
		}
		got, err := ReadRepeatedCallsForRuns(r, []string{"run-open-1", "run-open-2"})
		if err != nil {
			return err
		}
		if len(got) != 2 {
			t.Fatalf("len(got) = %d, want 2 (%+v)", len(got), got)
		}
		if got["run-open-1"].Route != RepeatActionHold || got["run-open-1"].NextBackend != "" {
			t.Errorf("legacy failover route not normalized: %+v", got["run-open-1"])
		}
		if got["run-open-2"].Route != RepeatActionFreshAttempt || got["run-open-2"].Tool != "read" {
			t.Errorf("run-open-2 = %+v", got["run-open-2"])
		}
		if _, ok := got["run-closed-1"]; ok {
			t.Errorf("unexpected run-closed-1 in scoped result")
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadRetriesAndAcksForItems(t *testing.T) {
	ctx := context.Background()
	s := openRunHistoryTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(
			`INSERT INTO retries(retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at) VALUES (?, ?, ?, ?, ?, ?)`,
			"retry-1", "item-open", "run-1", "owner", "first", at.UnixMilli(),
		); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO retries(retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at) VALUES (?, ?, ?, ?, ?, ?)`,
			"retry-2", "item-open", "run-2", "owner", "second", at.Add(time.Second).UnixMilli(),
		); err != nil {
			return err
		}
		if _, err := tx.Exec(
			`INSERT INTO retries(retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at) VALUES (?, ?, ?, ?, ?, ?)`,
			"retry-closed", "item-closed", "run-c1", "owner", "old", at.UnixMilli(),
		); err != nil {
			return err
		}
		ackBody, err := json.Marshal(RetryAckPayload{Acknowledge: "accepted-build"})
		if err != nil {
			return err
		}
		id := "retry-2"
		_, err = tx.Append(event.Event{
			Kind:       RetryAckKind,
			ObjectType: "retry",
			ObjectID:   &id,
			Payload:    ackBody,
			At:         at,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		emptyRetries, err := ReadRetriesForItems(r, nil)
		if err != nil || len(emptyRetries) != 0 {
			t.Fatalf("empty retries = %v, %v", emptyRetries, err)
		}
		emptyAcks, err := ReadRetryAcksFor(r, nil)
		if err != nil || len(emptyAcks) != 0 {
			t.Fatalf("empty acks = %v, %v", emptyAcks, err)
		}

		got, err := ReadRetriesForItems(r, []string{"item-open"})
		if err != nil {
			return err
		}
		if len(got) != 1 {
			t.Fatalf("len(got) = %d, want 1 (%+v)", len(got), got)
		}
		row := got["item-open"]
		if row.RetryID != "retry-2" || row.SupersedesRunID != "run-2" || row.Acknowledge != "accepted-build" {
			t.Errorf("row = %+v, want retry-2 with accepted-build ack", row)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReadItemFaultsForItems(t *testing.T) {
	ctx := context.Background()
	s := openRunHistoryTestStore(t)
	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, itemID := range []string{"item-open", "item-closed"} {
			id := itemID
			body, err := json.Marshal(map[string]any{"item_id": itemID, "reason": "dirty-worktree", "detail": "dirty"})
			if err != nil {
				return err
			}
			if _, err := tx.Append(event.Event{
				Kind:       faults.HeldKind,
				ObjectType: "item",
				ObjectID:   &id,
				Payload:    body,
				At:         at,
			}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		got, err := ReadItemFaultsForItems(r, []string{"item-open"}, nil)
		if err != nil {
			return err
		}
		if len(got) != 1 || got["item-open"].ItemID != "item-open" {
			t.Fatalf("got = %+v, want only item-open fault", got)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
