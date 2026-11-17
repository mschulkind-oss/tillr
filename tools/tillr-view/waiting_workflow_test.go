package main

import (
	"strings"
	"testing"
)

func TestWaitingWorkflowInvalidNamesFile(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES('workflow','workflow','in-queue',0)`)
	h.exec(`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES('workflow','workflow','hold','workflow-invalid: config/tillr/workflows/broken@v1.json: workflow-cycle',1000)`)
	status, body, raw := h.get("/api/v1/waiting")
	if status != 200 {
		t.Fatalf("%d: %s", status, raw)
	}
	for _, v := range body["data"].([]any) {
		row := v.(map[string]any)
		if row["item_id"] == "workflow" {
			why, _ := row["why"].(string)
			if row["class"] == "unclassified" || row["where"] != "queue" || !strings.Contains(why, "workflow-invalid") || !strings.Contains(why, "broken@v1.json") {
				t.Fatalf("workflow hold: %+v", row)
			}
			return
		}
	}
	t.Fatal("missing workflow hold")
}
