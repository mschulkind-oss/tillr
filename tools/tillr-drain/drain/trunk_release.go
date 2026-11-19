package drain

import (
	"context"
	"database/sql"
	"errors"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// repairedTrunkWaiter returns the underlying state only for an acquittal whose
// own fixer landed in the base staging will select, or whose fixer ended by any other
// route (resolved by a proof, or closed) once a proof has passed its checks on a commit
// the selected base contains. A landing newer than the proved base is not evidence.
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
	trunk := IsTrunkFixer(fixer) && strings.HasPrefix(waiting.Reason, RefusalTrunkAlreadyRed+":")
	// A tooling obstacle's wait is released by the same rule: its defect item landed in
	// the base staging will select (defect.go).
	defect := IsDefectItem(fixer) && strings.HasPrefix(waiting.Reason, RefusalToolingDefect+":")
	if !strings.HasPrefix(waiting.Waiter, "item:") || (!trunk && !defect) {
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
	if defect && state != StateLanded {
		// A defect item someone closed or resolved without landing is that person's ruling
		// that the tooling no longer stands in the way: the waiter is released with it.
		declared, err := definition.ForReader(r)
		if err != nil {
			return "", err
		}
		if finishedState(declared.Parsed, state) {
			return underlying, nil
		}
		return "", nil
	}
	declared, err := definition.ForReader(r)
	if err != nil {
		return "", err
	}
	if state != StateLanded && !finishedState(declared.Parsed, state) {
		return "", nil
	}
	green, err := provedLaneBase(ctx, l.cfg.Root, l.gatePolicy.LaneBase)
	if err != nil {
		return "", err
	}
	if state != StateLanded {
		// A fixer a proof resolved (resolveProvedFixers) repaired nothing itself, and
		// neither did one someone closed: in both the proof of its checks is the repair
		// evidence, recorded by resolveProvedFixers whatever ended the fixer. Release once
		// the selected base contains the proved commit, so the fresh run starts from a
		// green tree. A closed fixer whose checks no proof has passed releases nothing.
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
	// The read phase gathers the git answers (the green pointer, ancestry) the write's
	// recheck asks again, so the write runs none (gathered.go).
	gctx, facts := gatheringFor(ctx)
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := readItems(r)
		if err != nil {
			return err
		}
		for _, row := range rows {
			if row.State != StateBlocked {
				continue
			}
			state, err := l.repairedTrunkWaiter(gctx, r, row.ItemID)
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
	return writeGatheredFrom(ctx, s, facts, func(ctx context.Context, tx *store.Tx) error {
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
