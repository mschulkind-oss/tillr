package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"

	"modernc.org/sqlite"
)

// NeedsRecovery identifies SQLite's extended SQLITE_READONLY_ROLLBACK code (776):
// the reader needs a writer to undo a dead writer's uncommitted rollback journal.
func NeedsRecovery(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && e.Code() == 776
}

func readOpenError(path string, err error) error {
	if NeedsRecovery(err) {
		return fmt.Errorf("store: %s carries an uncommitted writer's transaction (SQLITE_READONLY_ROLLBACK, 776); run tillr-store recover --store %q, then read it again: %w", path, path, err)
	}
	return fmt.Errorf("store: opening %s read-only: %w", path, err)
}

// Recover lets SQLite roll back an existing rollback journal under its own locks.
// It never migrates, changes journal mode, or removes journal files itself. With
// no journal it opens read-only, so repeating the command does not write.
// The returned journal names the file found, not a claim that any live transaction
// was rolled back: BEGIN IMMEDIATE waits for a live writer before proceeding.
func Recover(ctx context.Context, path string) (*Store, string, error) {
	journal := path + "-journal"
	if _, err := os.Stat(journal); errors.Is(err, os.ErrNotExist) {
		s, err := OpenRead(ctx, path)
		return s, "", err
	} else if err != nil {
		return nil, "", fmt.Errorf("store: inspecting journal: %w", err)
	}
	if _, err := os.Stat(path); err != nil {
		return nil, journal, err
	}
	db, err := sql.Open("sqlite", "file:"+path+"?mode=rw&_pragma=busy_timeout(5000)")
	if err != nil {
		return nil, journal, err
	}
	db.SetMaxOpenConns(1)
	conn, err := db.Conn(ctx)
	if err != nil {
		db.Close()
		return nil, journal, err
	}
	// Acquiring the write lock performs hot-journal recovery before a schema read.
	_, err = conn.ExecContext(ctx, `BEGIN IMMEDIATE`)
	if err == nil {
		_, err = conn.ExecContext(ctx, `ROLLBACK`)
	}
	conn.Close()
	if err != nil {
		db.Close()
		return nil, journal, fmt.Errorf("store: recovering %s: %w", journal, err)
	}
	return &Store{db: db, path: path}, journal, nil
}
