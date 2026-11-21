package item_test

import (
	"context"
	"encoding/json"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestConflictReleasesOnlyItsOwnWait(t *testing.T) {
	for _, mode := range []string{"conflict", "legacy conflict", "operator"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			s, err := store.Open(ctx, filepath.Join(scratch(t), "state.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			at := time.UnixMilli(1000)
			err = s.WriteTx(ctx, func(tx *store.Tx) error {
				if _, err := tx.Exec("INSERT INTO items(item_id,title,state,updated_at) VALUES('item-a','Work','in-queue',1)"); err != nil {
					return err
				}
				c := item.Conflict{ConflictID: "conflict-a", ItemID: "item-a", RunID: "run-a", Text: "needs approval", WaitingOn: "owner", Status: "owner"}
				if err := item.RecordConflict(tx, c, at); err != nil {
					return err
				}
				switch mode {
				case "legacy conflict":
					if _, err := tx.Exec(`UPDATE items SET waiting_on='{"waiter":"owner","reason":"needs approval"}' WHERE item_id='item-a'`); err != nil {
						return err
					}
				case "operator":
					// Even identical text and waiter do not give the conflict ownership.
					if _, err := item.SetState(ctx, tx, c.ItemID, "blocked", at, item.WaitingOn{Waiter: c.WaitingOn, Reason: c.Text}); err != nil {
						return err
					}
				}
				c.Status, c.WaitingOn = "answered", "build"
				if err := item.RecordConflict(tx, c, at); err != nil {
					return err
				}
				var state string
				if err := tx.QueryRow("SELECT state FROM items WHERE item_id=?", c.ItemID).Scan(&state); err != nil {
					return err
				}
				waiting, _, err := item.ReadWaitingOn(tx.Reader(), c.ItemID)
				if err != nil {
					return err
				}
				if mode == "operator" {
					if state != "blocked" || waiting == nil || waiting.Waiter != "owner" || waiting.Reason != c.Text {
						t.Fatalf("operator's wait was changed: state=%s wait=%+v", state, waiting)
					}
				} else if state != "in-queue" || waiting != nil {
					t.Fatalf("conflict's wait still stands: state=%s wait=%+v", state, waiting)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

func TestConflictRecordsOnlyChangedTransitions(t *testing.T) {
	for _, change := range []string{"identical", "evidence", "body", "criteria", "waiter", "status", "design", "answer", "new identity"} {
		t.Run(change, func(t *testing.T) {
			ctx := context.Background()
			s, err := store.Open(ctx, filepath.Join(scratch(t), "state.db"), true)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			c := item.Conflict{ConflictID: "conflict-a", ItemID: "item-a", RunID: "run-a", BodyRef: "docs/work.md#work", Text: "first report", BlockedCriteria: []string{"c1"}, WaitingOn: "design", Status: "raised"}
			err = s.WriteTx(ctx, func(tx *store.Tx) error {
				if _, err := tx.Exec("INSERT INTO items(item_id,title,state,updated_at) VALUES('item-a','Work','in-queue',1)"); err != nil {
					return err
				}
				if err := item.RecordConflict(tx, c, time.UnixMilli(1000)); err != nil {
					return err
				}
				// A no-op must compare against the last record, including inside this transaction.
				switch change {
				case "evidence":
					c.Text += "\nsecond report"
					c.RunID = "run-b"
				case "body":
					c.BodyRef = "docs/other.md#work"
				case "criteria":
					c.BlockedCriteria = []string{"c2"}
				case "waiter":
					c.WaitingOn = "owner"
				case "status":
					c.Status = "awaiting"
				case "design":
					c.DesignRunID = "design-a"
				case "answer":
					c.AnswerHead = "answer-a"
				case "new identity":
					c.ConflictID = "conflict-b"
				}
				return item.RecordConflict(tx, c, time.UnixMilli(2000))
			})
			if err != nil {
				t.Fatal(err)
			}
			err = s.WriteTx(ctx, func(tx *store.Tx) error {
				return item.RecordConflict(tx, c, time.UnixMilli(3000))
			})
			if err != nil {
				t.Fatal(err)
			}
			err = s.ReadTx(ctx, func(r *store.Reader) error {
				var count int
				if err := r.QueryRow("SELECT count(*) FROM events WHERE kind=?", item.ConflictKind).Scan(&count); err != nil {
					return err
				}
				want := 2
				if change == "identical" {
					want = 1
				}
				if count != want {
					t.Errorf("conflict events=%d, want %d", count, want)
				}
				var raw []byte
				if err := r.QueryRow("SELECT payload FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1", item.ConflictKind).Scan(&raw); err != nil {
					return err
				}
				var last item.Conflict
				if err := json.Unmarshal(raw, &last); err != nil {
					return err
				}
				if last.Text != c.Text || last.RunID != c.RunID {
					t.Errorf("lost evidence: %+v", last)
				}
				return nil
			})
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}
