package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// A run whose session pi ended because the model kept repeating one identical tool call
// (run end cause `repeating-call`) is neither an answer nor a machinery failure, and the
// conversation that looped is not one to continue. The owner asked on 2026-11-19 that such
// a loop lead to a different action, never to a resume of the same conversation and never
// to a hold on him:
//
//   - The first such ending starts the same item or node again in a fresh session on the
//     same backend, with a note in its brief that names the call (repeatedCallBrief),
//     unless the run already recovered from a loop in-run (LoopRecovery count > 0), in
//     which case it skips the fresh session on the same backend and goes straight to the
//     seat's next backend.
//   - When that fresh run, dispatched for that reason, ends the same way (or when a run
//     with in-run recovery ends that way), the existing failover walks the seat's backend
//     list to the next backend.
//   - When the next backend is one this unbroken chain already ended on (the walk wraps)
//     or the list has none, the item is held with ReasonRepeatingCall, whose mover is the
//     operator.
//
// Nothing is counted. "The same way again" is read from the dispatch decision itself:
// a dispatch made for this reason carries the RepeatedCall it answers, and its Backends
// are the backends whose sessions in that chain repeated. Any other ending breaks the chain,
// because the next dispatch carries no RepeatedCall.

// repeatingCallKind records the ending and the route chosen for it, on the run.
const repeatingCallKind = "drain.repeating-call"

// AttemptRepeating is a run whose session pi ended for a repeating call. It is not
// infrastructure (it never closes a backend) and not an answer.
const AttemptRepeating AttemptClass = "repeating-call"

// ReasonRepeatingCall holds an item every backend on its seat's list ended for a
// repeating call. ReasonRepeatingCallFresh and ReasonRepeatingCallFailover are the
// dispatch reasons of the runs that answer such an ending.
const (
	ReasonRepeatingCall         = "repeating-call"
	ReasonRepeatingCallFresh    = "restarted in a fresh session: the earlier session repeated one identical call"
	ReasonRepeatingCallFailover = "failover: the earlier sessions repeated one identical call"
)

// The three routes a repeating-call ending takes.
const (
	repeatRouteFresh    = "fresh"
	repeatRouteFailover = "failover"
	repeatRouteHold     = "hold"
)

// RepeatedCall is one repeating-call ending, as its event records it and as the dispatch
// that answers it carries it into staging.
type RepeatedCall struct {
	RunID      string `json:"run_id"`
	ItemID     string `json:"item_id"`
	NodeID     string `json:"node_id,omitempty"`
	Backend    string `json:"backend"`
	Tool       string `json:"tool"`
	Args       string `json:"args"`
	ArgsDigest string `json:"args_digest"`
	Streak     int    `json:"streak"`
	Action     string `json:"action,omitempty"`
	Shape      string `json:"shape,omitempty"`
	// Backends are the backends whose sessions in this unbroken chain repeated, oldest
	// first, ending with Backend.
	Backends []string `json:"backends"`
	// Route is what follows: fresh, failover or hold. NextBackend names the backend the
	// fresh run or the failover runs on, and is empty for a hold.
	Route       string `json:"route"`
	NextBackend string `json:"next_backend,omitempty"`
}

func init() {
	if err := event.Register(repeatingCallKind, Utility); err != nil {
		panic(err)
	}
}

// repeatingRoute answers the route for a run that ended for a repeating call on backend,
// given the RepeatedCall its own dispatch answered (nil when it answered none) and whether
// the run already recovered from a loop in-run. It is pure: the same chain, recovery
// state, and policy always give the same route.
func repeatingRoute(policy *Policy, seat string, prior *RepeatedCall, backend string, recovered bool) (route, next string, chain []string) {
	if prior != nil {
		chain = append(chain, prior.Backends...)
	}
	if len(chain) == 0 || chain[len(chain)-1] != backend {
		chain = append(chain, backend)
	}
	if prior == nil && !recovered {
		return repeatRouteFresh, backend, chain
	}
	if policy == nil {
		return repeatRouteHold, "", chain
	}
	candidate, ok := policy.nextBackend(backend, seat)
	if !ok {
		return repeatRouteHold, "", chain
	}
	for _, tried := range chain {
		if tried == candidate {
			return repeatRouteHold, "", chain
		}
	}
	return repeatRouteFailover, candidate, chain
}

// newRepeatedCall is the record of one repeating-call ending and its route.
func newRepeatedCall(policy *Policy, d Decision, rec run.Record) RepeatedCall {
	rc := RepeatedCall{RunID: rec.RunID, ItemID: d.ItemID, Backend: rec.Backend}
	if rec.RepeatingCall != nil {
		rc.Tool, rc.Args, rc.ArgsDigest, rc.Streak = rec.RepeatingCall.Tool, rec.RepeatingCall.Args, rec.RepeatingCall.ArgsDigest, rec.RepeatingCall.Streak
		rc.Action, rc.Shape = rec.RepeatingCall.Action, rec.RepeatingCall.Shape
	}
	if d.Workflow != nil && d.NodeIndex < len(d.Workflow.Stages) {
		rc.NodeID = d.Workflow.Stages[d.NodeIndex].ID
	}
	recovered := (rec.LoopRecovery != nil && rec.LoopRecovery.Count > 0) ||
		(rec.RepeatingCall != nil && rec.RepeatingCall.Action == "ended")
	rc.Route, rc.NextBackend, rc.Backends = repeatingRoute(policy, d.Seat, d.RepeatedCall, rec.Backend, recovered)
	return rc
}

func (rc RepeatedCall) callLabel() string {
	if rc.Tool != "" {
		return rc.Tool
	}
	if rc.Shape != "" {
		return "(" + rc.Shape + ")"
	}
	return "(no tool)"
}

// describe is the call in one line, for the loop's report and a preservation reason.
func (rc RepeatedCall) describe() string {
	return fmt.Sprintf("pi ended the session: the model repeated one identical call %d times in a row (%s %s)", rc.Streak, rc.callLabel(), rc.Args)
}

// recordRepeatedCall appends the ending's event. It does no I/O beyond the store.
func (l *Loop) recordRepeatedCall(ctx context.Context, rc RepeatedCall) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		return appendEffect(tx, repeatingCallKind, rc.RunID, rc, l.cfg.Now().UTC())
	})
}

// settleRepeatingCall settles an item-level run (no workflow) that pi ended for a
// repeating call: it records the ending and its route and abandons the lane, preserving
// and pinning the run's work exactly as an interrupted run's is. The next pass's planner
// acts on the recorded route (planner.repeatedDecision).
func (l *Loop) settleRepeatingCall(ctx context.Context, d Decision, rec run.Record, lane string) laneOutcome {
	rc := newRepeatedCall(l.currentPolicy(), d, rec)
	if err := l.recordRepeatedCall(ctx, rc); err != nil {
		return laneOutcome{err: err}
	}
	l.reportf("loop: %s %s; route %s %s\n", rec.RunID, rc.describe(), rc.Route, rc.NextBackend)
	return laneOutcome{stop: l.abandon(ctx, lane, rec.RunID, l.headOf(ctx, lane), rc.describe())}
}

// restartRepeatingNode settles a workflow node pi ended for a repeating call: the same
// node is dispatched again, on its lane and base, in a fresh session on the same backend
// or the failover's, or the node is recorded `repeating-call` so the planner holds the
// item (commandNodeHold). Unlike retryWorkflowNode it charges nothing to the backend: a
// model that loops is not machinery that failed.
func (l *Loop) restartRepeatingNode(ctx context.Context, d Decision, base string, rec run.Record) error {
	policy := l.currentPolicy()
	rc := newRepeatedCall(policy, d, rec)
	var head string
	if !l.nonLanding {
		head = l.headOf(ctx, d.LaneID)
		if err := l.preserveWork(ctx, d.LaneID, rec.RunID, rc.describe()); err != nil {
			if stop := l.retainUnpreserved(ctx, d.LaneID, rec.RunID, head, err); stop != "" {
				return retainedAfter(d.LaneID, stop)
			}
		}
		if err := l.pinLane(ctx, d.LaneID, rec.RunID, head); err != nil {
			return err
		}
	}
	l.reportf("loop: %s %s; route %s %s\n", rec.RunID, rc.describe(), rc.Route, rc.NextBackend)
	if rc.Route == repeatRouteHold {
		s, err := store.Open(ctx, l.cfg.Store, true)
		if err != nil {
			return err
		}
		defer s.Close()
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			if err := appendEffect(tx, repeatingCallKind, rc.RunID, rc, l.cfg.Now().UTC()); err != nil {
				return err
			}
			_, err := tx.Exec(`UPDATE node_runs SET outcome=? WHERE run_id=?`, ReasonRepeatingCall, rec.RunID)
			return err
		}); err != nil {
			return err
		}
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, rc.describe()); stop != "" {
			return retainedAfter(d.LaneID, stop)
		}
		return l.markExamined(ctx, d)
	}
	if policy == nil {
		return Usagef("workflow seat %s has no backend route", d.Seat)
	}
	suffix := "fresh"
	reason := ReasonRepeatingCallFresh
	if rc.Route == repeatRouteFailover {
		suffix, reason = "failover", ReasonRepeatingCallFailover
	}
	request := policy.request(d.ItemID, d.LaneID, markRunID(d.RunRequest.RunID, suffix), rc.NextBackend)
	request.NodeID = d.Workflow.Stages[d.NodeIndex].ID
	request.WorkingDir = d.RunRequest.WorkingDir
	if d.Workflow.Stages[d.NodeIndex].TimeoutMS > 0 {
		request.TimeoutMS = d.Workflow.Stages[d.NodeIndex].TimeoutMS
	}
	stampDecisionRequest(d, request)
	d.RunRequest = request
	d.WorkflowRestart = true
	d.Returned, d.WorkerSessionReason = nil, ""
	d.Action, d.Reason = ActionDispatch, reason
	answered := rc
	d.RepeatedCall = &answered
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := appendEffect(tx, repeatingCallKind, rc.RunID, rc, l.cfg.Now().UTC()); err != nil {
			return err
		}
		changed, err := tx.Exec(`UPDATE node_runs SET outcome='running' WHERE item_id=? AND attempt=? AND node_id=?`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID)
		if err != nil {
			return err
		}
		if n, err := changed.RowsAffected(); err != nil {
			return err
		} else if n != 1 {
			return fmt.Errorf("lost workflow node during a repeating-call restart")
		}
		return appendEffect(tx, nodeAdvancedKind, request.RunID, pendingWork{Decision: d, BaseSHA: base, Ready: true, ParentRunID: rec.RunID}, l.cfg.Now().UTC())
	})
}

// readRepeatedCalls answers every recorded repeating-call ending, keyed by run id.
func readRepeatedCalls(r *store.Reader) (map[string]RepeatedCall, error) {
	out := map[string]RepeatedCall{}
	rows, err := r.Query(`SELECT object_id, payload FROM events WHERE kind=? ORDER BY seq`, repeatingCallKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id sql.NullString
		var body []byte
		if err := rows.Scan(&id, &body); err != nil {
			return nil, err
		}
		var rc RepeatedCall
		if err := json.Unmarshal(body, &rc); err != nil {
			return nil, Internalf("reading the repeating-call ending of %q: %v", id.String, err)
		}
		out[id.String] = rc
	}
	return out, rows.Err()
}

// repeatedCallBrief is the brief's section for a run dispatched because an earlier
// session repeated one identical call: what was repeated, that this is a new session, and
// where the earlier run's work is kept.
func repeatedCallBrief(rc RepeatedCall) string {
	var b strings.Builder
	b.WriteString("\n## Your previous session\n\n")
	fmt.Fprintf(&b, "This work's previous run, %s, was ended by pi because its session repeated one identical tool call %d times in a row, each time with the same result:\n\n```\n%s %s\n```\n\n", rc.RunID, rc.Streak, rc.callLabel(), rc.Args)
	fmt.Fprintf(&b, "This run is a new session on %s; nothing from that conversation carries over. This is not a count against the item. That call's result has not changed, so do not make it again expecting a different one.\n", rc.NextBackend)
	fmt.Fprintf(&b, "\nThe previous run's committed head, when it committed, is pinned at `refs/tillr/runs/%s`, and uncommitted work it left is kept at `%s%s`. Read them with `git show` or `git diff`, and reuse what is sound; do not move either ref.\n", rc.RunID, preservedPrefix, rc.RunID)
	return b.String()
}
