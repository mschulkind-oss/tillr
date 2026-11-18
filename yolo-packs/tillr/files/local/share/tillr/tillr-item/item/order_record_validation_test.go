package item_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSyncOrderRecordInvalidAndDryRun(t *testing.T) {
	for _, text := range []string{"not-an-id\n", "item-a\nitem-a\n"} {
		t.Run(text, func(t *testing.T) {
			s := syncStore(t)
			root := orderRecordFixture(t, text)
			err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
				report, err := item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{Root: root, At: syncAt, Documents: []item.SyncDocument{doc("docs/a.md", orderBlock("item-a", ""))}})
				found := false
				for _, warning := range report.Warnings {
					if warning.Rule == "order-record-unusable" {
						found = true
					}
				}
				if err == nil && !found {
					t.Fatalf("missing record warning: %+v", report)
				}
				return err
			})
			if err != nil {
				t.Fatal(err)
			}
			if n := count(t, s, "SELECT count(*) FROM items"); n != 1 {
				t.Fatalf("unusable record blocked import: %d", n)
			}
		})
	}
	root := orderRecordFixture(t, "# last_hand_move_event_seq: 7\nitem-a\n")
	report, err := item.Sync(context.Background(), nil, nil, item.SyncInput{Root: root, At: syncAt, Documents: []item.SyncDocument{doc("docs/a.md", orderBlock("item-a", ""))}})
	if err != nil || !report.DryRun || report.CreatedCount != 1 {
		t.Fatalf("dry run: %+v %v", report, err)
	}
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[order]\nrecord='.'"), 0600); err != nil {
		t.Fatal(err)
	}
	if _, err := item.Sync(context.Background(), nil, nil, item.SyncInput{Root: root, At: syncAt}); err == nil {
		t.Fatal("invalid configured path accepted")
	}
}

func TestOrderRecordSyncDoesNotReplaceHandReceipt(t *testing.T) {
	s := syncStore(t)
	root := orderRecordFixture(t, "item-a\nitem-b\n")
	docs := doc("docs/items.md", orderBlock("item-a", "")+orderBlock("item-b", ""))
	runSync(t, s, root, docs)
	var seq int64
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := item.Place(context.Background(), tx, "item-b", item.Destination{To: "first"}, syncAt, "owner", "cli")
		if err != nil {
			return err
		}
		seq, err = tx.Reader().EventHeadSeq()
		return err
	}); err != nil {
		t.Fatal(err)
	}
	// A later import must not make a hand-order record stale.
	writeOrderRecord(t, root, "item-a\nitem-new\nitem-b\n")
	runSync(t, s, root, doc("docs/new.md", orderBlock("item-new", "")), docs)
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		text, err := item.OrderRecord(r)
		if err != nil {
			return err
		}
		if !strings.HasPrefix(text, fmt.Sprintf("# last_hand_move_event_seq: %d\n", seq)) {
			t.Fatalf("lost hand receipt: %s", text)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
