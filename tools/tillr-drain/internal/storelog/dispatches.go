// Architecture and invariants: docs/reference/tillr-drain-successor.md
package storelog

import (
	"database/sql"
	"errors"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/store"
)

// DispatchDDL is drain's core dispatches, seat, workflow selection, and node tables.
const DispatchDDL = `
CREATE TABLE IF NOT EXISTS dispatches (
  decision_id   TEXT PRIMARY KEY,
  item_id       TEXT NOT NULL,
  lane_id       TEXT,
  run_id        TEXT,
  action        TEXT NOT NULL,
  reason        TEXT NOT NULL,
  dispatched_at INTEGER NOT NULL,
  deferred_by   TEXT,
  seat          TEXT,
  pass_id       TEXT,
  backend       TEXT
);
CREATE TABLE IF NOT EXISTS seat_dispatches (
  ordinal INTEGER PRIMARY KEY AUTOINCREMENT,
  run_id TEXT NOT NULL UNIQUE,
  seat TEXT NOT NULL,
  backend TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS seat_dispatches_counts ON seat_dispatches(seat, backend);
CREATE INDEX IF NOT EXISTS seat_dispatches_order ON seat_dispatches(seat, ordinal);
CREATE TABLE IF NOT EXISTS seat_waiting_choices (
  run_id TEXT PRIMARY KEY,
  seat TEXT NOT NULL,
  backend TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS workflow_selections (
 item_id TEXT PRIMARY KEY,
 workflow TEXT NOT NULL,
 source TEXT NOT NULL,
 graph TEXT NOT NULL,
 judge_graph TEXT NOT NULL DEFAULT '',
 judge_attempt INTEGER NOT NULL DEFAULT 0
);
CREATE TABLE IF NOT EXISTS node_runs (
 item_id TEXT NOT NULL,
 attempt INTEGER NOT NULL,
 node_id TEXT NOT NULL,
 run_id TEXT NOT NULL UNIQUE,
 outcome TEXT NOT NULL,
 seat TEXT,
 model TEXT,
 quick_way INTEGER NOT NULL DEFAULT 0,
 PRIMARY KEY(item_id,attempt,node_id)
);
CREATE TABLE IF NOT EXISTS node_artifacts (
 item_id TEXT NOT NULL,
 attempt INTEGER NOT NULL,
 node_id TEXT NOT NULL,
 run_id TEXT NOT NULL,
 name TEXT NOT NULL,
 type TEXT NOT NULL,
 sha256 TEXT NOT NULL,
 location TEXT NOT NULL,
 PRIMARY KEY(item_id,attempt,node_id,name)
);
`

// DecisionCollisionError is a decision whose id the store already holds with other columns.
type DecisionCollisionError struct {
	*drainmodel.ExitError
	DecisionID, ItemID string
}

func (e *DecisionCollisionError) Unwrap() error { return e.ExitError }

// DispatchRow holds the scalar columns for one dispatches table insert or replay check.
type DispatchRow struct {
	DecisionID string
	ItemID     string
	LaneID     any
	RunID      any
	Action     string
	Reason     string
	DeferredBy any
	Seat       any
	PassID     string
	Backend    any
}

// InsertDispatchRow inserts row into dispatches, returning inserted=false when an
// identical row already exists, or *DecisionCollisionError when an existing row differs.
func InsertDispatchRow(tx *store.Tx, row DispatchRow, at time.Time) (inserted bool, err error) {
	var itemID, action, reason string
	var laneID, runID, deferredBy, seat, backend sql.NullString
	err = tx.QueryRow(`SELECT item_id, lane_id, run_id, action, reason, deferred_by, seat, backend
		FROM dispatches WHERE decision_id = ?`, row.DecisionID).
		Scan(&itemID, &laneID, &runID, &action, &reason, &deferredBy, &seat, &backend)
	if err == nil {
		if itemID == row.ItemID && action == row.Action && reason == row.Reason &&
			nullColumn(laneID) == row.LaneID && nullColumn(runID) == row.RunID &&
			nullColumn(deferredBy) == row.DeferredBy && nullColumn(seat) == row.Seat &&
			(!backend.Valid || nullColumn(backend) == row.Backend) {
			return false, nil
		}
		return false, &DecisionCollisionError{
			ExitError:  drainmodel.Internalf("the decision %s is already recorded with different columns", row.DecisionID).(*drainmodel.ExitError),
			DecisionID: row.DecisionID,
			ItemID:     row.ItemID,
		}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, ReadFailure(err)
	}
	if _, err := tx.Exec(
		`INSERT INTO dispatches (decision_id, item_id, lane_id, run_id, action, reason, dispatched_at, deferred_by, seat, pass_id, backend)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		row.DecisionID, row.ItemID, row.LaneID, row.RunID,
		row.Action, row.Reason, at.UnixMilli(), row.DeferredBy, row.Seat, NullablePassID(row.PassID), row.Backend,
	); err != nil {
		return false, drainmodel.Internalf("recording the decision for %q: %v", row.ItemID, err)
	}
	return true, nil
}

// EnsureDispatchUnrecorded returns a usage error if decisionID is already in dispatches.
func EnsureDispatchUnrecorded(tx *store.Tx, decisionID, itemID string) error {
	var existing string
	err := tx.QueryRow(`SELECT decision_id FROM dispatches WHERE decision_id = ?`, decisionID).Scan(&existing)
	if err == nil {
		return drainmodel.Usagef("the decision %s is already recorded: a tick is run once, and re-running one would dispatch %q twice",
			decisionID, itemID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return ReadFailure(err)
	}
	return nil
}

// ReadFailure classifies a failed SQLite read as incompatible (missing table) or internal.
func ReadFailure(err error) error {
	if strings.Contains(err.Error(), "no such table") {
		return drainmodel.Incompatiblef("%v", err)
	}
	return drainmodel.Internalf("%v", err)
}

// NullablePassID returns nil for an empty pass id and id otherwise.
func NullablePassID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func nullColumn(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}
