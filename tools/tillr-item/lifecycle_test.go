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

func lifecycle(t *testing.T, db, verb, reason string) (int, []byte) {
	t.Helper()
	return run(t, verb, "--store", db, "--item", "item_042", "--reason", reason, "--by", "owner", "--now", "2026-11-16T12:00:00Z")
}

func lifecyclePayload(t *testing.T, raw []byte) map[string]any {
	t.Helper()
	var env struct {
		Payload map[string]any `json:"payload"`
	}
	if err := json.Unmarshal(raw, &env); err != nil {
		t.Fatal(err)
	}
	return env.Payload
}

func TestCloseAndReopenRestoreEveryStartingState(t *testing.T) {
	for _, initial := range []string{"not-started", "in-queue", "blocked"} {
		t.Run(initial, func(t *testing.T) {
			db := filepath.Join(scratch(t, "lifecycle"), "state.db")
			seeded := base("item_042", "an item")
			if initial == "in-queue" {
				seeded.State = initial
			}
			if code, out := apply(t, db, envelope(t, "item.record", "rec-1", seeded)); code != 0 {
				t.Fatalf("apply: %d %s", code, out)
			}
			if initial == "blocked" {
				if code, out := state(t, db, "item_042", initial, "2026-11-16T11:00:00Z"); code != 0 {
					t.Fatalf("block: %d %s", code, out)
				}
			}
			code, out := lifecycle(t, db, "close", "not wanted")
			if code != 0 {
				t.Fatalf("close: %d %s", code, out)
			}
			p := lifecyclePayload(t, out)
			for field, want := range map[string]string{"state": "closed", "close_reason": "not wanted", "closed_at": "2026-11-16T12:00:00Z", "closed_by": "owner", "closed_from": initial} {
				if p[field] != want {
					t.Fatalf("%s = %v, want %s", field, p[field], want)
				}
			}
			for _, target := range []string{"closed", "in-queue"} {
				code, out := state(t, db, "item_042", target, "2026-11-16T12:01:00Z")
				if code != 1 || !strings.Contains(string(out), "tillr item reopen") {
					t.Fatalf("state escape: %d %s", code, out)
				}
			}
			edited := seeded
			edited.State = "in-queue"
			if code, out := apply(t, db, envelope(t, "item.record", "rec-2", edited)); code != 1 || decode(t, out).Payload.Rule != "closed-use-reopen" {
				t.Fatalf("apply escape: %d %s", code, out)
			}
			if code, out := lifecycle(t, db, "reopen", " "); code != 1 || decode(t, out).Payload.Rule != "reopen-reason-missing" {
				t.Fatalf("missing reopen reason: %d %s", code, out)
			}
			code, out = lifecycle(t, db, "reopen", "wanted again")
			if code != 0 {
				t.Fatalf("reopen: %d %s", code, out)
			}
			p = lifecyclePayload(t, out)
			if p["state"] != initial {
				t.Fatalf("restored %v, want %s", p["state"], initial)
			}
			if initial == "blocked" {
				wait, ok := p["waiting_on"].(map[string]any)
				if !ok || wait["waiter"] != "owner" || wait["reason"] != "needs a decision" || p["underlying_state"] != "not-started" {
					t.Fatalf("blocked metadata lost: %v", p)
				}
			}
			code, out = run(t, "show", "--store", db, "--item", "item_042")
			if code != 0 || lifecyclePayload(t, out)["state"] != initial {
				t.Fatalf("show: %d %s", code, out)
			}
		})
	}
}

func TestLegacyCloseMetadataReadableBeforeMigration(t *testing.T) {
	db := filepath.Join(scratch(t, "legacy-close"), "state.db")
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", base("item_042", "old item"))); code != 0 {
		t.Fatalf("apply: %d %s", code, out)
	}
	raw, err := sql.Open("sqlite", db)
	if err != nil {
		t.Fatal(err)
	}
	for _, column := range []string{"close_reason", "closed_at", "closed_by", "closed_from"} {
		if _, err := raw.Exec(`ALTER TABLE items DROP COLUMN ` + column); err != nil {
			t.Fatal(err)
		}
	}
	raw.Close()
	if code, out := run(t, "show", "--store", db, "--item", "item_042"); code != 0 {
		t.Fatalf("legacy show: %d %s", code, out)
	}
	if code, out := lifecycle(t, db, "close", "not wanted"); code != 0 {
		t.Fatalf("migrating close: %d %s", code, out)
	}
	if code, out := run(t, "show", "--store", db, "--item", "item_042"); code != 0 || lifecyclePayload(t, out)["closed_from"] != "not-started" {
		t.Fatalf("metadata after migration: %d %s", code, out)
	}
}

func TestCloseRefusals(t *testing.T) {
	db := filepath.Join(scratch(t, "close-refusals"), "state.db")
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", base("item_042", "an item"))); code != 0 {
		t.Fatalf("apply: %d %s", code, out)
	}
	if code, out := state(t, db, "item_042", "closed", "2026-11-16T12:00:00Z"); code != 1 || !strings.Contains(string(out), "tillr item close") {
		t.Fatalf("raw close: %d %s", code, out)
	}
	for _, reason := range []string{"", " \n "} {
		if code, out := lifecycle(t, db, "close", reason); code != 1 || decode(t, out).Payload.Rule != "close-reason-missing" {
			t.Fatalf("missing reason: %d %s", code, out)
		}
	}
	if code, out := lifecycle(t, db, "reopen", "wanted"); code != 1 || decode(t, out).Payload.Rule != "item-not-closed" {
		t.Fatalf("not closed: %d %s", code, out)
	}
}

func TestCloseAfterDesignLanding(t *testing.T) {
	db := filepath.Join(scratch(t, "close-after-design"), "state.db")
	seeded := base("item_042", "an item")
	seeded.State = "in-queue"
	if code, out := apply(t, db, envelope(t, "item.record", "rec-1", seeded)); code != 0 {
		t.Fatalf("apply: %d %s", code, out)
	}
	ctx := context.Background()
	s, err := store.Open(ctx, db, false)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE landings (item_id TEXT, result TEXT, run_id TEXT, trunk_after TEXT)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO landings VALUES ('item_042', 'landed', 'run-design', 'design-commit')`)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	if code, out := lifecycle(t, db, "close", "not wanted"); code != 0 || lifecyclePayload(t, out)["closed_from"] != "in-queue" {
		t.Fatalf("close after design landing: %d %s", code, out)
	}
	if got := stateRow(t, db, "item_042"); got != "closed" {
		t.Fatalf("expected closed, got %s", got)
	}
}
