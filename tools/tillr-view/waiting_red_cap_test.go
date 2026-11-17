package main

import (
	"database/sql"
	"strings"
	"testing"
)

func TestWaitingTrunkUnprovedNamesHeavyRunner(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES ('proof-item','proof item','in-queue',0);
 INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES ('proof-hold','proof-item','hold','trunk-unproved',1000)`)
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("%d: %s", status, raw)
	}
	for _, v := range body["data"].([]any) {
		row := v.(map[string]any)
		if row["item_id"] == "proof-item" {
			if row["class"] != "waits on work" || row["where"] != "trunk" || row["mover"] != "the heavy runner; prove the trunk" {
				t.Fatalf("proof hold loses cause/mover: %v", row)
			}
			return
		}
	}
	t.Fatalf("proof item absent: %s", raw)
}

func TestWaitingRedCapNamesFixer(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`ALTER TABLE dispatches ADD COLUMN deferred_by TEXT;
 INSERT INTO items(item_id,title,state,updated_at) VALUES ('cap-item','cap item','in-queue',0);
 INSERT INTO dispatches(decision_id,item_id,action,reason,deferred_by,dispatched_at) VALUES ('cap-hold','cap-item','hold','red-cap: red_cap.landings=2 (2 since green); checks: unit; waiting on fixer-main-123456789012','fixer-main-123456789012',1000)`)
	if err != nil {
		t.Fatal(err)
	}
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("%d: %s", status, raw)
	}
	for _, v := range body["data"].([]any) {
		row := v.(map[string]any)
		if row["item_id"] == "cap-item" {
			if row["class"] != "waits on work" || row["where"] != "land" || row["mover"] != "fixer-main-123456789012" || !strings.Contains(row["why"].(string), "checks: unit") {
				t.Fatalf("cap wait loses cause/mover: %v", row)
			}
			return
		}
	}
	t.Fatalf("cap item absent: %s", raw)
}
