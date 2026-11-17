package item

import (
	"database/sql"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// Show answers the stored item.record for one item, read from the rows rather than
// from the newest record in the log: the row is the item's current state, and the log
// is the history of how it got there.
//
// Where the item carries a design_doc reference, the record also carries that
// document's open questions, read from the document at show time. The board stores no
// question state, so the document is the only copy and the two cannot drift apart.
func Show(r *store.Reader, id, root string) (record.Envelope, error) {
	payload, updatedAt, err := readStoredItem(r, id)
	if err != nil {
		return record.Envelope{}, err
	}
	if payload.State == "blocked" && (payload.WaitingOn == nil || payload.WaitingOn.Waiter == "") {
		payload.WaitingOn = &WaitingOn{Waiter: "waiter not recorded"}
	}
	if payload.DesignDoc != "" {
		questions, err := ReadQuestions(root, payload.DesignDoc)
		if errors.Is(err, ErrDesignDocMissing) {
			return record.Envelope{}, Rejectionf("design-doc-missing", id,
				"item %q points at %s, which is not there", id, payload.DesignDoc)
		}
		if err != nil {
			return record.Envelope{}, Internalf("reading the design document %s: %v", payload.DesignDoc, err)
		}
		payload.Questions = questions
		payload.QuestionsSource = payload.DesignDoc
	}
	return envelopeOf(RecordKind, "item-"+id, updatedAt, payload)
}

// readStoredItem answers one item's record as the rows hold it, with the clock the row was
// last written at. It is the read both faces that answer an item share: show adds the
// design document's open questions, which are the document's rather than the store's, and
// the state face echoes the item without them.
func readStoredItem(r *store.Reader, id string) (itemPayload, time.Time, error) {
	var (
		payload      itemPayload
		updatedAt    int64
		allowedPaths string
	)
	err := r.QueryRow(
		`SELECT item_id, title, COALESCE(body_ref, ''), state, COALESCE(design_doc, ''),
		        updated_at, event_seq, COALESCE(allowed_paths, ''), COALESCE(rank, ''), COALESCE(seat, ''), COALESCE(prose, '')
		   FROM items WHERE item_id = ?`, id,
	).Scan(
		&payload.ItemID, &payload.Title, &payload.BodyRef, &payload.State,
		&payload.DesignDoc, &updatedAt, &payload.EventSeq, &allowedPaths, &payload.Rank, &payload.Seat, &payload.Prose,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return itemPayload{}, time.Time{}, Rejectionf("not-found", id, "no item %q in this store", id)
	}
	if err != nil {
		return itemPayload{}, time.Time{}, Internalf("reading item %q: %v", id, err)
	}
	// A read-only open cannot add the workflow column to a pre-workflow store.
	// Such items named no workflow; return the empty value without mutating them.
	var hasWorkflow bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM pragma_table_info('items') WHERE name = 'workflow')`).Scan(&hasWorkflow); err != nil {
		return itemPayload{}, time.Time{}, Internalf("checking workflow metadata: %v", err)
	}
	if hasWorkflow {
		if err := r.QueryRow(`SELECT workflow FROM items WHERE item_id=?`, id).Scan(&payload.Workflow); err != nil {
			return itemPayload{}, time.Time{}, Internalf("reading workflow metadata: %v", err)
		}
	}
	// Read-only opens do not migrate old stores. Absence means no close metadata
	// was recorded; all other query failures remain explicit errors.
	var hasClose bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM pragma_table_info('items') WHERE name = 'closed_from')`).Scan(&hasClose); err != nil {
		return itemPayload{}, time.Time{}, Internalf("checking close metadata: %v", err)
	}
	if hasClose {
		if err := r.QueryRow(`SELECT close_reason, closed_at, closed_by, closed_from FROM items WHERE item_id = ?`, id).Scan(&payload.CloseReason, &payload.ClosedAt, &payload.ClosedBy, &payload.ClosedFrom); err != nil {
			return itemPayload{}, time.Time{}, Internalf("reading close metadata: %v", err)
		}
	}
	if payload.WaitingOn, payload.UnderlyingState, err = ReadWaitingOn(r, id); err != nil {
		return itemPayload{}, time.Time{}, err
	}
	stored := time.UnixMilli(updatedAt).UTC()
	payload.UpdatedAt = stored.Format(time.RFC3339)
	if payload.AllowedPaths, err = decodeAllowedPaths(allowedPaths); err != nil {
		return itemPayload{}, time.Time{}, err
	}
	if payload.Criteria, err = readCriteria(r, id); err != nil {
		return itemPayload{}, time.Time{}, err
	}
	if payload.BlockedBy, err = readEdges(r, id); err != nil {
		return itemPayload{}, time.Time{}, err
	}
	return payload, stored, nil
}

// readCriteria answers the item's criteria in criterion-id order, so two reads of one
// item produce the same record.
func readCriteria(r *store.Reader, id string) ([]criterion, error) {
	rows, err := r.Query(
		`SELECT criterion_id, text, state, claim_class FROM item_criteria WHERE item_id = ? ORDER BY criterion_id`, id,
	)
	if err != nil {
		return nil, Internalf("reading the criteria of %q: %v", id, err)
	}
	defer rows.Close()
	criteria := []criterion{}
	for rows.Next() {
		var c criterion
		if err := rows.Scan(&c.CriterionID, &c.Text, &c.State, &c.ClaimClass); err != nil {
			return nil, Internalf("reading a criterion of %q: %v", id, err)
		}
		criteria = append(criteria, c)
	}
	if err := rows.Err(); err != nil {
		return nil, Internalf("reading the criteria of %q: %v", id, err)
	}
	return criteria, nil
}

// readEdges answers the items this one is blocked by, in id order.
func readEdges(r *store.Reader, id string) ([]string, error) {
	rows, err := r.Query(`SELECT blocked_by FROM item_edges WHERE item_id = ? ORDER BY blocked_by`, id)
	if err != nil {
		return nil, Internalf("reading the edges of %q: %v", id, err)
	}
	defer rows.Close()
	blockedBy := []string{}
	for rows.Next() {
		var blocker string
		if err := rows.Scan(&blocker); err != nil {
			return nil, Internalf("reading an edge of %q: %v", id, err)
		}
		blockedBy = append(blockedBy, blocker)
	}
	if err := rows.Err(); err != nil {
		return nil, Internalf("reading the edges of %q: %v", id, err)
	}
	return blockedBy, nil
}
