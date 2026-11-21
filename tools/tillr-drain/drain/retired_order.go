package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const retiredOrderKind = "drain.order-record"

func init() {
	// Existing stores keep these events. Only retirement can append another.
	if err := event.Register(retiredOrderKind, Utility); err != nil {
		panic(err)
	}
}

type retiredOrderWork struct {
	Phase     string     `json:"phase"`
	Cleaned   bool       `json:"cleaned,omitempty"`
	Owner     *LoopOwner `json:"owner,omitempty"`
	Candidate Candidate  `json:"candidate"`
	Reason    string     `json:"reason,omitempty"`
}

func legacyOrderWorktree(root, path string) bool {
	dir := filepath.Dir(path)
	if root == "" || filepath.Base(path) != WorktreeDir || filepath.Dir(dir) != filepath.Join(root, ".order-record") {
		return false
	}
	id, ok := strings.CutPrefix(filepath.Base(dir), "run-order-record-")
	seq, claim, split := strings.Cut(id, "-")
	return ok && split && seq != "" && claim != "" && strings.Trim(seq+claim, "0123456789") == ""
}

// retireOrderWork discards an abandoned recorder's detached worktree and
// appends a terminal receipt. It never creates, gates, or lands a candidate.
// A stopped cleanup can retry: an already-removed worktree is fine.
func (l *Loop) retireOrderWork(ctx context.Context) error {
	if l.orderWorkRetired || l.nonLanding {
		return nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	var work retiredOrderWork
	var seq int64
	var live bool
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		var raw []byte
		err := r.QueryRow("SELECT seq, payload FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1", retiredOrderKind).Scan(&seq, &raw)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		if err := json.Unmarshal(raw, &work); err != nil {
			return err
		}
		if work.Phase == "done" || work.Owner == nil {
			return nil
		}
		here, err := currentLoopOwner()
		if err != nil {
			return err
		}
		// This process only runs the new code, including after an in-place exec.
		if *here == *work.Owner {
			return nil
		}
		if fromOtherDomain(work.Owner, here) {
			ended, err := endedSessionOwners(r)
			if err != nil {
				return err
			}
			identity, err := processStoreIdentity(l.cfg.Store)
			if err != nil {
				return err
			}
			live = !foreignOwnerGone(identity, *work.Owner, *here, ended)
			return nil
		}
		dead, err := ownerDead(work.Owner, here)
		live = !dead
		return err
	}); err != nil {
		return err
	}
	if seq == 0 || (work.Phase == "done" && work.Cleaned) {
		l.orderWorkRetired = true
		return nil
	}
	if live {
		// Another process still owns the old work. Check again next pass.
		return nil
	}
	c := work.Candidate
	if !legacyOrderWorktree(l.cfg.LaneRoot, c.Worktree) || filepath.Base(filepath.Dir(c.Worktree)) != c.RunID {
		return Internalf("cannot retire order record %q: unexpected worktree %q", c.RunID, c.Worktree)
	}
	if err := withWorktreeRegistryLock(ctx, l.cfg.Root, func() error {
		worktrees, err := listWorktreesLocked(ctx, l.cfg.Root)
		if err != nil {
			return err
		}
		if wt := worktreeFor(worktrees, c.Worktree); wt != nil {
			if wt.Branch != "" {
				return Internalf("cannot retire order record %q: worktree is not detached", c.RunID)
			}
			_, err = l.git(ctx, l.cfg.Root, "worktree", "remove", "--force", c.Worktree)
			return err
		}
		return nil
	}); err != nil {
		return err
	}
	settled := false
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		var latest int64
		if err := tx.QueryRow("SELECT max(seq) FROM events WHERE kind=?", retiredOrderKind).Scan(&latest); err != nil {
			return err
		}
		if latest != seq {
			return nil
		}
		if work.Phase != "done" {
			work.Reason = "order recording retired"
		}
		work.Phase, work.Cleaned, work.Owner = "done", true, nil
		settled = true
		return appendEffect(tx, retiredOrderKind, c.RunID, work, l.cfg.Now())
	}); err != nil {
		return err
	}
	l.orderWorkRetired = settled
	return nil
}
