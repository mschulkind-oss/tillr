package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// scratchUnderDurable answers a directory under $YOLO_DURABLE_DIR, never /tmp: the owner
// refuses /tmp for evidence, and a suite that writes there loses its scratch when the box is
// cleaned.
func scratchUnderDurable(t *testing.T) string {
	t.Helper()
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("cannot create the scratch root: %v", err)
	}
	dir, err := os.MkdirTemp(root, "drain-timing-")
	if err != nil {
		t.Fatalf("cannot create the scratch directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

// A day of overruns files one item, and a second overrun does not re-apply it.
//
// This is the negative control for the bug the first cut had: `item.Apply` is an upsert, so a
// wrong id in the existence check re-applied the item on every overrunning tick. One
// `item.add` and no `item.update` is what "filed once" reads as in the log.
func TestAnOverrunIsFiledOncePerDay(t *testing.T) {
	path := filepath.Join(scratchUnderDurable(t), "board.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("creating the fixture store failed: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("closing the fixture store failed: %v", err)
	}

	first := PassTiming{
		TickID: "tick-1", AtMS: time.Date(2026, 11, 14, 1, 0, 0, 0, time.UTC).UnixMilli(),
		GateMS: 400_000, GateBudgetMS: 330_000, Gates: 1,
	}
	if err := fileGateRegression(context.Background(), path, first); err != nil {
		t.Fatalf("filing the first overrun failed: %v", err)
	}
	second := first
	second.TickID = "tick-2"
	second.AtMS = time.Date(2026, 11, 14, 2, 0, 0, 0, time.UTC).UnixMilli()
	if err := fileGateRegression(context.Background(), path, second); err != nil {
		t.Fatalf("filing the second overrun failed: %v", err)
	}

	read, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatalf("opening the store failed: %v", err)
	}
	defer read.Close()
	var items, adds, updates int
	var state string
	if err := read.ReadTx(context.Background(), func(r *store.Reader) error {
		if err := r.QueryRow(`SELECT count(*), COALESCE(MAX(state), '') FROM items WHERE item_id = ?`,
			"gate-budget-2026-11-14").Scan(&items, &state); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind = ? AND object_id = ?`,
			"item.add", "gate-budget-2026-11-14").Scan(&adds); err != nil {
			return err
		}
		return r.QueryRow(`SELECT count(*) FROM events WHERE kind = ? AND object_id = ?`,
			"item.update", "gate-budget-2026-11-14").Scan(&updates)
	}); err != nil {
		t.Fatalf("reading the filed item failed: %v", err)
	}
	if items != 1 {
		t.Fatalf("the store holds %d gate-budget item(s), want 1", items)
	}
	if state != "in-queue" {
		t.Fatalf("the filed item is %q, want in-queue so the queue picks the regression up", state)
	}
	if adds != 1 || updates != 0 {
		t.Fatalf("the day's overruns appended %d item.add and %d item.update event(s), want 1 and 0: a second apply would reset a landed item's state", adds, updates)
	}
}
