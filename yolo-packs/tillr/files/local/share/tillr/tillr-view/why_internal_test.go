package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr/store"
)

// storeFingerprint answers the store's logical content. A test that a face writes nothing
// compares these, never the database file's bytes: a commit stays in the write-ahead log,
// so the file does not move when a face writes (store.Fingerprint).
func storeFingerprint(t *testing.T, path string) string {
	t.Helper()
	f, err := store.Fingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

func TestWhyAPIAndWaitingBeforeAnyPass(t *testing.T) {
	dir := plannerFixtureDir(t)
	gitRoot(t, dir)
	viewBin, drainBin := buildPairInto(t, dir)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	path := filepath.Join(dir, "state.db")
	input := filepath.Join(dir, "pass.json")
	raw := `{"record_version":1,"kind":"drain.pass","id":"fixture","producer":{"system":"tillr","utility":"drain","version":"0.1.0"},"created_at":"2026-11-16T00:00:00Z","payload":{}}`
	if err := os.WriteFile(input, []byte(raw), 0600); err != nil {
		t.Fatal(err)
	}
	// Initialize the current schema through the owning utility, before any item exists.
	if out, err := exec.Command(drainBin, "once", "--store", path, "--input", input, "--runner", "record-sink", "--runner-output", filepath.Join(dir, "sink.json")).CombinedOutput(); err != nil {
		t.Fatalf("init: %v %s", err, out)
	}
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	_, err = db.Exec(`INSERT INTO items(item_id,title,state,rank,allowed_paths,updated_at) VALUES ('a','ready','in-queue','a','["tools/a/"]',0),('b','blocked','in-queue','b','["tools/b/"]',0),('done','landed','landed','c','[]',0); INSERT INTO item_edges(item_id,blocked_by) VALUES ('b','a'); INSERT INTO lanes(lane_id,state) VALUES ('lane-1','idle'); INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (0,'item.record','item','a','{"state":"in-queue"}'),(0,'item.record','item','b','{"state":"in-queue"}')`)
	if err != nil {
		t.Fatal(err)
	}
	before := storeFingerprint(t, path)
	srv := newServer(context.Background(), path, "", "", filepath.Join(dir, "no-tillr-trace"), dir, "", nil)
	defer srv.close()
	httpServer := httptest.NewServer(srv.routes())
	defer httpServer.Close()
	h := &serveHarness{t: t, http: httpServer}
	status, body, rawBody := h.get("/api/v1/why")
	if status != 200 {
		t.Fatalf("why: %d %s", status, rawBody)
	}
	data := body["data"].(map[string]any)
	rows := data["rows"].([]any)
	if len(rows) != 2 {
		t.Fatalf("rows: %s", rawBody)
	}
	for _, value := range rows {
		row := value.(map[string]any)
		if row["reason"] == "" || row["why"] == "no pass yet" || row["class_source"] != "planner" {
			t.Fatalf("unexplained row: %v", row)
		}
	}
	if rows[0].(map[string]any)["reason"] != "no-policy" || rows[1].(map[string]any)["mover"] != "drain; a" {
		t.Fatalf("actual planner reasons: %s", rawBody)
	}
	summary := data["summary"].(map[string]any)
	count := summary["running"].(float64) + summary["could_run"].(float64)
	for _, n := range summary["held"].(map[string]any) {
		count += n.(float64)
	}
	if int(count) != len(rows) {
		t.Fatalf("summary does not partition rows: %s", rawBody)
	}
	if body["snapshot"].(map[string]any)["event_head_seq"] != data["event_head_seq"] {
		t.Fatalf("cursor not planner's: %s", rawBody)
	}

	out, err := exec.Command(viewBin, "waiting", "--store", path, "--root", dir, "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("waiting: %v %s", err, out)
	}
	var waiting []waitingRow
	if err := json.Unmarshal(out, &waiting); err != nil {
		t.Fatal(err)
	}
	if len(waiting) != 2 || waiting[0].Why != "no-policy" || waiting[1].Mover != "drain; a" {
		t.Fatalf("waiting diverged: %s", out)
	}
	if storeFingerprint(t, path) != before {
		t.Fatal("why/waiting wrote store")
	}
	var n int
	if err := db.QueryRow(`SELECT COUNT(*) FROM dispatches`).Scan(&n); err != nil || n != 0 {
		t.Fatalf("recorded pass: %d %v", n, err)
	}
}

// The drain's restart test exercises the real processes. Here the same event
// ordering and lease feed the real planner, and we check what the view exposes.
func TestWhyAPIAndWaitingShowTheLiveResumedWorker(t *testing.T) {
	viewBin, drainBin := buildPair(t)
	dir, path := whyFixture(t, drainBin)
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	owner, err := presence.CurrentOwner()
	if err != nil {
		t.Fatal(err)
	}
	ownerJSON, err := json.Marshal(owner)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`
INSERT INTO drain_sessions(session_id,started_at,refreshed_at,next_pass_at,ended_at,loop_owner,lanes) VALUES
  ('original',500,3000,3000,3500,NULL,'["lane-1"]'),
  ('replacement',4000,4000,4000,NULL,?,'["lane-1"]');
UPDATE items SET state='in-progress' WHERE item_id='a';
UPDATE lanes SET state='leased',run_id='run-a-a1' WHERE lane_id='lane-1';
INSERT INTO runs(run_id,item_id,lane_id,backend,state,end_cause,started_at,finished_at) VALUES
  ('run-a-a1','a','lane-1','pi','paused','restart',1000,2000);
INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES
  (1000,'drain.started','run','run-a-a1','{"loop_id":"original","decision":{"item_id":"a","lane_id":"lane-1","attempt":1,"run_request":{"run_id":"run-a-a1","item_id":"a","lane_id":"lane-1","backend":"pi"}}}'),
  (2000,'run.finish','run','run-a-a1','{"run_id":"run-a-a1","state":"paused","end_cause":"restart"}'),
  (3000,'run.resume','run','run-a-a1','{"run_id":"run-a-a1","item_id":"a","lane_id":"lane-1","backend":"pi","session_id":"11111111-1111-4111-8111-111111111111","resumed_at":"1970-01-01T00:00:03Z"}');`, string(ownerJSON))
	if err != nil {
		t.Fatal(err)
	}
	before := storeFingerprint(t, path)
	srv := newServer(context.Background(), path, "", "", filepath.Join(dir, "no-tillr-trace"), dir, "", nil)
	defer srv.close()
	srv.plannerFlag = drainBin
	httpServer := httptest.NewServer(srv.routes())
	defer httpServer.Close()
	h := &serveHarness{t: t, http: httpServer}
	status, body, raw := h.get("/api/v1/why")
	if status != 200 {
		t.Fatalf("why: %d %s", status, raw)
	}
	var worker map[string]any
	for _, value := range body["data"].(map[string]any)["rows"].([]any) {
		row := value.(map[string]any)
		if row["item_id"] == "a" {
			if worker != nil {
				t.Fatalf("duplicate worker row: %s", raw)
			}
			worker = row
		}
	}
	if worker["position"] != "running" || worker["class"] != "moving" || worker["reason"] != "run-live" || worker["class_source"] != "planner" {
		t.Fatalf("expected the planner's running row, got %+v", worker)
	}
	why, _ := worker["why"].(string)
	mover, _ := worker["mover"].(string)
	if !strings.Contains(why, "run-a-a1 on lane-1") || !strings.Contains(why, "timing does not establish takeover") || strings.Contains(why, "does not take over") ||
		!strings.HasPrefix(mover, "worker run-a-a1 on lane-1;") || !strings.Contains(mover, "waits for its recorded finish") {
		t.Fatalf("why does not explain where the worker is and who moves: %+v", worker)
	}
	out, err := exec.Command(viewBin, "waiting", "--store", path, "--root", dir).CombinedOutput()
	if err != nil {
		t.Fatalf("waiting: %v %s", err, out)
	}
	var count int
	for _, line := range strings.Split(string(out), "\n") {
		fields := strings.Fields(line)
		if len(fields) > 2 && fields[1] == "a" {
			count++
			if strings.Contains(line, "no-lane") || !strings.Contains(line, "moving (running)") || !strings.Contains(line, why) || !strings.Contains(line, mover) {
				t.Errorf("waiting must show running, location and next mover on one row: %s", line)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected one waiting row for a, got %d:\n%s", count, out)
	}
	out, err = exec.Command(viewBin, "waiting", "--store", path, "--root", dir, "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("waiting --json: %v %s", err, out)
	}
	var rows []waitingRow
	if err := json.Unmarshal(out, &rows); err != nil {
		t.Fatalf("waiting --json: %v %s", err, out)
	}
	count = 0
	for _, row := range rows {
		if row.ItemID == "a" {
			count++
			if row.Position != "running" || row.Why != why || row.Mover != mover {
				t.Errorf("waiting --json disagrees with the planner: %+v", row)
			}
		}
	}
	if count != 1 {
		t.Errorf("expected one JSON waiting row for a, got %d: %s", count, out)
	}
	if storeFingerprint(t, path) != before {
		t.Fatal("why/waiting wrote store")
	}
}

func TestWhyMissingPlannerIsAnExplicitError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, _, err := askPlanner(context.Background(), filepath.Join(t.TempDir(), "absent-tillr-drain"), buildStamp{}, "unused", ".", "", "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "--tillr-drain") || !strings.Contains(err.Error(), "just observatory") {
		t.Fatalf("missing planner: %v", err)
	}
}
