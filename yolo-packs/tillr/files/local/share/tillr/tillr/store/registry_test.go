package store_test

import (
	"context"
	"database/sql"
	"fmt"
	"path/filepath"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// A utility's DDL is applied by the store's migration because the utility registered
// it, with no edit to a central list. The table name is unique to this test so the
// registration cannot be satisfied by a table another test created.
func TestARegisteredDDLIsAppliedByMigration(t *testing.T) {
	const ddl = `CREATE TABLE IF NOT EXISTS registry_test_rows (id TEXT PRIMARY KEY);`
	if err := store.RegisterDDL("registry-test", ddl); err != nil {
		t.Fatalf("registering a DDL failed: %v", err)
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
	if _, ok := counts["registry_test_rows"]; !ok {
		t.Fatalf("a registered DDL was not applied by the migration: %v", counts)
	}
}

// The same owner registering the same DDL again is a no-op: a package linked twice,
// or a test run with -count=2, is not a defect and must not become an import-time
// panic. A *different* DDL for the same owner is a defect, because the store would
// then apply whichever registration ran last and the schema would depend on import
// order.
func TestAnOwnerCannotReRegisterADifferentDDL(t *testing.T) {
	const ddl = `CREATE TABLE IF NOT EXISTS registry_same (id TEXT);`
	if err := store.RegisterDDL("registry-same", ddl); err != nil {
		t.Fatalf("registering a DDL failed: %v", err)
	}
	if err := store.RegisterDDL("registry-same", ddl); err != nil {
		t.Fatalf("re-registering the same DDL failed: %v", err)
	}
	if err := store.RegisterDDL("registry-same", `CREATE TABLE IF NOT EXISTS registry_other (id TEXT);`); err == nil {
		t.Fatal("an owner re-registered a different DDL")
	}
}

// A registration with no owner or no DDL names nothing to apply and nothing to
// attribute it to, so it is refused rather than stored.
func TestARegistrationNeedsAnOwnerAndADDL(t *testing.T) {
	if err := store.RegisterDDL("", `CREATE TABLE IF NOT EXISTS registry_anon (id TEXT);`); err == nil {
		t.Fatal("a DDL registered with no owner")
	}
	if err := store.RegisterDDL("registry-empty", ""); err == nil {
		t.Fatal("an empty DDL registered")
	}
}

// A registered column is added to a table that already exists without it, which is the
// one thing CREATE TABLE IF NOT EXISTS cannot do: this DDL deliberately does not declare
// the column, so the store is created with the table and the migration has to add it.
// The declaration asserted is the registration's, not a bare column, because a store that
// gained the column through the migration has to be the shape a fresh CREATE TABLE writes.
func TestARegisteredColumnIsAddedToATableThatLacksIt(t *testing.T) {
	if err := store.RegisterDDL("registry-columns", `CREATE TABLE IF NOT EXISTS registry_column_rows (id TEXT PRIMARY KEY);`); err != nil {
		t.Fatalf("registering a DDL failed: %v", err)
	}
	if err := store.RegisterColumn("registry-columns", "registry_column_rows", "added", "TEXT NOT NULL DEFAULT ''"); err != nil {
		t.Fatalf("registering a column failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	if got := columnDeclaration(t, s, "registry_column_rows", "added"); got != "TEXT|notnull|''" {
		t.Fatalf("the registered column is %q, want the registered declaration", got)
	}
}

// The probe is what makes the step idempotent, and it has to be: migrate runs on every
// create-open, so a second open of the same store would otherwise try to add the column
// again and fail on the duplicate. Two processes opening one store are the same case
// serialized by the store's write lock.
func TestAddingARegisteredColumnIsIdempotent(t *testing.T) {
	if err := store.RegisterDDL("registry-columns-again", `CREATE TABLE IF NOT EXISTS registry_column_again (id TEXT PRIMARY KEY);`); err != nil {
		t.Fatalf("registering a DDL failed: %v", err)
	}
	if err := store.RegisterColumn("registry-columns-again", "registry_column_again", "added", "INTEGER NOT NULL DEFAULT 0"); err != nil {
		t.Fatalf("registering a column failed: %v", err)
	}
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	for open := 1; open <= 2; open++ {
		s, err := store.InitFixture(context.Background(), path)
		if err != nil {
			t.Fatalf("create-open %d of the same store failed: %v", open, err)
		}
		got := columnDeclaration(t, s, "registry_column_again", "added")
		s.Close()
		if got != "INTEGER|notnull|0" {
			t.Fatalf("after create-open %d the column is %q, want the registered declaration", open, got)
		}
	}
}

// A column belongs to the table, and the table to one owner, so a second owner naming it
// is the two-writers defect the DDL registry refuses for the same reason. The table's DDL
// is registered with the column because a column registration names a table its owner's
// DDL creates, and one that named nobody's would fail the migration of every store this
// test process goes on to create.
func TestAColumnBelongsToOneOwner(t *testing.T) {
	if err := store.RegisterDDL("registry-column-owner", `CREATE TABLE IF NOT EXISTS registry_column_owned (id TEXT PRIMARY KEY);`); err != nil {
		t.Fatalf("registering a DDL failed: %v", err)
	}
	if err := store.RegisterColumn("registry-column-owner", "registry_column_owned", "added", "TEXT"); err != nil {
		t.Fatalf("registering a column failed: %v", err)
	}
	if err := store.RegisterColumn("registry-column-owner", "registry_column_owned", "added", "TEXT"); err != nil {
		t.Fatalf("re-registering the same column failed: %v", err)
	}
	if err := store.RegisterColumn("registry-column-other", "registry_column_owned", "added", "TEXT"); err == nil {
		t.Fatal("a second owner registered another owner's column")
	}
	if err := store.RegisterColumn("registry-column-owner", "registry_column_owned", "added", "INTEGER"); err == nil {
		t.Fatal("an owner re-registered its column with a different declaration")
	}
}

// A registration that names nothing names nothing to add and nothing to attribute it to.
func TestAColumnRegistrationNeedsEveryPart(t *testing.T) {
	for _, args := range [][4]string{
		{"", "registry_column_anon", "added", "TEXT"},
		{"registry-column-anon", "", "added", "TEXT"},
		{"registry-column-anon", "registry_column_anon", "", "TEXT"},
		{"registry-column-anon", "registry_column_anon", "added", ""},
	} {
		if err := store.RegisterColumn(args[0], args[1], args[2], args[3]); err == nil {
			t.Fatalf("a column registration with a missing part was accepted: %q", args)
		}
	}
}

// columnDeclaration answers a column's type, non-nullness and default as sqlite reports
// them, so a test can compare a migrated column with a declared one instead of asserting
// that a name exists.
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

// Two opens of one store at the same time both succeed, and the column is added once.
// This is the guarantee a second process depends on: the migration reads before it writes,
// and SQLite refuses to let a transaction that has already read become the writer instead
// of waiting, so a deferred migration answered the second opener SQLITE_BUSY rather than
// waiting for the first. Each opener here has its own connection, which is the contention
// two processes have.
//
// The table is in the old shape — created, then the column dropped — because that is the
// case the migration has to do work in; a store with nothing to add never reaches the
// statement the two openers contended.
func TestTwoOpensOfOneStoreAtOnceBothAddTheMissingColumn(t *testing.T) {
	if err := store.RegisterDDL("registry-concurrent", `CREATE TABLE IF NOT EXISTS registry_concurrent_rows (id TEXT PRIMARY KEY);`); err != nil {
		t.Fatalf("registering a DDL failed: %v", err)
	}
	if err := store.RegisterColumn("registry-concurrent", "registry_concurrent_rows", "added", "TEXT NOT NULL DEFAULT ''"); err != nil {
		t.Fatalf("registering a column failed: %v", err)
	}
	for attempt := 0; attempt < 3; attempt++ {
		path := filepath.Join(t.TempDir(), "board.sqlite3")
		s, err := store.Open(context.Background(), path, true)
		if err != nil {
			t.Fatalf("creating the store failed: %v", err)
		}
		s.Close()
		db, err := sql.Open("sqlite", "file:"+path)
		if err != nil {
			t.Fatalf("opening %s to drop the column failed: %v", path, err)
		}
		if _, err := db.Exec(`ALTER TABLE registry_concurrent_rows DROP COLUMN added`); err != nil {
			t.Fatalf("dropping the column failed: %v", err)
		}
		db.Close()

		var wg sync.WaitGroup
		opened := make([]*store.Store, 2)
		errs := make([]error, 2)
		for opener := range opened {
			wg.Add(1)
			go func(opener int) {
				defer wg.Done()
				opened[opener], errs[opener] = store.Open(context.Background(), path, true)
			}(opener)
		}
		wg.Wait()
		for opener, err := range errs {
			if err != nil {
				t.Fatalf("opener %d of two at once failed: %v", opener, err)
			}
		}
		for opener, s := range opened {
			if got := columnDeclaration(t, s, "registry_concurrent_rows", "added"); got != "TEXT|notnull|''" {
				t.Fatalf("after two opens the column is %q (opener %d), want the registered declaration", got, opener)
			}
			s.Close()
		}
	}
}
