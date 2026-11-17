package drain

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/mschulkind-oss/tillr-drain/faults"
	"time"

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

// cancelStaging retracts the claim and dispatch row together. The append-only
// started/examined history still explains the aborted staging, but it cannot
// collide with a later plan assigning the same attempt to a different lane.
func (l *Loop) cancelStaging(ctx context.Context, d Decision) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := appendEffect(tx, examinationDoneKind, d.RunRequest.RunID, struct{}{}, l.cfg.Now().UTC()); err != nil {
			return err
		}
		if _, err := tx.Exec(`DELETE FROM dispatches WHERE decision_id=? AND item_id=? AND run_id=?`, d.DecisionID, d.ItemID, d.RunRequest.RunID); err != nil {
			return err
		}
		_, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND run_id=?`, "idle", d.LaneID, d.RunRequest.RunID)
		return err
	})
}

// A sync fault's explicit clearance invalidates the cached document read even
// when trunk has not changed. Read the append sequence, not a timestamp tie.
func syncClearVersion(r *store.Reader) (int64, error) {
	var seq int64
	err := r.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events WHERE kind=? AND json_extract(payload,'$.reason')=?`, itemFaultClearedKind, ReasonDefinitionSyncFailed).Scan(&seq)
	return seq, err
}
