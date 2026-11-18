package drain

import (
	"context"
	"errors"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestDispatchWithoutAGateRecordsAPassAndDecision(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	result := &LoopResult{}
	if _, err := l.pass(context.Background(), result); err != nil {
		t.Fatal(err)
	}
	// Consume the worker's completion before the fixture worktrees are removed.
	if len(l.active) > 0 {
		if _, err := l.takeCompletion(<-l.completions, result); err != nil {
			t.Fatal(err)
		}
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var applied, count, gates int
		var started, ended int64
		if err := r.QueryRow(`SELECT applied_count, json_extract(decision_counts, '$.dispatch'), gate_count, started_at, ended_at FROM drain_passes`).Scan(&applied, &count, &gates, &started, &ended); err != nil {
			return err
		}
		if applied != 1 || count != 1 || gates != 0 || ended < started {
			t.Errorf("dispatch measurement: %d %d %d %d %d", applied, count, gates, started, ended)
		}
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM dispatches d JOIN drain_passes t ON d.pass_id = t.tick_id`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("correlated dispatches = %d", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAsyncMeasurementsAccumulateWithoutReplacingPassAnswers(t *testing.T) {
	path := filepath.Join(scratchUnderDurable(t), "store.db")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	pass := PassTiming{TickID: "pass", AtMS: 1000, EndMS: 1100, DecisionCounts: map[string]int{"judge": 2}, Applied: 2, GateBudgetMS: 100}
	if err := recordPassTiming(context.Background(), path, pass); err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		if err := recordPassTiming(context.Background(), path, PassTiming{TickID: "pass", GateMS: 60, Gates: 1}); err != nil {
			t.Fatal(err)
		}
	}
	s = openFixture(t, path)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var n, gate, gates, over, ended, decisions int
		if err := r.QueryRow(`SELECT count(*), gate_ms, gate_count, gate_over_budget, ended_at, json_extract(decision_counts, '$.judge') FROM drain_passes`).Scan(&n, &gate, &gates, &over, &ended, &decisions); err != nil {
			return err
		}
		// Two 60ms gates against a 100ms per-run budget: the cost is their sum, and neither
		// run overran, so the pass is not over budget.
		if n != 1 || gate != 120 || gates != 2 || over != 0 || ended != 1100 || decisions != 2 {
			t.Errorf("completion replaced pass: %d %d %d %d %d %d", n, gate, gates, over, ended, decisions)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

// The budget is a per-run target, so a pass is over budget only when one of its gate runs
// is: two runs each under it are not an overrun however their sum compares. And a budget is
// a reported target, never a hold, so an overrun files no item.
func TestThePassBudgetComparesEachGateRunAndFilesNothing(t *testing.T) {
	path := filepath.Join(scratchUnderDurable(t), "store.db")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	for _, id := range []string{"under", "over"} {
		pass := PassTiming{TickID: id, AtMS: 1000, EndMS: 1100, DecisionCounts: map[string]int{"judge": 2}, Applied: 2, GateBudgetMS: 100}
		if err := recordPassTiming(context.Background(), path, pass); err != nil {
			t.Fatal(err)
		}
	}
	var report strings.Builder
	l := &Loop{cfg: LoopConfig{Store: path, Report: &report}, gateBudget: 100 * time.Millisecond}
	for i := 0; i < 2; i++ {
		l.recordPass(&PassTiming{TickID: "under", AtMS: 1000, GateBudgetMS: 100, GateMS: 60, GateMaxMS: 60, Gates: 1})
	}
	l.recordPass(&PassTiming{TickID: "over", AtMS: 1000, GateBudgetMS: 100, GateMS: 30, GateMaxMS: 30, Gates: 1})
	l.recordPass(&PassTiming{TickID: "over", AtMS: 1000, GateBudgetMS: 100, GateMS: 150, GateMaxMS: 150, Gates: 1})
	l.recordPass(&PassTiming{TickID: "over", AtMS: 1000, GateBudgetMS: 100, GateMS: 20, GateMaxMS: 20, Gates: 1})
	s = openFixture(t, path)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		for id, want := range map[string][3]int{"under": {120, 60, 0}, "over": {200, 150, 1}} {
			var gate, longest, over int
			if err := r.QueryRow(`SELECT gate_ms, gate_max_ms, gate_over_budget FROM drain_passes WHERE tick_id = ?`, id).Scan(&gate, &longest, &over); err != nil {
				return err
			}
			if got := [3]int{gate, longest, over}; got != want {
				t.Errorf("pass %s recorded gate/longest/over %v, want %v", id, got, want)
			}
		}
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM items`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Errorf("a gate overrun filed %d item(s); a budget is a reported target, never an item", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	out := report.String()
	if strings.Count(out, "overran its budget") != 1 || !strings.Contains(out, "tick over ") {
		t.Fatalf("the report must name the one overrunning pass once:\n%s", out)
	}
	if strings.Contains(out, "filed") {
		t.Fatalf("the report names a filed item:\n%s", out)
	}
}

// next_pass_at is a scheduled deadline, not hard expiry. Match the view's
// five-second read/scheduling allowance (tillr-view/heartbeat.go SessionGraceMS)
// while still requiring proof that startup refreshed its initial session.
func startupHeartbeatCurrent(started, refreshed, next, now int64) bool {
	return refreshed > started && (now <= next || now-next <= 5000)
}

func TestStartupHeartbeatUsesTheViewsExpiryContract(t *testing.T) {
	for _, tc := range []struct {
		name            string
		refreshed, next int64
		current         bool
	}{
		{"refreshed-within-grace", 2000, 3000, true},
		{"exact-grace-edge", 2000, 1000, true},
		{"expired", 2000, 999, false},
		{"never-refreshed", 500, 10000, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := startupHeartbeatCurrent(500, tc.refreshed, tc.next, 6000); got != tc.current {
				t.Fatalf("heartbeat current = %v, want %v", got, tc.current)
			}
		})
	}
}

// manualClock is the heartbeat's clock in tests: now only moves when the test moves it,
// and a tick only happens when the test sends one.
type manualClock struct {
	mu    sync.Mutex
	now   time.Time
	ticks chan time.Time
}

func (c *manualClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *manualClock) advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func (c *manualClock) heartbeat() heartbeatTicker {
	return heartbeatTicker{C: c.ticks, Stop: func() {}}
}

// heartbeatTicksDuringStartup is how many heartbeat ticks the startup step below sends.
const heartbeatTicksDuringStartup = 4

// heartbeatStallBound only bounds how long a FAILING test waits to learn that something is
// stuck (the heartbeat, or a writer a diagnostic must not retain). A passing run never
// waits on it: each event is observed as soon as it happens, however slow the machine is.
const heartbeatStallBound = hangGuard

type slowHeartbeatSyncSteps struct {
	recordingSteps
	store string
	t     *testing.T
	clock *manualClock
}

// Sync is the long synchronous startup step. It never returns on its own schedule: it
// hands the heartbeat tick after tick and returns once it has been served enough of them.
// The ticks channel is unbuffered, so a send only completes when the heartbeat goroutine
// has taken that tick, which it does only after finishing the previous tick's refresh. So
// when send n returns, refresh n-1 is on disk: the order of events, not a duration, is what
// proves the heartbeat ran while this step was still in progress.
func (s *slowHeartbeatSyncSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	for i := 1; i <= heartbeatTicksDuringStartup; i++ {
		s.clock.advance(PassInterval)
		select {
		case s.clock.ticks <- s.clock.Now():
		case <-ctx.Done():
			s.t.Errorf("startup was cancelled while the heartbeat was still owed tick %d: %v", i, context.Cause(ctx))
			return SyncOutcome{}, ctx.Err()
		case <-time.After(heartbeatStallBound):
			s.t.Errorf("the heartbeat took no tick %d while startup was in progress: it is blocked", i)
			return SyncOutcome{}, errors.New("heartbeat blocked")
		}
	}
	read := openFixture(s.t, s.store)
	defer read.Close()
	if err := read.ReadTx(ctx, func(r *store.Reader) error {
		var started, refreshed, next int64
		if err := r.QueryRow(`SELECT started_at, refreshed_at, next_pass_at FROM drain_sessions`).Scan(&started, &refreshed, &next); err != nil {
			return err
		}
		// Refreshes 1..n-1 are done, each stamped at or after its own tick's time.
		wantRefreshed := started + int64(heartbeatTicksDuringStartup-1)*PassInterval.Milliseconds()
		if refreshed < wantRefreshed {
			s.t.Errorf("startup heartbeat refreshed at +%dms, want at least +%dms after the session started",
				refreshed-started, wantRefreshed-started)
		}
		if !startupHeartbeatCurrent(started, refreshed, next, s.clock.Now().UnixMilli()) {
			s.t.Errorf("startup heartbeat expired: %d %d %d", started, refreshed, next)
		}
		return nil
	}); err != nil {
		s.t.Fatal(err)
	}
	return SyncOutcome{}, errors.New("fixture startup finished")
}

// The heartbeat must keep the session fresh while a startup step is still running. The
// test proves it with a clock it moves and ticks it sends, never by letting wall time pass.
func TestHeartbeatContinuesDuringLongSynchronousStartup(t *testing.T) {
	f, l := passFixture(t)
	clock := &manualClock{now: l.cfg.Now(), ticks: make(chan time.Time)}
	l.cfg.Now = clock.Now
	l.heartbeatClock = clock.heartbeat
	l.cfg.Steps = &slowHeartbeatSyncSteps{store: f.storePath, t: t, clock: clock}
	if _, err := l.Run(context.Background()); err == nil || err.Error() != "fixture startup finished" {
		t.Fatalf("startup result: %v", err)
	}
}

// checkpointHeartbeatSteps is a startup step that moves the Loop's clock one checkpoint
// interval at its second tick, so that tick's heartbeat is the one that checkpoints, and
// then sends two more.
type checkpointHeartbeatSteps struct {
	recordingSteps
	store  string
	t      *testing.T
	clock  *manualClock
	cycles *[]store.Checkpointed
	mu     *sync.Mutex
}

func (s *checkpointHeartbeatSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	var thirdTick time.Time
	for i, step := range []time.Duration{PassInterval, CheckpointInterval, PassInterval, PassInterval} {
		s.clock.advance(step)
		if i == 2 {
			thirdTick = s.clock.Now()
		}
		select {
		case s.clock.ticks <- s.clock.Now():
		case <-ctx.Done():
			s.t.Errorf("startup was cancelled while the heartbeat was still owed tick %d: %v", i+1, context.Cause(ctx))
			return SyncOutcome{}, ctx.Err()
		case <-time.After(heartbeatStallBound):
			s.t.Errorf("the heartbeat took no tick %d: it is blocked", i+1)
			return SyncOutcome{}, errors.New("heartbeat blocked")
		}
	}
	// Tick 3 was taken only after tick 2's refresh and checkpoint returned, and tick 4 only
	// after tick 3's refresh: the order of events says the cycle ran on tick 2's heartbeat,
	// finished, and the heartbeat went on.
	s.mu.Lock()
	cycles := append([]store.Checkpointed(nil), *s.cycles...)
	s.mu.Unlock()
	if len(cycles) != 1 || !cycles[0].Restarted {
		s.t.Errorf("the heartbeat ran %d checkpoint cycles across one checkpoint interval of the injected clock, want exactly one that restarted the log: %+v", len(cycles), cycles)
	}
	read := openFixture(s.t, s.store)
	defer read.Close()
	if err := read.ReadTx(ctx, func(r *store.Reader) error {
		var refreshed int64
		if err := r.QueryRow(`SELECT refreshed_at FROM drain_sessions`).Scan(&refreshed); err != nil {
			return err
		}
		if refreshed < thirdTick.UnixMilli() {
			s.t.Errorf("the heartbeat after the checkpoint did not refresh the session: refreshed at %d, tick 3 at %d", refreshed, thirdTick.UnixMilli())
		}
		return nil
	}); err != nil {
		s.t.Fatal(err)
	}
	return SyncOutcome{}, errors.New("fixture startup finished")
}

// The heartbeat's checkpoint is due by the Loop's injected clock, not the wall clock, and it
// runs on the heartbeat's goroutine between two ticks: a test that moves the clock one
// checkpoint interval sees exactly one cycle, on that tick, and the next tick taken only
// once the cycle has returned. Nothing here waits on wall time.
func TestTheHeartbeatCheckpointsByTheInjectedClockAndThenGoesOn(t *testing.T) {
	f, l := passFixture(t)
	clock := &manualClock{now: l.cfg.Now(), ticks: make(chan time.Time)}
	l.cfg.Now = clock.Now
	l.heartbeatClock = clock.heartbeat
	var mu sync.Mutex
	var cycles []store.Checkpointed
	l.checkpointed = func(c store.Checkpointed) {
		mu.Lock()
		cycles = append(cycles, c)
		mu.Unlock()
	}
	l.cfg.Steps = &checkpointHeartbeatSteps{store: f.storePath, t: t, clock: clock, cycles: &cycles, mu: &mu}
	if _, err := l.Run(context.Background()); err == nil || err.Error() != "fixture startup finished" {
		t.Fatalf("startup result: %v", err)
	}
}

func TestHeartbeatDoesNotShortenAScheduledBackoff(t *testing.T) {
	f, l := passFixture(t)
	at := l.cfg.Now()
	if err := l.refreshSession(context.Background(), at.Add(30*time.Second)); err != nil {
		t.Fatal(err)
	}
	if err := l.refreshSession(context.Background(), at.Add(PassInterval)); err != nil {
		t.Fatal(err)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var next int64
		if err := r.QueryRow(`SELECT next_pass_at FROM drain_sessions`).Scan(&next); err != nil {
			return err
		}
		if next != at.Add(30*time.Second).UnixMilli() {
			t.Errorf("heartbeat shortened the scheduled backoff to %d", next)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestCanceledSessionStartupIsAnOperatorStop(t *testing.T) {
	_, l := passFixture(t)
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	result, err := l.Run(ctx)
	if err != nil || result.Stop != StopOperatorStop {
		t.Fatalf("canceled startup: %+v / %v", result, err)
	}
}

func TestLoopRecordsStartupSessionAndEndsItEvenWithoutDispatch(t *testing.T) {
	f, l := passFixture(t)
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var started, refreshed, next, ended int64
		if err := r.QueryRow(`SELECT started_at, refreshed_at, next_pass_at, ended_at FROM drain_sessions`).Scan(&started, &refreshed, &next, &ended); err != nil {
			return err
		}
		if started != l.cfg.Now().UnixMilli() || refreshed != started || next != started+time.Second.Milliseconds() || ended != started {
			t.Errorf("session lifecycle: %d %d %d %d", started, refreshed, next, ended)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
