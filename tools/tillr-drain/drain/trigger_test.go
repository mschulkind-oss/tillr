package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func storeRows(t *testing.T, path string) map[string]int {
	t.Helper()
	s := openFixture(t, path)
	defer s.Close()
	counts := map[string]int{}
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
		if err != nil {
			return err
		}
		var names []string
		for rows.Next() {
			var name string
			if err := rows.Scan(&name); err != nil {
				return err
			}
			names = append(names, name)
		}
		if err := rows.Close(); err != nil {
			return err
		}
		for _, name := range names {
			var n int
			if err := r.QueryRow(fmt.Sprintf(`SELECT COUNT(*) FROM %q`, name)).Scan(&n); err != nil {
				return err
			}
			counts[name] = n
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return counts
}

func loopFiles(t *testing.T, root string) map[string]string {
	t.Helper()
	files := map[string]string{}
	err := filepath.WalkDir(LoopDir(root), func(path string, d os.DirEntry, err error) error {
		if os.IsNotExist(err) {
			return nil
		}
		if err != nil {
			return err
		}
		if !d.IsDir() {
			raw, err := os.ReadFile(path)
			if err != nil {
				return err
			}
			files[path] = string(raw)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return files
}

// emptyPassRepeats is how many empty passes follow the fixed point. Each pass starts about
// thirteen git processes (trunk, identity, heavy-pointer and ref reads), so the count is the
// test's cost. It was 1,000: about 13,000 processes, 28 s idle and 62 s to 324 s on the
// loaded drain host, which two workers on 2026-11-18 read as a hang under a 90 s timeout
// (the goroutine dumps were of the git call then in flight; five runs alone all passed).
// The fixture's clock does not move, so nothing a pass could write on a schedule is reached
// by more passes: what this asserts, that repeating an empty pass changes nothing, is the
// same at 100.
const emptyPassRepeats = 100

func TestRepeatedEmptyPassesWriteNothing(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.Steps = &recordingSteps{}
	fixtureWrite(t, f, func(tx *store.Tx) error { _, err := tx.Exec(`UPDATE items SET state = ?`, StateBlocked); return err })
	var result LoopResult
	if stop, err := l.pass(context.Background(), &result); err != nil || stop != StopQueueDrained {
		t.Fatalf("fixed point: %s / %v", stop, err)
	}
	rows, files := storeRows(t, f.storePath), loopFiles(t, f.root)
	if rows["dispatches"] != 0 {
		t.Fatal("empty pass recorded a hold")
	}
	for i := 0; i < emptyPassRepeats; i++ {
		if stop, err := l.pass(context.Background(), &result); err != nil || stop != StopQueueDrained {
			t.Fatalf("pass %d: %s / %v", i, stop, err)
		}
	}
	if got := storeRows(t, f.storePath); !reflect.DeepEqual(rows, got) {
		t.Fatalf("rows changed: %v -> %v", rows, got)
	}
	if got := loopFiles(t, f.root); !reflect.DeepEqual(files, got) {
		t.Fatalf("files changed: %v -> %v", files, got)
	}
}

func TestGenerationsContinueAcrossLoopProcesses(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	for i := 0; i < 2; i++ {
		if i > 0 {
			l = f.loop(t, &recordingSteps{}, &itemContentStager{})
		}
		plans := mustPlan(t, l)
		ok, work, err := l.applyPlan(context.Background(), plans[i], base, l.cfg.Now())
		if err != nil || !ok {
			t.Fatalf("application: %v / %v", ok, err)
		}
		want := fmt.Sprintf("gen-%d", i+1)
		if work.Decision.RunRequest.CorrelationID != want {
			t.Fatalf("correlation = %q, want %q", work.Decision.RunRequest.CorrelationID, want)
		}
	}
}

type triggerSteps struct {
	recordingSteps
	started  chan string
	release  chan struct{}
	store    string
	requests chan run.Request
	delay    time.Duration
}

func (s *triggerSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if s.delay > 0 && !waitFor(ctx, s.delay) {
		return run.Record{}, "", ctx.Err()
	}
	if s.requests != nil {
		s.requests <- req
	}
	s.started <- req.ItemID
	if req.ItemID == "item-a" {
		select {
		case <-s.release:
		case <-ctx.Done():
			return run.Record{}, "", ctx.Err()
		}
	}
	rec := run.Record{RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend, State: run.StateExited, ExitCode: countPtr(0), StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:00:00Z", ArtifactRefs: []run.ArtifactRef{}}
	st, err := store.Open(ctx, s.store, true)
	if err != nil {
		return rec, "", err
	}
	defer st.Close()
	err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, "", err
}

func TestNewStoreFactDispatchesBeforeOwnedRunEnds(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = 'item-b'`, StateBlocked)
		return err
	})
	steps := &triggerSteps{started: make(chan string, 2), release: make(chan struct{}), store: f.storePath}
	l.cfg.Steps = steps
	ctx, cancel := context.WithCancel(context.Background())
	ended := make(chan error, 1)
	go func() { _, err := l.Run(ctx); ended <- err }()
	defer func() { cancel(); close(steps.release); <-ended }()
	select {
	case id := <-steps.started:
		if id != "item-a" {
			t.Fatal(id)
		}
	case <-time.After(hangGuard):
		t.Fatal("first run not started")
	}
	st, err := store.Open(context.Background(), f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	seedRefusalItem(t, st, "item-c")
	if err := st.Close(); err != nil {
		t.Fatal(err)
	}
	select {
	case id := <-steps.started:
		if id != "item-c" {
			t.Fatal(id)
		}
	// The first run stays blocked, so a lane that is never woken waits here; the bound is
	// a hang guard, not a latency assertion.
	case <-time.After(hangGuard):
		t.Fatal("hung: new fact did not wake free lane before first run ended")
	}
}

// replayToStop uses one lane so the order is the store's rank order rather than
// worker scheduling. Time changes between passes, not according to real sleeps.
func replayToStop(t *testing.T, advance time.Duration, pollOnly bool) []string {
	t.Helper()
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1"}
	l.pollOnly = pollOnly
	// Each item's run commits nothing, goes back to its worker once, and its identical
	// second ending waits on the owner (the owner's ruling of 2026-11-17): two starts each.
	steps := &triggerSteps{started: make(chan string, 4), release: make(chan struct{}), store: f.storePath}
	close(steps.release)
	l.cfg.Steps = steps
	var mu sync.Mutex
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { mu.Lock(); defer mu.Unlock(); return at }
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	var result LoopResult
	for {
		stop, err := l.pass(ctx, &result)
		if err != nil {
			t.Fatal(err)
		}
		if stop != "" {
			if stop != StopQueueDrained {
				t.Fatalf("replay stopped: %s", stop)
			}
			break
		}
		mu.Lock()
		at = at.Add(advance)
		mu.Unlock()
	}
	var identities []string
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT decision_id, COALESCE(run_id, '') FROM dispatches ORDER BY rowid`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var decision, runID string
			if err := rows.Scan(&decision, &runID); err != nil {
				return err
			}
			identities = append(identities, decision, runID)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	counts := storeRows(t, f.storePath)
	if counts["drain_generations"] != result.Ticks || counts["drain_passes"] != result.Ticks {
		t.Fatalf("applied passes lack generation/timing rows: %v, passes %d", counts, result.Ticks)
	}
	// Two dispatches and two settles per item: the run and its return.
	if result.Dispatched != 4 || result.Ticks != 8 {
		t.Fatalf("replay did not dispatch and settle both items: %+v", result)
	}
	return identities
}

func TestOneAppliedPassSharesItsGenerationAcrossRequests(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	steps := &triggerSteps{started: make(chan string, 2), requests: make(chan run.Request, 2), release: make(chan struct{}), store: f.storePath}
	l.cfg.Steps = steps
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	defer func() {
		// Cancellation lets sendCompletion discard notifications. Drain the
		// workers while their context is still live.
		close(steps.release)
		drainOwned(t, l, &LoopResult{})
	}()
	var result LoopResult
	if stop, err := l.pass(ctx, &result); err != nil || stop != "" {
		t.Fatalf("pass: %s / %v", stop, err)
	}
	for i := 0; i < 2; i++ {
		select {
		case req := <-steps.requests:
			if req.CorrelationID != "gen-1" {
				t.Fatalf("request correlation: %q", req.CorrelationID)
			}
		case <-time.After(hangGuard):
			t.Fatal("request not dispatched")
		}
	}
	counts := storeRows(t, f.storePath)
	if counts["drain_generations"] != 1 || counts["drain_passes"] != 1 || result.Ticks != 1 {
		t.Fatalf("one pass allocated multiple generations: %v / %+v", counts, result)
	}
}

func TestReapingWithoutDispatchIsAnAppliedPassBeforeQueueDrained(t *testing.T) {
	f, l := passFixture(t)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO lanes (lane_id, state, run_id, leased_until) VALUES ('lane-1', ?, 'foreign', ?)`, run.LaneStateLeased, l.cfg.Now().Add(-time.Second).UnixMilli())
		return err
	})
	var result LoopResult
	if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
		t.Fatalf("reaping pass stopped: %s / %v", stop, err)
	}
	counts := storeRows(t, f.storePath)
	if result.Ticks != 1 || counts["drain_generations"] != 1 || counts["drain_passes"] != 1 {
		t.Fatalf("reap was not counted: %+v / %v", result, counts)
	}
	before := eventCount(t, f.storePath)
	if stop, err := l.pass(context.Background(), &result); err != nil || stop != StopQueueDrained {
		t.Fatalf("fixed point: %s / %v", stop, err)
	}
	if got := storeRows(t, f.storePath); !reflect.DeepEqual(got, counts) || eventCount(t, f.storePath) != before {
		t.Fatal("fixed-point pass wrote after reap")
	}
}

func TestSeparateLeaseExpiriesEachRecordTheirGenerationTiming(t *testing.T) {
	f, l := passFixture(t)
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { return at }
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for i, until := range []time.Time{at.Add(-time.Second), at.Add(time.Second)} {
			if _, err := tx.Exec(`INSERT INTO lanes (lane_id, state, run_id, leased_until) VALUES (?, ?, 'foreign', ?)`, fmt.Sprintf("lane-%d", i+1), run.LaneStateLeased, until.UnixMilli()); err != nil {
				return err
			}
		}
		return nil
	})
	var result LoopResult
	for i := 0; i < 2; i++ {
		if stop, err := l.pass(context.Background(), &result); err != nil || stop != "" {
			t.Fatalf("expiry %d: %s / %v", i+1, stop, err)
		}
		at = at.Add(2 * time.Second)
	}
	counts := storeRows(t, f.storePath)
	if result.Ticks != 2 || counts["drain_generations"] != 2 || counts["drain_passes"] != 2 {
		t.Fatalf("separate reaps lost generation timing: %+v / %v", result, counts)
	}
	if stop, err := l.pass(context.Background(), &result); err != nil || stop != StopQueueDrained {
		t.Fatalf("fixed point: %s / %v", stop, err)
	}
	if got := storeRows(t, f.storePath); !reflect.DeepEqual(got, counts) {
		t.Fatalf("fixed-point pass wrote after reaps: %v -> %v", counts, got)
	}
}

func TestGenerationTimingSumsIndependentExaminationsAndComparesEachRunToTheBudget(t *testing.T) {
	f, l := passFixture(t)
	at := l.cfg.Now().UnixMilli()
	l.recordPass(&PassTiming{TickID: "gen-1", AtMS: at, StageMS: 10, GateBudgetMS: 330, Applied: 2, DecisionCounts: map[string]int{ActionJudge: 2}})
	for i := 0; i < 2; i++ {
		l.recordPass(&PassTiming{TickID: "gen-1", AtMS: at, GateMS: 200, GateMaxMS: 200, GateBudgetMS: 330, Gates: 1})
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var stage, gate, over, items int
		if err := r.QueryRow(`SELECT stage_ms, gate_ms, gate_over_budget FROM drain_passes WHERE tick_id = 'gen-1'`).Scan(&stage, &gate, &over); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT COUNT(*) FROM items WHERE item_id LIKE 'gate-budget-%'`).Scan(&items); err != nil {
			return err
		}
		// Two 200ms runs against a 330ms per-run budget: the generation's cost is their
		// 400ms sum, and neither run overran.
		if stage != 10 || gate != 400 || over != 0 || items != 0 {
			t.Fatalf("timing lost or duplicated measurements: stage %d gate %d over %d items %d", stage, gate, over, items)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestDispatchTimingBelongsOnlyToItsDispatchGeneration(t *testing.T) {
	f, l := passFixture(t, "item-a")
	// The run commits nothing, so it goes back to its worker once and its identical second
	// ending waits on the owner (the owner's ruling of 2026-11-17): room for both starts.
	steps := &triggerSteps{started: make(chan string, 2), release: make(chan struct{}), store: f.storePath, delay: 20 * time.Millisecond}
	close(steps.release)
	l.cfg.Steps = steps
	if result, err := l.Run(context.Background()); err != nil || result.Stop != StopQueueDrained {
		t.Fatalf("loop: %+v / %v", result, err)
	}
	s := openFixture(t, f.storePath)
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var dispatched, settled int64
		if err := r.QueryRow(`SELECT dispatch_ms FROM drain_passes WHERE tick_id = 'gen-1'`).Scan(&dispatched); err != nil {
			return err
		}
		if err := r.QueryRow(`SELECT dispatch_ms FROM drain_passes WHERE tick_id = 'gen-2'`).Scan(&settled); err != nil {
			return err
		}
		if dispatched < 20 || settled != 0 {
			t.Fatalf("dispatch measured on wrong pass: dispatch gen=%dms settle gen=%dms", dispatched, settled)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestReplayIsInvariantToClockRateAndTriggerSuppression(t *testing.T) {
	baseline := replayToStop(t, 0, false)
	for _, tc := range []struct {
		name     string
		advance  time.Duration
		pollOnly bool
	}{
		{"hour-between-passes", time.Hour, false},
		{"poll-only", 0, true},
		{"poll-only-hour", time.Hour, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := replayToStop(t, tc.advance, tc.pollOnly); !reflect.DeepEqual(baseline, got) {
				t.Fatalf("identities changed: %v -> %v", baseline, got)
			}
		})
	}
}
