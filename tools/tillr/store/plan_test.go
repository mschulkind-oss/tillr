package store

import (
	"errors"
	"path/filepath"
	"testing"
)

func TestApplyPlanRejectsConcurrentCommit(t *testing.T) {
	ctx := t.Context()
	s, err := Open(ctx, filepath.Join(t.TempDir(), "state.db"), true, WithBusyTimeout(0))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = s.Close() })

	err = s.ApplyPlan(ctx, func(rd *Reader) (func(*Tx) error, error) {
		if HoldsWrite() {
			t.Fatal("planner holds the write lock")
		}
		// Another writer can commit while the snapshot is still open. This
		// changes no event cursor: a check of the event head would miss it.
		if err := s.WriteTx(ctx, func(tx *Tx) error {
			_, err := tx.Exec(`INSERT INTO meta (key,value) VALUES ('plan-test','committed')`)
			return err
		}); err != nil {
			t.Fatal(err)
		}
		var rows int
		if err := rd.QueryRow(`SELECT count(*) FROM meta WHERE key='plan-test'`).Scan(&rows); err != nil || rows != 0 {
			t.Fatalf("snapshot changed during planning: count=%d err=%v", rows, err)
		}
		return func(*Tx) error {
			t.Fatal("stale plan was applied")
			return nil
		}, nil
	})
	if !errors.Is(err, ErrStalePlan) {
		t.Fatalf("ApplyPlan: got %v, want ErrStalePlan", err)
	}
}
