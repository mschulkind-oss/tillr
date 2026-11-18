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

func orderRecordFixture(t *testing.T, record string) string {
	t.Helper()
	root := scratch(t)
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[order]\nrecord='order.txt'\n"), 0600); err != nil {
		t.Fatal(err)
	}
	writeOrderRecord(t, root, record)
	return root
}
func writeOrderRecord(t *testing.T, root, record string) {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "order.txt"), []byte(record), 0600); err != nil {
		t.Fatal(err)
	}
}
func orderBlock(id, from string) string {
	mark := ""
	if from != "" {
		mark = fmt.Sprintf(" from=\"%s\"", from)
	}
	return fmt.Sprintf("### %s\n<!-- tillr: item id=\"%s\" paths=\"tools/\"%s -->\n- **c1:** Works\n", id, id, mark)
}

func TestSyncOrderRecordRebuildAndPrecedence(t *testing.T) {
	for _, extra := range []bool{false, true} {
		t.Run(fmt.Sprint(extra), func(t *testing.T) {
			s := syncStore(t)
			root := orderRecordFixture(t, "# last_hand_move_event_seq: 42\nitem-c\nitem-a\nitem-b\n")
			content := orderBlock("item-b", "") + orderBlock("item-a", "item-b") + orderBlock("item-c", "")
			if extra {
				content = orderBlock("item-unlisted", "") + content
			}
			runSync(t, s, root, doc("docs/items.md", content))
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				text, err := item.OrderRecord(r)
				if err != nil {
					return err
				}
				if !strings.HasPrefix(text, "# last_hand_move_event_seq: 0\n") {
					t.Fatalf("sync counted as hand move: %s", text)
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			want := "item-c,item-a,item-b"
			if extra {
				want += ",item-unlisted"
			}
			if got := column(t, s, `SELECT group_concat(item_id,',') FROM (SELECT item_id FROM items ORDER BY rank,item_id)`); got != want {
				t.Fatalf("order %s want %s", got, want)
			}
		})
	}
}

func TestSyncOrderRecordAnchorsAndExistingItems(t *testing.T) {
	for _, tc := range []struct{ record, want string }{
		{"item-left\nitem-new\nitem-right\n", "item-left,item-new,item-right"},
		{"item-unknown\nitem-new\nitem-right\n", "item-left,item-new,item-right"},
		{"item-unknown\nitem-new\n", "item-left,item-right,item-new"},
	} {
		t.Run(tc.record, func(t *testing.T) {
			s := syncStore(t)
			exec(t, s, `INSERT INTO items(item_id,title,state,rank,updated_at) VALUES('item-left','Left','in-queue','a',0),('item-right','Right','in-queue','z',0)`)
			root := orderRecordFixture(t, tc.record)
			docs := doc("docs/items.md", orderBlock("item-new", ""))
			runSync(t, s, root, docs)
			if got := column(t, s, `SELECT group_concat(item_id,',') FROM (SELECT item_id FROM items ORDER BY rank,item_id)`); got != tc.want {
				t.Fatalf("order %s want %s", got, tc.want)
			}
			head := count(t, s, "SELECT count(*) FROM events")
			writeOrderRecord(t, root, "item-right\nitem-new\nitem-unknown\nitem-left\n")
			runSync(t, s, root, docs)
			if got := count(t, s, "SELECT count(*) FROM events"); got != head {
				t.Fatalf("hand edit wrote %d events", got-head)
			}
			if got := count(t, s, "SELECT count(*) FROM items WHERE item_id='item-unknown'"); got != 0 {
				t.Fatal("unknown record item was created")
			}
		})
	}
}

func TestSyncOrderRecordUnsetAndMissing(t *testing.T) {
	s := syncStore(t)
	root := scratch(t)
	writeOrderRecord(t, root, "item-b\nitem-a\n")
	docs := doc("docs/items.md", orderBlock("item-a", "")+orderBlock("item-b", ""))
	runSync(t, s, root, docs)
	if got := column(t, s, `SELECT group_concat(item_id,',') FROM (SELECT item_id FROM items ORDER BY rank,item_id)`); got != "item-a,item-b" {
		t.Fatal(got)
	}
	// An opted-in repository may not have landed its first record yet.
	other := syncStore(t)
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[order]\nrecord='not-yet-landed.txt'"), 0600); err != nil {
		t.Fatal(err)
	}
	runSync(t, other, root, docs)
}
