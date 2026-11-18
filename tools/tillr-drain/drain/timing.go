package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// DefaultGateBudget is the one place the gate's time budget is stated.
//
// It is a bound on ONE gate run's own wall time, and nothing else. Staging, dispatch (the
// lane's run) and landing are measured and recorded beside it, so a slow lane or a slow
// model is never read as a slow gate. The number is the full `just done` measured on
// 2026-11-14 (~310s) rounded up: one run that plans a comparable check set fits, and a run
// that spends half again as long says so.
//
// A pass may gate several candidates, so it is compared by its longest run (GateMaxMS), never
// by the sum of its runs (GateMS): the sum is the pass's cost and is reported beside it, and a
// sum against a per-run number reads three ordinary gates as an overrun.
//
// The budget is a reported target, never a hold: an overrun is a report line and the board's
// flag, and it files no item.
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
	TickID     string
	AtMS       int64
	StageMS    int64
	DispatchMS int64
	GateMS     int64
	// GateMaxMS is the longest single ordinary gate run, the measurement the per-run budget
	// is compared against.
	GateMaxMS      int64
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

// IsOverBudget is whether one of the tick's gate runs exceeded the per-run budget. It is a
// method rather than a field so a zero value is honest: a tick with no gate has no overrun,
// and a caller cannot set the answer and the measurement to disagree.
func (t PassTiming) IsOverBudget() bool {
	return t.GateBudgetMS > 0 && t.GateMaxMS > t.GateBudgetMS
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
  gate_max_ms      INTEGER NOT NULL DEFAULT 0,
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
			 gate_max_ms = MAX(COALESCE(gate_max_ms, 0), ?),
			 gate_over_budget = (gate_budget_ms > 0 AND MAX(COALESCE(gate_max_ms, 0), ?) > gate_budget_ms),
			 runner_gate_count = runner_gate_count + ?, runner_gate_ms = runner_gate_ms + ?,
			 runner_land_ms = runner_land_ms + ?
			 WHERE tick_id = ?`, timing.DispatchMS, timing.GateMS, timing.LandMS, timing.Gates, timing.GateMaxMS, timing.GateMaxMS,
				timing.RunnerGates, timing.RunnerGateMS, timing.RunnerLandMS, timing.TickID)
			return err
		}
		_, err := tx.Exec(
			`INSERT INTO drain_passes (tick_id, started_at, stage_ms, dispatch_ms, gate_ms, gate_max_ms, land_ms, gate_budget_ms, gate_over_budget, ended_at, decision_counts, applied_count, gate_count, runner_gate_count, runner_gate_ms, runner_land_ms)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
			 ON CONFLICT(tick_id) DO UPDATE SET
			   started_at = excluded.started_at, stage_ms = excluded.stage_ms,
			   dispatch_ms = excluded.dispatch_ms, gate_ms = excluded.gate_ms, gate_max_ms = excluded.gate_max_ms,
			   land_ms = excluded.land_ms, gate_budget_ms = excluded.gate_budget_ms,
			   gate_over_budget = excluded.gate_over_budget,
			   ended_at = excluded.ended_at, decision_counts = excluded.decision_counts,
			   applied_count = excluded.applied_count, gate_count = excluded.gate_count,
			   runner_gate_count = excluded.runner_gate_count, runner_gate_ms = excluded.runner_gate_ms,
			   runner_land_ms = excluded.runner_land_ms`,
			timing.TickID, timing.AtMS, timing.StageMS, timing.DispatchMS, timing.GateMS, timing.GateMaxMS,
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
		return r.QueryRow(`SELECT started_at, stage_ms, dispatch_ms, gate_ms, COALESCE(gate_max_ms, 0), land_ms,
		 gate_budget_ms, COALESCE(gate_count, 0), runner_gate_count, runner_gate_ms, runner_land_ms
		 FROM drain_passes WHERE tick_id = ?`, id).
			Scan(&timing.AtMS, &timing.StageMS, &timing.DispatchMS, &timing.GateMS, &timing.GateMaxMS,
				&timing.LandMS, &timing.GateBudgetMS, &timing.Gates,
				&timing.RunnerGates, &timing.RunnerGateMS, &timing.RunnerLandMS)
	})
	timing.OverBudget = timing.IsOverBudget()
	return timing, err
}

// recordPass adds an asynchronous examination's measurements to its originating pass and
// reports the gate time. An overrun is reported and flagged on the pass row for the board;
// it files nothing, because a budget is a reported target, never a hold.
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
	wasOver := timing.OverBudget
	*timing = aggregate
	l.reportf("loop: tick %s gate %s over %d run(s), longest %s of a %s per-run budget (staging %s, dispatch %s, land %s)\n",
		timing.TickID, seconds(time.Duration(timing.GateMS)*time.Millisecond), timing.Gates,
		seconds(time.Duration(timing.GateMaxMS)*time.Millisecond), seconds(time.Duration(timing.GateBudgetMS)*time.Millisecond),
		seconds(time.Duration(timing.StageMS)*time.Millisecond),
		seconds(time.Duration(timing.DispatchMS)*time.Millisecond),
		seconds(time.Duration(timing.LandMS)*time.Millisecond))
	if timing.RunnerGates > 0 {
		l.reportf("loop: tick %s runner changes %d gate %s land %s (outside ordinary budget)\n",
			timing.TickID, timing.RunnerGates, seconds(time.Duration(timing.RunnerGateMS)*time.Millisecond),
			seconds(time.Duration(timing.RunnerLandMS)*time.Millisecond))
	}
	if !wasOver {
		// Only the measurement that overran reports it, so a pass whose later runs are
		// ordinary does not repeat the line once per run.
		return nil
	}
	over := time.Duration(timing.GateMaxMS-timing.GateBudgetMS) * time.Millisecond
	l.reportf("loop: tick %s gate run overran its budget: %s against %s, over by %s\n",
		timing.TickID, seconds(time.Duration(timing.GateMaxMS)*time.Millisecond), seconds(time.Duration(timing.GateBudgetMS)*time.Millisecond), seconds(over))
	return nil
}

// GateBudgetItemPrefix is the id prefix of the overrun alarm an older build filed, one per
// day (`gate-budget-2026-11-18`). The loop files none now; the prefix remains so the alarms
// already in a store are recognised as generated and closed (releaseGeneratedTriage).
const GateBudgetItemPrefix = "gate-budget-"

// IsGateBudgetItem answers whether an item is one of those alarms.
func IsGateBudgetItem(itemID string) bool { return strings.HasPrefix(itemID, GateBudgetItemPrefix) }

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
