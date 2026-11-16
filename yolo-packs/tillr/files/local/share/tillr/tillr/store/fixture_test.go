package store_test

import (
	"context"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestAFixtureStoreIsMarkedAndALiveStoreIsNot(t *testing.T) {
	dir := t.TempDir()
	fixture := filepath.Join(dir, "fixture.sqlite3")
	s, err := store.InitFixture(context.Background(), fixture)
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	if !s.IsFixture() {
		t.Fatal("a store created as a fixture does not report itself as one")
	}

	live := filepath.Join(dir, "live.sqlite3")
	l, err := store.Open(context.Background(), live, true)
	if err != nil {
		t.Fatalf("opening a store failed: %v", err)
	}
	defer l.Close()
	if l.IsFixture() {
		t.Fatal("a store created by Open reports itself as a fixture")
	}
}

// Seeding a live store is the one mistake that cannot be undone, so it is refused
// by the store rather than by every caller remembering to check.
func TestSeedingALiveStoreIsRefused(t *testing.T) {
	dir := t.TempDir()
	live := filepath.Join(dir, "live.sqlite3")
	s, err := store.Open(context.Background(), live, true)
	if err != nil {
		t.Fatalf("opening a store failed: %v", err)
	}
	defer s.Close()
	err = s.Seed(context.Background(), []byte(`{"kind":"fixture.seed","payload":{"records":[]}}`))
	if err == nil {
		t.Fatal("seeding a live store succeeded")
	}
}

// A read-only open must not migrate: the whole point of the read-only face is that
// it is safe to point at a store a writer is using. The store is left at version 0
// first, because a migrating open of an already-current store would leave the
// version unchanged and the test could not fail for the reason it states.
func TestAReadOnlyOpenDoesNotMigrate(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "fixture.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	if err := s.ForgetMigrations(); err != nil {
		t.Fatalf("resetting the migration rows failed: %v", err)
	}
	s.Close()

	r, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatalf("read-only open failed: %v", err)
	}
	defer r.Close()
	after, err := r.SchemaVersion()
	if err != nil {
		t.Fatalf("reading the schema version failed: %v", err)
	}
	if after != 0 {
		t.Fatalf("a read-only open migrated the store to version %d", after)
	}
}

// Seeding a fixture is the only path that appends records, so it is where the
// append primitive is exercised. A seed with no records never runs the loop, which
// is why the earlier suite left Append with no coverage at all.
func TestSeedingAFixtureAppendsItsRecords(t *testing.T) {
	dir := t.TempDir()
	s, err := store.InitFixture(context.Background(), filepath.Join(dir, "fixture.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	bundle := []byte(`{"kind":"fixture.seed","payload":{"records":[
		{"record_version":1,"kind":"fixture.seed","id":"seed-1",
		 "producer":{"system":"tillr","utility":"store","version":"0.1.0"},
		 "created_at":"2026-11-06T00:00:00Z","payload":{"records":0}}]}}`)
	if err := s.Seed(context.Background(), bundle); err != nil {
		t.Fatalf("seeding a fixture with one record failed: %v", err)
	}
}

// A seed record of a kind nothing registered is refused, because an unregistered
// kind is a kind the conformance suite cannot check.
func TestSeedingAnUnregisteredKindIsRefused(t *testing.T) {
	dir := t.TempDir()
	s, err := store.InitFixture(context.Background(), filepath.Join(dir, "fixture.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	bundle := []byte(`{"kind":"fixture.seed","payload":{"records":[
		{"record_version":1,"kind":"gate.verdict","id":"v-1",
		 "producer":{"system":"tillr","utility":"gate","version":"0.1.0"},
		 "created_at":"2026-11-06T00:00:00Z","payload":{}}]}}`)
	if err := s.Seed(context.Background(), bundle); err == nil {
		t.Fatal("seeding a record of an unregistered kind succeeded")
	}
}

// §5.1: seeding a live store must be impossible, not merely unlikely. InitFixture is
// the second command that could defeat it -- it stamps the fixture mark onto whatever
// store it is pointed at -- so it refuses a store that already exists and is not
// already a fixture.
func TestInitFixtureRefusesALiveStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	live, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatalf("creating a live store failed: %v", err)
	}
	if err := live.Close(); err != nil {
		t.Fatalf("closing the live store failed: %v", err)
	}
	if _, err := store.InitFixture(context.Background(), path); !errors.Is(err, store.ErrLiveStore) {
		t.Fatalf("InitFixture on a live store answered %v, want ErrLiveStore", err)
	}
}

// The refusal is about a store that is not a fixture, not about the path existing: a
// fixture re-initialized is the same fixture, and refusing it would make the face
// unusable for the one caller that legitimately repeats it.
func TestInitFixtureAcceptsAnExistingFixture(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	first, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	if err := first.Close(); err != nil {
		t.Fatalf("closing the fixture store failed: %v", err)
	}
	second, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("re-initializing a fixture store failed: %v", err)
	}
	defer second.Close()
	if !second.IsFixture() {
		t.Fatal("a re-initialized fixture store is not a fixture")
	}
}
