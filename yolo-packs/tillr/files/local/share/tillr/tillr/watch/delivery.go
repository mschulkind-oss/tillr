package watch

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// Notice's Seq refers to the alert's opening event, not the fact that caused it.
type Notice struct {
	Seq     int64
	AlertID string
	Bundle  Bundle
}

// Pending returns open foreground alerts in notice sequence order.
func (e Evaluator) Pending(ctx context.Context, owner string) ([]Notice, error) {
	if owner != "foreground" {
		return nil, nil
	}
	// A rule's evaluation eligibility does not withdraw its open notices.
	foreground := map[string]bool{}
	for _, r := range e.Rules {
		if r.Delivery.Receiver == "foreground" {
			foreground[r.ID] = true
		}
	}
	var notices []Notice
	err := e.Store.ReadTx(ctx, func(rd *store.Reader) error {
		rows, err := rd.Query(`SELECT v.seq,a.alert_id,a.rule_id,a.bundle_json
   FROM alert_episodes a JOIN events v ON v.object_id=a.alert_id AND v.kind=?
   WHERE a.state='open' AND v.seq>COALESCE((SELECT seen_seq FROM foreground_delivery WHERE owner_handle=?),0)
   ORDER BY v.seq`, openedKind, owner)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var n Notice
			var id string
			var bundle []byte
			if err := rows.Scan(&n.Seq, &n.AlertID, &id, &bundle); err != nil {
				return err
			}
			if !foreground[id] {
				continue
			}
			if err := json.Unmarshal(bundle, &n.Bundle); err != nil {
				return err
			}
			notices = append(notices, n)
		}
		return rows.Err()
	})
	return notices, err
}

// Seen confirms a printed cursor without changing alert state.
func (e Evaluator) Seen(ctx context.Context, owner string, seq int64) error {
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		var shown, seen int64
		err := tx.QueryRow(`SELECT shown_seq,seen_seq FROM foreground_delivery WHERE owner_handle=?`, owner).Scan(&shown, &seen)
		if err != nil && err != sql.ErrNoRows {
			return err
		}
		if seq > shown {
			return fmt.Errorf("seen cursor %d exceeds shown_seq %d for %q", seq, shown, owner)
		}
		if seq < 0 {
			return fmt.Errorf("seen cursor must be nonnegative")
		}
		if seq <= seen {
			return nil
		}
		_, err = tx.Exec(`UPDATE foreground_delivery SET seen_seq=? WHERE owner_handle=?`, seq, owner)
		return err
	})
}

// Shown records successful printing, never confirmation.
func (e Evaluator) Shown(ctx context.Context, owner string, seq int64) error {
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO foreground_delivery(owner_handle,shown_seq) VALUES (?,?)
   ON CONFLICT(owner_handle) DO UPDATE SET shown_seq=MAX(shown_seq,excluded.shown_seq) WHERE shown_seq<excluded.shown_seq`, owner, seq)
		return err
	})
}

func (e Evaluator) BeginWait(ctx context.Context, owner string) (time.Time, error) {
	now := e.now()
	err := e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO foreground_delivery(owner_handle,waiting_since,last_wait_at) VALUES (?,?,?)
   ON CONFLICT(owner_handle) DO UPDATE SET waiting_since=excluded.waiting_since,last_wait_at=excluded.last_wait_at`, owner, now.UnixMilli(), now.UnixMilli())
		return err
	})
	return now, err
}

func (e Evaluator) EndWait(ctx context.Context, owner string, start time.Time) error {
	return e.Store.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE foreground_delivery SET waiting_since=NULL,last_wait_at=? WHERE owner_handle=? AND waiting_since=?`, e.now().UnixMilli(), owner, start.UnixMilli())
		return err
	})
}
