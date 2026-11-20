package main

import "testing"

func TestRunSeatWorkflowNodeHTTPAPI(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `
		ALTER TABLE runs ADD COLUMN seat TEXT;
		ALTER TABLE runs ADD COLUMN workflow TEXT;
		ALTER TABLE runs ADD COLUMN node TEXT;
		ALTER TABLE dispatches ADD COLUMN seat TEXT;
		INSERT INTO runs(run_id,item_id,lane_id,backend,seat,workflow,node,state,exit_code,started_at,finished_at,event_seq)
		VALUES ('run-new','item-seed-02','lane-a','pi','edit','work','piece-view','exited',0,2000,3000,10),
		       ('run-old-dispatched','item-seed-02','lane-b','pi',NULL,NULL,NULL,'exited',0,1500,1800,11),
		       ('run-old-empty','item-seed-04','lane-c','pi',NULL,NULL,NULL,'exited',0,1200,1400,12);
		INSERT INTO dispatches(decision_id,item_id,lane_id,run_id,action,reason,dispatched_at,seat)
		VALUES ('dec-old','item-seed-02','lane-b','run-old-dispatched','dispatch','ready',1500,'design');
		INSERT INTO lanes(lane_id,state,run_id,leased_until)
		VALUES ('lane-live','leased','run-live',9999999999999);
		INSERT INTO events(ts,kind,payload)
		VALUES (4000,'run.allocate','{"run_id":"run-live","item_id":"item-seed-04","lane_id":"lane-live","backend":"pi","seat":"build","workflow":"work","node":"impl","started_at":"2026-11-20T00:00:00Z"}');
	`)

	// 1. /api/v1/snapshot
	status, snapBody, snapRaw := h.get("/api/v1/snapshot")
	if status != 200 {
		t.Fatalf("snapshot: %d %s", status, snapRaw)
	}
	snapData := snapBody["data"].(map[string]any)
	recentRuns := snapData["recent_runs"].([]any)
	byID := map[string]map[string]any{}
	for _, r := range recentRuns {
		m := r.(map[string]any)
		byID[m["run_id"].(string)] = m
	}
	if byID["run-new"]["seat"] != "edit" || byID["run-new"]["workflow"] != "work" || byID["run-new"]["node"] != "piece-view" {
		t.Fatalf("snapshot run-new: %+v", byID["run-new"])
	}
	if byID["run-old-dispatched"]["seat"] != "design" {
		t.Fatalf("snapshot run-old-dispatched should fall back to dispatches.seat: %+v", byID["run-old-dispatched"])
	}
	if _, ok := byID["run-old-empty"]["seat"]; ok {
		t.Fatalf("snapshot run-old-empty should omit seat: %+v", byID["run-old-empty"])
	}
	if byID["run-live"]["seat"] != "build" || byID["run-live"]["workflow"] != "work" || byID["run-live"]["node"] != "impl" {
		t.Fatalf("snapshot run-live: %+v", byID["run-live"])
	}

	// 2. /api/v1/items/:id
	status, itemBody, itemRaw := h.get("/api/v1/items/item-seed-02")
	if status != 200 {
		t.Fatalf("item: %d %s", status, itemRaw)
	}
	itemRuns := itemBody["data"].(map[string]any)["runs"].([]any)
	itemByID := map[string]map[string]any{}
	for _, r := range itemRuns {
		m := r.(map[string]any)
		itemByID[m["run_id"].(string)] = m
	}
	if itemByID["run-new"]["seat"] != "edit" || itemByID["run-new"]["workflow"] != "work" || itemByID["run-new"]["node"] != "piece-view" {
		t.Fatalf("item run-new: %+v", itemByID["run-new"])
	}
	if itemByID["run-old-dispatched"]["seat"] != "design" {
		t.Fatalf("item run-old-dispatched: %+v", itemByID["run-old-dispatched"])
	}
	graphAttempts := itemBody["data"].(map[string]any)["graph_attempts"].([]any)
	var sawEditGraphNode bool
	for _, ga := range graphAttempts {
		nodes := ga.(map[string]any)["nodes"].([]any)
		for _, n := range nodes {
			if n.(map[string]any)["seat"] == "edit" {
				sawEditGraphNode = true
			}
		}
	}
	if !sawEditGraphNode {
		t.Fatalf("item graph did not carry single-worker run seat: %+v", graphAttempts)
	}

	// 3. /api/v1/runs/:id (finished, fallback, live)
	status, runBody, runRaw := h.get("/api/v1/runs/run-new")
	if status != 200 {
		t.Fatalf("run-new: %d %s", status, runRaw)
	}
	rNew := runBody["data"].(map[string]any)["run"].(map[string]any)
	if rNew["seat"] != "edit" || rNew["workflow"] != "work" || rNew["node"] != "piece-view" {
		t.Fatalf("run detail run-new: %+v", rNew)
	}

	status, runOldBody, runOldRaw := h.get("/api/v1/runs/run-old-dispatched")
	if status != 200 {
		t.Fatalf("run-old-dispatched: %d %s", status, runOldRaw)
	}
	rOld := runOldBody["data"].(map[string]any)["run"].(map[string]any)
	if rOld["seat"] != "design" {
		t.Fatalf("run detail run-old-dispatched: %+v", rOld)
	}

	status, runLiveBody, runLiveRaw := h.get("/api/v1/runs/run-live")
	if status != 200 {
		t.Fatalf("run-live: %d %s", status, runLiveRaw)
	}
	rLive := runLiveBody["data"].(map[string]any)["run"].(map[string]any)
	if rLive["seat"] != "build" || rLive["workflow"] != "work" || rLive["node"] != "impl" {
		t.Fatalf("run detail run-live: %+v", rLive)
	}
}
