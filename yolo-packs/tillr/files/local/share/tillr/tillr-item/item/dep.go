package item

import (
	"context"
	"encoding/json"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// SetDeps adds and removes blocked_by edges on an existing item without rewriting
// other fields or changing its rank/place. Unchanged dependencies append no event.
func SetDeps(ctx context.Context, tx *store.Tx, itemID string, add, remove []string, at time.Time) (record.Envelope, error) {
	if itemID == "" {
		return record.Envelope{}, Usagef("an item dep write needs an item_id")
	}
	payload, storedAt, err := readStoredItem(tx.Reader(), itemID)
	if err != nil {
		return record.Envelope{}, err
	}
	normAdd := make([]string, 0, len(add))
	for _, target := range add {
		target = strings.TrimSpace(target)
		if target == "" {
			return record.Envelope{}, Rejectionf("edge-target-missing", itemID, "item %q dependency target must not be empty", itemID)
		}
		if target == itemID {
			return record.Envelope{}, Rejectionf("edge-self", itemID, "item %q cannot be blocked by itself", itemID)
		}
		exists, err := itemExists(ctx, tx.Reader(), target)
		if err != nil {
			return record.Envelope{}, err
		}
		if !exists {
			return record.Envelope{}, Rejectionf("edge-target-missing", itemID, "item %q is blocked by %q, which does not exist", itemID, target)
		}
		normAdd = append(normAdd, target)
	}
	normRemove := make([]string, 0, len(remove))
	for _, target := range remove {
		target = strings.TrimSpace(target)
		if target == "" {
			return record.Envelope{}, Rejectionf("edge-target-missing", itemID, "item %q dependency target must not be empty", itemID)
		}
		exists, err := itemExists(ctx, tx.Reader(), target)
		if err != nil {
			return record.Envelope{}, err
		}
		if !exists {
			return record.Envelope{}, Rejectionf("edge-target-missing", itemID, "item %q cannot remove dependency %q, which does not exist", itemID, target)
		}
		normRemove = append(normRemove, target)
	}

	deps := make(map[string]bool, len(payload.BlockedBy)+len(normAdd))
	for _, existing := range payload.BlockedBy {
		deps[existing] = true
	}
	for _, target := range normRemove {
		delete(deps, target)
	}
	for _, target := range normAdd {
		deps[target] = true
	}
	nextBlockedBy := make([]string, 0, len(deps))
	for dep := range deps {
		nextBlockedBy = append(nextBlockedBy, dep)
	}
	slices.Sort(nextBlockedBy)

	if slices.Equal(payload.BlockedBy, nextBlockedBy) {
		return envelopeOf(RecordKind, "item-"+itemID, storedAt, payload)
	}
	payload.BlockedBy = nextBlockedBy
	if err := replaceEdges(ctx, tx, payload); err != nil {
		return record.Envelope{}, err
	}
	if _, err := tx.Exec(`UPDATE items SET updated_at = ? WHERE item_id = ?`, at.UnixMilli(), itemID); err != nil {
		return record.Envelope{}, Internalf("updating item %q dependencies: %v", itemID, err)
	}
	payload.UpdatedAt = at.UTC().Format(time.RFC3339)
	body, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, Internalf("encoding the dep update: %v", err)
	}
	seq, err := tx.Append(event.Event{Kind: UpdateKind, ObjectType: "item", ObjectID: &payload.ItemID, Payload: body, At: at})
	if err != nil {
		return record.Envelope{}, Internalf("appending the dep update: %v", err)
	}
	if _, err := tx.Exec(`UPDATE items SET event_seq = ? WHERE item_id = ?`, seq, itemID); err != nil {
		return record.Envelope{}, Internalf("recording the event sequence: %v", err)
	}
	payload.EventSeq = seq
	return envelopeOf(RecordKind, "item-"+itemID, at, payload)
}
