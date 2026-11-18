package drain

import (
	"context"
	"database/sql"
	"errors"

	"github.com/mschulkind-oss/tillr/store"
)

// ReasonPreserveFailed holds an item whose settled run left uncommitted work in its lane
// that the loop could not preserve. The lane is retained as it stands — not reset, and not
// released for the next staging to clear — and the item waits for the operator, who
// repairs the cause and clears the fault so the next pass settles the run again.
const ReasonPreserveFailed = "preserve-failed"

// stopLaneRetained is the settlement's stop for that case. It ends the one examination
// without recording it examined — which would release the lane — and the loop takes it as
// that lane's condition, not its own: the hold keeps the item from being re-examined, so
// nothing spins, and every other lane goes on.
const stopLaneRetained = "lane-retained"

// retainUnpreserved answers abandon when preservation failed. It does not hide that
// failure: the cause is reported and recorded as the item's fault detail. Only a store
// that cannot record the hold stops the loop, because then nothing would keep the lane's
// work from the next staging.
//
// On 2026-11-17 the loop stopped on exactly this ("lane lane-14 retained: could not
// preserve uncommitted work: ... database is locked"), so one lane's settlement took
// every other lane's work down with it.
func (l *Loop) retainUnpreserved(ctx context.Context, lane, runID string, cause error) string {
	l.reportf("loop: lane %s retained: could not preserve uncommitted work: %v\n", lane, cause)
	itemID, err := l.runItem(ctx, runID)
	if err == nil && itemID == "" {
		err = errors.New("no item to hold for the retained lane")
	}
	if err == nil {
		err = l.holdItemFault(ctx, Candidate{ItemID: itemID, RunID: runID, LaneID: lane}, ReasonPreserveFailed, cause.Error())
	}
	if err != nil {
		l.reportf("loop: lane %s retained, and its item could not be held: %v\n", lane, err)
		return StopFault
	}
	return stopLaneRetained
}

// runItem answers the item a run belongs to, or "" for no run.
func (l *Loop) runItem(ctx context.Context, runID string) (string, error) {
	if runID == "" {
		return "", nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", err
	}
	defer s.Close()
	var itemID sql.NullString
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT item_id FROM runs WHERE run_id = ?`, runID).Scan(&itemID)
	})
	if errors.Is(err, sql.ErrNoRows) {
		return "", nil
	}
	return itemID.String, err
}
