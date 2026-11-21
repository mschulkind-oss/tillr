package store

import (
	"context"
	"database/sql"
	"fmt"
)

// SchemaVersion is the schema this package migrates a store to.
//
// Version 6 replaces timed judge grants with persisted evaluator steps.
//
// It was 5 because the item record gained rank — the queue's order, which the drain's
// planner sorts by — on a table that already existed. A store at 4 predates it, and a read
// face that selected it would answer a SQL error instead of naming the incompatibility;
// the repair is migrate's addMissingColumns, as below.
//
// It was 4 because the run record gained its spend — usage_tokens and usage_cost_usd, the
// two numbers the drain loop's ceiling reads — on a table that already existed. A store
// at 3 predates them, and a read face that selected them would answer a SQL error instead
// of naming the incompatibility; the repair is migrate's addMissingColumns, as below.
//
// It was 3 because the item record gained allowed_paths — the fence a brief is staged from —
// on a table that already existed, and the version is what a face that cannot migrate (a
// read-only open, or the store CLI's gate before it writes) decides compatibility from: an
// item read face selects the column, so a store at 2 would answer a SQL error instead of
// naming the incompatibility. Two shapes cannot both report one number, and each earlier
// bump carries the same reason: 2 is land's landing_attempts gaining gated_tree,
// landed_tree and carried, so a store left at 1 predates those columns and a store at 2
// predates this one.
//
// The repair does not read this number. migrate() adds every registered column a store
// lacks on each create-open (addMissingColumns), so a store at 1 is repaired by the
// first write-open and recorded as 2 by that same transaction; the version tells the
// faces that never migrate the truth, and the hook is what makes such a store
// writable.
const SchemaVersion = 6

// MetaDDL is the store's own administration: the meta rows a store is described by
// and the record of which migrations have been applied.
const MetaDDL = `
CREATE TABLE IF NOT EXISTS meta (
  key   TEXT PRIMARY KEY,
  value TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS schema_migrations (
  version    INTEGER PRIMARY KEY,
  applied_at INTEGER NOT NULL
);
`

// migrationsExec is the statement surface the migration runs on: the connection
// open.go's migrate has begun IMMEDIATE. It is not *sql.Tx because database/sql cannot
// ask for an immediate begin — the driver takes that from the handle's dsn — and the
// migration is the one transaction in the store that has to have the write lock before it
// reads (see that function).
type migrationsExec interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	ExecContext(ctx context.Context, query string, args ...any) (sql.Result, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// migrate applies the DDL list, adds the columns a store is missing, and records the
// version, in one transaction so a half-migrated store cannot exist. The list is
// composed from the per-owner registrations (registry.go), so a utility's tables are
// applied because the utility registered them and no central list is edited to add
// one.
func migrate(ctx context.Context, tx migrationsExec) error {
	// The caller already holds the writer lock. Refuse a newer schema before
	// applying even the first DDL; a separate preflight read would race another
	// writer's migration. A fresh store has no version table yet.
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master WHERE type='table' AND name='schema_migrations'`).Scan(&exists); err != nil {
		return fmt.Errorf("store: checking schema compatibility: %w", err)
	}
	var version int
	if exists != 0 {
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(version),0) FROM schema_migrations`).Scan(&version); err != nil {
			return fmt.Errorf("store: reading schema compatibility: %w", err)
		}
		if version > SchemaVersion {
			return fmt.Errorf("store: schema version %d is newer than supported version %d", version, SchemaVersion)
		}
	}
	for _, ddl := range migrations() {
		if _, err := tx.ExecContext(ctx, ddl); err != nil {
			return fmt.Errorf("store: applying schema: %w", err)
		}
	}
	// After the DDL list, so a registered column's table exists by the time it is
	// probed: a column registration names a table its own owner's DDL creates. A
	// registration naming a table nobody creates fails the migration rather than being
	// skipped, which is the registration defect it is.
	if err := addMissingColumns(ctx, tx); err != nil {
		return err
	}
	if version < 6 {
		if err := migrateJudgeSteps(ctx, tx); err != nil {
			return fmt.Errorf("store: migrating judge steps: %w", err)
		}
	}
	if _, err := tx.ExecContext(ctx,
		`INSERT OR IGNORE INTO schema_migrations (version, applied_at) VALUES (?, unixepoch('subsec') * 1000)`,
		SchemaVersion,
	); err != nil {
		return fmt.Errorf("store: recording schema version: %w", err)
	}
	return nil
}

// addMissingColumns adds each registered column the store's table does not have. It
// exists because CREATE TABLE IF NOT EXISTS cannot: a table that already exists keeps
// its shape, so a column added to an existing table is in every new store and none of
// the old ones.
//
// The probe is what makes it idempotent — the migration runs on every create-open, so a
// second open of the same store must add nothing — and the transaction is what keeps a
// repair from being half-applied: a column this fails on takes the whole migration with
// it, so no store is left at a version whose shape it does not have.
func addMissingColumns(ctx context.Context, tx migrationsExec) error {
	for _, column := range columns() {
		exists, err := columnExists(ctx, tx, column.table, column.column)
		if err != nil {
			return err
		}
		if exists {
			continue
		}
		// The declaration is the owner's DDL fragment, not data: SQL has no parameter
		// position in ALTER TABLE, which is why it is registered as a string.
		stmt := fmt.Sprintf("ALTER TABLE %s ADD COLUMN %s %s", column.table, column.column, column.decl)
		if _, err := tx.ExecContext(ctx, stmt); err != nil {
			return fmt.Errorf("store: adding column %s.%s: %w", column.table, column.column, err)
		}
	}
	return nil
}

// columnExists reports whether table has column, through the pragma the schema itself
// answers. Both names are bound as arguments rather than quoted into the statement.
func columnExists(ctx context.Context, tx migrationsExec, table, column string) (bool, error) {
	var n int
	if err := tx.QueryRowContext(ctx,
		`SELECT count(*) FROM pragma_table_info(?) WHERE name = ?`, table, column,
	).Scan(&n); err != nil {
		return false, fmt.Errorf("store: probing %s for column %s: %w", table, column, err)
	}
	return n > 0, nil
}
