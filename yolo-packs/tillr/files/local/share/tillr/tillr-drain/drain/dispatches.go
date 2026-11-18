package drain

import (
	"database/sql"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// DispatchDDL is drain's table: design §5.1's logical ownership, `dispatches`.
//
// A row is one decision's record — which item, which lane, which run the request named,
// and the action and reason — and it is written by `once` and by the loop's own plan. It is
// not the item→run link the planner reads: that is `runs.item_id` (D-UT19), because a
// decision's request exists before the run does. `plan` itself writes nothing anywhere, so
// a decision face can be pointed at any store and answer without leaving a mark.
//
// A row records the decision, not the run: run_id is the request the runner was handed,
// so it exists before `run exec` has run anything, and the row answers "which run was
// dispatched for this item" from the moment of dispatch. `action` and `reason` are the
// decision's own, so a hold is recorded as the answer it was — a table of dispatches
// alone could not say why an item was not dispatched.
//
// No foreign key, for the reason land gives its candidates: items and lanes are other
// utilities' tables, and a foreign key would make drain's schema depend on a table drain
// does not own. lane_id and run_id are NULL on a hold, because a hold assigned no lane and
// requested no run.
//
// deferred_by is the one field a hold needs besides its reason: a `fence-overlap` hold is a
// deferral behind a named item, and the row is the loop's durable record of the decision
// (`plan` writes no row), so the name goes here as well as in the decision payload. It is
// NULL for every other decision, and it is nullable on purpose: a store created before the
// column existed gains it through addMissingColumns, and a reader that inserted the old
// column list still writes a valid row.
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
CREATE TABLE IF NOT EXISTS workflow_selections (
 item_id TEXT PRIMARY KEY,
 workflow TEXT NOT NULL,
 source TEXT NOT NULL,
 graph TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS node_runs (
 item_id TEXT NOT NULL,
 attempt INTEGER NOT NULL,
 node_id TEXT NOT NULL,
 run_id TEXT NOT NULL UNIQUE,
 outcome TEXT NOT NULL,
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

// decisionCollisionError is a decision whose id the store already holds with other columns.
// It keeps the internal exit code a one-shot face answers, and the loop reads it as the
// item's condition (applyPlanGeneration).
type decisionCollisionError struct {
	*ExitError
	DecisionID, ItemID string
}

func (e *decisionCollisionError) Unwrap() error { return e.ExitError }

// recordPassDecision inserts an immutable decision in the tick's transaction. A replay
// of the same payload is a no-op; its first recording time stays intact. Reusing an
// identity for different columns is an error, never permission to overwrite history.
func recordPassDecision(tx *store.Tx, decision Decision, at time.Time) error {
	columns := decisionColumns(decision)
	var itemID, action, reason string
	var laneID, runID, deferredBy, seat, backend sql.NullString
	err := tx.QueryRow(`SELECT item_id, lane_id, run_id, action, reason, deferred_by, seat, backend
		FROM dispatches WHERE decision_id = ?`, decision.DecisionID).
		Scan(&itemID, &laneID, &runID, &action, &reason, &deferredBy, &seat, &backend)
	if err == nil {
		if itemID == decision.ItemID && action == decision.Action && reason == decision.Reason &&
			nullColumn(laneID) == columns.laneID && nullColumn(runID) == columns.runID &&
			nullColumn(deferredBy) == columns.deferredBy && nullColumn(seat) == columns.seat && (!backend.Valid || nullColumn(backend) == columns.backend) {
			// Pre-share rows have no backend column value. Preserve their replay
			// semantics without inventing a historical choice.
			return nil
		}
		return &decisionCollisionError{ExitError: Internalf("the decision %s is already recorded with different columns", decision.DecisionID).(*ExitError), DecisionID: decision.DecisionID, ItemID: decision.ItemID}
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return readFailure(err)
	}
	if _, err := tx.Exec(
		`INSERT INTO dispatches (decision_id, item_id, lane_id, run_id, action, reason, dispatched_at, deferred_by, seat, pass_id, backend)
		 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		decision.DecisionID, decision.ItemID, columns.laneID, columns.runID,
		decision.Action, decision.Reason, at.UnixMilli(), columns.deferredBy, columns.seat, nullablePassID(decision.PassID), columns.backend,
	); err != nil {
		return Internalf("recording the decision for %q: %v", decision.ItemID, err)
	}
	if decision.Action == ActionDispatch || decision.Action == ActionRetry {
		for _, node := range parallelNodes(decision) {
			if err := recordSeatDispatch(tx, node); err != nil {
				return err
			}
		}
	}
	// Every dispatch fact names its build: a decision with no Inputs (a design-seat or
	// owner-question dispatch) records the planning build alone, so build migration does not
	// read its run as an older build's (migrateBuildRuns).
	inputs := decision.Inputs
	if inputs == nil && decision.DispatchBuild != "" {
		inputs = &RunInputs{TillrBuild: decision.DispatchBuild}
	}
	if inputs != nil && decision.RunRequest != nil && (decision.Action == ActionDispatch || decision.Action == ActionRetry) {
		for _, node := range parallelNodes(decision) {
			node.Inputs = inputs
			if err := appendEffect(tx, inputRecordedKind, node.RunRequest.RunID, node, at); err != nil {
				return err
			}
		}
	}
	return nil
}

func nullColumn(value sql.NullString) any {
	if value.Valid {
		return value.String
	}
	return nil
}

// dispatchColumns is one decision's nullable columns as SQL arguments: a hold has no lane
// and no run, only a fence deferral names the item it waits behind, and only a seat item
// names a seat. They are nullable together so a row is a complete account of the decision
// whether it dispatched anything or not.
type dispatchColumns struct {
	laneID, runID, deferredBy, seat, backend any
}

// decisionColumns answers the row's nullable columns: a hold has no lane and no run, only a
// fence deferral names the item it waits behind, and only an item that asked for a seat
// carries one. The seat is recorded on the row and not only in the decision payload because
// the row is the store's durable account of the dispatch, and it is what explains the
// backend the run reached without the policy that was live at dispatch time.
func decisionColumns(decision Decision) dispatchColumns {
	var columns dispatchColumns
	if decision.LaneID != "" {
		columns.laneID = decision.LaneID
	}
	if decision.RunRequest != nil {
		columns.runID = decision.RunRequest.RunID
		if decision.RunRequest.Backend != "" {
			columns.backend = decision.RunRequest.Backend
		}
	}
	if decision.DeferredBy != "" {
		columns.deferredBy = decision.DeferredBy
	}
	if decision.Seat != "" {
		columns.seat = decision.Seat
	}
	return columns
}

// init registers drain's DDL from drain's own package: the store composes its migration
// list from what was registered, and a registration a face remembers to make is one a
// face can forget.
//
// Only the DDL is registered here. drain registers no record→row mapping: these rows are
// written by `once` from its own transaction rather than materialized from a record. The
// event kinds drain appends are the loop's judgment events, registered in events.go;
// `drain.decision` is not one of them, because plan asserts no transition.
func init() {
	// Drain's tables are registered as one owner's contribution: the registry refuses a
	// second DDL for an owner already registered, so the tick-timing and retry tables ride
	// the same statement. A store created before drain_passes or retries existed gains each
	// on its next write-open, which is what the migration's CREATE TABLE IF NOT EXISTS is
	// for.
	if err := store.RegisterDDL("drain", DispatchDDL+graphRevisionDDL+PassDDL+passSpendDDL+legacyPassMigrationDDL+SessionDDL+RetryDDL+GateSpanDDL+rollingDDL+trunkHistoryDDL+generationDDL+backendWindowDDL+backendQuotaDDL+backendOutcomesDDL); err != nil {
		panic(err)
	}
	for name, definition := range map[string]string{"loop_owner": "TEXT", "stop_requested": "INTEGER"} {
		if err := store.RegisterColumn("drain", "drain_sessions", name, definition); err != nil {
			panic(err)
		}
	}
	for name, definition := range map[string]string{
		"ended_at": "INTEGER", "decision_counts": "TEXT", "applied_count": "INTEGER",
		"gate_count":        "INTEGER",
		"gate_max_ms":       "INTEGER NOT NULL DEFAULT 0",
		"runner_gate_count": "INTEGER NOT NULL DEFAULT 0",
		"runner_gate_ms":    "INTEGER NOT NULL DEFAULT 0",
		"runner_land_ms":    "INTEGER NOT NULL DEFAULT 0",
	} {
		for _, table := range []string{"drain_ticks", "drain_passes"} {
			if err := store.RegisterColumn("drain", table, name, definition); err != nil {
				panic(err)
			}
		}
	}
	if err := store.RegisterColumn("drain", "dispatches", "pass_id", "TEXT"); err != nil {
		panic(err)
	}
	// deferred_by was added to a table that already existed, so a store created before it
	// keeps the old shape under CREATE TABLE IF NOT EXISTS and gains it here.
	if err := store.RegisterColumn("drain", "dispatches", "deferred_by", "TEXT"); err != nil {
		panic(err)
	}
	// seat is the same kind of addition: the dispatch's durable account of which seat the
	// item asked for, added to a table that predates it by the additive-column hook.
	if err := store.RegisterColumn("drain", "dispatches", "backend", "TEXT"); err != nil {
		panic(err)
	}
	if err := store.RegisterColumn("drain", "dispatches", "seat", "TEXT"); err != nil {
		panic(err)
	}
}

// recordDecision writes one decision's row inside the tick's transaction.
func recordDecision(tx *store.Tx, decision Decision, at time.Time) error {
	if decision.Action == ActionHold {
		return recordPassDecision(tx, decision, at)
	}
	// A decision id already in the table means this tick has been run before. Recording
	// it again would put a second run on an item one is already dispatched to, so the
	// face refuses the tick instead: a replayed tick is the caller's mistake, not a
	// second dispatch.
	var existing string
	err := tx.QueryRow(`SELECT decision_id FROM dispatches WHERE decision_id = ?`, decision.DecisionID).Scan(&existing)
	if err == nil {
		return Usagef("the decision %s is already recorded: a tick is run once, and re-running one would dispatch %q twice",
			decision.DecisionID, decision.ItemID)
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return readFailure(err)
	}

	return recordPassDecision(tx, decision, at)
}
