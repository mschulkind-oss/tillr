package main

import (
	"fmt"
	"testing"
	"time"
)

// The header's data carries the answer to the latest restart request
// of each role; a role nobody asked has no entry.
func TestSnapshotCarriesTheLatestRestartAnswerOfEachRole(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
 (1000,'drain.restart-request','restart','restart-view-1','{"id":"restart-view-1","role":"view","at":"2026-11-17T00:00:00Z"}'),
 (1001,'drain.restart-answer','restart','restart-view-1','{"request_id":"restart-view-1","role":"view","answer":"refused","from_build":"a","to_build":"digest b","detail":"the store is unreadable","at":"2026-11-17T00:00:01Z"}'),
 (1002,'drain.restart-request','restart','restart-view-2','{"id":"restart-view-2","role":"view","at":"2026-11-17T00:01:00Z"}'),
 (1003,'drain.restart-answer','restart','restart-view-2','{"request_id":"restart-view-2","role":"view","answer":"restarted","from_build":"a","to_build":"b","at":"2026-11-17T00:01:01Z"}'),
 (1004,'drain.restart-request','restart','restart-drain-1','{"id":"restart-drain-1","role":"drain","at":"2026-11-17T00:02:00Z"}')`)
	status, body, raw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("snapshot: %d %s", status, raw)
	}
	restarts := body["data"].(map[string]any)["drain_summary"].(map[string]any)["restarts"].(map[string]any)
	view := restarts["view"].(map[string]any)
	if view["answer"] != "restarted" || view["request_id"] != "restart-view-2" {
		t.Fatalf("view answer = %v, want the latest", view)
	}
	if _, asked := restarts["drain"]; asked {
		t.Fatalf("an unanswered request is not an answer: %v", restarts["drain"])
	}
}

// A live loop's running build is the one it published at start, whatever the last terminal
// result names; with no live session the terminal result is what is shown.
func TestHeaderShowsTheLiveLoopsPublishedBuild(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE IF NOT EXISTS drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL, refreshed_at INTEGER NOT NULL, next_pass_at INTEGER NOT NULL, ended_at INTEGER)`)
	now := time.Date(2026, 11, 22, 12, 0, 0, 0, time.UTC)
	h.setNow(now)
	h.exec(`INSERT INTO drain_sessions VALUES ('live', 1, ` + fmt.Sprint(now.UnixMilli()) + `, ` + fmt.Sprint(now.Add(time.Hour).UnixMilli()) + `, NULL)`)
	h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
 (1000,'drain.build-started','build','drain-1','{"role":"drain","revision":"live-revision","digest":"live-digest","executable":"/live","pid":1,"at":"2026-11-17T00:00:00Z"}')`)
	status, body, raw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("snapshot: %d %s", status, raw)
	}
	build := body["data"].(map[string]any)["drain_summary"].(map[string]any)["build"].(map[string]any)
	if build["revision"] != "live-revision" || build["digest"] != "live-digest" {
		t.Fatalf("header drain build = %v, want the live loop's published build", build)
	}
}
