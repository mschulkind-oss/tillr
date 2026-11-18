package main

import (
	"bytes"
	"os"
	"path/filepath"
	"testing"
)

func TestPassesMissingHistoricalStopDoesNotBorrowNextSession(t *testing.T) {
	h := newServeHarness(t)
	seedPasses(t, h)
	path := filepath.Join(h.loopDir, "drain-19700101T000003Z.json")
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	writePassStop(t, h, 5000, "fault", "The second session could not read its policy.")
	rows := passData(t, h, "")
	first := rows[0].(map[string]any)
	second := rows[1].(map[string]any)
	if first["stop"] != "fault" || first["detail"] != "The second session could not read its policy." || first["session_id"] != "s2" {
		t.Fatalf("second session: %v", first)
	}
	if second["stop"] != "stop not recorded" || second["detail"] != nil || second["session_id"] != "s1" {
		t.Fatalf("borrowed stop: %v", second)
	}
}

func TestPassesRanksRemovedItemsAndExactMilliseconds(t *testing.T) {
	h := newServeHarness(t)
	seedPasses(t, h)
	perfExec(t, h, `INSERT INTO dispatches(decision_id,item_id,lane_id,run_id,action,reason,dispatched_at) VALUES
 ('removed','removed-item','stale-lane','stale-run','hold','blocked',2000),
 ('separate','item-seed-02',NULL,NULL,'hold','blocked',2001)`)
	rows := passData(t, h, "?before=4000")
	if len(rows) != 3 || rows[0].(map[string]any)["dispatched_at_ms"] != float64(2001) {
		t.Fatalf("millisecond grouping: %v", rows)
	}
	decisions := rows[1].(map[string]any)["decisions"].([]any)
	last := decisions[2].(map[string]any)
	if last["item_id"] != "removed-item" || last["place"] != nil || last["lane_id"] != nil || last["run_id"] != nil {
		t.Fatalf("removed hold: %v", last)
	}
	if rows[1].(map[string]any)["stop"] != nil {
		t.Fatalf("stop on older pass: %v", rows)
	}
}

func TestPassesAbsentMeasurementsAndUnreadableResult(t *testing.T) {
	h := newServeHarness(t)
	seedPasses(t, h)
	perfExec(t, h, `DELETE FROM drain_passes WHERE tick_id = 'p1'`)
	rows := passData(t, h, "")
	if rows[2].(map[string]any)["phases"] != nil {
		t.Fatalf("invented phases: %v", rows)
	}
	path := filepath.Join(h.loopDir, "drain-19700101T000003Z.json")
	if err := os.WriteFile(path, []byte("not a record"), 0644); err != nil {
		t.Fatal(err)
	}
	status, body, _ := h.get("/api/v1/passes")
	if status != 503 || body["error"].(map[string]any)["code"] != codeStoreUnreadable {
		t.Fatalf("unreadable result: %d %v", status, body)
	}
}

func TestPassesHistoryBeforeSessionTableWasRecorded(t *testing.T) {
	h := newServeHarness(t)
	seedPasses(t, h)
	perfExec(t, h, `DELETE FROM drain_sessions WHERE session_id = 's1'`)
	rows := passData(t, h, "")
	older := rows[1].(map[string]any)
	if older["stop"] != "operator-stop" || older["detail"] != "The operator stopped this session." {
		t.Fatalf("historical session stop lost after migration: %v", older)
	}
	if rows[2].(map[string]any)["stop"] != nil {
		t.Fatalf("historical stop repeated: %v", rows)
	}
}

func TestPassesRetryRetainsDispatchLinksAndPhases(t *testing.T) {
	h := newServeHarness(t)
	seedPasses(t, h)
	perfExec(t, h, `UPDATE dispatches SET action = 'retry' WHERE decision_id = 'd1'`)
	rows := passData(t, h, "")
	oldest := rows[2].(map[string]any)
	d := oldest["decisions"].([]any)[0].(map[string]any)
	if d["action"] != "retry" || d["lane_id"] != "lane-a" || d["run_id"] != "run-1" || oldest["phases"] == nil {
		t.Fatalf("retry lost dispatched data: %v", oldest)
	}
}

func TestPassesUnmeasuredAndMeasuredZeroGates(t *testing.T) {
	h := newServeHarness(t)
	seedPasses(t, h)
	perfExec(t, h, `ALTER TABLE drain_passes ADD COLUMN gate_count INTEGER;
 UPDATE drain_passes SET gate_ms = 0, gate_count = 0 WHERE tick_id = 'p1'`)
	rows := passData(t, h, "")
	phases := rows[2].(map[string]any)["phases"].(map[string]any)
	if phases["gate_ms"] != nil || phases["stage_ms"] != float64(12) {
		t.Fatalf("unmeasured gate must be null: %v", phases)
	}
	perfExec(t, h, `UPDATE drain_passes SET gate_count = 1 WHERE tick_id = 'p1'`)
	rows = passData(t, h, "")
	if gate := rows[2].(map[string]any)["phases"].(map[string]any)["gate_ms"]; gate != float64(0) {
		t.Fatalf("measured zero lost: %v", gate)
	}
}

func TestPassesMissingStopCannotBorrowStartupFault(t *testing.T) {
	h := newServeHarness(t)
	seedPasses(t, h)
	if err := os.Remove(filepath.Join(h.loopDir, "drain-19700101T000003Z.json")); err != nil {
		t.Fatal(err)
	}
	// Startup faults explicitly record zero ticks and create no session row.
	writePassStop(t, h, 6000, "fault", "A subsequent startup could not load its policy.")
	path := filepath.Join(h.loopDir, "drain-19700101T000006Z.json")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	raw = bytes.Replace(raw, []byte(`"stop":"fault"`), []byte(`"stop":"fault","ticks":0`), 1)
	if err := os.WriteFile(path, raw, 0644); err != nil {
		t.Fatal(err)
	}
	rows := passData(t, h, "")
	if rows[0].(map[string]any)["stop"] != "stop not recorded" || rows[1].(map[string]any)["stop"] != "stop not recorded" {
		t.Fatalf("borrowed unrelated startup stop: %v", rows)
	}
}

func TestPassesActiveSessionHasNoInventedStop(t *testing.T) {
	h := newServeHarness(t)
	seedPasses(t, h)
	perfExec(t, h, `UPDATE drain_sessions SET ended_at = NULL WHERE session_id = 's2'`)
	// A result from a failed startup is not proof that this live session stopped.
	writePassStop(t, h, 5000, "fault", "Another startup failed.")
	rows := passData(t, h, "")
	if rows[0].(map[string]any)["stop"] != "stop not recorded" {
		t.Fatalf("invented active stop: %v", rows)
	}
}
