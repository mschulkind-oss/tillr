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
			if old == h.observed {
				if err := t.storedTipExists(old, "observed"); err != nil {
					return nil, false, err
				}
			} else if err := t.HasCommit(old); err != nil {
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
		if err := t.storedTipExists(h.lost, "lost"); err != nil {
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

// storedTipExists refuses a trunk tip the store recorded and the repository no longer has.
// It is the reverse of a lost receipt: the store committed a landing and its observation,
// and Git lost the commit — Git's defaults flush neither refs nor loose objects, so an OS
// crash can drop a landing the store kept. That is not a malformed request and not a
// rewrite to wait out; it is a loss a person has to repair, so it is an external failure
// that names the commit, and no landing proceeds over it.
//
// Only a missing object is that loss. `git cat-file -e` exits 1 for exactly that and 128
// for anything else (an unreadable object store, a bad repository), so any other failure
// is reported as the Git failure it is, and an operator is not sent to restore a commit
// that may still be there.
func (t *Trunk) storedTipExists(sha, which string) error {
	_, stderr, err := t.runGit(nil, "cat-file", "-e", sha)
	if t.ctx != nil && t.ctx.Err() != nil {
		return t.ctx.Err()
	}
	if err == nil {
		if _, stderr, err := t.runGit(nil, "cat-file", "-e", sha+"^{commit}"); err != nil {
			return Externalf("the store's trunk history records %s as the %s trunk tip, but in %s that object is not a commit: %s", sha, which, t.repo, failureText(stderr, err))
		}
		return nil
	}
	var exit *exec.ExitError
	if !errors.As(err, &exit) || exit.ExitCode() != 1 {
		return Externalf("reading the %s trunk tip %s that the store's trunk history records: %s", which, sha, failureText(stderr, err))
	}
	return Externalf("the store's trunk history records %s as the %s trunk tip, but %s no longer has that commit: Git lost a landing the store recorded (an OS crash before Git flushed it), or the commit was pruned; nothing was moved. Restore %s (fetch it from a clone or remote that has it and fast-forward the trunk to it) before landing again; if it cannot be restored, the trunk_history row and the landings row naming it have to be corrected by hand",
		sha, which, t.repo, sha)
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
