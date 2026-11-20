package main

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"
)

// An adapter's refusal of a lane's run is a standing item fault (adapter-refused). The lanes
// view shows the adapter's own words on that lane's row, with the operator as mover.
func TestTheLanesViewShowsAnAdapterRefusalsWords(t *testing.T) {
	h := newServeHarness(t)
	rows, err := readLaneRows(context.Background(), h.store, h.loopDir, h.laneRoot, time.Now())
	if err != nil || len(rows) == 0 {
		t.Fatalf("fixture lanes: %+v / %v", rows, err)
	}
	lane := rows[0]
	if lane.Hold != nil {
		t.Fatalf("a lane nothing refused shows a hold: %+v", lane.Hold)
	}
	payload, _ := json.Marshal(map[string]string{
		"item_id": lane.ItemID, "run_id": lane.RunID, "lane_id": lane.LaneID,
		"reason": "adapter-refused",
		"detail": "the brief carries a literal {{ that is not a template placeholder",
	})
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (5000,'drain.item-fault','item','` + lane.ItemID + `','` + string(payload) + `')`)

	rows, err = readLaneRows(context.Background(), h.store, h.loopDir, h.laneRoot, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Hold == nil || !strings.Contains(rows[0].Hold.Detail, "carries a literal {{") || rows[0].Hold.Mover == "" {
		t.Fatalf("the lane row carries no hold with the adapter's words: %+v", rows[0])
	}
	var out bytes.Buffer
	if err := printLanes(&out, rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "adapter-refused: the brief carries a literal {{") {
		t.Fatalf("lanes does not print the adapter's words: %s", out.String())
	}
}
