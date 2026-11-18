package main

import (
	"encoding/json"
	"testing"
)

func TestItemGraphHistoryRetriesChildrenAndRevisions(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE workflow_selections(item_id TEXT PRIMARY KEY,workflow TEXT,source TEXT,graph TEXT);
 CREATE TABLE node_runs(item_id TEXT,attempt INTEGER,node_id TEXT,run_id TEXT,outcome TEXT);
 CREATE TABLE node_reused(item_id TEXT,attempt INTEGER,node_id TEXT,source_attempt INTEGER);
 CREATE TABLE graph_revisions(revision_id INTEGER PRIMARY KEY,item_id TEXT,attempt INTEGER,proposing_run TEXT,proposing_node TEXT,target TEXT,workflow TEXT,rule TEXT,detail TEXT,before_graph TEXT,after_graph TEXT,before_nodes INTEGER,after_nodes INTEGER,recorded_at INTEGER);
 INSERT INTO workflow_selections VALUES('item-seed-04','test@v1','block','{"stages":[{"id":"old","kind":"agent","seat":"scout","access":"read"}],"edges":[]}');
 INSERT INTO node_runs VALUES('item-seed-04',1,'old','first','passed'),('item-seed-04',2,'new','retry','passed'),('item-seed-04',2,'unseen','run-none','running');
 INSERT INTO node_reused VALUES('item-seed-04',2,'old',1);
 INSERT INTO graph_revisions VALUES(1,'item-seed-04',2,'first-try','old','next','new@v1','','','{}','{"stages":[{"id":"old","kind":"agent","access":"read"},{"id":"new","parent":"group","kind":"agent","access":"read"},{"id":"unseen","kind":"agent","access":"read"}],"edges":[]}',1,3,1000);
 INSERT INTO events(kind,object_type,object_id,payload,ts) VALUES
 ('drain.started','run','first-try','{"decision":{"item_id":"item-seed-04","attempt":2,"node_index":1,"workflow":{"stages":[{"id":"old"},{"id":"new"},{"id":"unseen"}],"edges":[]}}}',1000),
 ('run.child-session','run','first-try','{"run_id":"first-try","item_id":"item-seed-04","session_id":"child","role":"reviewer"}',1001),
 ('run.children-recorded','run','retry','{"run_id":"retry","item_id":"item-seed-04"}',1002);`)
	status, _, raw := h.get("/api/v1/items/item-seed-04")
	if status != 200 {
		t.Fatalf("%d: %s", status, raw)
	}
	var body struct {
		Data struct {
			GraphAttempts  []itemGraphAttempt  `json:"graph_attempts"`
			GraphRevisions []graphRevisionView `json:"graph_revisions"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	a := body.Data.GraphAttempts
	if len(a) != 2 || a[0].Attempt != 2 || a[1].Attempt != 1 || len(a[0].Nodes) != 3 || len(a[1].Nodes) != 1 {
		t.Fatalf("attempts: %s", raw)
	}
	old, n, unseen := a[0].Nodes[0], a[0].Nodes[1], a[0].Nodes[2]
	if old.State != "passed" || len(old.Runs) != 1 || old.Runs[0] != "first" {
		t.Fatalf("reused: %+v", old)
	}
	if len(n.Runs) != 2 || n.Runs[0] != "first-try" || n.Runs[1] != "retry" || len(n.Children["first-try"].Sessions) != 1 || !n.Children["retry"].Recorded || len(n.Children["retry"].Sessions) != 0 {
		t.Fatalf("retry children: %+v", n)
	}
	if len(unseen.Children) != 0 {
		t.Fatalf("unrecorded: %+v", unseen)
	}
	if len(body.Data.GraphRevisions) != 1 || body.Data.GraphRevisions[0].ProposingNode != "old" {
		t.Fatalf("proposal: %s", raw)
	}
}
