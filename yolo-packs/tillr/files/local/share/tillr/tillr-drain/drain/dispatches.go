// Architecture and invariants: docs/reference/tillr-drain-successor.md
package drain

import (
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr/store"
)

// recordPassDecision inserts an immutable decision in the tick's transaction. A replay
// of the same payload is a no-op; its first recording time stays intact. Reusing an
// identity for different columns is an error, never permission to overwrite history.
func recordPassDecision(tx *store.Tx, decision Decision, at time.Time) error {
	columns := decisionColumns(decision)
	if columns.deferredBy == nil && decision.Action == ActionHold && strings.HasPrefix(decision.Reason, ReasonRunLinkMissing) {
		if _, _, moverAction := runLinkMissingHold(tx.Reader(), decision.ItemID, decision.SupersedesRunID); moverAction != "" {
			columns.deferredBy = moverAction
		}
	}
	inserted, err := storelog.InsertDispatchRow(tx, storelog.DispatchRow{
		DecisionID: decision.DecisionID,
		ItemID:     decision.ItemID,
		LaneID:     columns.laneID,
		RunID:      columns.runID,
		Action:     decision.Action,
		Reason:     decision.Reason,
		DeferredBy: columns.deferredBy,
		Seat:       columns.seat,
		PassID:     decision.PassID,
		Backend:    columns.backend,
	}, at)
	if err != nil || !inserted {
		return err
	}
	if decision.Action == ActionDispatch || decision.Action == ActionRetry {
		for _, node := range parallelNodes(decision) {
			if err := recordSeatDispatch(tx, node); err != nil {
				return err
			}
		}
	}
	// Every dispatch fact names its build: a decision with no Inputs (a design-seat or
	// owner-question dispatch) records the planning build alone.
	inputs := decision.Inputs
	if inputs == nil && decision.DispatchBuild != "" {
		inputs = &RunInputs{TillrBuild: decision.DispatchBuild}
	}
	if inputs != nil && decision.RunRequest != nil && (decision.Action == ActionDispatch || decision.Action == ActionRetry) {
		for _, node := range parallelNodes(decision) {
			node.Inputs = inputs
			if err := appendEffect(tx, inputRecordedKind, node.RunRequest.RunID, node, at); err != nil {
				return err
			}
		}
	}
	return nil
}

// dispatchColumns is one decision's nullable columns as SQL arguments: a hold has no lane
// and no run, only a fence deferral names the item it waits behind, and only a seat item
// names a seat. They are nullable together so a row is a complete account of the decision
// whether it dispatched anything or not.
type dispatchColumns struct {
	laneID, runID, deferredBy, seat, backend any
}

// decisionColumns answers the row's nullable columns: a hold has no lane and no run, only a
// fence deferral names the item it waits behind, and only an item that asked for a seat
// carries one. The seat is recorded on the row and not only in the decision payload because
// the row is the store's durable account of the dispatch, and it is what explains the
// backend the run reached without the policy that was live at dispatch time.
func decisionColumns(decision Decision) dispatchColumns {
	var columns dispatchColumns
	if decision.LaneID != "" {
		columns.laneID = decision.LaneID
	}
	if decision.RunRequest != nil {
		columns.runID = decision.RunRequest.RunID
		if decision.RunRequest.Backend != "" {
			columns.backend = decision.RunRequest.Backend
		}
	}
	if decision.DeferredBy != "" {
		columns.deferredBy = decision.DeferredBy
	}
	if decision.Seat != "" {
		columns.seat = decision.Seat
	}
	return columns
}

// recordDecision writes one decision's row inside the tick's transaction.
func recordDecision(tx *store.Tx, decision Decision, at time.Time) error {
	if decision.Action == ActionHold {
		return recordPassDecision(tx, decision, at)
	}
	if err := storelog.EnsureDispatchUnrecorded(tx, decision.DecisionID, decision.ItemID); err != nil {
		return err
	}
	return recordPassDecision(tx, decision, at)
}
