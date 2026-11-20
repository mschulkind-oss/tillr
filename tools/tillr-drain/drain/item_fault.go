package drain

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mschulkind-oss/tillr-drain/faults"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

var errItemFaultHeld = errors.New("item fault held")

const (
	itemFaultKind              = faults.HeldKind
	itemFaultClearedKind       = faults.ClearedKind
	ReasonCandidateMismatch    = "candidate-record-mismatch"
	ReasonDefinitionSyncFailed = "definition-sync-failed"
	// ReasonLandHarnessError holds an item whose land step could not deliver it. Like a
	// gate that cannot judge, it is a fault about this candidate's delivery, not a verdict
	// on its code, and it must not end the loop for every other lane.
	ReasonLandHarnessError = "land-harness-error"
	// ReasonDecisionCollision holds an item whose planned decision could not be recorded,
	// because its id is already recorded with other columns: a drain defect, held on the
	// one item it names rather than ending the loop.
	ReasonDecisionCollision = "decision-collision"
	// ReasonSettleFailed holds an item whose run could not be settled: an error while
	// settling one run is that item's condition, not the loop's.
	ReasonSettleFailed = "settle-failed"
	// ReasonAdapterRefused holds an item whose run an adapter refused before any model ran
	// (`<adapter>: refused: <reason>`, exit 2). The adapter's words are the hold's detail
	// and the operator is the mover: the brief or the environment is what to fix, so no
	// backend is closed, none is failed over to, and no second run is staged.
	ReasonAdapterRefused = "adapter-refused"
)

func init() {
	for _, kind := range []string{itemFaultKind, itemFaultClearedKind} {
		if err := event.Register(kind, Utility); err != nil {
			panic(err)
		}
	}
}

type ItemFault = faults.Hold

func ReadItemFaults(r *store.Reader) (map[string]ItemFault, error) { return faults.Read(r) }

func (l *Loop) holdItemFault(ctx context.Context, c Candidate, reason, detail string) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := requireHold(tx.Reader(), reason); err != nil {
			return err
		}
		if c.RunID != "" {
			// A run's fault judges that run; a terminal item's run is not judged.
			if err := refuseTerminalItem(tx.Reader(), c.ItemID, reason); err != nil {
				return err
			}
		}
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		if f, ok := faults[c.ItemID]; ok && f.Reason == reason && f.Detail == detail && f.RunID == c.RunID && f.LaneID == c.LaneID {
			return nil
		}
		f := ItemFault{ItemID: c.ItemID, RunID: c.RunID, LaneID: c.LaneID, Reason: reason, Detail: detail}
		body, err := json.Marshal(f)
		if err != nil {
			return err
		}
		id := c.ItemID
		_, err = tx.Append(event.Event{Kind: itemFaultKind, ObjectType: "item", ObjectID: &id, Payload: body, At: l.cfg.Now().UTC()})
		return err
	})
	if err == nil {
		l.reportf("loop: holding %s (%s, run %s, lane %s): %s\n", c.ItemID, reason, c.RunID, c.LaneID, detail)
	}
	return err
}

// ClearItemFault acknowledges a diagnosed fault, binding the act to the latest
// fault's sequence so a stale command cannot clear a newer conviction.
func ClearItemFault(tx *store.Tx, itemID string, seq int64, at time.Time) error {
	faults, err := ReadItemFaults(tx.Reader())
	if err != nil {
		return err
	}
	f, ok := faults[itemID]
	if !ok {
		return Usagef("item %q has no fault hold", itemID)
	}
	if f.Seq != seq {
		return Usagef("item %q has fault %d, not %d; inspect waiting again", itemID, f.Seq, seq)
	}
	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: itemFaultClearedKind, ObjectType: "item", ObjectID: &itemID, Payload: body, At: at})
	return err
}

func (l *Loop) candidateFault(ctx context.Context, c Candidate, detail string) error {
	if err := l.holdItemFault(ctx, c, ReasonCandidateMismatch, detail); err != nil {
		return err
	}
	return errItemFaultHeld
}
func (l *Loop) landFault(ctx context.Context, c Candidate, detail string) error {
	if err := l.holdItemFault(ctx, c, ReasonLandHarnessError, detail); err != nil {
		return err
	}
	return errItemFaultHeld
}
func (l *Loop) gateFault(ctx context.Context, c Candidate, detail string) error {
	if err := l.holdItemFault(ctx, c, StopGateHarnessError, detail); err != nil {
		return err
	}
	return errItemFaultHeld
}

// cancelStaging retracts a claim that could not launch, undoing every store effect the
// claim wrote (retractClaim): the dispatch row, the lane lease, the workflow node rows, the
// conflict move and the parallel readers' memberships. The append-only started history
// still explains the aborted staging, but it cannot collide with a later plan assigning
// the same attempt to a different lane.
//
// A claim that continued a pending workflow node (drain.node-advanced) puts that pending
// work back, so the item is offered its next node again; marking it examined instead ended
// the pending work and left the item held as run-link-missing (2026-11-19, a build's
// offered commit refused by the lane that held it). Any other claim is examined, as before.
func (l *Loop) cancelStaging(ctx context.Context, d Decision) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		return retractClaim(tx, d, l.cfg.Now().UTC(), true)
	})
}

// A sync fault's explicit clearance invalidates the cached document read even
// when trunk has not changed. Read the append sequence, not a timestamp tie.
func syncClearVersion(r *store.Reader) (int64, error) {
	var seq int64
	err := r.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events WHERE kind=? AND json_extract(payload,'$.reason')=?`, itemFaultClearedKind, ReasonDefinitionSyncFailed).Scan(&seq)
	return seq, err
}

// holdAdapterRefusal holds the item whose run an adapter refused before any model ran, with
// the adapter's own words as the reason, and settles the lane. It counts no backend failure.
func (l *Loop) holdAdapterRefusal(ctx context.Context, lane string, request run.Request, log string) (string, error) {
	words, _ := AdapterRefusal(log)
	c := Candidate{ItemID: request.ItemID, LaneID: lane, RunID: request.RunID}
	if err := l.holdItemFault(ctx, c, ReasonAdapterRefused, words); err != nil {
		return "", err
	}
	return l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "adapter refused the brief"), nil
}
