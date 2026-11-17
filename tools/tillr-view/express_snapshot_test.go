package main

import (
	"database/sql"
	"net/http"
	"testing"
)

func TestSnapshotExpressCountsDistinguishSettling(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	// The ordinary run is live; the express lane still holds a terminal run's lease.
	if _, err := db.Exec(`INSERT INTO lanes(lane_id,state,run_id) VALUES('express-1','leased','run-1')`); err != nil {
		t.Fatal(err)
	}
	status, payload, raw := h.get("/api/v1/snapshot")
	if status != http.StatusOK {
		t.Fatalf("snapshot: %d %s", status, raw)
	}
	data := payload["data"].(map[string]any)
	states := map[string]string{}
	for _, entry := range data["lanes"].([]any) {
		lane := entry.(map[string]any)
		if lane["state"] != "leased" {
			t.Fatalf("raw lease state changed: %+v", lane)
		}
		states[lane["lane_id"].(string)], _ = lane["work_state"].(string)
	}
	if states["lane-a"] != "running" || states["express-1"] != "settling" {
		t.Fatalf("snapshot work states: %+v", states)
	}
}
