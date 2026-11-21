package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestWaitingBackendClosedNamesBackendAndReopeningMover(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", "file:"+h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, q := range []string{
		`ALTER TABLE dispatches ADD COLUMN deferred_by TEXT`,
		`DELETE FROM dispatches`,
		`INSERT INTO items(item_id,title,state,updated_at) VALUES ('item-backend','backend wait','in-queue',1000)`,
		`INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at,deferred_by) VALUES ('closed-backend','item-backend','hold','backend-closed',1000,'fixture')`,
	} {
		if _, err := db.Exec(q); err != nil {
			t.Fatal(err)
		}
	}
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ItemID == "item-backend" {
			if row.Class != "alarm" || row.Where != "queue" || !strings.Contains(row.Mover, "fixture") || !strings.Contains(row.Mover, "probe-backend") || !strings.Contains(row.Mover, "non-infrastructure run") {
				t.Fatalf("hold lacks mover: %+v", row)
			}
			return
		}
	}
	t.Fatal("missing held item")
}
