package drain

import (
	"context"
	"database/sql"
	"errors"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// ReasonTrunkRewritten holds landings until the trunk contains its lost history again.
const ReasonTrunkRewritten = "trunk-rewritten"
const candidateGatedKind = "drain.gated"
const observedTrunkRef = "refs/tillr/trunk-history/observed"
const lostTrunkRef = "refs/tillr/trunk-history/lost"
const trunkHistoryDDL = `
CREATE TABLE IF NOT EXISTS trunk_history (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 observed_tip TEXT NOT NULL,
 lost_tip TEXT NOT NULL DEFAULT ''
);
`

var errTrunkRewritten = errors.New("landing waits on restored trunk history")

type trunkHistory struct{ Observed, Lost string }

func (h trunkHistory) reason() string {
	return ReasonTrunkRewritten + ": " + h.Lost + " -> " + h.Observed
}

func readTrunkHistory(r *store.Reader) (trunkHistory, error) {
	var h trunkHistory
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='trunk_history')`).Scan(&exists); err != nil {
		return h, err
	}
	if !exists {
		return h, nil
	} // old read-only stores have not observed history yet
	err := r.QueryRow(`SELECT observed_tip,lost_tip FROM trunk_history WHERE id=1`).Scan(&h.Observed, &h.Lost)
	if err == sql.ErrNoRows {
		return h, nil
	}
	return h, err
}

// A negative ancestry answer is not a Git failure. Missing objects and other Git
// errors refuse observation rather than fabricating a rewrite.
func trunkAncestor(ctx context.Context, root, old, tip string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "merge-base", "--is-ancestor", old, tip)
	cmd.Env = boundaryGitEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, Externalf("checking trunk ancestry %s -> %s: %v: %s", old, tip, err, strings.TrimSpace(string(out)))
}

// Every pass persists observation before it considers candidates. Landing workers
// use the same transaction so replacing a loop cannot forget a candidate-free rewrite.
// expected additionally preserves a rewrite the lander caught under its lock.
func (l *Loop) observeTrunk(ctx context.Context, expected string) (trunkHistory, error) {
	l.mu.Lock()
	ready := l.historyPinned
	l.mu.Unlock()
	if ready {
		// Commit ancestry is immutable. An unchanged observed tip cannot restore
		// a lost tip or introduce a rewrite. Do not migrate or take the writer
		// lock merely to record the same row again while runs write their records.
		r, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return trunkHistory{}, err
		}
		var h trunkHistory
		err = r.ReadTx(ctx, func(tx *store.Reader) error { var err error; h, err = readTrunkHistory(tx); return err })
		r.Close()
		if err != nil {
			return trunkHistory{}, err
		}
		tip, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil {
			return trunkHistory{}, err
		}
		if h.Observed == tip && (expected == "" || expected == tip) {
			return h, nil
		}
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return trunkHistory{}, err
	}
	defer s.Close()
	var h trunkHistory
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		h, err = readTrunkHistory(tx.Reader())
		if err != nil {
			return err
		}
		tip, err := l.revParse(ctx, l.cfg.Root, TrunkRef)
		if err != nil {
			return err
		}
		if h.Lost == "" {
			for _, old := range []string{h.Observed, expected} {
				if old == "" {
					continue
				}
				ancestor, err := trunkAncestor(ctx, l.cfg.Root, old, tip)
				if err != nil {
					return err
				}
				if !ancestor {
					h.Lost = old
					break
				}
			}
		}
		if h.Lost != "" {
			restored, err := trunkAncestor(ctx, l.cfg.Root, h.Lost, tip)
			if err != nil {
				return err
			}
			if restored {
				h.Lost = ""
			}
		}
		h.Observed = tip
		// Ref updates precede the row. On a rewrite pin the lost commit before
		// replacing the observed ref; on restoration it remains reachable from
		// the new observed tip. A crash cannot leave a stored tip collectible.
		if h.Lost != "" {
			if _, err := l.git(ctx, l.cfg.Root, "update-ref", lostTrunkRef, h.Lost); err != nil {
				return err
			}
		}
		if _, err := l.git(ctx, l.cfg.Root, "update-ref", observedTrunkRef, tip); err != nil {
			return err
		}
		if h.Lost == "" {
			if _, err := l.git(ctx, l.cfg.Root, "update-ref", "-d", lostTrunkRef); err != nil {
				return err
			}
		}
		_, err = tx.Exec(`INSERT INTO trunk_history(id,observed_tip,lost_tip) VALUES(1,?,?) ON CONFLICT(id) DO UPDATE SET observed_tip=excluded.observed_tip,lost_tip=excluded.lost_tip`, h.Observed, h.Lost)
		return err
	})
	if err == nil {
		l.mu.Lock()
		l.historyPinned = true
		l.mu.Unlock()
	}
	return h, err
}

func (l *Loop) holdRewrite(ctx context.Context, c Candidate, h trunkHistory) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, RunRequest: c.Request, Seat: c.Seat, From: "candidate", Action: ActionHold, Reason: h.reason(), PassID: c.TickID}
		identifyDecision(&d)
		return recordDirtyDecision(tx, d, l.cfg.Now().UTC())
	})
	if err != nil {
		return err
	}
	l.reportf("loop: holding %s (%s); restore the trunk's history\n", c.ItemID, h.reason())
	return errTrunkRewritten
}
