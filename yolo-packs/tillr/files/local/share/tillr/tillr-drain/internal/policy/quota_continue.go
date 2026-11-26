package policy

import (
	"context"
	"database/sql"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func QuotaPauseBackend(r *store.Reader, id string) (string, error) {
	var backend string
	err := r.QueryRow(`SELECT backend FROM quota_pauses WHERE run_id=?`, id).Scan(&backend)
	if err == sql.ErrNoRows || (err != nil && strings.Contains(err.Error(), "no such table: quota_pauses")) {
		return "", nil
	}
	return backend, err
}

func QuotaInterrupted(ctx context.Context, storePath, id string) (bool, error) {
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return false, err
	}
	defer s.Close()
	backend := ""
	err = s.ReadTx(ctx, func(r *store.Reader) error { var err error; backend, err = QuotaPauseBackend(r, id); return err })
	return backend != "", err
}

func RecordQuotaPeerPause(tx *store.Tx, runID, itemID, laneID, backend string, pauseTimeout int64, now time.Time) error {
	rec, err := runhistory.TerminalRecord(tx.Reader(), runID)
	if err != nil {
		return err
	}
	if rec != nil {
		// A resume retains the preceding terminal row until it finishes.
		// Only a later finish, not that old row, establishes natural completion.
		var latest string
		err := tx.Reader().QueryRow(`SELECT kind FROM events WHERE object_type='run' AND object_id=? AND kind IN (?,?) ORDER BY seq DESC LIMIT 1`, runID, run.ResumeKind, run.FinishKind).Scan(&latest)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if latest != run.ResumeKind {
			return nil
		}
	}
	var live int
	if err := tx.Reader().QueryRow(`SELECT count(*) FROM lanes WHERE run_id=? AND state=?`, runID, run.LaneStateLeased).Scan(&live); err != nil {
		return err
	}
	if live != 1 {
		return nil
	}
	inserted, err := tx.Exec(`INSERT INTO quota_pauses(run_id,item_id,lane_id,backend,state,paused_at) VALUES(?,?,?,?,'requested',?) ON CONFLICT(run_id) DO NOTHING`, runID, itemID, laneID, backend, now.UnixMilli())
	if err != nil {
		return err
	}
	n, err := inserted.RowsAffected()
	if err != nil {
		return err
	}
	if n == 0 {
		return nil
	}
	return runhistory.AppendEffect(tx, run.PauseRequestKind, runID, map[string]int64{"timeout_ms": pauseTimeout * 1000}, now)
}

func HoldQuotaRun(ctx context.Context, storePath, runID, itemID, laneID, backend string, now time.Time) error {
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO quota_pauses(run_id,item_id,lane_id,backend,state,paused_at) VALUES(?,?,?,?,'waiting',?) ON CONFLICT(run_id) DO UPDATE SET state='waiting'`, runID, itemID, laneID, backend, now.UnixMilli())
		return err
	})
}

func ClearQuotaPause(ctx context.Context, storePath, id string) error {
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error { _, err := tx.Exec(`DELETE FROM quota_pauses WHERE run_id=?`, id); return err })
}
