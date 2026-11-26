package drain

import (
	"context"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
)

// spendRecord is one finished run's record carrying the spend a backend reported, with
// nil standing for "the report carried no such number" — the state the measurement has to
// keep honest and, since docs/design/momentum-principle.md, the only state it acts on.
func spendRecord(runID, backend string, tokens *int64, cost *float64) run.Record {
	rec := run.Record{RunID: runID, Backend: backend}
	if tokens != nil || cost != nil {
		rec.Usage = &run.Usage{Tokens: tokens, CostUSD: cost}
	}
	return rec
}

// ptr is the two pointer helpers these cases read as "reported".
func count(value int64) *int64       { return &value }
func dollars(value float64) *float64 { return &value }

// TestTheLoopSumsWhatTheRunsReported is the measurement's positive case: runs that reported
// tokens and a cost are summed, and the total is an answer the loop ends with.
//
// This case is `TestTheTokenCeilingStopsTheLoop`'s rewrite. It used to answer a stop at the
// ceiling; it now pins that the same total is measured and that measuring it is the whole of
// what spend does here — there is no ceiling left for a total to compare against.
func TestTheLoopSumsWhatTheRunsReported(t *testing.T) {
	loop, err := NewLoop(LoopConfig{ExitWhenDrained: true,
		Root: "/repo", Store: "/store", LaneRoot: "/lanes", Lanes: []string{"lane-1"},
		Policy: &Policy{}, Steps: stubSteps{}, Stage: stubStager{},
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	var result LoopResult
	result.Dispatched = 2
	loop.countSpend(&result, spendRecord("run-1", "pi", count(60), dollars(0.5)))
	loop.countSpend(&result, spendRecord("run-2", "pi", count(40), dollars(0.5)))
	if result.Tokens != 100 || result.SpendUSD != 1 {
		t.Fatalf("the loop measured %d tokens and $%f, want 100 and $1", result.Tokens, result.SpendUSD)
	}
	if result.SpendUnknown != 0 || result.CostUnknown != 0 {
		t.Fatalf("two fully reported runs counted %d unknown tokens and %d unknown costs, want none",
			result.SpendUnknown, result.CostUnknown)
	}
}

// TestACostTheBackendNeverReportedIsCountedUnknown is the rule that keeps the dollar total
// honest: muse and agy report tokens and no cost, so their nil is counted as unknown rather
// than added as zero. The case used to stop a dollar ceiling by name; it now pins that the
// unknown is recorded and the total still reads what was actually measured.
//
// This is `TestTheDollarCeilingStopsTheLoop`'s and
// `TestACostTheBackendNeverReportedStopsTheDollarCeiling`'s rewrite: same run, same
// reporting shape, and a measurement instead of a wall.
func TestACostTheBackendNeverReportedIsCountedUnknown(t *testing.T) {
	loop, err := NewLoop(LoopConfig{ExitWhenDrained: true,
		Root: "/repo", Store: "/store", LaneRoot: "/lanes", Lanes: []string{"lane-1"},
		Policy: &Policy{}, Steps: stubSteps{}, Stage: stubStager{},
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	var result LoopResult
	result.Dispatched = 2
	loop.countSpend(&result, spendRecord("run-1", "pi", count(100), dollars(0.25)))
	loop.countSpend(&result, spendRecord("run-2", "muse", count(100), nil))
	if result.SpendUSD != 0.25 {
		t.Fatalf("the reported total is $%f, want $0.25: the cost-less run must not have added a zero", result.SpendUSD)
	}
	if result.Tokens != 200 {
		t.Fatalf("the token total is %d, want 200: muse reports tokens even though it reports no cost", result.Tokens)
	}
	if result.CostUnknown != 1 || result.CostUnknownExample != "run-2 on muse" {
		t.Fatalf("cost-unknown reads %d (%s), want 1 (run-2 on muse)", result.CostUnknown, result.CostUnknownExample)
	}
}

// TestARunThatReportedNoUsageIsCountedUnknown is the token side of the same rule: a report
// with no usage at all is a run the loop cannot measure, not a run that spent nothing.
func TestARunThatReportedNoUsageIsCountedUnknown(t *testing.T) {
	loop, err := NewLoop(LoopConfig{ExitWhenDrained: true,
		Root: "/repo", Store: "/store", LaneRoot: "/lanes", Lanes: []string{"lane-1"},
		Policy: &Policy{}, Steps: stubSteps{}, Stage: stubStager{},
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	var result LoopResult
	result.Dispatched = 1
	loop.countSpend(&result, spendRecord("run-1", "agy", nil, nil))
	if result.Tokens != 0 || result.SpendUnknown != 1 || result.CostUnknown != 1 {
		t.Fatalf("an unreported run measured %d tokens, %d unknown token counts and %d unknown costs; want 0, 1 and 1",
			result.Tokens, result.SpendUnknown, result.CostUnknown)
	}
	if result.SpendUnknownExample != "run-1 on agy" || result.CostUnknownExample != "run-1 on agy" {
		t.Fatalf("the unmeasured run is named %q and %q, want the run by id and backend",
			result.SpendUnknownExample, result.CostUnknownExample)
	}
}

func TestNotApplicableSpendIsNotCountedUnreported(t *testing.T) {
	loop, err := NewLoop(LoopConfig{ExitWhenDrained: true,
		Root: "/repo", Store: "/store", LaneRoot: "/lanes", Lanes: []string{"lane-1"},
		Policy: &Policy{}, Steps: stubSteps{}, Stage: stubStager{},
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	var result LoopResult
	result.Dispatched = 1
	rec := run.Record{RunID: "run-cmd-1", Backend: "command", SpendStatus: "not-applicable"}
	loop.countSpend(&result, rec)
	if result.StartedSpend.Runs != 1 {
		t.Fatalf("started runs = %d, want 1", result.StartedSpend.Runs)
	}
	if result.StartedSpend.Unreported != 0 || result.StartedSpend.TokensUnreported != 0 || result.StartedSpend.CostUnreported != 0 {
		t.Fatalf("not-applicable spend counted as unreported: %+v", result.StartedSpend)
	}
	if result.SpendUnknown != 0 || result.CostUnknown != 0 || result.SpendUnknownExample != "" || result.CostUnknownExample != "" {
		t.Fatalf("loop result counted not-applicable spend as unknown: spend=%d (%q) cost=%d (%q)",
			result.SpendUnknown, result.SpendUnknownExample, result.CostUnknown, result.CostUnknownExample)
	}
}

// TestBackoffDelayDoublesUpToItsCap pins the shape of the wait a repeated fault earns: the
// base after the first failure, doubled per consecutive one, and never longer than the cap.
// A streak of zero is no wait, which is how a healthy loop is told from a broken one.
func TestBackoffDelayDoublesUpToItsCap(t *testing.T) {
	base := time.Second
	for _, c := range []struct {
		streak int
		want   time.Duration
	}{
		{0, 0},
		{1, base},
		{2, 2 * base},
		{3, 4 * base},
		{4, 8 * base},
		{30, BackoffCap},
	} {
		if got := BackoffDelay(c.streak, base); got != c.want {
			t.Fatalf("a streak of %d waits %s, want %s", c.streak, got, c.want)
		}
	}
	// The delay is growth and not a deadline: a caller's own base is doubled the same way,
	// which is what lets a test drive this without waiting seconds.
	if got := BackoffDelay(3, time.Millisecond); got != 4*time.Millisecond {
		t.Fatalf("a 1ms base at a streak of 3 waits %s, want 4ms", got)
	}
}

// TestTheWaitGivesUpWhenTheContextIsDone pins the interruptibility the ruling asks for: an
// operator stop does not sit out a five-minute wait. A live context waits the duration and
// answers that it finished; a cancelled one answers immediately.
//
// The negative control is the assertion order below: a wait that ignored the context would
// block for the whole hour on the second line and never answer false.
func TestTheWaitGivesUpWhenTheContextIsDone(t *testing.T) {
	done, cancel := context.WithCancel(context.Background())
	cancel()
	if waitFor(done, time.Hour) {
		t.Fatalf("a cancelled context waited out an hour and answered that the wait finished")
	}
	if !waitFor(context.Background(), time.Millisecond) {
		t.Fatalf("a live context did not finish a 1ms wait")
	}
}

// stubStager stages nothing: these cases never tick either.
type stubStager struct{}

func (stubStager) Stage(_ context.Context, _ StageSpec) error { return nil }

// stubSteps is a Steps with no programs: these cases never tick, so nothing it could do
// would be called. NewLoop refuses a nil one, which is the only reason it exists here.
type stubSteps struct{}

func (stubSteps) Run(_ context.Context, _ run.Request) (run.Record, string, error) {
	return run.Record{}, "", nil
}
func (stubSteps) Gate(_ context.Context, _ Candidate) (GateOutcome, error) {
	return GateOutcome{Outcome: GatePassed}, nil
}
func (stubSteps) Land(_ context.Context, _ Candidate) (LandOutcome, error) {
	return LandOutcome{Result: LandLanded}, nil
}
func (stubSteps) Verdict(_ context.Context, _ Candidate, _ string) error                 { return nil }
func (stubSteps) ItemState(_ context.Context, _, _, _ string, _ ...item.WaitingOn) error { return nil }
func (stubSteps) Sync(_ context.Context) (SyncOutcome, error) {
	return SyncOutcome{Skipped: "a stub step set reads no documents"}, nil
}
