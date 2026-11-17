package main

import (
	"bytes"
	"context"
	"net/http"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestGraphRevisionItemAndWaiting(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE graph_revisions(revision_id INTEGER PRIMARY KEY,item_id TEXT,proposing_run TEXT,proposing_node TEXT,target TEXT,workflow TEXT,rule TEXT,detail TEXT,before_nodes INTEGER,after_nodes INTEGER,recorded_at INTEGER)`)
	h.exec(`INSERT INTO graph_revisions VALUES(1,'item-seed-06','plan-run','open','write','replacement@v1','','',4,5,1000),(2,'item-seed-06','second-run','plan-again','write','replacement@v1','proposal-target-started','write has a run',5,5,2000)`)
	status, body, raw := h.get("/api/v1/items/item-seed-06")
	if status != http.StatusOK {
		t.Fatalf("%d: %s", status, raw)
	}
	revisions := body["data"].(map[string]any)["graph_revisions"].([]any)
	if len(revisions) != 2 {
		t.Fatalf("revisions: %s", raw)
	}
	accepted := revisions[0].(map[string]any)
	refused := revisions[1].(map[string]any)
	if accepted["proposing_node"] != "open" || accepted["before_nodes"] != float64(4) || accepted["after_nodes"] != float64(5) || refused["rule"] != "proposal-target-started" || refused["proposing_run"] != "second-run" {
		t.Fatal(revisions)
	}
	rows := []waitingRow{{ItemID: "item-seed-06", Why: "running"}}
	st, err := store.OpenRead(context.Background(), h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	if err := st.ReadTx(context.Background(), func(r *store.Reader) error { return addGraphRevisionHistory(r, rows) }); err != nil {
		t.Fatal(err)
	}
	var out bytes.Buffer
	if err := printWaiting(&out, rows); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out.String(), "graph changed by open (plan-run): 4 → 5 nodes") {
		t.Fatal(out.String())
	}
	if len(rows[0].GraphChanges) != 1 {
		t.Fatal(rows)
	}
}

func TestGraphRevisionOldStore(t *testing.T) {
	h := newServeHarness(t)
	status, body, raw := h.get("/api/v1/items/item-seed-06")
	if status != http.StatusOK {
		t.Fatalf("%d: %s", status, raw)
	}
	if rows, ok := body["data"].(map[string]any)["graph_revisions"].([]any); !ok || len(rows) != 0 {
		t.Fatalf("old store: %s", raw)
	}
}
