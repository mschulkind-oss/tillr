package main

import (
	"database/sql"
	"strings"
	"testing"
)

func TestWaitingOwnerCriterionDoesNotMaskFault(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`ALTER TABLE item_criteria ADD COLUMN claim_class TEXT NOT NULL DEFAULT 'verification-only'`)
	h.exec(`UPDATE items SET state='in-queue' WHERE item_id='item-seed-02'`)
	h.exec(`INSERT INTO item_criteria (item_id,criterion_id,text,state,claim_class) VALUES ('item-seed-02','c-owner','Choose the policy.','not-done','owner-only')`)
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(3000,'drain.item-fault','item','item-seed-02','{"item_id":"item-seed-02","run_id":"run-fault","lane_id":"lane-a","reason":"gate-stalled","detail":"fixture diagnosis"}')`)
	status, _, raw := h.get("/api/v1/waiting")
	if status != 200 || !strings.Contains(string(raw), "fixture diagnosis") || !strings.Contains(string(raw), "clear-fault") {
		t.Fatalf("fault hidden by owner hold: %d %s", status, raw)
	}
}

func TestWaitingOwnerCriterionAndDispatchableCount(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{
		`ALTER TABLE item_criteria ADD COLUMN claim_class TEXT NOT NULL DEFAULT 'verification-only'`,
		`UPDATE items SET state='in-queue' WHERE item_id='item-seed-02'`,
		`INSERT INTO item_criteria (item_id,criterion_id,text,state,claim_class) VALUES ('item-seed-02','c-owner','Choose the policy.' || char(10) || 'Details are not a first line.','not-done','owner-only')`,
		`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (1000,'item.record','item','item-seed-02','{"state":"in-queue"}')`,
		`DELETE FROM dispatches`,
		`DELETE FROM runs WHERE item_id='item-seed-02'`,
		`DELETE FROM candidates WHERE item_id='item-seed-02'`,
		`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES ('old-owner-run','item-seed-02','lane-b','pi','finished',1000,2000,'[]',20)`,
		`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES ('owner-hold','item-seed-02','hold','owner-only-owed',3000)`,
	} {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("waiting: %d %s", status, raw)
	}
	rows := body["data"].([]any)
	found := false
	for _, value := range rows {
		row := value.(map[string]any)
		if row["item_id"] != "item-seed-02" {
			continue
		}
		found = true
		if row["class"] != "needs you" || row["class"] == "unclassified" || row["mover"] != "the owner; rule on c-owner" || row["why"] != "owner-only-owed: c-owner: Choose the policy." || row["since"] != "1970-01-01T00:00:03Z" {
			t.Fatalf("waiting: %+v", row)
		}
	}
	if !found {
		t.Fatalf("owner item missing: %s", raw)
	}
	if _, err := db.Exec(`DELETE FROM runs WHERE run_id='old-owner-run'`); err != nil {
		t.Fatal(err)
	}
	status, body, raw = h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("snapshot: %d %s", status, raw)
	}
	// Only this item has known never-run queued history in the fixture.
	data := body["data"].(map[string]any)
	counts := data["counts"].(map[string]any)
	if counts["dispatchable"] != float64(0) {
		t.Fatalf("dispatchable includes owner-held item: %+v", counts)
	}
	if _, err := db.Exec(`UPDATE item_criteria SET state='done' WHERE criterion_id='c-owner'`); err != nil {
		t.Fatal(err)
	}
	status, body, raw = h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("released snapshot: %d %s", status, raw)
	}
	statusWaiting, _, rawWaiting := h.get("/api/v1/waiting")
	if statusWaiting != 200 || strings.Contains(string(rawWaiting), "owner-only-owed") {
		t.Fatalf("stale owner hold after ruling: %d %s", statusWaiting, rawWaiting)
	}
	counts = body["data"].(map[string]any)["counts"].(map[string]any)
	if counts["dispatchable"] != float64(1) {
		t.Fatalf("release still excluded: %+v", counts)
	}
}
