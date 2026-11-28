package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"slices"
	"time"

	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

var errItemFaultHeld = errors.New("item fault held")

func (l *Loop) holdItemFault(ctx context.Context, c Candidate, reason, detail string) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	f := ItemFault{ItemID: c.ItemID, RunID: c.RunID, LaneID: c.LaneID, HeadSHA: c.HeadSHA, WorkerHeadSHA: c.WorkerHeadSHA, Reason: reason, Detail: detail}
	if reason == ReasonGateStalled || reason == StopGateHarnessError {
		f.GateDigest = l.installedGateDigest()
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		f.Seq, err = l.appendItemFaultTx(tx, f)
		if err != nil {
			return err
		}
		return retractRefusedRunClaim(tx, f, l.cfg.Now().UTC())
	})
	if err == nil {
		if reason == ReasonPreserveFailed || reason == ReasonDecisionCollision {
			detail = faultClearDetail(f, l.cfg.Store)
		}
		l.reportf("loop: holding %s (%s, run %s, lane %s): %s\n", c.ItemID, reason, c.RunID, c.LaneID, detail)
		if reason == ReasonStageFailed || reason == ReasonSettleFailed || reason == ReasonPackSyncFailed {
			l.reportf("loop: %s mover: %s\n", c.ItemID, f.CompleteMover(holdrules.For(reason).Mover, l.cfg.Store))
		}
	}
	return err
}

func (l *Loop) holdItemFaultTx(tx *store.Tx, c Candidate, reason, detail string) error {
	return l.recordItemFaultTx(tx, ItemFault{ItemID: c.ItemID, RunID: c.RunID, LaneID: c.LaneID, HeadSHA: c.HeadSHA, WorkerHeadSHA: c.WorkerHeadSHA, Reason: reason, Detail: detail})
}

func (l *Loop) recordItemFaultTx(tx *store.Tx, f ItemFault) error {
	_, err := l.appendItemFaultTx(tx, f)
	return err
}

// appendItemFaultTx returns the active fault's sequence, including on a duplicate.
func (l *Loop) appendItemFaultTx(tx *store.Tx, f ItemFault) (int64, error) {
	if err := requireHold(tx.Reader(), f.Reason); err != nil {
		return 0, err
	}
	if f.RunID != "" {
		if err := refuseTerminalItem(tx.Reader(), f.ItemID, f.Reason); err != nil {
			return 0, err
		}
	}
	faults, err := ReadItemFaults(tx.Reader())
	if err != nil {
		return 0, err
	}
	if prior, ok := faults[f.ItemID]; ok {
		seq := prior.Seq
		prior.Seq, prior.AtMS = 0, 0
		if prior == f || prior.Reason == reasonVerifyTriage && f.Reason == reasonVerifyTriage {
			return seq, nil
		}
	}
	body, err := json.Marshal(f)
	if err != nil {
		return 0, err
	}
	return tx.Append(event.Event{Kind: itemFaultKind, ObjectType: "item", ObjectID: &f.ItemID, Payload: body, At: l.cfg.Now().UTC()})
}

// ClearItemFault acknowledges a diagnosed fault, binding the act to the latest
// fault's sequence so a stale command cannot clear a newer conviction.
func ClearItemFault(tx *store.Tx, itemID string, seq int64, at time.Time) error {
	_, err := ClearItemFaultHold(tx, itemID, seq, at)
	return err
}

// ClearItemFaultHold acknowledges a diagnosed fault, binding the act to the
// latest fault's sequence so a stale command cannot clear a newer conviction,
// and returns the cleared hold.
func ClearItemFaultHold(tx *store.Tx, itemID string, seq int64, at time.Time) (ItemFault, error) {
	faults, err := ReadItemFaults(tx.Reader())
	if err != nil {
		return ItemFault{}, err
	}
	f, ok := faults[itemID]
	if !ok {
		return ItemFault{}, Usagef("item %q has no fault hold", itemID)
	}
	if f.Seq != seq {
		return ItemFault{}, Usagef("item %q has fault %d, not %d; inspect waiting again", itemID, f.Seq, seq)
	}
	if f.Reason == reasonVerifyTriage {
		return ItemFault{}, Usagef("item %q is held for verifier triage; %s", itemID, f.ClearCommand(""))
	}
	if err := retractRefusedRunClaim(tx, f, at); err != nil {
		return ItemFault{}, err
	}
	if err := releaseRetainedRunProcessesLease(tx, f, at); err != nil {
		return ItemFault{}, err
	}
	if err := clearItemFault(tx, f, at); err != nil {
		return ItemFault{}, err
	}
	return f, nil
}

func retractRefusedRunClaim(tx *store.Tx, f ItemFault, at time.Time) error {
	if !execSetupRefused(f) {
		return nil
	}
	r := tx.Reader()
	works, err := readPending(r)
	if err != nil {
		return err
	}
	w := works[f.ItemID]
	if w == nil || w.Done || w.Ready || w.Examining || w.Examiner != nil || w.Decision.RunRequest == nil ||
		w.Decision.RunRequest.RunID != f.RunID || len(w.Decision.Parallel) != 0 {
		return nil
	}
	var recorded bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs WHERE run_id=?)`, f.RunID).Scan(&recorded); err != nil || recorded {
		return err
	}
	// Release the unlaunched dispatch in the same write as the fault change.
	// Once the fault is clear, the next pass can offer the item with no
	// synthetic run record in its history.
	return retractClaim(tx, w.Decision, at, true)
}

// releaseRetainedRunProcessesLease releases the examination lease and claim kept when
// run-processes-live retained an ended run's lane. Pending work still reserves the lane
// for this item, so clearing the fault lets the running loop examine the run on a
// following pass without opening the lane to another item.
func releaseRetainedRunProcessesLease(tx *store.Tx, f ItemFault, at time.Time) error {
	if f.Reason != ReasonRunProcessesLive {
		return nil
	}
	r := tx.Reader()
	works, err := readPending(r)
	if err != nil {
		return err
	}
	w := works[f.ItemID]
	if w == nil || w.Done || w.Decision.RunRequest == nil {
		return nil
	}
	runID, laneID := w.Decision.RunRequest.RunID, w.Decision.LaneID
	if runID == "" || laneID == "" {
		return nil
	}
	if f.LaneID != "" && f.LaneID != laneID {
		return nil
	}
	if f.RunID != "" && !slices.Contains(examinationRunIDs(w), f.RunID) {
		return nil
	}
	if w.Candidate == nil {
		if len(w.Decision.Parallel) > 0 {
			done, err := parallelTerminal(r, w.Decision)
			if err != nil || !done {
				return err
			}
		} else {
			rec, err := terminalRecord(r, runID)
			if err != nil || rec == nil {
				return err
			}
		}
	}
	res, err := tx.Exec(`UPDATE lanes SET state = ?, run_id = NULL, leased_until = NULL WHERE lane_id = ? AND run_id = ? AND state = ? AND leased_until IS NULL`, run.LaneStateIdle, laneID, runID, run.LaneStateLeased)
	if err != nil {
		return err
	}
	n, err := res.RowsAffected()
	if err != nil {
		return err
	}
	if w.Examiner != nil {
		if err := appendEffect(tx, examinationOwnerKind, runID, (*LoopOwner)(nil), at); err != nil {
			return err
		}
	}
	if w.Examining {
		if err := appendEffect(tx, examinationReleasedKind, runID, struct{}{}, at); err != nil {
			return err
		}
	}
	if n > 0 {
		if err := appendEffect(tx, laneReleasedKind, runID, laneReleased{LaneID: laneID, Cause: laneReleaseEnded}, at); err != nil {
			return err
		}
	}
	return nil
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
	var at time.Time
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		at = l.cfg.Now().UTC()
		return retractClaim(tx, d, at, true)
	})
	if err == nil {
		l.signalPassWake()
	}
	return err
}

// holdAdapterRefusal holds the item whose run an adapter refused before any model ran, with
// the adapter's own words as the reason, and settles the lane. It counts no backend failure.
func (l *Loop) holdAdapterRefusal(ctx context.Context, lane string, request run.Request, rec run.Record, log string) (string, error) {
	words, _ := RecordAdapterRefusal(rec, log)
	c := Candidate{ItemID: request.ItemID, LaneID: lane, RunID: request.RunID}
	if err := l.holdItemFault(ctx, c, ReasonAdapterRefused, words); err != nil {
		return "", err
	}
	if err := l.clearUnrunNode(ctx, request.RunID); err != nil {
		return "", err
	}
	return l.abandon(ctx, lane, request.RunID, l.headOf(ctx, lane), "adapter refused the brief"), nil
}

// holdStageFailed records a durable stage-failed fault on d's item when staging d's run
// fails on a per-item step. Operator stops and storeless unit tests return stageErr directly.
func (l *Loop) holdStageFailed(ctx context.Context, d Decision, step string, stageErr error) error {
	if stoppedByOperator(ctx, stageErr) || ctx.Err() != nil {
		return stageErr
	}
	if l.cfg.Store == "" {
		return stageErr
	}
	itemID := d.ItemID
	runID := ""
	if d.RunRequest != nil {
		if itemID == "" {
			itemID = d.RunRequest.ItemID
		}
		runID = d.RunRequest.RunID
	}
	if itemID == "" {
		return stageErr
	}
	detail := stageErr.Error()
	if step != "" {
		detail = step + ": " + detail
	}
	if err := l.holdItemFault(ctx, Candidate{ItemID: itemID, RunID: runID, LaneID: d.LaneID}, ReasonStageFailed, detail); err != nil {
		var termErr *itemTerminalError
		if errors.As(err, &termErr) {
			return nil
		}
		return err
	}
	l.clearStageInfraLine(itemID, d.LaneID)
	return nil
}

// holdExaminationFailure is the per-item boundary for a claimed examination. The
// loop still stops for interruptions and errors in its own machinery.
func (l *Loop) holdExaminationFailure(ctx context.Context, d Decision, work *pendingWork, step string, failure error) error {
	code := ExitCode(failure)
	if ctx.Err() != nil || stoppedByOperator(ctx, failure) || errors.Is(failure, context.Canceled) ||
		errors.Is(failure, errRestartPending) || code == record.ExitUsage || code == record.ExitIncompatible {
		return failure
	}
	if terminal := asItemTerminal(failure); terminal != nil {
		return l.settleTerminalItem(ctx, work, terminal)
	}
	var judgmentErr *gateJudgmentRecordError
	if errors.As(failure, &judgmentErr) {
		return failure
	}
	// Read before opening the writer: it must not create a replacement store if
	// the original disappeared. The hold write rechecks terminal state atomically.
	var graphCompleted bool
	if err := l.readStore(ctx, func(r *store.Reader) error {
		if err := refuseTerminalItem(r, d.ItemID, step); err != nil {
			return err
		}
		if step != ActionSettle || d.Workflow == nil {
			return nil
		}
		return r.QueryRow(`SELECT EXISTS(SELECT 1 FROM events WHERE kind='drain.graph-completed' AND object_type='run' AND object_id=?)`, d.RunRequest.RunID).Scan(&graphCompleted)
	}); err != nil {
		if terminal := asItemTerminal(err); terminal != nil {
			return l.settleTerminalItem(ctx, work, terminal)
		}
		return fmt.Errorf("%v; reading store: %w", failure, err)
	}
	// The graph has already published its completion evidence. Leave it pending
	// so the next loop can finish the item state move without running nodes again.
	if graphCompleted {
		return failure
	}
	// A missing trunk is a shared fault, not a failure of the candidate's checkout.
	// Check only on the error path.
	if !l.nonLanding {
		if _, err := l.revParse(ctx, l.cfg.Root, l.trunkRef()+"^{commit}"); err != nil {
			return fmt.Errorf("%v; reading trunk: %w", failure, err)
		}
	}
	c := Candidate{ItemID: d.ItemID, RunID: d.RunRequest.RunID, LaneID: d.LaneID}
	reason, detail := ReasonSettleFailed, fmt.Sprintf("%s: %v", step, failure)
	var collision *decisionCollisionError
	var seatConflict *seatDispatchConflictError
	if errors.As(failure, &collision) || errors.As(failure, &seatConflict) {
		reason = ReasonDecisionCollision
	}
	err := l.holdItemFault(ctx, c, reason, detail)
	if terminal := asItemTerminal(err); terminal != nil {
		return l.settleTerminalItem(ctx, work, terminal)
	}
	if err != nil {
		return fmt.Errorf("%v; recording item fault: %w", failure, err)
	}
	return nil
}

func (l *Loop) clearStageInfraLine(itemID, lane string) {
	l.mu.Lock()
	delete(l.infraLines, itemID+"\x00"+lane)
	l.mu.Unlock()
}
