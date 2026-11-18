package store_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// The 2026-11-18 log that never restarted: every lane's tillr-run polls its pause request
// ten times a second, and with no index but seq that poll scanned the whole event log
// backwards. Ten lanes kept a reader slot share-locked for minutes, so no checkpoint
// could copy past its mark. The polls, and the other per-object and per-kind reads, now
// search an index; a plan that scans the events table is the defect returning.
func TestPerObjectAndPerKindEventReadsUseAnIndex(t *testing.T) {
	s := writeStore(t)
	queries := []string{
		// tillr-run's pendingPause, the 100 ms poll.
		`SELECT kind, payload FROM events WHERE object_type = 'run' AND object_id = ? AND kind IN (?, ?, ?) ORDER BY seq DESC LIMIT 1`,
		`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`,
		`SELECT payload FROM events WHERE kind = ? AND object_id = ? ORDER BY seq DESC LIMIT 1`,
		`SELECT payload FROM events WHERE kind = ? ORDER BY seq`,
	}
	for _, q := range queries {
		var plan []string
		if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
			rows, err := r.Query("EXPLAIN QUERY PLAN "+q, "a", "b", "c", "d")
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id, parent, unused int
				var detail string
				if err := rows.Scan(&id, &parent, &unused, &detail); err != nil {
					return err
				}
				plan = append(plan, detail)
			}
			return rows.Err()
		}); err != nil {
			t.Fatal(err)
		}
		joined := strings.Join(plan, "; ")
		if !strings.Contains(joined, "USING INDEX events_by_") {
			t.Errorf("%s\n  plans as %q, not through an events index", q, joined)
		}
	}
}

// A store migrated before the indexes existed gains them on its next migrating open.
func TestAnExistingStoreGainsTheEventIndexes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP INDEX events_by_object; DROP INDEX events_by_kind`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	reopened, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var n int
	if err := reopened.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'index' AND name IN ('events_by_object', 'events_by_kind')`).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	if n != 2 {
		t.Fatalf("the reopened store has %d of the 2 event indexes", n)
	}
}
