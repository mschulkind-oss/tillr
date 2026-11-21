package item

import (
	"context"
	"database/sql"
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/definition"
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
	landed, err := hasCurrentLanding(tx.Reader(), id)
	if err != nil {
		return record.Envelope{}, Internalf("reading landings: %v", err)
	}
	if landed {
		return record.Envelope{}, Rejectionf("already-landed", id, "item %q has already landed; it cannot be closed", id)
	}
	if err := refuseClaimedLanding(tx.Reader(), id, "closed"); err != nil {
		return record.Envelope{}, err
	}
	d, err := itemDefinition(tx.Reader())
	if err != nil {
		return record.Envelope{}, err
	}
	if err := checkState(d, id, d.Closed); err != nil {
		return record.Envelope{}, err
	}
	if p.State == d.Closed || frozenStates[p.State] || d.Name == definition.GitLandingName && !d.Allows(p.State, d.Closed) {
		return record.Envelope{}, Rejectionf("close-state-invalid", id, "item %q cannot be closed from %q", id, p.State)
	}
	if err := checkMove(d, id, p.State, d.Closed, MoveReceipt{Reason: reason, By: by}); err != nil {
		return record.Envelope{}, err
	}
	p.ClosedFrom, p.State = p.State, d.Closed
	p.CloseReason, p.ClosedBy, p.ClosedAt = reason, by, at.UTC().Format(time.RFC3339)
	if _, err := tx.Exec(`UPDATE items SET state = ?, close_reason = ?, closed_at = ?, closed_by = ?, closed_from = ?, updated_at = ? WHERE item_id = ?`, d.Closed, reason, p.ClosedAt, by, p.ClosedFrom, at.UnixMilli(), id); err != nil {
		return record.Envelope{}, Internalf("closing item: %v", err)
	}
	return recordLifecycle(tx, p, at, "close", reason, by)
}

// refuseClaimedLanding refuses to finish an item a landing is publishing. The lander's
// first short write claims the landing (landing_claims, tools/tillr-land/land/schema.go)
// and its receipt write removes the claim; between them the trunk moves with no store write
// open, so this row, not the write lock, is what keeps an item from being closed under a
// landing that is about to put it on the trunk. The refusal names the run: once that
// landing records its receipt the item is landed, and a landing killed mid-way is finished
// by its run's retry or, when its move never happened, cleared by the next landing.
func refuseClaimedLanding(r *store.Reader, id, to string) error {
	var hasClaims bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type = 'table' AND name = 'landing_claims')`).Scan(&hasClaims); err != nil {
		return Internalf("checking landing claim schema: %v", err)
	}
	if !hasClaims {
		return nil
	}
	var run string
	err := r.QueryRow(`SELECT run_id FROM landing_claims WHERE item_id = ? ORDER BY run_id LIMIT 1`, id).Scan(&run)
	if err == sql.ErrNoRows {
		return nil
	}
	if err != nil {
		return Internalf("reading landing claims: %v", err)
	}
	return Rejectionf("landing-in-progress", id, "item %q is being landed by run %s; it cannot be %s until that landing records its receipt", id, run, to)
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
	d, err := itemDefinition(tx.Reader())
	if err != nil {
		return record.Envelope{}, err
	}
	if p.State != d.Closed {
		return record.Envelope{}, Rejectionf("item-not-closed", id, "item %q is %q, not closed", id, p.State)
	}
	if !d.HasState(p.ClosedFrom) || frozenStates[p.ClosedFrom] || d.Name == definition.GitLandingName && !d.Allows(p.State, p.ClosedFrom) {
		return record.Envelope{}, Rejectionf("closed-from-missing", id, "item %q has no valid closed_from state to restore", id)
	}
	if err := checkMove(d, id, p.State, p.ClosedFrom, MoveReceipt{Reason: reason, By: by}); err != nil {
		return record.Envelope{}, err
	}
	p.State = p.ClosedFrom
	if _, err := tx.Exec(`UPDATE items SET state = ?, updated_at = ? WHERE item_id = ?`, p.State, at.UnixMilli(), id); err != nil {
		return record.Envelope{}, Internalf("reopening item: %v", err)
	}
	return recordLifecycle(tx, p, at, "reopen", reason, by)
}

func terminalRejection(id, state string) error {
	return Rejectionf("state-terminal", id, "item %q is %q; raw writes cannot change a finished item", id, state)
}

func lifecycleEvent(p itemPayload, action, reason, by string) any {
	return struct {
		itemPayload
		Action string `json:"lifecycle_action"`
		Reason string `json:"lifecycle_reason"`
		By     string `json:"lifecycle_by"`
	}{p, action, reason, by}
}

func recordLifecycle(tx *store.Tx, p itemPayload, at time.Time, action, reason, by string) (record.Envelope, error) {
	p.UpdatedAt = at.UTC().Format(time.RFC3339)
	// The event preserves the reopen reason and actor without turning them into current
	// disposition fields. The ordinary item fields remain readable by existing consumers.
	body, err := json.Marshal(lifecycleEvent(p, action, reason, by))
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
