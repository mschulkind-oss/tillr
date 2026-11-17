package item

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Close records why an item is no longer wanted and retains its prior disposition.
// The store write transaction serializes this decision with Land's final state check.
func Close(ctx context.Context, tx *store.Tx, id, reason, by string, at time.Time) (record.Envelope, error) {
	if strings.TrimSpace(reason) == "" {
		return record.Envelope{}, Rejectionf("close-reason-missing", id, "tillr item close needs a non-empty --reason")
	}
	if strings.TrimSpace(by) == "" {
		return record.Envelope{}, Usagef("tillr item close needs --by <who>")
	}
	p, _, err := readStoredItem(tx.Reader(), id)
	if err != nil {
		return record.Envelope{}, err
	}
	// An item utility can create a store without the land utility's tables.
	var hasLandings bool
	if err := tx.Reader().QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'landings')`).Scan(&hasLandings); err != nil {
		return record.Envelope{}, Internalf("checking landing schema: %v", err)
	}
	if hasLandings {
		var landed bool
		if err := tx.Reader().QueryRow(`SELECT EXISTS (SELECT 1 FROM landings WHERE item_id = ? AND result = 'landed')`, id).Scan(&landed); err != nil {
			return record.Envelope{}, Internalf("reading landings: %v", err)
		}
		if landed {
			return record.Envelope{}, Rejectionf("already-landed", id, "item %q has already landed; it cannot be closed", id)
		}
	}
	switch p.State {
	case "not-started", "in-queue", "blocked":
	default:
		return record.Envelope{}, Rejectionf("close-state-invalid", id, "item %q cannot be closed from %q", id, p.State)
	}
	p.ClosedFrom, p.State = p.State, "closed"
	p.CloseReason, p.ClosedBy, p.ClosedAt = reason, by, at.UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`UPDATE items SET state = 'closed', close_reason = ?, closed_at = ?, closed_by = ?, closed_from = ?, updated_at = ? WHERE item_id = ?`, reason, p.ClosedAt, by, p.ClosedFrom, at.UnixMilli(), id); err != nil {
		return record.Envelope{}, Internalf("closing item: %v", err)
	}
	return recordLifecycle(tx, p, at, "close", reason, by)
}

// Reopen restores the saved disposition, including blocked metadata left intact by Close.
func Reopen(ctx context.Context, tx *store.Tx, id, reason, by string, at time.Time) (record.Envelope, error) {
	if strings.TrimSpace(reason) == "" {
		return record.Envelope{}, Rejectionf("reopen-reason-missing", id, "tillr item reopen needs a non-empty --reason")
	}
	if strings.TrimSpace(by) == "" {
		return record.Envelope{}, Usagef("tillr item reopen needs --by <who>")
	}
	p, _, err := readStoredItem(tx.Reader(), id)
	if err != nil {
		return record.Envelope{}, err
	}
	if p.State != "closed" {
		return record.Envelope{}, Rejectionf("item-not-closed", id, "item %q is %q, not closed", id, p.State)
	}
	switch p.ClosedFrom {
	case "not-started", "in-queue", "blocked":
	default:
		return record.Envelope{}, Rejectionf("closed-from-missing", id, "item %q has no valid closed_from state to restore", id)
	}
	p.State = p.ClosedFrom
	if _, err := tx.Exec(`UPDATE items SET state = ?, updated_at = ? WHERE item_id = ?`, p.State, at.UnixMilli(), id); err != nil {
		return record.Envelope{}, Internalf("reopening item: %v", err)
	}
	return recordLifecycle(tx, p, at, "reopen", reason, by)
}

// terminalState names the finished dispositions raw writes cannot change.
func terminalState(state string) bool {
	return state == "landed" || state == "resolved-elsewhere"
}

func terminalRejection(id, state string) error {
	return Rejectionf("state-terminal", id, "item %q is %q; raw writes cannot change a finished item", id, state)
}

// checkClosedTransition keeps raw state/apply writes from bypassing the two commands.
// Apply may update a closed item's definition without changing its disposition.
func checkClosedTransition(id, from, to string, definition bool) error {
	if from == "closed" && (!definition || to != from) {
		return Rejectionf("closed-use-reopen", id, "item %q is closed; use tillr item reopen --item %s --reason <text> --by <who>", id, id)
	}
	if to == "closed" && from != "closed" {
		return Rejectionf("closed-use-close", id, "use tillr item close --item %s --reason <text> --by <who>", id)
	}
	return nil
}

func recordLifecycle(tx *store.Tx, p itemPayload, at time.Time, action, reason, by string) (record.Envelope, error) {
	p.UpdatedAt = at.UTC().Format(time.RFC3339)
	// The event preserves the reopen reason and actor without turning them into current
	// disposition fields. The ordinary item fields remain readable by existing consumers.
	body, err := json.Marshal(struct {
		itemPayload
		Action string `json:"lifecycle_action"`
		Reason string `json:"lifecycle_reason"`
		By     string `json:"lifecycle_by"`
	}{p, action, reason, by})
	if err != nil {
		return record.Envelope{}, Internalf("encoding lifecycle event: %v", err)
	}
	seq, err := tx.Append(event.Event{Kind: UpdateKind, ObjectType: "item", ObjectID: &p.ItemID, Payload: body, At: at})
	if err != nil {
		return record.Envelope{}, Internalf("appending lifecycle event: %v", err)
	}
	if _, err := tx.Exec(`UPDATE items SET event_seq = ? WHERE item_id = ?`, seq, p.ItemID); err != nil {
		return record.Envelope{}, Internalf("recording lifecycle sequence: %v", err)
	}
	p.EventSeq = seq
	return envelopeOf(RecordKind, "item-"+p.ItemID, at, p)
}
