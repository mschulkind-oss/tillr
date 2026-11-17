package main

import (
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

func seedPasses(t *testing.T, h *serveHarness) {
	t.Helper()
	perfExec(t, h, `ALTER TABLE items ADD COLUMN rank TEXT;
 UPDATE items SET rank = 'b' WHERE item_id = 'item-seed-02';
 UPDATE items SET rank = 'a' WHERE item_id = 'item-seed-04';
 DELETE FROM dispatches;
 ALTER TABLE dispatches ADD COLUMN pass_id TEXT;
 DELETE FROM drain_passes;
 INSERT INTO drain_passes VALUES ('p1',1000,12,13,14,15,100,0),('p2',2000,0,0,0,0,100,0);
 CREATE TABLE drain_sessions(session_id TEXT PRIMARY KEY, started_at INTEGER, refreshed_at INTEGER, next_pass_at INTEGER, ended_at INTEGER);
 INSERT INTO drain_sessions VALUES ('s1',1000,2900,3000,3000),('s2',4000,4900,5000,5000);
 INSERT INTO dispatches(decision_id,item_id,lane_id,run_id,action,reason,dispatched_at,pass_id) VALUES
 ('d1','item-seed-02','lane-a','run-1','dispatch','ready',1000,'p1'),
 ('d2','item-seed-04',NULL,NULL,'hold','blocked',1000,'p1'),
 ('d3','item-seed-02',NULL,NULL,'hold','no-lane',2000,'p2'),
 ('d4','item-seed-04',NULL,NULL,'hold','question-open',2000,'p2'),
 ('d5','item-seed-02',NULL,NULL,'hold','blocked',4000,NULL);`)
	writePassStop(t, h, 3000, "operator-stop", "The operator stopped this session.")
}

func writePassStop(t *testing.T, h *serveHarness, at int64, stop, detail string) {
	t.Helper()
	body, err := record.Encode(envelope(loopResultKind, map[string]any{"stop": stop, "detail": detail}))
	if err != nil {
		t.Fatal(err)
	}
	var env record.Envelope
	if err := json.Unmarshal(body, &env); err != nil {
		t.Fatal(err)
	}
	env.CreatedAt = time.UnixMilli(at).UTC()
	body, err = record.Encode(env)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(h.loopDir, "drain-"+env.CreatedAt.Format("20060102T150405Z")+".json")
	if err := os.WriteFile(path, body, 0644); err != nil {
		t.Fatal(err)
	}
}

func passData(t *testing.T, h *serveHarness, query string) []any {
	t.Helper()
	status, body, raw := h.get("/api/v1/passes" + query)
	if status != 200 {
		t.Fatalf("passes: %d %s", status, raw)
	}
	rows, ok := body["data"].([]any)
	if !ok {
		t.Fatalf("passes data: %s", raw)
	}
	return rows
}

func TestPassesDecisionsTimingsSessionsAndCLI(t *testing.T) {
	h := newServeHarness(t)
	seedPasses(t, h)
	rows := passData(t, h, "")
	if len(rows) != 3 {
		t.Fatalf("passes: %v", rows)
	}
	newest := rows[0].(map[string]any)
	if newest["dispatched_at_ms"] != float64(4000) || newest["stop"] != "stop not recorded" {
		t.Fatalf("missing stop: %v", newest)
	}
	hold := rows[1].(map[string]any)
	if hold["phases"] != nil || hold["stop"] != "operator-stop" || hold["detail"] != "The operator stopped this session." {
		t.Fatalf("hold-only pass: %v", hold)
	}
	decisions := hold["decisions"].([]any)
	if len(decisions) != 2 || decisions[0].(map[string]any)["item_id"] != "item-seed-04" {
		t.Fatalf("rank order: %v", decisions)
	}
	oldest := rows[2].(map[string]any)
	if _, present := oldest["stop"]; present {
		t.Fatalf("stop repeated: %v", oldest)
	}
	phases := oldest["phases"].(map[string]any)
	if phases["stage_ms"] != float64(12) || phases["gate_ms"] != float64(14) {
		t.Fatalf("phases: %v", phases)
	}
	dispatch := oldest["decisions"].([]any)[1].(map[string]any)
	if dispatch["lane_id"] != "lane-a" || dispatch["run_id"] != "run-1" || dispatch["action"] != "dispatch" || dispatch["reason"] != "ready" {
		t.Fatalf("dispatch: %v", dispatch)
	}
	page := passData(t, h, "?before=4000&since=2000")
	if len(page) != 1 || !reflect.DeepEqual(page[0], hold) {
		t.Fatalf("page: %v", page)
	}
	page = passData(t, h, "?before=1970-01-01T00:00:02Z")
	if len(page) != 1 || !reflect.DeepEqual(page[0], oldest) {
		t.Fatalf("paged newest is not session newest: %v", page)
	}
	before := storeFingerprint(t, h.store)
	bin := filepath.Join(t.TempDir(), "tillr-view")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	cli, err := exec.Command(bin, "passes", "--store", h.store, "--last", "2", "--json").CombinedOutput()
	if err != nil {
		t.Fatalf("CLI: %v %s", err, cli)
	}
	var got []any
	if err := json.Unmarshal(cli, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, rows[:2]) {
		t.Fatalf("CLI/API differ: %s / %v", cli, rows[:2])
	}
	table, err := exec.Command(bin, "passes", "--store", h.store, "--last", "3").CombinedOutput()
	if err != nil {
		t.Fatalf("CLI table: %v %s", err, table)
	}
	for _, want := range []string{"item-seed-04", "dispatch", "ready", "lane-a", "run-1", "operator-stop", "The operator stopped this session.", "stop not recorded"} {
		if !strings.Contains(string(table), want) {
			t.Errorf("table lacks %q: %s", want, table)
		}
	}
	if storeFingerprint(t, h.store) != before {
		t.Fatal("passes modified the store's content")
	}
}

func TestPassesPagingCapAndQueryErrors(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `DELETE FROM dispatches`)
	for n := 1; n <= 55; n++ {
		perfExec(t, h, `INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES (?, 'item-seed-02','hold','blocked',?)`, fmt.Sprint(n), n)
	}
	rows := passData(t, h, "")
	if len(rows) != 50 || rows[0].(map[string]any)["dispatched_at_ms"] != float64(55) {
		t.Fatalf("cap/order: %v", rows)
	}
	next := passData(t, h, "?before=6")
	if len(next) != 5 {
		t.Fatalf("backward page: %v", next)
	}
	for _, query := range []string{"?before=bad", "?since=-1", "?before=2&before=3", "?since=4&before=3", "?before=1;since=2"} {
		status, body, _ := h.get("/api/v1/passes" + query)
		if status != 400 || body["error"].(map[string]any)["code"] != codeBadQuery {
			t.Errorf("query %s: %d %v", query, status, body)
		}
	}
}

func TestPassesLegacyAndUnreadableSources(t *testing.T) {
	h := newServeHarness(t)
	// Older stores have neither pass links nor sessions. The result clock closes a session.
	writePassStop(t, h, 3000, "queue-drained", "No dispatchable work remains.")
	rows := passData(t, h, "")
	if rows[0].(map[string]any)["stop"] != "queue-drained" {
		t.Fatalf("legacy result: %v", rows)
	}
	perfExec(t, h, `DROP TABLE dispatches`)
	if rows := passData(t, h, ""); len(rows) != 0 {
		t.Fatalf("empty: %v", rows)
	}
}
