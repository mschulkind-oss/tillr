package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// A lane's run history is the lane page's table and nobody else's: on a live store it was
// 983 KB of a 1.36 MB lanes answer, sent to the home page's strip that never reads it. The
// list carries no history; `?history=<lane id>` carries that one lane's.
func TestLanesRouteCarriesHistoryOnlyForTheNamedLane(t *testing.T) {
	h := newServeHarness(t)
	historyOf := func(path string) map[string]int {
		status, _, raw := h.get(path)
		if status != 200 {
			t.Fatalf("%s: %d %s", path, status, raw)
		}
		var answer struct {
			Data []struct {
				LaneID  string           `json:"lane_id"`
				History []map[string]any `json:"history"`
			} `json:"data"`
		}
		if err := json.Unmarshal(raw, &answer); err != nil {
			t.Fatal(err)
		}
		out := map[string]int{}
		for _, row := range answer.Data {
			out[row.LaneID] = len(row.History)
		}
		return out
	}
	for lane, n := range historyOf("/api/v1/lanes") {
		if n != 0 {
			t.Fatalf("the lanes list carried %d history runs for %s", n, lane)
		}
	}
	named := historyOf("/api/v1/lanes?history=lane-a")
	if named["lane-a"] == 0 {
		t.Fatalf("?history=lane-a carried no history for lane-a: %v", named)
	}
	for lane, n := range named {
		if lane != "lane-a" && n != 0 {
			t.Fatalf("?history=lane-a carried %d runs for %s", n, lane)
		}
	}
}

func TestLaneRowsRecords(t *testing.T) {
	now := time.UnixMilli(600000)
	root := t.TempDir()
	d := &storeDomain{lanes: []storeLane{{LaneID: "a", State: "leased", RunID: "live"}, {LaneID: "b", State: "leased", RunID: "ended"}, {LaneID: "c", State: "idle"}, {LaneID: "d", State: "idle"}}, allocations: map[string]allocation{"live": {RunID: "live", ItemID: "item", LaneID: "a", Backend: "pi", StartedAtMS: 2000}}, runs: []storeRun{{RunID: "old", ItemID: "item", LaneID: "c", State: "timed-out", StartedAtMS: 1000, FinishedAtMS: 1500}, {RunID: "ended", ItemID: "other", LaneID: "b", State: "exited", StartedAtMS: 3000, FinishedAtMS: 5000}}}
	writeLaneFile(t, filepath.Join(root, "loop", "ended", "steps.jsonl"), `{"step":"gate","finished_at_ms":560000}`+"\n"+`{"step":"land","started_at_ms":580000}`+"\n")
	rows := buildLaneRows(d, nil, map[string]string{"item": "builder"}, filepath.Join(root, "loop"), filepath.Join(root, "lanes"), now)
	if len(rows) != 4 || rows[0].State != "running" || rows[1].State != "settling" || rows[2].State != "idle" {
		t.Fatalf("rows: %+v", rows)
	}
	if rows[0].Seat != "builder" || rows[0].Attempt != 2 || *rows[0].StateSinceMS != 2000 {
		t.Fatalf("live: %+v", rows[0])
	}
	if rows[1].LastStep != "gate" || *rows[1].LastStepAtMS != 560000 || *rows[1].StateSinceMS != 5000 {
		t.Fatalf("settling: %+v", rows[1])
	}
	if rows[2].RunID != "old" || rows[2].Outcome != "timed-out" || rows[3].RunID != "" {
		t.Fatalf("idle: %+v", rows[2:])
	}
}

func writeLaneFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0644); err != nil {
		t.Fatal(err)
	}
}

func TestLaneActionsLiveAdapters(t *testing.T) {
	for _, tc := range []struct{ backend, line, want string }{
		{"pi", `{"type":"message_end","message":{"role":"assistant","timestamp":1234,"content":[{"type":"toolCall","name":"bash","arguments":{"command":"go test ./...","timeout":10}}]}}`, "bash: go test ./..."},
		{"pi-designer", `{"type":"tool_execution_start","toolName":"read","args":{"path":"docs/a.md"},"timestamp":1234}`, "read: docs/a.md"},
		{"claude", `{"type":"assistant","timestamp":"1970-01-01T00:00:01.234Z","message":{"role":"assistant","content":[{"type":"tool_use","name":"Bash","input":{"command":"go test ./...","timeout":10}}]}}`, "Bash: go test ./..."},
		{"claude", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"` + strings.Repeat("é", 90) + `"}]}}`, strings.Repeat("é", 80)},
	} {
		t.Run(tc.backend+tc.want[:4], func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "run.log")
			writeLaneFile(t, path, tc.line+"\n"+`{"type":"partial`)
			action, _, grew := readLaneAction(path, tc.backend)
			if action.Text != tc.want || action.Kind != "action" || grew == nil {
				t.Fatalf("action: %+v grew %v", action, grew)
			}
			f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
			if err != nil {
				t.Fatal(err)
			}
			_, _ = f.WriteString("\"}\n" + `{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"new action"}]}}` + "\n")
			_ = f.Close()
			if tc.backend == "pi" {
				next, _, _ := readLaneAction(path, tc.backend)
				if next.Text != "new action" {
					t.Fatalf("not live: %+v", next)
				}
			}
		})
	}
}

func TestLaneActionPiUpdatesBeforeMessageEnd(t *testing.T) {
	raw, err := os.ReadFile("testdata/lane-pi-live.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.SplitAfter(string(raw), "\n")
	path := filepath.Join(t.TempDir(), "live.log")
	writeLaneFile(t, path, strings.Join(lines[:3], ""))
	first, _, _ := readLaneAction(path, "pi-designer")
	if first.Text != "paths changed: src/example.go\n" || first.AtMS == nil || *first.AtMS != 1765000002000 || first.Clock != stateObserved {
		t.Fatalf("first live update: %+v", first)
	}
	writeLaneFile(t, path, string(raw))
	next, _, _ := readLaneAction(path, "pi-designer")
	if next.Text != "paths changed: src/example.go\nverification: go test ./... -> ok\n" || next.AtMS == nil || *next.AtMS != 1765000003000 {
		t.Fatalf("second live update: %+v", next)
	}
}

func TestLaneLivenessAndQuiet(t *testing.T) {
	path := filepath.Join(t.TempDir(), "run.log")
	writeLaneFile(t, path, "not readable as adapter JSON\n")
	at := time.Now().Add(-5 * time.Minute)
	if err := os.Chtimes(path, at, at); err != nil {
		t.Fatal(err)
	}
	action, _, grew := readLaneAction(path, "unknown")
	if action.Kind != "liveness" || action.AtMS == nil || grew == nil {
		t.Fatalf("%+v", action)
	}
}

func TestLanesAPIAndCLIShareRead(t *testing.T) {
	h := newServeHarness(t)
	status, _, raw := h.get("/api/v1/lanes")
	var got struct {
		Data []laneRow `json:"data"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if status != 200 || len(got.Data) != 1 {
		t.Fatalf("status %d rows %+v", status, got.Data)
	}
	rows, err := readLaneRows(context.Background(), h.store, h.loopDir, h.laneRoot, time.Now())
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != len(got.Data) || rows[0].RunID != got.Data[0].RunID || rows[0].State != got.Data[0].State {
		t.Fatalf("API %+v CLI %+v", got.Data, rows)
	}
}

func TestLaneWorkStatePausedRun(t *testing.T) {
	paused := storeRun{RunID: "r", State: "paused", EventSeq: 10}
	for _, c := range []struct {
		name   string
		resume int64
		want   string
	}{{"continuation live", 11, "running"}, {"no continuation", 0, "paused"}, {"resume older than row", 9, "paused"}} {
		lane := storeLane{LaneID: "a", State: "leased", RunID: "r", ResumedSeq: c.resume}
		if got := laneWorkState(lane, paused); got != c.want {
			t.Errorf("%s: %q, want %q", c.name, got, c.want)
		}
	}
}

func TestLanesGroupsConfiguredDrainingAndIdleHistorical(t *testing.T) {
	h := newServeHarness(t)
	at := time.Date(2026, 11, 23, 5, 35, 0, 0, time.UTC)
	h.setNow(at)
	now := at.UnixMilli()
	h.exec(`DELETE FROM lanes`)
	h.exec(`CREATE TABLE drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
		refreshed_at INTEGER NOT NULL, next_pass_at INTEGER NOT NULL, ended_at INTEGER, lanes TEXT)`)
	h.exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, lanes)
		VALUES ('live', ?, ?, ?, '["lane-1"]')`, now-30000, now, now+60000)

	// 1 configured lane (lane-1, running), 2 draining lanes (lane-2 running + lane-3 idle lease
	// still holding an unreleased pending reservation from an earlier session), and 20 idle
	// historical lanes (lane-4..lane-23) holding nothing.
	h.exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES ('lane-1', 'leased', 'run-conf-1', NULL)`)
	h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, 'run.allocate', 'run', 'run-conf-1', '{"run_id":"run-conf-1","item_id":"item-conf-1","lane_id":"lane-1","backend":"pi","started_at":"2026-11-23T05:34:00Z"}')`, now-60000)

	h.exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES ('lane-2', 'leased', 'run-drain-2', NULL)`)
	h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, 'run.allocate', 'run', 'run-drain-2', '{"run_id":"run-drain-2","item_id":"item-drain-2","lane_id":"lane-2","backend":"pi","started_at":"2026-11-23T05:20:00Z"}')`, now-900000)
	h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, 'drain.started', 'run', 'run-drain-2', '{"decision":{"item_id":"item-drain-2","lane_id":"lane-2","run_request":{"run_id":"run-drain-2","backend":"pi"}}}')`, now-900000)

	h.exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES ('lane-3', 'idle', '', NULL)`)
	h.exec(`INSERT INTO runs(run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at) VALUES ('run-drain-3', 'item-drain-3', 'lane-3', 'pi', 'exited', 0, ?, ?)`, now-800000, now-700000)
	h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, 'drain.started', 'run', 'run-drain-3', '{"decision":{"item_id":"item-drain-3","lane_id":"lane-3","run_request":{"run_id":"run-drain-3","backend":"pi"}}}')`, now-800000)
	h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, 'drain.candidate-lane', 'run', 'run-drain-3', '{"candidate":{"item_id":"item-drain-3","run_id":"run-drain-3","lane_id":"lane-3"},"released":false,"reason":"candidate pin cannot be read back"}')`, now-690000)

	for i := 4; i <= 23; i++ {
		laneID := fmt.Sprintf("lane-%d", i)
		runID := fmt.Sprintf("run-hist-%d", i)
		itemID := fmt.Sprintf("item-hist-%d", i)
		h.exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES (?, 'idle', '', NULL)`, laneID)
		h.exec(`INSERT INTO runs(run_id, item_id, lane_id, backend, state, exit_code, started_at, finished_at) VALUES (?, ?, ?, 'pi', 'exited', 0, ?, ?)`, runID, itemID, laneID, now-3600_000, now-3500_000)
		// One completed reservation must not read as draining.
		if i == 4 {
			h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, 'drain.started', 'run', ?, ?)`,
				now-3600_000, runID, fmt.Sprintf(`{"decision":{"item_id":%q,"lane_id":%q,"run_request":{"run_id":%q,"backend":"pi"}}}`, itemID, laneID, runID))
			h.exec(`INSERT INTO events(ts, kind, object_type, object_id, payload) VALUES (?, 'drain.examined', 'run', ?, '{}')`, now-3400_000, runID)
		}
	}

	status, _, raw := h.get("/api/v1/lanes")
	if status != 200 {
		t.Fatalf("/api/v1/lanes: %d %s", status, raw)
	}
	var got struct {
		Data []laneRow `json:"data"`
	}
	if err := json.Unmarshal(raw, &got); err != nil {
		t.Fatal(err)
	}
	if len(got.Data) != 23 {
		t.Fatalf("len(data) = %d, want 23", len(got.Data))
	}
	counts := map[string]int{}
	for _, row := range got.Data {
		counts[row.Group]++
	}
	if counts["configured"] != 1 || counts["draining"] != 2 || counts["idle"] != 20 {
		t.Fatalf("group counts = %+v, want 1 configured, 2 draining, 20 idle", counts)
	}
	if got.Data[0].LaneID != "lane-1" || got.Data[0].Group != "configured" {
		t.Fatalf("first row = %+v, want configured lane-1", got.Data[0])
	}
	if got.Data[1].LaneID != "lane-2" || got.Data[1].Group != "draining" || got.Data[1].GroupNote != "finishing old work, gets no new work" {
		t.Fatalf("second row = %+v, want draining lane-2 with note", got.Data[1])
	}
	if got.Data[2].LaneID != "lane-3" || got.Data[2].Group != "draining" || got.Data[2].GroupNote != "finishing old work, gets no new work" || got.Data[2].HeldItemID != "item-drain-3" || !strings.Contains(got.Data[2].HeldReason, "cannot be read back") {
		t.Fatalf("third row = %+v, want draining lane-3 holding item-drain-3", got.Data[2])
	}

	if header := laneCapacityLine(got.Data); header != "1 configured, 3 busy (1 of 1 configured + 2 draining old work) · 20 idle holding nothing" {
		t.Fatalf("header = %q", header)
	}
	var cli bytes.Buffer
	if err := printLanes(&cli, got.Data); err != nil {
		t.Fatal(err)
	}
	out := cli.String()
	for _, want := range []string{
		"1 configured, 3 busy (1 of 1 configured + 2 draining old work) · 20 idle holding nothing",
		"Configured lanes (1):",
		"Draining lanes (2) — finishing old work, gets no new work:",
		"Idle lanes holding nothing: 20 folded",
	} {
		if !strings.Contains(out, want) {
			t.Fatalf("CLI output missing %q:\n%s", want, out)
		}
	}

	status, snapEnv, snapRaw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("/api/v1/snapshot: %d %s", status, snapRaw)
	}
	snapLanes := snapEnv["data"].(map[string]any)["lanes"].([]any)
	if len(snapLanes) != 23 {
		t.Fatalf("snapshot lanes = %d, want 23", len(snapLanes))
	}
	snapCounts := map[string]int{}
	for _, item := range snapLanes {
		l := item.(map[string]any)
		g, _ := l["group"].(string)
		snapCounts[g]++
	}
	if snapCounts["configured"] != 1 || snapCounts["draining"] != 2 || snapCounts["idle"] != 20 {
		t.Fatalf("snapshot group counts = %+v, want 1 configured, 2 draining, 20 idle", snapCounts)
	}

	// Scaling the session to 1 ordinary and 1 express lane updates /api/v1/lanes without
	// needing a new event, and synthesizes express-1 before its first lease.
	h.exec(`UPDATE drain_sessions SET lanes = '["lane-1","express-1"]' WHERE session_id = 'live'`)
	status, _, raw = h.get("/api/v1/lanes")
	var scaled struct {
		Data []laneRow `json:"data"`
	}
	if status != 200 || json.Unmarshal(raw, &scaled) != nil {
		t.Fatalf("scaled /api/v1/lanes: %d %s", status, raw)
	}
	if len(scaled.Data) != 24 || scaled.Data[0].LaneID != "lane-1" || scaled.Data[1].LaneID != "express-1" || scaled.Data[1].Group != "configured" || scaled.Data[1].State != "idle" {
		t.Fatalf("scaled configured rows = %+v", scaled.Data[:3])
	}
	if header := laneCapacityLine(scaled.Data); header != "2 configured (1 ordinary, 1 express), 3 busy (1 of 2 configured + 2 draining old work) · 20 idle holding nothing" {
		t.Fatalf("scaled header = %q", header)
	}
}
