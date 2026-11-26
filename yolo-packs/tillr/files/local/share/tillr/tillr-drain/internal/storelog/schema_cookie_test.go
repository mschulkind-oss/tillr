package storelog

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// An open reads whether the schema is current by SQLite's schema cookie, which every DDL
// statement that changes the schema bumps (store.schemaApplied). So migrating a store whose
// schema is already current must change nothing in it: a registered statement that
// re-creates an object on every run bumps the cookie, makes every other program's record
// of its schema stale, and turns their opens back into write-lock migrations. This applies
// the drain binary's whole registered schema to a current store and compares the cookie.
func TestMigratingACurrentStoreLeavesItsSchemaUnchanged(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(scratchUnderDurable(t), "cookie.db")
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	cookie := func(s *store.Store) (n int64) {
		t.Helper()
		if err := s.ReadTx(ctx, func(r *store.Reader) error { return r.QueryRow(`PRAGMA schema_version`).Scan(&n) }); err != nil {
			t.Fatal(err)
		}
		return n
	}
	before := cookie(s)
	// Forgetting the migration rows is data, not schema: the next open migrates again.
	if err := s.ForgetMigrations(); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if after := cookie(s); after != before {
		t.Fatalf("re-applying the registered schema to a current store moved its schema cookie from %d to %d: some registered DDL re-creates an object on every migration", before, after)
	}
}
