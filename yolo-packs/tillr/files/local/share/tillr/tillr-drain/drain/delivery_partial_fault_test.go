package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

type partialFaultSteps struct {
	*workflowSteps
	mu            sync.Mutex
	failFirstLand bool
	landAttempts  int
	landings      []string
	runsOn        []string
	runIDs        []string
	forkRunID     string
}

func (s *partialFaultSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	target := filepath.Join(s.f.laneRoot, req.LaneID, WorktreeDir)
	active, err := os.ReadFile(filepath.Join(s.f.laneRoot, req.LaneID, TargetFile))
	if err == nil {
		target = strings.TrimSpace(string(active))
	}
	s.mu.Lock()
	s.runsOn = append(s.runsOn, target)
	s.runIDs = append(s.runIDs, req.RunID)
	s.mu.Unlock()
	if req.NodeID == "classify" || req.NodeID == "plan" {
		dir := nodeOutputsDir(s.f.root, req.RunID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(dir, deliveryOutput), `{"kind":"both","fork":"pi"}`)
	} else {
		if err := os.MkdirAll(filepath.Join(target, "tools/"+req.ItemID), 0755); err != nil {
			return run.Record{}, "", err
		}
		writeForkFile(s.t, filepath.Join(target, "tools/"+req.ItemID+"/delivered.txt"), req.RunID+"\n")
		runGit(s.t, target, "add", "tools/"+req.ItemID+"/delivered.txt")
		runGit(s.t, target, "commit", "-qm", "delivery piece "+req.RunID)
	}
	report := "RESULT: done\n"
	path := filepath.Join(s.f.laneRoot, req.LaneID, "report.md")
	writeForkFile(s.t, path, report)
	rec := run.Record{
		RunID:        req.RunID,
		ItemID:       req.ItemID,
		LaneID:       req.LaneID,
		Backend:      req.Backend,
		State:        run.StateExited,
		ExitCode:     countPtr(0),
		StartedAt:    "2026-11-15T00:00:00Z",
		FinishedAt:   "2026-11-15T00:00:00Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}},
	}
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
	return rec, report, nil
}

func (s *partialFaultSteps) Gate(ctx context.Context, c Candidate) (GateOutcome, error) {
	fixtureWrite(s.t, s.f, func(tx *store.Tx) error {
		return appendEffect(tx, GateVerdictKind, c.RunID, map[string]any{
			"verdict_id": "v-" + c.RunID,
			"run_id":     c.RunID,
			"result":     "pass",
		}, time.Date(2026, 11, 15, 0, 1, 0, 0, time.UTC))
	})
	return GateOutcome{Outcome: GatePassed}, nil
}

func (s *partialFaultSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	s.mu.Lock()
	s.landAttempts++
	fail := s.failFirstLand && s.landAttempts == 1 && c.BaseRef == "refs/heads/patch/pi"
	if c.BaseRef == "refs/heads/patch/pi" {
		s.forkRunID = c.RunID
	}
	s.mu.Unlock()
	if fail {
		return LandOutcome{}, Externalf("simulated lander harness error")
	}
	s.mu.Lock()
	s.landings = append(s.landings, c.BaseRef)
	s.mu.Unlock()
	if c.BaseRef == "refs/heads/patch/pi" {
		runGit(s.t, s.f.root, "fetch", "--no-tags", c.Worktree, "refs/heads/main:"+c.BaseRef)
		fixtureLanding(s.t, s.f.storePath, c)
		return LandOutcome{Result: LandLanded, TrunkAfter: c.HeadSHA}, nil
	}
	return s.workflowSteps.Land(ctx, c)
}

func setupMixedForkFixture(t *testing.T) (refusalLoopFixture, *Loop, *partialFaultSteps) {
	t.Helper()
	f, l, steps := workflowLoopFixtureWithLanes(t, 3, "")
	l.cfg.Lanes = []string{"lane-1", "lane-2", "lane-3"}
	l.cfg.Policy.Seats["classify"] = "fixture"
	if err := os.Mkdir(filepath.Join(f.root, "bin"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(f.root, "bin/pi-forks.list"), "upstream/pi patch/pi\n")
	writeForkFile(t, filepath.Join(f.root, ".gitignore"), ".forks/\n")
	runGit(t, f.root, "add", "bin/pi-forks.list", ".gitignore")
	runGit(t, f.root, "commit", "-qm", "fork catalog")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='landed' WHERE item_id='item-b'`); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET workflow='single-worker@v1',allowed_paths='["tools/item-a/"]' WHERE item_id='item-a'`)
		return err
	})
	for _, lane := range l.cfg.Lanes {
		fork := filepath.Join(f.root, ".forks/lanes", lane, "pi")
		if err := os.MkdirAll(fork, 0755); err != nil {
			t.Fatal(err)
		}
		runGit(t, fork, "init", "-qb", "main")
		writeForkFile(t, filepath.Join(fork, "fork.txt"), "base\n")
		runGit(t, fork, "add", "fork.txt")
		runGit(t, fork, "commit", "-qm", "fork base")
		if lane == "lane-1" {
			runGit(t, f.root, "fetch", "--no-tags", fork, "refs/heads/main:refs/heads/patch/pi")
		}
	}
	s := &partialFaultSteps{workflowSteps: steps}
	l.cfg.Steps = s
	return f, l, s
}

func TestPartialDeliveryAfterClearedLanderFaultDispatchesRepoPart(t *testing.T) {
	f, l, s := setupMixedForkFixture(t)
	s.failFirstLand = true
	l.cfg.ExitWhenDrained = false
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	var cleared bool
	go func() {
		ticker := time.NewTicker(50 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				if !cleared {
					var seq int64
					fixtureRead(t, f, func(r *store.Reader) error {
						faults, err := ReadItemFaults(r)
						if err != nil {
							return err
						}
						if h, ok := faults["item-a"]; ok && h.Reason == ReasonLandHarnessError {
							seq = h.Seq
						}
						return nil
					})
					if seq > 0 {
						cleared = true
						fixtureWrite(t, f, func(tx *store.Tx) error {
							return ClearItemFault(tx, "item-a", seq, l.cfg.Now().UTC())
						})
					}
					continue
				}
				s.mu.Lock()
				n := len(s.landings)
				s.mu.Unlock()
				if n >= 2 {
					cancel()
					return
				}
			}
		}
	}()

	res, err := l.Run(ctx)
	if len(s.landings) != 2 {
		t.Fatalf("want 2 landings (fork + repo), got %d: %v (err=%v, stop=%s, runs=%v)", len(s.landings), s.landings, err, res.Stop, s.runIDs)
	}
}

func TestStartItemSyncPreservesPartialDelivery(t *testing.T) {
	f, l, _ := setupMixedForkFixture(t)
	at := time.Date(2026, 11, 15, 0, 2, 0, 0, time.UTC)
	runID := "run-item-a-a1-n2"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		rec := run.Record{
			RunID: runID, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture",
			State: run.StateExited, ExitCode: countPtr(0),
			StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:01:00Z",
		}
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		if err := appendEffect(tx, GateVerdictKind, runID, map[string]any{
			"verdict_id": "v-" + runID, "run_id": runID, "result": "pass",
		}, at); err != nil {
			return err
		}
		return appendEffectOn(tx, deliveryPartialKind, "item", "item-a", deliveryDecision{
			Kind: "both", Fork: "pi", ForkLanded: "abc123", ForkRun: runID,
		}, at)
	})

	// Seed startStoreSync, then emit an item.update event so refreshItems runs
	// incrementally instead of reseeding.
	var syncState startStoreSync
	defer syncState.close()
	tick := l.startViewPass()
	questions := &VantageQuestions{Root: f.root}
	rebuild := func(r *store.Reader, next *startSnapshot, touched map[string]bool) (func() error, error) {
		return next.prepareRepair(context.Background(), r, at, l.currentPolicy(), tick, questions, startSnapshot{}, touched)
	}
	snap1, err := syncState.syncStore(context.Background(), f.storePath, at, l.currentPolicy(), startSnapshot{}, rebuild)
	if err != nil {
		t.Fatalf("seed syncStore: %v", err)
	}
	if got := snap1.entries["item-a"].decision; got.Action != ActionDispatch || got.Reason != ReasonReady {
		t.Fatalf("after seed: decision = %s/%s, want dispatch/ready", got.Action, got.Reason)
	}

	// Emit an item.update event for item-a so refreshItems runs incrementally.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		id := "item-a"
		_, err := tx.Append(event.Event{
			Kind: item.UpdateKind, ObjectType: "item", ObjectID: &id,
			Payload: []byte(`{"item_id":"item-a","state":"in-queue"}`), At: at.Add(time.Second),
		})
		return err
	})
	snap2, err := syncState.syncStore(context.Background(), f.storePath, at.Add(2*time.Second), l.currentPolicy(), snap1, rebuild)
	if err != nil {
		t.Fatalf("incremental syncStore: %v", err)
	}
	if d := snap2.itemsByID["item-a"].Delivery; d.Kind != "both" || d.ForkRun != runID {
		t.Fatalf("incremental sync dropped Delivery: %+v", d)
	}
	if got := snap2.entries["item-a"].decision; got.Action != ActionDispatch || got.Reason != ReasonReady {
		t.Fatalf("after item.update: decision = %s/%s, want dispatch/ready", got.Action, got.Reason)
	}
}

func TestRetryOnPassedGateNeedsNoGateFailedAckAndDispatches(t *testing.T) {
	f, l, s := setupMixedForkFixture(t)
	at := time.Date(2026, 11, 15, 0, 2, 0, 0, time.UTC)
	runID := "run-item-a-a1-n2"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		rec := run.Record{
			RunID: runID, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture",
			State: run.StateExited, ExitCode: countPtr(0),
			StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:01:00Z",
		}
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		if err := appendEffect(tx, GateVerdictKind, runID, map[string]any{
			"verdict_id": "v-" + runID, "run_id": runID, "result": "pass",
		}, at); err != nil {
			return err
		}
		return appendEffectOn(tx, deliveryPartialKind, "item", "item-a", deliveryDecision{
			Kind: "both", Fork: "pi", ForkLanded: "abc123", ForkRun: runID,
		}, at)
	})

	// Passing --acknowledge gate-failed on a run whose gate passed must be refused
	// and point to the bare retry command.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
			StorePath:   f.storePath,
			ItemID:      "item-a",
			RunID:       runID,
			By:          "operator",
			Why:         "dispatch repository piece",
			Acknowledge: RetryAckGateFailed,
			At:          at.Add(time.Minute),
		})
		if err == nil {
			t.Fatalf("AuthorizeRetry with --acknowledge gate-failed on a passed gate unexpectedly succeeded")
		}
		if !strings.Contains(err.Error(), "gate passed") || strings.Contains(err.Error(), "--acknowledge gate-failed") && strings.Contains(err.Error(), "accepted command: tillr drain retry --item item-a --run "+runID+" --by <who> --why <reason> --acknowledge gate-failed") {
			t.Fatalf("unexpected refusal message: %v", err)
		}
		return nil
	})

	// AuthorizeRetry on a run whose gate passed must succeed without --acknowledge gate-failed.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
			StorePath: f.storePath,
			ItemID:    "item-a",
			RunID:     runID,
			By:        "operator",
			Why:       "dispatch repository piece",
			At:        at.Add(time.Minute),
		})
		if err != nil {
			t.Fatalf("AuthorizeRetry on passed gate refused without --acknowledge gate-failed: %v", err)
		}
		return nil
	})

	// Why must plan the operator retry on a free lane, and Loop.Run must dispatch it.
	l.cfg.ExitWhenDrained = true
	res, err := l.Run(context.Background())
	if err != nil {
		t.Fatalf("Loop.Run after passed-gate retry: %v (res=%+v)", err, res)
	}
	if len(s.landings) != 1 || s.landings[0] != "" {
		t.Fatalf("want 1 repo landing after accepted operator retry, got %v (runs=%v)", s.landings, s.runIDs)
	}
}

func TestStartViewLocalHoldClearedByOperatorRetry(t *testing.T) {
	f, l, s := setupMixedForkFixture(t)
	at := time.Date(2026, 11, 15, 0, 2, 0, 0, time.UTC)
	runID := "run-item-a-a1-n2"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		rec := run.Record{
			RunID: runID, ItemID: "item-a", LaneID: "lane-1", Backend: "fixture",
			State: run.StateExited, ExitCode: countPtr(0),
			StartedAt: "2026-11-15T00:00:00Z", FinishedAt: "2026-11-15T00:01:00Z",
		}
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		if err := appendEffect(tx, GateVerdictKind, runID, map[string]any{
			"verdict_id": "v-" + runID, "run_id": runID, "result": "pass",
		}, at); err != nil {
			return err
		}
		return appendEffectOn(tx, deliveryPartialKind, "item", "item-a", deliveryDecision{
			Kind: "both", Fork: "pi", ForkLanded: "abc123", ForkRun: runID,
		}, at)
	})

	// Put item-a into startView's local holds directly (simulating a staging failure
	// during a running loop), then verify a subsequent operator retry clears it.
	view := newStartView()
	tick := l.startViewPass()
	questions := &VantageQuestions{Root: f.root}
	var syncState startStoreSync
	defer syncState.close()
	rebuild := func(r *store.Reader, next *startSnapshot, touched map[string]bool) (func() error, error) {
		return next.prepareRepair(context.Background(), r, at, l.currentPolicy(), tick, questions, startSnapshot{}, touched)
	}
	var snap startSnapshot
	stop := view.watch(context.Background(), time.Hour, nil, func(ctx context.Context, _ startWatchChange) (startSnapshot, error) {
		var err error
		snap, err = syncState.syncStore(ctx, f.storePath, at.Add(2*time.Minute), l.currentPolicy(), snap, rebuild)
		return snap, err
	})
	defer stop()
	<-view.updated
	hold := heldItem{reason: ReasonStagingFailed + ": lane-1", at: at, contentVersion: view.snapshot().contentVersions["item-a"]}
	view.noteItemHold("item-a", hold)
	// Drain the buffered notification published by noteItemHold itself.
	select {
	case <-view.updated:
	default:
	}
	if got := view.snapshot().entries["item-a"].decision; got.Action != ActionHold {
		t.Fatalf("expected local hold in startView, got %s/%s", got.Action, got.Reason)
	}

	// Authorize an operator retry after the hold timestamp.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := AuthorizeRetry(context.Background(), tx, RetryRequest{
			StorePath: f.storePath,
			ItemID:    "item-a",
			RunID:     runID,
			By:        "operator",
			Why:       "retry after staging hold",
			At:        at.Add(time.Minute),
		})
		return err
	})

	// Trigger a refresh on startView and verify the retry cleared v.holds["item-a"].
	select {
	case view.laneTargetChanged <- struct{}{}:
	default:
	}
	select {
	case <-view.updated:
	case <-time.After(hangGuard):
		t.Fatal("timed out waiting for startView refresh after retry")
	}
	if got := view.snapshot().entries["item-a"].decision; got.Action != ActionRetry {
		t.Fatalf("after operator retry: startView decision = %s/%s, want retry", got.Action, got.Reason)
	}
	stop()

	// Run with startView active (pollOnly = false): the accepted retry must dispatch and land.
	l.cfg.Now = func() time.Time { return at.Add(2 * time.Minute) }
	l.cfg.ExitWhenDrained = true
	res, err := l.Run(context.Background())
	if err != nil {
		t.Fatalf("Loop.Run after retry clearing local hold: %v (res=%+v)", err, res)
	}
	if len(s.landings) != 1 || s.landings[0] != "" {
		t.Fatalf("want 1 repo landing after retry clears local hold, got %v (runs=%v)", s.landings, s.runIDs)
	}
}
