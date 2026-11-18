package main

import (
	"context"
	"testing"
)

func historyItem(t *testing.T, h *serveHarness, id string) waitingRow {
	t.Helper()
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ItemID == id {
			return row
		}
	}
	t.Fatalf("missing item %s: %+v", id, rows)
	return waitingRow{}
}

func TestWaitingHistoryNotYetPlanned(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DELETE FROM dispatches; INSERT INTO items(item_id,title,state,updated_at) VALUES ('fresh','fresh','in-queue',1)`)
	row := historyItem(t, h, "fresh")
	if row.Class != "waits on the loop" || row.Why != "not yet planned" || row.Mover != "drain; the next pass" {
		t.Fatalf("unplanned row: %+v", row)
	}
}

func TestWaitingHistoryMissingReason(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DELETE FROM dispatches; INSERT INTO items(item_id,title,state,updated_at) VALUES ('fresh','fresh','in-queue',1); INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES ('empty','fresh','hold','',1000)`)
	row := historyItem(t, h, "fresh")
	if row.Class != "needs a cause" || row.Why != "reason not recorded" || row.Mover != "operator; add a history rule for this recorded reason" {
		t.Fatalf("empty reason: %+v", row)
	}
}

func TestWaitingHistoryStoredWaitMissingReason(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`ALTER TABLE items ADD COLUMN waiting_on TEXT NOT NULL DEFAULT ''; UPDATE items SET state='blocked', waiting_on='{"waiter":"owner","reason":""}' WHERE item_id='item-seed-02'`)
	row := historyItem(t, h, "item-seed-02")
	if row.Class != "needs you" || row.Why != "waiting reason not recorded" || row.Mover != "operator; resolve the recorded hold; end cause not recorded" {
		t.Fatalf("empty stored reason: %+v", row)
	}
}

func TestWaitingHistoryStoppedRun(t *testing.T) {
	for _, tc := range []struct{ name, extra, class, why, mover string }{
		{"stopped", "", "waits on the loop", "run ended by a stop", "drain; stopped"},
		{"no decision", `DELETE FROM dispatches`, "waits on the loop", "run ended by a stop", "drain; stopped"},
		{"same millisecond dispatch", `UPDATE dispatches SET dispatched_at=2000`, "waits on the loop", "run ended by a stop", "drain; stopped"},
		{"same millisecond retry", `UPDATE dispatches SET dispatched_at=2000, action='retry'`, "waits on the loop", "run ended by a stop", "drain; stopped"},
		{"tied hold before launch ID", `UPDATE dispatches SET dispatched_at=2000, decision_id='z-dispatch'; INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES ('a-hold','fresh','hold','no-policy',2000)`, "needs you", "no-policy", "operator; end cause not recorded"},
		{"tied hold after launch ID", `UPDATE dispatches SET dispatched_at=2000, decision_id='a-dispatch'; INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES ('z-hold','fresh','hold','no-policy',2000)`, "needs you", "no-policy", "operator; end cause not recorded"},
		{"earlier hold", `INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES ('hold','fresh','hold','no-policy',1999)`, "waits on the loop", "run ended by a stop", "drain; stopped"},
		{"same millisecond hold", `INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES ('hold','fresh','hold','no-policy',2000)`, "needs you", "no-policy", "operator; end cause not recorded"},
		{"later hold", `INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES ('hold','fresh','hold','no-policy',2001)`, "needs you", "no-policy", "operator; end cause not recorded"},
		{"resumed at same time", `INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (2000,'run.resume','run','stopped','{}'); UPDATE runs SET state='running' WHERE run_id='stopped'`, "moving", "ready", "stopped"},
		{"resumed and finished", `INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (3000,'run.resume','run','stopped','{}')`, "ready", "ready", "the next pass"},
		{"stopped again", `INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (3000,'run.resume','run','stopped','{}'),(4000,'drain.orphaned','run','stopped','{}')`, "waits on the loop", "run ended by a stop", "drain; stopped"},
		{"new run", `INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES ('new','fresh','lane-b','pi','running',3000,0,'[]',30)`, "moving", "ready", "new"},
		{"stale running state", `UPDATE runs SET state='running' WHERE run_id='stopped'`, "waits on the loop", "run ended by a stop", "drain; stopped"},
		{"stale running state with tied hold", `UPDATE runs SET state='running' WHERE run_id='stopped'; INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES ('hold','fresh','hold','no-policy',2000)`, "needs you", "no-policy", "operator; end cause not recorded"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			h := newServeHarness(t)
			h.exec(`DELETE FROM dispatches; INSERT INTO items(item_id,title,state,updated_at) VALUES ('fresh','fresh','in-queue',1);
INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,artifact_refs,event_seq) VALUES ('stopped','fresh','lane-b','pi','finished',1000,2000,'[]',20);
INSERT INTO dispatches(decision_id,item_id,run_id,action,reason,dispatched_at) VALUES ('dispatch','fresh','stopped','dispatch','ready',1000);
INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES (2000,'drain.orphaned','run','stopped','{}')`)
			if tc.extra != "" {
				h.exec(tc.extra)
			}
			row := historyItem(t, h, "fresh")
			if row.Class != tc.class || row.Why != tc.why || row.Mover != tc.mover {
				t.Fatalf("row: %+v; want %s / %s / %s", row, tc.class, tc.why, tc.mover)
			}
			if row.Why == "run ended by a stop" && row.Since == "no pass yet" {
				t.Fatalf("stop has no timestamp: %+v", row)
			}
		})
	}
}
