package main

import (
	"context"
	"database/sql"
	"strings"
	"testing"
)

func TestBlockedItemProjectionNamesStoredWaiterAndReason(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, query := range []string{
		`ALTER TABLE items ADD COLUMN waiting_on TEXT NOT NULL DEFAULT ''`,
		`ALTER TABLE items ADD COLUMN underlying_state TEXT NOT NULL DEFAULT ''`,
		`UPDATE items SET state = 'blocked', waiting_on = '{"waiter":"owner","reason":"choose the contract"}', underlying_state = 'in-queue' WHERE item_id = 'item-seed-02'`,
	} {
		if _, err := db.Exec(query); err != nil {
			t.Fatal(err)
		}
	}
	s, err := openStore(context.Background(), h.store, 0)
	if err != nil {
		t.Fatal(err)
	}
	var blocked storeItem
	for _, it := range s.domain.items {
		if it.ItemID == "item-seed-02" {
			blocked = it
		}
	}
	p := ProjectItemState(blocked, nil, nil, []string{"old-edge"}, "")
	if p.Underlying != "owner: choose the contract" || len(p.WaitingOn) != 1 || p.WaitingOn[0] != "owner" {
		t.Fatalf("projection: %+v", p)
	}
	rows, err := readWaiting(context.Background(), h.store, nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.ItemID == blocked.ItemID {
			if !strings.HasPrefix(row.Mover, "operator;") || row.Why != "choose the contract" {
				t.Fatalf("waiting: %+v", row)
			}
			return
		}
	}
	t.Fatal("blocked row missing")
}

func TestBlockedItemLegacyWaiterIsExplicitAndReadOnly(t *testing.T) {
	h := newServeHarness(t)
	db, err := sql.Open("sqlite", h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`UPDATE items SET state = 'blocked' WHERE item_id = 'item-seed-02'`); err != nil {
		t.Fatal(err)
	}
	s, err := openStore(context.Background(), h.store, 0)
	if err != nil {
		t.Fatal(err)
	}
	for _, it := range s.domain.items {
		if it.ItemID == "item-seed-02" {
			p := ProjectItemState(it, nil, nil, nil, "")
			if !strings.Contains(p.Underlying, "waiter not recorded") {
				t.Fatalf("legacy: %+v", p)
			}
		}
	}
	var n int
	if err := db.QueryRow(`SELECT count(*) FROM pragma_table_info('items') WHERE name = 'waiting_on'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 0 {
		t.Fatal("reader migrated the legacy store")
	}
}
