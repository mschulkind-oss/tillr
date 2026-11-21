package main

import (
	"strings"
	"testing"
)

func TestWaitingIgnoresRetiredIdentityDecisions(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES
		('obsolete','obsolete','in-queue',0), ('rewrite','rewrite','in-queue',0)`)
	hold := "trunk-rewritten: " + strings.Repeat("a", 40) + " -> " + strings.Repeat("b", 40)
	h.exec(`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES
		('rewrite-hold','rewrite','hold','` + hold + `',1000),
		('stale-after-rewrite','rewrite','hold','trunk-foreign-identity: old commit',2000),
		('stale-only','obsolete','hold','trunk-foreign-identity: old commit',2000)`)
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 || strings.Contains(string(raw), "trunk-foreign-identity") {
		t.Fatalf("retired hold in waiting response: %d %s", status, raw)
	}
	for _, v := range body["data"].([]any) {
		row := v.(map[string]any)
		if row["item_id"] == "rewrite" {
			if row["why"] != hold || row["class"] != "needs you" {
				t.Fatalf("valid hold hidden: %v", row)
			}
			return
		}
	}
	t.Fatal("valid hold missing")
}
