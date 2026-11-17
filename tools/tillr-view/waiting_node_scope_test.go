package main

import (
	"context"
	"strings"
	"testing"
)

func TestWaitingNodeScopesAreClassified(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES
 ('node-scope','item-seed-02','hold','node-scope-outside-fence: node build: outside/ is outside the item fence',1000),
 ('node-escape','item-seed-06','hold','boundary-scope-escape: node build: outside/file.txt',1000)`)
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	seen := 0
	for _, row := range rows {
		if row.ItemID == "item-seed-02" || row.ItemID == "item-seed-06" {
			seen++
			if row.Class != "needs you" || !strings.Contains(row.Why, "node build") || row.Mover == "" {
				t.Fatalf("unexplained scope hold: %+v", row)
			}
		}
	}
	if seen != 2 {
		t.Fatalf("found %d scope holds, want 2", seen)
	}
}
