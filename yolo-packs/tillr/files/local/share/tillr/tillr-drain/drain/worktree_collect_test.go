package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

func newWorktreeCollectTestLoop(t *testing.T, f refusalLoopFixture) (*Loop, *bytes.Buffer) {
	t.Helper()
	var reports bytes.Buffer
	l := &Loop{cfg: LoopConfig{
		Root:          f.root,
		Store:         f.storePath,
		LaneRoot:      f.laneRoot,
		Lanes:         []string{"lane-1"},
		ItemWorktrees: true,
		Report:        &reports,
		Now:           func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) },
	}}
	return l, &reports
}

func TestWorktreeCollectImmediateOnOwnerAndItemWait(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t)
	l, _ := newWorktreeCollectTestLoop(t, f)
	l.cfg.WorktreeCountOverride = 10 // well above 3 items

	_, wtOwner, scOwner, _, headOwner := stageFixtureItemDir(t, f, "item-owner-wait", "run-owner-1")
	_, wtDep, scDep, _, headDep := stageFixtureItemDir(t, f, "item-dep-wait", "run-dep-1")
	_, wtWarm, scWarm, _, _ := stageFixtureItemDir(t, f, "item-warm", "run-warm-1")

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state='blocked', waiting_on='{"waiter":"owner","reason":"needs ruling"}' WHERE item_id='item-owner-wait'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO item_edges(item_id, blocked_by, kind) VALUES('item-dep-wait', 'item-warm', 'blocked_by')`); err != nil {
			return err
		}
		return nil
	})

	snap, err := l.collectIdleWorktrees(ctx)
	if err != nil {
		t.Fatalf("collectIdleWorktrees: %v", err)
	}

	// Both waiting items are collected immediately even though OnDisk <= countLimit.
	for _, p := range []string{wtOwner, scOwner, wtDep, scDep} {
		if _, err := os.Stat(p); !os.IsNotExist(err) {
			t.Fatalf("expected %s to be collected, stat err=%v", p, err)
		}
	}
	// Warm idle item stays.
	for _, p := range []string{wtWarm, scWarm} {
		if _, err := os.Stat(p); err != nil {
			t.Fatalf("expected warm item %s to remain: %v", p, err)
		}
	}
	// Pinned refs and run logs are kept.
	if got := runGit(t, f.root, "rev-parse", itemPinPrefix+"item-owner-wait"); got != headOwner {
		t.Fatalf("owner-wait pin = %s, want %s", got, headOwner)
	}
	if got := runGit(t, f.root, "rev-parse", itemPinPrefix+"item-dep-wait"); got != headDep {
		t.Fatalf("dep-wait pin = %s, want %s", got, headDep)
	}
	if _, err := os.Stat(filepath.Join(RunDir(f.root, "run-owner-1"), "run-owner-1.log")); err != nil {
		t.Fatalf("expected run log to remain: %v", err)
	}
	if snap.OnDisk != 1 {
		t.Fatalf("snap.OnDisk = %d, want 1", snap.OnDisk)
	}
}

func TestWorktreeCollectLRUOnCountAndDiskBound(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t)
	l, _ := newWorktreeCollectTestLoop(t, f)
	l.cfg.WorktreeCountOverride = 2
	l.cfg.WorktreeDiskBudgetOverride = 100 * 1024 * 1024 // 100 MiB (won't bind yet)

	_, wtOldest, scOldest, _, headOldest := stageFixtureItemDir(t, f, "item-1-oldest", "run-1")
	_, wtMiddle, _, _, _ := stageFixtureItemDir(t, f, "item-2-middle", "run-2")
	_, wtNewest, _, _, _ := stageFixtureItemDir(t, f, "item-3-newest", "run-3")

	now := time.Now().UnixMilli()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE runs SET finished_at=? WHERE run_id='run-1'`, now-30000); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE runs SET finished_at=? WHERE run_id='run-2'`, now-20000); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE runs SET finished_at=? WHERE run_id='run-3'`, now-10000); err != nil {
			return err
		}
		return nil
	})

	snap, err := l.collectIdleWorktrees(ctx)
	if err != nil {
		t.Fatalf("collectIdleWorktrees (count bound): %v", err)
	}
	if snap.OnDisk != 2 {
		t.Fatalf("snap.OnDisk = %d, want 2", snap.OnDisk)
	}
	if _, err := os.Stat(wtOldest); !os.IsNotExist(err) {
		t.Fatalf("expected oldest worktree to be collected, stat err=%v", err)
	}
	if _, err := os.Stat(scOldest); !os.IsNotExist(err) {
		t.Fatalf("expected oldest scratch to be collected, stat err=%v", err)
	}
	if got := runGit(t, f.root, "rev-parse", itemPinPrefix+"item-1-oldest"); got != headOldest {
		t.Fatalf("oldest pin = %s, want %s", got, headOldest)
	}
	if _, err := os.Stat(wtMiddle); err != nil {
		t.Fatalf("expected middle worktree to remain: %v", err)
	}
	if _, err := os.Stat(wtNewest); err != nil {
		t.Fatalf("expected newest worktree to remain: %v", err)
	}

	// Now tighten disk budget below 2 worktrees so middle is collected by disk bound.
	writeCollectFile(t, filepath.Join(f.laneRoot, "item-2-middle", "scratch", "run-2", "big.bin"), strings.Repeat("x", 64*1024))
	l.cfg.WorktreeCountOverride = 10
	l.cfg.WorktreeDiskBudgetOverride = 32 * 1024 // 32 KiB

	snap, err = l.collectIdleWorktrees(ctx)
	if err != nil {
		t.Fatalf("collectIdleWorktrees (disk bound): %v", err)
	}
	if _, err := os.Stat(wtMiddle); !os.IsNotExist(err) {
		t.Fatalf("expected middle worktree to be collected on disk bound, stat err=%v", err)
	}
	if _, err := os.Stat(wtNewest); err != nil {
		t.Fatalf("expected newest worktree to remain: %v", err)
	}
	if snap.OnDisk != 1 {
		t.Fatalf("snap.OnDisk after disk bound = %d, want 1", snap.OnDisk)
	}
}

func TestWorktreeCollectProtectsInUseInterruptedAndReportsOvershoot(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t)
	l, reports := newWorktreeCollectTestLoop(t, f)
	l.cfg.WorktreeCountOverride = 1
	l.cfg.WorktreeDiskBudgetOverride = 100 // 100 bytes: guaranteed overshoot

	_, wtInUse, scInUse, _, _ := stageFixtureItemDir(t, f, "item-in-use", "run-inuse")
	_, wtPaused, scPaused, _, _ := stageFixtureItemDir(t, f, "item-paused", "run-paused")
	_, wtIdle, _, _, _ := stageFixtureItemDir(t, f, "item-idle", "run-idle")

	writeCollectFile(t, filepath.Join(scInUse, "huge.bin"), strings.Repeat("a", 4096))

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE lanes SET state='leased', run_id='run-inuse' WHERE lane_id='item-in-use'`); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE runs SET state=?, finished_at=0 WHERE run_id='run-paused'`, run.StatePaused); err != nil {
			return err
		}
		return nil
	})

	snap, err := l.collectIdleWorktrees(ctx)
	if err != nil {
		t.Fatalf("collectIdleWorktrees: %v", err)
	}
	if _, err := os.Stat(wtInUse); err != nil {
		t.Fatalf("in-use worktree must not be collected: %v", err)
	}
	if _, err := os.Stat(scInUse); err != nil {
		t.Fatalf("in-use scratch must not be collected: %v", err)
	}
	if _, err := os.Stat(wtPaused); err != nil {
		t.Fatalf("paused/interrupted worktree must not be collected: %v", err)
	}
	if _, err := os.Stat(scPaused); err != nil {
		t.Fatalf("paused/interrupted scratch must not be collected: %v", err)
	}
	if _, err := os.Stat(wtIdle); !os.IsNotExist(err) {
		t.Fatalf("idle worktree should have been collected, stat err=%v", err)
	}
	if snap.TotalBytes <= l.cfg.WorktreeDiskBudgetOverride {
		t.Fatalf("expected overshoot TotalBytes > %d, got %d", l.cfg.WorktreeDiskBudgetOverride, snap.TotalBytes)
	}
	if !strings.Contains(reports.String(), "worktree disk budget overshoot") || !strings.Contains(reports.String(), "run-inuse") {
		t.Fatalf("expected overshoot report naming largest scratch dir, got:\n%s", reports.String())
	}
}

func TestWorktreeCollectUnremovableDirRetriesWithoutFailingItem(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t)
	l, _ := newWorktreeCollectTestLoop(t, f)
	l.cfg.WorktreeCountOverride = 10

	_, wtBlocked, scBlocked, _, _ := stageFixtureItemDir(t, f, "item-unremovable", "run-unrem")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='blocked', waiting_on='{"waiter":"owner"}' WHERE item_id='item-unremovable'`)
		return err
	})

	// Simulate an unremovable scratch directory via removeEndedDirHook.
	scratchRoot := filepath.Dir(scBlocked)
	removeEndedDirHook = func(path string) error {
		if path == scratchRoot {
			return errors.New("simulated busy directory")
		}
		return nil
	}
	t.Cleanup(func() { removeEndedDirHook = nil })

	if _, err := l.collectIdleWorktrees(ctx); err != nil {
		t.Fatalf("collectIdleWorktrees pass 1: %v", err)
	}
	// Worktree was removed, scratch could not be removed.
	if _, err := os.Stat(wtBlocked); !os.IsNotExist(err) {
		t.Fatalf("expected worktree to be removed, stat err=%v", err)
	}
	if _, err := os.Stat(scBlocked); err != nil {
		t.Fatalf("expected unremovable scratch to still exist: %v", err)
	}

	// Clear hook and retry: next collection cleans it up.
	removeEndedDirHook = nil
	if _, err := l.collectIdleWorktrees(ctx); err != nil {
		t.Fatalf("collectIdleWorktrees pass 2: %v", err)
	}
	if _, err := os.Stat(scBlocked); !os.IsNotExist(err) {
		t.Fatalf("expected scratch to be removed on retry, stat err=%v", err)
	}
}

func TestWorktreeBoundsFollowSavedScaleTargetAndLiveScale(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t)
	if err := Scale(ctx, f.storePath, 10); err != nil {
		t.Fatalf("Scale(10): %v", err)
	}

	l, _ := newWorktreeCollectTestLoop(t, f)
	l.cfg.Lanes = []string{"lane-1", "lane-2"} // default when started without --lanes
	l.cfg.LiveScale = true
	l.cfg.ExplicitLanes = nil
	l.cfg.Now = time.Now

	if err := l.resolveStartupLaneCount(ctx, false); err != nil {
		t.Fatalf("resolveStartupLaneCount: %v", err)
	}
	t.Cleanup(l.endSession)

	checkBudgetMatchesWhy := func(wantAgents int) {
		t.Helper()
		snap, err := l.collectIdleWorktrees(ctx)
		if err != nil {
			t.Fatalf("collectIdleWorktrees: %v", err)
		}
		var bounds settings.Worktrees
		wantBudget := bounds.EffectiveDiskBudgetBytes(wantAgents)
		wantCount := bounds.EffectiveMaxCount(wantAgents)
		if snap.DiskBudgetBytes != wantBudget || snap.CountLimit != wantCount {
			t.Fatalf("loop bounds = (%d, %d), want (%d, %d) for %d agents",
				snap.CountLimit, snap.DiskBudgetBytes, wantCount, wantBudget, wantAgents)
		}

		cmd := exec.CommandContext(ctx, "go", "run", "..", "why", "--root", f.root, "--store", f.storePath, "--json")
		cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+t.TempDir())
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("drain why --json failed: %v\n%s", err, out)
		}
		var parsed struct {
			WhyAnswer
		}
		if err := json.Unmarshal(out, &parsed); err != nil {
			t.Fatalf("unmarshal why json: %v\n%s", err, out)
		}
		if parsed.Worktrees == nil {
			t.Fatalf("why json missing worktrees:\n%s", out)
		}
		if snap.DiskBudgetBytes != parsed.Worktrees.DiskBudgetBytes {
			t.Fatalf("loop DiskBudgetBytes = %d, why DiskBudgetBytes = %d (want %d)",
				snap.DiskBudgetBytes, parsed.Worktrees.DiskBudgetBytes, wantBudget)
		}
		if snap.CountLimit != parsed.Worktrees.CountLimit {
			t.Fatalf("loop CountLimit = %d, why CountLimit = %d (want %d)",
				snap.CountLimit, parsed.Worktrees.CountLimit, wantCount)
		}
	}

	checkBudgetMatchesWhy(10)

	// Live scale change updates both the loop's budget and what why sees.
	if err := Scale(ctx, f.storePath, 6); err != nil {
		t.Fatalf("Scale(6): %v", err)
	}
	if err := l.refreshLaneCount(ctx, false); err != nil {
		t.Fatalf("refreshLaneCount: %v", err)
	}
	if err := l.refreshSession(ctx, time.Now().Add(PassInterval)); err != nil {
		t.Fatalf("refreshSession: %v", err)
	}
	checkBudgetMatchesWhy(6)
}

func TestWorktreeOvershootDedupesUnchangedAndRecordsChange(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t)
	l, reports := newWorktreeCollectTestLoop(t, f)
	l.cfg.WorktreeCountOverride = 10
	l.cfg.WorktreeDiskBudgetOverride = 100

	_, _, scInUse, _, _ := stageFixtureItemDir(t, f, "item-overshoot", "run-overshoot-1")
	writeCollectFile(t, filepath.Join(scInUse, "data.bin"), strings.Repeat("x", 4096))
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE lanes SET state='leased', run_id='run-overshoot-1' WHERE lane_id='item-overshoot'`)
		return err
	})

	overshootEventCount := func() int {
		t.Helper()
		var n int
		fixtureRead(t, f, func(r *store.Reader) error {
			return r.QueryRow(`SELECT count(*) FROM events WHERE kind = ?`, worktreeBudgetOvershootKind).Scan(&n)
		})
		return n
	}

	// Three passes with unchanged overshoot facts record exactly one event and one report line.
	for pass := 1; pass <= 3; pass++ {
		if _, err := l.collectIdleWorktrees(ctx); err != nil {
			t.Fatalf("collectIdleWorktrees pass %d: %v", pass, err)
		}
	}
	if got := overshootEventCount(); got != 1 {
		t.Fatalf("overshoot events after 3 unchanged passes = %d, want 1", got)
	}
	if got := strings.Count(reports.String(), "worktree disk budget overshoot"); got != 1 {
		t.Fatalf("overshoot report lines after 3 unchanged passes = %d, want 1:\n%s", got, reports.String())
	}

	// Growing scratch changes the overshoot facts and records a second event (once across two passes).
	writeCollectFile(t, filepath.Join(scInUse, "more.bin"), strings.Repeat("y", 4096))
	for pass := 1; pass <= 2; pass++ {
		if _, err := l.collectIdleWorktrees(ctx); err != nil {
			t.Fatalf("collectIdleWorktrees changed pass %d: %v", pass, err)
		}
	}
	if got := overshootEventCount(); got != 2 {
		t.Fatalf("overshoot events after changed overshoot = %d, want 2", got)
	}

	// Clearing the overshoot resets dedupe so a later recurrence is recorded again.
	l.cfg.WorktreeDiskBudgetOverride = 100 * 1024 * 1024
	if _, err := l.collectIdleWorktrees(ctx); err != nil {
		t.Fatalf("collectIdleWorktrees cleared: %v", err)
	}
	if got := overshootEventCount(); got != 2 {
		t.Fatalf("overshoot events after clearing = %d, want 2", got)
	}
	l.cfg.WorktreeDiskBudgetOverride = 100
	if _, err := l.collectIdleWorktrees(ctx); err != nil {
		t.Fatalf("collectIdleWorktrees recurred: %v", err)
	}
	if got := overshootEventCount(); got != 3 {
		t.Fatalf("overshoot events after recurrence = %d, want 3", got)
	}
}

func TestWorktreeCollectPoolLaneEndedRunScratch(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixtureWithLanes(t, 1, "lane-1")
	l, _ := newWorktreeCollectTestLoop(t, f)
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.WorktreeCountOverride = 10
	l.cfg.WorktreeDiskBudgetOverride = 32 * 1024 // 32 KiB: forces LRU collection of large ended pool scratch

	laneDir := filepath.Join(f.laneRoot, "lane-1")
	poolWorktree := filepath.Join(laneDir, WorktreeDir)
	if _, err := os.Stat(poolWorktree); err != nil {
		t.Fatalf("fixture missing pool worktree: %v", err)
	}

	endedInQueueDir := filepath.Join(laneDir, "durable", "run-pool-inqueue")
	endedWaitingDir := filepath.Join(laneDir, "durable", "run-pool-waiting")
	orphanedDir := filepath.Join(laneDir, "durable", "run-pool-orphaned")
	pausedDir := filepath.Join(laneDir, "durable", "run-pool-paused")

	writeCollectFile(t, filepath.Join(endedInQueueDir, "big.bin"), strings.Repeat("a", 64*1024))
	writeCollectFile(t, filepath.Join(endedInQueueDir, landingOfferFile), `{"call_id":"call-pool-1","result":"done"}`+"\n")
	writeCollectFile(t, filepath.Join(endedWaitingDir, "wait.bin"), strings.Repeat("b", 1024))
	writeCollectFile(t, filepath.Join(orphanedDir, "orphan.bin"), strings.Repeat("c", 1024))
	writeCollectFile(t, filepath.Join(pausedDir, "paused.bin"), strings.Repeat("d", 1024))

	now := time.Now().UnixMilli()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id,state) VALUES('lane-1','idle')`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO items(item_id,title,state,updated_at,seat) VALUES('item-pool-inqueue','inqueue',?,?, '')`, StateInQueue, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO items(item_id,title,state,waiting_on,updated_at,seat) VALUES('item-pool-waiting','waiting',?,'{"waiter":"owner"}',?, '')`, StateBlocked, now); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT OR IGNORE INTO items(item_id,title,state,updated_at,seat) VALUES('item-pool-paused','paused',?,?, '')`, StateInQueue, now); err != nil {
			return err
		}
		for _, r := range []struct {
			runID, itemID, state string
			finishedAt           int64
		}{
			{"run-pool-inqueue", "item-pool-inqueue", run.StateExited, now - 86400_000},
			{"run-pool-waiting", "item-pool-waiting", run.StateExited, now - 86400_000},
			{"run-pool-paused", "item-pool-paused", run.StatePaused, 0},
		} {
			if _, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,event_seq) VALUES(?,?,?,?,?,?,?,?)`,
				r.runID, r.itemID, "lane-1", "fixture", r.state, now-90000_000, r.finishedAt, 1); err != nil {
				return err
			}
		}
		return nil
	})

	if _, err := l.collectIdleWorktrees(ctx); err != nil {
		t.Fatalf("collectIdleWorktrees: %v", err)
	}

	// Ended runs (waiting, orphaned, and over-budget in-queue) under lane-1/durable are collected.
	for _, gone := range []string{endedWaitingDir, orphanedDir, endedInQueueDir} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("expected ended pool scratch %s to be collected, stat err=%v", gone, err)
		}
	}
	// Interrupted (paused) run scratch and the pool lane worktree are preserved.
	for _, kept := range []string{pausedDir, poolWorktree} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("expected %s to remain: %v", kept, err)
		}
	}
	// Drain-reader files from collected pool scratch are preserved in RunDir.
	if _, err := os.Stat(filepath.Join(RunDir(f.root, "run-pool-inqueue"), landingOfferFile)); err != nil {
		t.Errorf("expected %s to be preserved in RunDir: %v", landingOfferFile, err)
	}
}
