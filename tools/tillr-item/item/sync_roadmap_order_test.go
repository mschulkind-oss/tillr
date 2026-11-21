package item_test

import (
	"context"
	"encoding/json"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func roadmapOrderDocs() []item.SyncDocument {
	return []item.SyncDocument{
		doc("docs/a/roadmap.md", "## Next\n[One](one.md)\n"+fromBlock("item-a2", "")+"[Three](three.md)\n[Shared](../b/one.md)\n[Other roadmap](../b/roadmap.md)\n## Icebox\n[Parked](parked.md)\n"+fromBlock("item-parked-inline", "")),
		doc("docs/a/one.md", fromBlock("item-a1", "")),
		doc("docs/a/three.md", fromBlock("item-a3", "")),
		doc("docs/a/parked.md", fromBlock("item-parked", "")),
		doc("docs/b/roadmap.md", "## Next\n"+fromBlock("item-b1", "")+"[Shared](one.md)\n"),
		doc("docs/b/one.md", fromBlock("item-shared", "")),
		doc("docs/unlinked.md", fromBlock("item-unlinked-z", "")+fromBlock("item-unlinked-a", "")),
	}
}

func TestSyncRoadmapEntriesInterleaveAndPark(t *testing.T) {
	s := syncStore(t)
	docs := roadmapOrderDocs()
	// Reader input order is not roadmap path order.
	for i, j := 0, len(docs)-1; i < j; i, j = i+1, j-1 {
		docs[i], docs[j] = docs[j], docs[i]
	}
	report := runSync(t, s, "", docs...)
	if len(report.Findings) != 0 {
		t.Fatal(report.Findings)
	}
	assertFromOrder(t, s, "item-a1,item-b1,item-a2,item-a3,item-shared,item-unlinked-z,item-unlinked-a,item-parked,item-parked-inline")
	if n := count(t, s, `SELECT count(*) FROM items WHERE state='not-started' AND rank=''`); n != 2 {
		t.Fatalf("parked items: %d", n)
	}
	if n := count(t, s, `SELECT count(*) FROM items WHERE state='in-queue' AND (rank='' OR rank GLOB '*[^0-9a-z]*')`); n != 0 {
		t.Fatalf("invalid keys: %d", n)
	}
}

func TestSyncRoadmapAppendPreservesStoredKeys(t *testing.T) {
	s := syncStore(t)
	docs := []item.SyncDocument{doc("docs/roadmap.md", fromBlock("item-old-a", "")+fromBlock("item-old-b", ""))}
	runSync(t, s, "", docs...)
	exec(t, s, `UPDATE items SET rank='zz' WHERE item_id='item-old-b'`)
	before := column(t, s, `SELECT group_concat(item_id||'='||rank,',') FROM (SELECT * FROM items ORDER BY item_id)`)
	docs[0] = doc("docs/roadmap.md", fromBlock("item-new-first", "")+string(docs[0].Content))
	runSync(t, s, "", docs...)
	after := column(t, s, `SELECT group_concat(item_id||'='||rank,',') FROM (SELECT * FROM items WHERE item_id LIKE 'item-old-%' ORDER BY item_id)`)
	if before != after {
		t.Fatalf("stored keys %s -> %s", before, after)
	}
	assertFromOrder(t, s, "item-old-a,item-old-b,item-new-first")
	head := count(t, s, `SELECT count(*) FROM events`)
	runSync(t, s, "", docs...)
	if got := count(t, s, `SELECT count(*) FROM events`); got != head {
		t.Fatalf("repeat appended %d events", got-head)
	}
}

func TestSyncRoadmapThousandShortKeys(t *testing.T) {
	for _, last := range []string{"", "zz"} {
		t.Run(last, func(t *testing.T) {
			s := syncStore(t)
			if last != "" {
				exec(t, s, `INSERT INTO items(item_id,title,state,rank,updated_at) VALUES('item-held','Held','landed',?,0)`, last)
			}
			var blocks strings.Builder
			for i := 0; i < 1000; i++ {
				blocks.WriteString(fromBlock(fmt.Sprintf("item-%04d", i), ""))
			}
			runSync(t, s, "", doc("docs/roadmap.md", blocks.String()))
			if n := count(t, s, `SELECT count(*) FROM items WHERE item_id!='item-held' AND (rank<=? OR length(rank)>6 OR rank GLOB '*[^0-9a-z]*')`, last); n != 0 {
				t.Fatalf("invalid append keys: %d", n)
			}
			if n := count(t, s, `SELECT count(DISTINCT rank) FROM items WHERE item_id!='item-held'`); n != 1000 {
				t.Fatalf("distinct keys %d", n)
			}
			if got := column(t, s, `SELECT item_id FROM items WHERE item_id!='item-held' ORDER BY rank LIMIT 1`); got != "item-0000" {
				t.Fatal(got)
			}
		})
	}
}

func TestSyncRoadmapPlacedReportAndDryRun(t *testing.T) {
	s := fromFixture(t, "a", "z")
	exec(t, s, `INSERT INTO items(item_id,title,state,rank,updated_at) VALUES('item-legacy','Legacy','in-queue','',0)`)
	docs := []item.SyncDocument{doc("docs/roadmap.md", fromBlock("item-append", "")+fromBlock("item-child", "item-source"))}
	var dry item.SyncReport
	head := count(t, s, `SELECT count(*) FROM events`)
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		dry, err = item.Sync(context.Background(), r, nil, item.SyncInput{Documents: docs, At: syncAt})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if got := count(t, s, `SELECT count(*) FROM events`); got != head {
		t.Fatal("dry run wrote events")
	}
	report := runSync(t, s, "", docs...)
	placed := func(r item.SyncReport) any {
		env, err := item.SyncEnvelope(r, syncAt)
		if err != nil {
			t.Fatal(err)
		}
		var p map[string]any
		if err := json.Unmarshal(env.Payload, &p); err != nil {
			t.Fatal(err)
		}
		return p["placed"]
	}
	want := []any{map[string]any{"item_id": "item-child", "place": float64(2), "from": "item-source"}, map[string]any{"item_id": "item-legacy", "place": float64(4)}, map[string]any{"item_id": "item-append", "place": float64(5)}}
	if got := placed(report); !reflect.DeepEqual(got, want) {
		t.Fatalf("placed %v want %v", got, want)
	}
	if got := placed(dry); !reflect.DeepEqual(got, want) {
		t.Fatalf("dry placed %v want %v", got, want)
	}
	text := item.SyncText(report, syncAt)
	for _, line := range []string{"placed: item-child at 2 from item-source", "placed: item-legacy at 4", "placed: item-append at 5"} {
		if !strings.Contains(text, line) {
			t.Fatalf("missing %q in %s", line, text)
		}
	}
	assertFromOrder(t, s, "item-source,item-child,item-next,item-legacy,item-append")
}

func TestSyncRoadmapThreeEntriesAndUnlinkedDocument(t *testing.T) {
	s := syncStore(t)
	docs := []item.SyncDocument{
		doc("docs/a/roadmap.md", "## Next\n[One](one.md)\n"+fromBlock("item-a2", "")+"[Three](three.md)\n"),
		doc("docs/a/one.md", fromBlock("item-a1", "")),
		doc("docs/a/three.md", fromBlock("item-a3", "")),
		doc("docs/b/roadmap.md", fromBlock("item-b1", "")),
		doc("docs/unlinked.md", fromBlock("item-unlinked", "")),
	}
	runSync(t, s, "", docs...)
	assertFromOrder(t, s, "item-a1,item-b1,item-a2,item-a3,item-unlinked")
}

func TestSyncRoadmapLinkedBlocksStayTogether(t *testing.T) {
	s := syncStore(t)
	runSync(t, s, "",
		doc("docs/a/roadmap.md", "## Next\n[One](one.md)\n[One again](one.md#heading)\n```md\n[Ignored](../unlinked.md)\n```\n[Next](next.md)\n"),
		doc("docs/a/one.md", fromBlock("item-a1", "")+fromBlock("item-a2", "")),
		doc("docs/a/next.md", fromBlock("item-a3", "")),
		doc("docs/b/roadmap.md", fromBlock("item-b1", "")),
		doc("docs/unlinked.md", fromBlock("item-unlinked", "")),
	)
	assertFromOrder(t, s, "item-a1,item-a2,item-b1,item-a3,item-unlinked")
}

func TestSyncRoadmapDrySkipsRefusedFilePlacements(t *testing.T) {
	s := fromFixture(t, "a", "z")
	bad := strings.Replace(fromBlock("item-bad-a", ""), `paths="tools/thing/"`, `paths="tools/thing/" blocked_by="item-bad-b"`, 1) + strings.Replace(fromBlock("item-bad-b", ""), `paths="tools/thing/"`, `paths="tools/thing/" blocked_by="item-bad-a"`, 1)
	docs := []item.SyncDocument{doc("docs/a.md", bad), doc("docs/z.md", fromBlock("item-good", ""))}
	var dry item.SyncReport
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		dry, err = item.Sync(context.Background(), r, nil, item.SyncInput{Documents: docs, At: syncAt})
		return err
	}); err != nil {
		t.Fatal(err)
	}
	live := runSync(t, s, "", docs...)
	if !reflect.DeepEqual(dry.Placed, live.Placed) {
		t.Fatalf("dry placements %+v; live %+v", dry.Placed, live.Placed)
	}
	if len(live.Placed) != 1 || live.Placed[0].ItemID != "item-good" {
		t.Fatalf("live placements %+v", live.Placed)
	}
}

func TestSyncRoadmapParkedFromDoesNotGetAKey(t *testing.T) {
	s := fromFixture(t, "a", "a0")
	runSync(t, s, "", doc("docs/roadmap.md", "## Next\n"+fromBlock("item-child", "item-source")+"## Icebox\n"+fromBlock("item-parked", "item-source")))
	if got := column(t, s, `SELECT rank FROM items WHERE item_id='item-parked'`); got != "" {
		t.Fatalf("parked from got key %q", got)
	}
}
