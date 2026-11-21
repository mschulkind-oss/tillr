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

// repairedTrunkWaiter returns the underlying state for an item held behind a trunk fixer
// or tooling defect item once the hold's condition is gone: when the fixer or defect item
// is closed, when the fixer landed in the base staging will select, or when a proof has
// passed the fixer's checks on a commit the selected base contains. A landing newer than
// the proved base is not evidence.
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
	trunk := IsTrunkFixer(fixer)
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
	declared, err := definition.ForReader(r)
	if err != nil {
		return "", err
	}
	if state == declared.Parsed.Closed || state == "closed" {
		// Closing a fixer or defect item is the operator's ruling that it no longer stands
		// in the way (including when its fix was landed by hand): release every item held
		// behind it on the next pass without an operator retry.
		return underlying, nil
	}
	if defect && state != StateLanded {
		if finishedState(declared.Parsed, state) {
			return underlying, nil
		}
		return "", nil
	}
	green, err := provedLaneBase(ctx, l.cfg.Root, l.gatePolicy.LaneBase)
	if err != nil {
		return "", err
	}
	if state != StateLanded {
		// A fixer resolved without landing (resolveProvedFixers, or an already-on-trunk
		// resolution later proved) — or a live fixer kept open after a rerun passed its
		// checks — repaired nothing itself yet: the proof of its checks is the repair
		// evidence, recorded by resolveProvedFixers. Release once the selected base
		// contains the proved commit, so the fresh run starts from a green tree. An open
		// fixer whose checks no proof has passed after its last red observation releases
		// nothing.
		proved, err := fixerProvedGreenAfterSeen(r, fixer)
		if err != nil {
			return "", err
		}
		if proved == "" && finishedState(declared.Parsed, state) {
			proved, err = fixerProvedGreen(r, fixer)
			if err != nil {
				return "", err
			}
		}
		if proved == "" {
			return "", nil
		}
		if green != "" {
			th, err := readTrunkHistory(r)
			if err != nil {
				return "", err
			}
			ok, err := proofPastRed(ctx, l.cfg.Root, th.Lost, proved, green)
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
	registerRewriteStore(l.cfg.Root, l.cfg.Store)
	if err := l.releaseTrunkObstacleHolds(ctx); err != nil {
		return err
	}
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
