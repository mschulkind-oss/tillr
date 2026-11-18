package item

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// SetState moves one item's own state, and nothing else about it: not a criterion's state
// (that moves only on an item.verdict — board-utilities.md §5.3), not a criterion's frozen
// text, not the edges, not the fence. It is the writer of the item-state half of
// board-item-state-model.md §2: a landed candidate moves its item to `landed`, and a
// candidate that failed the gate leaves its item where it was — `in-queue` — because a
// non-landing run does not move the item's disposition. `in-progress` is a frozen legacy
// state: no transition writes it, and a legacy row moves out of it through this face.
//
// It exists because that ruling needs a writer and `apply` is not one: apply is the
// record→row mapping, so it re-presents the whole item — title, fence, edges, criteria —
// to move one field, and a caller that judged a candidate would have to read the item,
// rewrite it and hope it re-presented it faithfully. This face writes the one field and
// echoes the item as stored, which is what a loop step can call.
//
// The write is idempotent: an item already in the state asked for is answered as it stands
// and appends no event, so a step retried after a half-completed tick does not record a
// transition that did not happen.
func SetState(ctx context.Context, tx *store.Tx, itemID, state string, at time.Time, waiting ...WaitingOn) (record.Envelope, error) {
	return SetStateWithReceipt(ctx, tx, itemID, state, at, MoveReceipt{}, waiting...)
}

// SetStateWithReceipt records evidence and an actor for entering a finished state,
// and the existing reason and actor event for leaving one.
func SetStateWithReceipt(ctx context.Context, tx *store.Tx, itemID, state string, at time.Time, receipt MoveReceipt, waiting ...WaitingOn) (record.Envelope, error) {
	if itemID == "" {
		return record.Envelope{}, Usagef("an item state write needs an item_id")
	}
	payload, storedAt, err := readStoredItem(tx.Reader(), itemID)
	if err != nil {
		return record.Envelope{}, err
	}
	d, err := itemDefinition(tx.Reader())
	if err != nil {
		return record.Envelope{}, err
	}
	if d.Name == definition.GitLandingName && d.IsFinished(payload.State) && payload.State != d.Closed && state != payload.State {
		if err := checkMove(d, itemID, payload.State, state, receipt); err != nil {
			return record.Envelope{}, err
		}
	}
	if err := checkState(d, itemID, state); err != nil {
		return record.Envelope{}, err
	}
	if d.Name != definition.GitLandingName {
		if err := checkMove(d, itemID, payload.State, state, receipt); err != nil {
			return record.Envelope{}, err
		}
	}
	if err := checkClosedTransition(d, itemID, payload.State, state, false); err != nil {
		return record.Envelope{}, err
	}
	if err := checkMove(d, itemID, payload.State, state, receipt); err != nil {
		return record.Envelope{}, err
	}
	if err := checkStateReceipt(tx.Reader(), d, itemID, payload.State, state, receipt); err != nil {
		return record.Envelope{}, err
	}
	if receipt.RequireVerdicts {
		// A verdict a withdrawal named (WithdrawVerdicts) no longer stands.
		for _, criterion := range payload.Criteria {
			var judged bool
			if err := tx.QueryRow(`SELECT EXISTS (SELECT 1 FROM events WHERE kind = ? AND object_id = ? AND json_extract(payload, '$.criterion_id') = ? AND json_extract(payload, '$.criterion_hash') = ? AND seq > (SELECT COALESCE(MAX(seq), 0) FROM events WHERE kind = ? AND object_id = ?))`, VerdictRecordKind, itemID, criterion.CriterionID, CriterionHash(criterion.Text), VerdictsWithdrawnKind, itemID).Scan(&judged); err != nil {
				return record.Envelope{}, Internalf("checking criterion verdicts: %v", err)
			}
			if !judged {
				return record.Envelope{}, Rejectionf("criteria-unjudged", itemID, "criterion %q has no recorded verdict for its stored text", criterion.CriterionID)
			}
		}
	}
	previousWaiting, previousUnderlying := payload.WaitingOn, payload.UnderlyingState
	if state == d.Blocked {
		var next WaitingOn
		if len(waiting) > 0 {
			next = waiting[0]
		}
		if err := checkWaitingOn(tx.Reader(), itemID, next); err != nil {
			return record.Envelope{}, err
		}
		// Legacy rows may leave their frozen state through this face, but the
		// Git-landing block command has never admitted them as blocked sources.
		if d.Name == definition.GitLandingName && payload.State != state && !d.Allows(payload.State, state) {
			return record.Envelope{}, Rejectionf("blocked-state-invalid", itemID,
				"item %q cannot move from %q to blocked", itemID, payload.State)
		}
		if payload.State != d.Blocked || payload.UnderlyingState == "" {
			payload.UnderlyingState = payload.State
		}
		payload.WaitingOn = &next
	} else if state != d.Closed && !d.IsFinished(state) {
		payload.WaitingOn, payload.UnderlyingState = nil, ""
	}
	unchangedWaiting := previousWaiting == nil && payload.WaitingOn == nil ||
		previousWaiting != nil && payload.WaitingOn != nil && *previousWaiting == *payload.WaitingOn
	if payload.State == state && unchangedWaiting && previousUnderlying == payload.UnderlyingState {
		return envelopeOf(RecordKind, "item-"+itemID, storedAt, payload)
	}
	waitingJSON := ""
	if payload.WaitingOn != nil {
		body, err := json.Marshal(payload.WaitingOn)
		if err != nil {
			return record.Envelope{}, Internalf("encoding blocked metadata: %v", err)
		}
		waitingJSON = string(body)
	}
	if payload.State != state && d.IsFinished(state) {
		payload.LifecycleReason, payload.LifecycleBy, payload.ResolvesVia = receipt.Reason, receipt.By, receipt.ResolvesVia
	}
	if _, err := tx.Exec(
		`UPDATE items SET state = ?, waiting_on = ?, underlying_state = ?, lifecycle_reason = ?, lifecycle_by = ?, resolves_via = ?, updated_at = ? WHERE item_id = ?`,
		state, waitingJSON, payload.UnderlyingState, payload.LifecycleReason, payload.LifecycleBy, payload.ResolvesVia, at.UnixMilli(), itemID,
	); err != nil {
		return record.Envelope{}, Internalf("moving item %q to %q: %v", itemID, state, err)
	}
	// The event carries the item record, as apply's does: the row holds the state the item
	// is in, and the log holds how it got there. A reader that asks an item's history for
	// its states (drain's planner, which reads `in-progress` as evidence an item has run)
	// reads this event's payload for that field.
	from := payload.State
	payload.State = state
	if from != state && (d.IsFinished(from) || d.IsFinished(state)) {
		return recordLifecycle(tx, payload, at, "state", receipt.Reason, receipt.By)
	}
	payload.UpdatedAt = at.UTC().Format(time.RFC3339)
	body, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the %s event: %v", UpdateKind, err)
	}
	seq, err := tx.Append(event.Event{
		Kind:       UpdateKind,
		ObjectType: "item",
		ObjectID:   &payload.ItemID,
		Payload:    body,
		At:         at,
	})
	if err != nil {
		return record.Envelope{}, Internalf("appending the %s event: %v", UpdateKind, err)
	}
	if _, err := tx.Exec(`UPDATE items SET event_seq = ? WHERE item_id = ?`, seq, itemID); err != nil {
		return record.Envelope{}, Internalf("recording the event sequence: %v", err)
	}
	payload.EventSeq = seq
	return envelopeOf(RecordKind, "item-"+itemID, at, payload)
}
