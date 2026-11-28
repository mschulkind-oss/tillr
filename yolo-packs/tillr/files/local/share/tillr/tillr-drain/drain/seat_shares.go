package drain

import (
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/policy"
	"github.com/mschulkind-oss/tillr/store"
)

func usesSeatShares(p *Policy, d Decision) bool {
	return p.UsesSeatShares(d.ItemID, d.Seat, d.SelectSeat, d.WorkflowRestart)
}

// selectSeatDispatch makes and journals the choice inside the claim transaction.
// Planning remains a read-only capacity preview. Choose before checking availability;
// a closed draw keeps its backend without creating a dispatch.
func selectSeatDispatch(tx *store.Tx, d *Decision, p *Policy, shut map[string]bool, at time.Time) (bool, error) {
	if p == nil || d.RunRequest == nil || (d.Action != ActionDispatch && d.Action != ActionRetry) {
		return true, nil
	}
	if commandStage(*d) {
		return true, nil
	}
	if d.WorkflowRestart {
		// A parallel group's primary may already be finished. Check each
		// unfinished retained request independently at the atomic claim too.
		rec, err := terminalRecord(tx.Reader(), d.RunRequest.RunID)
		if err != nil {
			return false, err
		}
		return rec != nil || !shut[d.RunRequest.Backend], nil
	}
	if !usesSeatShares(p, *d) {
		return true, nil
	}
	old := d.RunRequest
	chosen, err := policy.ChooseSeatBackend(tx, p, old.RunID, d.Seat, shut)
	if err != nil {
		return false, err
	}
	request := p.Request(d.ItemID, d.LaneID, old.RunID, chosen)
	// Keep node, continuation, timeout, and correlation metadata while replacing
	// the backend-derived adapter command and model fields.
	copy := *old
	copy.Backend, copy.Command, copy.WorkingDir = request.Backend, request.Command, request.WorkingDir
	if old.WorkingDir == PlainDir {
		copy.WorkingDir = PlainDir
	}
	copy.TimeoutMS = request.TimeoutMS
	if d.Workflow != nil && d.Workflow.Stages[d.NodeIndex].TimeoutMS > 0 {
		copy.TimeoutMS = d.Workflow.Stages[d.NodeIndex].TimeoutMS
	}
	stampDecisionRequest(*d, &copy)
	d.RunRequest = &copy
	if d.Designer != nil {
		adapter := ""
		if d.Designer.Backend != "" {
			adapter = p.Backends[d.Designer.Backend].Adapter
		}
		d.DesignerSessionID, d.DesignerReason = chooseDesigner(d.Designer, d.RunRequest, at, adapter)
	}
	if _, err := tx.Exec(`DELETE FROM seat_waiting_choices WHERE run_id=?`, old.RunID); err != nil {
		return false, err
	}
	if d.BackendMove != nil {
		move := *d.BackendMove
		move.ToBackend = chosen
		if move.RunID == "" {
			move.RunID = old.RunID
		}
		if move.NodeID == "" {
			move.NodeID = copy.NodeID
		}
		if move.FromBackend == move.ToBackend {
			d.BackendMove = nil
		} else {
			if err := recordBackendMoveTx(tx, move, at); err != nil {
				return false, err
			}
			d.BackendMove = nil
		}
	}
	return true, recordSeatDispatch(tx, *d)
}

func recordSeatDispatch(tx *store.Tx, d Decision) error {
	if d.Seat == "" || d.RunRequest == nil || d.RunRequest.Backend == "" || commandStage(d) {
		return nil
	}
	return policy.RecordSeatDispatchRow(tx, d.RunRequest.RunID, d.Seat, d.RunRequest.Backend)
}
