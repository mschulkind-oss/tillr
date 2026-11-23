package main

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// /why spent about ten seconds per request re-folding the store's whole event log in the
// planner, though nothing had been written since the last answer. The server now answers a
// repeat request from the planner's last answer for the same event head and the same index,
// and asks again after any event is written.
func TestWhyAnswerIsReusedUntilTheStoreChanges(t *testing.T) {
	index := `{"index":{"documents":[]}}`
	h := newServeHarnessWithQuestions(t, "", stubIndexCommand(t, index))
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	planner := filepath.Join(dir, "tillr-drain")
	writeScript(t, planner, "cat >/dev/null\necho ran >> "+marker+"\nprintf '%s' '{\"event_head_seq\":1,\"rows\":[],\"summary\":{\"held\":{}}}'\n")
	h.srv.plannerFlag = planner
	runs := func() int {
		raw, _ := os.ReadFile(marker)
		return strings.Count(string(raw), "ran")
	}
	for i := 0; i < 3; i++ {
		if status, _, raw := h.get("/api/v1/why"); status != 200 {
			t.Fatalf("why %d: %d %s", i, status, raw)
		}
	}
	if got := runs(); got != 1 {
		t.Fatalf("planner ran %d times for three requests against one unchanged store, want 1", got)
	}
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('run.progress','run','run-2','{}',1000)`)
	if status, _, raw := h.get("/api/v1/why"); status != 200 {
		t.Fatalf("why after write: %d %s", status, raw)
	}
	if got := runs(); got != 2 {
		t.Fatalf("planner ran %d times after a store write, want 2", got)
	}
}
