package drain

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// repairedTrunkWaiter returns the underlying state only for an acquittal whose
// own fixer landed in the base staging will select. Closed or otherwise terminal
// fixers are not repair evidence, nor is a landing newer than the proved base.
func (l *Loop) repairedTrunkWaiter(ctx context.Context, r *store.Reader, id string) (string, error) {
	var state string
	if err := r.QueryRow(`SELECT state FROM items WHERE item_id=?`, id).Scan(&state); err != nil {
		return "", err
	}
	if state != StateBlocked {
		return "", nil
	}
	waiting, underlying, err := item.ReadWaitingOn(r, id)
	if err != nil || waiting == nil {
		return "", err
	}
	fixer := strings.TrimPrefix(waiting.Waiter, "item:")
	if !strings.HasPrefix(waiting.Waiter, "item:") || !IsTrunkFixer(fixer) ||
		!strings.HasPrefix(waiting.Reason, RefusalTrunkAlreadyRed+":") {
		return "", nil
	}
	if underlying != StateInQueue && underlying != "not-started" {
		return "", nil
	}
	err = r.QueryRow(`SELECT state FROM items WHERE item_id=?`, fixer).Scan(&state)
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	if state != StateLanded && state != StateResolvedElsewhere {
		return "", nil
	}
	green, err := provedLaneBase(ctx, l.cfg.Root, l.gatePolicy.LaneBase)
	if err != nil {
		return "", err
	}
	if state == StateResolvedElsewhere {
		// A fixer a green heavy proof resolved (resolveProvedFixers) repaired nothing
		// itself; the proof is the repair evidence. Release once the selected base
		// contains the proved commit, so the fresh run starts from a green tree.
		proved, err := fixerProvedGreen(r, fixer)
		if err != nil || proved == "" {
			return "", err
		}
		if green != "" {
			ok, err := containsCommit(ctx, l.cfg.Root, proved, green)
			if err != nil || !ok {
				return "", err
			}
		}
		return underlying, nil
	}
	// A waiter is not a dependency edge. Check its landing before clearing it,
	// using the same proof rule as ordinary blockers, so the fresh run cannot
	// start from a green pointer that still excludes the repair.
	waits, err := unprovedBlockers(ctx, r, l.cfg.Root, green,
		map[string]string{fixer: StateLanded}, map[string][]string{id: {fixer}})
	if err != nil || waits[id] {
		return "", err
	}
	return underlying, nil
}

// releaseTrunkAcquittals runs before planning, without a write when nothing is
// releasable. Recheck under the write transaction so an owner's newer disposition
// cannot be overwritten by an earlier read. SetState clears the waiter and keeps
// the release in the item's event history; the old candidate is never retained.
func (l *Loop) releaseTrunkAcquittals(ctx context.Context) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	var ready []string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := readItems(r)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.State != StateBlocked {
				continue
			}
			state, err := l.repairedTrunkWaiter(ctx, r, row.ItemID)
			if err != nil {
				return err
			}
			if state != "" {
				ready = append(ready, row.ItemID)
			}
		}
		return nil
	})
	if err != nil || len(ready) == 0 {
		return err
	}
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, id := range ready {
			state, err := l.repairedTrunkWaiter(ctx, tx.Reader(), id)
			if err != nil {
				return err
			}
			if state == "" {
				continue
			}
			if _, err := item.SetState(ctx, tx, id, state, l.cfg.Now().UTC()); err != nil {
				return err
			}
		}
		return nil
	})
}
