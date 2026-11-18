package main

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
)

func TestItemGraphNodesAndSingleWorker(t *testing.T) {
	h := newServeHarness(t)
	path := filepath.Join(t.TempDir(), "out.txt")
	if err := os.WriteFile(path, []byte("result text"), 0600); err != nil {
		t.Fatal(err)
	}
	h.exec(`CREATE TABLE workflow_selections(item_id TEXT PRIMARY KEY,workflow TEXT,source TEXT,graph TEXT);
 CREATE TABLE node_runs(item_id TEXT,attempt INTEGER,node_id TEXT,run_id TEXT,outcome TEXT);
 CREATE TABLE node_artifacts(item_id TEXT,attempt INTEGER,node_id TEXT,run_id TEXT,name TEXT,type TEXT,sha256 TEXT,location TEXT);
 INSERT INTO workflow_selections VALUES('item-seed-04','test@v1','block','{"stages":[{"id":"outer/a","parent":"outer","kind":"agent","seat":"scout","access":"read"},{"id":"outer/b","parent":"outer","kind":"agent","seat":"review","access":"read"},{"id":"write","kind":"command","command_ref":"build","access":"write"}],"edges":[{"from_node":"outer/a","to_node":"write"},{"from_node":"outer/b","to_node":"write"}]}');
 INSERT INTO node_runs VALUES('item-seed-04',1,'outer/a','run-1','passed'),('item-seed-04',1,'outer/b','run-2','running');`)
	// Use the store's quoted literal rather than an API path for the artifact location.
	h.exec("INSERT INTO node_artifacts VALUES('item-seed-04',1,'outer/a','run-1','report','text',''," + "'" + path + "')")
	status, _, raw := h.get("/api/v1/items/item-seed-04")
	if status != 200 {
		t.Fatalf("%d: %s", status, raw)
	}
	var body struct {
		Data struct {
			GraphAttempts []itemGraphAttempt `json:"graph_attempts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.GraphAttempts) != 1 {
		t.Fatalf("attempts: %s", raw)
	}
	nodes := body.Data.GraphAttempts[0].Nodes
	if len(nodes) != 3 || nodes[0].ID != "outer/a" || nodes[0].Parent != "outer" || nodes[0].Seat != "scout" || nodes[0].Access != "read" || nodes[0].State != "passed" || nodes[1].State != "running" || nodes[2].State != "not started" || nodes[2].CommandRef != "build" || nodes[0].Level != nodes[1].Level || nodes[2].Level <= nodes[0].Level {
		t.Fatalf("nodes: %+v", nodes)
	}
	if len(nodes[0].Outputs) != 1 || nodes[0].Outputs[0].Body == nil || *nodes[0].Outputs[0].Body != "result text" {
		t.Fatalf("output: %+v", nodes[0].Outputs)
	}
	status, _, raw = h.get("/api/v1/items/item-seed-02")
	if status != 200 {
		t.Fatalf("%d: %s", status, raw)
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.GraphAttempts) != 1 || len(body.Data.GraphAttempts[0].Nodes) != 1 || len(body.Data.GraphAttempts[0].Nodes[0].Runs) != 1 || body.Data.GraphAttempts[0].Nodes[0].Runs[0] != "run-1" {
		t.Fatalf("single worker: %s", raw)
	}
}
