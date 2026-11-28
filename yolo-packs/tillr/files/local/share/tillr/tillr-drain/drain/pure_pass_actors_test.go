package drain

import (
	"context"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/notify"
	"github.com/mschulkind-oss/tillr/store"
)

// TestPurePassZeroSubprocesses verifies Slice 6's core invariant (D-SN1, R-SN1,
// D-SN10): when the background actors own housekeeping and cache trunk state, a
// scheduling pass invokes zero git/Vantage subprocesses during both ReadTx
// selection (passPlansMode) and assignment WriteTx commit (applyPlan).
func TestPurePassZeroSubprocesses(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	ctx := context.Background()
	_, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Report = io.Discard
	l.cfg.ExitWhenDrained = true
	l.cfg.QuestionsCmd = []string{"vantage-trap"}
	// Seed actor caches with real git first (as Loop.Run does on startup).
	if err := l.seedActorCaches(ctx, &LoopResult{}); err != nil {
		t.Fatalf("seedActorCaches: %v", err)
	}
	l.actors.Active.Store(true)
	t.Cleanup(func() { l.actors.Active.Store(false) })

	// Replace PATH with trap binaries so any subprocess call (git, vantage, etc.)
	// during the scheduling pass is immediately detected and fails the test.
	trapDir := t.TempDir()
	trapLog := filepath.Join(trapDir, "invoked.log")
	for _, bin := range []string{"git", "uvx", "vantage-trap"} {
		script := "#!/bin/sh\necho " + bin + " \"$@\" >> " + trapLog + "\nexit 99\n"
		if err := os.WriteFile(filepath.Join(trapDir, bin), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", trapDir)

	var gen string
	plans, _, err := l.passPlansMode(ctx, l.cfg.Now(), &gen)
	if err != nil {
		t.Fatalf("pure passPlansMode failed: %v", err)
	}
	if invoked, err := os.ReadFile(trapLog); err == nil && len(invoked) > 0 {
		t.Fatalf("pure passPlansMode invoked external subprocesses:\n%s", invoked)
	}

	var dispatched *plannedApplication
	for i := range plans {
		if plans[i].decision.Action == ActionDispatch && plans[i].decision.ItemID == "item-a" {
			dispatched = &plans[i]
			break
		}
	}
	if dispatched == nil {
		t.Fatalf("pure passPlansMode did not select item-a for dispatch: %+v", plans)
	}

	// Assignment WriteTx must also invoke zero subprocesses.
	ok, work, err := l.applyPlan(ctx, *dispatched, l.cachedTrunkTip(), l.cfg.Now())
	if err != nil || !ok || work == nil {
		t.Fatalf("applyPlan = (ok=%v work=%+v err=%v), want claimed", ok, work, err)
	}
	if invoked, err := os.ReadFile(trapLog); err == nil && len(invoked) > 0 {
		t.Fatalf("pure applyPlan invoked external subprocesses:\n%s", invoked)
	}
}

// TestBackgroundActorsSyncTrunkProofAndGC verifies that the three independent
// background actors (sync, trunk-proof, gc) wake on notifications, update
// cached trunk state outside the scheduling pass, and signal passWake.
func TestBackgroundActorsSyncTrunkProofAndGC(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Report = io.Discard
	l.cfg.StorePollInterval = time.Hour
	l.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}
	holdWALKeeper(t, f.storePath)

	if err := l.seedActorCaches(ctx, &LoopResult{}); err != nil {
		t.Fatalf("seedActorCaches: %v", err)
	}
	initialTip := l.cachedTrunkTip()
	if initialTip == "" {
		t.Fatal("seedActorCaches did not populate cached trunk tip")
	}

	// Drain any startup wake signal so we observe the actor-driven wake below.
	select {
	case <-l.actors.PassWakeChan():
	default:
	}

	result := &LoopResult{}
	stopSync := l.startSyncActor(ctx, result)
	defer stopSync()
	stopTrunk := l.startTrunkProofActor(ctx, result)
	defer stopTrunk()
	stopGC := l.startGCActor(ctx)
	defer stopGC()

	// Advance trunk on disk and wake all three actors.
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "advance trunk")
	newTip := runGit(t, f.root, "rev-parse", TrunkRef)
	if newTip == initialTip {
		t.Fatal("trunk did not advance")
	}

	beforeSync := l.actors.Sync.Runs.Load()
	beforeTrunk := l.actors.TrunkProof.Runs.Load()
	beforeGC := l.actors.GC.Runs.Load()

	l.signalSyncWake()
	l.signalTrunkProofWake()
	l.signalGCWake()

	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		if l.actors.Sync.Runs.Load() > beforeSync &&
			l.actors.TrunkProof.Runs.Load() > beforeTrunk &&
			l.actors.GC.Runs.Load() > beforeGC &&
			l.cachedTrunkTip() == newTip {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if l.actors.Sync.Runs.Load() <= beforeSync {
		t.Fatal("sync actor did not run after wake")
	}
	if l.actors.TrunkProof.Runs.Load() <= beforeTrunk {
		t.Fatal("trunk-proof actor did not run after wake")
	}
	if l.actors.GC.Runs.Load() <= beforeGC {
		t.Fatal("gc actor did not run after wake")
	}
	if got := l.cachedTrunkTip(); got != newTip {
		t.Fatalf("trunk-proof actor cached tip = %q, want %q", got, newTip)
	}

	// The trunk cache update must wake the scheduling pass.
	timer := time.NewTimer(hangGuard)
	defer timer.Stop()
	select {
	case <-l.actors.PassWakeChan():
	case <-timer.C:
		t.Fatal("trunk-proof actor did not signal passWake after trunk moved")
	}
}

// TestPurePassUsesCachedUnprovedBlockersWithoutGit verifies that unprovedBlockersPure
// computes unproved blocker holds purely from the cached rev-list set without
// spawning git during passPlansMode.
func TestPurePassUsesCachedUnprovedBlockersWithoutGit(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	ctx := context.Background()
	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Report = io.Discard
	l.gatePolicy.LaneBase = "green"

	base := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", GreenRef, base)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "unproved landing for item-a")
	unprovedTip := runGit(t, f.root, "rev-parse", TrunkRef)

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state = 'landed' WHERE item_id = 'item-a'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO item_edges(item_id, blocked_by, kind) VALUES ('item-b', 'item-a', 'blocked_by')`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO landings(landing_id, item_id, run_id, verdict_id, result, base_sha, head_sha, trunk_before, trunk_after, event_seq) VALUES ('l-1', 'item-a', 'run-a', 'v-1', 'landed', ?, ?, ?, ?, 1)`, base, unprovedTip, base, unprovedTip)
		return err
	})

	if _, err := l.refreshTrunkCache(ctx, unprovedTip); err != nil {
		t.Fatalf("refreshTrunkCache: %v", err)
	}
	if _, err := l.refreshSyncPlannerCaches(ctx); err != nil {
		t.Fatalf("refreshSyncPlannerCaches: %v", err)
	}
	l.actors.Active.Store(true)
	t.Cleanup(func() { l.actors.Active.Store(false) })

	// Block git so any accidental git call fails.
	t.Setenv("PATH", t.TempDir())

	var gen string
	plans, _, err := l.passPlansMode(ctx, l.cfg.Now(), &gen)
	if err != nil {
		t.Fatalf("passPlansMode with unproved blocker: %v", err)
	}
	for _, p := range plans {
		if p.decision.ItemID == "item-b" {
			if p.decision.Action != ActionHold || !strings.Contains(p.decision.Reason, "unproved") {
				t.Fatalf("item-b decision = %+v, want hold on unproved blocker", p.decision)
			}
			return
		}
	}
	t.Fatalf("item-b not found in plans: %+v", plans)
}

type purePassSlotSteps struct {
	recordingSteps
	fixture refusalLoopFixture
	now     func() time.Time
	mu      sync.Mutex
	started []string
}

func (s *purePassSlotSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	wt := filepath.Join(s.fixture.laneRoot, req.LaneID, req.WorkingDir)
	file := filepath.Join("tools", req.ItemID, "work.txt")
	if err := os.MkdirAll(filepath.Dir(filepath.Join(wt, file)), 0o755); err != nil {
		return run.Record{}, "", err
	}
	if err := os.WriteFile(filepath.Join(wt, file), []byte(req.ItemID+"\n"), 0o644); err != nil {
		return run.Record{}, "", err
	}
	for _, args := range [][]string{{"add", file}, {"commit", "-m", req.ItemID + " work"}} {
		if _, err := heavyGitOutput(ctx, wt, args...); err != nil {
			return run.Record{}, "", err
		}
	}
	st, err := store.Open(ctx, s.fixture.storePath, true)
	if err != nil {
		return run.Record{}, "", err
	}
	defer st.Close()
	exit := 0
	stamp := s.now().UTC().Format(time.RFC3339Nano)
	rec := run.Record{
		RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, ExitCode: &exit, StartedAt: stamp, FinishedAt: stamp,
		ArtifactRefs: []run.ArtifactRef{},
	}
	if err := st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) }); err != nil {
		return rec, "", err
	}
	s.mu.Lock()
	s.started = append(s.started, req.ItemID)
	s.mu.Unlock()
	return rec, "", nil
}

// TestPurePassSlotRefillAndNoNotifyParity proves structurally (without
// wall-clock assertions, per D-GR47 and D-SN10) that notifications drive
// single-slot refill across two queued items even with a non-firing fallback
// poll timer, that drain_passes records stage_ms == 0, and that --no-notify
// produces identical recorded outcomes (D-SN6, D-SN7).
func TestPurePassSlotRefillAndNoNotifyParity(t *testing.T) {
	runCase := func(t *testing.T, noNotify bool, poll time.Duration) []string {
		t.Helper()
		t.Setenv("TILLR_LOOP_STORE", "")
		f := newRefusalLoopFixtureWithLanes(t, 1, "item-a", "item-b")
		steps := &purePassSlotSteps{fixture: f, now: time.Now}
		l := f.loop(t, steps, settlingStartStager{f.laneRoot})
		l.cfg.Lanes = []string{"lane-1"}
		l.cfg.Now = time.Now
		l.cfg.Report = io.Discard
		l.cfg.NoNotify = noNotify
		l.cfg.StorePollInterval = poll
		l.cfg.ExitWhenDrained = true
		l.cfg.QuestionsCmd = []string{"sh", "-c", `printf '%s' '{"index":{"documents":[]}}'`}
		holdWALKeeper(t, f.storePath)

		if !noNotify {
			relayCtx, relayCancel := context.WithCancel(context.Background())
			relayDone := make(chan error, 1)
			go func() {
				relayDone <- notify.Serve(relayCtx, notify.ServeConfig{
					StorePath: f.storePath,
					IdleExit:  time.Minute,
				})
			}()
			t.Cleanup(func() {
				relayCancel()
				<-relayDone
			})
		}

		ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
		defer cancel()
		res, err := l.Run(ctx)
		if err != nil {
			t.Fatalf("Loop.Run(noNotify=%v): %v (res=%+v)", noNotify, err, res)
		}
		if res.Stop != StopQueueDrained {
			t.Fatalf("stop = %q, want %q (detail=%q)", res.Stop, StopQueueDrained, res.Detail)
		}
		if res.Landed != 2 {
			t.Fatalf("landed = %d, want 2 (res=%+v)", res.Landed, res)
		}

		// Verify drain_passes recorded stage_ms == 0 and collect final item states.
		st, err := store.OpenRead(ctx, f.storePath)
		if err != nil {
			t.Fatalf("OpenRead: %v", err)
		}
		defer st.Close()
		var states []string
		err = st.ReadTx(ctx, func(r *store.Reader) error {
			var maxStageMS int64
			if err := r.QueryRow(`SELECT COALESCE(MAX(stage_ms), 0) FROM drain_passes`).Scan(&maxStageMS); err != nil {
				return err
			}
			if maxStageMS != 0 {
				t.Errorf("drain_passes max(stage_ms) = %d, want 0", maxStageMS)
			}
			rows, err := r.Query(`SELECT item_id, state FROM items ORDER BY item_id`)
			if err != nil {
				return err
			}
			defer rows.Close()
			for rows.Next() {
				var id, state string
				if err := rows.Scan(&id, &state); err != nil {
					return err
				}
				states = append(states, id+":"+state)
			}
			return rows.Err()
		})
		if err != nil {
			t.Fatalf("ReadTx: %v", err)
		}
		return states
	}

	var notifyStates, pollStates []string
	// Non-firing 1-hour fallback poll proves notifications alone drive slot refill.
	t.Run("notify-enabled", func(t *testing.T) {
		notifyStates = runCase(t, false, time.Hour)
	})
	t.Run("no-notify-fallback", func(t *testing.T) {
		pollStates = runCase(t, true, 50*time.Millisecond)
	})
	if !slices.Equal(notifyStates, pollStates) {
		t.Fatalf("recorded item state parity mismatch: notify=%v, no-notify=%v", notifyStates, pollStates)
	}
}

func TestSeedActorCachesNonLandingSkipsGitAndVantage(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	ctx := context.Background()
	_, l := passFixture(t, "item-a")
	l.nonLanding = true
	l.cfg.Report = io.Discard
	l.cfg.QuestionsCmd = []string{"vantage-trap"}

	trapDir := t.TempDir()
	trapLog := filepath.Join(trapDir, "invoked.log")
	for _, bin := range []string{"git", "uvx", "vantage-trap"} {
		script := "#!/bin/sh\necho " + bin + " \"$@\" >> " + trapLog + "\nexit 99\n"
		if err := os.WriteFile(filepath.Join(trapDir, bin), []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", trapDir)

	if err := l.seedActorCaches(ctx, &LoopResult{}); err != nil {
		t.Fatalf("seedActorCaches(nonLanding): %v", err)
	}
	if invoked, err := os.ReadFile(trapLog); err == nil && len(invoked) > 0 {
		t.Fatalf("non-landing seedActorCaches invoked external subprocesses:\n%s", invoked)
	}
}

func TestRefreshSyncPlannerCachesSkipsFinishedDesignDocs(t *testing.T) {
	t.Setenv("TILLR_LOOP_STORE", "")
	ctx := context.Background()
	f, l := passFixture(t, "item-a", "item-done")
	l.cfg.Report = io.Discard
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state = 'landed', design_doc = 'docs/design/done.md' WHERE item_id = 'item-done'`)
		return err
	})
	countFile := filepath.Join(t.TempDir(), "vantage-calls")
	l.cfg.QuestionsCmd = []string{"sh", "-c", `echo x >> "$1"; printf '{"index":{"documents":[]}}'`, "_", countFile}

	if _, err := l.refreshSyncPlannerCaches(ctx); err != nil {
		t.Fatalf("refreshSyncPlannerCaches: %v", err)
	}
	if raw, err := os.ReadFile(countFile); err == nil && len(raw) > 0 {
		t.Fatalf("refreshSyncPlannerCaches invoked QuestionsCmd for finished item design_doc")
	}
}
