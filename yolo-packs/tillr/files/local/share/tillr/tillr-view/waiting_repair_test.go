package main

import (
	"strings"
	"testing"
)

func TestWaitingShowsRepairCauseAndConcreteRecoveryThenClearsIt(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`INSERT INTO runs (run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq)
 VALUES ('repair-original','item-seed-02','lane-a','pi','finished',1000,2000,'[]',11)`)
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES
 (3000,'drain.repair-held','run','repair-original','{"item_id":"item-seed-02","cause":"literal brace pair in conflict facts","next_action":"correct the brief template; next pass retries the retained candidate"}')`)
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("waiting: %d %s", status, raw)
	}
	rows := body["data"].([]any)
	var held map[string]any
	for _, value := range rows {
		row := value.(map[string]any)
		if row["item_id"] == "item-seed-02" {
			held = row
		}
	}
	if held == nil || held["class"] != "needs you" || held["where"] != "land" || !strings.Contains(held["why"].(string), "literal brace pair") || !strings.Contains(held["mover"].(string), "correct the brief template") {
		t.Fatalf("held repair is not actionable: %s", raw)
	}
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (4000,'drain.examined','run','repair-original','{}')`)
	status, _, raw = h.get("/api/v1/waiting")
	if status != 200 || strings.Contains(string(raw), "literal brace pair") {
		t.Fatalf("completed repair still held: %d %s", status, raw)
	}
}
