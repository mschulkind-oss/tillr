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
	db          *sql.DB
	path        string
	busyTimeout time.Duration
	// writable is whether Open made this handle; only a writable handle commits, so only
	// its close can checkpoint (checkpoint.go).
	writable bool
	// sidecar is the writable handle's read-only connection, opened before db and closed
	// after it, so this handle's close is never the store's last and cannot checkpoint or
	// delete the log; nil when it could not be opened or attached (traced as
	// sidecar.unavailable).
	sidecar *sql.DB
	// owner marks a checkpoint owner's own writer, whose close leaves checkpoints to the
	// owner's cycles.
	owner bool
	// lock is the checkpoint owner's lock file, opened once per handle so that its close
	// can see whether an owner runs; nil when it could not be opened, which reads as none.
	lock *os.File
}

// Open opens the store at path and selects SQLite's write-ahead log (WAL), so
// readers do not need to undo an interrupted writer's uncommitted transaction.
// With create, a missing store is created and the schema is migrated; without it
// the store must already exist and is not migrated. Mode conversion needs SQLite's
// exclusive lock and fails explicitly if it cannot acquire it.
func Open(ctx context.Context, path string, create bool, options ...OpenOption) (*Store, error) {
	return open(ctx, path, create, create, options...)
}

// open is Open with creating a missing store and migrating it separated: a checkpoint
// owner that starts before any store exists creates the empty WAL database and leaves the
// migration to the program that then opens it with create (CreateAndOwnCheckpoints).
func open(ctx context.Context, path string, create, migrate bool, options ...OpenOption) (result *Store, resultErr error) {
	cfg, err := storeOpenConfig(options)
	if err != nil {
		return nil, err
	}
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
	trace := startSQLiteTrace(path, "open")
	defer func() { trace.finish(resultErr) }()
	s := &Store{path: path, writable: true, busyTimeout: cfg.busyTimeout}
	// The sidecar first, so that even this open's own failure paths close no connection
	// that is the store's last. A store that does not exist yet gets it once WAL is
	// selected, before anything is migrated.
	attached := false
	if _, err := os.Stat(path); err == nil {
		attached = s.attachSidecar(ctx, trace)
	}
	db, err := sql.Open("sqlite", dsn(path, false, cfg.busyTimeout))
	if err != nil {
		s.release(nil)
		return nil, fmt.Errorf("store: opening %s: %w", path, err)
	}
	s.db = db
	// One connection: the append-only triggers and the migration transaction are
	// serialized by the store, not by the caller.
	db.SetMaxOpenConns(1)
	trace.record("ping.begin", nil)
	pingErr := db.PingContext(ctx)
	trace.record("ping.end", pingErr)
	if err := pingErr; err != nil {
		s.release(nil)
		return nil, fmt.Errorf("store: opening %s: %w", path, err)
	}
	var mode string
	trace.record("wal.begin", nil)
	walErr := db.QueryRowContext(ctx, `PRAGMA journal_mode=WAL`).Scan(&mode)
	trace.record("wal.end", walErr)
	if err := walErr; err != nil {
		s.release(nil)
		return nil, fmt.Errorf("store: selecting WAL for %s: %w", path, err)
	}
	if mode != "wal" {
		s.release(nil)
		return nil, fmt.Errorf("store: selecting WAL for %s: SQLite answered %q", path, mode)
	}
	if !attached && !s.attachSidecar(ctx, trace) {
		// Without a sidecar the handle runs on: its commits still never checkpoint
		// (wal_autocheckpoint=0, dsn) and its close skips the cycle (checkpointIfDue,
		// reason=no-sidecar). What it cannot prevent is SQLite's own last close: when no
		// other connection has the store open, the writer's close checkpoints and deletes
		// the log, because the driver offers no way to turn that off
		// (SQLITE_DBCONFIG_NO_CKPT_ON_CLOSE is not reachable through it).
		if s.sidecar != nil {
			s.sidecar.Close()
			s.sidecar = nil
		}
		trace.record("sidecar.unavailable", nil)
	}
	// A lock file this process cannot open or create means no owner can be seen: the
	// handle's close then checkpoints past CheckpointFrames, as it would with no loop.
	lock, lockErr := openOwnerLock(path)
	s.lock = lock
	if lockErr != nil {
		trace.record("owner-lock.unavailable", lockErr)
	}
	if migrate {
		if err := s.migrate(ctx); err != nil {
			s.release(nil)
			return nil, err
		}
	}
	return s, nil
}

// failSidecar is a test seam: an error it answers is the sidecar's read's, as if attaching
// had failed.
var failSidecar func() error

// attachSidecar opens the handle's read-only sidecar, if it has none yet, and reads through
// it, which attaches it to the log's shared index. It answers whether the sidecar is now
// attached to a WAL store; a store still in rollback mode (before Open selects WAL) or one
// it could not read answers false, and the caller tries again after selecting WAL.
func (s *Store) attachSidecar(ctx context.Context, trace *sqliteTraceSpan) bool {
	trace.record("sidecar.begin", nil)
	if s.sidecar == nil {
		db, err := sql.Open("sqlite", dsn(s.path, true, s.busyTimeout))
		if err != nil {
			trace.record("sidecar.end", err)
			return false
		}
		db.SetMaxOpenConns(1)
		s.sidecar = db
	}
	var n int
	var mode string
	var err error
	if failSidecar != nil {
		err = failSidecar()
	}
	if err == nil {
		err = s.sidecar.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master`).Scan(&n)
	}
	if err == nil {
		err = s.sidecar.QueryRowContext(ctx, `PRAGMA journal_mode`).Scan(&mode)
	}
	trace.record("sidecar.end", err)
	return err == nil && mode == "wal"
}

// OpenRead opens an existing store read-only. It never migrates: the read-only face
// is safe to point at a store a writer is using.
func OpenRead(ctx context.Context, path string, options ...OpenOption) (*Store, error) {
	cfg, err := storeOpenConfig(options)
	if err != nil {
		return nil, err
	}
	if path == "" {
		return nil, errors.New("store: a store path is required")
	}
	// The stat is what makes a missing store a missing store: the driver answers a
	// bare "unable to open database file (14)", which a caller cannot tell from a
	// permission problem or a corrupt file.
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	db, err := sql.Open("sqlite", dsn(path, true, cfg.busyTimeout))
	if err != nil {
		return nil, fmt.Errorf("store: opening %s read-only: %w", path, err)
	}
	db.SetMaxOpenConns(1)
	if err := db.PingContext(ctx); err != nil {
		db.Close()
		return nil, readOpenError(path, err)
	}
	return &Store{db: db, path: path, busyTimeout: cfg.busyTimeout}, nil
}

// Close closes the store. A writable handle first checkpoints the log if it is due
// (checkpointIfDue), then closes its writer, and its sidecar last: its close neither
// checkpoints nor deletes the log, so it flushes nothing and takes no lock another
// connection can wait behind. A read-only handle just closes.
//
// With TILLR_SQLITE_TRACE=1 a writable handle's close is the span "close" (the
// log-checkpoint decision, writer.close.begin, writer.close.end, sidecar.close.begin,
// sidecar.close.end), so a close a writer waited behind is attributable.
func (s *Store) Close() error {
	if !s.writable {
		return s.db.Close()
	}
	trace := startSQLiteTrace(s.path, "close")
	err := s.checkpointIfDue(context.Background(), trace)
	err = errors.Join(err, s.release(trace))
	trace.finish(err)
	return err
}

// release closes the writer, the lock file and then the sidecar, recording each close
// into trace (which may be nil).
func (s *Store) release(trace *sqliteTraceSpan) error {
	var err error
	if s.db != nil {
		trace.record("writer.close.begin", nil)
		err = s.db.Close()
		trace.record("writer.close.end", err)
	}
	if s.lock != nil {
		s.lock.Close()
	}
	if s.sidecar != nil {
		trace.record("sidecar.close.begin", nil)
		sidecarErr := s.sidecar.Close()
		trace.record("sidecar.close.end", sidecarErr)
		err = errors.Join(err, sidecarErr)
	}
	return err
}

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
	trace := startSQLiteTrace(s.path, "migrate")
	defer func() { trace.finish(result) }()
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: opening the connection to migrate on: %w", err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		return fmt.Errorf("store: beginning the migration: %w", err)
	}
	trace.record("acquired", nil)
	trace.record("schema.begin", nil)
	schemaErr := migrate(ctx, conn)
	trace.record("schema.end", schemaErr)
	if err := schemaErr; err != nil {
		conn.ExecContext(ctx, `ROLLBACK`)
		return err
	}
	trace.record("commit.begin", nil)
	_, commitErr := conn.ExecContext(ctx, `COMMIT`)
	trace.record("commit.end", commitErr)
	if err := commitErr; err != nil {
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
//
// The writable handle runs synchronous=NORMAL, so a commit appends to the write-ahead
// log without flushing it to disk; the log is flushed before each checkpoint and the
// database after it. The default, FULL, flushed inside every commit, and a commit holds
// SQLite's writer, so one slow disk flush became a writer every other connection waited
// behind until its busy timeout. The trade: an OS crash or power loss can roll back the
// last committed transactions, typically the last 30 to 60 seconds, which the operating
// system's writeback had not yet put on disk. It cannot corrupt the store, and a process,
// container or jail kill loses nothing. The owner ruled on 2026-11-16 that NORMAL stays
// (tools/tillr-store/README.md, "Durability and checkpoints").
//
// What NORMAL leaves inside a writer is the log restart's header flush. The writable handle
// also runs wal_autocheckpoint=0, so no commit checkpoints: checkpoint.go decides which
// program checkpoints, and so which writer pays that flush.
func dsn(path string, readOnly bool, wait time.Duration) string {
	busy := fmt.Sprintf("_pragma=busy_timeout(%d)", wait.Milliseconds())
	params := "?" + busy + "&_pragma=foreign_keys(1)&_pragma=synchronous(NORMAL)&_pragma=wal_autocheckpoint(0)&_txlock=immediate"
	if readOnly {
		params = "?mode=ro&" + busy
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
