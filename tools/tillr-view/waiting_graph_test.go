package main

import (
	"bytes"
	"encoding/json"
	"strings"
	"testing"
)

func TestWaitingGraphHoldLine(t *testing.T) {
	// The current planner carries both the held node and passed count. The view
	// must retain that explanation in JSON decoding and the terminal table.
	var rows []waitingRow
	if err := json.Unmarshal([]byte(`[{"item_id":"three-stage-item","class":"needs you","where":"lane / node two","why":"node-failed: two; 1/3 nodes passed","mover":"inspect the named node and authorize a retry"}]`), &rows); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printWaiting(&out, rows); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(out.String(), "\n")
	if len(lines) < 2 || !strings.Contains(out.String(), "lane / node two") || !strings.Contains(out.String(), "node-failed: two; 1/3 nodes passed") {
		t.Fatalf("graph hold line lost: %s", out.String())
	}
}
