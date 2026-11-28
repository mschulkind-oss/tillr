package runhistory

import (
	"database/sql"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// RetryKind is the record kind one operator authorization is written and read as. It is a
// record and not an event: the `retries` row is the store's copy, and registering the kind
// as appendable would claim a state transition this face does not assert.
const RetryKind = "drain.retry"

// RetryAckKind is the event a retry appends in the same transaction as its `retries` row
// insertion or replacement.
const RetryAckKind = "drain.retry.ack"

// DesignRetryKind is the event a retry appends when it names an item and the design run
// that ended without answering the item's conflict, now with the owner.
const DesignRetryKind = "drain.retry.design"

// StageRetryKind is the event a retry appends when it names an item and the unstarted
// run id held by an active stage-failed fault.
const StageRetryKind = "drain.retry.stage"

func init() {
	for _, kind := range []string{RetryAckKind, DesignRetryKind, StageRetryKind} {
		if err := event.Register(kind, drainmodel.Utility); err != nil {
			panic(err)
		}
	}
}

// The two outcomes a retry may acknowledge.
const (
	// RetryAckGateFailed acknowledges a judged run: the gate answered it, and the
	// operator is re-dispatching it anyway.
	RetryAckGateFailed = "gate-failed"
	// RetryAckUnjudged acknowledges a run a wall cut short: no judge saw it, and the
	// operator has inspected the tree and is re-dispatching it anyway.
	RetryAckUnjudged = "unjudged"
)

type RetryAckPayload struct {
	Acknowledge string `json:"acknowledge"`
}

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
	// ClearsStageFault is the active stage-failed fault sequence a stage retry cleared,
	// and 0 for a build or design retry. A stage retry writes no `retries` row (StageRetryKind).
	ClearsStageFault int64 `json:"clears_stage_fault,omitempty"`
}

// RetryRequest is one operator's authorization to re-dispatch a run, before it is written.
type RetryRequest struct {
	StorePath   string
	ItemID      string
	RunID       string
	By          string
	Why         string
	At          time.Time
	Acknowledge string
}

// RetryRow is one stored authorization as the planner reads it.
type RetryRow struct {
	RetryID         string
	SupersedesRunID string
	RequestedBy     string
	Reason          string
	RequestedAt     time.Time
	Acknowledge     string
}

// TerminalRunStates are run's terminal states, as the retry face reads them: a retry
// supersedes a finished run, never one still in flight.
var TerminalRunStates = map[string]bool{
	run.StateExited:        true,
	run.StateFailedToStart: true,
	run.StateTimedOut:      true,
}

// RetryID is one authorization's deterministic identity: the item and the run it supersedes.
func RetryID(itemID, runID string) string { return "retry-" + itemID + "-" + runID }

func AuthorizeStageRetry(tx *store.Tx, req RetryRequest, f ItemFault, at time.Time) (Retry, error) {
	if err := ClearItemFaultTx(tx, f, at); err != nil {
		return Retry{}, drainmodel.Internalf("releasing %s hold for %q: %v", f.Reason, req.ItemID, err)
	}
	retry := Retry{
		RetryID:          "retry-stage-" + req.ItemID + "-" + req.RunID,
		ItemID:           req.ItemID,
		SupersedesRunID:  req.RunID,
		RequestedBy:      req.By,
		Reason:           req.Why,
		RequestedAt:      at,
		ClearsStageFault: f.Seq,
	}
	body, err := json.Marshal(retry)
	if err != nil {
		return Retry{}, drainmodel.Internalf("encoding the stage retry for %q: %v", req.ItemID, err)
	}
	id := req.RunID
	if _, err := tx.Append(event.Event{Kind: StageRetryKind, ObjectType: "run", ObjectID: &id, Payload: body, At: at}); err != nil {
		return Retry{}, drainmodel.Internalf("recording the stage retry for %q: %v", req.ItemID, err)
	}
	return retry, nil
}

// RetryEnvelope wraps one authorization in design §5.1's envelope.
func RetryEnvelope(retry Retry) (record.Envelope, error) {
	body, err := json.Marshal(retry)
	if err != nil {
		return record.Envelope{}, drainmodel.Internalf("encoding the retry for %q: %v", retry.ItemID, err)
	}
	kind := RetryKind
	if retry.ReraisesConflict != "" {
		kind = DesignRetryKind
	} else if retry.ClearsStageFault != 0 {
		kind = StageRetryKind
	}
	return record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          kind,
		ID:            retry.RetryID,
		Producer:      record.Producer{System: record.System, Utility: drainmodel.Utility, Version: drainmodel.Version},
		CreatedAt:     retry.RequestedAt,
		Subject:       &record.Subject{Kind: "item", ID: retry.ItemID},
		Payload:       body,
	}, nil
}

// ReadRetries answers every item's latest authorization, keyed by item id.
func ReadRetries(r *store.Reader) (map[string]RetryRow, error) {
	acks, err := ReadRetryAcks(r)
	if err != nil {
		return nil, err
	}
	rows, err := r.Query(
		`SELECT retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at
		   FROM retries ORDER BY requested_at, rowid`)
	if err != nil {
		if strings.Contains(err.Error(), "no such table: retries") {
			return map[string]RetryRow{}, nil
		}
		return nil, storelog.ReadFailure(err)
	}
	defer rows.Close()
	retries := map[string]RetryRow{}
	for rows.Next() {
		var (
			itemID      string
			requestedAt int64
			row         RetryRow
		)
		if err := rows.Scan(&row.RetryID, &itemID, &row.SupersedesRunID, &row.RequestedBy, &row.Reason, &requestedAt); err != nil {
			return nil, storelog.ReadFailure(err)
		}
		row.RequestedAt = time.UnixMilli(requestedAt).UTC()
		row.Acknowledge = acks[row.RetryID]
		retries[itemID] = row
	}
	if err := rows.Err(); err != nil {
		return nil, storelog.ReadFailure(err)
	}
	return retries, nil
}

// ReadRetryAcks answers every retry acknowledgment, keyed by the retry id it names.
func ReadRetryAcks(r *store.Reader) (map[string]string, error) {
	events, err := r.EventsOfKind(0, RetryAckKind)
	if err != nil {
		return nil, storelog.ReadFailure(err)
	}
	acks := map[string]string{}
	for _, e := range events {
		if e.Kind != RetryAckKind || e.ObjectType != "retry" || e.ObjectID == nil {
			continue
		}
		var payload RetryAckPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil || payload.Acknowledge == "" {
			continue
		}
		acks[*e.ObjectID] = payload.Acknowledge
	}
	return acks, nil
}

// ReadRetriesForItems reads the latest retry authorization for each named item.
func ReadRetriesForItems(r *store.Reader, itemIDs []string) (map[string]RetryRow, error) {
	retries := map[string]RetryRow{}
	if len(itemIDs) == 0 {
		return retries, nil
	}
	type rawRetry struct {
		itemID string
		row    RetryRow
	}
	var collected []rawRetry
	var retryIDs []string
	const chunk = 400
	for start := 0; start < len(itemIDs); start += chunk {
		end := start + chunk
		if end > len(itemIDs) {
			end = len(itemIDs)
		}
		batch := itemIDs[start:end]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := r.Query(
			`SELECT retry_id, item_id, supersedes_run_id, requested_by, reason, requested_at
			   FROM retries WHERE item_id IN (`+placeholders+`) ORDER BY requested_at, rowid`, args...)
		if err != nil {
			if strings.Contains(err.Error(), "no such table: retries") {
				return retries, nil
			}
			return nil, storelog.ReadFailure(err)
		}
		for rows.Next() {
			var (
				itemID      string
				requestedAt int64
				row         RetryRow
			)
			if err := rows.Scan(&row.RetryID, &itemID, &row.SupersedesRunID, &row.RequestedBy, &row.Reason, &requestedAt); err != nil {
				rows.Close()
				return nil, storelog.ReadFailure(err)
			}
			row.RequestedAt = time.UnixMilli(requestedAt).UTC()
			collected = append(collected, rawRetry{itemID: itemID, row: row})
			retryIDs = append(retryIDs, row.RetryID)
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, storelog.ReadFailure(err)
		}
		rows.Close()
	}
	if len(collected) == 0 {
		return retries, nil
	}
	acks, err := ReadRetryAcksFor(r, retryIDs)
	if err != nil {
		return nil, err
	}
	for _, entry := range collected {
		entry.row.Acknowledge = acks[entry.row.RetryID]
		retries[entry.itemID] = entry.row
	}
	return retries, nil
}

// ReadRetryAcksFor answers retry acknowledgments for the named retry IDs.
func ReadRetryAcksFor(r *store.Reader, retryIDs []string) (map[string]string, error) {
	acks := map[string]string{}
	if len(retryIDs) == 0 {
		return acks, nil
	}
	events, err := r.EventsForObjects(0, retryIDs, RetryAckKind)
	if err != nil {
		return nil, storelog.ReadFailure(err)
	}
	for _, e := range events {
		if e.Kind != RetryAckKind || e.ObjectType != "retry" || e.ObjectID == nil {
			continue
		}
		var payload RetryAckPayload
		if err := json.Unmarshal(e.Payload, &payload); err != nil || payload.Acknowledge == "" {
			continue
		}
		acks[*e.ObjectID] = payload.Acknowledge
	}
	return acks, nil
}

// RunJudged answers whether the gate judged one run.
func RunJudged(r *store.Reader, runID string) (bool, error) {
	judged, _, err := RunGateResult(r, runID)
	return judged, err
}

// RunGateResult returns whether the gate recorded a verdict for runID and the latest
// verdict's result ("pass", "pass-inherited", "fail", "error", or "").
func RunGateResult(r *store.Reader, runID string) (bool, string, error) {
	var payload []byte
	err := r.QueryRow(
		`SELECT payload FROM events WHERE kind = ? AND object_type = 'run' AND object_id = ? ORDER BY seq DESC LIMIT 1`,
		storelog.GateVerdictKind, runID,
	).Scan(&payload)
	if errors.Is(err, sql.ErrNoRows) {
		return false, "", nil
	}
	if err != nil {
		return false, "", storelog.ReadFailure(err)
	}
	var v struct {
		Result string `json:"result"`
	}
	_ = json.Unmarshal(payload, &v)
	return true, v.Result, nil
}

func GateResultPassed(result string) bool {
	return drainmodel.ClassifyCheck(checkresult.Observation{
		Source: checkresult.ResultWord,
		Word:   result,
	}).Outcome == checkresult.Pass
}

func RunNeedsGateFailedAck(r *store.Reader, runID string) (judged, gateFailed bool, err error) {
	judged, result, err := RunGateResult(r, runID)
	if err != nil {
		return false, false, err
	}
	return judged, judged && !GateResultPassed(result), nil
}
