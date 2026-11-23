package main

import (
	"net/http"
	"strings"
	"testing"
)

// The Run page's schedule keeps every event that names the run, in each field the drain
// records it under, and only those. The read narrows the scan by the run id's text before
// it decodes a payload, so each spelling must still be found.
func TestRunScheduleFindsEveryEventNamingTheRun(t *testing.T) {
	h := newServeHarness(t)
	fields := []string{`"run_id":"run-2"`, `"worker_session":{"run_id":"run-2"}`, `"writer_run_id":"run-2"`,
		`"parent_run_id":"run-2"`, `"supersedes_run_id":"run-2"`, `"next_run_id":"run-2"`,
		`"fix_run_id":"run-2"`, `"design_run_id":"run-2"`, `"decision":{"run_request":{"run_id":"run-2"}}`}
	for i, f := range fields {
		h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('drain.note-` + string(rune('a'+i)) + `','item','item-x','{` + f + `}',` + "2000" + `)`)
	}
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('drain.note-by-object','run','run-2','not json',2001)`)
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('drain.note-other','item','item-x','{"run_id":"run-22"}',2002)`)
	h.exec(`INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES ('drain.note-text-only','item','item-x','{"text":"run-2 mentioned"}',2003)`)
	status, _, raw := h.get("/api/v1/runs/run-2")
	if status != http.StatusOK {
		t.Fatalf("run: %d %s", status, raw)
	}
	for i := range fields {
		if !strings.Contains(string(raw), `drain.note-`+string(rune('a'+i))) {
			t.Errorf("event naming the run by %s is missing: %s", fields[i], raw)
		}
	}
	if !strings.Contains(string(raw), "drain.note-by-object") {
		t.Errorf("event on the run object is missing")
	}
	for _, no := range []string{"drain.note-other", "drain.note-text-only"} {
		if strings.Contains(string(raw), no) {
			t.Errorf("%s names another run or only mentions it", no)
		}
	}
}

