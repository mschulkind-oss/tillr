package main

import (
	"encoding/json"

	"github.com/mschulkind-oss/tillr/store"
)

// The store's spellings this file reads, spelled here for the same reason allocateKind is:
// this module reads the store, it does not write it. A value these disagree with is a
// disagreement the agreement case (TestEveryNonLandingOutcomeRecoversAndAgreesWithView)
// catches.
const (
	// retryAckKind is the event an operator's retry authorization carries when it
	// acknowledges the outcome the re-dispatch exists to recover from.
	retryAckKind = "drain.retry.ack"
	// retryAckGateFailed and retryAckUnjudged are the two outcomes an authorization may
	// acknowledge. An authorization that names one of them is what lets the planner
	// re-dispatch a run a judge or a wall already answered.
	retryAckGateFailed = "gate-failed"
	retryAckUnjudged   = "unjudged"
	// stateInProgress is the frozen legacy item state that is evidence the item has been
	// dispatched and run before. No transition writes it now.
	stateInProgress = "in-progress"
	// runStateTimedOut is the run state a wall leaves behind.
	runStateTimedOut = "timed-out"
	// adapterWallExit is the exit `timeout` gives a harness it killed at the wall, which is
	// the second shape of an unjudged run.
	adapterWallExit = 124
)

// dispatchHistory is the planner's read of the store's attempt history, kept here because
// the header's Dispatchable is the planner's own answer rather than a second arithmetic
// (design §5.6, D-JL4).
//
// It carries the half of drain's `decide` a read face can answer without a policy, a
// questions index or a tick: whether an item has run before, whether its history is in the
// log at all, and whether an operator has authorized a retry whose claim the store's own
// record supports. `dispatchable` documents the rules it deliberately leaves to the loop.
type dispatchHistory struct {
	// ran is every item with a run row in the runs table: the direct evidence that
	// dispatching it again is a retry rather than a first attempt.
	ran map[string]bool
	// known is every item the log carries a state event for, and inProgress the subset
	// that has ever been in progress. An item in neither is unknown rather than
	// never-run, which is the conservative direction the planner takes: a dispatch on an
	// unknown history could be a second attempt nobody asked for.
	known      map[string]bool
	inProgress map[string]bool
	// retries is each item's latest operator authorization, keyed by item. The rows are
	// read oldest first, so the last one for an item is the one that speaks -- which is how
	// an operator corrects an authorization that named an old run.
	retries map[string]retryAuthorization
	// judged is every run the gate answered a `gate.verdict` for. It is the record, not the
	// run's exit status: a candidate the fence refused exits 0 and was judged by nobody.
	judged map[string]bool
	// unjudged is every run a wall cut short, from the run's own record.
	unjudged map[string]bool
	// latest is each item's latest run id, which is the run an authorization must name for
	// the claim to still hold.
	latest map[string]string
	// refusals is each run's drain refusal step, from its `drain.refusal` event.
	refusals map[string]string
	// gateFailed is every run the gate answered `fail` for.
	gateFailed map[string]bool
}

// returnedSteps are the refusal steps whose ending the planner sends back to the item's
// worker with its evidence, uncounted (the owner's ruling of 2026-11-17): a gate refusal, a
// run with no commit, a fence escape and a refused landing.
var returnedSteps = map[string]bool{"gate": true, "no-op": true, "fence": true, "land": true}

// retryAuthorization is one operator's claim that a run may be re-dispatched, with the
// outcome they acknowledged when they made it.
type retryAuthorization struct {
	supersedes  string
	acknowledge string
}

// readDispatchHistory answers the planner's attempt history from the store's rows. runs is
// the store's `runs` table as readRuns answered it -- the table, never the run a lease
// projects, because a retry is a second attempt at finished work.
func readDispatchHistory(r *store.Reader, runs []storeRun) (*dispatchHistory, error) {
	h := &dispatchHistory{
		ran:        map[string]bool{},
		known:      map[string]bool{},
		inProgress: map[string]bool{},
		retries:    map[string]retryAuthorization{},
		judged:     map[string]bool{},
		unjudged:   map[string]bool{},
		latest:     map[string]string{},
		refusals:   map[string]string{},
		gateFailed: map[string]bool{},
	}
	// readRuns orders by started_at, so the last row for an item is its newest run -- the
	// same answer readPriorRuns gives the planner.
	for _, run := range runs {
		if run.ItemID == "" {
			continue
		}
		h.ran[run.ItemID] = true
		h.latest[run.ItemID] = run.RunID
		if runUnjudged(run) {
			h.unjudged[run.RunID] = true
		}
	}
	if err := readItemHistory(r, h); err != nil {
		return nil, err
	}
	verdicts, err := readGateVerdicts(r)
	if err != nil {
		return nil, err
	}
	for runID, verdict := range verdicts {
		h.judged[runID] = true
		if verdict.Result == "fail" {
			h.gateFailed[runID] = true
		}
	}
	if err := readRetryAuthorizations(r, h); err != nil {
		return nil, err
	}
	if err := readRefusals(r, h); err != nil {
		return nil, err
	}
	return h, nil
}

// runUnjudged answers whether a wall cut the run short. It is the two shapes ClassifyAttempt
// calls AttemptUnjudged, read from the run's record rather than its log: a run whose state
// says it timed out, and a run whose adapter's own timeout exited 124.
func runUnjudged(run storeRun) bool {
	if run.State == runStateTimedOut {
		return true
	}
	return run.ExitCode != nil && *run.ExitCode == adapterWallExit
}

// readItemHistory answers which items the log carries a state for, and which have been in
// progress. The filter is the payload's own `state` rather than a kind name: an item state
// write is an event about an item that says what state the item is in, whatever kind item
// chose to append.
func readItemHistory(r *store.Reader, h *dispatchHistory) error {
	rows, err := r.Query(`SELECT COALESCE(object_id, ''), payload FROM events WHERE object_type = 'item' ORDER BY seq`)
	if err != nil {
		if isMissingTable(err) {
			return nil
		}
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var itemID, payload string
		if err := rows.Scan(&itemID, &payload); err != nil {
			return err
		}
		if itemID == "" {
			continue
		}
		var body struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal([]byte(payload), &body); err != nil || body.State == "" {
			continue
		}
		h.known[itemID] = true
		if body.State == stateInProgress {
			h.inProgress[itemID] = true
		}
	}
	return rows.Err()
}

// readRefusals answers each run's refusal step. The newest event for a run speaks.
func readRefusals(r *store.Reader, h *dispatchHistory) error {
	rows, err := r.Query(`SELECT COALESCE(object_id, ''), COALESCE(json_extract(payload, '$.step'), '') FROM events WHERE kind = 'drain.refusal' AND object_type = 'run' ORDER BY seq`)
	if err != nil {
		if isMissingTable(err) {
			return nil
		}
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var runID, step string
		if err := rows.Scan(&runID, &step); err != nil {
			return err
		}
		if runID != "" && step != "" {
			h.refusals[runID] = step
		}
	}
	return rows.Err()
}

// readRetryAuthorizations answers each item's latest retry authorization and the outcome it
// acknowledged. A store written before `retries` existed answers none, which is the answer
// an old store's silence gives; that is an absence, not a failure.
func readRetryAuthorizations(r *store.Reader, h *dispatchHistory) error {
	acks, err := readRetryAcks(r)
	if err != nil {
		return err
	}
	rows, err := r.Query(
		`SELECT retry_id, item_id, supersedes_run_id, requested_at
		   FROM retries ORDER BY requested_at, retry_id`)
	if err != nil {
		if isMissingTable(err) {
			return nil
		}
		return err
	}
	defer rows.Close()
	for rows.Next() {
		var (
			retryID    string
			itemID     string
			supersedes string
			requested  int64
		)
		if err := rows.Scan(&retryID, &itemID, &supersedes, &requested); err != nil {
			return err
		}
		h.retries[itemID] = retryAuthorization{supersedes: supersedes, acknowledge: acks[retryID]}
	}
	return rows.Err()
}

// readRetryAcks answers every retry acknowledgment, keyed by the retry id it names. An event
// that names no retry, or whose payload names no outcome, is not evidence of an
// acknowledgment and is skipped rather than guessed at.
func readRetryAcks(r *store.Reader) (map[string]string, error) {
	rows, err := r.Query(
		`SELECT COALESCE(object_id, ''), payload FROM events WHERE kind = ? AND object_type = 'retry' ORDER BY seq`,
		retryAckKind)
	if err != nil {
		if isMissingTable(err) {
			return map[string]string{}, nil
		}
		return nil, err
	}
	defer rows.Close()
	acks := map[string]string{}
	for rows.Next() {
		var retryID, payload string
		if err := rows.Scan(&retryID, &payload); err != nil {
			return nil, err
		}
		var body struct {
			Acknowledge string `json:"acknowledge"`
		}
		if err := json.Unmarshal([]byte(payload), &body); err != nil || body.Acknowledge == "" || retryID == "" {
			continue
		}
		acks[retryID] = body.Acknowledge
	}
	return acks, rows.Err()
}

// dispatchable answers whether the drain planner would dispatch this item on its next tick,
// given a free lane. It is the planner's own predicate, in the planner's order, for the
// rules a read face can answer from the store:
//
//   - the item is queued, unblocked, and held by neither a question nor an owner-only criterion;
//   - an operator's retry authorization that the store does not refuse makes the item a
//     retry the planner would dispatch; and otherwise
//   - the item's latest run ended in a refusal the planner returns to the item's worker
//     (returnedSteps) makes it dispatchable; and otherwise
//   - the item may not be a retry -- it has no run, its history is in the log, and it has
//     never been in progress.
//
// Three of the planner's holds are not here, and each is a rule whose input the store does
// not carry:
//
//   - a failover (`failover`, and `run-infra-exhausted` when the walk has no hop left),
//     which is the policy's backend preference rather than the store's. The view holds an
//     item whose only failure is the machinery, so a policy that walks to another backend
//     dispatches one the header did not count; the direction is deliberate, because the
//     repository's own policy names one backend in its preference and therefore never walks.
//
//   - the design document's open questions (`question-open`), which are the index's answer
//     rather than the store's -- the recorded decision is read instead, through hold; and
//
//   - the fence rules (`no-fence`, `fence-overlap`), which are the staging layer's: a fence
//     that admits nothing is parsed from the item's allowed_paths by the planner's own
//     boundary code, and two overlapping fences are a property of a tick's item order
//     rather than of one item. The header's figure is a filter over items, not a simulated
//     tick, so two items with overlapping fences are both counted.
//
// A failover and a fence contention undercount in the header, and a question hold overcounts
// until a tick records the hold: the item's own header says what it can actually dispatch,
// and the ruling is the index's. The agreement case pins the rules that are here, so a drift
// shows up as a failing test rather than as a number.
func (h *dispatchHistory) dispatchable(item storeItem, stateOf map[string]string, waiting map[string][]string, hold string) bool {
	if item.State != "in-queue" || hold != "" || item.OwnerOwed != nil {
		return false
	}
	if unmetBlocker(stateOf, waiting, item.ItemID) {
		return false
	}
	// An authorization that names a run that is no longer the item's latest no longer
	// speaks, and the item is routed as if it had none.
	if authorization, ok := h.retries[item.ItemID]; ok && h.latest[item.ItemID] == authorization.supersedes {
		return !h.retryRefused(item.ItemID, authorization)
	}
	if latest := h.latest[item.ItemID]; latest != "" && (returnedSteps[h.refusals[latest]] || h.refusals[latest] == "" && h.gateFailed[latest]) {
		return true
	}
	return !h.mayBeARetry(item.ItemID)
}

// mayBeARetry is the planner's conservative half of the retry rule: an item that could be a
// second attempt holds unless a person authorized it, so a run nobody asked to repeat never
// spends a second attempt by accident. An item whose history is not in the log at all is
// unknown rather than never-run, which is the same conservative direction.
func (h *dispatchHistory) mayBeARetry(itemID string) bool {
	return h.ran[itemID] || !h.known[itemID] || h.inProgress[itemID]
}

// retryRefused answers why an authorization does not hold, or false when it does. The checks
// are the planner's three: the claim names the item's latest run, a judged run needs its own
// acknowledgment, and a wall-cut run needs its own.
func (h *dispatchHistory) retryRefused(itemID string, authorization retryAuthorization) bool {
	if h.latest[itemID] != authorization.supersedes {
		return true
	}
	if h.judged[authorization.supersedes] && authorization.acknowledge != retryAckGateFailed {
		return true
	}
	// Guarded by `!judged`: a walled run can also have committed and reached the gate, and
	// the gate's verdict then governs which acknowledgment a retry may carry (design
	// D-RW2). Without the guard a judged-and-walled run has no acceptable acknowledgment.
	if !h.judged[authorization.supersedes] && h.unjudged[authorization.supersedes] && authorization.acknowledge != retryAckUnjudged {
		return true
	}
	return false
}
