package drain

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestLegacyPassMigrationPreservesEveryColumnAndDoesNotOverwriteNewMeasurements(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(scratchUnderDurable(t), "legacy-full.db")
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO drain_ticks (tick_id, started_at, stage_ms, dispatch_ms, gate_ms, land_ms,
         gate_budget_ms, gate_over_budget, ended_at, decision_counts, applied_count, gate_count) VALUES
		 ('gen-1',100,1,2,3,4,5,0,110,'{"dispatch":1}',1,2),
		 ('gen-2',200,6,7,8,9,10,1,220,'{"judge":1}',2,3)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	for i := 0; i < 2; i++ {
		s, err = store.Open(ctx, path, true)
		if err != nil {
			t.Fatal(err)
		}
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			var different, count int
			if err := r.QueryRow(`SELECT COUNT(*) FROM (SELECT * FROM drain_ticks EXCEPT SELECT * FROM drain_passes)`).Scan(&different); err != nil {
				return err
			}
			if err := r.QueryRow(`SELECT COUNT(*) FROM drain_passes`).Scan(&count); err != nil {
				return err
			}
			if different != 0 || count != 2 {
				t.Errorf("migration: different=%d rows=%d", different, count)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		s.Close()
	}
	// The legacy source must not overwrite a later asynchronous measurement.
	s, err = store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE drain_passes SET gate_ms=99 WHERE tick_id='gen-1'`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	s, err = store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var gate int
		err := r.QueryRow(`SELECT gate_ms FROM drain_passes WHERE tick_id='gen-1'`).Scan(&gate)
		if gate != 99 {
			t.Errorf("new measurement overwritten: %d", gate)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestPassColumnsMigrateWithoutInventingHistoricalMeasurements(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(scratchUnderDurable(t), "old.db")
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`DROP TABLE drain_ticks;
   CREATE TABLE drain_ticks(tick_id TEXT PRIMARY KEY, started_at INTEGER NOT NULL,
    stage_ms INTEGER NOT NULL, dispatch_ms INTEGER NOT NULL, gate_ms INTEGER NOT NULL,
    land_ms INTEGER NOT NULL, gate_budget_ms INTEGER NOT NULL, gate_over_budget INTEGER NOT NULL);
   INSERT INTO drain_ticks VALUES ('old', 100, 1, 2, 3, 4, 5, 0);
   DROP TABLE dispatches;
   CREATE TABLE dispatches(decision_id TEXT PRIMARY KEY, item_id TEXT NOT NULL, lane_id TEXT,
    run_id TEXT, action TEXT NOT NULL, reason TEXT NOT NULL, dispatched_at INTEGER NOT NULL,
    deferred_by TEXT, seat TEXT);
   INSERT INTO dispatches(decision_id,item_id,action,reason,dispatched_at) VALUES ('old','item','hold','blocked',100);`)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}
	s, err = store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var end, gates sql.NullInt64
		var counts, pass sql.NullString
		var gate int
		if err := r.QueryRow(`SELECT gate_ms, ended_at, decision_counts, gate_count FROM drain_passes`).Scan(&gate, &end, &counts, &gates); err != nil {
			return err
		}
		if gate != 3 || end.Valid || counts.Valid || gates.Valid {
			t.Errorf("migration invented or replaced data: %d %v %v %v", gate, end, counts, gates)
		}
		if err := r.QueryRow(`SELECT pass_id FROM dispatches`).Scan(&pass); err != nil {
			return err
		}
		if pass.Valid {
			t.Errorf("migration invented historical pass: %v", pass)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
