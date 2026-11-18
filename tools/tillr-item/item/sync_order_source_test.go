package item_test

import "testing"

func TestSyncOrderRecordPlacesFromAfterAllAppends(t *testing.T) {
	s := syncStore(t)
	root := orderRecordFixture(t, "item-c\n")
	runSync(t, s, root, doc("docs/items.md", orderBlock("item-a", "item-b")+orderBlock("item-b", "")+orderBlock("item-c", "")))
	if got := column(t, s, `SELECT group_concat(item_id,',') FROM (SELECT item_id FROM items ORDER BY rank,item_id)`); got != "item-c,item-b,item-a" {
		t.Fatalf("order %s want item-c,item-b,item-a", got)
	}
}
