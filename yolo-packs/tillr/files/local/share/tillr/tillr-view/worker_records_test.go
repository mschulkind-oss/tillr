package main

import (
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestWorkerProgressInvalidatesLiveItem(t *testing.T) {
	h := newServeHarness(t)
	d := newChangeDetector(h.srv)
	pollOnce(t, d)
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('run.progress','run','run-2','{"run_id":"run-2","item_id":"item-seed-02","criterion_id":"c1"}',1000)`)
	frame, changed := pollOnce(t, d)
	if !changed || strings.Join(frame.Changed, ",") != "items:item-seed-02,runs:run-2,snapshot" {
		t.Fatalf("worker claim must refresh the live Item page too: %+v", frame)
	}
	if _, changed := pollOnce(t, d); changed {
		t.Fatal("unchanged worker claim produced a second frame")
	}
}

// A lane answer reads worker records for the runs its lanes show, not every run the store
// has ever recorded: a live store holds over a million child-session events, and decoding
// them all cost the lanes route ten seconds. A record of a run no lane shows is therefore
// never read, which a payload that cannot be decoded proves.
func TestLanesReadWorkerRecordsOnlyForLaneRuns(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('run.child-session','run','run-no-lane','not json',1001)`)
	status, _, raw := h.get("/api/v1/lanes")
	if status != http.StatusOK {
		t.Fatalf("a record of a run no lane shows was read: %d %s", status, raw)
	}
}

func TestLanesStillCarryWorkerRecordsOfLaneRuns(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('run.child-session','run','run-2','{"run_id":"run-2","session_id":"child-a","role":"reviewer"}',1001)`)
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('run.progress','run','run-2','{"run_id":"run-2","item_id":"item-seed-02","criterion_id":"c1"}',1002)`)
	status, _, raw := h.get("/api/v1/lanes")
	if status != http.StatusOK || !strings.Contains(string(raw), `"child-a"`) || !strings.Contains(string(raw), `"c1"`) {
		t.Fatalf("lane run's worker records absent: %d %s", status, raw)
	}
}

func TestWorkerRecordsClaimsAndChildTranscript(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('run.progress','run','run-2','{"run_id":"run-2","item_id":"item-seed-02","criterion_id":"c1"}',1000)`)
	status, itemBody, raw := h.get("/api/v1/items/item-seed-02")
	if status != http.StatusOK || len(itemBody["data"].(map[string]any)["progress"].([]any)) != 1 {
		t.Fatalf("claim absent: %d %s", status, raw)
	}
	path := filepath.Join(h.laneRoot, "child.jsonl")
	if err := os.WriteFile(path, []byte(`{"type":"message","timestamp":"2026-11-18T00:00:00Z","message":{"role":"assistant","timestamp":1000,"content":[{"type":"text","text":"child speaks"}]}}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('run.child-session','run','run-2','{"run_id":"run-2","session_id":"child-a","role":"reviewer","transcript":"` + path + `","started_at_ms":1000,"turn_count":1}',1001)`)
	status, body, raw := h.get("/api/v1/runs/child-a/transcript")
	if status != http.StatusOK || !strings.Contains(string(raw), "child speaks") {
		t.Fatalf("child transcript: %d %s", status, raw)
	}
	cursor := body["data"].(map[string]any)["cursor"].(string)
	f, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = f.WriteString(`{"type":"message","message":{"role":"assistant","timestamp":2000,"content":[{"type":"text","text":"live child append"}]}}` + "\n")
	f.Close()
	if err != nil {
		t.Fatal(err)
	}
	status, _, raw = h.get("/api/v1/runs/child-a/transcript?after=" + cursor)
	if status != http.StatusOK || !strings.Contains(string(raw), "live child append") || strings.Contains(string(raw), "child speaks") {
		t.Fatalf("child follow: %d %s", status, raw)
	}
	status, _, raw = h.get("/api/v1/runs/unrecorded-child/transcript")
	if status != http.StatusNotFound {
		t.Fatalf("unrecorded child accepted: %d %s", status, raw)
	}
}
