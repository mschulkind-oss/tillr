package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// A lane the drain took out of rotation (drain.lane-refused) shows in the waiting view as
// one row naming the lane, the reason and the remedy, and on that lane's row in `tillr view
// lanes`; a later drain.lane-restored clears both.
func TestLaneChildrenWaitingNamesPIDsAndKillCommandUntilRestored(t *testing.T) {
	h := newServeHarness(t)
	rows, err := readLaneRows(context.Background(), h.store, h.loopDir, h.laneRoot, time.Now())
	if err != nil || len(rows) == 0 {
		t.Fatalf("fixture lanes: %+v / %v", rows, err)
	}
	lane := rows[0].LaneID
	payload, _ := json.Marshal(map[string]string{
		"lane_id": lane, "reason": "lane-run-children-live",
		"detail": "ended runs still have live marked processes in lane " + lane + ": PIDs 123, 456",
		"remedy": "wait for these processes to exit, or end them with `kill 123 456`; the next pass rechecks the lane",
	})
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (5000,'drain.lane-refused','lane','` + lane + `','` + string(payload) + `')`)

	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("waiting: %d %s", status, raw)
	}
	var row map[string]any
	for _, value := range body["data"].([]any) {
		if r := value.(map[string]any); r["item_id"] == "lane:"+lane {
			row = r
		}
	}
	if row == nil || row["class"] != "waits on work" || !strings.Contains(row["why"].(string), "lane-run-children-live") || !strings.Contains(row["why"].(string), "PIDs 123, 456") || !strings.Contains(row["mover"].(string), "kill 123 456") {
		t.Fatalf("the lane condition is not a waiting row naming the reason and remedy: %s", raw)
	}

	rows, err = readLaneRows(context.Background(), h.store, h.loopDir, h.laneRoot, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Condition == nil || rows[0].Condition.Reason != "lane-run-children-live" {
		t.Fatalf("the lane row carries no condition: %+v", rows[0])
	}
	var out bytes.Buffer
	if err := printLanes(&out, rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "out of rotation since") || !strings.Contains(out.String(), "kill 123 456") {
		t.Fatalf("lanes does not print the condition: %s", out.String())
	}

	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (6000,'drain.lane-restored','lane','` + lane + `','{"lane_id":"` + lane + `"}')`)
	_, _, raw = h.get("/api/v1/waiting")
	if strings.Contains(string(raw), "lane:"+lane) {
		t.Fatalf("a restored lane still waits: %s", raw)
	}
	rows, err = readLaneRows(context.Background(), h.store, h.loopDir, h.laneRoot, time.Now())
	if err != nil || rows[0].Condition != nil {
		t.Fatalf("a restored lane still carries its condition: %+v / %v", rows, err)
	}
}
