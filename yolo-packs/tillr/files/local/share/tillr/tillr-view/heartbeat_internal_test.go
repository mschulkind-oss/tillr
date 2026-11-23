package main

import (
	"net/http"
	"testing"
	"time"
)

func TestSnapshotCountsRecordedPassesAndKeepsUngatedPassesOutOfGateTrend(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`ALTER TABLE drain_passes ADD COLUMN ended_at INTEGER;
	 ALTER TABLE drain_passes ADD COLUMN gate_count INTEGER;
	 ALTER TABLE dispatches ADD COLUMN pass_id TEXT;
	 INSERT INTO drain_passes(tick_id,started_at,stage_ms,dispatch_ms,gate_ms,land_ms,gate_budget_ms,gate_over_budget,ended_at,gate_count)
	 VALUES ('pass-3',2500,0,0,0,0,330000,0,2501,0);
	 INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at,pass_id)
	 VALUES ('new','item-seed-06','hold','blocked',2500,'pass-3')`)
	status, env, raw := h.get("/api/v1/snapshot")
	if status != http.StatusOK {
		t.Fatalf("snapshot: %d %s", status, raw)
	}
	summary := env["data"].(map[string]any)["drain_summary"].(map[string]any)
	if summary["ticks"] != float64(3) || len(summary["gate_trend"].([]any)) != 2 {
		t.Fatalf("pass count or gate trend includes an invented measurement: %s", raw)
	}
	if summary["last_decision"].(map[string]any)["pass_id"] != "pass-3" {
		t.Fatalf("decision lost its pass: %s", raw)
	}
}

func TestSnapshotLiveSessionHidesHistoricalStop(t *testing.T) {
	h := newServeHarness(t)
	writeLoopResult(t, h.loopDir, "20261113T065219Z", "operator-stop", nil)
	h.exec(`CREATE TABLE drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
	 refreshed_at INTEGER NOT NULL, next_pass_at INTEGER NOT NULL, ended_at INTEGER)`)
	now := time.Now().UnixMilli()
	h.exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at)
	 VALUES ('live', ?, ?, ?)`, now-30000, now, now+60000)
	status, env, raw := h.get("/api/v1/snapshot")
	if status != http.StatusOK {
		t.Fatalf("snapshot: %d %s", status, raw)
	}
	summary := env["data"].(map[string]any)["drain_summary"].(map[string]any)
	if summary["last_stop_reason"] != nil || summary["stop_note"] != "" {
		t.Fatalf("live session exposes a historical stop: %s", raw)
	}
	session := summary["session"].(map[string]any)
	if session["started_at_ms"] != float64(now-30000) || session["refreshed_at_ms"] != float64(now) {
		t.Fatalf("session lost its clocks: %s", raw)
	}
	h.exec(`UPDATE drain_sessions SET ended_at = ?`, now)
	_, env, raw = h.get("/api/v1/snapshot")
	summary = env["data"].(map[string]any)["drain_summary"].(map[string]any)
	if summary["last_stop_reason"] != "operator-stop" {
		t.Fatalf("ended session lost stop: %s", raw)
	}
}

func TestLoopLivenessNeverComesFromLeases(t *testing.T) {
	h := newServeHarness(t)
	status, env, raw := h.get("/api/v1/snapshot")
	if status != http.StatusOK {
		t.Fatalf("snapshot: %d %s", status, raw)
	}
	summary := env["data"].(map[string]any)["drain_summary"].(map[string]any)
	if summary["active"] != false {
		t.Fatalf("leases without a session claim an active loop: %s", raw)
	}
}

func TestLoopLivenessUsesTheSessionDeadlineAndStop(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
	 refreshed_at INTEGER NOT NULL, next_pass_at INTEGER NOT NULL, ended_at INTEGER)`)
	h.exec(`DELETE FROM lanes; UPDATE runs SET state = 'finished'`)
	now := time.Now().UnixMilli()
	h.exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at) VALUES ('live', ?, ?, ?)`, now, now, now+60000)
	check := func(want bool) {
		t.Helper()
		status, env, raw := h.get("/api/v1/snapshot")
		if status != http.StatusOK {
			t.Fatalf("snapshot: %d %s", status, raw)
		}
		summary := env["data"].(map[string]any)["drain_summary"].(map[string]any)
		if summary["active"] != want {
			t.Fatalf("active = %v, want %v: %s", summary["active"], want, raw)
		}
	}
	check(true)
	h.exec(`UPDATE drain_sessions SET next_pass_at = ?`, now-60000)
	check(false)
	h.exec(`UPDATE drain_sessions SET next_pass_at = ?, ended_at = ?`, now+60000, now)
	check(false)
}

func TestSnapshotSessionCarriesTheConfiguredLanes(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
	 refreshed_at INTEGER NOT NULL, next_pass_at INTEGER NOT NULL, ended_at INTEGER, lanes TEXT)`)
	now := time.Now().UnixMilli()
	h.exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, lanes)
	 VALUES ('live', ?, ?, ?, '["lane-1","express-1"]')`, now, now, now+60000)
	_, env, raw := h.get("/api/v1/snapshot")
	session := env["data"].(map[string]any)["drain_summary"].(map[string]any)["session"].(map[string]any)
	lanes, _ := session["lanes"].([]any)
	if len(lanes) != 2 || lanes[0] != "lane-1" || lanes[1] != "express-1" {
		t.Fatalf("session lost the loop's configured lanes: %s", raw)
	}
}
