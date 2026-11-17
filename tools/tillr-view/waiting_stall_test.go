package main

import (
	"strings"
	"testing"
)

func TestWaitingGateStallNamesCheckObservationsAndGateCount(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES ('stall','item-seed-02','hold','gate-stall-rerun',3000)`)
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (3000,'drain.gate-stall','run','run-1','{"candidate":{"item_id":"item-seed-02","run_id":"run-1"},"gates_in_flight":4,"observations":[{"check_id":"documents","last_output_line":"heartbeat","cpu_time_ms":12,"silent_samples":60,"duration_ms":60000}]}')`)
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("waiting %d %s", status, raw)
	}
	var held map[string]any
	for _, v := range body["data"].([]any) {
		row := v.(map[string]any)
		if row["item_id"] == "item-seed-02" {
			held = row
		}
	}
	if held == nil || held["class"] != "waits on work" || held["where"] != "gate" {
		t.Fatalf("stall wait: %s", raw)
	}
	for _, want := range []string{"gate-stall-rerun", "documents", "heartbeat", "4 gates in flight"} {
		if !strings.Contains(held["why"].(string), want) {
			t.Errorf("stall wait hides %s: %v", want, held)
		}
	}
}
