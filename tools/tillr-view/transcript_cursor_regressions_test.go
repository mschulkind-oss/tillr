package main

import (
	"encoding/json"
	"fmt"
	"testing"
)

func TestTranscriptForwardCursorSplitsOneMessageWithoutRepeatingBlocks(t *testing.T) {
	h := newTranscriptHarness(t)
	h.writeLog()
	initial := h.obs().transcript(h.run(), transcriptQuery{})
	cursor, ok := decodeTranscriptCursor(initial.Cursor)
	if !ok {
		t.Fatal("invalid initial cursor")
	}
	blocks := []map[string]string{}
	for i := 0; i < 251; i++ {
		blocks = append(blocks, map[string]string{"type": "text", "text": fmt.Sprintf("block-%03d", i)})
	}
	raw, err := json.Marshal(map[string]any{"type": "message_end", "message": map[string]any{"role": "assistant", "timestamp": 1000, "content": blocks}})
	if err != nil {
		t.Fatal(err)
	}
	h.appendLog(string(raw))
	for page := 0; page < 3; page++ {
		result := h.obs().transcript(h.run(), transcriptQuery{After: &cursor, Limit: 100})
		want := 100
		if page == 2 {
			want = 51
		}
		if len(result.Entries) != want {
			t.Fatalf("page %d has %d entries, want %d", page, len(result.Entries), want)
		}
		for i, entry := range result.Entries {
			if entry.Text != fmt.Sprintf("block-%03d", page*100+i) {
				t.Fatalf("page %d repeats or skips block: %s", page, entry.Text)
			}
		}
		cursor, ok = decodeTranscriptCursor(result.Cursor)
		if !ok {
			t.Fatal("invalid next cursor")
		}
	}
	h.appendLog(`{"type":"message_end","message":{"role":"assistant","timestamp":2000,"content":[{"type":"text","text":"later"}]}}`)
	result := h.obs().transcript(h.run(), transcriptQuery{After: &cursor, Limit: 100})
	if len(result.Entries) != 1 || result.Entries[0].Text != "later" {
		t.Fatalf("absolute offset lost on resume: %+v", result.Entries)
	}
	cursor, _ = decodeTranscriptCursor(result.Cursor)
	empty := h.obs().transcript(h.run(), transcriptQuery{After: &cursor, Limit: 100})
	if len(empty.Entries) != 0 || empty.HasMore {
		t.Fatalf("exhausted cursor repeated entries: %+v", empty)
	}
}

func TestTranscriptBackwardBoundarySurvivesEarlierChronologicalInsertion(t *testing.T) {
	h := newTranscriptHarness(t)
	for i := 0; i < 300; i++ {
		line := fmt.Sprintf(`{"type":"message_end","message":{"role":"assistant","timestamp":%d,"content":[{"type":"text","text":"message-%03d"}]}}`, 2000+i, i)
		if i == 0 {
			h.writeLog(line)
		} else {
			h.appendLog(line)
		}
	}
	tail := h.obs().transcript(h.run(), transcriptQuery{Limit: 100})
	if tail.Before == nil {
		t.Fatal("tail lacks older boundary")
	}
	before, ok := decodeTranscriptCursor(*tail.Before)
	if !ok {
		t.Fatal("invalid before cursor")
	}
	h.writeSteps(`{"step":"run","argv":["worker"],"cwd":"/work","started_at_ms":1000,"finished_at_ms":5000,"duration_ms":4000,"exit_code":0}`)
	older := h.obs().transcript(h.run(), transcriptQuery{Before: &before, Limit: 100})
	if len(older.Entries) != 100 {
		t.Fatalf("older has %d entries", len(older.Entries))
	}
	for i, entry := range older.Entries {
		if entry.Text != fmt.Sprintf("message-%03d", i+100) {
			t.Fatalf("older page repeated tail: %s", entry.Text)
		}
	}
}

func TestLaneKeepsItsRunsAttemptWhenANewerAttemptExistsElsewhere(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE node_runs(item_id TEXT,attempt INTEGER,node_id TEXT,run_id TEXT,outcome TEXT); UPDATE runs SET item_id='item-seed-02' WHERE run_id='run-2'; UPDATE lanes SET run_id='run-1'; INSERT INTO node_runs VALUES('item-seed-02',1,'old-node','run-1','passed'),('item-seed-02',2,'new-node','run-2','running')`)
	_, _, raw := h.get("/api/v1/lanes")
	var answer struct {
		Data []laneRow `json:"data"`
	}
	if err := json.Unmarshal(raw, &answer); err != nil {
		t.Fatal(err)
	}
	if len(answer.Data) != 1 || answer.Data[0].Attempt != 1 {
		t.Fatalf("another lane's attempt selected: %s", raw)
	}
}
