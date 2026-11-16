package item_test

import (
	"context"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSyncPreservesClosedDispositionAndBlockedMetadata(t *testing.T) {
	s := syncStore(t)
	original := doc("docs/design/plan.md", goodBlock)
	if report := runSync(t, s, "", original); len(report.Findings) != 0 {
		t.Fatalf("initial sync: %+v", report)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		if _, err := item.SetState(context.Background(), tx, "item-build-thing", "blocked", syncAt, item.WaitingOn{Waiter: "owner", Reason: "needs a ruling"}); err != nil {
			return err
		}
		_, err := item.Close(context.Background(), tx, "item-build-thing", "not wanted", "owner", syncAt)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	for _, content := range []string{goodBlock, strings.Replace(goodBlock, "The thing builds.", "The thing builds and runs.", 1)} {
		report := runSync(t, s, "", doc(original.Path, content))
		if len(report.Findings) != 0 {
			t.Fatalf("sync closed item: %+v", report)
		}
		if state := column(t, s, `SELECT state FROM items WHERE item_id = 'item-build-thing'`); state != "closed" {
			t.Fatalf("sync reopened item: %s", state)
		}
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := item.Reopen(context.Background(), tx, "item-build-thing", "wanted again", "owner", syncAt)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if state := column(t, s, `SELECT state FROM items WHERE item_id = 'item-build-thing'`); state != "blocked" {
		t.Fatalf("reopened state: %s", state)
	}
	if waiting := column(t, s, `SELECT waiting_on FROM items WHERE item_id = 'item-build-thing'`); !strings.Contains(waiting, "needs a ruling") {
		t.Fatalf("sync lost blocked reason: %s", waiting)
	}
}
