package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func writeCollectFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

// stageFixtureItemDir builds an item directory (<laneRoot>/<itemID>) by hand with a real
// Git worktree, per-run scratch and legacy durable directories, item/run refs, run logs,
// brief, report, node directory, and idle lane + run store records.
func stageFixtureItemDir(t *testing.T, f refusalLoopFixture, itemID, runID string) (itemDir, worktree, scratchDir, durableDir, head string) {
	t.Helper()
	itemDir = filepath.Join(f.laneRoot, itemID)
	worktree = filepath.Join(itemDir, WorktreeDir)
	scratchDir = filepath.Join(itemDir, "scratch", runID)
	durableDir = filepath.Join(itemDir, "durable", runID)
	if err := os.MkdirAll(itemDir, 0o755); err != nil {
		t.Fatal(err)
	}
	base := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "worktree", "add", "--detach", worktree, base)
	writeCollectFile(t, filepath.Join(worktree, itemID+".txt"), "committed work for "+itemID+"\n")
	runGit(t, worktree, "add", itemID+".txt")
	runGit(t, worktree, "commit", "-qm", "work for "+itemID)
	head = runGit(t, worktree, "rev-parse", "HEAD")

	// Pin both the item ref and the run pin.
	runGit(t, f.root, "update-ref", itemPinPrefix+itemID, head)
	runGit(t, f.root, "update-ref", "refs/tillr/runs/"+runID, head)

	// Populate scratch and legacy durable dirs.
	writeCollectFile(t, filepath.Join(scratchDir, "build.out"), "scratch bytes\n")
	writeCollectFile(t, filepath.Join(durableDir, "legacy.out"), "durable bytes\n")

	// Populate run artifacts outside scratch (log, brief, report, node dir).
	runDir := RunDir(f.root, runID)
	writeCollectFile(t, filepath.Join(runDir, runID+".log"), "run log line\n")
	writeCollectFile(t, filepath.Join(runDir, BriefFile), "# brief\n")
	writeCollectFile(t, filepath.Join(runDir, "node-report.md"), "RESULT: done\n")
	writeCollectFile(t, filepath.Join(runDir, "artifacts", "node-out.txt"), "kept node output\n")

	now := time.Now().UnixMilli()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO items(item_id,title,state,updated_at,seat) VALUES(?,?,?,?,?)`,
			itemID, itemID, StateInQueue, now, ""); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES(?,?,NULL,NULL) ON CONFLICT(lane_id) DO UPDATE SET state='idle',run_id=NULL,leased_until=NULL`,
			itemID, run.LaneStateIdle); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,event_seq) VALUES(?,?,?,?,?,?,?,?)`,
			runID, itemID, itemID, "fixture", run.StateExited, now-1000, now, 1); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES(?,?,?,?,?)`,
			itemID, 1, "build", runID, "done"); err != nil {
			return err
		}
		return nil
	})
	return itemDir, worktree, scratchDir, durableDir, head
}

func TestCollectEndedItemDirRemovesWorktreeScratchAndIdleLaneRow(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 1, "lane-1")
	var reports bytes.Buffer
	l := &Loop{cfg: LoopConfig{
		Root:     f.root,
		Store:    f.storePath,
		LaneRoot: f.laneRoot,
		Lanes:    []string{"lane-1"},
		Report:   &reports,
		Now:      func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) },
	}}

	itemID := "item-collect-1"
	runID := "run-item-collect-1-a1-n1"
	itemDir, worktree, scratchDir, durableDir, head := stageFixtureItemDir(t, f, itemID, runID)

	// Add uncommitted work to verify saveEndedItemWork runs before deletion.
	writeForkFile(t, filepath.Join(worktree, "uncommitted.txt"), "unsaved edit\n")

	ctx := context.Background()
	// End the item as closed.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, itemID, "done elsewhere", "owner", l.cfg.Now())
		return err
	})

	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("reclaimEndedItemLanes: %v", err)
	}

	// Worktree, scratch, and durable must be gone.
	for _, gone := range []string{worktree, filepath.Join(itemDir, "scratch"), scratchDir, filepath.Join(itemDir, "durable"), durableDir} {
		if _, err := os.Stat(gone); !os.IsNotExist(err) {
			t.Errorf("expected %s to be deleted, stat err=%v", gone, err)
		}
	}

	// Git worktree registration must be pruned.
	wtList := runGit(t, f.root, "worktree", "list", "--porcelain")
	if strings.Contains(wtList, worktree) {
		t.Errorf("worktree %s still registered in git worktree list:\n%s", worktree, wtList)
	}

	// Uncommitted work must have been saved before deletion.
	endedRef := runGit(t, f.root, "for-each-ref", "--format=%(refname)", endedWorkPrefix+runID)
	if strings.TrimSpace(endedRef) != endedWorkPrefix+runID {
		t.Errorf("expected uncommitted work saved at %s, got %q", endedWorkPrefix+runID, endedRef)
	}

	// Item ref and run pin must remain intact.
	if got := runGit(t, f.root, "rev-parse", itemPinPrefix+itemID); got != head {
		t.Errorf("item ref = %q, want %q", got, head)
	}
	if got := runGit(t, f.root, "rev-parse", "refs/tillr/runs/"+runID); got != head {
		t.Errorf("run pin = %q, want %q", got, head)
	}

	// Run logs, brief, per-run report, and node output directory must remain intact.
	runDir := RunDir(f.root, runID)
	for _, kept := range []string{
		filepath.Join(runDir, runID+".log"),
		filepath.Join(runDir, BriefFile),
		filepath.Join(runDir, "node-report.md"),
		filepath.Join(runDir, "artifacts", "node-out.txt"),
	} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("expected %s to be kept, stat err=%v", kept, err)
		}
	}

	// Idle lanes row must be deleted, while runs, node_runs, items, and collection event remain.
	fixtureRead(t, f, func(r *store.Reader) error {
		var laneCount, runCount, nodeCount, itemCount int
		if err := r.QueryRow(`SELECT count(*) FROM lanes WHERE lane_id = ?`, itemID).Scan(&laneCount); err != nil {
			return err
		}
		if laneCount != 0 {
			t.Errorf("idle lanes row for %s still present (%d)", itemID, laneCount)
		}
		if err := r.QueryRow(`SELECT count(*) FROM runs WHERE run_id = ?`, runID).Scan(&runCount); err != nil {
			return err
		}
		if runCount != 1 {
			t.Errorf("runs row count = %d, want 1", runCount)
		}
		if err := r.QueryRow(`SELECT count(*) FROM node_runs WHERE run_id = ?`, runID).Scan(&nodeCount); err != nil {
			return err
		}
		if nodeCount != 1 {
			t.Errorf("node_runs row count = %d, want 1", nodeCount)
		}
		if err := r.QueryRow(`SELECT count(*) FROM items WHERE item_id = ?`, itemID).Scan(&itemCount); err != nil {
			return err
		}
		if itemCount != 1 {
			t.Errorf("items row count = %d, want 1", itemCount)
		}
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ? ORDER BY seq DESC LIMIT 1`,
			endedWorktreeCollectedKind, itemID).Scan(&raw); err != nil {
			t.Errorf("expected %s event for %s: %v", endedWorktreeCollectedKind, itemID, err)
			return nil
		}
		var p EndedWorktreeCollected
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if p.ItemID != itemID || !slices.Contains(p.Deleted, "worktree") || !slices.Contains(p.Deleted, "scratch") || !slices.Contains(p.Deleted, "durable") || !slices.Contains(p.Deleted, "lane-row") {
			t.Errorf("unexpected collection event payload: %+v", p)
		}
		return nil
	})
}

func TestCollectLandedItemDirWaitsForGreenRef(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 1, "lane-1")
	l := &Loop{cfg: LoopConfig{
		Root:     f.root,
		Store:    f.storePath,
		LaneRoot: f.laneRoot,
		Lanes:    []string{"lane-1"},
		Report:   io.Discard,
		Now:      func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) },
	}}

	initialTrunk := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", GreenRef, initialTrunk)

	itemID := "item-landed-1"
	runID := "run-item-landed-1-a1-n1"
	_, worktree, scratchDir, _, head := stageFixtureItemDir(t, f, itemID, runID)

	// Simulate landing on trunk ahead of refs/tillr/green.
	writeForkFile(t, filepath.Join(f.root, "landed.txt"), "landed commit\n")
	runGit(t, f.root, "add", "landed.txt")
	runGit(t, f.root, "commit", "-qm", "land "+itemID)
	landedTrunk := runGit(t, f.root, "rev-parse", "HEAD")

	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`UPDATE items SET state = ? WHERE item_id = ?`, StateLanded, itemID); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			"land-1", itemID, runID, "verdict-1", "landed", initialTrunk, head, initialTrunk, landedTrunk, 10)
		return err
	})

	ctx := context.Background()
	// Before refs/tillr/green contains landedTrunk, directory must stay.
	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("reclaimEndedItemLanes before green: %v", err)
	}
	if _, err := os.Stat(worktree); err != nil {
		t.Fatalf("unproven landed item worktree was deleted early: %v", err)
	}
	if _, err := os.Stat(scratchDir); err != nil {
		t.Fatalf("unproven landed item scratch was deleted early: %v", err)
	}

	// Advance refs/tillr/green to contain landedTrunk.
	runGit(t, f.root, "update-ref", GreenRef, landedTrunk)

	// Now collection must delete worktree, scratch, and idle lane row.
	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("reclaimEndedItemLanes after green: %v", err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("proven landed item worktree still exists: err=%v", err)
	}
	if _, err := os.Stat(scratchDir); !os.IsNotExist(err) {
		t.Errorf("proven landed item scratch still exists: err=%v", err)
	}
}

func TestCollectNeverDeletesRunningLeasedOrInterruptedItem(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, f refusalLoopFixture, itemID, runID string)
	}{
		{
			name: "running-node",
			setup: func(t *testing.T, f refusalLoopFixture, itemID, runID string) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE node_runs SET outcome = 'running' WHERE run_id = ?`, runID)
					return err
				})
			},
		},
		{
			name: "leased-lane",
			setup: func(t *testing.T, f refusalLoopFixture, itemID, runID string) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = ? WHERE lane_id = ?`, run.LaneStateLeased, runID, itemID)
					return err
				})
			},
		},
		{
			name: "interrupted-node",
			setup: func(t *testing.T, f refusalLoopFixture, itemID, runID string) {
				fixtureWrite(t, f, func(tx *store.Tx) error {
					_, err := tx.Exec(`UPDATE node_runs SET outcome = 'interrupted' WHERE run_id = ?`, runID)
					return err
				})
			},
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			f := newRefusalLoopFixtureWithLanes(t, 1, "lane-1")
			l := &Loop{cfg: LoopConfig{
				Root:     f.root,
				Store:    f.storePath,
				LaneRoot: f.laneRoot,
				Lanes:    []string{"lane-1"},
				Report:   io.Discard,
				Now:      func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) },
			}}

			itemID := "item-" + tc.name
			runID := "run-" + tc.name + "-a1-n1"
			_, worktree, scratchDir, _, _ := stageFixtureItemDir(t, f, itemID, runID)

			ctx := context.Background()
			fixtureWrite(t, f, func(tx *store.Tx) error {
				_, err := item.Close(ctx, tx, itemID, "closed while busy", "owner", l.cfg.Now())
				return err
			})
			tc.setup(t, f, itemID, runID)

			if err := l.reclaimEndedItemLanes(ctx); err != nil {
				t.Fatalf("reclaimEndedItemLanes: %v", err)
			}
			if _, err := os.Stat(worktree); err != nil {
				t.Errorf("%s: worktree was unexpectedly deleted: %v", tc.name, err)
			}
			if _, err := os.Stat(scratchDir); err != nil {
				t.Errorf("%s: scratch was unexpectedly deleted: %v", tc.name, err)
			}
			// Leased lane row must never be deleted.
			if tc.name == "leased-lane" {
				fixtureRead(t, f, func(r *store.Reader) error {
					var state string
					if err := r.QueryRow(`SELECT state FROM lanes WHERE lane_id = ?`, itemID).Scan(&state); err != nil {
						t.Errorf("leased lane row was deleted: %v", err)
						return nil
					}
					if state != run.LaneStateLeased {
						t.Errorf("leased lane state = %q, want %q", state, run.LaneStateLeased)
					}
					return nil
				})
			}
		})
	}
}

func TestCollectUnremovableDirectoryReportedAndDeletedOnLaterPass(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 1, "lane-1")
	var reports bytes.Buffer
	l := &Loop{cfg: LoopConfig{
		Root:     f.root,
		Store:    f.storePath,
		LaneRoot: f.laneRoot,
		Lanes:    []string{"lane-1"},
		Report:   &reports,
		Now:      func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) },
	}}

	itemID := "item-retry-delete"
	runID := "run-item-retry-delete-a1-n1"
	itemDir, worktree, scratchDir, _, _ := stageFixtureItemDir(t, f, itemID, runID)

	ctx := context.Background()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := item.Close(ctx, tx, itemID, "closed", "owner", l.cfg.Now())
		return err
	})

	scratchRoot := filepath.Join(itemDir, "scratch")
	failErr := errors.New("simulated busy directory")
	removeEndedDirHook = func(path string) error {
		if path == scratchRoot {
			return failErr
		}
		return nil
	}
	t.Cleanup(func() { removeEndedDirHook = nil })

	// Pass 1: worktree is removed, scratch removal fails, is left in place, and is reported.
	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("pass 1 reclaimEndedItemLanes: %v", err)
	}
	if _, err := os.Stat(scratchDir); err != nil {
		t.Fatalf("expected unremovable scratch %s to remain after pass 1: %v", scratchDir, err)
	}
	if !strings.Contains(reports.String(), scratchRoot) || !strings.Contains(reports.String(), failErr.Error()) {
		t.Errorf("report missing failed dir/reason: %q", reports.String())
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var raw []byte
		if err := r.QueryRow(`SELECT payload FROM events WHERE kind = ? AND object_id = ? ORDER BY seq DESC LIMIT 1`,
			endedWorktreeCollectFailedKind, itemID).Scan(&raw); err != nil {
			t.Errorf("expected %s event: %v", endedWorktreeCollectFailedKind, err)
			return nil
		}
		var p EndedWorktreeCollectFailed
		if err := json.Unmarshal(raw, &p); err != nil {
			return err
		}
		if p.Directory != scratchRoot || !strings.Contains(p.Reason, failErr.Error()) {
			t.Errorf("unexpected failure event payload: %+v", p)
		}
		return nil
	})

	// Pass 2: clear the failure and verify the later pass deletes scratch and the idle lane row.
	removeEndedDirHook = nil
	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("pass 2 reclaimEndedItemLanes: %v", err)
	}
	if _, err := os.Stat(worktree); !os.IsNotExist(err) {
		t.Errorf("worktree still present after pass 2: %v", err)
	}
	if _, err := os.Stat(scratchRoot); !os.IsNotExist(err) {
		t.Errorf("scratch still present after pass 2: %v", err)
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var laneCount int
		if err := r.QueryRow(`SELECT count(*) FROM lanes WHERE lane_id = ?`, itemID).Scan(&laneCount); err != nil {
			return err
		}
		if laneCount != 0 {
			t.Errorf("idle lanes row still present after pass 2 (%d)", laneCount)
		}
		return nil
	})
}

func TestCollectPoolLaneScratchDeletesEndedRunsAndKeepsWorktree(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 1, "lane-1")
	l := &Loop{cfg: LoopConfig{
		Root:     f.root,
		Store:    f.storePath,
		LaneRoot: f.laneRoot,
		Lanes:    []string{"lane-1"},
		Report:   io.Discard,
		Now:      func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) },
	}}

	initialTrunk := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", GreenRef, initialTrunk)

	laneDir := filepath.Join(f.laneRoot, "lane-1")
	poolWorktree := filepath.Join(laneDir, WorktreeDir)
	if _, err := os.Stat(poolWorktree); err != nil {
		t.Fatalf("fixture missing pool lane worktree: %v", err)
	}

	endedRun := "run-ended-pool-a1"
	activeRun := "run-active-pool-a1"
	landedRun := "run-landed-pool-a1"

	endedScratch := filepath.Join(laneDir, "scratch", endedRun)
	endedDurable := filepath.Join(laneDir, "durable", endedRun)
	activeScratch := filepath.Join(laneDir, "scratch", activeRun)
	landedScratch := filepath.Join(laneDir, "scratch", landedRun)
	writeCollectFile(t, filepath.Join(endedScratch, "tmp.bin"), "ended\n")
	writeCollectFile(t, filepath.Join(endedDurable, "old.bin"), "old\n")
	writeCollectFile(t, filepath.Join(activeScratch, "tmp.bin"), "active\n")
	writeCollectFile(t, filepath.Join(landedScratch, "tmp.bin"), "landed\n")

	writeForkFile(t, filepath.Join(f.root, "pool-landed.txt"), "landed\n")
	runGit(t, f.root, "add", "pool-landed.txt")
	runGit(t, f.root, "commit", "-qm", "land item-pool-landed")
	landedTrunk := runGit(t, f.root, "rev-parse", "HEAD")

	now := time.Now().UnixMilli()
	ctx := context.Background()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO lanes(lane_id,state) VALUES('lane-1','idle')`); err != nil {
			return err
		}
		for _, it := range []struct{ id, state string }{
			{"item-pool-ended", StateInQueue},
			{"item-pool-active", StateInQueue},
			{"item-pool-landed", StateLanded},
		} {
			if _, err := tx.Exec(`INSERT OR IGNORE INTO items(item_id,title,state,updated_at,seat) VALUES(?,?,?,?,?)`,
				it.id, it.id, it.state, now, ""); err != nil {
				return err
			}
		}
		if _, err := item.Close(ctx, tx, "item-pool-ended", "done", "owner", l.cfg.Now()); err != nil {
			return err
		}
		for _, r := range []struct{ runID, itemID string }{
			{endedRun, "item-pool-ended"},
			{activeRun, "item-pool-active"},
			{landedRun, "item-pool-landed"},
		} {
			if _, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,event_seq) VALUES(?,?,?,?,?,?,?,?)`,
				r.runID, r.itemID, "lane-1", "fixture", run.StateExited, now-1000, now, 1); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES(?,?,?,?,?,?,?,?,?,?)`,
			"land-pool-1", "item-pool-landed", landedRun, "v-1", "landed", initialTrunk, initialTrunk, initialTrunk, landedTrunk, 10)
		return err
	})

	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("reclaimEndedItemLanes pass 1: %v", err)
	}
	// Ended item's scratch and durable under pool lane are gone.
	if _, err := os.Stat(endedScratch); !os.IsNotExist(err) {
		t.Errorf("ended run scratch under pool lane still exists: %v", err)
	}
	if _, err := os.Stat(endedDurable); !os.IsNotExist(err) {
		t.Errorf("ended run durable under pool lane still exists: %v", err)
	}
	// Pool lane's worktree, active item's scratch, and unproven landed item's scratch stay.
	for _, kept := range []string{poolWorktree, activeScratch, landedScratch} {
		if _, err := os.Stat(kept); err != nil {
			t.Errorf("expected %s to stay: %v", kept, err)
		}
	}
	// Pool lane's lanes row stays.
	fixtureRead(t, f, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM lanes WHERE lane_id = 'lane-1'`).Scan(&n); err != nil {
			return err
		}
		if n != 1 {
			t.Errorf("pool lane row count = %d, want 1", n)
		}
		return nil
	})

	// Once refs/tillr/green contains landedTrunk, landedRun's scratch is deleted too.
	runGit(t, f.root, "update-ref", GreenRef, landedTrunk)
	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("reclaimEndedItemLanes pass 2: %v", err)
	}
	if _, err := os.Stat(landedScratch); !os.IsNotExist(err) {
		t.Errorf("proven landed run scratch under pool lane still exists: %v", err)
	}
	if _, err := os.Stat(poolWorktree); err != nil {
		t.Errorf("pool lane worktree was deleted: %v", err)
	}
}

func TestCollectPreservesScratchFilesReadByDrainReaders(t *testing.T) {
	f := newRefusalLoopFixtureWithLanes(t, 1, "lane-1")
	l := &Loop{cfg: LoopConfig{
		Root:     f.root,
		Store:    f.storePath,
		LaneRoot: f.laneRoot,
		Lanes:    []string{"lane-1"},
		Report:   io.Discard,
		Now:      func() time.Time { return time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC) },
	}}

	itemID := "item-readers"
	runID := "run-item-readers-a1-n1"
	_, _, scratchDir, _, _ := stageFixtureItemDir(t, f, itemID, runID)

	// Write all four scratch files that drain readers inspect.
	writeForkFile(t, filepath.Join(scratchDir, graphExitFile), `{"item_id":"`+itemID+`","reason":"no commit by design"}`+"\n")
	writeForkFile(t, filepath.Join(scratchDir, landingOfferFile), `{"call_id":"call-1","base":"abc123","result":"done"}`+"\n")
	writeForkFile(t, filepath.Join(scratchDir, "eval.output"), `{"verdict":"done"}`+"\n")
	writeForkFile(t, filepath.Join(scratchDir, commentRepliesFile), `{"id":"c1","round":0,"summary":"answered in section 2"}`+"\n")

	ctx := context.Background()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		// Attach a comment batch so readCommentReplies can validate replies.jsonl after collection.
		batch := item.CommentBatch{
			Document: "docs/example.md",
			Comments: []item.ReviewComment{{ID: "c1", Comment: "clarify this"}},
		}
		rawBatch, err := json.Marshal(batch)
		if err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO comment_batches(item_id,document,batch) VALUES(?,?,?)`, itemID, batch.Document, string(rawBatch)); err != nil {
			return err
		}
		_, err = item.Close(ctx, tx, itemID, "closed", "owner", l.cfg.Now())
		return err
	})

	if err := l.reclaimEndedItemLanes(ctx); err != nil {
		t.Fatalf("reclaimEndedItemLanes: %v", err)
	}
	if _, err := os.Stat(scratchDir); !os.IsNotExist(err) {
		t.Fatalf("scratchDir %s still exists after collection", scratchDir)
	}

	// 1. Workflow exit record is readable via graphExitReadPath.
	exitPath := l.graphExitReadPath(itemID, runID)
	if raw, err := os.ReadFile(exitPath); err != nil || !strings.Contains(string(raw), "no commit by design") {
		t.Errorf("graphExitReadPath(%s) = %q, err=%v", exitPath, string(raw), err)
	}

	// 2. Landing offer is preserved beside the run's log in RunDir.
	offerPath := filepath.Join(RunDir(f.root, runID), landingOfferFile)
	if raw, err := os.ReadFile(offerPath); err != nil || !strings.Contains(string(raw), "call-1") {
		t.Errorf("preserved landing-offer.json = %q, err=%v", string(raw), err)
	}

	// 3. Judge output is readable via judgeEvalOutputPath.
	evalPath := l.judgeEvalOutputPath(itemID, runID)
	if raw, err := os.ReadFile(evalPath); err != nil || !strings.Contains(string(raw), `"verdict":"done"`) {
		t.Errorf("judgeEvalOutputPath(%s) = %q, err=%v", evalPath, string(raw), err)
	}

	// 4. Comment replies are readable via readCommentReplies.
	batch, replies, err := l.readCommentReplies(ctx, Candidate{ItemID: itemID, LaneID: itemID, RunID: runID})
	if err != nil {
		t.Fatalf("readCommentReplies after collection: %v", err)
	}
	if batch == nil || len(replies) != 1 || replies[0].ID != "c1" {
		t.Errorf("unexpected replies after collection: batch=%+v replies=%+v", batch, replies)
	}
}
