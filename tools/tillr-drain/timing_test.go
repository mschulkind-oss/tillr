package main_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

// slowerGate is a gate program that sleeps long enough to be measured. It exits 0, so the
// candidate passes and the tick reaches its landing step: the case is about the gate's time,
// not a verdict.
const slowerGate = counting + "sleep 0.2\nexit 0\n"

// tickRow is one drain_passes row as the board reads it.
type tickRow struct {
	passID       string
	startedAtMS  int64
	stageMS      int64
	dispatchMS   int64
	gateMS       int64
	landMS       int64
	gateBudgetMS int64
	overBudget   bool
}

// readTicks answers every drain_passes row, oldest first. The order is by the row's own key,
// not by started_at: a test's injected clock is one instant for every tick, and the row's
// identity is the tick's, which is what the board's trend orders by when the clock repeats.
func readTicks(t *testing.T, path string) []tickRow {
	t.Helper()
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatalf("opening the store failed: %v", err)
	}
	defer s.Close()
	var rows []tickRow
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		got, err := r.Query(`SELECT tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
			gate_budget_ms, gate_over_budget FROM drain_passes ORDER BY started_at, tick_id`)
		if err != nil {
			return err
		}
		defer got.Close()
		for got.Next() {
			var row tickRow
			var over int
			if err := got.Scan(&row.passID, &row.startedAtMS, &row.stageMS, &row.dispatchMS,
				&row.gateMS, &row.landMS, &row.gateBudgetMS, &over); err != nil {
				return err
			}
			row.overBudget = over != 0
			rows = append(rows, row)
		}
		return got.Err()
	})
	if err != nil {
		t.Fatalf("reading drain_passes failed: %v", err)
	}
	return rows
}

// tickFor follows the gate judgment's generation to its timing row; ids no
// longer embed item names or clock readings.
func tickFor(t *testing.T, path string, rows []tickRow, itemID string) tickRow {
	t.Helper()
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var generation string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT json_extract(payload, '$.tick_id') FROM events WHERE kind = ? AND json_extract(payload, '$.item_id') = ? ORDER BY seq DESC LIMIT 1`, drain.GatePlanKind, itemID).Scan(&generation)
	}); err != nil {
		t.Fatal(err)
	}
	for _, row := range rows {
		if row.passID == generation {
			return row
		}
	}
	t.Fatalf("no timing row for %s's generation %q: %+v", itemID, generation, rows)
	return tickRow{}
}

// readItemsByName answers every item the store holds, by id.
func readItemsByName(t *testing.T, path string) map[string]struct{ title, state string } {
	t.Helper()
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatalf("opening the store failed: %v", err)
	}
	defer s.Close()
	out := map[string]struct{ title, state string }{}
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT item_id, title, state FROM items`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id, title, state string
			if err := rows.Scan(&id, &title, &state); err != nil {
				return err
			}
			out[id] = struct{ title, state string }{title, state}
		}
		return rows.Err()
	})
	if err != nil {
		t.Fatalf("reading items failed: %v", err)
	}
	return out
}

// budgetLoop builds a one-lane loop with the gate budget the case names, reporting to the
// writer it was given. A zero budget reads the one place the budget is stated.
func budgetLoop(t *testing.T, f loopFixture, steps *drain.ProcessSteps, report *strings.Builder, budget time.Duration) *drain.Loop {
	t.Helper()
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{laneName(1)},
		Policy: f.policy, Steps: &evidenceProcessSteps{steps}, Stage: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}},
		Now: steps.Now, Report: report, GateBudget: budget,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	return loop
}

// A tick records the gate's own wall time — the gate program's, not the tick's and not the
// lane's — and a tick whose gate run exceeds its budget says so in the loop's own output.
// The budget is a reported target, never a hold, so the overrun files no item.
func TestTheTickRecordsTheGateTimeAndReportsAnOverrun(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_061", "in-queue"))
	steps := f.steps(t, stub(t, "slow-gate", slowerGate), always(t, "land", 0), always(t, "verdict", 0))
	var report strings.Builder
	loop := budgetLoop(t, f, steps, &report, time.Millisecond)

	if _, err := loop.Run(context.Background()); err != nil {
		t.Fatalf("the loop failed: %v", err)
	}

	ticks := readTicks(t, f.store)
	if len(ticks) == 0 {
		t.Fatalf("the loop recorded no tick")
	}
	tick := tickFor(t, f.store, ticks, "item_061")
	// The gate slept 200ms; the measurement is the program's own span, so it cannot read
	// less than that. It is not the tick's total: dispatch ran a lane and landing ran a
	// program, and the row keeps them apart.
	if tick.gateMS < 150 {
		t.Fatalf("the tick recorded %dms of gate time for a gate that slept 200ms: %+v", tick.gateMS, tick)
	}
	var dispatchMS int64
	for _, pass := range ticks {
		dispatchMS += pass.dispatchMS
	}
	if dispatchMS <= 0 || tick.dispatchMS != 0 {
		t.Fatalf("dispatch must be measured on the dispatch pass, not the judging pass: sum %dms judge %dms; rows %+v\n%s", dispatchMS, tick.dispatchMS, ticks, report.String())
	}
	if tick.landMS < 0 || tick.stageMS < 0 {
		t.Fatalf("a phase was recorded negative: %+v", tick)
	}
	if tick.gateBudgetMS != 1 {
		t.Fatalf("the tick recorded a budget of %dms, want the 1ms the case set", tick.gateBudgetMS)
	}
	if !tick.overBudget {
		t.Fatalf("the tick recorded over_budget = false for %dms against a 1ms budget", tick.gateMS)
	}
	if !strings.Contains(report.String(), "overran its budget") {
		t.Fatalf("the loop's own report does not name the overrun:\n%s", report.String())
	}

	assertNoGateBudgetItem(t, f.store)
	if strings.Contains(report.String(), "regression item") {
		t.Fatalf("the loop's report names a filed regression item:\n%s", report.String())
	}
}

// assertNoGateBudgetItem fails when the store holds an item the gate budget filed: a budget
// is a target the board reports, and an item for it is a ticket nobody asked for.
func assertNoGateBudgetItem(t *testing.T, path string) {
	t.Helper()
	for id := range readItemsByName(t, path) {
		if strings.HasPrefix(id, "gate-budget-") {
			t.Fatalf("the gate budget filed item %s; an overrun is reported, never filed", id)
		}
	}
}

// The budget is stated in one place: a loop that names none measures against
// DefaultGateBudget, and nothing else in the loop has a second number.
func TestALoopWithNoBudgetReadsTheOneStatement(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_062", "in-queue"))
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	var report strings.Builder
	loop := budgetLoop(t, f, steps, &report, 0)

	if _, err := loop.Run(context.Background()); err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	tick := tickFor(t, f.store, readTicks(t, f.store), "item_062")
	if tick.gateBudgetMS != drain.DefaultGateBudget.Milliseconds() {
		t.Fatalf("the tick measured against %dms, want DefaultGateBudget's %dms",
			tick.gateBudgetMS, drain.DefaultGateBudget.Milliseconds())
	}
	if tick.overBudget {
		t.Fatalf("a 0ms gate overran a %dms budget", tick.gateBudgetMS)
	}
}

// A night of overruns is a trend on the board, not a ticket: two ticks whose gates both
// overran record their overruns and file nothing.
func TestOverrunningTicksFileNoItem(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store,
		itemRecord("item_063", "in-queue"),
		itemRecord("item_064", "in-queue"),
	)
	gate := stub(t, "slow-gate", slowerGate)
	steps := f.steps(t, gate, always(t, "land", 0), always(t, "verdict", 0))
	var report strings.Builder
	loop := budgetLoop(t, f, steps, &report, time.Millisecond)

	if _, err := loop.Run(context.Background()); err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	ticks := readTicks(t, f.store)
	if len(ticks) < 2 {
		t.Fatalf("the loop recorded %d tick(s), want at least the two that gated items: %+v", len(ticks), ticks)
	}
	if !tickFor(t, f.store, ticks, "item_063").overBudget || !tickFor(t, f.store, ticks, "item_064").overBudget {
		t.Fatalf("a tick did not record its overrun: %+v", ticks)
	}
	assertNoGateBudgetItem(t, f.store)
}
