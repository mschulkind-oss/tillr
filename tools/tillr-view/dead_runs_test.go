package main

import (
	"context"
	"encoding/json"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/presence"
)

func foreignOwnerForTest(t *testing.T, rebooted bool) presence.Owner {
	t.Helper()
	here, err := presence.CurrentOwner()
	if err != nil {
		t.Fatal(err)
	}
	boot, ns, ok := strings.Cut(here.Domain, ":")
	if !ok {
		t.Fatalf("unexpected domain %q", here.Domain)
	}
	foreign := *here
	if rebooted {
		foreign.Domain = "dead-boot-0000:" + ns
	} else {
		foreign.Domain = boot + ":" + ns + "-foreign"
	}
	foreign.PID = 999999
	return foreign
}

func seedDeadRunEvents(t *testing.T, h *serveHarness, runID, itemID, laneID string, owner presence.Owner, startedAtMS int64, resumedAt string) {
	t.Helper()
	allocPayload, err := json.Marshal(map[string]any{
		"run_id":     runID,
		"item_id":    itemID,
		"lane_id":    laneID,
		"backend":    "pi",
		"started_at": time.UnixMilli(startedAtMS).UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	startedPayload, err := json.Marshal(map[string]any{
		"loop_owner": owner,
		"decision": map[string]any{
			"item_id": itemID,
			"lane_id": laneID,
			"run_request": map[string]any{
				"run_id":  runID,
				"item_id": itemID,
				"lane_id": laneID,
				"backend": "pi",
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES(?, 'run.allocate', 'lane', ?, ?)`,
		startedAtMS, laneID, string(allocPayload))
	h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES(?, 'drain.started', 'run', ?, ?)`,
		startedAtMS, runID, string(startedPayload))
	if resumedAt != "" {
		resumePayload, err := json.Marshal(map[string]any{
			"run_id":     runID,
			"item_id":    itemID,
			"lane_id":    laneID,
			"backend":    "pi",
			"session_id": "11111111-1111-1111-1111-111111111111",
			"resumed_at": resumedAt,
		})
		if err != nil {
			t.Fatal(err)
		}
		h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES(?, 'run.resume', 'run', ?, ?)`,
			startedAtMS+500, runID, string(resumePayload))
	}
}

func seedDeadSession(t *testing.T, h *serveHarness, sessionID string, owner presence.Owner, now int64) {
	t.Helper()
	rawOwner, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	h.exec(`CREATE TABLE IF NOT EXISTS drain_sessions(
		session_id TEXT PRIMARY KEY,
		started_at INTEGER NOT NULL,
		refreshed_at INTEGER NOT NULL,
		next_pass_at INTEGER NOT NULL,
		loop_owner TEXT,
		ended_at INTEGER
	)`)
	h.exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, loop_owner) VALUES(?, ?, ?, ?, ?)`,
		sessionID, now-10000, now, now+60000, string(rawOwner))
}

func TestDeadForeignFreshRunFilteredBeforeReap(t *testing.T) {
	h := newServeHarness(t)
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	h.setNow(at)
	now := at.UnixMilli()
	foreign := foreignOwnerForTest(t, true)

	h.exec(`DELETE FROM lanes; DELETE FROM runs;`)
	h.exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES('lane-1', 'leased', 'run-dead-fresh', NULL)`)
	seedDeadRunEvents(t, h, "run-dead-fresh", "item-seed-02", "lane-1", foreign, now-5000, "")
	seedDeadSession(t, h, "sess-dead-1", foreign, now)

	// tillr-view running
	running, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, time.UnixMilli(now))
	if err != nil {
		t.Fatal(err)
	}
	if running.Loop.Active {
		t.Fatalf("running reported dead loop session as active: %+v", running.Loop)
	}
	if running.Loop.Session == nil || running.Loop.Session.EndedAtMS == nil {
		t.Fatalf("running did not mark dead session ended: %+v", running.Loop.Session)
	}
	if running.Lanes.Leased != 0 || len(running.Active) != 0 {
		t.Fatalf("running reported dead leased run as active: lanes=%+v active=%+v", running.Lanes, running.Active)
	}

	// tillr-view lanes
	laneRows, err := readLaneRows(context.Background(), h.store, h.loopDir, h.laneRoot, time.UnixMilli(now))
	if err != nil {
		t.Fatal(err)
	}
	if len(laneRows) != 1 || laneRows[0].State != "idle" || laneRows[0].Outcome != "interrupted" {
		t.Fatalf("lanes = %+v, want state=idle outcome=interrupted", laneRows)
	}

	// tillr-view serve: /api/v1/snapshot
	status, env, raw := h.get("/api/v1/snapshot")
	if status != http.StatusOK {
		t.Fatalf("snapshot: %d %s", status, raw)
	}
	data := env["data"].(map[string]any)
	summary := data["drain_summary"].(map[string]any)
	if summary["active"] != false {
		t.Fatalf("snapshot drain_summary.active = %v, want false: %s", summary["active"], raw)
	}
	if summary["running_runs"] != float64(0) {
		t.Fatalf("snapshot running_runs = %v, want 0: %s", summary["running_runs"], raw)
	}
	counts := data["counts"].(map[string]any)
	if counts["runs_live"] != float64(0) {
		t.Fatalf("snapshot counts.runs_live = %v, want 0: %s", counts["runs_live"], raw)
	}
	snapLanes := data["lanes"].([]any)
	if len(snapLanes) != 1 || snapLanes[0].(map[string]any)["state"] != "idle" {
		t.Fatalf("snapshot lanes = %v, want idle: %s", snapLanes, raw)
	}

	// tillr-view serve: /api/v1/lanes
	status, env, raw = h.get("/api/v1/lanes")
	if status != http.StatusOK {
		t.Fatalf("lanes: %d %s", status, raw)
	}
	apiLanes := env["data"].([]any)
	if len(apiLanes) != 1 {
		t.Fatalf("api lanes = %v", apiLanes)
	}
	if apiLanes[0].(map[string]any)["state"] != "idle" || apiLanes[0].(map[string]any)["outcome"] != "interrupted" {
		t.Fatalf("api lane = %v, want state=idle outcome=interrupted", apiLanes[0])
	}

	// tillr-view serve: /api/v1/items/item-seed-02
	status, env, raw = h.get("/api/v1/items/item-seed-02")
	if status != http.StatusOK {
		t.Fatalf("item: %d %s", status, raw)
	}
	itemData := env["data"].(map[string]any)
	for _, r := range itemData["runs"].([]any) {
		if r.(map[string]any)["state"] == "running" {
			t.Fatalf("item run still running: %v", r)
		}
	}
}

func TestDeadResumedRunFilteredBeforeAndAfterReap(t *testing.T) {
	h := newServeHarness(t)
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	h.setNow(at)
	now := at.UnixMilli()
	foreign := foreignOwnerForTest(t, true)

	h.exec(`DELETE FROM lanes; DELETE FROM runs;`)
	h.exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES('lane-1', 'leased', 'run-dead-resumed', NULL)`)
	seedDeadRunEvents(t, h, "run-dead-resumed", "item-seed-02", "lane-1", foreign, now-10000, time.UnixMilli(now-2000).UTC().Format(time.RFC3339))
	// Earlier paused segment row has event_seq = 0 (older than run.resume).
	h.exec(`INSERT INTO runs(run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at, event_seq)
		VALUES('run-dead-resumed', 'item-seed-02', 'lane-1', 'pi', 'paused', 0, ?, ?, 0)`, now-10000, now-5000)

	checkViews := func(phase string) {
		t.Helper()
		running, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, time.UnixMilli(now))
		if err != nil {
			t.Fatalf("%s running: %v", phase, err)
		}
		if running.Lanes.Leased != 0 || len(running.Active) != 0 {
			t.Fatalf("%s running active=%+v lanes=%+v", phase, running.Active, running.Lanes)
		}
		laneRows, err := readLaneRows(context.Background(), h.store, h.loopDir, h.laneRoot, time.UnixMilli(now))
		if err != nil {
			t.Fatalf("%s lanes: %v", phase, err)
		}
		if len(laneRows) != 1 || laneRows[0].State != "idle" || laneRows[0].Outcome != "interrupted" {
			t.Fatalf("%s lanes = %+v, want state=idle outcome=interrupted", phase, laneRows)
		}
		status, env, raw := h.get("/api/v1/snapshot")
		if status != http.StatusOK {
			t.Fatalf("%s snapshot: %d %s", phase, status, raw)
		}
		counts := env["data"].(map[string]any)["counts"].(map[string]any)
		if counts["runs_live"] != float64(0) {
			t.Fatalf("%s snapshot counts.runs_live = %v, want 0", phase, counts["runs_live"])
		}
		status, env, raw = h.get("/api/v1/items/item-seed-02")
		if status != http.StatusOK {
			t.Fatalf("%s item: %d %s", phase, status, raw)
		}
		itemData := env["data"].(map[string]any)
		for _, r := range itemData["runs"].([]any) {
			if r.(map[string]any)["state"] == "running" {
				t.Fatalf("%s item run still running: %v", phase, r)
			}
		}
	}

	checkViews("before-reap")

	// Simulate tillr-drain reaping the dead resumed run.
	h.exec(`UPDATE lanes SET state = 'idle', run_id = NULL, leased_until = NULL WHERE lane_id = 'lane-1'`)
	h.exec(`UPDATE runs SET state = 'interrupted', exit_code = NULL, finished_at = ?, event_seq = 999 WHERE run_id = 'run-dead-resumed'`, now-1000)

	checkViews("after-reap")
}

func TestDeadWorkflowNodeNotRunning(t *testing.T) {
	h := newServeHarness(t)
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	h.setNow(at)
	now := at.UnixMilli()
	foreign := foreignOwnerForTest(t, true)

	h.exec(`DELETE FROM lanes; DELETE FROM runs;`)
	h.exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES('lane-1', 'leased', 'run-dead-wf', NULL)`)
	h.exec(`CREATE TABLE IF NOT EXISTS node_runs(item_id TEXT NOT NULL, node_id TEXT NOT NULL, run_id TEXT NOT NULL, attempt INTEGER NOT NULL, outcome TEXT NOT NULL, PRIMARY KEY(item_id, node_id, attempt))`)
	h.exec(`INSERT INTO node_runs(item_id, node_id, run_id, attempt, outcome) VALUES('item-seed-02', 'build', 'run-dead-wf', 1, 'running')`)

	allocPayload, err := json.Marshal(map[string]any{
		"run_id":     "run-dead-wf",
		"item_id":    "item-seed-02",
		"lane_id":    "lane-1",
		"backend":    "pi",
		"started_at": time.UnixMilli(now - 5000).UTC().Format(time.RFC3339),
	})
	if err != nil {
		t.Fatal(err)
	}
	wfPayload, err := json.Marshal(map[string]any{
		"loop_owner": foreign,
		"decision": map[string]any{
			"item_id":    "item-seed-02",
			"lane_id":    "lane-1",
			"attempt":    1,
			"node_index": 0,
			"workflow": map[string]any{
				"stages": []map[string]any{{"id": "build", "kind": "agent", "seat": "build"}},
				"edges":  []any{},
			},
			"run_request": map[string]any{"run_id": "run-dead-wf", "item_id": "item-seed-02", "lane_id": "lane-1", "backend": "pi"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES(?, 'run.allocate', 'lane', 'lane-1', ?)`, now-5000, string(allocPayload))
	h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES(?, 'drain.started', 'run', 'run-dead-wf', ?)`, now-5000, string(wfPayload))

	status, env, raw := h.get("/api/v1/items/item-seed-02")
	if status != http.StatusOK {
		t.Fatalf("item: %d %s", status, raw)
	}
	attempts := env["data"].(map[string]any)["graph_attempts"].([]any)
	if len(attempts) == 0 {
		t.Fatalf("no graph_attempts: %s", raw)
	}
	nodes := attempts[0].(map[string]any)["nodes"].([]any)
	if len(nodes) == 0 || nodes[0].(map[string]any)["state"] == "running" {
		t.Fatalf("dead workflow node still running: %v", nodes)
	}
	if nodes[0].(map[string]any)["state"] != "failed" {
		t.Fatalf("dead workflow node state = %v, want failed", nodes[0].(map[string]any)["state"])
	}

	status, env, raw = h.get("/api/v1/lanes")
	if status != http.StatusOK {
		t.Fatalf("lanes: %d %s", status, raw)
	}
	laneNodes := env["data"].([]any)[0].(map[string]any)["nodes"].([]any)
	if len(laneNodes) == 0 || laneNodes[0].(map[string]any)["state"] != "failed" {
		t.Fatalf("lane node state = %v, want failed", laneNodes)
	}
}

func TestLiveForeignRunWithOpenStreamAndPresenceRemainsLive(t *testing.T) {
	h := newServeHarness(t)
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	h.setNow(at)
	now := at.UnixMilli()
	foreign := foreignOwnerForTest(t, false)

	identity, err := presence.StoreIdentity(h.store)
	if err != nil {
		t.Fatal(err)
	}
	presFile, err := presence.OpenLoopPresence(identity, foreign)
	if err != nil {
		t.Fatal(err)
	}
	defer presFile.Close()

	runDir := filepath.Join(h.loopDir, "run-live-foreign")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatal(err)
	}
	streamFile, err := os.OpenFile(filepath.Join(runDir, "run.stderr"), os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	defer streamFile.Close()

	h.exec(`DELETE FROM lanes; DELETE FROM runs;`)
	h.exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES('lane-1', 'leased', 'run-live-foreign', NULL)`)
	seedDeadRunEvents(t, h, "run-live-foreign", "item-seed-02", "lane-1", foreign, now-5000, "")
	seedDeadSession(t, h, "sess-live-foreign", foreign, now)

	// tillr-view running
	running, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, time.UnixMilli(now))
	if err != nil {
		t.Fatal(err)
	}
	if !running.Loop.Active {
		t.Fatalf("running hid live foreign loop session: %+v", running.Loop)
	}
	if running.Lanes.Leased != 1 || len(running.Active) != 1 || running.Active[0].RunID != "run-live-foreign" {
		t.Fatalf("running hid live foreign run: lanes=%+v active=%+v", running.Lanes, running.Active)
	}

	// tillr-view lanes
	laneRows, err := readLaneRows(context.Background(), h.store, h.loopDir, h.laneRoot, time.UnixMilli(now))
	if err != nil {
		t.Fatal(err)
	}
	if len(laneRows) != 1 || laneRows[0].State != "running" || laneRows[0].RunID != "run-live-foreign" {
		t.Fatalf("lanes = %+v, want state=running run_id=run-live-foreign", laneRows)
	}

	// tillr-view serve
	status, env, raw := h.get("/api/v1/snapshot")
	if status != http.StatusOK {
		t.Fatalf("snapshot: %d %s", status, raw)
	}
	data := env["data"].(map[string]any)
	if data["drain_summary"].(map[string]any)["active"] != true {
		t.Fatalf("snapshot active = false: %s", raw)
	}
	if data["counts"].(map[string]any)["runs_live"] != float64(1) {
		t.Fatalf("snapshot counts.runs_live = %v, want 1: %s", data["counts"], raw)
	}
	status, env, raw = h.get("/api/v1/items/item-seed-02")
	if status != http.StatusOK {
		t.Fatalf("item: %d %s", status, raw)
	}
	foundLive := false
	for _, r := range env["data"].(map[string]any)["runs"].([]any) {
		rm := r.(map[string]any)
		if rm["run_id"] == "run-live-foreign" && rm["state"] == "running" {
			foundLive = true
		}
	}
	if !foundLive {
		t.Fatalf("item runs missing live run-live-foreign: %v", env["data"].(map[string]any)["runs"])
	}
}
