package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
)

// holdWriteLock begins a write transaction on its own connection to path and answers the
// function that ends it. While it is held, any other connection that asks for the write
// lock is answered SQLITE_BUSY at once when its busy timeout is zero, so a test can show
// whether an operation needs the lock without measuring how long anything takes.
func holdWriteLock(t *testing.T, path string) func() {
	t.Helper()
	ctx := context.Background()
	holder, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	conn, err := holder.Conn(ctx)
	if err != nil {
		holder.Close()
		t.Fatal(err)
	}
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		conn.Close()
		holder.Close()
		t.Fatal(err)
	}
	released := false
	release := func() {
		if released {
			return
		}
		released = true
		conn.ExecContext(ctx, `ROLLBACK`)
		conn.Close()
		holder.Close()
	}
	t.Cleanup(release)
	return release
}

// Opening a store whose schema is already current is a read: it must not ask for the
// store's write lock. Every open used to begin the migration IMMEDIATE, so each of the
// loop's many concurrent opens queued behind whichever writer held the lock, and the
// one that waited past the busy timeout faulted the loop (2026-11-17, "store: beginning
// the migration: database is locked"). Here another connection holds the write lock and
// the open has no busy timeout at all, so an open that still asked for the lock fails
// deterministically, with no clock involved.
func TestOpeningACurrentStoreDoesNotTakeTheWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	holdWriteLock(t, path)
	reopened, err := Open(context.Background(), path, true, WithBusyTimeout(0))
	if err != nil {
		t.Fatalf("opening a current store while another connection writes answered %v; the open must not need the write lock", err)
	}
	if version, err := reopened.SchemaVersion(); err != nil || version != SchemaVersion {
		t.Fatalf("the reopened store reads version %d (%v), want %d", version, err, SchemaVersion)
	}
	if err := reopened.Close(); err != nil {
		t.Fatal(err)
	}
}

// The other half of the same rule: a store whose schema is NOT current still migrates,
// and so still needs the write lock. A store this binary has not migrated (here: its
// record of the applied schema forgotten) is refused SQLITE_BUSY while another writer
// holds the lock and no wait is allowed, and migrates once the lock is free.
func TestOpeningAStoreThatNeedsMigrationStillTakesTheWriteLock(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.ForgetMigrations(); err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	release := holdWriteLock(t, path)
	if _, err := Open(context.Background(), path, true, WithBusyTimeout(0)); !IsBusy(err) {
		t.Fatalf("opening a store that needs migration while another connection writes answered %v, want SQLITE_BUSY", err)
	}
	release()
	migrated, err := Open(context.Background(), path, true, WithBusyTimeout(0))
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()
	if version, err := migrated.SchemaVersion(); err != nil || version != SchemaVersion {
		t.Fatalf("the migrated store reads version %d (%v), want %d", version, err, SchemaVersion)
	}
}

// A registration the store has not applied yet makes the schema not current even though
// the version number is unchanged: the version is global, while the DDL and columns are
// whatever the opening program registered. So the fast path keys on what was applied,
// not on the number, and a newly registered DDL is still applied by the next open.
func TestANewRegistrationIsAppliedByTheNextOpenOfACurrentStore(t *testing.T) {
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	registryMu.Lock()
	previous := append([]ownerDDL(nil), registeredDDL...)
	registeredDDL = append(registeredDDL, ownerDDL{owner: "open-lock-fixture", ddl: `CREATE TABLE IF NOT EXISTS open_lock_fixture (id TEXT PRIMARY KEY);`})
	registryMu.Unlock()
	defer func() { registryMu.Lock(); registeredDDL = previous; registryMu.Unlock() }()
	reopened, err := Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer reopened.Close()
	var n int
	if err := reopened.db.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='table' AND name='open_lock_fixture'`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	if n != 1 {
		t.Fatal("a DDL registered after the store was migrated was not applied by the next open")
	}
}
