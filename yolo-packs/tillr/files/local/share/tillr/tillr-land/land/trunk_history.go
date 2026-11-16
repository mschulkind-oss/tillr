package land

import (
	"database/sql"
	"errors"
	"os/exec"

	"github.com/mschulkind-oss/tillr/store"
)

// This optional row belongs to the drain. Loop-observed requests reconcile it
// inside the landing transaction so an old argv cannot bypass a newer hold.
// Manual stores without the row still check the observation supplied on argv.
type trunkHistory struct{ observed, lost string }

// historySnapshot distinguishes a missing table or row from an initialized empty
// observation, so initialization racing preparation cannot bypass validation.
type historySnapshot struct {
	table, row bool
	history    trunkHistory
}

func readHistory(r rowQueryer) (historySnapshot, error) {
	snapshot := historySnapshot{}
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='trunk_history')`).Scan(&snapshot.table); err != nil {
		return snapshot, Internalf("checking trunk history schema: %v", err)
	}
	if snapshot.table {
		err := r.QueryRow(`SELECT observed_tip,lost_tip FROM trunk_history WHERE id=1`).Scan(&snapshot.history.observed, &snapshot.history.lost)
		if err != nil && err != sql.ErrNoRows {
			return snapshot, Internalf("reading trunk history: %v", err)
		}
		snapshot.row = err == nil
	}
	return snapshot, nil
}

func (t *Trunk) prepareHistory(snapshot historySnapshot, expected, tip string) (*trunkHistory, bool, error) {
	h := snapshot.history
	if h.lost == "" {
		for _, old := range []string{h.observed, expected} {
			if old == "" {
				continue
			}
			if err := t.HasCommit(old); err != nil {
				return nil, false, err
			}
			ancestor, err := t.historyAncestor(old, tip)
			if err != nil {
				return nil, false, err
			}
			if !ancestor {
				h.lost = old
				break
			}
		}
	}
	if h.lost != "" {
		if err := t.HasCommit(h.lost); err != nil {
			return nil, false, err
		}
		ancestor, err := t.historyAncestor(h.lost, tip)
		if err != nil {
			return nil, false, err
		}
		if ancestor {
			h.lost = ""
		}
	}
	// Restoring an older durable hold does not waive this request's later
	// observation. A waiter may have seen more history than the held row.
	if h.lost == "" && expected != "" {
		ancestor, err := t.historyAncestor(expected, tip)
		if err != nil {
			return nil, false, err
		}
		if !ancestor {
			h.lost = expected
		}
	}
	h.observed = tip
	rewritten := h.lost != ""
	if !snapshot.table {
		return nil, rewritten, nil
	}
	return &h, rewritten, nil
}

// Exit 1 is a negative ancestry answer; cancellation and Git failures are errors,
// never evidence that history was rewritten.
func (t *Trunk) historyAncestor(old, tip string) (bool, error) {
	_, stderr, err := t.runGit(nil, "merge-base", "--is-ancestor", old, tip)
	if t.ctx != nil && t.ctx.Err() != nil {
		return false, t.ctx.Err()
	}
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, Externalf("checking trunk ancestry: %s", failureText(stderr, err))
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
