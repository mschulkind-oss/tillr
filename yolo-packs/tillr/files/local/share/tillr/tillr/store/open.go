// Package store opens, migrates and describes a tillr store. Store access is always
// explicit: a caller names a path, and no face here derives the live store.
package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// Store is one open store.
type Store struct {
	db   *sql.DB
	path string
}

// Open opens the store at path and selects SQLite's write-ahead log (WAL), so
// readers do not need to undo an interrupted writer's uncommitted transaction.
// With create, a missing store is created and the schema is migrated; without it
// the store must already exist and is not migrated. Mode conversion needs SQLite's
// exclusive lock and fails explicitly if it cannot acquire it.
func Open(ctx context.Context, path string, create bool) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: a store path is required")
	}
	if create {
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("store: creating the store directory: %w", err)
		}
	} else if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(path, false))
	if err != nil {
		return nil, fmt.Errorf("store: opening %s: %w", path, err)
	}
	// One connection: the append-only triggers and the migration transaction are
	// serialized by the store, not by the caller.
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: opening %s: %w", path, err)
	}
	var mode string
	if err := db.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&mode); err != nil {
		db.Close()
		return nil, fmt.Errorf("store: selecting WAL for %s: %w", path, err)
	}
	if mode != "wal" {
		db.Close()
		return nil, fmt.Errorf("store: selecting WAL for %s: SQLite answered %q", path, mode)
	}
	s := &Store{db: db, path: path}
	if create {
		if err := s.migrate(ctx); err != nil {
			db.Close()
			return nil, err
		}
	}
	return s, nil
}

// OpenRead opens an existing store read-only. It never migrates: the read-only face
// is safe to point at a store a writer is using.
func OpenRead(ctx context.Context, path string) (*Store, error) {
	if path == "" {
		return nil, errors.New("store: a store path is required")
	}
	// The stat is what makes a missing store a missing store: the driver answers a
	// bare "unable to open database file (14)", which a caller cannot tell from a
	// permission problem or a corrupt file.
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(path, true))
	if err != nil {
		return nil, fmt.Errorf("store: opening %s read-only: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, readOpenError(path, err)
	}
	return &Store{db: db, path: path}, nil
}

// Close closes the store.
func (s *Store) Close() error { return s.db.Close() }

// Path answers the path this store was opened at.
func (s *Store) Path() string { return s.path }

// SchemaVersion answers the highest migration this store has applied.
func (s *Store) SchemaVersion() (int, error) {
	return schemaVersion(context.Background(), s.db)
}

// schemaVersion answers the highest migration the store has applied, and 0 for a store
// that has applied none.
func schemaVersion(ctx context.Context, q queryer) (int, error) {
	var version int
	err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(version), 0) FROM schema_migrations`).Scan(&version)
	if err != nil {
		return 0, fmt.Errorf("store: reading the schema version: %w", err)
	}
	return version, nil
}

// Meta answers one meta row, or nil when the key is absent.
func (s *Store) Meta(key string) (*string, error) {
	var value string
	err := s.db.QueryRow(`SELECT value FROM meta WHERE key = ?`, key).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, fmt.Errorf("store: reading meta %q: %w", key, err)
	}
	return &value, nil
}

// WriteMeta writes one meta row.
func (s *Store) WriteMeta(key, value string) error {
	_, err := s.db.Exec(
		`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`,
		key, value,
	)
	if err != nil {
		return fmt.Errorf("store: writing meta %q: %w", key, err)
	}
	return nil
}

// migrate applies the schema in one transaction, which it begins IMMEDIATE rather than
// with the handle's deferred begin.
//
// The difference is what two processes opening one store get. The migration reads before
// it can write — the DDL list probes sqlite_master, the column step probes
// pragma_table_info — and SQLite answers SQLITE_BUSY instead of waiting when a
// transaction that has already read tries to become the writer, because waiting there
// could deadlock. A deferred migration therefore made the second opener of an old store
// fail rather than wait. An immediate begin takes the store's write lock before anything
// is read, so the second migrator waits (busy_timeout, dsn) and then finds the work
// already done.
func (s *Store) migrate(ctx context.Context) (result error) {
	var id uint64
	var started time.Time
	if os.Getenv("TILLR_SQLITE_TRACE") == "1" {
		id, started = writerSequence.Add(1), time.Now()
		traceWriter(id, s.path, "migrate", "begin", started, nil)
		defer func() { traceWriter(id, s.path, "migrate", "end", started, result) }()
	}
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: opening the connection to migrate on: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("store: beginning the migration: %w", err)
	}
	if id != 0 {
		traceWriter(id, s.path, "migrate", "acquired", started, nil)
	}
	if err := migrate(ctx, conn); err != nil {
		conn.ExecContext(ctx, `ROLLBACK`)
		return err
	}
	if _, err := conn.ExecContext(ctx, `COMMIT`); err != nil {
		conn.ExecContext(ctx, `ROLLBACK`)
		return fmt.Errorf("store: committing the migration: %w", err)
	}
	return nil
}

// dsn is the sqlite connection string. Read-only opens carry mode=ro so a write
// attempt fails at the driver rather than at a caller's discipline.
//
// The writable handle begins every transaction IMMEDIATE. A write transaction reads
// before it writes, and SQLite answers a transaction that has already read and now asks
// to become the writer with SQLITE_BUSY rather than waiting, because waiting there could
// deadlock; so a deferred write transaction made a second writer fail instead of queueing
// behind the first. Taking the write lock at the begin means the second writer waits out
// the connection's busy timeout and then proceeds, which is what migrate does by hand.
//
// The driver applies this mode only to a transaction that is not read-only (tx.go,
// newTx), so a read path on a writable handle must pass the read-only option to keep its
// deferred begin: ReadTx is where that happens.
func dsn(path string, readOnly bool) string {
	params := "?_pragma=busy_timeout(5000)&_pragma=foreign_keys(1)&_txlock=immediate"
	if readOnly {
		params = "?mode=ro&_pragma=busy_timeout(5000)"
	}
	return "file:" + path + params
}

// ForgetMigrations deletes the migration rows, leaving the store at version 0. It
// exists so a test can make a migrating open observable: an open of an
// already-current store would leave the version unchanged either way, so a test
// built on that comparison cannot fail for the reason it states.
func (s *Store) ForgetMigrations() error {
	if _, err := s.db.Exec(`DELETE FROM schema_migrations`); err != nil {
		return fmt.Errorf("store: forgetting the migration rows: %w", err)
	}
	return nil
}

// DefaultPath is where derivation names a store for a workspace root: under a
// configured store root as <root>/<slug>/board.sqlite3, and otherwise the fixed
// default under the state home. The configured root is a parameter rather than a
// config read: reading the config is the config package's business, and the store
// package must not grow a dependency on it. Dropping the configured-root branch
// would name a different store than the monolith on any machine that sets one.
func DefaultPath(workspaceRoot, configuredRoot string) string {
	if configuredRoot != "" {
		return filepath.Join(configuredRoot, slugFor(workspaceRoot), "board.sqlite3")
	}
	stateHome := os.Getenv("XDG_STATE_HOME")
	if stateHome == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		stateHome = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(stateHome, "board", slugFor(workspaceRoot), "board.sqlite3")
}

// slugFor is the workspace slug the store path is keyed by, copied from the
// monolith's SlugFor so a fixture and the live store cannot disagree about it.
func slugFor(root string) string {
	abs, err := filepath.Abs(root)
	if err != nil {
		abs = root
	}
	parts := []string{}
	for _, part := range strings.Split(filepath.ToSlash(abs), "/") {
		if part != "" {
			parts = append(parts, part)
		}
	}
	return "--" + strings.Join(parts, "-") + "--"
}
