package item_test

import "testing"

func TestSyncPlacesFromAfterAllAppends(t *testing.T) {
	s := syncStore(t)
	root := scratch(t)
	runSync(t, s, root, doc("docs/items.md", orderBlock("item-a", "item-b")+orderBlock("item-b", "")+orderBlock("item-c", "")))
	if got := column(t, s, `SELECT group_concat(item_id,',') FROM (SELECT item_id FROM items ORDER BY rank,item_id)`); got != "item-b,item-a,item-c" {
		t.Fatalf("order %s want item-b,item-a,item-c", got)
	}
}
