package drain

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-drain/holdrules"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
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
	// ReasonPackSyncFailed keeps the unjudged candidate when mirror preparation fails.
	ReasonPackSyncFailed = "pack-sync-failed"
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
	// ReasonStageFailed holds an item whose run could not be staged on its lane or run
	// directory. Staging one item's run is that item's condition, so its unlaunched claim
	// is retracted, its lane is freed, and the loop continues for every other item.
	ReasonStageFailed = "stage-failed"
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
	f := ItemFault{ItemID: c.ItemID, RunID: c.RunID, LaneID: c.LaneID, Reason: reason, Detail: detail}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		var err error
		f.Seq, err = l.appendItemFaultTx(tx, f)
		return err
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

// Render the command after append: the event sequence binds it to this exact fault.
func faultClearDetail(f ItemFault, storePath string) string {
	return f.Detail + "; after correcting the cause, run " + f.ClearCommand(storePath)
}

func decisionCollisionDetail(collision *decisionCollisionError, action string) string {
	return collision.Error() + "; the planned " + action + " was not applied. A drain defect named two decisions alike"
}

func (l *Loop) holdItemFaultTx(tx *store.Tx, c Candidate, reason, detail string) error {
	return l.recordItemFaultTx(tx, ItemFault{ItemID: c.ItemID, RunID: c.RunID, LaneID: c.LaneID, Reason: reason, Detail: detail})
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
	if f.Reason == reasonVerifyTriage {
		return Usagef("item %q is held for verifier triage; %s", itemID, f.ClearCommand(""))
	}
	return clearItemFault(tx, f, at)
}

func clearItemFault(tx *store.Tx, f ItemFault, at time.Time) error {
	body, err := json.Marshal(f)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: itemFaultClearedKind, ObjectType: "item", ObjectID: &f.ItemID, Payload: body, At: at})
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
	var at time.Time
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		at = l.cfg.Now().UTC()
		return retractClaim(tx, d, at, true)
	})
	if err == nil {
		l.notifyStartLaneReleased(d.LaneID, d.RunRequest.RunID, true, at)
	}
	return err
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
		if _, err := l.revParse(ctx, l.cfg.Root, TrunkRef+"^{commit}"); err != nil {
			return fmt.Errorf("%v; reading trunk: %w", failure, err)
		}
	}
	c := Candidate{ItemID: d.ItemID, RunID: d.RunRequest.RunID, LaneID: d.LaneID}
	reason, detail := ReasonSettleFailed, fmt.Sprintf("%s: %v", step, failure)
	var collision *decisionCollisionError
	if errors.As(failure, &collision) {
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

func (l *Loop) clearUnrunNode(ctx context.Context, runID string) error {
	if l.cfg.Store == "" || runID == "" {
		return nil
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		hasNodes, err := tableExists(tx.Reader(), "node_runs")
		if err != nil || !hasNodes {
			return err
		}
		_, err = tx.Exec(`DELETE FROM node_runs WHERE run_id=? AND outcome='running'`, runID)
		return err
	})
}
