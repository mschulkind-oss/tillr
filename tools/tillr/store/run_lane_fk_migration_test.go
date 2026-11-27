package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestDropRunLaneForeignKeysPreservesRowsAndAllowsLaneDeletion(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "legacy-fk.db")

	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`
CREATE TABLE lanes (
  lane_id TEXT PRIMARY KEY,
  state TEXT NOT NULL,
  run_id TEXT,
  leased_until INTEGER
);
CREATE TABLE runs (
  run_id TEXT PRIMARY KEY,
  item_id TEXT,
  lane_id TEXT NOT NULL REFERENCES lanes(lane_id),
  backend TEXT NOT NULL,
  state TEXT NOT NULL,
  started_at INTEGER NOT NULL,
  finished_at INTEGER NOT NULL
);
CREATE TABLE read_only_runs (
  run_id TEXT PRIMARY KEY,
  group_id TEXT NOT NULL,
  item_id TEXT NOT NULL,
  lane_id TEXT NOT NULL REFERENCES lanes(lane_id),
  settled INTEGER NOT NULL DEFAULT 0
);
CREATE INDEX read_only_group_members ON read_only_runs(group_id, settled);
INSERT INTO lanes(lane_id, state) VALUES ('item-ended', 'idle');
INSERT INTO runs(run_id, item_id, lane_id, backend, state, started_at, finished_at)
  VALUES ('run-1', 'item-ended', 'item-ended', 'fixture', 'exited', 1, 2);
INSERT INTO read_only_runs(run_id, group_id, item_id, lane_id, settled)
  VALUES ('ro-1', 'grp-1', 'item-ended', 'item-ended', 1);`)
		return err
	})
	if err != nil {
		s.Close()
		t.Fatal(err)
	}
	// Before migration, deleting the idle lanes row must fail due to the legacy FK.
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`DELETE FROM lanes WHERE lane_id = 'item-ended'`)
		return err
	}); err == nil {
		s.Close()
		t.Fatal("expected legacy FK to block deleting lanes row before migration")
	}
	if err := s.ForgetMigrations(); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	// Reopening runs migrate(), which drops the legacy foreign keys on runs and read_only_runs.
	migrated, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer migrated.Close()

	if err := migrated.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`DELETE FROM lanes WHERE lane_id = 'item-ended'`); err != nil {
			return err
		}
		var runCount, roCount, idxCount int
		if err := tx.QueryRow(`SELECT count(*) FROM runs WHERE run_id = 'run-1'`).Scan(&runCount); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT count(*) FROM read_only_runs WHERE run_id = 'ro-1'`).Scan(&roCount); err != nil {
			return err
		}
		if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type='index' AND name='read_only_group_members'`).Scan(&idxCount); err != nil {
			return err
		}
		if runCount != 1 || roCount != 1 || idxCount != 1 {
			t.Fatalf("after migration: runs=%d read_only_runs=%d idx=%d, want 1/1/1", runCount, roCount, idxCount)
		}
		return nil
	}); err != nil {
		t.Fatalf("deleting idle lane row after migration: %v", err)
	}
}
