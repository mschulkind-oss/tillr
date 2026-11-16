package main_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func blockState(t *testing.T, db, waiter, reason string) (int, []byte) {
	t.Helper()
	return run(t, "state", "--store", db, "--item", "item_042", "--state", "blocked", "--waiter", waiter, "--reason", reason, "--now", "2026-11-16T00:00:00Z")
}

func blockedPayload(t *testing.T, db string) map[string]any {
	t.Helper()
	code, out := run(t, "show", "--store", db, "--item", "item_042")
	if code != 0 {
		t.Fatalf("show: %d: %s", code, out)
	}
	var env struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(out, &env); err != nil {
		t.Fatal(err)
	}
	return env.Payload
}

func TestBlockedStateRequiresWaiterAndReason(t *testing.T) {
	db := filepath.Join(scratch(t, "blocked-required"), "board.sqlite3")
	if code, out := apply(t, db, envelope(t, "item.record", "rec", base("item_042", "work"))); code != 0 {
		t.Fatalf("apply: %s", out)
	}
	for _, args := range [][]string{nil, {"--waiter", "owner"}, {"--reason", "decision"}, {"--waiter", " ", "--reason", "decision"}, {"--waiter", "owner", "--reason", " "}} {
		code, out := run(t, append([]string{"state", "--store", db, "--item", "item_042", "--state", "blocked", "--now", "2026-11-16T00:00:00Z"}, args...)...)
		if code != 1 || decode(t, out).Payload.Rule != "blocked-waiter-missing" {
			t.Fatalf("missing metadata %v: %d: %s", args, code, out)
		}
	}
	if got := stateRow(t, db, "item_042"); got != "not-started" {
		t.Fatalf("refusal changed state: %s", got)
	}
}

func TestBlockedStateWaiterVocabulary(t *testing.T) {
	for _, waiter := range []string{"owner", "design", "plan", "item:item_043", "build", "item:absent", "item:"} {
		t.Run(waiter, func(t *testing.T) {
			db := filepath.Join(scratch(t, "blocked-waiter"), "board.sqlite3")
			for _, id := range []string{"item_042", "item_043"} {
				if code, out := apply(t, db, envelope(t, "item.record", id, base(id, "work"))); code != 0 {
					t.Fatalf("apply: %s", out)
				}
			}
			code, out := blockState(t, db, waiter, "needs a decision")
			valid := waiter == "owner" || waiter == "design" || waiter == "plan" || waiter == "item:item_043"
			if valid && code != 0 || !valid && code != 1 {
				t.Fatalf("waiter %s: %d: %s", waiter, code, out)
			}
		})
	}
}

func TestBlockedStateTransitionsAndClearing(t *testing.T) {
	for _, from := range []string{"not-started", "in-queue", "blocked", "in-progress", "landed", "resolved-elsewhere", "closed", "abandoned", "deleted"} {
		for _, to := range []string{"not-started", "in-queue"} {
			t.Run(from+"-"+to, func(t *testing.T) {
				db := filepath.Join(scratch(t, "blocked-transitions"), "board.sqlite3")
				s, err := store.Open(context.Background(), db, true)
				if err != nil {
					t.Fatal(err)
				}
				err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
					_, err := tx.Exec(`INSERT INTO items(item_id,title,state,updated_at) VALUES ('item_042','work',?,0)`, from)
					return err
				})
				s.Close()
				if err != nil {
					t.Fatal(err)
				}
				code, out := blockState(t, db, "design", "first reason")
				if from != "not-started" && from != "in-queue" && from != "blocked" {
					if code != 1 {
						t.Fatalf("illegal move from %s: %d: %s", from, code, out)
					}
					return
				}
				if code != 0 {
					t.Fatalf("block: %d: %s", code, out)
				}
				p := blockedPayload(t, db)
				if p["underlying_state"] != from {
					t.Fatalf("underlying state: %+v", p)
				}
				n := eventsOfKind(t, db, "item.update")
				if code, out := blockState(t, db, "design", "first reason"); code != 0 {
					t.Fatalf("repeat: %s", out)
				}
				if got := eventsOfKind(t, db, "item.update"); got != n {
					t.Fatalf("repeat appended event")
				}
				if code, out := blockState(t, db, "owner", "changed reason"); code != 0 {
					t.Fatalf("reblock: %s", out)
				}
				p = blockedPayload(t, db)
				waiting := p["waiting_on"].(map[string]any)
				if waiting["waiter"] != "owner" || waiting["reason"] != "changed reason" || p["underlying_state"] != from {
					t.Fatalf("reblock: %+v", p)
				}
				if code, out := state(t, db, "item_042", to, "2026-11-16T00:01:00Z"); code != 0 {
					t.Fatalf("unblock: %s", out)
				}
				p = blockedPayload(t, db)
				if p["waiting_on"] != nil || p["underlying_state"] != nil {
					t.Fatalf("metadata not cleared: %+v", p)
				}
			})
		}
	}
}

func TestBlockedStateLegacyStoreReadableBeforeAndAfterMigration(t *testing.T) {
	db := filepath.Join(scratch(t, "blocked-legacy"), "board.sqlite3")
	seeded := base("item_042", "old blocked item")
	seeded.State = "blocked"
	if code, out := apply(t, db, envelope(t, "item.record", "rec", seeded)); code != 0 {
		t.Fatalf("apply: %s", out)
	}
	raw, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	for _, col := range []string{"waiting_on", "underlying_state"} {
		var count int
		if err := raw.QueryRow(`SELECT count(*) FROM pragma_table_info('items') WHERE name = ?`, col).Scan(&count); err != nil {
			t.Fatal(err)
		}
		if count > 0 {
			if _, err := raw.Exec(`ALTER TABLE items DROP COLUMN ` + col); err != nil {
				t.Fatal(err)
			}
		}
	}
	raw.Close()
	for i := 0; i < 2; i++ {
		p := blockedPayload(t, db)
		b, _ := json.Marshal(p)
		if !strings.Contains(string(b), "waiter not recorded") {
			t.Fatalf("legacy absence hidden: %s", b)
		}
		code, out := run(t, "state", "--store", db, "--item", "item_042", "--state", "blocked", "--now", "2026-11-16T00:00:00Z")
		if code != 1 || decode(t, out).Payload.Rule != "blocked-waiter-missing" {
			t.Fatalf("legacy new move: %d: %s", code, out)
		}
	}
}
