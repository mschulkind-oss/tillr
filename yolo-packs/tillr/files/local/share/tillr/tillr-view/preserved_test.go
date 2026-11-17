package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"testing"
)

func TestViewsShowPreservedWork(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var runID, itemID string
	if err := db.QueryRow(`SELECT run_id,item_id FROM runs LIMIT 1`).Scan(&runID, &itemID); err != nil {
		t.Fatal(err)
	}
	ref := "refs/tillr/preserved/" + runID
	payload, _ := json.Marshal(map[string]any{"item_id": itemID, "run_id": runID, "ref": ref, "summary": "2 files, +3 -1 lines; 1 outside fence", "reason": "reported RESULT: blocked"})
	if _, err := db.Exec(`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES (9000,'drain.work-preserved','run',?,?)`, runID, string(payload)); err != nil {
		t.Fatal(err)
	}
	for _, url := range []string{"/api/v1/runs/" + runID, "/api/v1/waiting"} {
		status, _, raw := h.get(url)
		if status != 200 {
			t.Fatalf("%s: %d %s", url, status, raw)
		}
		for _, want := range []string{ref, "2 files, +3 -1 lines", "reported RESULT: blocked"} {
			if !strings.Contains(string(raw), want) {
				t.Fatalf("%s omits %q: %s", url, want, raw)
			}
		}
	}
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	var out strings.Builder
	if err := printWaiting(&out, rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), ref) || !strings.Contains(out.String(), "2 files") {
		t.Fatalf("waiting table: %s", out.String())
	}
}
