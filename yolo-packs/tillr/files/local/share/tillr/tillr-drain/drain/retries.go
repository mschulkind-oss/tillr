package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// RetryKind is the record kind one operator authorization is written and read as. It is a
// record and not an event: the `retries` row is the store's copy, and registering the kind
// as appendable would claim a state transition this face does not assert.
//
// The authorization is a claim — a person says "this run failed in the harness, re-dispatch
// it and name the run it supersedes" — and `plan` is what decides whether the claim holds:
// the run must still be the item's latest terminal run, and the gate must never have judged
// it. That is the distinction A-NORETRY left to "backfill semantics"; the claim supplies the
// intent, and the store's own `gate.verdict` event supplies the evidence.
const RetryKind = "drain.retry"

// RetryAckKind is the event a retry appends in the same transaction as its `retries` row
// when the operator acknowledges the outcome the retry exists to recover from. It is an
// event rather than a column: the acknowledgment names evidence the store already holds —
// a `gate.verdict` for a judged run, an attempt class for a wall cut short — and a column
// on the retries row would be a second copy of that evidence. Its object is the retry, so
// a reader follows one id from the row to the event.
const RetryAckKind = "drain.retry.ack"

// DesignRetryKind is the event a retry appends when it names an item and the design run
// that ended without answering the item's conflict, now with the owner: the operator's
// authorization to raise that conflict for one more design run. It is an event and not a
// `retries` row, because a row is a build retry the plan reads as one; this authorization
// moves the conflict instead, and the conflict transition it writes beside the event is
// what the plan reads. Its object is the design run, and its payload is the Retry.
const DesignRetryKind = "drain.retry.design"

// The two outcomes a retry may acknowledge. They are the two the face refuses without an
// acknowledgment: a run the gate judged, and a run a wall cut short. The empty string is
// the unchanged harness-only retry.
const (
	// RetryAckGateFailed acknowledges a judged run: the gate answered it, and the
	// operator is re-dispatching it anyway.
	RetryAckGateFailed = "gate-failed"
	// RetryAckUnjudged acknowledges a run a wall cut short: no judge saw it, and the
	// operator has inspected the tree and is re-dispatching it anyway.
	RetryAckUnjudged = "unjudged"
)

// retryAckPayload is the `drain.retry.ack` payload: the outcome the operator acknowledged.
type retryAckPayload struct {
	Acknowledge string `json:"acknowledge"`
}

// RetryDDL is drain's third table: the authorizations that let `plan` re-dispatch a run the
// harness refused.
//
// A row is the operator's claim and nothing more. It names the item, the run it supersedes,
// who asked and why, so a second attempt is attributable and a reader can tell an
// authorization from a first dispatch. It records no consumed flag: consumption is a fact
// about the runs, not a column — an authorization applies only while the run it names is
// still the item's latest terminal run, so the retry run's own row is what retires it.
// That keeps `plan` a pure function of the store: a plan never writes the authorization
// away, and two plans over one store answer the same decisions.
const RetryDDL = `
CREATE TABLE IF NOT EXISTS retries (
  retry_id          TEXT PRIMARY KEY,
  item_id           TEXT NOT NULL,
  supersedes_run_id TEXT NOT NULL,
  requested_by      TEXT NOT NULL,
  reason            TEXT NOT NULL,
  requested_at      INTEGER NOT NULL
);
`

// Retry is one recorded authorization: the `drain.retry` payload and the `retries` row,
// field for field.
type Retry struct {
	RetryID         string    `json:"retry_id"`
	ItemID          string    `json:"item_id"`
	SupersedesRunID string    `json:"supersedes_run_id"`
	RequestedBy     string    `json:"requested_by"`
	Reason          string    `json:"reason"`
	RequestedAt     time.Time `json:"requested_at"`
	// ReraisesConflict is the conflict a design retry raised again, and "" for a build
	// retry. A design retry writes no `retries` row (DesignRetryKind).
	ReraisesConflict string `json:"reraises_conflict,omitempty"`
}

// RetryRequest is one operator's authorization to re-dispatch a run, before it is written.
// At is the injected clock, so two authorizations made from the same words and the same
// clock produce the same row.
type RetryRequest struct {
	ItemID string
	RunID  string
	By     string
	Why    string
	At     time.Time
	// Acknowledge is the outcome the operator is acknowledging: "" for the unchanged
	// harness-only retry, RetryAckGateFailed for a run the gate judged, or
	// RetryAckUnjudged for a run a wall cut short. It is validated against the store's
	// own record before the row is written, so an acknowledgment that does not match the
	// run is refused rather than recorded as a claim the plan will not honour.
	Acknowledge string
}

// retryRow is one stored authorization as the planner reads it: the two ids the decision
// needs, and the clock that orders two authorizations of one item. The split from Retry is
// so a column's meaning does not depend on a JSON tag, and so the planner cannot accidentally
// read a field the decision does not use.
type retryRow struct {
	RetryID         string
	SupersedesRunID string
	RequestedBy     string
	Reason          string
	RequestedAt     time.Time
	// Acknowledge is the outcome the authorization's `drain.retry.ack` event names, or
	// "" when the authorization made no acknowledgment.
	Acknowledge string
}

// AuthorizeRetry records one operator's authorization to re-dispatch an item's run, and
// puts the item back in the queue when the failed attempt left it out of it.
//
// The structural checks are this face's: the item exists and is not terminal, the run exists
// and belongs to that item, and the run is terminal. The judgment checks are the same rule
// `plan` applies, refused here too so an operator learns immediately instead of creating an
// authorization the plan cannot honour: the gate must never have judged the run, and a run a
// wall cut short is inspected rather than retried.
//
// The one check this face deliberately leaves to `plan` is "the run is still the latest":
// a newer run can appear between the authorization and the next tick, and `plan` then
// reads the authorization as no longer speaking: the item routes by its latest run, as
// it would with no authorization.
func AuthorizeRetry(ctx context.Context, tx *store.Tx, req RetryRequest) (Retry, error) {
	if req.ItemID == "" {
		return Retry{}, Usagef("a retry authorization needs an item: --item <id>")
	}
	if req.RunID == "" {
		return Retry{}, Usagef("a retry authorization needs the run it supersedes: --run <id>")
	}
	if req.By == "" {
		return Retry{}, Usagef("a retry authorization needs who asked for it: --by <who>")
	}
	if req.Why == "" {
		return Retry{}, Usagef("a retry authorization needs a reason: --why <reason>")
	}
	if req.At.IsZero() {
		return Retry{}, Usagef("a retry authorization needs the injected clock: --now <RFC 3339>")
	}
	at := req.At.UTC()

	var state string
	if err := tx.QueryRow(`SELECT state FROM items WHERE item_id = ?`, req.ItemID).Scan(&state); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Retry{}, Usagef("the store holds no item %q. %s", req.ItemID, noRetryCommand("the owner moves: tillr item apply records the item from its document block"))
		}
		return Retry{}, readFailure(err)
	}
	declared, err := definition.ForReader(tx.Reader())
	if err != nil {
		return Retry{}, readFailure(err)
	}
	if finishedState(declared.Parsed, state) {
		return Retry{}, Usagef("item %q is %q: a retry does not re-open a terminal item. %s", req.ItemID, state, noRetryCommand("the owner moves: tillr item reopen puts the item back first"))
	}

	var (
		runItemID sql.NullString
		rec       run.Record
		exitCode  sql.NullInt64
		refs      string
	)
	if err := tx.QueryRow(
		`SELECT item_id, run_id, backend, state, exit_code, artifact_refs
		   FROM runs WHERE run_id = ?`, req.RunID,
	).Scan(&runItemID, &rec.RunID, &rec.Backend, &rec.State, &exitCode, &refs); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Retry{}, Usagef("the store holds no run %q. %s", req.RunID, latestRunCommand(tx.Reader(), req.ItemID))
		}
		return Retry{}, readFailure(err)
	}
	if !runItemID.Valid || runItemID.String != req.ItemID {
		if runItemID.Valid {
			return Retry{}, Usagef("run %q belongs to item %q, not %q. The accepted command names its item: %s", req.RunID, runItemID.String, req.ItemID, retryCommand(runItemID.String, req.RunID, ""))
		}
		return Retry{}, Usagef("run %q belongs to no item at all, not %q. %s", req.RunID, req.ItemID, latestRunCommand(tx.Reader(), req.ItemID))
	}
	if !terminalRunStates[rec.State] {
		return Retry{}, Usagef("run %q is %q: a retry supersedes a finished run. %s", req.RunID, rec.State, noRetryCommand("the loop moves: the run ends first, and a retry is accepted then"))
	}
	if design, err := authorizeDesignRetry(ctx, tx, req, at); err != nil || design != nil {
		if design == nil {
			return Retry{}, err
		}
		return *design, nil
	}
	// The plan honors a build retry only while the run it names is the item's latest
	// finished run (retryFor), so an authorization naming an earlier one is recorded and
	// then silently ignored. Refuse it here, naming the run the plan would accept.
	var latest string
	if err := tx.QueryRow(
		`SELECT run_id FROM runs WHERE item_id = ? AND state IN (?, ?, ?)
		  ORDER BY started_at DESC, event_seq DESC, rowid DESC LIMIT 1`,
		req.ItemID, run.StateExited, run.StateFailedToStart, run.StateTimedOut,
	).Scan(&latest); err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Retry{}, readFailure(err)
	}
	if latest != "" && latest != req.RunID {
		return Retry{}, Usagef("run %q is not item %q's latest finished run, and a build retry supersedes the latest: %s", req.RunID, req.ItemID, latestRunCommand(tx.Reader(), req.ItemID))
	}
	if exitCode.Valid {
		code := int(exitCode.Int64)
		rec.ExitCode = &code
	}
	var artifactRefs []run.ArtifactRef
	if err := json.Unmarshal([]byte(refs), &artifactRefs); err != nil {
		return Retry{}, Internalf("reading the artifact refs of run %q: %v", req.RunID, err)
	}

	judged, err := runJudged(tx.Reader(), req.RunID)
	if err != nil {
		return Retry{}, err
	}
	class := ClassifyAttempt(rec, logTail(artifactRefs))
	// The acknowledgment is validated against the store's own record before the row is
	// written: an operator who acknowledges the wrong outcome learns here, where the face
	// can name the run, rather than leaving a retry the plan will refuse on every tick.
	switch req.Acknowledge {
	case "":
	case RetryAckGateFailed:
		if !judged {
			return Retry{}, Usagef("--acknowledge %s names a run the gate did not judge: %q. The accepted command: %s", RetryAckGateFailed, req.RunID, retryCommand(req.ItemID, req.RunID, acceptedAck(judged, class)))
		}
	case RetryAckUnjudged:
		if judged || class != AttemptUnjudged {
			return Retry{}, Usagef("--acknowledge %s names a run a wall did not cut short: %q. The accepted command: %s", RetryAckUnjudged, req.RunID, retryCommand(req.ItemID, req.RunID, acceptedAck(judged, class)))
		}
	default:
		return Retry{}, Usagef("--acknowledge %q is not an outcome: want %q or %q. The accepted command: %s", req.Acknowledge, RetryAckGateFailed, RetryAckUnjudged, retryCommand(req.ItemID, req.RunID, acceptedAck(judged, class)))
	}
	if judged && req.Acknowledge != RetryAckGateFailed {
		return Retry{}, Usagef("run %q was judged by the gate: a judged candidate is not retryable by link. An operator who has read the verdict acknowledges it: %s", req.RunID, retryCommand(req.ItemID, req.RunID, RetryAckGateFailed))
	}
	// Guarded by `!judged`: a walled run can also have committed and reached the gate, and
	// once a `gate.verdict` exists the gate's word is the one a retry acknowledges (design
	// D-RW2). Without the guard both branches demand an acknowledgment and neither accepts
	// the other's.
	if !judged && class == AttemptUnjudged && req.Acknowledge != RetryAckUnjudged {
		return Retry{}, Usagef("run %q was cut short by a wall: an unjudged run is inspected, not retried. An operator who has inspected the tree acknowledges it: %s", req.RunID, retryCommand(req.ItemID, req.RunID, RetryAckUnjudged))
	}

	retry := Retry{
		RetryID:         retryID(req.ItemID, req.RunID),
		ItemID:          req.ItemID,
		SupersedesRunID: req.RunID,
		RequestedBy:     req.By,
		Reason:          req.Why,
		RequestedAt:     at,
	}
	var seen int
	if err := tx.QueryRow(`SELECT count(*) FROM retries WHERE retry_id = ?`, retry.RetryID).Scan(&seen); err != nil {
		return Retry{}, readFailure(err)
	}
	if seen > 0 {
		return Retry{}, Usagef("run %q is already authorized for retry: the authorization %q survives in the store. %s", req.RunID, retry.RetryID, noRetryCommand("the loop moves: its next pass dispatches the retry, or holds it naming why (tillr drain why)"))
	}
	if _, err := tx.Exec(
		`INSERT INTO retries (retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		retry.RetryID, retry.ItemID, retry.SupersedesRunID, retry.RequestedBy, retry.Reason,
		retry.RequestedAt.UnixMilli(),
	); err != nil {
		return Retry{}, Internalf("recording the retry for %q: %v", req.ItemID, err)
	}
	if req.Acknowledge != "" {
		body, err := json.Marshal(retryAckPayload{Acknowledge: req.Acknowledge})
		if err != nil {
			return Retry{}, Internalf("encoding the retry acknowledgment for %q: %v", req.ItemID, err)
		}
		id := retry.RetryID
		if _, err := tx.Append(event.Event{
			Kind: RetryAckKind, ObjectType: "retry", ObjectID: &id, Payload: body, At: at,
		}); err != nil {
			return Retry{}, Internalf("recording the retry acknowledgment for %q: %v", req.ItemID, err)
		}
	}

	// A run that consumed its item — the fence refusal, the candidate-no-op — leaves the
	// item in the queue now, and a retry changes nothing there. A legacy `in-progress` row
	// or a blocked item whose finished run this call supersedes returns to the ready state
	// in the same transaction, so `tillr drain retry` is the single command that answers
	// the run and releases the hold.
	var openCallID string
	if err := tx.Reader().QueryRow(`SELECT COALESCE(object_id, '') FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=? ORDER BY seq DESC LIMIT 1`, callOffered, req.RunID).Scan(&openCallID); err == nil && openCallID != "" {
		if err := projectLandingCall(tx, openCallID, "", "superseded", at); err != nil {
			return Retry{}, Internalf("closing open call %q on %q: %v", openCallID, req.RunID, err)
		}
	}
	releaseBlocked := false
	if state == declared.Parsed.Blocked {
		conflicts, err := item.ReadConflicts(tx.Reader())
		if err != nil {
			return Retry{}, readFailure(err)
		}
		if c, ok := conflicts[req.ItemID]; ok {
			if c.Status != "awaiting" && c.Status != "raised" && c.DesignRunID == "" && c.TriageVersion == "" {
				releaseBlocked = true
				var callID string
				if err := tx.Reader().QueryRow(`SELECT COALESCE(object_id, '') FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=? ORDER BY seq DESC LIMIT 1`, callOffered, c.RunID).Scan(&callID); err == nil && callID != "" {
					if err := projectLandingCall(tx, callID, "", "superseded", at); err != nil {
						return Retry{}, Internalf("closing open call %q on %q: %v", callID, c.RunID, err)
					}
				}
			}
		} else {
			releaseBlocked = true
		}
	}
	if state == StateInProgress || releaseBlocked {
		if _, err := item.SetState(ctx, tx, req.ItemID, declared.Parsed.Ready, at); err != nil {
			return Retry{}, Internalf("moving item %q back to the queue: %v", req.ItemID, err)
		}
	}
	return retry, nil
}

// RetryEnvelope wraps one authorization in design §5.1's envelope, so the face echoes a
// record a reader can store beside the store's own row. The subject is the item, as a
// decision's is. A design retry is echoed as DesignRetryKind, the event it wrote.
func RetryEnvelope(retry Retry) (record.Envelope, error) {
	body, err := json.Marshal(retry)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the retry for %q: %v", retry.ItemID, err)
	}
	kind := RetryKind
	if retry.ReraisesConflict != "" {
		kind = DesignRetryKind
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          kind,
		ID:            retry.RetryID,
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     retry.RequestedAt,
		Subject:       &record.Subject{Kind: "item", ID: retry.ItemID},
		Payload:       body,
	}, nil
}

// readRetries answers every item's latest authorization, keyed by item id.
//
// A store written before the `retries` table existed has no authorizations, which is the
// answer this read gives it: the table's absence is not a store this binary cannot read,
// because the table is drain's own and its first row is what creates the question. `plan`
// opens such a store read-only and must not fail on a table a writer has not migrated yet.
func readRetries(r *store.Reader) (map[string]retryRow, error) {
	// The acknowledgments are read before the rows that name them: both reads run on one
	// reader, and the events read must not share an open cursor with the retries query.
	acks, err := readRetryAcks(r)
	if err != nil {
		return nil, err
	}
	// At equal timestamps the last inserted authorization is the latest. SQLite's
	// insertion row number (rowid) preserves that order; the id's attempt spelling
	// does not (an authorization for a10 sorts before one for a9).
	rows, err := r.Query(
		`SELECT retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at
		   FROM retries ORDER BY requested_at, rowid`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table: retries") {
			return map[string]retryRow{}, nil
		}
		return nil, readFailure(err)
	}
	defer rows.Close()
	retries := map[string]retryRow{}
	for rows.Next() {
		var (
			itemID      string
			requestedAt int64
			row         retryRow
		)
		if err := rows.Scan(&row.RetryID, &itemID, &row.SupersedesRunID, &row.RequestedBy, &row.Reason, &requestedAt); err != nil {
			return nil, readFailure(err)
		}
		row.RequestedAt = time.UnixMilli(requestedAt).UTC()
		row.Acknowledge = acks[row.RetryID]
		// The rows are ordered oldest first, so the last authorization for an item is the
		// one that speaks: a stale claim is superseded by a newer one rather than joined
		// to it, which is how an operator corrects an authorization naming an old run.
		retries[itemID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, readFailure(err)
	}
	return retries, nil
}

// readRetryAcks answers every retry acknowledgment, keyed by the retry id it names.
//
// A store written before the kind existed has no acknowledgments, which is the answer this
// read gives it: the events table is the substrate's, and an old store's silence is not an
// error. An event that names no retry, or whose payload names no outcome, is not evidence
// of an acknowledgment and is skipped rather than guessed at.
func readRetryAcks(r *store.Reader) (map[string]string, error) {
	events, err := r.EventsOfKind(0, RetryAckKind)
	if err != nil {
		return nil, readFailure(err)
	}
	acks := map[string]string{}
	for _, e := range events {
		if e.Kind != RetryAckKind || e.ObjectType != "retry" || e.ObjectID == nil {
			continue
		}
		var payload retryAckPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil || payload.Acknowledge == "" {
			continue
		}
		acks[*e.ObjectID] = payload.Acknowledge
	}
	return acks, nil
}

// runJudged answers whether the gate judged one run. The `gate.verdict` event is the
// record, not the run's exit status: a candidate refused at the fence exits 0 and was
// judged by nobody, and a candidate the gate judged carries the event whether it passed or
// failed.
func runJudged(r *store.Reader, runID string) (bool, error) {
	var seen int
	if err := r.QueryRow(
		`SELECT count(*) FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ?`,
		GateVerdictKind, runID,
	).Scan(&seen); err != nil {
		return false, readFailure(err)
	}
	return seen > 0, nil
}

// terminalRunStates are run's terminal states, as the retry face reads them: a retry
// supersedes a finished run, never one still in flight.
var terminalRunStates = map[string]bool{
	run.StateExited:        true,
	run.StateFailedToStart: true,
	run.StateTimedOut:      true,
}

// retryID is one authorization's identity: the item and the run it supersedes, so a second
// authorization of the same run is the same row rather than a second claim.
func retryID(itemID, runID string) string { return "retry-" + itemID + "-" + runID }
