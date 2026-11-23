package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

func TestSnapshotCarriesTheLoopsStopAccount(t *testing.T) {
	h := newServeHarness(t)
	at := time.Now().UTC()
	stop := stopExplanation{Reason: "fault", AtMS: at.UnixMilli(), ItemID: "item-a", Step: "judge", Sentence: "The loop stopped because the store could not be read.", Next: "Owner repairs the store, then restarts with tillr-drain loop."}
	payload, _ := json.Marshal(map[string]any{"stop": stop.Reason, "detail": "the store could not be read", "stop_explanation": stop})
	raw, err := record.Encode(record.Envelope{RecordVersion: record.SupportedVersion, Kind: loopResultKind, ID: "loop-test", Producer: record.Producer{System: "tillr", Utility: "tillr-drain", Version: "test"}, CreatedAt: at, Payload: payload})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(h.loopDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.loopDir, "drain-0-0.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	_, env, body := h.get("/api/v1/snapshot")
	summary := env["data"].(map[string]any)["drain_summary"].(map[string]any)
	account, ok := summary["stop"].(map[string]any)
	if !ok || account["sentence"] != stop.Sentence || account["item_id"] != stop.ItemID || account["step"] != stop.Step || account["next"] != stop.Next || account["at_ms"] != float64(stop.AtMS) {
		t.Fatalf("stop account lost: %s", body)
	}
	if summary["heartbeat_interval_ms"] != float64(1000) {
		t.Fatalf("heartbeat interval missing: %s", body)
	}
}

func TestLegacyStopKeepsItsRecordedCause(t *testing.T) {
	h := newServeHarness(t)
	writeLoopResult(t, h.loopDir, "legacy", "fault", nil)
	_, env, body := h.get("/api/v1/snapshot")
	stop := env["data"].(map[string]any)["drain_summary"].(map[string]any)["stop"].(map[string]any)
	if stop["sentence"] != "The loop stopped because a full tick planned nothing dispatchable." || stop["step"] != "not recorded (older loop result)" || stop["item_id"] != "" {
		t.Fatalf("legacy stop was guessed from newer decisions: %s", body)
	}
}

func TestHeartbeatAgeOverridesAFutureDeadline(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL, refreshed_at INTEGER NOT NULL, next_pass_at INTEGER NOT NULL, ended_at INTEGER)`)
	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	h.setNow(at)
	now := at.UnixMilli()
	h.exec(`INSERT INTO drain_sessions VALUES ('old', ?, ?, ?, NULL)`, now-60000, now-2001, now+60000)
	_, env, body := h.get("/api/v1/snapshot")
	if env["data"].(map[string]any)["drain_summary"].(map[string]any)["active"] != false {
		t.Fatalf("stale heartbeat claimed alive: %s", body)
	}
}
