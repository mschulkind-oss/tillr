package main

import (
	"strings"
	"testing"
)

func TestWaitingRewriteShowsBothTipsAndRestorationMover(t *testing.T) {
	h := newServeHarness(t)
	old, new := strings.Repeat("a", 40), strings.Repeat("b", 40)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES('rewrite','rewrite','in-queue',0)`)
	h.exec(`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES('rewrite','rewrite','hold','trunk-rewritten: ` + old + ` -> ` + new + `',1000)`)
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("%d: %s", status, raw)
	}
	for _, v := range body["data"].([]any) {
		row := v.(map[string]any)
		if row["item_id"] != "rewrite" {
			continue
		}
		if row["class"] != "needs you" || row["where"] != "trunk" || row["mover"] != "restore the trunk's history" || row["why"] != "trunk-rewritten: "+old+" -> "+new {
			t.Fatalf("rewrite wait: %v", row)
		}
		return
	}
	t.Fatal("rewrite candidate missing from waiting face")
}
