package main

import (
	"context"
	"strings"
	"testing"
)

func TestWaitingHeldQueueNamesFixerCheckAndOwner(t *testing.T) {
	h := newServeHarness(t)
	h.exec(`DELETE FROM dispatches`)
	h.exec(`ALTER TABLE dispatches ADD COLUMN deferred_by TEXT NOT NULL DEFAULT ''`)
	h.exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES('fixer-main-red','fix trunk','blocked',1000)`)
	h.exec(`CREATE TABLE IF NOT EXISTS item_criteria(item_id TEXT,criterion_id TEXT,text TEXT,state TEXT)`)
	h.exec(`INSERT INTO item_criteria VALUES('fixer-main-red','trunk-green','the full gate is green (red: unit)', 'not-done')`)
	h.exec(`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at,deferred_by) VALUES('red','item-seed-02','hold','trunk-broken',1000,'fixer-main-red')`)
	for _, owner := range []bool{false, true} {
		if owner {
			h.exec(`INSERT INTO events(ts,kind,object_type,object_id,payload) VALUES(1000,'item.conflict','item','fixer-main-red','{"conflict_id":"repair","item_id":"fixer-main-red","run_id":"fixer-run","text":"needs tests/outside_test.go outside fence","status":"owner","waiting_on":"owner"}')`)
		}
		rows, err := readWaiting(context.Background(), h.store, nil)
		if err != nil {
			t.Fatal(err)
		}
		var row waitingRow
		for _, candidate := range rows {
			if candidate.ItemID == "item-seed-02" {
				row = candidate
			}
		}
		if !strings.Contains(row.Why, "fixer-main-red") || !strings.Contains(row.Why, "check: unit") {
			t.Fatalf("missing repair cause: %+v", row)
		}
		if owner {
			if row.Class != "needs you" || !strings.Contains(row.Mover, "waiting on owner: needs tests/outside_test.go") {
				t.Fatalf("owner wait absent: %+v", row)
			}
		} else if !strings.Contains(row.Mover, "not waiting on owner") {
			t.Fatalf("repair wait absent: %+v", row)
		}
	}
}
