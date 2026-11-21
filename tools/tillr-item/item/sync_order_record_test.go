package item_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func orderBlock(id, from string) string {
	mark := ""
	if from != "" {
		mark = fmt.Sprintf(" from=\"%s\"", from)
	}
	return fmt.Sprintf("### %s\n<!-- tillr: item id=\"%s\" paths=\"tools/\"%s -->\n- **c1:** Works\n", id, id, mark)
}

func TestSyncKeepsHandOrderOnlyInStore(t *testing.T) {
	s := syncStore(t)
	root := scratch(t)
	path := filepath.Join(root, "docs", "tillr", "queue-order.txt")
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("# last_hand_move_event_seq: 42\nitem-c\nitem-b\nitem-a\n"), 0600); err != nil {
		t.Fatal(err)
	}
	docs := doc("docs/items.md", orderBlock("item-a", "")+orderBlock("item-b", "")+orderBlock("item-c", ""))
	assertOrder := func(s *store.Store, want string) {
		t.Helper()
		if got := column(t, s, `SELECT group_concat(item_id,',') FROM (SELECT item_id FROM items ORDER BY rank,item_id)`); got != want {
			t.Fatalf("order %s, want %s", got, want)
		}
	}
	runSync(t, s, root, docs)
	assertOrder(s, "item-a,item-b,item-c")
	ctx := context.Background()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.Place(ctx, tx, "item-b", item.Destination{To: "first"}, syncAt, "owner", "cli")
		return err
	}); err != nil {
		t.Fatal(err)
	}
	withNew := doc("docs/items.md", string(docs.Content)+orderBlock("item-new", ""))
	runSync(t, s, root, withNew)
	assertOrder(s, "item-b,item-a,item-c,item-new")

	rebuilt := syncStore(t)
	runSync(t, rebuilt, root, withNew)
	assertOrder(rebuilt, "item-a,item-b,item-c,item-new")
}
