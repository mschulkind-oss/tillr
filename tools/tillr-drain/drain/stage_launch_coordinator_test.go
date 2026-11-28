package drain

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/stagelaunch"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestStageAndLaunchCoordinatorsSeparateStepsAndZeroPassStageMS(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.ExitWhenDrained = true
	l.cfg.StorePollInterval = 25 * time.Millisecond

	var mu sync.Mutex
	stagedOrder := map[string]int64{}
	launched := map[string]bool{}
	l.cfg.Stage = stagerFunc(func(_ context.Context, spec StageSpec) error {
		// Sleep briefly so any synchronous pass staging would show stage_ms > 0.
		time.Sleep(15 * time.Millisecond)
		mu.Lock()
		stagedOrder[spec.RunID] = time.Now().UnixMilli()
		mu.Unlock()
		return nil
	})
	l.cfg.Steps = &stageLaunchTestSteps{
		store: f.storePath,
		onRun: func(req run.Request) {
			mu.Lock()
			launched[req.RunID] = true
			mu.Unlock()
		},
	}

	res, err := l.Run(ctx)
	if err != nil {
		t.Fatalf("Loop.Run: %v", err)
	}
	if res.Stop != StopQueueDrained {
		t.Fatalf("Loop.Run stop = %q (detail=%q), want %q", res.Stop, res.Detail, StopQueueDrained)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		stagedEvents, err := r.EventsOfKind(0, dispatchStagedKind)
		if err != nil {
			return err
		}
		if len(stagedEvents) < 2 {
			t.Fatalf("drain.staged count = %d, want >= 2", len(stagedEvents))
		}
		for _, ev := range stagedEvents {
			var payload dispatchStaged
			if err := json.Unmarshal(ev.Payload, &payload); err != nil {
				return err
			}
			if payload.RunID == "" || payload.WorktreePath == "" || payload.BaseSHA == "" {
				t.Errorf("incomplete drain.staged payload: %+v", payload)
			}
			if payload.RequestPath == "" {
				t.Errorf("drain.staged missing request_path for %s", payload.RunID)
			} else if _, err := os.Stat(payload.RequestPath); err != nil {
				t.Errorf("rendered run.request %s missing: %v", payload.RequestPath, err)
			}
			mu.Lock()
			wasLaunched := launched[payload.RunID]
			mu.Unlock()
			if !wasLaunched {
				t.Errorf("staged run %s was never launched", payload.RunID)
			}
		}

		rows, err := r.Query(`SELECT tick_id, stage_ms FROM drain_passes`)
		if err != nil {
			return err
		}
		defer rows.Close()
		passes := 0
		for rows.Next() {
			var tickID string
			var stageMS int64
			if err := rows.Scan(&tickID, &stageMS); err != nil {
				return err
			}
			passes++
			if stageMS != 0 {
				t.Errorf("drain_passes %s stage_ms = %d, want 0 (staging must run outside the pass)", tickID, stageMS)
			}
		}
		if passes == 0 {
			t.Errorf("expected at least one drain_passes row")
		}
		return rows.Err()
	})
}

func TestStageCoordinatorRunsItemsInParallel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	f, l := passFixture(t, "item-a", "item-b")
	l.cfg.Lanes = []string{"lane-1", "lane-2"}
	l.cfg.StorePollInterval = 25 * time.Millisecond

	var inStage atomic.Int32
	bothInStage := make(chan struct{})
	var closeOnce sync.Once
	l.cfg.Stage = stagerFunc(func(ctx context.Context, _ StageSpec) error {
		if inStage.Add(1) >= 2 {
			closeOnce.Do(func() { close(bothInStage) })
		}
		select {
		case <-bothInStage:
			return nil
		case <-ctx.Done():
			return ctx.Err()
		}
	})

	plans := mustPlan(t, l)
	if len(plans) < 2 {
		t.Fatalf("expected 2 dispatch plans, got %d", len(plans))
	}
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	at := l.cfg.Now().UTC()
	for _, p := range plans {
		if ok, _, err := l.applyPlan(ctx, p, base, at); !ok || err != nil {
			t.Fatalf("applyPlan %s: ok=%v err=%v", p.decision.ItemID, ok, err)
		}
	}

	var result LoopResult
	stopStage := l.startStageCoordinator(ctx, &result)
	defer stopStage()

	select {
	case <-bothInStage:
	case <-time.After(hangGuard):
		t.Fatal("stage coordinator did not stage two items concurrently")
	}

	// Wait for both drain.staged events to commit.
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		var count int
		fixtureRead(t, f, func(r *store.Reader) error {
			events, err := r.EventsOfKind(0, dispatchStagedKind)
			count = len(events)
			return err
		})
		if count >= 2 {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatal("timed out waiting for both drain.staged events")
}

func TestStageCoordinatorLockExclusionAndIdempotency(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Stage = stagerFunc(func(context.Context, StageSpec) error { return nil })

	plan := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	at := l.cfg.Now().UTC()
	ok, work, err := l.applyPlan(ctx, plan, base, at)
	if !ok || err != nil || work == nil {
		t.Fatalf("applyPlan: ok=%v work=%+v err=%v", ok, work, err)
	}

	// Hold <worktree>.stage.lock externally: stageAssignedRun must skip cleanly
	// without retracting the assignment or committing drain.staged.
	unlock, acquired, err := stagelaunch.AcquireStageLock(l.lanePath(work.Decision.LaneID))
	if err != nil || !acquired {
		t.Fatalf("acquireStageLock: acquired=%v err=%v", acquired, err)
	}
	var result LoopResult
	ready, cancelled, err := l.stageAssignedRun(ctx, work.Decision, work.BaseSHA, at, &result)
	if err != nil || ready != nil || cancelled {
		unlock()
		t.Fatalf("contended stageAssignedRun = ready=%v cancelled=%v err=%v; want nil, false, nil", ready != nil, cancelled, err)
	}
	unlock()

	// Now stage succeeds and commits drain.staged once.
	ready, cancelled, err = l.stageAssignedRun(ctx, work.Decision, work.BaseSHA, at, &result)
	if err != nil || ready == nil || cancelled {
		t.Fatalf("uncontended stageAssignedRun = ready=%v cancelled=%v err=%v; want non-nil, false, nil", ready != nil, cancelled, err)
	}

	// Second recordDispatchStaged for the same run_id is a guarded no-op.
	committed, err := l.recordDispatchStaged(ctx, *ready, work.BaseSHA, filepath.Join(l.cfg.LaneRoot, ".requests", ready.RunRequest.RunID+".request.json"), at)
	if err != nil || committed {
		t.Fatalf("duplicate recordDispatchStaged = committed=%v err=%v; want false, nil", committed, err)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		events, err := r.EventsOfKind(0, dispatchStagedKind)
		if err != nil {
			return err
		}
		if len(events) != 1 {
			t.Fatalf("drain.staged count = %d, want 1", len(events))
		}
		return nil
	})
}

func TestRetryGitConfigLockBackoff(t *testing.T) {
	ctx := context.Background()
	attempts := 0
	err := stagelaunch.RetryGitConfigLock(ctx, waitFor, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("error: could not lock config file /repo/.git/config.lock: File exists")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("retryGitConfigLock = err=%v attempts=%d; want nil, 3", err, attempts)
	}

	nonLockAttempts := 0
	sentinel := errors.New("unrelated git failure")
	err = stagelaunch.RetryGitConfigLock(ctx, waitFor, func() error {
		nonLockAttempts++
		return sentinel
	})
	if !errors.Is(err, sentinel) || nonLockAttempts != 1 {
		t.Fatalf("non-lock error = err=%v attempts=%d; want sentinel, 1", err, nonLockAttempts)
	}
}

func TestPreExistingRunsRowImplicitlyStaged(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	f, l := passFixture(t, "item-a")
	l.cfg.Lanes = []string{"lane-1"}

	plan := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	at := l.cfg.Now().UTC()
	ok, work, err := l.applyPlan(ctx, plan, base, at)
	if !ok || err != nil || work == nil {
		t.Fatalf("applyPlan: ok=%v work=%+v err=%v", ok, work, err)
	}

	// Simulate a pre-existing store where runs already has a row for this run_id
	// and no drain.staged event exists (design Section 7.3).
	rec := run.Record{
		RunID: work.Decision.RunRequest.RunID, ItemID: work.Decision.ItemID, LaneID: work.Decision.LaneID,
		Backend: work.Decision.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0),
		StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339),
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		return applyTerminal(tx, rec)
	})

	stageable, err := l.findStageableRuns(ctx)
	if err != nil {
		t.Fatalf("findStageableRuns: %v", err)
	}
	if len(stageable) != 0 {
		t.Fatalf("findStageableRuns returned %d runs for pre-existing runs row, want 0", len(stageable))
	}
	launchable, err := l.findLaunchableRuns(ctx)
	if err != nil {
		t.Fatalf("findLaunchableRuns: %v", err)
	}
	if len(launchable) != 0 {
		t.Fatalf("findLaunchableRuns returned %d runs for pre-existing runs row, want 0", len(launchable))
	}
}

func TestRestartedParallelSiblingStagesAndLaunchesWhenPrimaryFinished(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()

	f, l, _ := parallelFixture(t)
	plan := mustPlan(t, l)[0]
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	at := l.cfg.Now().UTC()
	ok, work, err := l.applyPlan(ctx, plan, base, at)
	if !ok || err != nil || work == nil {
		t.Fatalf("applyPlan: ok=%v work=%+v err=%v", ok, work, err)
	}
	if len(work.Decision.Parallel) == 0 {
		t.Fatalf("expected parallel decision, got %+v", work.Decision)
	}

	// Mark nodes[0] as already finished in runs and already launched in-process,
	// while assigning a fresh -restart RunID to a sibling reader.
	restarted := work.Decision
	restarted.WorkflowRestart = true
	restarted.Parallel = append([]Decision(nil), work.Decision.Parallel...)
	siblingReq := *restarted.Parallel[0].RunRequest
	siblingReq.RunID = markRunID(siblingReq.RunID, "restart")
	restarted.Parallel[0].RunRequest = &siblingReq
	restarted.Parallel[0].WorkflowRestart = true

	primary := restarted.RunRequest
	l.mu.Lock()
	l.stageLaunch.MarkLaunched(primary.RunID)
	l.mu.Unlock()

	fixtureWrite(t, f, func(tx *store.Tx) error {
		rec := run.Record{
			RunID: primary.RunID, ItemID: restarted.ItemID, LaneID: restarted.LaneID,
			Backend: primary.Backend, State: run.StateExited, ExitCode: countPtr(0),
			StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339),
		}
		if err := applyTerminal(tx, rec); err != nil {
			return err
		}
		return appendEffect(tx, dispatchStartedKind, primary.RunID, &pendingWork{
			Decision: restarted, BaseSHA: base,
		}, at)
	})

	stageable, err := l.findStageableRuns(ctx)
	if err != nil {
		t.Fatalf("findStageableRuns: %v", err)
	}
	if len(stageable) != 1 || stageable[0].runID != primary.RunID {
		t.Fatalf("findStageableRuns = %+v, want 1 run for %s", stageable, primary.RunID)
	}

	var result LoopResult
	ready, cancelled, err := l.stageAssignedRun(ctx, restarted, base, at, &result)
	if err != nil || cancelled || ready == nil {
		t.Fatalf("stageAssignedRun = ready=%v cancelled=%v err=%v; want non-nil, false, nil", ready != nil, cancelled, err)
	}

	launchable, err := l.findLaunchableRuns(ctx)
	if err != nil {
		t.Fatalf("findLaunchableRuns: %v", err)
	}
	if len(launchable) != 1 || launchable[0].runID != primary.RunID {
		t.Fatalf("findLaunchableRuns = %+v, want 1 run for %s", launchable, primary.RunID)
	}

	// Once every sibling reader also has a runs row, the parallel group is no
	// longer launchable.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		for _, node := range parallelNodes(restarted)[1:] {
			rec := run.Record{
				RunID: node.RunRequest.RunID, ItemID: node.ItemID, LaneID: node.LaneID,
				Backend: node.RunRequest.Backend, State: run.StateExited, ExitCode: countPtr(0),
				StartedAt: at.Format(time.RFC3339), FinishedAt: at.Format(time.RFC3339),
			}
			if err := applyTerminal(tx, rec); err != nil {
				return err
			}
		}
		return nil
	})

	launchable, err = l.findLaunchableRuns(ctx)
	if err != nil {
		t.Fatalf("findLaunchableRuns after all siblings recorded: %v", err)
	}
	if len(launchable) != 0 {
		t.Fatalf("findLaunchableRuns returned %d runs after all siblings recorded, want 0", len(launchable))
	}
}

func TestStageAndLaunchParityWithNoNotify(t *testing.T) {
	runScenario := func(noNotify bool) (startedCount, stagedCount, runCount int) {
		ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
		defer cancel()

		f, l := passFixture(t, "item-a", "item-b")
		l.cfg.Lanes = []string{"lane-1", "lane-2"}
		l.cfg.ExitWhenDrained = true
		l.cfg.NoNotify = noNotify
		l.cfg.StorePollInterval = 20 * time.Millisecond
		l.cfg.Stage = stagerFunc(func(context.Context, StageSpec) error { return nil })
		l.cfg.Steps = &stageLaunchTestSteps{store: f.storePath}

		res, err := l.Run(ctx)
		if err != nil {
			t.Fatalf("Loop.Run(noNotify=%v): %v", noNotify, err)
		}
		if res.Stop != StopQueueDrained {
			t.Fatalf("Loop.Run(noNotify=%v) stop = %q, want %q", noNotify, res.Stop, StopQueueDrained)
		}

		fixtureRead(t, f, func(r *store.Reader) error {
			started, err := r.EventsOfKind(0, dispatchStartedKind)
			if err != nil {
				return err
			}
			staged, err := r.EventsOfKind(0, dispatchStagedKind)
			if err != nil {
				return err
			}
			startedCount = len(started)
			stagedCount = len(staged)
			return r.QueryRow(`SELECT COUNT(*) FROM runs`).Scan(&runCount)
		})
		return startedCount, stagedCount, runCount
	}

	sOn, stOn, rOn := runScenario(false)
	sOff, stOff, rOff := runScenario(true)
	if sOn == 0 || stOn == 0 || rOn == 0 {
		t.Fatalf("notify-on produced empty counts: started=%d staged=%d runs=%d", sOn, stOn, rOn)
	}
	if sOn != sOff || stOn != stOff || rOn != rOff {
		t.Fatalf("parity mismatch: notify-on=(started=%d staged=%d runs=%d) vs no-notify=(started=%d staged=%d runs=%d)",
			sOn, stOn, rOn, sOff, stOff, rOff)
	}
}

type stageLaunchTestSteps struct {
	stubSteps
	store string
	onRun func(run.Request)
}

func (s *stageLaunchTestSteps) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if s.onRun != nil {
		s.onRun(req)
	}
	now := time.Now().UTC().Format(time.RFC3339)
	rec := run.Record{
		RunID: req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Backend: req.Backend,
		State: run.StateExited, ExitCode: countPtr(0), StartedAt: now, FinishedAt: now,
	}
	if s.store != "" {
		st, err := store.Open(ctx, s.store, true)
		if err != nil {
			return run.Record{}, "", err
		}
		err = st.WriteTx(ctx, func(tx *store.Tx) error { return applyTerminal(tx, rec) })
		st.Close()
		if err != nil {
			return run.Record{}, "", err
		}
	}
	return rec, "RESULT: done\n", nil
}

func TestStageAssignedRunRequiresLaneRoot(t *testing.T) {
	dir := t.TempDir()
	t.Chdir(dir)
	l := &Loop{cfg: LoopConfig{Root: dir}}
	d := Decision{
		ItemID: "item-a",
		LaneID: "lane-1",
		RunRequest: &run.Request{
			ItemID:  "item-a",
			RunID:   "run-item-a-a1",
			Backend: "fixture",
			Command: []string{"true"},
		},
	}
	var result LoopResult
	ready, cancelled, err := l.stageAssignedRun(context.Background(), d, "deadbeef", time.Date(2026, 11, 28, 0, 0, 0, 0, time.UTC), &result)
	if err == nil || !strings.Contains(err.Error(), "LaneRoot") {
		t.Fatalf("stageAssignedRun without LaneRoot = ready=%v cancelled=%v err=%v, want LaneRoot usage error", ready != nil, cancelled, err)
	}
	if _, statErr := os.Stat(filepath.Join(dir, ".requests")); !os.IsNotExist(statErr) {
		t.Fatalf("stageAssignedRun without LaneRoot created .requests in cwd: %v", statErr)
	}
	if _, statErr := os.Stat(filepath.Join(dir, d.LaneID)); !os.IsNotExist(statErr) {
		t.Fatalf("stageAssignedRun without LaneRoot created lane directory in cwd: %v", statErr)
	}
}
