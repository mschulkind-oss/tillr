package main

import (
	"net/http"
	"testing"
)

func TestItemHistoryIncludesLostLeaseWithoutChangingUsage(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (3000,'run.allocate','run','history-lost','{"run_id":"history-lost","item_id":"item-seed-06","backend":"pi","lane_id":"lane-lost","started_at":"1970-01-01T00:00:03Z"}')`)
	status, body, raw := h.get("/api/v1/items/item-seed-06")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}
	data := body["data"].(map[string]any)
	rows := data["runs"].([]any)
	if len(rows) != 1 {
		t.Fatalf("lost allocation missing: %s", raw)
	}
	row := rows[0].(map[string]any)
	if row["run_id"] != "history-lost" || row["state"] == "running" || row["usage_tokens"] != nil || row["end_cause"] != nil {
		t.Fatalf("lost allocation must not invent a live lease or terminal data: %s", raw)
	}
	usage := data["usage"].(map[string]any)
	if usage["unrecorded_runs"] != float64(1) || usage["lower_bound"] != true {
		t.Fatalf("lost allocation no longer counted in usage: %s", raw)
	}
}

func TestItemHistoryIncludesLiveRun(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO lanes (lane_id,state,run_id,leased_until) VALUES ('lane-live','leased','history-live',900000)`)
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (3000,'run.allocate','run','history-live','{"run_id":"history-live","item_id":"item-seed-06","backend":"pi","lane_id":"lane-live","started_at":"1970-01-01T00:00:03Z"}')`)
	status, body, raw := h.get("/api/v1/items/item-seed-06")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, raw)
	}
	rows := body["data"].(map[string]any)["runs"].([]any)
	if len(rows) != 1 {
		t.Fatalf("live run missing: %s", raw)
	}
	row := rows[0].(map[string]any)
	for key, want := range map[string]any{"run_id": "history-live", "state": "running", "backend": "pi", "started_at_ms": float64(3000), "end_cause": nil, "cost_usd": nil, "finished_at_ms": float64(0)} {
		if row[key] != want {
			t.Errorf("%s = %v; want %v", key, row[key], want)
		}
	}
}
