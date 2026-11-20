package main

import (
	"bytes"
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// The attempt number numbers allocations, so an item whose dispatches ended before a worker
// started reads "attempt 8" before its first run. The lanes view and the item entry carry
// what the attempt number does not: the runs that started a worker (a run row exists), the
// attempts that reached a verify-seat worker, and the verify refusals.
func TestItemProgressCountsStartedRunsVerifyCyclesAndVerifyRefusals(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE node_runs(item_id TEXT,attempt INTEGER,node_id TEXT,run_id TEXT,outcome TEXT,seat TEXT);
INSERT INTO node_runs VALUES
 ('item-seed-04',1,'plan','run-2','passed','plan'),
 ('item-seed-04',1,'check','run-2','passed','verify'),
 ('item-seed-04',2,'check','run-never-started','running','verify'),
 ('item-seed-04',3,'plan','run-never-started-2','running','plan')`)
	h.exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES
 (5000,'drain.refusal','run','run-2','{"step":"verify-not-done"}'),
 (5001,'drain.refusal','run','run-2','{"step":"gate"}'),
 (5002,'drain.refusal','item','item-seed-04','{"step":"verify-not-done"}'),
 (5003,'drain.refusal','run','unstarted','{"step":"fence","item_id":"item-seed-04"}'),
 (5004,'drain.refusal','run','run-2','{"step":"conflict"}'),
 (5005,'drain.refusal','item','other','{"step":"gate"}')`)

	s, err := store.OpenRead(context.Background(), h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var got map[string]itemProgress
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		got, err = readItemProgress(r)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	want := itemProgress{RunsStarted: 1, VerifyCycles: 1, VerifyRefusals: 2, GateRefusals: 1, FenceRefusals: 1, ConflictRefusals: 1}
	if got["item-seed-04"] != want {
		t.Fatalf("item-seed-04 progress = %+v, want %+v (an allocation with no run row is not a started worker)", got["item-seed-04"], want)
	}

	rows, err := readLaneRows(context.Background(), h.store, h.loopDir, h.laneRoot, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	seen := false
	for _, row := range rows {
		if row.ItemID == "item-seed-04" {
			seen = true
			if row.Work != want {
				t.Fatalf("lane row work = %+v, want %+v", row.Work, want)
			}
		}
	}
	if !seen {
		t.Fatalf("fixture: no lane row names item-seed-04: %+v", rows)
	}
	var out bytes.Buffer
	if err := printLanes(&out, rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "1 runs started · 1 reached verify · 2 verify refusals") {
		t.Fatalf("lanes does not print the progress: %s", out.String())
	}

	status, item, raw := h.get("/api/v1/items/item-seed-04")
	if status != 200 || item["data"].(map[string]any)["work_summary"] != want.Text() {
		t.Fatalf("item response: %d %s", status, raw)
	}
	status, body, raw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("items: %d %s", status, raw)
	}
	data, _ := body["data"].(map[string]any)
	items, _ := data["items"].([]any)
	found := false
	for _, v := range items {
		it, _ := v.(map[string]any)
		if it["item_id"] != "item-seed-04" {
			continue
		}
		found = true
		work, _ := it["work"].(map[string]any)
		if work["runs_started"] != float64(1) || work["verify_cycles"] != float64(1) || work["verify_refusals"] != float64(2) {
			t.Fatalf("the item entry's work = %v, want 1 started, 1 verify cycle, 2 verify refusals", work)
		}
	}
	if !found {
		t.Fatalf("the snapshot lists no item-seed-04: %s", raw)
	}
}
