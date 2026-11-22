package store

import (
	"context"
	"database/sql"
	"errors"
)

// ErrStalePlan means another connection committed between planning and writer
// admission. None of the plan was applied.
var ErrStalePlan = errors.New("store: write plan's read snapshot is stale")

// ApplyPlan derives an optional write from a read-only snapshot. A nil write
// never asks for writer admission. The non-nil write runs in its own transaction,
// only if the snapshot still describes the store; it should do only the work
// needed to apply the prepared changes.
func (s *Store) ApplyPlan(ctx context.Context, plan func(*Reader) (func(*Tx) error, error)) error {
	// Use a separate connection for the snapshot and its data_version witness.
	// data_version values are comparable only on the same connection, and do
	// not reflect writes made through that connection itself.
	db := s.sidecar
	if db == nil {
		var err error
		db, err = sql.Open("sqlite", dsn(s.path, true, s.busyTimeout))
		if err != nil {
			return err
		}
		defer db.Close()
	}
	conn, err := db.Conn(ctx)
	if err != nil {
		return err
	}
	defer conn.Close()

	const versionQuery = `PRAGMA data_version`
	var version int64
	var apply func(*Tx) error
	err = s.readTx(ctx, conn, func(rd *Reader) error {
		// This read establishes the snapshot used by the planner.
		if err := rd.QueryRow(versionQuery).Scan(&version); err != nil {
			return err
		}
		var err error
		apply, err = plan(rd)
		return err
	})
	if err != nil || apply == nil {
		return err
	}

	return s.WriteTx(ctx, func(tx *Tx) error {
		// The read transaction has ended, and the write lock now prevents
		// changes between the check on the witness and the first mutation.
		var current int64
		if err := conn.QueryRowContext(ctx, versionQuery).Scan(&current); err != nil {
			return err
		}
		if current != version {
			return ErrStalePlan
		}
		return apply(tx)
	})
}
