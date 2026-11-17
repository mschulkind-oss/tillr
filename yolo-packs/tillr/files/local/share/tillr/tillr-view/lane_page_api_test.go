package main

import (
	"net/http"
	"os"
	"path/filepath"
	"testing"
)

func TestItemRouteListsReverseDependencies(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT OR IGNORE INTO item_edges(item_id, blocked_by) VALUES ('item-seed-06', 'item-seed-02')`)
	status, body, raw := h.get("/api/v1/items/item-seed-02")
	if status != http.StatusOK {
		t.Fatalf("%d: %s", status, raw)
	}
	unblocks, ok := body["data"].(map[string]any)["unblocks"].([]any)
	if !ok {
		t.Fatalf("missing reverse dependencies: %s", raw)
	}
	found := false
	for _, id := range unblocks {
		if id == "item-seed-06" {
			found = true
		}
	}
	if !found {
		t.Fatalf("reverse edge missing: %s", raw)
	}
}

func TestTranscriptRouteReadsAnAppendedLiveStream(t *testing.T) {
	h := newServeHarness(t)
	path := filepath.Join(h.laneRoot, "lane-a", "run-2.log")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	_, before, raw := h.get("/api/v1/runs/run-2/transcript")
	if len(before["data"].(map[string]any)["entries"].([]any)) != 0 {
		t.Fatalf("not initially empty: %s", raw)
	}
	file, err := os.OpenFile(path, os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatal(err)
	}
	_, err = file.WriteString(`{"type":"message_end","message":{"role":"assistant","timestamp":1000,"content":[{"type":"text","text":"stream grew"}]}}` + "\n")
	file.Close()
	if err != nil {
		t.Fatal(err)
	}
	status, after, raw := h.get("/api/v1/runs/run-2/transcript")
	if status != http.StatusOK {
		t.Fatalf("%d: %s", status, raw)
	}
	entries := after["data"].(map[string]any)["entries"].([]any)
	if len(entries) != 1 || entries[0].(map[string]any)["text"] != "stream grew" {
		t.Fatalf("append not served: %s", raw)
	}
}
