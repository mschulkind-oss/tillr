package item_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// items.rank is a column added to a table that already existed, and CREATE TABLE IF NOT
// EXISTS is a no-op against a store that already has the table: a store created before the
// rank keeps its old shape, and the first read or write naming the column fails on it. A
// case on a fresh store cannot catch that — the fresh path never had the defect — so this
// one takes a store back to the shape a tillr-item built before rank left behind and opens
// it with the current code.
//
// Both halves of the migration are pinned. The first open is the repair: the column is
// there, with the declaration the CREATE TABLE states, and the store records the version
// this binary writes. The second open is the idempotence the probe in addMissingColumns
// exists for — migrate runs on every create-open, so a second add would fail on the
// duplicate — and it is asserted by the column still being there exactly once.
//
// Negative control: the same store before the first open, whose items table has no rank
// column at all. That is what makes the assertion about the repair rather than about a
// store this test created with the column already in it.
func TestAStoreCreatedBeforeTheRankColumnGainsItOnFirstOpen(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(scratch(t), "board.sqlite3")
	created, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatalf("creating the store failed: %v", err)
	}
	if err := created.Close(); err != nil {
		t.Fatalf("closing the store failed: %v", err)
	}

	restorePreRankShape(t, path)

	repaired, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatalf("opening the store created before the rank failed: %v", err)
	}
	if got := columnDeclaration(t, repaired, "items", "rank"); got != "TEXT|notnull|''" {
		repaired.Close()
		t.Fatalf("the repaired store's items.rank is %q, want the declaration ItemDDL states", got)
	}
	version, err := repaired.SchemaVersion()
	if err != nil {
		repaired.Close()
		t.Fatalf("reading the repaired store's schema version failed: %v", err)
	}
	if version != store.SchemaVersion {
		repaired.Close()
		t.Fatalf("the repaired store reports schema version %d, want %d", version, store.SchemaVersion)
	}
	if err := repaired.Close(); err != nil {
		t.Fatalf("closing the repaired store failed: %v", err)
	}

	// The second open adds nothing: the column is still there, once, and the version is
	// unchanged rather than re-recorded as a new migration.
	again, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatalf("the second create-open of the same store failed: %v", err)
	}
	defer again.Close()
	if got := columnDeclaration(t, again, "items", "rank"); got != "TEXT|notnull|''" {
		t.Fatalf("after the second open items.rank is %q, want the declaration ItemDDL states", got)
	}
	var columns int
	if err := again.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(
			`SELECT count(*) FROM pragma_table_info('items') WHERE name = 'rank'`,
		).Scan(&columns)
	}); err != nil {
		t.Fatalf("counting the rank columns failed: %v", err)
	}
	if columns != 1 {
		t.Fatalf("the second open left %d rank columns, want 1", columns)
	}
	version, err = again.SchemaVersion()
	if err != nil {
		t.Fatalf("reading the schema version after the second open failed: %v", err)
	}
	if version != store.SchemaVersion {
		t.Fatalf("after the second open the store reports schema version %d, want %d", version, store.SchemaVersion)
	}
}

// restorePreRankShape takes a store back to the shape a tillr-item built before the rank
// left behind: items without its rank column, and the schema version such a store recorded.
// It writes the database directly because the code under test migrates forward only — a
// helper that could not build the old shape could not test the repair.
func restorePreRankShape(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatalf("opening %s to restore the pre-rank shape failed: %v", path, err)
	}
	defer db.Close()
	if _, err := db.Exec(`ALTER TABLE items DROP COLUMN rank`); err != nil {
		t.Fatalf("dropping items.rank failed: %v", err)
	}
	if _, err := db.Exec(`DELETE FROM schema_migrations`); err != nil {
		t.Fatalf("clearing the migration rows failed: %v", err)
	}
	if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (4, 0)`); err != nil {
		t.Fatalf("recording the pre-rank schema version failed: %v", err)
	}
}

// columnDeclaration answers one column's declaration as the store's pragma reports it:
// its type, whether it is NOT NULL, and its default. The declaration is what a store that
// gained the column through the migration has to have — a column added with the wrong one
// reads back empty or NULL where the CREATE TABLE's default would have applied.
func columnDeclaration(t *testing.T, s *store.Store, table, column string) string {
	t.Helper()
	var got string
	err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var typ, dflt string
		var notNull int
		if err := r.QueryRow(
			`SELECT type, "notnull", COALESCE(dflt_value, '') FROM pragma_table_info(?) WHERE name = ?`,
			table, column,
		).Scan(&typ, &notNull, &dflt); err != nil {
			return err
		}
		nullness := "notnull"
		if notNull == 0 {
			nullness = "null"
		}
		got = fmt.Sprintf("%s|%s|%s", typ, nullness, dflt)
		return nil
	})
	if err != nil {
		t.Fatalf("reading column %s.%s failed: %v", table, column, err)
	}
	return got
}
