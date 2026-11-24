package item_test

import (
	"context"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func TestPartialSyncParkingAndSourcePreviewMatchesWrite(t *testing.T) {
	s := fromFixture(t, "", strings.Repeat("a", 62))
	runSync(t, s, "", doc("docs/roadmap.md", "## Icebox\n[Park](park.md)\n"), doc("docs/park.md", fromBlock("item-park", "")))
	exec(t, s, `UPDATE items SET rank='' WHERE item_id='item-source'`)
	in := item.SyncInput{Partial: true, Roadmaps: []string{"docs/roadmap.md"}, At: syncAt, Documents: []item.SyncDocument{
		doc("docs/roadmap.md", "## Active\n[Source](source.md)\n## Icebox 🧊\n[Park](park.md)\n"),
		doc("docs/source.md", fromBlock("item-source", "")+fromBlock("item-child", "item-source")),
		doc("docs/park.md", fromBlock("item-park", "")),
	}}
	var preview, written item.SyncReport
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		preview, err = item.Sync(context.Background(), r, nil, in)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var err error
		written, err = item.Sync(context.Background(), tx.Reader(), tx, in)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	wantSkipped := []item.SyncSkippedBlock{{File: "docs/park.md", ItemID: "item-park"}}
	if len(written.Findings) != 0 || !reflect.DeepEqual(preview.Placed, written.Placed) || !reflect.DeepEqual(preview.SkippedBlocks, wantSkipped) || !reflect.DeepEqual(written.SkippedBlocks, wantSkipped) || written.SkippedCount != 1 {
		t.Fatalf("preview %+v, written %+v", preview, written)
	}
	if column(t, s, `SELECT rank FROM items WHERE item_id='item-park'`) != "" {
		t.Fatal("scoped sync keyed a skipped parked item")
	}
	if column(t, s, `SELECT rank FROM items WHERE item_id='item-next'`) != strings.Repeat("a", 62) {
		t.Fatal("placement changed unread rank")
	}
}

func TestPartialSyncParkingSkipsMissingKeysAndPreservesUnreadItems(t *testing.T) {
	s := syncStore(t)
	docs := []item.SyncDocument{doc("docs/roadmap.md", "## Active\n[Work](work.md)\n"), doc("docs/work.md", goodBlock)}
	runSync(t, s, "", docs...)
	exec(t, s, `UPDATE items SET rank='' WHERE item_id='item-build-thing'`)
	exec(t, s, `INSERT INTO items(item_id,title,state,rank,updated_at) VALUES('item-unread','Unread','in-queue','',0)`)
	docs[0] = doc("docs/roadmap.md", "## Icebox 🧊\n[Work](work.md)\n")
	before := count(t, s, `SELECT count(*) FROM events`)
	var report item.SyncReport
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var err error
		report, err = item.Sync(context.Background(), tx.Reader(), tx, item.SyncInput{
			Partial: true, Documents: docs, Roadmaps: []string{"docs/roadmap.md"}, At: syncAt,
		})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	parkingLists(t, report, "", "")
	wantSkipped := []item.SyncSkippedBlock{{File: "docs/work.md", ItemID: "item-build-thing"}}
	if len(report.Findings) != 0 || report.SkippedCount != 1 || !reflect.DeepEqual(report.SkippedBlocks, wantSkipped) {
		t.Fatalf("scoped skip failed: %+v", report)
	}
	if column(t, s, `SELECT state||'|'||rank FROM items WHERE item_id='item-build-thing'`) != "in-queue|" || count(t, s, `SELECT count(*) FROM events`) != before {
		t.Fatal("scoped sync touched a skipped parked item")
	}
	if column(t, s, `SELECT title||'|'||state||'|'||rank FROM items WHERE item_id='item-unread'`) != "Unread|in-queue|" {
		t.Fatal("scoped sync changed an unread item")
	}
	// A whole sync (Roadmaps empty) still parks Icebox items as not-started.
	whole := runSync(t, s, "", docs...)
	parkingLists(t, whole, "item-build-thing", "")
	if column(t, s, `SELECT state||'|'||rank FROM items WHERE item_id='item-build-thing'`) != "not-started|" {
		t.Fatalf("whole sync parking failed: %+v", whole)
	}
}

func TestPartialSyncPreviewAndWriteUseTheSameRankAllocation(t *testing.T) {
	for _, tc := range []struct {
		name, next  string
		includeHeld bool
	}{{"sparse", "c", false}, {"crowded-all-read", "a0", true}} {
		t.Run(tc.name, func(t *testing.T) {
			s := fromFixture(t, "a", tc.next)
			content := fromBlock("item-first", "item-source") + fromBlock("item-second", "item-source")
			if tc.includeHeld {
				content = "[Seed](seed.md)\n" + content
			}
			docs := []item.SyncDocument{doc("docs/roadmap.md", content)}
			if tc.includeHeld {
				docs = append(docs, doc("docs/seed.md", fromBlock("item-source", "")+fromBlock("item-next", "")))
			}
			in := item.SyncInput{Partial: true, Documents: docs, Roadmaps: []string{"docs/roadmap.md"}, At: syncAt}
			var preview, written item.SyncReport
			if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
				var err error
				preview, err = item.Sync(context.Background(), r, nil, in)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
				var err error
				written, err = item.Sync(context.Background(), tx.Reader(), tx, in)
				return err
			}); err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(preview.Placed, written.Placed) {
				t.Fatalf("preview %v, written %v", preview.Placed, written.Placed)
			}
			assertFromOrder(t, s, "item-source,item-first,item-second,item-next")
		})
	}
}
