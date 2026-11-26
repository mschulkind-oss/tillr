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
	return SetTypedDeps(ctx, tx, itemID, add, nil, remove, at)
}

// SetTypedDeps adds blocked_by or ordering-only after edges and removes any scheduling
// edges on an existing item without rewriting other fields or changing its rank/place.
// Unchanged dependencies append no event.
func SetTypedDeps(ctx context.Context, tx *store.Tx, itemID string, addBlockedBy, addAfter, remove []string, at time.Time) (record.Envelope, error) {
	if itemID == "" {
		return record.Envelope{}, Usagef("an item dep write needs an item_id")
	}
	payload, storedAt, err := readStoredItem(tx.Reader(), itemID)
	if err != nil {
		return record.Envelope{}, err
	}
	normAddBlockedBy := make([]string, 0, len(addBlockedBy))
	addBlockedSet := make(map[string]bool, len(addBlockedBy))
	for _, target := range addBlockedBy {
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
		normAddBlockedBy = append(normAddBlockedBy, target)
		addBlockedSet[target] = true
	}
	normAddAfter := make([]string, 0, len(addAfter))
	for _, target := range addAfter {
		target = strings.TrimSpace(target)
		if target == "" {
			return record.Envelope{}, Rejectionf("edge-target-missing", itemID, "item %q dependency target must not be empty", itemID)
		}
		if target == itemID {
			return record.Envelope{}, Rejectionf("edge-self", itemID, "item %q cannot run after itself", itemID)
		}
		if addBlockedSet[target] {
			return record.Envelope{}, Rejectionf("edge-overlap", itemID, "item %q cannot add %q as both blocked_by and after", itemID, target)
		}
		exists, err := itemExists(ctx, tx.Reader(), target)
		if err != nil {
			return record.Envelope{}, err
		}
		if !exists {
			return record.Envelope{}, Rejectionf("edge-target-missing", itemID, "item %q runs after %q, which does not exist", itemID, target)
		}
		normAddAfter = append(normAddAfter, target)
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

	blockedDeps := make(map[string]bool, len(payload.BlockedBy)+len(normAddBlockedBy))
	for _, existing := range payload.BlockedBy {
		blockedDeps[existing] = true
	}
	afterDeps := make(map[string]bool, len(payload.After)+len(normAddAfter))
	for _, existing := range payload.After {
		afterDeps[existing] = true
	}
	for _, target := range normRemove {
		delete(blockedDeps, target)
		delete(afterDeps, target)
	}
	for _, target := range normAddBlockedBy {
		delete(afterDeps, target)
		blockedDeps[target] = true
	}
	for _, target := range normAddAfter {
		delete(blockedDeps, target)
		afterDeps[target] = true
	}
	nextBlockedBy := make([]string, 0, len(blockedDeps))
	for dep := range blockedDeps {
		nextBlockedBy = append(nextBlockedBy, dep)
	}
	slices.Sort(nextBlockedBy)
	var nextAfter []string
	if len(afterDeps) > 0 {
		nextAfter = make([]string, 0, len(afterDeps))
		for dep := range afterDeps {
			nextAfter = append(nextAfter, dep)
		}
		slices.Sort(nextAfter)
	}

	if slices.Equal(payload.BlockedBy, nextBlockedBy) && slices.Equal(payload.After, nextAfter) {
		return envelopeOf(RecordKind, "item-"+itemID, storedAt, payload)
	}
	payload.BlockedBy = nextBlockedBy
	payload.After = nextAfter
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
