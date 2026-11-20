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
	// ReasonDecisionCollision holds an item whose planned decision could not be recorded,
	// because its id is already recorded with other columns: a drain defect, held on the
	// one item it names rather than ending the loop.
	ReasonDecisionCollision = "decision-collision"
	// ReasonSettleFailed holds an item whose run could not be settled: an error while
	// settling one run is that item's condition, not the loop's.
	ReasonSettleFailed = "settle-failed"
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
		for _, node := range parallelNodes(d) {
			// Finished readers may be retained in a resumed parallel group. Only
			// never-started claims lose their share count when staging is canceled.
			if _, err := tx.Exec(`DELETE FROM seat_dispatches WHERE run_id=? AND NOT EXISTS(SELECT 1 FROM runs WHERE run_id=?)`, node.RunRequest.RunID, node.RunRequest.RunID); err != nil {
				return err
			}
			// The claim wrote each reader's group membership. A never-started reader's
			// membership goes with the claim; a retained finished reader's, reopened by
			// the resume claim, is settled again. Either way no unsettled member is left
			// on a lane this transaction releases, the rule run.freeLane keeps for a
			// released group: an unsettled member reads back as a live run beside
			// whatever claims the lane next.
			if _, err := tx.Exec(`DELETE FROM read_only_runs WHERE run_id=? AND NOT EXISTS(SELECT 1 FROM runs WHERE run_id=?)`, node.RunRequest.RunID, node.RunRequest.RunID); err != nil {
				return err
			}
			if _, err := tx.Exec(`UPDATE read_only_runs SET settled=1 WHERE run_id=?`, node.RunRequest.RunID); err != nil {
				return err
			}
			if hasNodes, err := tableExists(tx.Reader(), "node_runs"); err != nil {
				return err
			} else if hasNodes {
				if _, err := tx.Exec(`DELETE FROM node_runs WHERE run_id=? AND outcome='running' AND NOT EXISTS(SELECT 1 FROM runs WHERE run_id=?)`, node.RunRequest.RunID, node.RunRequest.RunID); err != nil {
					return err
				}
			}
		}
		if _, err := tx.Exec(`DELETE FROM dispatches WHERE decision_id=? AND item_id=? AND run_id=?`, d.DecisionID, d.ItemID, d.RunRequest.RunID); err != nil {
			return err
		}
		if _, err := tx.Exec(`UPDATE lanes SET state=?,run_id=NULL,leased_until=NULL WHERE lane_id=? AND run_id=?`, "idle", d.LaneID, d.RunRequest.RunID); err != nil {
			return err
		}
		// The run never started, so the claim's conflict move is undone with it
		// (unlaunched_claim.go): the re-plan offers the item another lane.
		_, err := undoClaimedConflict(tx, d, l.cfg.Now().UTC())
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
