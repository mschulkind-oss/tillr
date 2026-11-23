package main

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

func writeRepeatedLoopRecord(t *testing.T, loopDir, filename, stopReason, detail, itemID, step string, at time.Time) {
	t.Helper()
	payloadMap := map[string]any{
		"stop":         stopReason,
		"detail":       detail,
		"stop_item_id": itemID,
		"stop_step":    step,
		"ticks":        1,
	}
	payload, err := json.Marshal(payloadMap)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	body, err := record.Encode(record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          loopResultKind,
		ID:            strings.TrimSuffix(filename, ".json"),
		Producer:      record.Producer{System: record.System, Utility: "drain", Version: "0.1.0"},
		CreatedAt:     at.UTC(),
		Payload:       payload,
	})
	if err != nil {
		t.Fatalf("encode record: %v", err)
	}
	if err := os.MkdirAll(loopDir, 0o755); err != nil {
		t.Fatalf("mkdir loopDir: %v", err)
	}
	if err := os.WriteFile(filepath.Join(loopDir, filename), append(body, '\n'), 0o644); err != nil {
		t.Fatalf("write %s: %v", filename, err)
	}
}

func TestRepeatedLoopStopDetection(t *testing.T) {
	base := time.Date(2026, 11, 19, 20, 15, 0, 0, time.UTC)

	t.Run("single abnormal exit returns nil", func(t *testing.T) {
		dir := t.TempDir()
		writeRepeatedLoopRecord(t, dir, "drain-1-1.json", "fault", "cannot read landing offer", "item-1", "land", base)
		if got := (&observations{loopDir: dir}).repeatedLoopStop(); got != nil {
			t.Fatalf("got %+v, want nil", got)
		}
	})

	t.Run("two identical generation-stamped exits return streak", func(t *testing.T) {
		dir := t.TempDir()
		t1 := base
		t2 := base.Add(60 * time.Second)
		writeRepeatedLoopRecord(t, dir, "drain-9-9.json", "fault", "cannot read  landing\noffer", "item-1", "land", t1)
		writeRepeatedLoopRecord(t, dir, "drain-10-10.json", "fault", "cannot read landing offer", "item-1", "land", t2)
		got := (&observations{loopDir: dir}).repeatedLoopStop()
		if got == nil {
			t.Fatal("got nil, want repeatedStop")
		}
		if got.Count != 2 || got.Reason != "fault" || got.Detail != "cannot read landing offer" {
			t.Fatalf("unexpected repeatedStop: %+v", got)
		}
		if got.FirstAtMS != t1.UnixMilli() || got.LastAtMS != t2.UnixMilli() {
			t.Fatalf("timestamps = %d..%d, want %d..%d", got.FirstAtMS, got.LastAtMS, t1.UnixMilli(), t2.UnixMilli())
		}
		if got.ItemID != "item-1" || got.Step != "land" || got.Next == "" || got.Sentence == "" {
			t.Fatalf("missing context fields: %+v", got)
		}
	})

	t.Run("different causes return nil", func(t *testing.T) {
		dir := t.TempDir()
		writeRepeatedLoopRecord(t, dir, "drain-1-1.json", "fault", "cause A", "item-1", "land", base)
		writeRepeatedLoopRecord(t, dir, "drain-2-2.json", "fault", "cause B", "item-1", "land", base.Add(time.Minute))
		if got := (&observations{loopDir: dir}).repeatedLoopStop(); got != nil {
			t.Fatalf("got %+v, want nil for different details", got)
		}
		writeRepeatedLoopRecord(t, dir, "drain-3-3.json", "lanes-leased", "cause B", "item-1", "plan", base.Add(2*time.Minute))
		if got := (&observations{loopDir: dir}).repeatedLoopStop(); got != nil {
			t.Fatalf("got %+v, want nil for different reasons", got)
		}
	})

	t.Run("clean stop resets trailing streak", func(t *testing.T) {
		for _, clean := range []string{"operator-stop", "queue-drained"} {
			t.Run(clean, func(t *testing.T) {
				dir := t.TempDir()
				writeRepeatedLoopRecord(t, dir, "drain-1-1.json", "fault", "same cause", "item-1", "land", base)
				writeRepeatedLoopRecord(t, dir, "drain-2-2.json", "fault", "same cause", "item-1", "land", base.Add(time.Minute))
				writeRepeatedLoopRecord(t, dir, "drain-3-3.json", clean, "done", "", "loop", base.Add(2*time.Minute))
				if got := (&observations{loopDir: dir}).repeatedLoopStop(); got != nil {
					t.Fatalf("clean stop %s did not reset streak: %+v", clean, got)
				}
				writeRepeatedLoopRecord(t, dir, "drain-4-4.json", "fault", "same cause", "item-1", "land", base.Add(3*time.Minute))
				if got := (&observations{loopDir: dir}).repeatedLoopStop(); got != nil {
					t.Fatalf("single exit after clean stop should be nil: %+v", got)
				}
				writeRepeatedLoopRecord(t, dir, "drain-5-5.json", "fault", "same cause", "item-1", "land", base.Add(4*time.Minute))
				got := (&observations{loopDir: dir}).repeatedLoopStop()
				if got == nil || got.Count != 2 {
					t.Fatalf("trailing streak after clean stop = %+v, want Count=2", got)
				}
			})
		}
	})

	t.Run("archived drain-0-0 files order before drain-0-0.json", func(t *testing.T) {
		dir := t.TempDir()
		t1 := base
		t2 := base.Add(time.Minute)
		t3 := base.Add(2 * time.Minute)
		writeRepeatedLoopRecord(t, dir, "drain-1-1.json", "operator-stop", "manual stop", "", "loop", t1.Add(-time.Minute))
		writeRepeatedLoopRecord(t, dir, fmt.Sprintf("drain-0-0-%d.json", t1.UnixNano()), "fault", "policy failed", "", "startup", t1)
		writeRepeatedLoopRecord(t, dir, fmt.Sprintf("drain-0-0-%d.json", t2.UnixNano()), "fault", "policy failed", "", "startup", t2)
		writeRepeatedLoopRecord(t, dir, "drain-0-0.json", "fault", "policy failed", "", "startup", t3)
		got := (&observations{loopDir: dir}).repeatedLoopStop()
		if got == nil || got.Count != 3 || got.FirstAtMS != t1.UnixMilli() || got.LastAtMS != t3.UnixMilli() {
			t.Fatalf("got %+v, want Count=3 covering %d..%d", got, t1.UnixMilli(), t3.UnixMilli())
		}
	})
}

func TestRunningSurfacesRepeatedStopBothInactiveAndActive(t *testing.T) {
	base := time.Date(2026, 11, 19, 20, 15, 0, 0, time.UTC)
	h := newRunningHarness(t)
	writeRepeatedLoopRecord(t, h.loopDir, "drain-1-1.json", "fault", "cannot read landing offer for run-99 while its processes survive", "item-seed-01", "land", base)
	writeRepeatedLoopRecord(t, h.loopDir, "drain-2-2.json", "fault", "cannot read landing offer for run-99 while its processes survive", "item-seed-01", "land", base.Add(3*time.Minute))

	// Inactive between keeper restarts.
	payload, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, h.now)
	if err != nil {
		t.Fatalf("readRunning inactive: %v", err)
	}
	if payload.Loop.Active || payload.Loop.RepeatedStop == nil || payload.Loop.RepeatedStop.Count != 2 {
		t.Fatalf("inactive Loop = %+v", payload.Loop)
	}
	rawJSON, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal running payload: %v", err)
	}
	if !strings.Contains(string(rawJSON), `"repeated_stop"`) {
		t.Fatalf("running JSON missing repeated_stop: %s", rawJSON)
	}
	var out bytes.Buffer
	renderRunning(&out, payload)
	text := out.String()
	for _, want := range []string{
		"stuck drain: loop exited 2 times in a row",
		"fault: cannot read landing offer for run-99 while its processes survive",
		"2026-11-19T20:15:00Z..2026-11-19T20:18:00Z",
		"Next:",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("inactive renderRunning missing %q:\n%s", want, text)
		}
	}

	// Active during a newly restarted session following repeated exits.
	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("open db: %v", err)
	}
	nowMS := h.now.UnixMilli()
	if _, err := db.Exec(`CREATE TABLE IF NOT EXISTS drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL, refreshed_at INTEGER NOT NULL, next_pass_at INTEGER NOT NULL, ended_at INTEGER);
		INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, ended_at) VALUES('sess-restart', ?, ?, ?, NULL)`,
		nowMS-5000, nowMS-1000, nowMS+60000); err != nil {
		db.Close()
		t.Fatalf("insert drain_sessions: %v", err)
	}
	db.Close()

	activePayload, err := readRunning(context.Background(), h.store, h.loopDir, h.laneRoot, h.now)
	if err != nil {
		t.Fatalf("readRunning active: %v", err)
	}
	if !activePayload.Loop.Active || activePayload.Loop.RepeatedStop == nil || activePayload.Loop.RepeatedStop.Count != 2 {
		t.Fatalf("active Loop = %+v", activePayload.Loop)
	}
	out.Reset()
	renderRunning(&out, activePayload)
	activeText := out.String()
	for _, want := range []string{
		"stuck drain (restarted after repeated exits): loop exited 2 times in a row",
		"fault: cannot read landing offer for run-99 while its processes survive",
	} {
		if !strings.Contains(activeText, want) {
			t.Errorf("active renderRunning missing %q:\n%s", want, activeText)
		}
	}
}

func TestWaitingAndSnapshotSurfaceRepeatedStopAndRunProcessesLive(t *testing.T) {
	base := time.Date(2026, 11, 19, 20, 15, 0, 0, time.UTC)
	h := newServeHarness(t)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(3000,'drain.item-fault','item','item-seed-02','{"item_id":"item-seed-02","run_id":"run-proc","lane_id":"lane-28","reason":"run-processes-live","detail":"cannot read landing offer for run-proc while its processes survive: 101 (go tool test2json), 102 (test2json)"}')`)

	writeRepeatedLoopRecord(t, h.loopDir, "drain-1-1.json", "fault", "cannot read landing offer for run-proc while its processes survive", "item-seed-02", "land", base)
	writeRepeatedLoopRecord(t, h.loopDir, "drain-2-2.json", "fault", "cannot read landing offer for run-proc while its processes survive", "item-seed-02", "land", base.Add(2*time.Minute))
	writeRepeatedLoopRecord(t, h.loopDir, "drain-3-3.json", "fault", "cannot read landing offer for run-proc while its processes survive", "item-seed-02", "land", base.Add(4*time.Minute))

	// 1. GET /api/v1/waiting
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("waiting status %d: %s", status, raw)
	}
	var obsRow, procRow map[string]any
	for _, v := range body["data"].([]any) {
		row := v.(map[string]any)
		if row["observation"] == "loop:repeated-exit" {
			obsRow = row
		}
		if row["item_id"] == "item-seed-02" {
			procRow = row
		}
	}
	if obsRow == nil {
		t.Fatalf("missing loop:repeated-exit observation row: %s", raw)
	}
	if obsRow["class"] != "needs you" || obsRow["where"] != "loop" {
		t.Errorf("unexpected observation class/where: %+v", obsRow)
	}
	if !strings.Contains(obsRow["why"].(string), "loop exited 3 times in a row (fault): cannot read landing offer for run-proc while its processes survive") {
		t.Errorf("unexpected observation why: %v", obsRow["why"])
	}
	if !strings.HasPrefix(obsRow["mover"].(string), "operator; ") {
		t.Errorf("unexpected observation mover: %v", obsRow["mover"])
	}
	if obsRow["since"] != base.UTC().Format(time.RFC3339Nano) {
		t.Errorf("observation since = %v, want %s", obsRow["since"], base.UTC().Format(time.RFC3339Nano))
	}

	if procRow == nil || procRow["class"] != "needs you" {
		t.Fatalf("missing run-processes-live held row: %s", raw)
	}
	for _, want := range []string{"run-processes-live", "101 (go tool test2json)", "102 (test2json)", "run-proc", "lane-28"} {
		if !strings.Contains(procRow["why"].(string), want) {
			t.Errorf("run-processes-live why missing %q: %v", want, procRow["why"])
		}
	}
	if !strings.Contains(procRow["mover"].(string), "clear-fault") {
		t.Errorf("run-processes-live mover missing clear-fault: %v", procRow["mover"])
	}

	// 2. CLI tillr view waiting (--history)
	oldStdout := os.Stdout
	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatalf("pipe: %v", err)
	}
	os.Stdout = wPipe
	_, code := waitingFace([]string{"--store", h.store, "--root", h.dir, "--history"})
	wPipe.Close()
	os.Stdout = oldStdout
	stdoutBytes, err := io.ReadAll(rPipe)
	rPipe.Close()
	if err != nil {
		t.Fatalf("read stdout: %v", err)
	}
	stdout := string(stdoutBytes)
	if code != record.ExitOK {
		t.Fatalf("waiting CLI failed %d:\n%s", code, stdout)
	}
	for _, want := range []string{"loop:repeated-exit", "needs you", "loop exited 3 times in a row", "101 (go tool test2json)", "clear-fault"} {
		if !strings.Contains(stdout, want) {
			t.Errorf("waiting CLI missing %q:\n%s", want, stdout)
		}
	}

	// 3. GET /api/v1/snapshot (inactive and active)
	status, snapBody, snapRaw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("snapshot status %d: %s", status, snapRaw)
	}
	drainSummary := snapBody["data"].(map[string]any)["drain_summary"].(map[string]any)
	rep, ok := drainSummary["repeated_stop"].(map[string]any)
	if !ok || rep["count"] != float64(3) || rep["reason"] != "fault" {
		t.Fatalf("snapshot drain_summary.repeated_stop = %v", drainSummary["repeated_stop"])
	}

	at := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	h.setNow(at)
	nowMS := at.UnixMilli()
	h.exec(fmt.Sprintf(`CREATE TABLE IF NOT EXISTS drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL, refreshed_at INTEGER NOT NULL, next_pass_at INTEGER NOT NULL, ended_at INTEGER);
		INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, ended_at) VALUES('sess-active', %d, %d, %d, NULL)`,
		nowMS-2000, nowMS, nowMS+60000))
	status, activeSnap, activeRaw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("active snapshot status %d: %s", status, activeRaw)
	}
	activeDrain := activeSnap["data"].(map[string]any)["drain_summary"].(map[string]any)
	if activeDrain["active"] != true {
		t.Fatalf("expected active=true, got %v", activeDrain["active"])
	}
	activeRep, ok := activeDrain["repeated_stop"].(map[string]any)
	if !ok || activeRep["count"] != float64(3) {
		t.Fatalf("active snapshot lost repeated_stop: %s", activeRaw)
	}
}
