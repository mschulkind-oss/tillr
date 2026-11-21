package main

import (
	"database/sql"
	"encoding/json"
	"os/exec"
	"strings"
	"testing"
)

func TestBackendAlarmsLeadQueueFacesAndClearOnEvidence(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	sqlRun := func(q string) {
		t.Helper()
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	for _, q := range []string{
		`CREATE TABLE backend_outcomes(terminal_seq INTEGER PRIMARY KEY, class TEXT NOT NULL, closes INTEGER NOT NULL)`,
		`ALTER TABLE dispatches ADD COLUMN deferred_by TEXT`,
		`ALTER TABLE dispatches ADD COLUMN seat TEXT`,
		`INSERT INTO items(item_id,title,state,updated_at) VALUES ('judged','Finished change','in-progress',1000)`,
		`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (1795248000000,'run.finish','run','down','{"run_id":"down","backend":"fixture","state":"exited","exit_code":2}')`,
		`INSERT INTO backend_outcomes SELECT seq,'infrastructure',1 FROM events WHERE object_id='down'`,
		`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (1795247999999,'drain.candidate','run','candidate-run','{"item_id":"judged","run_id":"candidate-run","lane_id":"lane-2"}')`,
		`INSERT INTO dispatches(decision_id,item_id,lane_id,run_id,action,reason,dispatched_at,deferred_by,seat) VALUES ('judge-hold','judged','lane-2','candidate-run','hold','backend-closed',1795248000000,'fixture','review')`,
		`CREATE TABLE backend_windows(backend TEXT PRIMARY KEY,kind TEXT,run_id TEXT,reset_at INTEGER)`,
		`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (1795248000000,'run.finish','run','limited','{"run_id":"limited","backend":"quota-peer","state":"exited"}')`,
		`INSERT INTO backend_windows VALUES ('quota-peer','five_hour','limited',1795248120000)`,
	} {
		sqlRun(q)
	}
	view, planner := buildPair(t)
	clock := "2026-11-21T08:00:00Z"
	check := func(want int, args ...string) string {
		t.Helper()
		cmd := exec.Command(view, append(args, "--store", h.store, "--now", clock, "--root", h.dir)...)
		out, err := cmd.Output()
		code := 0
		if exit, ok := err.(*exec.ExitError); ok {
			code = exit.ExitCode()
			if code != want {
				t.Fatalf("%v: exit %d, want %d: %s\n%s", args, code, want, out, exit.Stderr)
			}
		} else if err != nil {
			t.Fatal(err)
		}
		if code != want {
			t.Fatalf("%v: exit %d, want %d: %s", args, code, want, out)
		}
		return string(out)
	}
	out := check(1, "alarms")
	for _, s := range []string{"ALARM backend-closed (fixture)", "ALARM backend-window (quota-peer)", "ALARM judge-backend-unavailable (fixture)", "EST", "1 item(s) [judged], 1 lane(s) [lane-2]", "run down (exited, exit 2)", "probe-backend", "provider reset"} {
		if !strings.Contains(out, s) {
			t.Errorf("alarm output lacks %q:\n%s", s, out)
		}
	}
	for _, args := range [][]string{{"status", "--text"}, {"waiting", "--history"}} {
		if out := check(0, args...); !strings.HasPrefix(out, "ALARM backend-closed") {
			t.Errorf("%v starts with %q", args, out)
		}
	}
	root, modern := whyFixture(t, planner)
	current, err := sql.Open("sqlite", "file:"+modern)
	if err != nil {
		t.Fatal(err)
	}
	defer current.Close()
	if _, err := current.Exec(`INSERT INTO backend_windows(backend,kind,fraction,status,reset_at,run_id) VALUES ('quota-peer','five_hour',1,'rejected',1795248120000,'limited')`); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(view, "waiting", "--store", modern, "--root", root, "--now", clock)
	if out, err := cmd.Output(); err != nil || !strings.HasPrefix(string(out), "ALARM backend-window (quota-peer)") {
		t.Fatalf("live waiting: %v %s", err, out)
	}
	var waiting []waitingRow
	if err := json.Unmarshal([]byte(check(0, "waiting", "--history", "--json")), &waiting); err != nil {
		t.Fatal(err)
	}
	if len(waiting) < 3 || waiting[0].Alarm == nil || waiting[0].Alarm.Kind != "backend-closed" {
		t.Fatalf("waiting did not lead with backend alarm: %+v", waiting)
	}
	resp, err := h.http.Client().Get(h.http.URL + "/api/v1/snapshot?now=" + clock)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	var snapshot struct {
		Data snapshotData `json:"data"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&snapshot); err != nil {
		t.Fatal(err)
	}
	if resp.StatusCode != 200 || len(snapshot.Data.Alarms) != 3 {
		t.Fatalf("snapshot: HTTP %d alarms %+v", resp.StatusCode, snapshot.Data.Alarms)
	}
	status, body, raw := h.get("/api/v1/waiting?now=" + clock)
	apiRows, _ := json.Marshal(body["data"])
	waiting = nil
	if err := json.Unmarshal(apiRows, &waiting); status != 200 || err != nil || len(waiting) == 0 || waiting[0].Alarm == nil {
		t.Fatalf("waiting API: HTTP %d, %v, %s", status, err, raw)
	}
	sqlRun(`INSERT INTO events(ts,kind,object_id,payload) VALUES (1795248000001,'drain.backend-probe','fixture','{"success":false}')`)
	check(1, "alarms")
	sqlRun(`INSERT INTO events(ts,kind,object_id,payload) VALUES (1795248000002,'drain.backend-probe','fixture','{"success":true}')`)
	if out := check(1, "alarms"); strings.Contains(out, "fixture") {
		t.Fatalf("successful probe did not clear the backend and judge alarms: %s", out)
	}
	clock = "2026-11-22T00:00:00Z"
	if out := check(0, "alarms"); out != "" {
		t.Fatalf("expired window still raised an alarm: %s", out)
	}
}
