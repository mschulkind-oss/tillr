package main_test

import (
	"database/sql"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

func TestLanesSeatPrecedenceHistoryAndNodes(t *testing.T) {
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
	if _, err = db.Exec(`
		ALTER TABLE items ADD COLUMN seat TEXT;
		ALTER TABLE runs ADD COLUMN seat TEXT;
		ALTER TABLE runs ADD COLUMN workflow TEXT;
		ALTER TABLE runs ADD COLUMN node TEXT;
		ALTER TABLE dispatches ADD COLUMN seat TEXT;
		CREATE TABLE node_runs(item_id TEXT, node_id TEXT, run_id TEXT, attempt INTEGER, outcome TEXT);
		INSERT INTO items(item_id,title,body_ref,state,design_doc,updated_at,seat)
		VALUES ('item-1','t1','b','queued','',1,'item-seat-1'),
		       ('item-2','t2','b','queued','',1,'item-seat-2'),
		       ('item-3','t3','b','queued','',1,'item-seat-3');
		INSERT INTO lanes(lane_id,state,run_id,leased_until)
		VALUES ('lane-run','idle','',NULL),
		       ('lane-dispatch','idle','',NULL),
		       ('lane-item','idle','',NULL),
		       ('lane-none','idle','',NULL);
		INSERT INTO runs(run_id,item_id,lane_id,backend,seat,workflow,node,state,exit_code,started_at,finished_at,event_seq)
		VALUES ('run-with-seat','item-1','lane-run','pi','run-seat','work','n1','exited',0,100,200,1),
		       ('run-with-dispatch','item-2','lane-dispatch','pi',NULL,NULL,NULL,'exited',0,100,200,2),
		       ('run-with-item','item-3','lane-item','pi',NULL,NULL,NULL,'exited',0,100,200,3),
		       ('run-with-none','item-missing','lane-none','pi',NULL,NULL,NULL,'exited',0,100,200,4);
		INSERT INTO dispatches(decision_id,item_id,lane_id,run_id,action,reason,dispatched_at,seat)
		VALUES ('d1','item-1','lane-run','run-with-seat','dispatch','ready',100,'dispatch-seat-ignored'),
		       ('d2','item-2','lane-dispatch','run-with-dispatch','dispatch','ready',100,'dispatch-seat');
		INSERT INTO node_runs(item_id,node_id,run_id,attempt,outcome)
		VALUES ('item-1','n1','run-with-seat',1,'passed'),
		       ('item-2','n2','run-with-dispatch',1,'passed');
	`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	code, raw := runView(t, "lanes", "--store", path, "--json")
	if code != 0 {
		t.Fatalf("lanes --json failed %d: %s", code, raw)
	}
	var rows []map[string]any
	if err := json.Unmarshal(raw, &rows); err != nil {
		t.Fatal(err)
	}
	byLane := map[string]map[string]any{}
	for _, r := range rows {
		byLane[r["lane_id"].(string)] = r
	}
	if byLane["lane-run"]["seat"] != "run-seat" || byLane["lane-run"]["seat_source"] != "run" {
		t.Fatalf("lane-run: %+v", byLane["lane-run"])
	}
	if byLane["lane-dispatch"]["seat"] != "dispatch-seat" || byLane["lane-dispatch"]["seat_source"] != "dispatch" {
		t.Fatalf("lane-dispatch: %+v", byLane["lane-dispatch"])
	}
	if byLane["lane-item"]["seat"] != "item-seat-3" || byLane["lane-item"]["seat_source"] != "item" {
		t.Fatalf("lane-item: %+v", byLane["lane-item"])
	}
	if byLane["lane-none"]["seat"] != "" || byLane["lane-none"]["seat_source"] != "none" {
		t.Fatalf("lane-none: %+v", byLane["lane-none"])
	}
	hist1 := byLane["lane-run"]["history"].([]any)[0].(map[string]any)
	if hist1["seat"] != "run-seat" {
		t.Fatalf("lane-run history seat: %+v", hist1)
	}
	nodes1 := byLane["lane-run"]["nodes"].([]any)[0].(map[string]any)
	if nodes1["seat"] != "run-seat" {
		t.Fatalf("lane-run node seat: %+v", nodes1)
	}
	nodes2 := byLane["lane-dispatch"]["nodes"].([]any)[0].(map[string]any)
	if nodes2["seat"] != "dispatch-seat" {
		t.Fatalf("lane-dispatch node seat: %+v", nodes2)
	}
}

func TestRunningPassesAndStatusSeatCLI(t *testing.T) {
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
	if _, err = db.Exec(`
		ALTER TABLE runs ADD COLUMN seat TEXT;
		ALTER TABLE runs ADD COLUMN workflow TEXT;
		ALTER TABLE runs ADD COLUMN node TEXT;
		ALTER TABLE dispatches ADD COLUMN seat TEXT;
		INSERT INTO runs(run_id,item_id,lane_id,backend,seat,workflow,node,state,exit_code,started_at,finished_at,event_seq)
		VALUES ('run-new','item-seed-02','lane-a','pi','edit','work','piece-view','exited',0,2000,3000,10),
		       ('run-old-dispatched','item-seed-02','lane-b','pi',NULL,NULL,NULL,'exited',0,1500,1800,11),
		       ('run-old-empty','item-seed-04','lane-c','pi',NULL,NULL,NULL,'exited',0,1200,1400,12);
		INSERT INTO dispatches(decision_id,item_id,lane_id,run_id,action,reason,dispatched_at,seat)
		VALUES ('dec-new','item-seed-02','lane-a','run-new','dispatch','ready',2000,'edit'),
		       ('dec-old','item-seed-02','lane-b','run-old-dispatched','dispatch','ready',1500,'design'),
		       ('dec-hold','item-seed-04',NULL,NULL,'hold','blocked',1500,'design'),
		       ('dec-empty','item-seed-04','lane-c','run-old-empty','retry','ready',1200,NULL);
		INSERT INTO lanes(lane_id,state,run_id,leased_until)
		VALUES ('lane-live','leased','run-live',9999999999999);
		INSERT INTO events(ts,kind,payload)
		VALUES (4000,'run.allocate','{"run_id":"run-live","item_id":"item-seed-04","lane_id":"lane-live","backend":"pi","seat":"build","workflow":"work","node":"impl","started_at":"2026-11-20T00:00:00Z"}');
	`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	loopDir := t.TempDir()
	lanesDir := t.TempDir()

	// running table
	code, runningTable := runView(t, "running", "--store", path, "--loop-dir", loopDir, "--lane-root", lanesDir)
	if code != 0 {
		t.Fatalf("running table failed %d: %s", code, runningTable)
	}
	tableStr := string(runningTable)
	if !strings.Contains(tableStr, "SEAT") {
		t.Fatalf("running table missing SEAT header:\n%s", tableStr)
	}
	if !strings.Contains(tableStr, "build") || !strings.Contains(tableStr, "edit") || !strings.Contains(tableStr, "design") || !strings.Contains(tableStr, "—") {
		t.Fatalf("running table missing expected seats or dash:\n%s", tableStr)
	}

	// running --json
	code, runningJSON := runView(t, "running", "--store", path, "--loop-dir", loopDir, "--lane-root", lanesDir, "--json")
	if code != 0 {
		t.Fatalf("running --json failed %d: %s", code, runningJSON)
	}
	var env map[string]any
	if err := json.Unmarshal(runningJSON, &env); err != nil {
		t.Fatal(err)
	}
	rp := env["payload"].(map[string]any)
	active := rp["active"].([]any)
	if len(active) != 1 || active[0].(map[string]any)["seat"] != "build" || active[0].(map[string]any)["workflow"] != "work" || active[0].(map[string]any)["node"] != "impl" {
		t.Fatalf("running --json active: %+v", active)
	}
	finished := rp["finished"].([]any)
	finByID := map[string]map[string]any{}
	for _, r := range finished {
		m := r.(map[string]any)
		finByID[m["run_id"].(string)] = m
	}
	if finByID["run-new"]["seat"] != "edit" || finByID["run-old-dispatched"]["seat"] != "design" {
		t.Fatalf("running --json finished: %+v", finByID)
	}
	if _, ok := finByID["run-old-empty"]["seat"]; ok {
		t.Fatalf("running --json run-old-empty should omit seat: %+v", finByID["run-old-empty"])
	}

	// passes table & --json
	code, passesTable := runView(t, "passes", "--store", path, "--loop-dir", loopDir)
	if code != 0 {
		t.Fatalf("passes table failed %d: %s", code, passesTable)
	}
	if !strings.Contains(string(passesTable), "seat") || !strings.Contains(string(passesTable), "edit") {
		t.Fatalf("passes table missing seat:\n%s", passesTable)
	}
	code, passesJSON := runView(t, "passes", "--store", path, "--loop-dir", loopDir, "--json")
	if code != 0 {
		t.Fatalf("passes --json failed %d: %s", code, passesJSON)
	}
	var passes []map[string]any
	if err := json.Unmarshal(passesJSON, &passes); err != nil {
		t.Fatal(err)
	}
	// Newest pass is 2000 (dispatch seat=edit), then 1500 (dispatch seat=design, hold omits seat), then 1200 (retry omits seat)
	dec2000 := passes[0]["decisions"].([]any)[0].(map[string]any)
	if dec2000["seat"] != "edit" {
		t.Fatalf("pass 2000 decision: %+v", dec2000)
	}
	for _, rawDec := range passes[1]["decisions"].([]any) {
		dec := rawDec.(map[string]any)
		if dec["action"] == "dispatch" && dec["seat"] != "design" {
			t.Fatalf("pass 1500 dispatch seat: %+v", dec)
		}
		if dec["action"] == "hold" {
			if _, ok := dec["seat"]; ok {
				t.Fatalf("pass 1500 hold should omit seat: %+v", dec)
			}
		}
	}
	dec1200 := passes[2]["decisions"].([]any)[0].(map[string]any)
	if _, ok := dec1200["seat"]; ok {
		t.Fatalf("pass 1200 empty seat should be omitted: %+v", dec1200)
	}

	// status --store
	code, statusRaw := runView(t, "status", "--store", path, "--json")
	if code != 0 {
		t.Fatalf("status --store failed %d: %s", code, statusRaw)
	}
	var statusEnv map[string]any
	if err := json.Unmarshal(statusRaw, &statusEnv); err != nil {
		t.Fatal(err)
	}
	statusPayload := statusEnv["payload"].(map[string]any)
	foundNew := false
	for _, r := range statusPayload["runs"].([]any) {
		m := r.(map[string]any)
		if m["run_id"] == "run-new" {
			foundNew = true
			if m["seat"] != "edit" || m["workflow"] != "work" || m["node"] != "piece-view" {
				t.Fatalf("status --store run-new: %+v", m)
			}
		}
	}
	if !foundNew {
		t.Fatalf("status --store missing run-new")
	}
}

func TestLegacyUnmigratedStoreAndPreChangeRecordsReadCleanly(t *testing.T) {
	// 1. Pre-change store without seat, workflow, node, usage_tokens, usage_cost_usd on runs
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
	if _, err = db.Exec(`
		DROP TABLE runs;
		CREATE TABLE runs(run_id TEXT PRIMARY KEY, item_id TEXT, lane_id TEXT, backend TEXT, state TEXT, exit_code INTEGER, started_at INTEGER, finished_at INTEGER, event_seq INTEGER);
		INSERT INTO runs(run_id,item_id,lane_id,backend,state,exit_code,started_at,finished_at,event_seq) VALUES ('run-legacy','item-seed-02','lane-1','pi','exited',0,100,200,1);
	`); err != nil {
		t.Fatal(err)
	}
	_ = db.Close()

	for _, args := range [][]string{
		{"status", "--store", path, "--json"},
		{"lanes", "--store", path, "--json"},
		{"running", "--store", path, "--json"},
	} {
		code, out, stderr := runViewStderr(t, args...)
		if code != 0 {
			t.Fatalf("view %v failed on legacy store (%d): out=%s stderr=%s", args, code, out, stderr)
		}
	}

	// 2. status --records with both pre-change and new run.record envelopes
	recDir := t.TempDir()
	oldEnvObj := envelope(t, "run.record", "rec-old", nil)
	oldEnvObj.CreatedAt = time.UnixMilli(1000).UTC()
	oldEnvObj.Payload = json.RawMessage(`{"run_id":"run-old","item_id":"item-1","lane_id":"lane-1","backend":"pi","state":"exited","started_at":"2026-11-18T00:00:00Z","finished_at":"2026-11-18T00:01:00Z","event_seq":1,"artifact_refs":[]}`)
	oldEnv, err := record.Encode(oldEnvObj)
	if err != nil {
		t.Fatal(err)
	}
	newEnvObj := envelope(t, "run.record", "rec-new", nil)
	newEnvObj.CreatedAt = time.UnixMilli(2000).UTC()
	newEnvObj.Payload = json.RawMessage(`{"run_id":"run-new","item_id":"item-1","lane_id":"lane-1","backend":"pi","seat":"edit","workflow":"work","node":"piece-view","state":"exited","started_at":"2026-11-20T00:00:00Z","finished_at":"2026-11-20T00:01:00Z","event_seq":2,"artifact_refs":[]}`)
	newEnv, err := record.Encode(newEnvObj)
	if err != nil {
		t.Fatal(err)
	}
	recFile := filepath.Join(recDir, "records.json")
	stream := append([]byte{'['}, oldEnv...)
	stream = append(stream, ',')
	stream = append(stream, newEnv...)
	stream = append(stream, ']')
	if err := os.WriteFile(recFile, stream, 0644); err != nil {
		t.Fatal(err)
	}
	code, recOut, recStderr := runViewStderr(t, "status", "--records", recFile, "--json")
	if code != 0 {
		t.Fatalf("status --records failed (%d): out=%s stderr=%s", code, recOut, recStderr)
	}
	var recEnv map[string]any
	if err := json.Unmarshal(recOut, &recEnv); err != nil {
		t.Fatal(err)
	}
	recStatus := recEnv["payload"].(map[string]any)
	recRuns := recStatus["runs"].([]any)
	if len(recRuns) != 2 {
		t.Fatalf("expected 2 runs, got %d: %s", len(recRuns), recOut)
	}
	r0 := recRuns[0].(map[string]any)
	r1 := recRuns[1].(map[string]any)
	if _, ok := r0["seat"]; ok {
		t.Fatalf("pre-change record should omit seat: %+v", r0)
	}
	if r1["seat"] != "edit" || r1["workflow"] != "work" || r1["node"] != "piece-view" {
		t.Fatalf("new record missing seat/workflow/node: %+v", r1)
	}
}
