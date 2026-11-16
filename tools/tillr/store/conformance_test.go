package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// The store utility's conformance case: the two things a second consumer of this
// package would break by linking it — the store's own event kind registered with its
// owner, and the store's own tables present in the composed migration list.
//
// It lives in the utility's own package so the conformance suite grows one case per
// utility as each lands, and no central file is edited to add one.
func TestTheStoreUtilityRegistersItsOwnContract(t *testing.T) {
	kind, err := event.Lookup("fixture.seed")
	if err != nil {
		t.Fatalf("the store's own event kind is not registered: %v", err)
	}
	if kind.Owner != "store" {
		t.Fatalf("fixture.seed is owned by %q, want store", kind.Owner)
	}
	s, err := store.InitFixture(context.Background(), filepath.Join(t.TempDir(), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	counts, err := s.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	for _, table := range []string{"meta", "schema_migrations", "events"} {
		if _, ok := counts[table]; !ok {
			t.Fatalf("the store's own table %q is not in the composed migration list: %v", table, counts)
		}
	}
}
