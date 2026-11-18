package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// DefaultGateBudget is the one place the gate's time budget is stated.
//
// It is a bound on the gate's own wall time for one tick — the sum of the gate programs the
// tick ran, and nothing else. Staging, dispatch (the lane's run) and landing are measured
// and recorded beside it, so a slow lane or a slow model is never read as a slow gate. The
// number is the full `just done` measured on 2026-11-14 (~310s) rounded up: a tick whose
// gate plans a comparable check set fits, and a tick that spends half again as long says so.
//
// The budget is stated here and nowhere else. It is not a policy value and not a flag: a
// caller that could choose it would be a second statement of the same number, and the item
// that asked for this one asked for a single place.
const DefaultGateBudget = 330 * time.Second

// PassTiming is one tick's phases, measured separately. It is the record the board reads
// for a tick's gate time and for the trend across ticks.
//
// The phases are the loop's, not the lane's: staging is worktree preparation,
// dispatch is the run itself, and gate and land are ordinary candidates' steps.
// A change to the judge runs its proof before publication and records both phases
// and its count in RunnerGateMS, RunnerLandMS, and RunnerGates instead. This keeps
// its whole cost out of the ordinary landing target without hiding a slow proof.
//
// GateBudgetMS is the budget the tick was measured against, carried with the measurement so
// a board reading an old row cannot compare it against a budget that has since moved
// without knowing which one applied that night.
type PassTiming struct {
	TickID         string
	AtMS           int64
	StageMS        int64
	DispatchMS     int64
	GateMS         int64
	LandMS         int64
	GateBudgetMS   int64
	OverBudget     bool
	EndMS          int64
	DecisionCounts map[string]int
	Applied        int
	// Gates distinguishes an unmeasured gate from a gate measured at zero milliseconds.
	Gates int
	// Runner proof is counted apart from ordinary landings, never hidden in their budget.
	RunnerGates  int
	RunnerGateMS int64
	RunnerLandMS int64
}

// IsOverBudget is whether the tick's gate time exceeded its budget. It is a method rather
// than a field so a zero value is honest: a tick with no gate has no overrun, and a caller
// cannot set the answer and the measurement to disagree.
func (t PassTiming) IsOverBudget() bool {
	return t.GateBudgetMS > 0 && t.GateMS > t.GateBudgetMS
}

// PassDDL is drain's second table: one row per tick, carrying the phases the loop measured.
//
// It is a table rather than an event because it is a measurement, not a transition: nothing
// about the store's state depends on a tick's gate time, and registering a kind for it would
// claim a transition that did not happen (the same reason plan asserts none). It is written
// by the loop and read by the board, which is what makes "the gate got slower" a number with
// a date instead of an impression.
const PassDDL = `
CREATE TABLE IF NOT EXISTS drain_passes (
  tick_id          TEXT PRIMARY KEY,
  started_at       INTEGER NOT NULL,
  stage_ms         INTEGER NOT NULL,
  dispatch_ms      INTEGER NOT NULL,
  gate_ms          INTEGER NOT NULL,
  land_ms          INTEGER NOT NULL,
  gate_budget_ms   INTEGER NOT NULL,
  gate_over_budget INTEGER NOT NULL,
  ended_at         INTEGER,
  decision_counts  TEXT,
  applied_count    INTEGER,
  gate_count       INTEGER,
  runner_gate_count INTEGER NOT NULL DEFAULT 0,
  runner_gate_ms    INTEGER NOT NULL DEFAULT 0,
  runner_land_ms    INTEGER NOT NULL DEFAULT 0
);
`

// legacyPassMigrationDDL retains the old table only as a migration source.
// The store inserts its schema version after adding registered columns. A BEFORE
// INSERT trigger copies the complete rows then, including on an already recorded
// version (INSERT OR IGNORE still fires it). This keeps migration in the store's
// opening transaction, without fabricating absent historical measurements.
// New measurements only write drain_passes; repeated opens never replace them.
//
// The trigger is created IF NOT EXISTS under a name of its own, and the older name, whose
// body predates the runner columns, is dropped. Dropping and re-creating one name on every
// migration changed SQLite's schema cookie every time, and an open reads that cookie to
// decide whether the schema is current (store.schemaApplied): each program's migration
// would have made every other program's schema look stale, and opens would have gone on
// taking the write lock to migrate. Dropping a trigger that is already gone changes
// nothing.
var legacyPassMigrationDDL = strings.Replace(PassDDL, "drain_passes", "drain_ticks", 1) + `
DROP TRIGGER IF EXISTS drain_copy_legacy_passes;
CREATE TRIGGER IF NOT EXISTS drain_copy_legacy_runner_passes
BEFORE INSERT ON schema_migrations BEGIN
 INSERT OR IGNORE INTO drain_passes
 (tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
  gate_budget_ms, gate_over_budget, ended_at, decision_counts, applied_count, gate_count,
  runner_gate_count, runner_gate_ms, runner_land_ms)
 SELECT tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
 gate_budget_ms, gate_over_budget, ended_at, decision_counts, applied_count, gate_count,
 runner_gate_count, runner_gate_ms, runner_land_ms
 FROM drain_ticks;
END;
`

// recordPassTiming writes phases under their correlation identity. Legacy tick
// measurements replace their row; independent loop examinations add measurements
// under the generation allocated by the pass that launched them.
//
// The write is deliberately not part of the tick's planning transaction: the phases are not
// known until the tick's runs and verifications have finished, and a tick that stopped
// partway through still measured the gate time it did spend. A row that cannot be written is
// reported and does not fail the tick — the measurement is introspection, and turning a
// failed record into a different stop would make the loop's stop disagree with what happened.
func recordPassTiming(ctx context.Context, storePath string, timing PassTiming) error {
	return recordAccumulatedPassTiming(ctx, storePath, &timing)
}

func recordAccumulatedPassTiming(ctx context.Context, storePath string, timing *PassTiming) error {
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return Incompatiblef("%v", err)
	}
	defer s.Close()
	counts, err := json.Marshal(timing.DecisionCounts)
	if err != nil {
		return err
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if timing.DecisionCounts == nil {
			// Asynchronous examinations add measurements to their originating pass,
			// never replace its decision counts or create a second pass row.
			_, err := tx.Exec(`UPDATE drain_passes SET dispatch_ms = dispatch_ms + ?,
			 gate_ms = gate_ms + ?, land_ms = land_ms + ?, gate_count = gate_count + ?,
			 gate_over_budget = (gate_budget_ms > 0 AND gate_ms + ? > gate_budget_ms),
			 runner_gate_count = runner_gate_count + ?, runner_gate_ms = runner_gate_ms + ?,
			 runner_land_ms = runner_land_ms + ?
			 WHERE tick_id = ?`, timing.DispatchMS, timing.GateMS, timing.LandMS, timing.Gates, timing.GateMS,
				timing.RunnerGates, timing.RunnerGateMS, timing.RunnerLandMS, timing.TickID)
			return err
		}
		_, err := tx.Exec(
			`INSERT INTO drain_passes (tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms, gate_budget_ms, gate_over_budget, ended_at, decision_counts, applied_count, gate_count, runner_gate_count, runner_gate_ms, runner_land_ms)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(tick_id) DO UPDATE SET
			   started_at = excluded.started_at, stage_ms = excluded.stage_ms,
			   dispatch_ms = excluded.dispatch_ms, gate_ms = excluded.gate_ms,
			   land_ms = excluded.land_ms, gate_budget_ms = excluded.gate_budget_ms,
			   gate_over_budget = excluded.gate_over_budget,
			   ended_at = excluded.ended_at, decision_counts = excluded.decision_counts,
			   applied_count = excluded.applied_count, gate_count = excluded.gate_count,
			   runner_gate_count = excluded.runner_gate_count, runner_gate_ms = excluded.runner_gate_ms,
			   runner_land_ms = excluded.runner_land_ms`,
			timing.TickID, timing.AtMS, timing.StageMS, timing.DispatchMS, timing.GateMS,
			timing.LandMS, timing.GateBudgetMS, boolInt(timing.OverBudget), timing.EndMS,
			string(counts), timing.Applied, timing.Gates, timing.RunnerGates, timing.RunnerGateMS, timing.RunnerLandMS,
		)
		if err != nil {
			return Internalf("recording the timing of tick %s: %v", timing.TickID, err)
		}
		return nil
	})
}

func aggregatePassTiming(ctx context.Context, storePath, id string) (PassTiming, error) {
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return PassTiming{}, err
	}
	defer s.Close()
	timing := PassTiming{TickID: id}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
		 gate_budget_ms, COALESCE(gate_count, 0), runner_gate_count, runner_gate_ms, runner_land_ms
		 FROM drain_passes WHERE tick_id = ?`, id).
			Scan(&timing.AtMS, &timing.StageMS, &timing.DispatchMS, &timing.GateMS,
				&timing.LandMS, &timing.GateBudgetMS, &timing.Gates,
				&timing.RunnerGates, &timing.RunnerGateMS, &timing.RunnerLandMS)
	})
	timing.OverBudget = timing.IsOverBudget()
	return timing, err
}

// recordPass adds an asynchronous examination's measurements to its originating pass,
// reports the gate time, and files an item when the gate exceeded its budget.
// The background context preserves measurements even after an operator stops the loop.
func (l *Loop) recordPass(timing *PassTiming) error {
	if timing.TickID == "" || (timing.Gates == 0 && timing.RunnerGates == 0 && !strings.HasPrefix(timing.TickID, "gen-")) {
		// Legacy once measurements omit ticks without gates; every applied loop
		// pass records its generation, even if it did not run a gate.
		return nil
	}
	timing.OverBudget = timing.IsOverBudget()
	ctx := context.Background()
	if err := recordAccumulatedPassTiming(ctx, l.cfg.Store, timing); err != nil {
		l.reportf("loop: the timing of tick %s was not recorded: %v\n", timing.TickID, err)
		return err
	}
	aggregate, err := aggregatePassTiming(ctx, l.cfg.Store, timing.TickID)
	if err != nil {
		l.reportf("loop: the aggregate timing of tick %s could not be read: %v\n", timing.TickID, err)
		return err
	}
	*timing = aggregate
	l.reportf("loop: tick %s gate %s of a %s budget (staging %s, dispatch %s, land %s)\n",
		timing.TickID, seconds(time.Duration(timing.GateMS)*time.Millisecond), seconds(time.Duration(timing.GateBudgetMS)*time.Millisecond),
		seconds(time.Duration(timing.StageMS)*time.Millisecond),
		seconds(time.Duration(timing.DispatchMS)*time.Millisecond),
		seconds(time.Duration(timing.LandMS)*time.Millisecond))
	if timing.RunnerGates > 0 {
		l.reportf("loop: tick %s runner changes %d gate %s land %s (outside ordinary budget)\n",
			timing.TickID, timing.RunnerGates, seconds(time.Duration(timing.RunnerGateMS)*time.Millisecond),
			seconds(time.Duration(timing.RunnerLandMS)*time.Millisecond))
	}
	if !timing.OverBudget {
		return nil
	}
	over := time.Duration(timing.GateMS-timing.GateBudgetMS) * time.Millisecond
	l.reportf("loop: tick %s gate overran its budget: %s against %s, over by %s\n",
		timing.TickID, seconds(time.Duration(timing.GateMS)*time.Millisecond), seconds(l.gateBudget), seconds(over))
	if err := fileGateRegression(ctx, l.cfg.Store, *timing); err != nil {
		l.reportf("loop: the gate's overrun item was not filed: %v\n", err)
		return err
	}
	l.reportf("loop: filed the gate's regression item %s\n",
		gateRegressionItemID(time.UnixMilli(timing.AtMS)))
	return nil
}

// boolInt is SQLite's integer spelling of a boolean, so the column's value is the same on
// every writer rather than whatever a driver happened to bind.
func boolInt(value bool) int {
	if value {
		return 1
	}
	return 0
}

// seconds is a duration as the report reads it: one decimal place, so a 71.6s gate and an
// 83.0s gate are distinguishable numbers rather than one rounded to 72 and 83.
func seconds(d time.Duration) string { return fmt.Sprintf("%.1fs", d.Seconds()) }

// gateRegressionItemID is the regression item's id: one item per day, so a night whose gate
// overran once files one ticket and a night that overran forty times files the same one
// rather than forty near-identical rows.
//
// The day is the tick's own clock, not the loop's: a test that injects a fixed clock gets one
// item, and a drain that crosses midnight gets one per day it ran slowly, which is the
// granularity an operator reads a regression at.
func gateRegressionItemID(at time.Time) string {
	return "gate-budget-" + at.UTC().Format("2006-01-02")
}

// gateRegressionItem is the item a gate overrun files: one item, whose criterion is the
// measurement that opened it. It is not derived from the seed document; the loop is the one
// component that knows the tick that overran, and the item names it.
func gateRegressionItem(timing PassTiming) map[string]any {
	at := time.UnixMilli(timing.AtMS).UTC()
	budget := time.Duration(timing.GateBudgetMS) * time.Millisecond
	over := time.Duration(timing.GateMS-timing.GateBudgetMS) * time.Millisecond
	return map[string]any{
		"item_id":  gateRegressionItemID(at),
		"title":    fmt.Sprintf("The gate exceeded its %s budget: %s on tick %s", seconds(budget), seconds(time.Duration(timing.GateMS)*time.Millisecond), timing.TickID),
		"body_ref": "docs/design/tillr-day2.md",
		"state":    "in-queue",
		"criteria": []map[string]string{{
			"criterion_id": "c1",
			"text": fmt.Sprintf("The gate's wall time is back under its budget: tick %s ran its gate set in %s against a %s budget (over by %s). The measurement is the gate program's own, recorded in drain_passes beside the staging, dispatch and land times.",
				timing.TickID, seconds(time.Duration(timing.GateMS)*time.Millisecond), seconds(budget), seconds(over)),
			"state": "",
		}},
		"blocked_by": []string{},
		// The fence is the gate's own check set: the planner's declarations, the gate's
		// registry and code checks, and the tests that pin them. A fix for a slow gate lives
		// there and nowhere else, so the item is dispatchable without admitting the tree.
		"allowed_paths": []string{"tools/fork-check.py", "tools/tillr-gate/", "tests/"},
		"design_doc":    "docs/design/tillr-day2.md",
	}
}

// fileGateRegression files the regression item unless the store already holds it.
//
// The existence check is the point rather than an optimisation: item.Apply is an upsert, so
// re-applying a tick's item would rewrite a landed or blocked item's state back to
// in-queue — the resurrection the seed write-back item calls out — and a night of overruns
// would bury every other row. One item per day, filed once, is the ticket; the trend is the
// table.
func fileGateRegression(ctx context.Context, storePath string, timing PassTiming) error {
	at := time.UnixMilli(timing.AtMS).UTC()
	// The item id and the record id are two different ids: the record the face would print is
	// `item-<item id>`, and the existence check must ask about the item's own id. Asking with
	// the record id matches nothing and re-applies the item every tick, which is the
	// resurrection this function exists to prevent.
	itemID := gateRegressionItemID(at)
	payload, err := json.Marshal(gateRegressionItem(timing))
	if err != nil {
		return Internalf("encoding the gate's regression item: %v", err)
	}
	env := record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          item.RecordKind,
		ID:            "item-" + itemID,
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     at,
		Payload:       payload,
	}
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return Incompatiblef("%v", err)
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		var seen int
		if err := tx.QueryRow(`SELECT count(*) FROM items WHERE item_id = ?`, itemID).Scan(&seen); err != nil {
			return Internalf("reading whether %s is already filed: %v", itemID, err)
		}
		if seen > 0 {
			return nil
		}
		if _, err := item.Apply(ctx, tx, env); err != nil {
			return Internalf("filing the gate's regression item: %v", err)
		}
		return nil
	})
}
