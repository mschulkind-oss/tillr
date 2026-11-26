package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// AuthorizeRetry records one operator's authorization to re-dispatch an item's run, and
// puts the item back in the queue when the failed attempt left it out of it.
//
// The structural checks are this face's: the item exists and is not terminal, the run exists
// and belongs to that item, and the run is terminal. The judgment checks are the same rule
// `plan` applies, refused here too so an operator learns immediately instead of creating an
// authorization the plan cannot honour: a judged run needs a gate-failed acknowledgment,
// and a run a wall cut short needs an unjudged acknowledgment after inspection.
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
			faults, err := ReadItemFaults(tx.Reader())
			if err != nil {
				return Retry{}, readFailure(err)
			}
			f := faults[req.ItemID]
			if f.RunID == req.RunID && f.Reason == ReasonStageFailed {
				if req.Acknowledge != "" {
					return Retry{}, Usagef("--acknowledge %q is not accepted for an unstarted stage-failed run %q. The accepted command: %s", req.Acknowledge, req.RunID, retryCommand(req.ItemID, req.RunID, ""))
				}
				return authorizeStageRetry(tx, req, f, at)
			}
			if f.RunID == req.RunID && execSetupRefused(f) {
				return Retry{}, Usagef("the store holds no run %q: the run face refused before the worker started. No retry command is accepted. After repairing the cause, run:\n%s", req.RunID, f.ClearCommand(req.StorePath))
			}
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
	faults, err := ReadItemFaults(tx.Reader())
	if err != nil {
		return Retry{}, readFailure(err)
	}
	triage := faults[req.ItemID]
	if triage.Reason == reasonVerifyTriage && triage.RunID != req.RunID {
		return Retry{}, Usagef("item %q needs an operator retry of its held run: %s", req.ItemID, retryCommand(req.ItemID, triage.RunID, triage.RetryAck))
	}
	// A recovered verifier hold can name a later design run. The command
	// answers the triage hold, not that run's design disposition or answer.
	if triage.Reason != reasonVerifyTriage {
		if design, err := authorizeDesignRetry(ctx, tx, req, at); err != nil || design != nil {
			if design == nil {
				return Retry{}, err
			}
			return *design, nil
		}
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

	judged, gateFailed, err := runNeedsGateFailedAck(tx.Reader(), req.RunID)
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
			return Retry{}, Usagef("--acknowledge %s names a run the gate did not judge: %q. The accepted command: %s", RetryAckGateFailed, req.RunID, retryCommand(req.ItemID, req.RunID, acceptedAck(judged, gateFailed, class)))
		}
		if !gateFailed {
			return Retry{}, Usagef("--acknowledge %s names a run whose gate passed: %q. The accepted command: %s", RetryAckGateFailed, req.RunID, retryCommand(req.ItemID, req.RunID, acceptedAck(judged, gateFailed, class)))
		}
	case RetryAckUnjudged:
		if judged || class != AttemptUnjudged {
			return Retry{}, Usagef("--acknowledge %s names a run a wall did not cut short: %q. The accepted command: %s", RetryAckUnjudged, req.RunID, retryCommand(req.ItemID, req.RunID, acceptedAck(judged, gateFailed, class)))
		}
	default:
		return Retry{}, Usagef("--acknowledge %q is not an outcome: want %q or %q. The accepted command: %s", req.Acknowledge, RetryAckGateFailed, RetryAckUnjudged, retryCommand(req.ItemID, req.RunID, acceptedAck(judged, gateFailed, class)))
	}
	if gateFailed && req.Acknowledge != RetryAckGateFailed {
		return Retry{}, Usagef("run %q was judged by the gate: a judged candidate is not retryable by link. An operator who has read the verdict acknowledges it: %s", req.RunID, retryCommand(req.ItemID, req.RunID, RetryAckGateFailed))
	}
	// Guarded by `!judged`: a walled run can also have committed and reached the gate, and
	// once a `gate.verdict` exists the gate's word is the one a retry follows (design
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
	var existingItemID, existingRunID string
	rowErr := tx.QueryRow(
		`SELECT item_id, supersedes_run_id FROM retries WHERE retry_id = ?`, retry.RetryID,
	).Scan(&existingItemID, &existingRunID)
	if rowErr != nil && !errors.Is(rowErr, sql.ErrNoRows) {
		return Retry{}, readFailure(rowErr)
	}
	if rowErr == nil {
		// Keep one authorization per run. The only replacement is an explicit gate
		// acknowledgment when the existing authorization does not cover the verdict.
		replace := false
		if existingItemID == req.ItemID && existingRunID == req.RunID && req.Acknowledge == RetryAckGateFailed {
			acks, err := readRetryAcks(tx.Reader())
			if err != nil {
				return Retry{}, err
			}
			replace = acks[retry.RetryID] != RetryAckGateFailed
		}
		if !replace {
			return Retry{}, Usagef("run %q is already authorized for retry: the authorization %q survives in the store. %s", req.RunID, retry.RetryID, noRetryCommand("the loop moves: its next pass dispatches the retry, or holds it naming why (tillr drain why)"))
		}
		if _, err := tx.Exec(
			`UPDATE retries SET requested_by = ?, reason = ?, requested_at = ? WHERE retry_id = ?`,
			retry.RequestedBy, retry.Reason, retry.RequestedAt.UnixMilli(), retry.RetryID,
		); err != nil {
			return Retry{}, Internalf("recording the retry for %q: %v", req.ItemID, err)
		}
	} else {
		if _, err := tx.Exec(
			`INSERT INTO retries (retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at)
			 VALUES (?, ?, ?, ?, ?, ?)`,
			retry.RetryID, retry.ItemID, retry.SupersedesRunID, retry.RequestedBy, retry.Reason,
			retry.RequestedAt.UnixMilli(),
		); err != nil {
			return Retry{}, Internalf("recording the retry for %q: %v", req.ItemID, err)
		}
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

	// Bind the triage window to this authorization, not to fault clearance or a
	// wall-clock comparison. There need not be a hold yet for a retry to reset it.
	if _, err := tx.Append(event.Event{
		Kind: verifyTriageResetKind, ObjectType: "item", ObjectID: &req.ItemID, Payload: []byte(`{}`), At: at,
	}); err != nil {
		return Retry{}, Internalf("resetting verifier triage for %q: %v", req.ItemID, err)
	}
	if triage.Reason == reasonVerifyTriage || triage.Reason == ReasonAdapterRefused || triage.Reason == ReasonStageFailed {
		if err := clearItemFault(tx, triage, at); err != nil {
			return Retry{}, Internalf("releasing %s hold for %q: %v", triage.Reason, req.ItemID, err)
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
			if triage.Reason == reasonVerifyTriage && triage.RepeatConflictID == c.ConflictID && legacyVerifyRepeat(c) {
				// Only retire the old route this hold superseded. An answered
				// or unrelated conflict is not part of the triage release.
				c.Status, c.WaitingOn = "closed", "worker"
				if err := item.RecordConflict(tx, c, at); err != nil {
					return Retry{}, Internalf("releasing legacy verifier repeat for %q: %v", req.ItemID, err)
				}
			} else if triage.Reason != reasonVerifyTriage && c.Status != "awaiting" && c.Status != "raised" && c.DesignRunID == "" && c.TriageVersion == "" {
				releaseBlocked = true
				var callID string
				if err := tx.Reader().QueryRow(`SELECT COALESCE(object_id, '') FROM events WHERE kind=? AND object_type='call' AND json_extract(payload,'$.run_id')=? ORDER BY seq DESC LIMIT 1`, callOffered, c.RunID).Scan(&callID); err == nil && callID != "" {
					if err := projectLandingCall(tx, callID, "", "superseded", at); err != nil {
						return Retry{}, Internalf("closing open call %q on %q: %v", callID, c.RunID, err)
					}
				}
			}
		} else if triage.Reason != reasonVerifyTriage {
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
