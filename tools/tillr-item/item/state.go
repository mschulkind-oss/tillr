package item

import (
	"context"
	"encoding/json"
	"time"

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
	if itemID == "" {
		return record.Envelope{}, Usagef("an item state write needs an item_id")
	}
	payload, storedAt, err := readStoredItem(tx.Reader(), itemID)
	if err != nil {
		return record.Envelope{}, err
	}
	if terminalState(payload.State) && state != payload.State {
		return record.Envelope{}, terminalRejection(itemID, payload.State)
	}
	if err := checkState(itemID, state); err != nil {
		return record.Envelope{}, err
	}
	if err := checkClosedTransition(itemID, payload.State, state, false); err != nil {
		return record.Envelope{}, err
	}
	previousWaiting, previousUnderlying := payload.WaitingOn, payload.UnderlyingState
	if state == "blocked" {
		var next WaitingOn
		if len(waiting) > 0 {
			next = waiting[0]
		}
		if err := checkWaitingOn(tx.Reader(), itemID, next); err != nil {
			return record.Envelope{}, err
		}
		switch payload.State {
		case "not-started", "in-queue", "blocked":
		default:
			return record.Envelope{}, Rejectionf("blocked-state-invalid", itemID,
				"item %q cannot move from %q to blocked", itemID, payload.State)
		}
		if payload.State != "blocked" || payload.UnderlyingState == "" {
			payload.UnderlyingState = payload.State
		}
		payload.WaitingOn = &next
	} else if state == "not-started" || state == "in-queue" {
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
	if _, err := tx.Exec(
		`UPDATE items SET state = ?, waiting_on = ?, underlying_state = ?, updated_at = ? WHERE item_id = ?`,
		state, waitingJSON, payload.UnderlyingState, at.UnixMilli(), itemID,
	); err != nil {
		return record.Envelope{}, Internalf("moving item %q to %q: %v", itemID, state, err)
	}
	// The event carries the item record, as apply's does: the row holds the state the item
	// is in, and the log holds how it got there. A reader that asks an item's history for
	// its states (drain's planner, which reads `in-progress` as evidence an item has run)
	// reads this event's payload for that field.
	payload.State = state
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
