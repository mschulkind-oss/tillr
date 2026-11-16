package conformance_test

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The conformance suite is the empirical content of the claim that per-module
// selection is safe, so it checks the four contract pieces exist and agree on a
// version. A piece that is missing is a boundary nothing is checking.
func TestTheFourContractPiecesArePresent(t *testing.T) {
	if record.System != "tillr" {
		t.Fatalf("the record system name is %q", record.System)
	}
	if record.SupportedVersion != 1 {
		t.Fatalf("the supported record version is %d", record.SupportedVersion)
	}
	// The number is pinned so a schema change is announced rather than absorbed: it is
	// 5 because the item record gained rank — the queue's order, which the drain's planner
	// sorts by — and a face that cannot migrate decides compatibility from this number
	// alone. 4 was the run record's spend (usage_tokens and usage_cost_usd, the numbers the
	// drain loop's ceiling reads), 3 was the item record's allowed_paths, 2 was land's
	// landing_attempts gaining gated_tree, landed_tree and carried.
	if store.SchemaVersion != 5 {
		t.Fatalf("the schema version is %d", store.SchemaVersion)
	}
	if len(event.Kinds()) == 0 {
		t.Fatal("the event-kind registry is empty")
	}
	if record.ExitInternal != 5 {
		t.Fatalf("the internal-error exit is %d", record.ExitInternal)
	}
}

// Scratch lives under $YOLO_DURABLE_DIR, never /tmp: a suite that writes to /tmp loses its
// evidence when the box is cleaned, and the owner refuses it.
func scratchRoot() string {
	if root := os.Getenv("YOLO_DURABLE_DIR"); root != "" {
		return root
	}
	return filepath.Join("..", "..", "..", ".yolo", "durable")
}

func scratch(t *testing.T, prefix string) string {
	t.Helper()
	root := scratchRoot()
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("cannot create the scratch root: %v", err)
	}
	dir, err := os.MkdirTemp(root, prefix+"-")
	if err != nil {
		t.Fatalf("cannot create the scratch directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// The envelope is the versioned contract seven modules bind, so a consumer that guesses at
// an unsupported version instead of refusing it is a consumer that reads a record it does
// not understand. Both refusals are executed, not asserted in prose.
func TestTheEnvelopeRefusesAnUnsupportedVersionAndAForeignSystem(t *testing.T) {
	body := `{"record_version":%d,"kind":"item.record","id":"rec-1",
"producer":{"system":%q,"utility":"item","version":"0.1.0"},
"created_at":"2026-11-10T00:00:00Z","payload":{}}`
	if _, err := record.Decode([]byte(fmt.Sprintf(body, record.SupportedVersion+1, record.System))); !errors.Is(err, record.ErrUnsupportedVersion) {
		t.Fatalf("a record at version %d decoded without refusing: %v", record.SupportedVersion+1, err)
	}
	if _, err := record.Decode([]byte(fmt.Sprintf(body, record.SupportedVersion, "another-system"))); !errors.Is(err, record.ErrForeignSystem) {
		t.Fatalf("a record from another system decoded without refusing: %v", err)
	}
	if _, err := record.Decode([]byte(fmt.Sprintf(body, record.SupportedVersion, record.System))); err != nil {
		t.Fatalf("a record at the supported version was refused: %v", err)
	}
}

// A kind has one owner. Two owners of one kind is two utilities claiming one state
// transition, which is the defect the registry exists to catch; the same owner twice is a
// package linked twice and must not become a panic.
func TestTheKindRegistryRefusesASecondOwner(t *testing.T) {
	if err := event.Register("conformance.one", "conformance-a"); err != nil {
		t.Fatalf("registering a kind failed: %v", err)
	}
	if err := event.Register("conformance.one", "conformance-b"); err == nil {
		t.Fatal("a second owner registered the same kind")
	}
	if err := event.Register("conformance.one", "conformance-a"); err != nil {
		t.Fatalf("the same owner registering twice was refused: %v", err)
	}
	if kind, err := event.Lookup("conformance.one"); err != nil || kind.Owner != "conformance-a" {
		t.Fatalf("the kind's owner is %q after a refused second registration: %v", kind.Owner, err)
	}
}

// Seeding a live store is the one mistake that cannot be undone, so the store refuses it
// rather than leaving the check to every caller. Both halves are executed: a fixture is
// marked, and a store that is not a fixture refuses the mark and refuses the seed.
func TestTheFixtureMarkRefusesALiveStore(t *testing.T) {
	dir := scratch(t, "fixture-mark")
	live := filepath.Join(dir, "live.sqlite3")
	s, err := store.Open(context.Background(), live, true)
	if err != nil {
		t.Fatalf("creating a store failed: %v", err)
	}
	s.Close()
	if _, err := store.InitFixture(context.Background(), live); !errors.Is(err, store.ErrLiveStore) {
		t.Fatalf("marking an existing store as a fixture was not refused: %v", err)
	}
	opened, err := store.Open(context.Background(), live, false)
	if err != nil {
		t.Fatalf("reopening the store failed: %v", err)
	}
	defer opened.Close()
	bundle := []byte(`{"kind":"fixture.seed","payload":{"records":[]}}`)
	if err := opened.Seed(context.Background(), bundle); err == nil {
		t.Fatal("a store that is not a fixture accepted a seed")
	}
}

// A read-only open does not migrate. The WS1 eval found the first version of this test
// comparing a value a migrating open leaves unchanged, so it could not fail for the reason
// it stated; this one deletes the migration rows and asserts the read-only open leaves them
// deleted. The version is read through the reader's own accessor rather than by a query of
// the suite's own: a state question is the substrate's to answer.
func TestAReadOnlyOpenDoesNotMigrate(t *testing.T) {
	dir := scratch(t, "read-only")
	path := filepath.Join(dir, "board.sqlite3")
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatalf("creating a store failed: %v", err)
	}
	if err := s.ForgetMigrations(); err != nil {
		t.Fatalf("deleting the migration rows failed: %v", err)
	}
	s.Close()
	r, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatalf("opening the store read-only failed: %v", err)
	}
	defer r.Close()
	var version int
	if err := r.ReadTx(context.Background(), func(reader *store.Reader) error {
		var err error
		version, err = reader.SchemaVersion()
		return err
	}); err != nil {
		t.Fatalf("reading the schema version failed: %v", err)
	}
	if version != 0 {
		t.Fatalf("a read-only open re-applied the migrations, leaving the store at version %d", version)
	}
}

// The exit codes are the CLI's documented contract, and a caller that cannot tell a
// negative decision from no decision retries a typo forever. The set is asserted whole, so
// a renumbering is a deliberate edit rather than a silent one.
func TestTheExitCodesAreTheContract(t *testing.T) {
	want := map[string]int{
		"ExitOK": 0, "ExitNegative": 1, "ExitUsage": 2,
		"ExitIncompatible": 3, "ExitExternal": 4, "ExitInternal": 5,
	}
	got := map[string]int{
		"ExitOK": record.ExitOK, "ExitNegative": record.ExitNegative, "ExitUsage": record.ExitUsage,
		"ExitIncompatible": record.ExitIncompatible, "ExitExternal": record.ExitExternal,
		"ExitInternal": record.ExitInternal,
	}
	for name, code := range want {
		if got[name] != code {
			t.Errorf("%s is %d, want %d", name, got[name], code)
		}
	}
}
