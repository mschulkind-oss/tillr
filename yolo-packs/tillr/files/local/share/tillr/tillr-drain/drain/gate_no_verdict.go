package drain

import (
	"context"
	"errors"

	"github.com/mschulkind-oss/tillr/store"
)

// errGateNoVerdict is the examination outcome of a gate that ran and answered "error":
// no verdict exists, so the candidate is kept on its lane and the next pass gates it again.
var errGateNoVerdict = errors.New("candidate waits for the gate to reach a verdict")

// holdGateNoVerdict records the wait after a gate that reached no verdict. The
// examination may release the lane once the candidate pin is verified. The loop
// gates the same commit again until the gate judges it. Nothing is counted,
// nothing is acknowledged, no retry is
// authorized and no fault is written, so the hold's mover is the next pass and never a
// person. The gate's answer is already recorded as its own `gate.verdict` (result
// "error"), which is the evidence a reader follows.
//
// The operator-held `gate-harness-error` remains for the one case re-gating cannot help:
// the gate step itself failed (Steps.Gate returned an external error), so the drain could
// not start the gate or read or record what it answered, and the same step would fail the
// same way again until someone repairs it.
func (l *Loop) holdGateNoVerdict(ctx context.Context, c Candidate) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := refuseTerminalItem(tx.Reader(), c.ItemID, ReasonGateNoVerdict); err != nil {
			return err
		}
		d := Decision{ItemID: c.ItemID, LaneID: c.LaneID, RunRequest: c.Request, Seat: c.Seat, From: "candidate", Action: ActionHold, Reason: ReasonGateNoVerdict, PassID: c.TickID}
		identifyDecision(&d)
		return recordDirtyDecision(tx, d, l.cfg.Now().UTC())
	})
	if err != nil {
		return err
	}
	l.reportf("loop: gate reached no verdict on %s (run %s, lane %s): %s; the next pass gates the same candidate again\n", c.ItemID, c.RunID, c.LaneID, ReasonGateNoVerdict)
	return errGateNoVerdict
}
