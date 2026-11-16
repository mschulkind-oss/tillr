package item_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// seatRecord builds one item.record for the write face, carrying the seat the caller names.
func seatRecord(t *testing.T, id, seat string) record.Envelope {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"item_id": id,
		"title":   "the item " + id,
		"state":   "in-queue",
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "a criterion", "state": ""},
		},
		"blocked_by": []string{},
		"seat":       seat,
	})
	if err != nil {
		t.Fatalf("encoding the item payload failed: %v", err)
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          item.RecordKind,
		ID:            "rec-" + id,
		Producer:      record.Producer{System: record.System, Utility: item.Utility, Version: item.Version},
		CreatedAt:     time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC),
		Payload:       body,
	}
}

// applyRecord writes one record through the item write face.
func applyRecord(t *testing.T, s *store.Store, env record.Envelope) (record.Envelope, error) {
	t.Helper()
	var stored record.Envelope
	err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		var err error
		stored, err = item.Apply(context.Background(), tx, env)
		return err
	})
	return stored, err
}

// showSeat answers the seat the store holds for one item, read through the item read face
// so the column and the record's field are proven together.
func showSeat(t *testing.T, s *store.Store, id string) string {
	t.Helper()
	var stored record.Envelope
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		stored, err = item.Show(r, id, ".")
		return err
	}); err != nil {
		t.Fatalf("reading %s back failed: %v", id, err)
	}
	var payload struct {
		Seat string `json:"seat"`
	}
	if err := json.Unmarshal(stored.Payload, &payload); err != nil {
		t.Fatalf("reading the stored payload failed: %v", err)
	}
	return payload.Seat
}

// An item names a seat and stores it; an item that names none is not an error, it asks for
// nothing and the planner uses the tick's default backend.
func TestAnItemStoresTheSeatItAsksFor(t *testing.T) {
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(scratch(t), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()

	for _, seat := range []string{"", item.SeatDesign, item.SeatBuild} {
		id := "item_" + seat
		if seat == "" {
			id = "item_unseated"
		}
		if _, err := applyRecord(t, s, seatRecord(t, id, seat)); err != nil {
			t.Fatalf("applying an item with seat %q failed: %v", seat, err)
		}
		if got := showSeat(t, s, id); got != seat {
			t.Fatalf("the stored item's seat is %q, want %q", got, seat)
		}
	}
}

// A seat outside the item model's vocabulary is refused at the write face, naming the seat
// and the vocabulary: a stored value that means nothing is a typo, not a role.
func TestAnItemRefusesASeatThatDoesNotExist(t *testing.T) {
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(scratch(t), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()

	_, err = applyRecord(t, s, seatRecord(t, "item_042", "qa"))
	if err == nil {
		t.Fatalf("an item asking for seat \"qa\" was accepted")
	}
	if code := item.ExitCode(err); code != 2 {
		t.Fatalf("the refusal exits %d, want 2", code)
	}
	for _, want := range []string{`"qa"`, "design, build, review, scout"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal %q does not name %q", err, want)
		}
	}
}

// The seed loader accepts a seat and stores it, so a seed document can state an item's role
// as part of its definition. The entry the store already holds is compared on it too, which
// the second half pins: changing the seat in a re-seed is a definition conflict, not a
// silent rewrite.
func TestTheSeedCarriesTheSeat(t *testing.T) {
	ctx := context.Background()
	s, err := store.InitFixture(ctx, filepath.Join(scratch(t), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()

	document := func(seat string) []byte {
		raw, err := json.Marshal(map[string]any{
			"kind":    "tillr.seed",
			"version": "0.1.0",
			"items": []map[string]any{{
				"item_id": "item_042",
				"title":   "the item item_042",
				"state":   "in-queue",
				"criteria": []map[string]string{
					{"criterion_id": "c1", "text": "a criterion", "state": ""},
				},
				"blocked_by":    []string{},
				"allowed_paths": []string{"items/item_042/"},
				"seat":          seat,
			}},
		})
		if err != nil {
			t.Fatalf("building the seed document failed: %v", err)
		}
		return raw
	}

	at := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.Seed(ctx, tx, "seed.json", document(item.SeatDesign), at)
		return err
	}); err != nil {
		t.Fatalf("seeding an item with the design seat failed: %v", err)
	}
	if got := showSeat(t, s, "item_042"); got != item.SeatDesign {
		t.Fatalf("the seeded item's seat is %q, want %q", got, item.SeatDesign)
	}

	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := item.Seed(ctx, tx, "seed.json", document(item.SeatBuild), at)
		return err
	})
	if err == nil || !strings.Contains(err.Error(), `seat is "design" in the store and "build" in the seed`) {
		t.Fatalf("re-seeding a different seat answered %v, want a definition conflict naming the seat", err)
	}
}

// items.seat is a column added to a table that already existed, so it lands through the
// store's additive-column hook exactly as items.rank did: CREATE TABLE IF NOT EXISTS is a
// no-op against a store that already has the table, and a store built before the seat would
// otherwise fail the first read that names the column. The case takes a store back to that
// shape and opens it with the current code.
//
// Negative control: the same store before the open, whose items table has no seat column —
// that is what makes the assertion about the repair rather than about a store this test
// created with the column already in it.
func TestAStoreCreatedBeforeTheSeatColumnGainsItOnFirstOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(scratch(t), "board.sqlite3")
	created, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatalf("creating the store failed: %v", err)
	}
	if err := created.Close(); err != nil {
		t.Fatalf("closing the store failed: %v", err)
	}

	dropColumn(t, path, "seat")

	repaired, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatalf("opening the store created before the seat failed: %v", err)
	}
	defer repaired.Close()
	if got := columnDeclaration(t, repaired, "items", "seat"); got != "TEXT|notnull|''" {
		t.Fatalf("the repaired store's items.seat is %q, want the declaration ItemDDL states", got)
	}
}

// dropColumn removes one column from items directly, because the code under test migrates
// forward only: a helper that could not build the old shape could not test the repair.
func dropColumn(t *testing.T, path, column string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening %s to drop items.%s failed: %v", path, column, err)
	}
	defer db.Close()
	if _, err := db.Exec(`ALTER TABLE items DROP COLUMN ` + column); err != nil {
		t.Fatalf("dropping items.%s failed: %v", column, err)
	}
}
