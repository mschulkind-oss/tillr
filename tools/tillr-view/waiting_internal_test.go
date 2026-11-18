package main

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
)

// The CLI and API must answer the same moment without modifying any table or
// the database bytes. The fixture breaks both the reason and the action streak.
func TestWaitingCLIAndAPI(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	statements := []string{
		`ALTER TABLE items ADD COLUMN rank TEXT`,
		`UPDATE items SET rank = 'a' WHERE item_id = 'item-seed-02'`,
		`DELETE FROM dispatches`,
		`INSERT INTO items (item_id,title,state,updated_at) VALUES
   ('item-closed','closed','closed',1000),
   ('item-resolved','resolved','resolved-elsewhere',1000),
   ('item-unknown','unknown','in-queue',1000),
   ('item-moving','moving','in-queue',1000),
   ('item-land','landing conflict','in-queue',1000),
   ('item-fence','fence refusal','in-queue',1000)`,
		`INSERT INTO dispatches (decision_id,item_id,action,reason,dispatched_at) VALUES
   ('d1','item-seed-02','hold','gate-failed',1000),
   ('d2','item-seed-02','hold','blocked',2000),
   ('d3','item-seed-02','hold','gate-failed',3000),
   ('d4','item-seed-02','dispatch','gate-failed',4000),
   ('d5','item-seed-02','hold','gate-failed',5000),
   ('d6','item-seed-02','hold','gate-failed',6000),
   ('u1','item-unknown','hold','future-reason',7000),
   ('m1','item-moving','dispatch','ready',8000),
   ('l1','item-land','hold','retry-stale',9000),
   ('f1','item-fence','hold','not-ready',9000)`,
		`INSERT INTO runs (run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq)
   VALUES ('run-moving','item-moving','lane-b','pi','running',8000,0,'[]',10),
   ('run-unknown','item-unknown','lane-b','pi','finished',8000,8500,'[]',11),
   ('run-land','item-land','lane-b','pi','finished',8000,8500,'[]',12),
   ('run-fence','item-fence','lane-b','pi','finished',8000,8500,'[]',13)`,
		`INSERT INTO events (ts,kind,object_type,object_id,payload) VALUES
   (8500,'gate.verdict','run','run-unknown','{"run_id":"run-unknown","result":"pass"}'),
   (8500,'drain.refusal','run','run-fence','{"run_id":"run-fence","item_id":"item-fence","step":"fence"}')`,
		`CREATE TABLE landings (landing_id TEXT PRIMARY KEY,item_id TEXT NOT NULL,run_id TEXT NOT NULL,result TEXT NOT NULL,event_seq INTEGER NOT NULL)`,
		`INSERT INTO landings VALUES ('landing-1','item-land','run-land','squash-conflict',14)`,
	}
	for _, statement := range statements {
		if _, err := db.Exec(statement); err != nil {
			t.Fatal(err)
		}
	}
	beforeCounts := waitingTableCounts(t, db)
	before := storeFingerprint(t, h.store)
	bin := filepath.Join(t.TempDir(), "tillr-view")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v: %s", err, out)
	}
	cmd := exec.Command(bin, "waiting", "--history", "--store", h.store, "--json")
	cli, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("waiting CLI: %v: %s", err, cli)
	}
	var want []map[string]any
	if err := json.Unmarshal(cli, &want); err != nil {
		t.Fatalf("CLI JSON: %v: %s", err, cli)
	}
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("waiting API status %d: %s", status, raw)
	}
	api, _ := json.Marshal(body["data"])
	var got []map[string]any
	if err := json.Unmarshal(api, &got); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("API %s differs from CLI %s", api, cli)
	}
	if len(got) != 6 {
		t.Fatalf("unlanded rows: %s", api)
	}
	if got[0]["item_id"] != "item-seed-02" {
		t.Fatalf("rank order: %s", api)
	}
	byID := map[string]map[string]any{}
	for _, row := range got {
		if len(row) != 10 || row["document"] != "" {
			t.Fatalf("want seven columns plus class_source, waited and an empty document on item rows: %v", row)
		}
		if row["class_source"] != "rule" {
			t.Fatalf("unrecorded class: %v", row)
		}
		byID[row["item_id"].(string)] = row
	}
	for id, fields := range map[string]map[string]any{
		"item-seed-02": {"class": "needs a cause", "where": "gate", "why": "gate-failed", "mover": "operator; cause not recorded; end cause not recorded", "since": "1970-01-01T00:00:05Z"},
		"item-seed-06": {"since": "no pass yet", "why": "waiter not recorded", "mover": "drain; waiter not recorded"},
		"item-unknown": {"class": "needs a cause", "where": "gate", "why": "future-reason", "mover": "operator; add a history rule for this recorded reason; end cause not recorded"},
		"item-land":    {"where": "land", "why": "retry-stale"},
		"item-fence":   {"where": "fence", "why": "not-ready"},
		"item-moving":  {"class": "moving", "where": "lane", "why": "ready", "mover": "run-moving"},
	} {
		for key, value := range fields {
			if byID[id][key] != value {
				t.Errorf("%s %s = %v, want %v", id, key, byID[id][key], value)
			}
		}
	}
	table, err := exec.Command(bin, "waiting", "--history", "--store", h.store).CombinedOutput()
	if err != nil {
		t.Fatalf("table: %v: %s", err, table)
	}
	lines := strings.Split(strings.TrimSpace(string(table)), "\n")
	if len(lines) != 11 || !strings.HasPrefix(lines[0], "definition: ") || lines[1] != "spend, last 24h: $0.00 over 0 runs" || lines[2] != "1 of 1 running, 0 of 0 express" || !strings.HasPrefix(lines[3], "summary: owner-waiting ") || strings.Join(strings.Fields(lines[4]), " ") != "# item_id class where why mover since" {
		t.Fatalf("table columns/rows: %s", table)
	}
	if storeFingerprint(t, h.store) != before {
		t.Fatal("waiting changed the store's content")
	}
	if !reflect.DeepEqual(beforeCounts, waitingTableCounts(t, db)) {
		t.Fatal("waiting changed a table's row count")
	}
}

func waitingTableCounts(t *testing.T, db *sql.DB) map[string]int {
	t.Helper()
	rows, err := db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatal(err)
	}
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatal(err)
		}
		tables = append(tables, name)
	}
	if err := rows.Err(); err != nil {
		t.Fatal(err)
	}
	rows.Close()
	counts := map[string]int{}
	for _, table := range tables {
		var n int
		if err := db.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM "%s"`, strings.ReplaceAll(table, `"`, `""`))).Scan(&n); err != nil {
			t.Fatal(err)
		}
		counts[table] = n
	}
	return counts
}

// Every row of the shared holdrules table must read back as an explained history row.
// The table is the single place a reason's rule is declared, and the drain module's own
// TestEveryReasonConstantHasAHoldRule requires a row for every exported Reason constant,
// so this test cannot fail for a missing rule; it checks that each row is displayable.
func TestWaitingEveryDrainReasonHasARule(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DELETE FROM dispatches; DELETE FROM items`)
	checked := 0
	for reason := range holdrules.Rules {
		checked++
		id := "reason-" + reason
		h.exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES (?,'hold','in-queue',1); INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES (?,?,'hold',?,1000)`, id, id, id, reason)
	}
	if checked == 0 {
		t.Fatal("no hold rules were enumerated")
	}
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(rows) != checked {
		t.Fatalf("got %d held rows, want %d", len(rows), checked)
	}
	for _, row := range rows {
		t.Run(row.ItemID, func(t *testing.T) {
			if row.Class == "unclassified" || row.Class == "" || strings.TrimSpace(row.Why) == "" || row.Mover == "" || strings.HasPrefix(row.Mover, "—") {
				t.Fatalf("unexplained hold %q: %+v", row.Why, row)
			}
		})
	}
	t.Logf("all %d hold rules read back as explained history rows", checked)
}

func TestWaitingMoversAndLeaseOnlyRun(t *testing.T) {
	index := `{"index":{"documents":[{"path":"docs/design.md","questions":[{"id":"OQ-1","state":"open","title":"OQ-1: proceed?"}]}]}}`
	h := newServeHarnessWithQuestions(t, "", stubIndexCommand(t, index))
	if err := os.MkdirAll(filepath.Join(h.dir, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(h.dir, "docs/design.md"), []byte("# Design\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	h.exec(`ALTER TABLE dispatches ADD COLUMN deferred_by TEXT`)
	h.exec(`UPDATE items SET design_doc = 'docs/design.md' WHERE item_id = 'item-seed-02'`)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`INSERT INTO dispatches (decision_id,item_id,action,reason,dispatched_at,deferred_by) VALUES
  ('q','item-seed-02','hold','question-open',1000,NULL),
  ('b','item-seed-06','hold','blocked',1000,NULL)`)
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("status %d: %s", status, raw)
	}
	data := body["data"].([]any)
	if data[0].(map[string]any)["mover"] != "owner; answer docs/design.md#OQ-1; end cause not recorded" || data[1].(map[string]any)["mover"] != "drain; item-seed-02" {
		t.Fatalf("question and blocker movers: %s", raw)
	}
	// A deferred mover is the recorded item, not an invented cause.
	h.exec(`UPDATE dispatches SET reason = 'fork-held', deferred_by = 'item-fork' WHERE decision_id = 'b'`)
	// Running work has no terminal runs row yet: allocation plus lease is enough.
	h.exec(`INSERT INTO lanes (lane_id,state,run_id,leased_until) VALUES ('lane-live','leased','run-live',99999)`)
	h.exec(`INSERT INTO events (ts,kind,payload) VALUES (2000,'run.allocate',
  '{"run_id":"run-live","item_id":"item-seed-02","lane_id":"lane-live","backend":"pi","started_at":"1970-01-01T00:00:02Z"}')`)
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Class != "moving" || rows[0].Mover != "run-live" || rows[0].Why != "question-open" || rows[0].Since != "1970-01-01T00:00:01Z" {
		t.Fatalf("lease-only live run: %+v", rows[0])
	}
	if rows[1].Mover != "drain; item-fork" {
		t.Fatalf("deferred mover: %+v", rows[1])
	}
}

func TestWaitingStreakIsNotTruncated(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`INSERT INTO dispatches (decision_id,item_id,action,reason,dispatched_at) VALUES
 ('a','item-seed-02','hold','gate-failed',1000),
 ('b','item-seed-02','hold','blocked',5000),
 ('c','item-seed-02','hold','gate-failed',5000)`)
	// More than any route's page size, spread across more than its default seven
	// days. The same-time b/c interruption uses the stable decision-id tiebreak.
	h.exec(`WITH RECURSIVE n(x) AS (SELECT 1 UNION ALL SELECT x+1 FROM n WHERE x < 1001)
 INSERT INTO dispatches (decision_id,item_id,action,reason,dispatched_at)
 SELECT printf('tail-%04d',x),'item-seed-02','hold','gate-failed',5000+x*86400000 FROM n`)
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	if rows[0].Since != "1970-01-01T00:00:05Z" {
		t.Fatalf("truncated or unbroken streak: %+v", rows[0])
	}
}

func TestWaitingIgnoresExcludedDecisionHistory(t *testing.T) {
	h := newServeHarness(t)
	// The terminal item's history is not part of this answer, even when its old
	// timestamp cannot be decoded. Scoping happens in SQL, not after materializing.
	h.exec(`INSERT INTO dispatches (decision_id,item_id,action,reason,dispatched_at)
 VALUES ('terminal-history','item-seed-04','hold','blocked','old-format')`)
	if _, err := readWaiting(context.Background(), h.store, nil); err != nil {
		t.Fatalf("read excluded history: %v", err)
	}
}

func TestWaitingAbsenceAndReadFailures(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DELETE FROM items`)
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil || rows == nil || len(rows) != 0 {
		t.Fatalf("empty store must answer []: %v, %v", rows, err)
	}
	h.exec(`DROP TABLE dispatches`)
	h.exec(`INSERT INTO items (item_id,title,state,updated_at) VALUES ('fresh','fresh','in-queue',1)`)
	rows, err = readWaiting(context.Background(), h.store, nil)
	if err != nil || len(rows) != 1 || rows[0].Since != "no pass yet" {
		t.Fatalf("no decision table: %v, %v", rows, err)
	}
	h.exec(`ALTER TABLE runs RENAME COLUMN backend TO unreadable_backend`)
	if _, err := readWaiting(context.Background(), h.store, nil); err == nil {
		t.Fatal("malformed run table must refuse, not answer no live runs")
	}
	status, _, raw := h.get("/api/v1/waiting")
	if status != 503 {
		t.Fatalf("unreadable waiting status %d: %s", status, raw)
	}
	if got := waitingRuleFor(`seat-degraded: seat "design" fell back to backend "pi"`).class; got != "ready" {
		t.Fatalf("declared decorated reason: %q", got)
	}
	if got := waitingRuleFor("seat-degraded-new").class; got != "needs a cause" {
		t.Fatalf("unknown similar reason: %q", got)
	}
}

// A replacement loop holds an item on `run-live` while a run from the earlier loop is still
// alive. The row must say what is waited on and name the operator's one option.
func TestWaitingRunLiveNamesTheSurvivingRun(t *testing.T) {
	rule := waitingRuleFor("run-live")
	if rule.class != "waits on work" || rule.where != "lane" {
		t.Fatalf("run-live rule = %+v, want waits on work in the lane", rule)
	}
	for _, want := range []string{"earlier loop", "finish", "tillr drain stop --end-runs"} {
		if !strings.Contains(rule.mover, want) {
			t.Errorf("run-live mover %q does not mention %q", rule.mover, want)
		}
	}
}
