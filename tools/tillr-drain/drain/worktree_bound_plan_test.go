package drain

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/worktreebudget"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func TestWorktreeBoundCountAndDiskDeferReadyItemNamingRunningNode(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixtureWithLanes(t, 4, "lane-1", "lane-2", "lane-3", "lane-4")

	now := time.Now().UTC()
	nowMs := now.UnixMilli()
	fixtureWrite(t, f, func(tx *store.Tx) error {
		applyItem := func(id string) error {
			payload, _ := json.Marshal(map[string]any{
				"item_id": id,
				"title":   id,
				"state":   StateInQueue,
				"criteria": []map[string]any{
					{"criterion_id": "c1", "text": "criterion"},
				},
			})
			_, err := item.Apply(ctx, tx, record.Envelope{
				RecordVersion: record.SupportedVersion,
				Kind:          item.RecordKind,
				ID:            "apply-" + id,
				Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
				CreatedAt:     now,
				Payload:       payload,
			})
			return err
		}
		// item-a and item-b are running in their worktrees.
		for _, pair := range []struct {
			item, runID, lane string
		}{
			{"item-a", "run-item-a-a1-n1", "lane-1"},
			{"item-b", "run-item-b-a1-n1", "lane-2"},
		} {
			if err := applyItem(pair.item); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO lanes(lane_id, state, run_id, leased_until) VALUES(?, 'leased', ?, ?) ON CONFLICT(lane_id) DO UPDATE SET state='leased', run_id=excluded.run_id, leased_until=excluded.leased_until`,
				pair.lane, pair.runID, nowMs+60000); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO runs(run_id,item_id,lane_id,backend,state,started_at,finished_at,event_seq) VALUES(?,?,?,?,?,?,?,?)`,
				pair.runID, pair.item, pair.lane, "fixture", "running", nowMs, 0, 1); err != nil {
				return err
			}
		}
		// item-c is ready and needs a new worktree.
		return applyItem("item-c")
	})

	lanes := []string{"lane-1", "lane-2", "lane-3", "lane-4"}

	// Case 1: Count bound reached (2 on disk, 2 in use, 0 idle, limit 2).
	countTick := Pass{
		Now:            now.Format(time.RFC3339),
		Root:           f.root,
		StorePath:      f.storePath,
		AvailableLanes: &lanes,
		ItemWorktrees:  true,
		WorktreeSnapshot: &worktreebudget.Snapshot{
			OnDisk:          2,
			InUse:           2,
			Idle:            0,
			CountLimit:      2,
			DiskBudgetBytes: 100 * 1024 * 1024,
			TotalBytes:      1024,
			Items: map[string]worktreebudget.ItemUsage{
				"item-a": {LaneID: "item-a", HasWorktree: true, InUse: true},
				"item-b": {LaneID: "item-b", HasWorktree: true, InUse: true},
			},
		},
	}

	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	pol := seatListPolicy(t)
	var ans WhyAnswer
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		ans, err = Why(r, countTick, &pol, nil)
		return err
	}); err != nil {
		t.Fatalf("Why (count bound): %v", err)
	}

	var rowC *WhyRow
	for i := range ans.Rows {
		if ans.Rows[i].ItemID == "item-c" {
			rowC = &ans.Rows[i]
		}
	}
	if rowC == nil {
		t.Fatalf("item-c missing from Why rows: %+v", ans.Rows)
	}
	if rowC.Reason != ReasonWorktreeBound {
		t.Fatalf("item-c Reason = %q, want %q (row=%+v)", rowC.Reason, ReasonWorktreeBound, *rowC)
	}
	if rowC.Mover != "run-item-a-a1-n1" && rowC.Mover != "run-item-a-a1-n1, run-item-b-a1-n1" {
		t.Fatalf("item-c Mover = %q, want running node name", rowC.Mover)
	}

	// Case 2: Disk bound reached (1 on disk, 1 in use, 0 idle, TotalBytes >= DiskBudgetBytes).
	diskTick := Pass{
		Now:            now.Format(time.RFC3339),
		Root:           f.root,
		StorePath:      f.storePath,
		AvailableLanes: &lanes,
		ItemWorktrees:  true,
		WorktreeSnapshot: &worktreebudget.Snapshot{
			OnDisk:          1,
			InUse:           1,
			Idle:            0,
			CountLimit:      10,
			DiskBudgetBytes: 1024,
			TotalBytes:      2048,
			Items: map[string]worktreebudget.ItemUsage{
				"item-a": {LaneID: "item-a", HasWorktree: true, InUse: true},
			},
		},
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		ans, err = Why(r, diskTick, &pol, nil)
		return err
	}); err != nil {
		t.Fatalf("Why (disk bound): %v", err)
	}
	rowC = nil
	for i := range ans.Rows {
		if ans.Rows[i].ItemID == "item-c" {
			rowC = &ans.Rows[i]
		}
	}
	if rowC == nil || rowC.Reason != ReasonWorktreeBound {
		t.Fatalf("item-c on disk bound = %+v, want Reason=%q", rowC, ReasonWorktreeBound)
	}
}
