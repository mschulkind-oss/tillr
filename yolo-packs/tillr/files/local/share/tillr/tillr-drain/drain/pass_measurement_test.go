package drain

import (
	"context"
	"errors"
	"io"
	"path/filepath"
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
		if n != 1 || gate != 120 || gates != 2 || over != 1 || ended != 1100 || decisions != 2 {
			t.Errorf("completion replaced pass: %d %d %d %d %d %d", n, gate, gates, over, ended, decisions)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestAggregatePassGateTimeFilesTheBudgetRegression(t *testing.T) {
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
	l := &Loop{cfg: LoopConfig{Store: path, Report: io.Discard}, gateBudget: 100 * time.Millisecond}
	for i := 0; i < 2; i++ {
		l.recordPass(&PassTiming{TickID: "pass", AtMS: 1000, GateBudgetMS: 100, GateMS: 60, Gates: 1})
	}
	s = openFixture(t, path)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM items WHERE item_id = 'gate-budget-1970-01-01'`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("aggregate overrun filed %d regression items, want 1", n)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
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

type slowHeartbeatSyncSteps struct {
	recordingSteps
	store string
	t     *testing.T
}

func (s *slowHeartbeatSyncSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	// This startup operation lasts longer than the one-second deadline and five-second
	// view grace period. Its live session must not depend on reaching the first pass.
	if !waitFor(ctx, 6500*time.Millisecond) {
		return SyncOutcome{}, ctx.Err()
	}
	read := openFixture(s.t, s.store)
	defer read.Close()
	if err := read.ReadTx(ctx, func(r *store.Reader) error {
		var started, refreshed, next int64
		if err := r.QueryRow(`SELECT started_at, refreshed_at, next_pass_at FROM drain_sessions`).Scan(&started, &refreshed, &next); err != nil {
			return err
		}
		if !startupHeartbeatCurrent(started, refreshed, next, time.Now().UnixMilli()) {
			s.t.Errorf("startup heartbeat expired: %d %d %d", started, refreshed, next)
		}
		return nil
	}); err != nil {
		s.t.Fatal(err)
	}
	return SyncOutcome{}, errors.New("fixture startup finished")
}

func TestHeartbeatContinuesDuringLongSynchronousStartup(t *testing.T) {
	f, l := passFixture(t)
	l.cfg.Now = time.Now
	l.cfg.Steps = &slowHeartbeatSyncSteps{store: f.storePath, t: t}
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
