package main

import "testing"

func TestWaitingUnprovedTrunkNamesTheHeavyRunner(t *testing.T) {
	rule := waitingRuleFor("trunk-unproved")
	if rule.class != "waits on work" || rule.where != "trunk" || rule.mover != "the heavy runner; prove the trunk" {
		t.Fatalf("rule=%+v", rule)
	}
}

func TestWaitingBrokenTrunkNamesTheFixer(t *testing.T) {
	rule := waitingRuleFor("trunk-broken")
	if rule.mover != "the trunk fixer to land, or a heavy proof green past its red commit" || rule.where != "trunk" {
		t.Fatalf("rule=%+v", rule)
	}
	h := newServeHarness(t)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`ALTER TABLE dispatches ADD COLUMN deferred_by TEXT`)
	h.exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES('tree-red','tree red','in-queue',0)`)
	h.exec(`INSERT INTO dispatches(decision_id,item_id,action,reason,deferred_by,dispatched_at) VALUES('tree-red','tree-red','hold','trunk-broken','fixer-main-abcdef',1000)`)
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("%d: %s", status, raw)
	}
	for _, v := range body["data"].([]any) {
		row := v.(map[string]any)
		if row["item_id"] != "tree-red" {
			continue
		}
		if row["class"] != "waits on work" || row["where"] != "trunk" || row["mover"] != "drain; fixer-main-abcdef; check not recorded; waiting on repair (not waiting on owner)" {
			t.Fatalf("row=%v", row)
		}
		return
	}
	t.Fatal("broken trunk hold missing")
}
