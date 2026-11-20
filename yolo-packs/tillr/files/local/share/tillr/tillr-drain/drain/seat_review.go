package drain

import (
	"context"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func firstOpenSeatBackend(p Policy, seat string, shut map[string]bool) (string, bool) {
	for _, name := range p.seatList(seat) {
		if !shut[name] {
			return name, true
		}
	}
	return "", false
}

// An implicit agent judge is not a workflow node but still dispatches the review
// seat. Journal its choice before constructing the receipt; restored receipts
// retain that request instead of drawing again. Workflow evaluators were already
// chosen in their node's claim and do not pass through here.
func (l *Loop) selectReviewDispatch(ctx context.Context, req run.Request) (run.Request, bool, error) {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return req, false, err
	}
	defer s.Close()
	open := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		shut, err := readQuotaShutBackends(tx.Reader(), l.cfg.Now(), l.currentPolicy())
		if err != nil {
			return err
		}
		policy := *l.cfg.Policy
		// Item overrides select the worker, not its independent review.
		policy.Overrides = nil
		name, ok := firstOpenSeatBackend(policy, "review", shut)
		if !ok {
			return errSeatWindowClosed
		}
		d := Decision{DecisionID: "decision-" + req.RunID, ItemID: req.ItemID, LaneID: req.LaneID, Seat: "review", Action: ActionDispatch, Reason: "agent-review", SelectSeat: true, RunRequest: policy.request(req.ItemID, req.LaneID, req.RunID, name)}
		if d.RunRequest != nil {
			d.RunRequest.Seat = req.Seat
			if d.RunRequest.Seat == "" {
				d.RunRequest.Seat = d.Seat
			}
			d.RunRequest.Workflow = req.Workflow
			d.RunRequest.Node = req.Node
			d.RunRequest.NodeID = req.NodeID
		}
		ok, err = selectSeatDispatch(tx, &d, &policy, shut, l.cfg.Now())
		if err != nil {
			return err
		}
		if !ok {
			return errSeatWindowClosed
		}
		if err := recordPassDecision(tx, d, l.cfg.Now()); err != nil {
			return err
		}
		req = *d.RunRequest
		open = true
		return nil
	})
	if err == errSeatWindowClosed {
		return req, false, nil
	}
	return req, open, err
}
