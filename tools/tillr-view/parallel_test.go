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

func TestRunningViewDoesNotHideUnsettledReadersOrNonTerminalRuns(t *testing.T) {
	parallelViewScratch(t)
	h := newServeHarness(t)

	const (
		landedItem = "item-doc-triage-2026-11-18-tillr-roadmap-designs"
		liveItem   = "item-live-holder"
		laneID     = "lane-4"
	)

	h.exec(`
		CREATE TABLE IF NOT EXISTS read_only_runs(run_id TEXT PRIMARY KEY, group_id TEXT, item_id TEXT, lane_id TEXT, settled INTEGER);
		CREATE TABLE IF NOT EXISTS workflow_selections(item_id TEXT PRIMARY KEY, workflow TEXT, source TEXT, graph TEXT);
		CREATE TABLE IF NOT EXISTS node_runs(item_id TEXT, attempt INTEGER, node_id TEXT, run_id TEXT, outcome TEXT);

		INSERT INTO items(item_id, title, body_ref, state, design_doc, updated_at, allowed_paths)
		VALUES
			('item-doc-triage-2026-11-18-tillr-roadmap-designs', 'Triage roadmap designs', 'items/triage.md', 'landed', '', 2000, ''),
			('item-live-holder', 'Live holder', 'items/live.md', 'in-queue', '', 3000, '');

		INSERT INTO lanes(lane_id, state, run_id, leased_until)
		VALUES ('lane-4', 'leased', 'run-live-holder', 1763460000000);

		INSERT INTO runs(run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at, artifact_refs, event_seq)
		VALUES
			('run-old-nonterminal', 'item-doc-triage-2026-11-18-tillr-roadmap-designs', 'lane-4', 'pi', 'running', NULL, 1763427600000, 0, '[]', 10),
			('run-live-holder', 'item-live-holder', 'lane-4', 'pi', 'running', NULL, 1763452800000, 0, '[]', 11);

		INSERT INTO workflow_selections(item_id, workflow, source, graph)
		VALUES ('item-doc-triage-2026-11-18-tillr-roadmap-designs', 'test@v1', 'block', '{"stages":[{"id":"scout/a","parent":"scout","kind":"agent","seat":"scout","access":"read"}],"edges":[]}');

		INSERT INTO node_runs(item_id, attempt, node_id, run_id, outcome)
		VALUES ('item-doc-triage-2026-11-18-tillr-roadmap-designs', 1, 'scout/a', 'r-old-1', 'running');
	`)

	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()

	readerIDs := []string{"r-old-1", "r-old-2", "r-old-3", "r-old-4"}
	for _, id := range readerIDs {
		if _, err := db.Exec(`INSERT INTO read_only_runs(run_id, group_id, item_id, lane_id, settled) VALUES(?,?,?,?,0)`,
			id, "run-old-group", landedItem, laneID); err != nil {
			t.Fatal(err)
		}
		raw, err := json.Marshal(map[string]string{
			"run_id":     id,
			"lane_id":    laneID,
			"item_id":    landedItem,
			"backend":    "pi",
			"started_at": "2026-11-18T01:00:00Z",
		})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := db.Exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(1763427600000,'run.allocate','lane',?,?)`,
			laneID, string(raw)); err != nil {
			t.Fatal(err)
		}
	}

	now := time.Date(2026, 11, 18, 9, 0, 0, 0, time.UTC)

	// 1. Before settlement/repair: tillr-view must NOT hide unfinished runs for a landed item.
	before, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, now)
	if err != nil {
		t.Fatal(err)
	}
	lane4Active := map[string]runningRun{}
	for _, row := range before.Active {
		if row.LaneID == laneID {
			lane4Active[row.RunID] = row
		}
	}
	for _, id := range readerIDs {
		row, ok := lane4Active[id]
		if !ok {
			t.Fatalf("expected unsettled reader %s to remain visible in running view; got %+v", id, lane4Active)
		}
		if row.ItemID != landedItem {
			t.Fatalf("reader %s item = %q, want %q", id, row.ItemID, landedItem)
		}
		if row.LeaseNoExpiry || row.LeasedUntilMS != nil || row.LeaseLeftMS != nil {
			t.Fatalf("reader %s should have lease not recorded, got noExpiry=%v until=%v left=%v",
				id, row.LeaseNoExpiry, row.LeasedUntilMS, row.LeaseLeftMS)
		}
		if row.ElapsedMS == nil || *row.ElapsedMS <= 0 {
			t.Fatalf("reader %s elapsed = %v, want > 0", id, row.ElapsedMS)
		}
	}
	if _, ok := lane4Active["run-old-nonterminal"]; !ok {
		t.Fatalf("expected non-terminal run-old-nonterminal to remain visible; got %+v", lane4Active)
	}
	holder, ok := lane4Active["run-live-holder"]
	if !ok || holder.LeasedUntilMS == nil {
		t.Fatalf("expected current lease holder run-live-holder with lease recorded; got %+v", lane4Active)
	}

	// Verify item_nodes / lane_nodes shows running before supersession.
	status, _, raw := h.get("/api/v1/items/" + landedItem)
	if status != 200 {
		t.Fatalf("item get before: %d %s", status, raw)
	}
	var itemBody struct {
		Data struct {
			GraphAttempts []itemGraphAttempt `json:"graph_attempts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &itemBody); err != nil {
		t.Fatal(err)
	}
	if len(itemBody.Data.GraphAttempts) != 1 || len(itemBody.Data.GraphAttempts[0].Nodes) != 1 || itemBody.Data.GraphAttempts[0].Nodes[0].State != "running" {
		t.Fatalf("expected node state running before supersession, got %+v", itemBody.Data.GraphAttempts)
	}

	// 2. Simulate tillr-drain settling read_only_runs, timing out the non-terminal run,
	// and marking node_runs superseded.
	h.exec(`
		UPDATE read_only_runs SET settled = 1 WHERE item_id = 'item-doc-triage-2026-11-18-tillr-roadmap-designs';
		UPDATE runs SET state = 'timed-out', finished_at = 1763428000000 WHERE run_id = 'run-old-nonterminal';
		UPDATE node_runs SET outcome = 'superseded' WHERE item_id = 'item-doc-triage-2026-11-18-tillr-roadmap-designs';
	`)

	after, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, now)
	if err != nil {
		t.Fatal(err)
	}
	var afterLane4 []runningRun
	for _, row := range after.Active {
		if row.LaneID == laneID {
			afterLane4 = append(afterLane4, row)
		}
	}
	if len(afterLane4) != 1 || afterLane4[0].RunID != "run-live-holder" {
		t.Fatalf("expected only run-live-holder on %s after settlement/repair, got %+v", laneID, afterLane4)
	}

	// Verify item_nodes / lane_nodes maps outcome='superseded' to failed rather than running.
	status, _, raw = h.get("/api/v1/items/" + landedItem)
	if status != 200 {
		t.Fatalf("item get after: %d %s", status, raw)
	}
	if err := json.Unmarshal(raw, &itemBody); err != nil {
		t.Fatal(err)
	}
	if len(itemBody.Data.GraphAttempts) != 1 || len(itemBody.Data.GraphAttempts[0].Nodes) != 1 || itemBody.Data.GraphAttempts[0].Nodes[0].State != "failed" {
		t.Fatalf("expected node state failed for outcome='superseded', got %+v", itemBody.Data.GraphAttempts)
	}
}
