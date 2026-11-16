package main_test

import (
	"context"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-item/item"
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
		Policy: f.policy, Steps: steps, Stage: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}},
		Now: steps.Now, Report: report, GateBudget: budget,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	return loop
}

// A tick records the gate's own wall time — the gate program's, not the tick's and not the
// lane's — and a tick whose gate exceeds its budget says so in the loop's own output and
// files one item for the regression.
func TestTheTickRecordsTheGateTimeAndFilesAnOverrun(t *testing.T) {
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

	// One item for the regression, visible on the board the same night. Its state is read at
	// filing by the drain-package case; here the loop may already have dispatched and landed
	// it, because the stub stager admits the stub adapter's candidate.
	items := readItemsByName(t, f.store)
	filed, ok := items["gate-budget-2026-11-12"]
	if !ok {
		t.Fatalf("no gate-budget item was filed; the store holds %v", items)
	}
	if !strings.Contains(filed.title, "budget") {
		t.Fatalf("the filed item's title does not name the budget: %q", filed.title)
	}
	if !strings.Contains(report.String(), "filed the gate's regression item gate-budget-2026-11-12") {
		t.Fatalf("the loop's report does not name the filed item:\n%s", report.String())
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

// A night of overruns files one item, not one per tick: the trend is the table, the ticket is
// the regression. Two items on two ticks, both gates over their budget, one item filed.
func TestTwoOverrunningTicksFileOneRegressionItem(t *testing.T) {
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
	// One row per tick that gated anything, and the two items' ticks are the first two the
	// store holds. The regression item the first tick filed is itself dispatchable, so a
	// later tick may add a third row; the case is about the two the items produced.
	ticks := readTicks(t, f.store)
	if len(ticks) < 2 {
		t.Fatalf("the loop recorded %d tick(s), want at least the two that gated items: %+v", len(ticks), ticks)
	}
	if !tickFor(t, f.store, ticks, "item_063").overBudget || !tickFor(t, f.store, ticks, "item_064").overBudget {
		t.Fatalf("a tick did not record its overrun: %+v", ticks)
	}
	filed := 0
	for id := range readItemsByName(t, f.store) {
		if strings.HasPrefix(id, "gate-budget-") {
			filed++
		}
	}
	if filed != 1 {
		t.Fatalf("the store holds %d gate-budget item(s), want exactly 1 for one day of overruns", filed)
	}
}

// The item the loop files is a legal item.record: item.Apply is the one materializer, and a
// payload it would refuse is a ticket nobody finds. This pins the payload against the item
// package's own validation and criterion rows rather than against a struct in this module.
func TestTheFiledRegressionItemIsOneItemWouldAccept(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_065", "in-queue"))
	steps := f.steps(t, stub(t, "slow-gate", slowerGate), always(t, "land", 0), always(t, "verdict", 0))
	var report strings.Builder
	loop := budgetLoop(t, f, steps, &report, time.Millisecond)
	if _, err := loop.Run(context.Background()); err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	s, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatalf("opening the store failed: %v", err)
	}
	defer s.Close()
	var criteria int
	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT count(*) FROM item_criteria WHERE item_id = ?`,
			"gate-budget-2026-11-12").Scan(&criteria)
	})
	if err != nil {
		t.Fatalf("reading the filed item's criteria failed: %v", err)
	}
	if criteria != 1 {
		t.Fatalf("the filed item carries %d criterion(s), want the one measurement that opened it", criteria)
	}
}

// The item package's own kind is what the loop writes; a typo in it would be caught by the
// materializer only at write time, which is the night the ticket had to be visible.
func TestTheRegressionItemUsesTheItemRecordKind(t *testing.T) {
	if item.RecordKind != "item.record" {
		t.Fatalf("item.RecordKind is %q, and the loop's regression item is built on it", item.RecordKind)
	}
}
