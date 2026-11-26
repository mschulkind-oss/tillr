package main

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func setNonLandingDefinition(t *testing.T, storePath string) {
	t.Helper()
	raw := []byte(`{
		"definition": "findings",
		"version": 1,
		"states": ["not-started", "in-queue", "blocked", "closed", "landed", "confirmed", "rejected"],
		"ready": "in-queue",
		"blocked": "blocked",
		"closed": "closed",
		"finished": ["landed", "confirmed", "rejected"],
		"transitions": [
			{"from": "not-started", "to": "in-queue"},
			{"from": "in-queue", "to": "blocked"},
			{"from": "in-queue", "to": "closed"},
			{"from": "in-queue", "to": "confirmed"},
			{"from": "in-queue", "to": "rejected"},
			{"from": "blocked", "to": "in-queue"},
			{"from": "blocked", "to": "closed"},
			{"from": "closed", "to": "in-queue"}
		],
		"prerequisites": [],
		"completes": "hook",
		"retention": "keep",
		"brief_template": "",
		"hooks": {"argv": ["/bin/true"], "names": ["complete-item"]}
	}`)
	s, err := store.Open(context.Background(), storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if _, err := definition.Set(context.Background(), s, raw); err != nil {
		t.Fatal(err)
	}
}

func TestCharterFinishedStateProjectsAsFinishedOnItemPage(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`UPDATE items SET state = 'confirmed' WHERE item_id = 'item-seed-02'`)
	h.exec(`UPDATE items SET state = 'rejected' WHERE item_id = 'item-seed-06'`)
	setNonLandingDefinition(t, h.store)

	for _, tc := range []struct {
		id, rawState string
	}{
		{"item-seed-06", "rejected"},
		{"item-seed-02", "confirmed"},
	} {
		status, body, raw := h.get("/api/v1/items/" + tc.id)
		if status != 200 {
			t.Fatalf("GET %s: %d %s", tc.id, status, raw)
		}
		proj := body["data"].(map[string]any)["item"].(map[string]any)["projected_state"].(map[string]any)
		if proj["state"] != "finished" || proj["raw_state"] != tc.rawState {
			t.Fatalf("%s projected_state = %v, want state=finished raw_state=%s", tc.id, proj, tc.rawState)
		}
	}

	status, snap, raw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("GET snapshot: %d %s", status, raw)
	}
	for _, entry := range snap["data"].(map[string]any)["items"].([]any) {
		row := entry.(map[string]any)
		if row["item_id"] == "item-seed-02" {
			p := row["projected_state"].(map[string]any)
			if p["state"] != "finished" || p["raw_state"] != "confirmed" {
				t.Fatalf("snapshot projected_state = %v, want state=finished raw_state=confirmed", p)
			}
		}
	}
}

func TestCommandNotApplicableSpendNotCountedUnrecorded(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `DELETE FROM runs`)
	perfExec(t, h, `INSERT INTO events(seq,ts,kind,object_type,object_id,payload) VALUES
		(50, 2000, 'run.finish', 'run', 'run-cmd-1', '{"run_id":"run-cmd-1","item_id":"item-seed-02","lane_id":"lane-a","backend":"command","state":"exited","exit_code":0,"spend_status":"not-applicable"}')`)
	perfExec(t, h, `INSERT INTO runs(run_id,item_id,lane_id,backend,state,exit_code,started_at,finished_at,artifact_refs,usage_tokens,usage_cost_usd,event_seq) VALUES
		('run-cmd-1','item-seed-02','lane-a','command','exited',0,1500,2000,'[]',NULL,NULL,50)`)

	status, body, raw := h.get("/api/v1/items/item-seed-02")
	if status != 200 {
		t.Fatalf("GET item: %d %s", status, raw)
	}
	usage := body["data"].(map[string]any)["usage"].(map[string]any)
	if usage["unrecorded_runs"] != float64(0) || usage["lower_bound"] != false {
		t.Fatalf("item usage = %v, want unrecorded_runs=0 lower_bound=false", usage)
	}

	status, snap, raw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("GET snapshot: %d %s", status, raw)
	}
	drain := snap["data"].(map[string]any)["drain_summary"].(map[string]any)
	if drain["unreported_runs"] != float64(0) {
		t.Fatalf("snapshot drain_summary = %v, want unreported_runs=0", drain)
	}
}

func TestViewStatusOnNonLandingStoreReportsNoGitFault(t *testing.T) {
	h := newServeHarness(t)
	setNonLandingDefinition(t, h.store)
	plainDir := t.TempDir()

	env, code := statusFace([]string{"--store", h.store, "--root", plainDir})
	if code != record.ExitOK {
		t.Fatalf("statusFace exit = %d, want %d", code, record.ExitOK)
	}
	var payload statusPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatalf("unmarshal payload: %v", err)
	}
	if payload.HeavyFault != "" {
		t.Fatalf("heavy_fault = %q, want empty on non-landing store", payload.HeavyFault)
	}
	if payload.Heavy != nil {
		t.Fatalf("heavy = %+v, want nil on non-landing store", payload.Heavy)
	}
}

func TestCommandRunWithoutWorkflowNotDrawnAsAgentNode(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DELETE FROM runs WHERE item_id = 'item-seed-02'`)
	h.exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,exit_code,started_at,finished_at,artifact_refs,event_seq) VALUES
		('run-item-seed-02-a1','item-seed-02','lane-a','command','exited',0,1500,2000,'[]',0)`)

	status, _, raw := h.get("/api/v1/items/item-seed-02")
	if status != 200 {
		t.Fatalf("GET item: %d %s", status, raw)
	}
	var body struct {
		Data struct {
			GraphAttempts []itemGraphAttempt `json:"graph_attempts"`
		} `json:"data"`
	}
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatal(err)
	}
	if len(body.Data.GraphAttempts) != 1 || len(body.Data.GraphAttempts[0].Nodes) != 1 {
		t.Fatalf("graph_attempts = %+v, want 1 attempt with 1 node", body.Data.GraphAttempts)
	}
	node := body.Data.GraphAttempts[0].Nodes[0]
	if node.Kind == "agent" || node.Kind != "command" {
		t.Fatalf("node.Kind = %q, want %q", node.Kind, "command")
	}
}
