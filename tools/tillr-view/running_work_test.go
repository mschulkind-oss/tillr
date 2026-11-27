package main

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestRunningWorkSummaryAndEndpoint(t *testing.T) {
	h := newServeHarness(t)
	laneRoot := t.TempDir()
	root := t.TempDir()
	h.srv.root = root
	h.srv.obs.laneRoot = laneRoot

	// Seed .tillr.toml with custom worktree bounds.
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[worktrees]\ndisk_budget_bytes = 1048576\nmax_count = 6\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Create one in-use worktree (lane-1) and one idle worktree (item-idle) on disk.
	if err := os.MkdirAll(filepath.Join(laneRoot, "lane-1", "worktree"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(laneRoot, "lane-1", "worktree", "file.txt"), make([]byte, 100), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(laneRoot, "lane-1", "scratch", "run-ord"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(laneRoot, "lane-1", "scratch", "run-ord", "out.bin"), make([]byte, 200), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(laneRoot, "item-idle", "worktree"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(laneRoot, "item-idle", "worktree", "idle.txt"), make([]byte, 50), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".tillr", "loop", "run-ord"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".tillr", "loop", "run-ord", "log.txt"), make([]byte, 40), 0o644); err != nil {
		t.Fatal(err)
	}

	// Set running-agent limit L=3 via drain_runtime_config.
	h.exec(`CREATE TABLE IF NOT EXISTS drain_runtime_config (key TEXT PRIMARY KEY, value_json TEXT NOT NULL, source TEXT NOT NULL, updated_at INTEGER NOT NULL, updated_by TEXT NOT NULL)`)
	h.exec(`INSERT INTO drain_runtime_config(key,value_json,source,updated_at,updated_by) VALUES('ordinary_lanes','3','operator',1000,'owner')`)

	// Add express column on items and mark item-seed-04 express=1.
	h.exec(`ALTER TABLE items ADD COLUMN express INTEGER NOT NULL DEFAULT 0`)
	h.exec(`UPDATE items SET express = 1 WHERE item_id = 'item-seed-04'`)

	// Seed 3 active runs: ordinary agent (lane-1), express agent (express-1), and command (lane-cmd).
	now := h.srv.clock()
	h.exec(`DELETE FROM lanes`)
	h.exec(`DELETE FROM runs`)
	h.exec(`ALTER TABLE runs ADD COLUMN seat TEXT`)
	h.exec(`ALTER TABLE runs ADD COLUMN node TEXT`)
	h.exec(`INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES
		('lane-1','leased','run-ord',?),
		('express-1','leased','run-exp',?),
		('lane-cmd','leased','run-cmd',?)`,
		now.Add(5*time.Minute).UnixMilli(),
		now.Add(5*time.Minute).UnixMilli(),
		now.Add(5*time.Minute).UnixMilli(),
	)
	h.exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,seat,node,state,started_at,finished_at,event_seq) VALUES
		('run-ord','item-seed-02','lane-1','pi','build','n1','running',?,0,0),
		('run-exp','item-seed-04','express-1','pi','build','n1','running',?,0,0),
		('run-cmd','item-seed-02','lane-cmd','command','','gate','running',?,0,0)`,
		now.Add(-20*time.Second).UnixMilli(),
		now.Add(-10*time.Second).UnixMilli(),
		now.Add(-5*time.Second).UnixMilli(),
	)

	// Check /api/v1/snapshot running_work summary.
	status, snapBody, snapRaw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("GET /api/v1/snapshot = %d: %s", status, snapRaw)
	}
	rwSnap := snapBody["data"].(map[string]any)["running_work"].(map[string]any)
	if rwSnap["ordinary_running"].(float64) != 1 {
		t.Fatalf("ordinary_running = %v, want 1", rwSnap["ordinary_running"])
	}
	if rwSnap["express_running"].(float64) != 1 {
		t.Fatalf("express_running = %v, want 1", rwSnap["express_running"])
	}
	if rwSnap["command_running"].(float64) != 1 {
		t.Fatalf("command_running = %v, want 1", rwSnap["command_running"])
	}
	if rwSnap["agent_limit"].(float64) != 3 {
		t.Fatalf("agent_limit = %v, want 3", rwSnap["agent_limit"])
	}
	wt := rwSnap["worktrees"].(map[string]any)
	if wt["on_disk"].(float64) != 2 || wt["in_use"].(float64) != 1 || wt["idle"].(float64) != 1 {
		t.Fatalf("worktrees counts = %#v, want on_disk=2 in_use=1 idle=1", wt)
	}
	if wt["count_limit"].(float64) != 6 || wt["disk_budget_bytes"].(float64) != 1048576 {
		t.Fatalf("worktrees bounds = %#v", wt)
	}
	if wt["worktree_bytes"].(float64) != 150 || wt["scratch_bytes"].(float64) != 200 || wt["total_bytes"].(float64) != 350 || wt["run_log_bytes"].(float64) != 40 {
		t.Fatalf("worktrees bytes = %#v, want worktree=150 scratch=200 total=350 run_log=40", wt)
	}

	// Check /api/v1/running-work rows.
	status, rwBody, rwRaw := h.get("/api/v1/running-work")
	if status != 200 {
		t.Fatalf("GET /api/v1/running-work = %d: %s", status, rwRaw)
	}
	rows := rwBody["data"].(map[string]any)["rows"].([]any)
	if len(rows) != 3 {
		t.Fatalf("running-work rows = %d, want 3", len(rows))
	}

	// Clear active runs and verify rows is empty [] and idle worktree is NOT counted as a running slot.
	h.exec(`UPDATE lanes SET state='idle', run_id=NULL, leased_until=NULL`)
	h.exec(`UPDATE runs SET state='finished', exit_code=0, finished_at=?`, now.UnixMilli())

	status, idleBody, idleRaw := h.get("/api/v1/running-work")
	if status != 200 {
		t.Fatalf("GET /api/v1/running-work (idle) = %d: %s", status, idleRaw)
	}
	idleData := idleBody["data"].(map[string]any)
	if len(idleData["rows"].([]any)) != 0 {
		t.Fatalf("idle running-work rows = %v, want []", idleData["rows"])
	}
	idleSummary := idleData["summary"].(map[string]any)
	if idleSummary["ordinary_running"].(float64) != 0 {
		t.Fatalf("idle ordinary_running = %v, want 0 (idle worktree must not count as slot)", idleSummary["ordinary_running"])
	}
}

func TestItemNodeExecutionsIncludeSegmentsLogLeaseEndingSpend(t *testing.T) {
	h := newServeHarness(t)
	laneRoot := t.TempDir()
	h.srv.obs.laneRoot = laneRoot

	// Create a pi stream log for run-exec-1.
	logDir := filepath.Join(laneRoot, "lane-1")
	if err := os.MkdirAll(logDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logPath := filepath.Join(logDir, "run-exec-1.log")
	if err := os.WriteFile(logPath, []byte(`{"type":"turn_end","message":{"usage":{"totalTokens":1500,"cost":{"total":0.12}}}}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	h.exec(`DELETE FROM lanes`)
	h.exec(`DELETE FROM runs`)
	h.exec(`INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES('lane-1','leased','run-exec-1',NULL)`)
	h.exec(`ALTER TABLE runs ADD COLUMN seat TEXT`)
	h.exec(`ALTER TABLE runs ADD COLUMN node TEXT`)
	h.exec(`ALTER TABLE runs ADD COLUMN end_cause TEXT`)
	h.exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,seat,node,state,exit_code,started_at,finished_at,usage_tokens,usage_cost_usd,end_cause,event_seq) VALUES
		('run-exec-1','item-seed-02','lane-1','pi','build','n1','running',NULL,1000,0,NULL,NULL,'',0)`)
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(2500,'run.resume','run','run-exec-1','{}')`)

	status, body, raw := h.get("/api/v1/items/item-seed-02")
	if status != 200 {
		t.Fatalf("GET /api/v1/items/item-seed-02 = %d: %s", status, raw)
	}
	attempts := body["data"].(map[string]any)["graph_attempts"].([]any)
	if len(attempts) == 0 {
		t.Fatalf("expected graph_attempts")
	}
	nodes := attempts[0].(map[string]any)["nodes"].([]any)
	execs := nodes[0].(map[string]any)["executions"].([]any)
	if len(execs) != 1 {
		t.Fatalf("executions len = %d, want 1", len(execs))
	}
	ex := execs[0].(map[string]any)
	if ex["segment_count"].(float64) != 2 {
		t.Fatalf("segment_count = %v, want 2", ex["segment_count"])
	}
	logObj := ex["log"].(map[string]any)
	if logObj["present"] != true || logObj["transcript_url"] != "#/run/run-exec-1" {
		t.Fatalf("log = %#v", logObj)
	}
	leaseObj := ex["lease"].(map[string]any)
	if leaseObj["active"] != true || leaseObj["lease_no_expiry"] != true || leaseObj["lane_id"] != "lane-1" {
		t.Fatalf("lease = %#v", leaseObj)
	}
	endingObj := ex["ending"].(map[string]any)
	if endingObj["state"] != "running" {
		t.Fatalf("ending = %#v", endingObj)
	}
	spendObj := ex["spend"].(map[string]any)
	if spendObj["live"] != true || spendObj["tokens"].(float64) != 1500 {
		t.Fatalf("spend = %#v", spendObj)
	}
}
