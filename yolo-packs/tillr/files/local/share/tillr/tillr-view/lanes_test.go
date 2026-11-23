package main

import (
	"context"
	"encoding/json"
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
