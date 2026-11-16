package main_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"strings"
	"testing"
)

func TestLanesCLIPrintsEveryConfiguredLane(t *testing.T) {
	path := fixture(t)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	schema, err := os.ReadFile("testdata/serve-fixture.sql")
	if err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(string(schema)); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES ('lane-idle','idle','',NULL),('lane-live','leased','run-live',NULL)`); err != nil {
		t.Fatal(err)
	}
	if _, err = db.Exec(`INSERT INTO events(ts,kind,payload) VALUES(1000,'run.allocate','{"run_id":"run-live","item_id":"item-live","lane_id":"lane-live","backend":"pi-designer","started_at":"2026-11-16T00:00:00Z"}')`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()
	code, raw := runView(t, "lanes", "--store", path, "--json")
	var rows []struct {
		LaneID  string `json:"lane_id"`
		State   string `json:"state"`
		Backend string `json:"backend"`
		Attempt int    `json:"attempt"`
	}
	if code != 0 || json.Unmarshal(raw, &rows) != nil || len(rows) != 2 || rows[0].State != "idle" || rows[1].State != "running" || rows[1].Attempt != 1 || rows[1].Backend != "pi-designer" {
		t.Fatalf("code %d output %s", code, raw)
	}
	code, raw = runView(t, "lanes", "--store", path)
	if code != 0 || !strings.Contains(string(raw), "lane-idle · idle") || !strings.Contains(string(raw), "lane-live · running") {
		t.Fatalf("table code %d output %s", code, raw)
	}
	code, _ = runView(t, "lanes")
	if code != 2 {
		t.Fatalf("missing store exit %d", code)
	}
}
