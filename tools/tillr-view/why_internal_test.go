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

func TestWhyMissingPlannerIsAnExplicitError(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	_, _, err := askPlanner(context.Background(), filepath.Join(t.TempDir(), "absent-tillr-drain"), buildStamp{}, "unused", ".", "", "", nil, nil)
	if err == nil || !strings.Contains(err.Error(), "--tillr-drain") || !strings.Contains(err.Error(), "just observatory") {
		t.Fatalf("missing planner: %v", err)
	}
}
