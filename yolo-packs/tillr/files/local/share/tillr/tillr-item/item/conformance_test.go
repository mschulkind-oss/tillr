package item_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// scratch answers a directory under $YOLO_DURABLE_DIR, never /tmp: a suite that writes
// to /tmp loses its evidence when the box is cleaned, and the owner refuses it.
func scratch(t *testing.T) string {
	t.Helper()
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("cannot create the scratch root: %v", err)
	}
	dir, err := os.MkdirTemp(root, "item-conformance-")
	if err != nil {
		t.Fatalf("cannot create the scratch directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// The item package's conformance case: the two things a second consumer of this
// package would break by linking it — the item kinds registered with their owner, and
// the item tables present in the composed migration list.
//
// It lives in the package's own external test package, so it proves the contract is
// reachable from outside package main: a main package cannot be imported, and a
// registration only main could reach would be invisible here. The negative control is
// the registration itself — remove the store.RegisterDDL call from schema.go's init and
// the table assertions below fail on a store that has no item tables.
func TestTheItemPackageRegistersItsOwnContract(t *testing.T) {
	for _, kind := range []string{item.AddKind, item.UpdateKind} {
		registered, err := event.Lookup(kind)
		if err != nil {
			t.Fatalf("the item kind %q is not registered: %v", kind, err)
		}
		if registered.Owner != "item" {
			t.Fatalf("%s is owned by %q, want item", kind, registered.Owner)
		}
	}
	s, err := store.InitFixture(context.Background(), filepath.Join(scratch(t), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	counts, err := s.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	for _, table := range []string{"items", "item_criteria", "item_edges", "item_requests"} {
		if _, ok := counts[table]; !ok {
			t.Fatalf("the item table %q is not in the composed migration list: %v", table, counts)
		}
	}
}

// The item package's materializer is registered, so the substrate's seed path writes
// item rows through item's own record→row mapping rather than appending an event and
// nothing else. That is the claim DRAIN-1 depends on: a drain process that links this
// package can build an item fixture without going through the item binary.
//
// The negative control is the registration: without the store.RegisterMaterializer call
// in schema.go's init, Seed has no mapping for item.record and fails — the record kind
// is not an event kind either, so the seed cannot fall back to appending it.
func TestTheItemPackageRegistersItsMaterializer(t *testing.T) {
	s, err := store.InitFixture(context.Background(), filepath.Join(scratch(t), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	if err := s.Seed(context.Background(), seedBundle(t, itemRecord(t, "item_042", "seeded"))); err != nil {
		t.Fatalf("seeding an item.record failed: %v", err)
	}

	var stored record.Envelope
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		stored, err = item.Show(r, "item_042", ".")
		return err
	})
	if err != nil {
		t.Fatalf("reading the seeded item back failed: %v", err)
	}
	var payload struct {
		ItemID string `json:"item_id"`
		Title  string `json:"title"`
	}
	if err := json.Unmarshal(stored.Payload, &payload); err != nil {
		t.Fatalf("reading the stored payload failed: %v", err)
	}
	if payload.ItemID != "item_042" || payload.Title != "seeded" {
		t.Fatalf("the seed materialized %q / %q, want item_042 / seeded", payload.ItemID, payload.Title)
	}
}

// itemRecord builds one item.record envelope for the seed path.
func itemRecord(t *testing.T, id, title string) []byte {
	t.Helper()
	body, err := json.Marshal(map[string]any{
		"item_id": id,
		"title":   title,
		"state":   "not-started",
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "the first criterion", "state": "open"},
		},
		"blocked_by": []string{},
	})
	if err != nil {
		t.Fatalf("encoding the item payload failed: %v", err)
	}
	raw, err := record.Encode(record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          item.RecordKind,
		ID:            "rec-" + id,
		Producer:      record.Producer{System: record.System, Utility: item.Utility, Version: item.Version},
		CreatedAt:     time.Date(2026, 11, 10, 0, 0, 0, 0, time.UTC),
		Payload:       body,
	})
	if err != nil {
		t.Fatalf("encoding the item record failed: %v", err)
	}
	return raw
}

// seedBundle wraps records in the fixture.seed bundle store.Seed reads.
func seedBundle(t *testing.T, records ...[]byte) []byte {
	t.Helper()
	raw := make([]json.RawMessage, len(records))
	for i, r := range records {
		raw[i] = r
	}
	bundle, err := json.Marshal(map[string]any{
		"kind":    "fixture.seed",
		"payload": map[string]any{"records": raw},
	})
	if err != nil {
		t.Fatalf("encoding the seed bundle failed: %v", err)
	}
	return bundle
}

// The delegated sign-off grant and the verdict are this package's newest contract pieces:
// two kinds registered with their owner, the grant table present in the composed
// migration list, and the criterion-hash spelling a verdict is bound to.
//
// The negative control is the registration and the digest: drop either kind from
// schema.go's registration loop and the lookups below fail, drop the item_grants DDL and
// the table assertion fails on a store that has no such table, and change
// CriterionHash's spelling and the known digest of "the first criterion" stops matching.
func TestTheItemPackageRegistersTheGrantAndVerdictContract(t *testing.T) {
	for _, kind := range []string{item.VerdictRecordKind, item.GrantRecordKind} {
		registered, err := event.Lookup(kind)
		if err != nil {
			t.Fatalf("the item kind %q is not registered: %v", kind, err)
		}
		if registered.Owner != "item" {
			t.Fatalf("%s is owned by %q, want item", kind, registered.Owner)
		}
	}
	s, err := store.InitFixture(context.Background(), filepath.Join(scratch(t), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	counts, err := s.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	if _, ok := counts["item_grants"]; !ok {
		t.Fatalf("the item_grants table is not in the composed migration list: %v", counts)
	}
	if got := item.CriterionHash("the first criterion"); got != "00cf50717100b32b0e92e665ccbaeb641d0b4e9eec4b4973b01f0430806fa52d" {
		t.Fatalf("the criterion hash of \"the first criterion\" is %s, want its SHA-256 in lowercase hex", got)
	}
}
