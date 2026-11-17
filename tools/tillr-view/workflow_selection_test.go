package main

import (
	"net/http"
	"testing"
)

func TestItemWorkflowSelection(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`CREATE TABLE workflow_selections(item_id TEXT PRIMARY KEY,workflow TEXT,source TEXT,graph TEXT)`)
	h.exec(`INSERT INTO workflow_selections VALUES('item-seed-02','build@v2','default','')`)
	status, body, raw := h.get("/api/v1/items/item-seed-02")
	if status != http.StatusOK {
		t.Fatalf("%d: %s", status, raw)
	}
	row := body["data"].(map[string]any)["item"].(map[string]any)
	if row["selected_workflow"] != "build@v2" || row["workflow_source"] != "default" {
		t.Fatalf("selection: %v", row)
	}
}
