package main

import "testing"

func TestWaitingProseRewriteNamesReasonAndMover(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`ALTER TABLE dispatches ADD COLUMN deferred_by TEXT NOT NULL DEFAULT ''`)
	h.exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES('item-prose','prose','in-queue',0)`)
	h.exec(`INSERT INTO dispatches(decision_id,item_id,action,reason,deferred_by,dispatched_at) VALUES('prose','item-prose','hold','awaits-rewrite','rewrite-item-prose',1000)`)
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("%d: %s", status, raw)
	}
	for _, value := range body["data"].([]any) {
		row := value.(map[string]any)
		if row["item_id"] == "item-prose" {
			if row["why"] != "awaits-rewrite" || row["mover"] != "rewrite-item-prose" || row["class"] != "waits on work" {
				t.Fatalf("prose hold: %v", row)
			}
			return
		}
	}
	t.Fatal("missing prose hold")
}
