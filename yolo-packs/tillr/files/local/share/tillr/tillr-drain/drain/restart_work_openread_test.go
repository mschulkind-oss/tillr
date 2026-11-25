package drain

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRestartWorkDoesNotOpenReadPerTickOrDuplicateStopCheck(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "")
	t.Run("no_duplicate_stop_check_in_settle", func(t *testing.T) {
		h := newRestartHarness(t, nil, nil, "")
		ctx := context.Background()
		if err := h.l.refreshSession(ctx, h.l.cfg.Now().Add(PassInterval)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			h.l.endSession()
			h.l.closeWALKeeper()
		})

		var openReads, stopQueries atomic.Int64
		neverTick := make(chan time.Time)
		h.l.restartClock = func() heartbeatTicker {
			return heartbeatTicker{C: neverTick, Stop: func() {}}
		}
		h.l.pollOpenRead = func(ctx context.Context, path string, opts ...store.OpenOption) (*store.Store, error) {
			openReads.Add(1)
			return store.OpenRead(ctx, path, opts...)
		}
		h.l.stopQueryObs = func() {
			stopQueries.Add(1)
		}

		// Immediate completion runs settle() twice (admission + completion), with zero 50 ms ticks.
		val, err := restartWork(ctx, h.l, func(context.Context) (int, error) {
			return 42, nil
		})
		if err != nil || val != 42 {
			t.Fatalf("restartWork = %d, %v; want 42, nil", val, err)
		}
		if got := openReads.Load(); got != 1 {
			t.Fatalf("openReads across admission+completion settle = %d, want 1", got)
		}
		if got := stopQueries.Load(); got != 2 {
			t.Fatalf("stopRequested queries across 2 settle() calls = %d, want 2 (no duplicate in reclaimDetachedGateExaminations)", got)
		}

		// Standalone reclaimDetachedGateExaminations still checks stopRequested.
		fixtureWrite(t, h.f, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE drain_sessions SET stop_requested = 1 WHERE ended_at IS NULL`)
			return err
		})
		beforeStandalone := stopQueries.Load()
		if err := h.l.reclaimDetachedGateExaminations(ctx); err != nil {
			t.Fatalf("reclaimDetachedGateExaminations: %v", err)
		}
		if got := stopQueries.Load() - beforeStandalone; got != 1 {
			t.Fatalf("standalone reclaimDetachedGateExaminations ran %d stopRequested queries, want 1", got)
		}
	})

	t.Run("single_reader_across_multiple_ticks", func(t *testing.T) {
		h := newRestartHarness(t, nil, nil, "")
		ctx := context.Background()
		if err := h.l.refreshSession(ctx, h.l.cfg.Now().Add(PassInterval)); err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			h.l.endSession()
			h.l.closeWALKeeper()
		})

		var openReads, stopQueries, ticksDone atomic.Int64
		fourTicks := make(chan struct{})
		h.l.pollOpenRead = func(ctx context.Context, path string, opts ...store.OpenOption) (*store.Store, error) {
			openReads.Add(1)
			return store.OpenRead(ctx, path, opts...)
		}
		h.l.stopQueryObs = func() {
			stopQueries.Add(1)
		}
		h.l.restartTickDoneObs = func(coalesced bool) {
			if !coalesced && ticksDone.Add(1) == 4 {
				close(fourTicks)
			}
		}

		fdsBefore := openStoreDescriptors(t, h.f.storePath)
		_, err := restartWork(ctx, h.l, func(child context.Context) (struct{}, error) {
			select {
			case <-child.Done():
				return struct{}{}, child.Err()
			case <-fourTicks:
				return struct{}{}, nil
			case <-time.After(hangGuard):
				t.Errorf("timed out waiting for 4 restartWork ticks")
				return struct{}{}, nil
			}
		})
		if err != nil {
			t.Fatalf("restartWork: %v", err)
		}
		if got := openReads.Load(); got != 1 {
			t.Fatalf("openReads across admission, >=4 ticks, and completion = %d, want 1", got)
		}
		if got := stopQueries.Load(); got < 6 {
			t.Fatalf("expected >= 6 stop queries across admission, >=4 ticks, and completion; got %d", got)
		}
		if fdsAfter := openStoreDescriptors(t, h.f.storePath); fdsAfter != fdsBefore {
			t.Fatalf("restartWork changed open descriptor count on %s: before=%d after=%d", h.f.storePath, fdsBefore, fdsAfter)
		}
	})
}

func TestRestartWorkConcurrentGoroutinesCoalesceTicks(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "")
	h := newRestartHarness(t, nil, nil, "")
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	if err := h.l.refreshSession(ctx, h.l.cfg.Now().Add(PassInterval)); err != nil {
		t.Fatal(err)
	}
	h.l.enablePollReader()
	t.Cleanup(func() {
		h.l.disableAndClosePollReader()
		h.l.endSession()
		h.l.closeWALKeeper()
	})

	const workers = 6
	var (
		tickChsMu      sync.Mutex
		tickChs        []chan time.Time
		openReads      atomic.Int64
		waveArmed      atomic.Bool
		arrived        atomic.Int64
		doneTicks      atomic.Int64
		settledTicks   atomic.Int64
		coalescedTicks atomic.Int64
		waveSettled    atomic.Int64
		waveCoalesced  atomic.Int64
	)
	allArrived := make(chan struct{})
	allClocks := make(chan struct{})
	waveDone := make(chan struct{})
	finishWork := make(chan struct{})
	var closeArrivedOnce, closeClocksOnce, closeWaveOnce sync.Once

	h.l.pollOpenRead = func(ctx context.Context, path string, opts ...store.OpenOption) (*store.Store, error) {
		openReads.Add(1)
		return store.OpenRead(ctx, path, opts...)
	}
	h.l.restartClock = func() heartbeatTicker {
		ch := make(chan time.Time, 1)
		tickChsMu.Lock()
		tickChs = append(tickChs, ch)
		if len(tickChs) == workers {
			closeClocksOnce.Do(func() { close(allClocks) })
		}
		tickChsMu.Unlock()
		return heartbeatTicker{C: ch, Stop: func() {}}
	}
	h.l.restartTickArriveObs = func() {
		if arrived.Add(1) >= workers {
			closeArrivedOnce.Do(func() { close(allArrived) })
		}
	}
	h.l.stopQueryObs = func() {
		if !waveArmed.Load() {
			return
		}
		select {
		case <-allArrived:
		case <-ctx.Done():
		}
	}
	h.l.restartTickDoneObs = func(coalesced bool) {
		if coalesced {
			coalescedTicks.Add(1)
		} else {
			settledTicks.Add(1)
		}
		if doneTicks.Add(1) == workers {
			waveSettled.Store(settledTicks.Load())
			waveCoalesced.Store(coalescedTicks.Load())
			waveArmed.Store(false)
			closeWaveOnce.Do(func() { close(waveDone) })
		}
	}

	var entered atomic.Int64
	allEntered := make(chan struct{})
	errs := make(chan error, workers)
	for range workers {
		go func() {
			_, err := restartWork(ctx, h.l, func(child context.Context) (struct{}, error) {
				if entered.Add(1) == workers {
					close(allEntered)
				}
				select {
				case <-finishWork:
					return struct{}{}, nil
				case <-child.Done():
					return struct{}{}, child.Err()
				}
			})
			errs <- err
		}()
	}

	awaitRestartTest(t, allEntered, "all concurrent restartWork goroutines to enter")
	awaitRestartTest(t, allClocks, "all concurrent restartWork tickers to register")
	waveArmed.Store(true)
	now := time.Now()
	tickChsMu.Lock()
	for _, ch := range tickChs {
		ch <- now
	}
	tickChsMu.Unlock()

	awaitRestartTest(t, waveDone, "concurrent tick wave to complete")
	close(finishWork)
	for range workers {
		if err := <-errs; err != nil {
			t.Fatalf("restartWork: %v", err)
		}
	}

	if got := openReads.Load(); got != 1 {
		t.Fatalf("openReads across %d concurrent restartWork goroutines = %d, want 1", workers, got)
	}
	if s, c := waveSettled.Load(), waveCoalesced.Load(); s != 1 || c != workers-1 {
		t.Fatalf("concurrent %d-goroutine tick wave ran settle=%d coalesced=%d; want settle=1 coalesced=%d",
			workers, s, c, workers-1)
	}
}

type blockingMultiWorkerSteps struct {
	recordingSteps
	mu      sync.Mutex
	entered int
	allIn   chan struct{}
}

func (s *blockingMultiWorkerSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	s.mu.Lock()
	s.entered++
	if s.entered == 4 {
		close(s.allIn)
	}
	s.mu.Unlock()
	<-ctx.Done()
	return run.Record{}, "", ctx.Err()
}

func TestRestartWorkConcurrentGoroutinesShareLoopPollReader(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	t.Setenv("TILLR_SQLITE_TRACE", "")
	f := newRefusalLoopFixtureWithLanes(t, 4, "item-a", "item-b", "item-c", "item-d")
	steps := &blockingMultiWorkerSteps{allIn: make(chan struct{})}
	l := f.loop(t, steps, settlingStartStager{f.laneRoot})
	l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3", "lane-4"}
	l.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}
	l.cfg.Report = io.Discard

	selfExe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	exe := filepath.Join(t.TempDir(), "tillr-drain")
	rawExe, err := os.ReadFile(selfExe)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeForkSafe(exe, rawExe, 0o755); err != nil {
		t.Fatal(err)
	}
	l.cfg.Restart = &RestartConfig{
		Executable: exe,
		Check:      func(context.Context, string) error { return nil },
	}

	var (
		openReads       atomic.Int64
		countingTicks   atomic.Bool
		ticksAfterAllIn atomic.Int64
	)
	twelveTicks := make(chan struct{})
	var closeTwelveOnce sync.Once

	l.pollOpenRead = func(ctx context.Context, path string, opts ...store.OpenOption) (*store.Store, error) {
		openReads.Add(1)
		return store.OpenRead(ctx, path, opts...)
	}
	l.restartTickDoneObs = func(bool) {
		if countingTicks.Load() && ticksAfterAllIn.Add(1) >= 12 {
			closeTwelveOnce.Do(func() { close(twelveTicks) })
		}
	}

	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	done := make(chan struct{})
	var res LoopResult
	var runErr error
	go func() {
		defer close(done)
		res, runErr = l.Run(ctx)
	}()

	select {
	case <-steps.allIn:
	case <-done:
		t.Fatalf("Loop.Run exited before all 4 workers entered: res=%+v err=%v", res, runErr)
	case <-time.After(hangGuard):
		t.Fatal("timed out waiting for 4 workers to enter Steps.Run")
	}

	opensAtAllIn := openReads.Load()
	countingTicks.Store(true)
	select {
	case <-twelveTicks:
	case <-done:
		t.Fatalf("Loop.Run exited before 12 worker ticks: res=%+v err=%v", res, runErr)
	case <-time.After(hangGuard):
		t.Fatal("timed out waiting for 12 worker restartWork ticks")
	}
	opensAfterTicks := openReads.Load()

	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE drain_sessions SET stop_requested = 1 WHERE ended_at IS NULL`)
		return err
	})

	select {
	case <-done:
	case <-time.After(hangGuard):
		t.Fatal("Loop.Run did not stop after stop_requested = 1")
	}
	if runErr != nil || res.Stop != StopOperatorStop {
		t.Fatalf("Loop.Run = %+v, %v; want %q, nil", res, runErr, StopOperatorStop)
	}
	if opensAfterTicks != opensAtAllIn {
		t.Fatalf("50ms ticks opened %d fresh read handle(s) across 4 concurrent workers (before=%d after=%d); want 0",
			opensAfterTicks-opensAtAllIn, opensAtAllIn, opensAfterTicks)
	}
	if opensAfterTicks > 1 {
		t.Fatalf("total poll OpenRead calls during active Loop.Run = %d, want <= 1", opensAfterTicks)
	}
	if n := openStoreDescriptors(t, f.storePath); n != 0 {
		t.Fatalf("Loop.Run returned with %d file descriptor(s) still open on %s", n, f.storePath)
	}
}
