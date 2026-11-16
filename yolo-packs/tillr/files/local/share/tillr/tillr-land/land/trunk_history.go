package land

import (
	"database/sql"

	"github.com/mschulkind-oss/tillr/store"
)

// This optional row belongs to the drain. Loop-observed requests reconcile it
// inside the landing transaction so an old argv cannot bypass a newer hold.
// Manual stores without the row still check the observation supplied on argv.
type trunkHistory struct{ observed, lost string }

func (t *Trunk) reconcileHistory(tx *store.Tx, expected, tip string) (*trunkHistory, bool, error) {
	var exists bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='trunk_history')`).Scan(&exists); err != nil {
		return nil, false, Internalf("checking trunk history schema: %v", err)
	}
	h := &trunkHistory{}
	if exists {
		err := tx.QueryRow(`SELECT observed_tip,lost_tip FROM trunk_history WHERE id=1`).Scan(&h.observed, &h.lost)
		if err != nil && err != sql.ErrNoRows {
			return nil, false, Internalf("reading trunk history: %v", err)
		}
	}
	if h.lost == "" {
		for _, old := range []string{h.observed, expected} {
			if old == "" {
				continue
			}
			if err := t.HasCommit(old); err != nil {
				return nil, false, err
			}
			if !t.IsAncestor(old, tip) {
				h.lost = old
				break
			}
		}
	}
	if h.lost != "" {
		if err := t.HasCommit(h.lost); err != nil {
			return nil, false, err
		}
		if t.IsAncestor(h.lost, tip) {
			h.lost = ""
		}
	}
	// Restoring an older durable hold does not waive this request's later
	// observation. A waiter may have seen more history than the held row.
	if h.lost == "" && expected != "" && !t.IsAncestor(expected, tip) {
		h.lost = expected
	}
	h.observed = tip
	rewritten := h.lost != ""
	if !exists {
		return nil, rewritten, nil
	}
	if err := t.saveHistory(tx, h); err != nil {
		return nil, false, err
	}
	return h, rewritten, nil
}

func (t *Trunk) saveHistory(tx *store.Tx, h *trunkHistory) error {
	// The observed and lost refs retain objects independently of reflogs. Pin the
	// lost object first; overwriting observed must never make stored history dangling.
	if h.lost != "" {
		if _, err := t.git("update-ref", "refs/tillr/trunk-history/lost", h.lost); err != nil {
			return err
		}
	}
	if _, err := t.git("update-ref", "refs/tillr/trunk-history/observed", h.observed); err != nil {
		return err
	}
	if h.lost == "" {
		if _, err := t.git("update-ref", "-d", "refs/tillr/trunk-history/lost"); err != nil {
			return err
		}
	}
	_, err := tx.Exec(`INSERT INTO trunk_history(id,observed_tip,lost_tip) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET observed_tip=excluded.observed_tip,lost_tip=excluded.lost_tip`, h.observed, h.lost)
	if err != nil {
		return Internalf("writing trunk history: %v", err)
	}
	return nil
}
