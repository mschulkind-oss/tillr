package main

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

// /why shows the loop's last recorded pass. These cases count planner runs with a decoy
// planner that leaves a marker each time it is started.
func whyPassHarness(t *testing.T, live bool) (*serveHarness, func() int) {
	t.Helper()
	h := newServeHarnessWithQuestions(t, "", stubIndexCommand(t, `{"index":{"documents":[]}}`))
	dir := t.TempDir()
	marker := filepath.Join(dir, "ran")
	planner := filepath.Join(dir, "tillr-drain")
	body := `{"event_head_seq":1,"rows":[{"item_id":"planned","class":"ready","why":"x"},{"item_id":"gone","class":"done","why":"no further work"} ],"summary":{"held":{}}}`
	writeScript(t, planner, "cat >/dev/null\necho ran >> "+marker+"\nprintf '%s' '"+body+"'\n")
	h.srv.plannerFlag = planner
	h.exec(`CREATE TABLE IF NOT EXISTS design_without_item(document TEXT PRIMARY KEY, roadmap TEXT NOT NULL, roadmap_line INTEGER NOT NULL, first_seen INTEGER NOT NULL)`)
	h.exec(`INSERT INTO design_without_item VALUES ('docs/tillr/design.md','docs/tillr/roadmap.md',7,1000),('docs/pi/design.md','docs/pi/roadmap.md',3,1000)`)
	if err := os.WriteFile(filepath.Join(h.dir, ".tillr.toml"), []byte("[sync]\nroadmaps = [\"docs/tillr/roadmap.md\"]\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if live {
		now := time.Now().UnixMilli()
		h.exec(`CREATE TABLE IF NOT EXISTS drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL, refreshed_at INTEGER NOT NULL, next_pass_at INTEGER NOT NULL, ended_at INTEGER)`)
		h.exec(fmt.Sprintf(`INSERT INTO drain_sessions VALUES ('live', 1, %d, %d, NULL)`, now, now+3600_000))
	}
	return h, func() int {
		raw, _ := os.ReadFile(marker)
		return strings.Count(string(raw), "ran")
	}
}

func TestWhyShowsTheLastRecordedPassWithoutPlanning(t *testing.T) {
	h, planned := whyPassHarness(t, true)
	status, body, raw := h.get("/api/v1/why")
	if status != 200 {
		t.Fatalf("why: %d %s", status, raw)
	}
	data := body["data"].(map[string]any)
	if planned() != 0 {
		t.Fatalf("the planner ran %d times; a recorded pass answers", planned())
	}
	source := data["source"].(map[string]any)
	if source["kind"] != "pass" || source["pass_id"] != "tick-2" || !strings.HasPrefix(source["text"].(string), "as of pass tick-2, ") {
		t.Fatalf("source: %v", source)
	}
	ids := map[string]map[string]any{}
	for _, v := range data["rows"].([]any) {
		row := v.(map[string]any)
		ids[row["item_id"].(string)] = row
	}
	if len(ids) != 2 || ids["item-seed-02"] == nil || ids["item-seed-06"] == nil || ids["item-seed-04"] != nil {
		t.Fatalf("rows are the open items only: %s", raw)
	}
	decision := ids["item-seed-02"]["decision"].(map[string]any)
	if decision["decision_id"] != "decision-item-seed-02-tick-2" || decision["action"] != "hold" || decision["reason"] != "not-ready" {
		t.Fatalf("decision: %v", decision)
	}
	if ids["item-seed-06"]["why"] != "not yet planned" {
		t.Fatalf("an item with no recorded decision says so: %v", ids["item-seed-06"])
	}
	designs := data["designs_waiting"].([]any)
	if len(designs) != 1 || designs[0].(map[string]any)["document"] != "docs/tillr/design.md" {
		t.Fatalf("designs come from the selected roadmap only: %v", designs)
	}
	for _, field := range []string{"alarms", "lanes", "summary"} {
		if _, ok := data[field]; !ok {
			t.Fatalf("missing %s: %s", field, raw)
		}
	}
	// Repeat requests never plan either, however many events arrive.
	h.appendEvent("run.progress", "run", "run-1")
	h.get("/api/v1/why")
	if planned() != 0 {
		t.Fatalf("the planner ran after a write")
	}
}

func TestWhyFallsBackToThePlannerWithNoLoop(t *testing.T) {
	h, planned := whyPassHarness(t, false)
	status, body, raw := h.get("/api/v1/why")
	if status != 200 {
		t.Fatalf("why: %d %s", status, raw)
	}
	data := body["data"].(map[string]any)
	if planned() != 1 {
		t.Fatalf("planner runs: %d, want 1", planned())
	}
	source := data["source"].(map[string]any)
	if source["kind"] != "planner" || !strings.Contains(source["fallback"].(string), "no loop is running") {
		t.Fatalf("source: %v", source)
	}
	rows := data["rows"].([]any)
	if len(rows) != 1 || rows[0].(map[string]any)["item_id"] != "planned" {
		t.Fatalf("done and design rows leave rows: %s", raw)
	}
	if len(data["designs_waiting"].([]any)) != 1 {
		t.Fatalf("designs_waiting: %s", raw)
	}
}

func TestWhyFallsBackWhenNoPassIsRecorded(t *testing.T) {
	h, planned := whyPassHarness(t, true)
	h.exec(`DELETE FROM drain_passes`)
	_, body, raw := h.get("/api/v1/why")
	source := body["data"].(map[string]any)["source"].(map[string]any)
	if planned() != 1 || source["kind"] != "planner" || !strings.Contains(source["fallback"].(string), "no pass is recorded") {
		t.Fatalf("fallback: %v %s", source, raw)
	}
}
