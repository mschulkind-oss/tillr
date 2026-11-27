package drain

import (
	"context"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWorktreeRestoreOnDemandAfterCollection(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t)
	l, _ := newWorktreeCollectTestLoop(t, f)
	l.cfg.WorktreeCountOverride = 10

	itemID := "item-restore"
	runID := "run-item-restore-a1-n1"
	_, wt, sc, _, head := stageFixtureItemDir(t, f, itemID, runID)

	// Mark item blocked on owner so collectIdleWorktrees collects its worktree and scratch.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO item_criteria(item_id, criterion_id, text, state) VALUES(?, 'c1', 'criterion 1', 'open')`, itemID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE items SET state='blocked', waiting_on='{"waiter":"owner","reason":"ruling"}' WHERE item_id=?`, itemID)
		return err
	})
	if _, err := l.collectIdleWorktrees(ctx); err != nil {
		t.Fatalf("collectIdleWorktrees: %v", err)
	}
	if _, err := os.Stat(wt); !os.IsNotExist(err) {
		t.Fatalf("expected worktree %s to be collected, stat err=%v", wt, err)
	}
	if _, err := os.Stat(sc); !os.IsNotExist(err) {
		t.Fatalf("expected scratch %s to be collected, stat err=%v", sc, err)
	}

	// Unblock item and stage the next node with Continue: true.
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET state='in-queue', waiting_on='' WHERE item_id=?`, itemID)
		return err
	})

	tpl := filepath.Join(f.root, "brief-template.md")
	writeCollectFile(t, tpl, "# {{ITEM_ID}}\n{{TASK}}\n")
	stager := &LaneStager{
		Root:     f.root,
		Store:    f.storePath,
		LaneRoot: f.laneRoot,
		Template: tpl,
	}
	base := runGit(t, f.root, "rev-parse", "HEAD")
	spec := StageSpec{
		ItemID:      itemID,
		RunID:       "run-item-restore-a1-n2",
		LaneID:      itemID,
		WorkingDir:  WorktreeDir,
		BaseSHA:     base,
		ItemBaseSHA: base,
		Continue:    true,
		Node: &WorkflowStage{
			ID:     "verify",
			Seat:   "verify",
			Access: "write",
		},
	}
	if err := stager.Stage(ctx, spec); err != nil {
		t.Fatalf("Stage(Continue: true) after collection: %v", err)
	}
	if _, err := os.Stat(wt); err != nil {
		t.Fatalf("expected worktree %s to be restored: %v", wt, err)
	}
	gotHead := runGit(t, wt, "rev-parse", "HEAD")
	if gotHead != head {
		t.Fatalf("restored HEAD = %s, want %s", gotHead, head)
	}
	if _, err := os.Stat(filepath.Join(wt, itemID+".txt")); err != nil {
		t.Fatalf("expected committed worktree file to be present: %v", err)
	}
}

func TestWorktreeRestoreFailureProducesNoTerminalEnding(t *testing.T) {
	ctx := context.Background()
	f := newRefusalLoopFixture(t)

	itemID := "item-restore-fail"
	fixtureWrite(t, f, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT OR IGNORE INTO items(item_id,title,state,updated_at,seat) VALUES(?,?,?,?,?)`,
			itemID, itemID, StateInQueue, 1000, ""); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO item_criteria(item_id, criterion_id, text, state) VALUES(?, 'c1', 'criterion 1', 'open')`, itemID)
		return err
	})

	tpl := filepath.Join(f.root, "brief-template.md")
	writeCollectFile(t, tpl, "# {{ITEM_ID}}\n{{TASK}}\n")
	stager := &LaneStager{
		Root:     f.root,
		Store:    f.storePath,
		LaneRoot: f.laneRoot,
		Template: tpl,
	}
	base := runGit(t, f.root, "rev-parse", "HEAD")
	err := stager.Stage(ctx, StageSpec{
		ItemID:      itemID,
		RunID:       "run-item-restore-fail-a1-n2",
		LaneID:      itemID,
		WorkingDir:  WorktreeDir,
		BaseSHA:     base,
		ItemBaseSHA: base,
		Continue:    true,
		Node: &WorkflowStage{
			ID:     "verify",
			Seat:   "verify",
			Access: "write",
		},
	})
	if err == nil {
		t.Fatal("expected Stage to fail when item pin is missing, got nil")
	}
	// Verify no terminal ending or run row was recorded for the failed restore.
	fixtureRead(t, f, func(r *store.Reader) error {
		var count int
		if err := r.QueryRow(`SELECT COUNT(*) FROM runs WHERE item_id=?`, itemID).Scan(&count); err != nil {
			return err
		}
		if count != 0 {
			t.Fatalf("expected 0 run rows after failed restore, got %d", count)
		}
		return nil
	})
}
