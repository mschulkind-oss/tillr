package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func parallelViewScratch(t *testing.T) {
	t.Helper()
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TMPDIR", root)
}

func TestParallelReadersAppearTogetherOnOneLane(t *testing.T) {
	parallelViewScratch(t)
	allocations := map[string]allocation{}
	ids := []string{"n1", "n2", "n3", "n4"}
	for _, id := range ids {
		allocations[id] = allocation{RunID: id, ItemID: "item", LaneID: "lane", Backend: "pi", StartedAtMS: 1000}
	}
	lanes := []storeLane{{LaneID: "lane", State: laneStateLeased, RunID: "n1", ActiveNodes: ids}}
	if got := liveRuns(lanes, nil, allocations); len(got) != 4 {
		t.Fatalf("live: %+v", got)
	}
	d := &storeDomain{lanes: lanes, allocations: allocations}
	rows := buildLaneRows(d, nil, nil, t.TempDir(), t.TempDir(), time.Now())
	if len(rows) != 1 || rows[0].RunningNodes != 4 || rows[0].State != "running" {
		t.Fatalf("lane: %+v", rows)
	}
	// Finishing the lease's representative does not hide the remaining readers.
	lanes[0].ActiveNodes = ids[1:]
	known := []storeRun{{RunID: "n1", State: "exited"}}
	if got := liveRuns(lanes, known, allocations); len(got) != 3 {
		t.Fatalf("remaining: %+v", got)
	}
}

func TestParallelReadersContinuationKeepsOneLiveRunRow(t *testing.T) {
	lanes := []storeLane{{LaneID: "lane", State: laneStateLeased, RunID: "n1", ActiveNodes: []string{"n2"}}}
	code := 124
	known := []storeRun{{RunID: "n1", State: "exited"}, {RunID: "n2", State: "exited", FinishedAtMS: 2000, ExitCode: &code}}
	if got := liveRuns(lanes, known, map[string]allocation{}); len(got) != 0 {
		t.Fatalf("duplicate continued run: %+v", got)
	}
	if known[0].State != "exited" || known[1].State != liveRunState || known[1].FinishedAtMS != 0 || known[1].ExitCode != nil {
		t.Fatalf("bad continuation projection: %+v", known)
	}
}

func TestParallelReadersWaitingCountsNodes(t *testing.T) {
	parallelViewScratch(t)
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, statement := range []string{`CREATE TABLE read_only_runs(run_id TEXT PRIMARY KEY,group_id TEXT,item_id TEXT,lane_id TEXT,settled INTEGER)`, `INSERT INTO lanes(lane_id,state,run_id) VALUES('parallel-lane','leased','n1')`} {
		if _, err = db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	for i := 1; i <= 4; i++ {
		id := fmt.Sprintf("n%d", i)
		if _, err = db.Exec(`INSERT INTO read_only_runs VALUES(?,?,?,?,0)`, id, "n1", "item-seed-02", "parallel-lane"); err != nil {
			t.Fatal(err)
		}
		raw, _ := json.Marshal(map[string]string{"run_id": id, "lane_id": "parallel-lane", "item_id": "item-seed-02", "backend": "pi", "started_at": "2026-11-17T00:00:00Z"})
		if _, err = db.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(1000,'run.allocate','lane','parallel-lane',?)`, string(raw)); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ItemID == "item-seed-02" {
			if !strings.Contains(row.Why, "4 nodes running") {
				t.Fatalf("waiting: %+v", row)
			}
			return
		}
	}
	t.Fatal("missing item")
}
