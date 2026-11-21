package drain

import (
	"context"
	"encoding/json"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Runner is where `once` hands the dispatch decisions' run requests: the caller-named
// transport design §5.5 requires the face to take.
//
// The seam is the request records, not a function that launches anything, because how a
// request reaches `tillr run exec` is a separate decision (a subprocess of this binary is
// one, and it is not this lane's). A runner that answers an error fails the face, and the
// transaction rolls back: a dispatches row for a request that was never handed over is a
// record asserting a dispatch that did not happen.
//
// It is called once per tick, with every dispatch decision's request in decision order, so
// a transport that must write one file per tick writes it once. A tick that dispatches
// nothing calls it with no requests — a successful tick with zero dispatches is still a
// completed face.
type Runner interface {
	HandOver(requests []record.Envelope) error
}

// Once is `drain once`: the reaping, the planning `plan` does, then one write transaction
// that records every decision in `dispatches` and hands the dispatch decisions' requests
// to runner.
//
// The plan is computed inside the same transaction that writes the rows, from the
// transaction's own reader, so the decisions and the rows they became describe one moment
// rather than two. The reap happens in that transaction and before the plan for the same
// reason: a lane whose lease expired is a lane the loop may reuse, so no decision may be
// computed against a lease the same tick is about to free — and a tick that failed after
// reaping would leave a store where the freed lane and the decisions about it came from
// different moments.
// The questions source is the caller's, and it reads Vantage's index lazily: a tick whose
// items name no design document never runs the tool, and one that does reads it on the first
// such item. That happens inside this face's write transaction, which is why the source is
// an argument rather than something this function constructs — the caller owns the policy on
// when the subprocess runs.
func Once(ctx context.Context, s *store.Store, tick Pass, policy *Policy, questions QuestionSource, runner Runner) error {
	if runner == nil {
		return Usagef("once needs a runner: the face has nowhere to hand a dispatch's run.request")
	}
	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		at, err := tick.At()
		if err != nil {
			return err
		}
		// Reap before planning (design §5.5, D-UT17): the planner then never sees a lane
		// held by a run that is gone, and the reaped run is closed in the same ask.
		if _, err := run.ReapExpiredLeases(ctx, tx, at); err != nil {
			return err
		}
		planned, err := Plan(tx.Reader(), tick, policy, questions)
		if err != nil {
			return err
		}
		decisions := make([]Decision, 0, len(planned))
		for _, env := range planned {
			var decision Decision
			if err := json.Unmarshal(env.Payload, &decision); err != nil {
				return Internalf("reading back the decision for %q: %v", env.ID, err)
			}
			decisions = append(decisions, decision)
		}
		// Only a changed observation allocates a generation. A repeated empty plan
		// leaves both the generation counter and the dispatch table unchanged.
		correlation := ""
		for _, decision := range decisions {
			var present int
			err := tx.QueryRow(`SELECT COUNT(*) FROM dispatches WHERE decision_id = ?`, decision.DecisionID).Scan(&present)
			if err != nil {
				return err
			}
			if present == 0 {
				correlation, err = allocateGeneration(tx)
				if err != nil {
					return err
				}
				break
			}
		}
		requests := make([]record.Envelope, 0, len(decisions))
		for i := range decisions {
			decision := decisions[i]
			shut, err := readQuotaShutBackends(tx.Reader(), at, policy)
			if err != nil {
				return err
			}
			choose := func() error {
				ok, err := selectSeatDispatch(tx, &decision, policy, shut, at)
				var wait *seatWait
				if errors.As(err, &wait) {
					decision.Action, decision.Reason, decision.To = ActionHold, ReasonBackendWindow, ""
					decision.DeferredBy, decision.LaneID, decision.RunRequest = wait.backend, "", nil
					identifyDecision(&decision)
					return err
				}
				if err == nil && !ok {
					return errSeatWindowClosed
				}
				return err
			}
			if policy != nil && len(policy.SeatShares) > 0 {
				err = keepSeatWait(tx, choose)
			} else {
				err = choose()
			}
			if err != nil {
				return err
			}
			decision.PassID = correlation
			if decision.RunRequest != nil {
				decision.RunRequest.CorrelationID = correlation
			}
			if err := recordDecision(tx, decision, at); err != nil {
				return err
			}
			if decision.Action != ActionDispatch && decision.Action != ActionRetry {
				continue
			}
			if err := recordWorkflowSelection(tx, decision); err != nil {
				return err
			}
			request, err := requestEnvelope(decision, at)
			if err != nil {
				return err
			}
			requests = append(requests, request)
		}
		return runner.HandOver(requests)
	})
	if err != nil {
		return classifyWrite(err)
	}
	return nil
}

// requestEnvelope wraps a dispatch decision's run.request in design §5.1's envelope, so the
// runner is handed a record `run exec` consumes as it stands. The producer is drain: drain
// emits the request, and run consumes it.
//
// The envelope is run.RequestRecord's, the same one the `request` face emits: the loop and a
// standalone driver produce one record shape, and only the producer names who made it.
func requestEnvelope(decision Decision, at time.Time) (record.Envelope, error) {
	if decision.RunRequest == nil {
		return record.Envelope{}, Internalf("the dispatch decision for %q carries no run_request", decision.ItemID)
	}
	return run.RequestRecord(*decision.RunRequest, record.Producer{System: record.System, Utility: Utility, Version: Version}, at)
}

// classifyWrite keeps a usage refusal and an incompatible schema as they were made, and
// answers anything else as a bug: a write transaction that failed for a reason the face did
// not classify is not an outcome, and the caller must not read it as one.
func classifyWrite(err error) error {
	var exit *ExitError
	if errors.As(err, &exit) {
		return err
	}
	return Internalf("%v", err)
}
