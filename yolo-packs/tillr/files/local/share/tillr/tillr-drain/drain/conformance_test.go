package drain_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// scratch answers a directory under $YOLO_DURABLE_DIR, never /tmp: a suite that writes
// to /tmp loses its evidence when the box is cleaned, and the owner refuses it.
func conformanceScratch(t *testing.T) string {
	t.Helper()
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("cannot create the scratch root: %v", err)
	}
	dir, err := os.MkdirTemp(root, "drain-conformance-")
	if err != nil {
		t.Fatalf("cannot create the scratch directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// The drain utility's conformance case: the two things a second consumer of this package
// would break by linking it.
//
// The first is the ITEM-PKG precondition made structural. plan writes nothing, but it
// reads items, lanes and runs, and a store created in drain's process carries those
// tables only because the item and run packages are linked into it — the substrate
// composes its migration list from what was registered at package load. Dropping either
// blank import leaves plan reading a store with no such table, which is exit 3 at the
// first tick rather than a compile error.
//
// The second is the claim that plan asserts no state transition: neither of drain's
// record kinds is an appendable event kind, and registering one would make that claim
// false while every other test stayed green.
func TestTheDrainUtilityComposesItsNeighboursAndRegistersNoEventKind(t *testing.T) {
	s, err := store.InitFixture(context.Background(), filepath.Join(conformanceScratch(t), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	counts, err := s.TableCounts()
	if err != nil {
		t.Fatalf("reading the table counts failed: %v", err)
	}
	for _, table := range []string{"items", "item_criteria", "item_edges", "lanes", "runs"} {
		if _, ok := counts[table]; !ok {
			t.Fatalf("the table %q is not in the composed migration list: %v", table, counts)
		}
	}

	for _, kind := range []string{drain.PassKind, drain.DecisionKind} {
		if registered, err := event.Lookup(kind); err == nil {
			t.Fatalf("%s is registered as an event kind owned by %q: plan asserts no state transition", kind, registered.Owner)
		}
	}
}
