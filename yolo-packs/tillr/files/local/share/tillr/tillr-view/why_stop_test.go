package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"testing"
)

func TestWaitingDefaultStoppedRunKeepsPlannerAnswer(t *testing.T) {
	dir := t.TempDir()
	drainBin := filepath.Join(dir, "tillr-drain")
	if out, err := exec.Command("go", "-C", "../tillr-drain", "build", "-o", drainBin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build drain: %v %s", err, out)
	}
	input := filepath.Join(dir, "pass.json")
	if err := os.WriteFile(input, []byte(`{"record_version":1,"kind":"drain.pass","id":"fixture","producer":{"system":"tillr","utility":"drain","version":"0.1.0"},"created_at":"2026-11-16T00:00:00Z","payload":{}}`), 0600); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "state.db")
	if out, err := exec.Command(drainBin, "once", "--store", path, "--input", input, "--runner", "record-sink", "--runner-output", filepath.Join(dir, "sink.json")).CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`INSERT INTO items(item_id,title,state,rank,allowed_paths,updated_at) VALUES ('stopped-item','stopped','in-queue','a','["tools/a/"]',0);
INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES ('stopped-run','stopped-item','lane-1','pi','exited',1000,2000,'[]',1);
INSERT INTO dispatches(decision_id,item_id,run_id,action,reason,dispatched_at) VALUES ('dispatch','stopped-item','stopped-run','dispatch','ready',1000);
INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (2000,'drain.orphaned','run','stopped-run','{}')`); err != nil {
		t.Fatal(err)
	}
	_, want, err := askPlanner(context.Background(), drainBin, buildStamp{}, path, dir, "", "", nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(want) != 1 || want[0].Class != "needs you" || want[0].Why != "no-policy" || want[0].Mover != "supply the loop's --policy file" {
		t.Fatalf("planner answer: %+v", want)
	}
	viewBin := filepath.Join(dir, "tillr-view")
	if out, err := exec.Command("go", "build", "-o", viewBin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build view: %v %s", err, out)
	}
	out, err := exec.Command(viewBin, "waiting", "--store", path, "--root", dir, "--tillr-drain", drainBin, "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("waiting: %v %s", err, out)
	}
	var got []waitingRow
	if err := json.Unmarshal(out, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("default changed planner answer: got %+v, want %+v", got, want)
	}
	history, err := readWaiting(context.Background(), path, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Why != "run ended by a stop" {
		t.Fatalf("history: %+v", history)
	}
}
