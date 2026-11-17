package item_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSyncResidueSnapshotTransactionAndPreview(t *testing.T) {
	s := syncStore(t)
	exec(t, s, `INSERT INTO items(item_id,title,state,updated_at) VALUES('private','Private','in-queue',?),('done','Done','landed',?)`, syncAt.Add(-time.Hour).UnixMilli(), syncAt.UnixMilli())
	docs := []item.SyncDocument{doc("docs/done.md", "### Done\n<!-- tillr: item id=\"done\" paths=\"docs/\" -->\n- **c1:** done\n")}
	runSync(t, s, "", docs...)
	query := `SELECT synced_at || '|' || unshared_open_ids || '|' || unshared_open_oldest || '|' || stale_doc_block_ids FROM item_sync_residue WHERE singleton=1`
	want := "2026-11-15T12:00:00Z|[\"private\"]|2026-11-15T11:00:00Z|[\"done\"]"
	if got := column(t, s, query); got != want {
		t.Fatalf("snapshot %q, want %q", got, want)
	}
	for _, mode := range []string{"dry", "partial", "rollback"} {
		err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
			writer := tx
			if mode == "dry" {
				writer = nil
			}
			_, err := item.Sync(context.Background(), tx.Reader(), writer, item.SyncInput{Documents: nil, At: syncAt.Add(time.Hour), Partial: mode == "partial"})
			if err != nil {
				return err
			}
			if mode == "rollback" {
				return errors.New("rollback")
			}
			return nil
		})
		if mode != "rollback" && err != nil {
			t.Fatal(err)
		}
		if got := column(t, s, query); got != want {
			t.Fatalf("%s replaced snapshot: %s", mode, got)
		}
	}
	events := count(t, s, `SELECT count(*) FROM events`)
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{Documents: docs, At: syncAt.Add(2 * time.Hour)})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := column(t, s, `SELECT synced_at FROM item_sync_residue`); got != "2026-11-15T14:00:00Z" {
		t.Fatalf("unchanged sync did not refresh time: %q", got)
	}
	if got := count(t, s, `SELECT count(*) FROM events`); got != events {
		t.Fatalf("unchanged sync added events: %d -> %d", events, got)
	}
	runSync(t, s, "", nil...)
	if got := column(t, s, `SELECT stale_doc_block_ids FROM item_sync_residue`); got != "[]" {
		t.Fatalf("replacement retained stale ids: %s", got)
	}
	if n := count(t, s, `SELECT count(*) FROM item_sync_residue`); n != 1 {
		t.Fatalf("snapshot rows: %d", n)
	}
	exec(t, s, `UPDATE items SET updated_at=0 WHERE item_id='private'`)
	runSync(t, s, "")
	if got := column(t, s, `SELECT unshared_open_oldest FROM item_sync_residue`); got != "1970-01-01T00:00:00Z" {
		t.Fatalf("epoch last write was lost: %q", got)
	}
}
