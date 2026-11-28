package drain

import (
	"context"
	"encoding/json"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-drain/internal/stagelaunch"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// TestWorktreeCollectLeavesStagedOrRunningAndSeatWaitingItemsAlone verifies:
//   - An item blocked waiting on a seat (e.g. design) is not collected as wait-owner.
//   - An item with a staged or running run (even after an earlier run ended waiting on
//     a seat, and even when the worktree count bound is exceeded) keeps its worktree
//     and scratch untouched.
//   - An item whose directory holds <worktree>.stage.lock is skipped by collection.
func TestWorktreeCollectLeavesStagedOrRunningAndSeatWaitingItemsAlone(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t)
	l, _ := newWorktreeCollectTestLoop(t, f)
	l.cfg.WorktreeCountOverride = 10

	// 1. Item waiting on the design seat (no run staged yet): must NOT be collected as wait-owner.
	_, wtDesignWait, scDesignWait, _, _ := stageFixtureItemDir(t, f, "item-design-wait", "run-design-prev")
	// 2. Item whose earlier run ended waiting on design, and now has a running run in store.
	_, wtDesignRunning, scDesignRunning, _, _ := stageFixtureItemDir(t, f, "item-design-running", "run-design-r1")
	// 3. Item whose worktree currently holds the stage lock (Step 1 staging in progress).
	_, wtStageLocked, scStageLocked, _, _ := stageFixtureItemDir(t, f, "item-stage-locked", "run-stage-prev")
	// 4. Control: item waiting on owner IS still collected immediately.
	_, wtOwnerWait, scOwnerWait, _, _ := stageFixtureItemDir(t, f, "item-owner-wait", "run-owner-prev")

	now := l.cfg.Now().UnixMilli()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='blocked', waiting_on='{"waiter":"design","reason":"conflict needs design answer"}' WHERE item_id IN ('item-design-wait','item-design-running')`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE items SET state='blocked', waiting_on='{"waiter":"owner","reason":"needs ruling"}' WHERE item_id IN ('item-stage-locked','item-owner-wait')`); err != nil {
			return err
		}
		// Record a new non-terminal run for item-design-running.
		if _, err := tx.Exec(
			`INSERT INTO runs(run_id, item_id, lane_id, backend, state, started_at, finished_at) VALUES(?, ?, ?, ?, ?, ?, 0)`,
			"run-design-r2", "item-design-running", "item-design-running", "claude-designer", "running", now,
		); err != nil {
			return err
		}
		return nil
	})

	unlockStage, acquired, err := stagelaunch.AcquireStageLock(wtStageLocked)
	if err != nil || !acquired {
		t.Fatalf("AcquireStageLock: acquired=%v err=%v", acquired, err)
	}
	defer unlockStage()

	// First check with high count bound: only immediate wait-collection triggers.
	l.cfg.WorktreeCountOverride = 10
	if _, err := l.collectIdleWorktrees(ctx); err != nil {
		t.Fatalf("collectIdleWorktrees (high bound): %v", err)
	}

	// Control: owner-waiting item was collected.
	for _, p := range []string{wtOwnerWait, scOwnerWait} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("expected owner-waiting %s to be collected, stat err=%v", p, err)
		}
	}
	// Seat-waiting, running, and stage-locked items must all remain untouched.
	for _, p := range []string{
		wtDesignWait, scDesignWait,
		wtDesignRunning, scDesignRunning,
		wtStageLocked, scStageLocked,
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected %s to remain untouched, stat err=%v", p, err)
		}
	}

	// Now tighten count bound to 1: item-design-running (running run) and item-stage-locked
	// (stage lock held) must STILL remain untouched.
	l.cfg.WorktreeCountOverride = 1
	if _, err := l.collectIdleWorktrees(ctx); err != nil {
		t.Fatalf("collectIdleWorktrees (tight bound): %v", err)
	}
	for _, p := range []string{
		wtDesignRunning, scDesignRunning,
		wtStageLocked, scStageLocked,
	} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected in-use/staged %s to remain untouched under tight bound, stat err=%v", p, err)
		}
	}
}

// TestMissingWorktreeRunDoesNotCloseBackend verifies that a run ending because its
// worktree is missing (getcwd/chdir error or worktree collected during the run)
// does not close its backend, while an unrelated infrastructure failure still does.
func TestMissingWorktreeRunDoesNotCloseBackend(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t, "item-a")
	l, _ := newWorktreeCollectTestLoop(t, f)
	now := l.cfg.Now()
	exitOne := 1

	recordFinish := func(tx *store.Tx, rec run.Record, at time.Time) error {
		payload, err := json.Marshal(rec)
		if err != nil {
			return err
		}
		id := rec.RunID
		_, err = tx.Append(event.Event{
			Kind:       run.FinishKind,
			ObjectType: "run",
			ObjectID:   &id,
			Payload:    payload,
			At:         at,
		})
		return err
	}

	fixtureWrite(t, f, func(tx *store.Tx) error {
		// 1. Run failed with getcwd error after its worktree was deleted.
		if err := recordFinish(tx, run.Record{
			RunID:      "run-getcwd",
			ItemID:     "item-a",
			LaneID:     "item-a",
			Backend:    "claude-designer",
			State:      run.StateExited,
			ExitCode:   &exitOne,
			StderrTail: "sh: 0: getcwd() failed: No such file or directory\nshell-init: error retrieving current directory: getcwd: cannot access parent directories: No such file or directory\n",
			StartedAt:  now.Format(time.RFC3339),
			FinishedAt: now.Add(time.Second).Format(time.RFC3339),
		}, now.Add(time.Second)); err != nil {
			return err
		}

		// 2. Run failed to start because worktree directory was missing (chdir ENOENT).
		if err := recordFinish(tx, run.Record{
			RunID:      "run-chdir",
			ItemID:     "item-a",
			LaneID:     "item-a",
			Backend:    "claude-worker",
			State:      run.StateFailedToStart,
			StderrTail: "chdir /workspace/.tillr/lanes/item-a/worktree: no such file or directory",
			StartedAt:  now.Add(2 * time.Second).Format(time.RFC3339),
			FinishedAt: now.Add(2 * time.Second).Format(time.RFC3339),
		}, now.Add(2*time.Second)); err != nil {
			return err
		}

		// 3. Run whose worktree was collected by drain.worktree-collected while in flight.
		work := pendingWork{
			Decision: Decision{
				Action: ActionDispatch,
				ItemID: "item-a",
				LaneID: "item-a",
				RunRequest: &run.Request{
					RunID:   "run-collected",
					ItemID:  "item-a",
					LaneID:  "item-a",
					Backend: "gemini-designer",
				},
			},
		}
		if err := appendEffect(tx, dispatchStartedKind, "run-collected", work, now.Add(3*time.Second)); err != nil {
			return err
		}
		colPayload, err := json.Marshal(WorktreeCollected{
			ItemID:  "item-a",
			LaneID:  "item-a",
			Reason:  "wait-owner",
			Deleted: []string{"worktree", "scratch"},
		})
		if err != nil {
			return err
		}
		itemObj := "item-a"
		if _, err := tx.Append(event.Event{
			Kind:       worktreeCollectedKind,
			ObjectType: "item",
			ObjectID:   &itemObj,
			Payload:    colPayload,
			At:         now.Add(4 * time.Second),
		}); err != nil {
			return err
		}
		if err := recordFinish(tx, run.Record{
			RunID:      "run-collected",
			ItemID:     "item-a",
			LaneID:     "item-a",
			Backend:    "gemini-designer",
			State:      run.StateExited,
			ExitCode:   &exitOne,
			StderrTail: "exited abnormally",
			StartedAt:  now.Add(3 * time.Second).Format(time.RFC3339),
			FinishedAt: now.Add(5 * time.Second).Format(time.RFC3339),
		}, now.Add(5*time.Second)); err != nil {
			return err
		}

		// 4. Control: normal infrastructure failure DOES close its backend.
		return recordFinish(tx, run.Record{
			RunID:      "run-real-infra",
			ItemID:     "item-a",
			LaneID:     "item-a",
			Backend:    "broken-backend",
			State:      run.StateExited,
			ExitCode:   &exitOne,
			StderrTail: "API returned HTTP 500 internal server error",
			StartedAt:  now.Add(6 * time.Second).Format(time.RFC3339),
			FinishedAt: now.Add(6 * time.Second).Format(time.RFC3339),
		}, now.Add(6*time.Second))
	})

	if err := RecordBackendOutcomes(ctx, f.storePath); err != nil {
		t.Fatalf("RecordBackendOutcomes: %v", err)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		closed, err := readInfrastructureClosed(r)
		if err != nil {
			return err
		}
		for _, openBackend := range []string{"claude-designer", "claude-worker", "gemini-designer"} {
			if _, isClosed := closed[openBackend]; isClosed {
				t.Errorf("backend %q was closed by a missing-worktree run; closed=%v", openBackend, closed)
			}
		}
		if _, isClosed := closed["broken-backend"]; !isClosed {
			t.Errorf("control backend %q was not closed by normal infra failure; closed=%v", "broken-backend", closed)
		}
		active, err := alarms.Read(r, now.Add(10*time.Second), nil)
		if err != nil {
			return err
		}
		for _, a := range active {
			if a.Kind == alarms.Closed && (a.Backend == "claude-designer" || a.Backend == "claude-worker" || a.Backend == "gemini-designer") {
				t.Errorf("unexpected backend-closed alarm for missing-worktree run: %s", a)
			}
		}
		return nil
	})
}

// TestMissingWorktreeBoundarySnapshotHoldsItemAndContinuesPass verifies that when a
// boundary snapshot cannot be taken because one item's worktree is missing (either
// before or after a run), that item is held with an item fault and the loop's pass
// continues without stopping.
func TestMissingWorktreeBoundarySnapshotHoldsItemAndContinuesPass(t *testing.T) {
	ctx := context.Background()

	t.Run("before-snapshot", func(t *testing.T) {
		f := newRefusalLoopFixture(t, "item-a", "item-b")
		l := f.loop(t, &recordingSteps{}, &itemContentStager{})
		holdWALKeeper(t, f.storePath)
		base := commitBlock(t, f.root, "item-a", "- **c1:** a")
		base = commitBlock(t, f.root, "item-b", "- **c1:** b")
		runGit(t, f.root, "update-ref", GreenRef, base)

		decisions := f.decisions()
		var dA Decision
		for _, d := range decisions {
			if d.ItemID == "item-a" {
				dA = d
				break
			}
		}
		if dA.RunRequest == nil {
			t.Fatalf("missing decision for item-a: %+v", decisions)
		}
		// Remove item-a's worktree before launchRun takes the opening boundary snapshot.
		if err := os.RemoveAll(l.lanePath(dA.LaneID)); err != nil {
			t.Fatalf("RemoveAll worktree: %v", err)
		}

		result := &LoopResult{}
		l.active[dA.RunRequest.RunID] = true
		l.launchRun(ctx, dA, l.cfg.Now(), nil, result)
		c := <-l.completions
		if c.outcome.err == nil || !strings.Contains(c.outcome.err.Error(), "boundary") {
			t.Fatalf("expected boundary error in completion, got %+v", c)
		}
		stop, err := l.takeCompletion(c, result)
		if err != nil || stop != "" || result.StopItemID != "" {
			t.Fatalf("missing worktree boundary failure stopped the loop: stop=%q err=%v result=%+v", stop, err, result)
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			fault, ok := mustFaults(t, r)["item-a"]
			if !ok || fault.Reason != ReasonSettleFailed {
				t.Fatalf("item-a fault = %+v (ok=%v), want ReasonSettleFailed", fault, ok)
			}
			return nil
		})
		// The pass continues: item-a is held, while item-b is still planned for dispatch.
		plans := mustPlan(t, l)
		var sawAHold, sawBDispatch bool
		for _, p := range plans {
			if p.decision.ItemID == "item-a" && p.decision.Action == ActionHold {
				sawAHold = true
			}
			if p.decision.ItemID == "item-b" && p.decision.Action == ActionDispatch {
				sawBDispatch = true
			}
		}
		if !sawAHold || !sawBDispatch {
			t.Fatalf("after missing-worktree hold: sawAHold=%v sawBDispatch=%v plans=%+v", sawAHold, sawBDispatch, plans)
		}
	})

	t.Run("after-snapshot", func(t *testing.T) {
		f := newRefusalLoopFixture(t, "item-a")
		var l *Loop
		steps := &deleteWorktreeDuringRunSteps{
			deletePath: func() string { return l.lanePath("lane-1") },
		}
		l = f.loop(t, steps, &itemContentStager{})
		holdWALKeeper(t, f.storePath)
		base := commitBlock(t, f.root, "item-a", "- **c1:** a")
		runGit(t, f.root, "update-ref", GreenRef, base)

		dA := f.decisions()[0]
		steps.deletePath = func() string { return l.lanePath(dA.LaneID) }
		result := &LoopResult{}
		l.active[dA.RunRequest.RunID] = true
		l.launchRun(ctx, dA, l.cfg.Now(), nil, result)
		c := <-l.completions
		if c.outcome.err == nil || !strings.Contains(c.outcome.err.Error(), "run boundary after") {
			t.Fatalf("expected run boundary after error in completion, got %+v", c)
		}
		stop, err := l.takeCompletion(c, result)
		if err != nil || stop != "" || result.StopItemID != "" {
			t.Fatalf("missing worktree after-snapshot stopped the loop: stop=%q err=%v result=%+v", stop, err, result)
		}
		fixtureRead(t, f, func(r *store.Reader) error {
			fault, ok := mustFaults(t, r)["item-a"]
			if !ok || fault.Reason != ReasonSettleFailed {
				t.Fatalf("item-a fault = %+v (ok=%v), want ReasonSettleFailed", fault, ok)
			}
			return nil
		})
	})
}

type deleteWorktreeDuringRunSteps struct {
	recordingSteps
	deletePath func() string
}

func (s *deleteWorktreeDuringRunSteps) Run(_ context.Context, req run.Request) (run.Record, string, error) {
	if err := os.RemoveAll(s.deletePath()); err != nil {
		return run.Record{}, "", err
	}
	exitOne := 1
	return run.Record{
		RunID:    req.RunID,
		ItemID:   req.ItemID,
		LaneID:   req.LaneID,
		Backend:  req.Backend,
		State:    run.StateExited,
		ExitCode: &exitOne,
	}, "", nil
}
