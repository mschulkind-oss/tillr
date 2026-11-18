package item

import (
	"database/sql"
	"encoding/json"
	"errors"
	"io/fs"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	"github.com/mschulkind-oss/tillr/vantage"
)

// Show answers the stored item.record for one item, read from the rows rather than
// from the newest record in the log: the row is the item's current state, and the log
// is the history of how it got there.
//
// Where the item carries a design_doc reference, the record also carries that
// document's unanswered questions, read through Vantage's index at show time. No
// question state is stored. An unreadable index is unknown, never an empty answer.
func Show(r *store.Reader, id, root string) (record.Envelope, error) {
	return ShowWithQuestions(r, id, root, &vantage.VantageQuestions{Root: root})
}

// ReadStored answers the item's current stored fields without reading the question
// index. A design_doc must still name a regular file, resolved against root.
// Staging and resume use this read so neither starts a program for discarded questions.
func ReadStored(r *store.Reader, id, root string) (record.Envelope, error) {
	payload, updatedAt, err := readStoredItem(r, id)
	if err != nil {
		return record.Envelope{}, err
	}
	if payload.State == "blocked" && (payload.WaitingOn == nil || payload.WaitingOn.Waiter == "") {
		payload.WaitingOn = &WaitingOn{Waiter: "waiter not recorded"}
	}
	if payload.DesignDoc != "" {
		path := payload.DesignDoc
		if !filepath.IsAbs(path) {
			path = filepath.Join(root, path)
		}
		info, err := os.Stat(path)
		if errors.Is(err, fs.ErrNotExist) || (err == nil && !info.Mode().IsRegular()) {
			return record.Envelope{}, Rejectionf("design-doc-missing", id,
				"item %q points at %s, which is not there", id, payload.DesignDoc)
		}
		if err != nil {
			return record.Envelope{}, Internalf("reading the design document %s: %v", payload.DesignDoc, err)
		}
	}
	places, err := OrderPlaces(r)
	if err != nil {
		return record.Envelope{}, Internalf("reading item places: %v", err)
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		return record.Envelope{}, err
	}
	var fields map[string]json.RawMessage
	if err := json.Unmarshal(raw, &fields); err != nil {
		return record.Envelope{}, err
	}
	delete(fields, "rank")
	if place := places[id]; place > 0 {
		fields["place"], err = json.Marshal(place)
		if err != nil {
			return record.Envelope{}, err
		}
	}
	return envelopeOf(RecordKind, "item-"+id, updatedAt, fields)
}

// ShowWithQuestions shares the same question source as the loop and view. The CLI
// supplies a source with the caller's command override; Show uses the default.
func ShowWithQuestions(r *store.Reader, id, root string, source vantage.QuestionSource) (record.Envelope, error) {
	env, err := ReadStored(r, id, root)
	if err != nil {
		return record.Envelope{}, err
	}
	var payload itemPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		return record.Envelope{}, Internalf("reading stored item %q: %v", id, err)
	}
	if payload.DesignDoc != "" {
		answer, err := source.Document(payload.DesignDoc)
		payload.QuestionsSource = payload.DesignDoc
		switch {
		case err != nil:
			payload.QuestionsStatus = "unknown"
			payload.QuestionsReason = err.Error()
		case !answer.Present:
			return record.Envelope{}, Rejectionf("design-doc-missing", id,
				"item %q points at %s, which is not there", id, payload.DesignDoc)
		default:
			payload.QuestionsStatus = "known"
			payload.Questions = []Question{}
			for _, question := range answer.Questions {
				if question.State != vantage.QuestionAnswered {
					payload.Questions = append(payload.Questions, Question{
						QuestionID: question.ID, Text: question.Title, State: question.State,
					})
				}
			}
		}
	}
	return envelopeOf(RecordKind, "item-"+id, env.CreatedAt, payload)
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
	if payload.Express, err = readExpress(r, id); err != nil {
		return itemPayload{}, time.Time{}, err
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
	if payload.SelectedWorkflow, payload.WorkflowSource, _, err = ReadWorkflowSelection(r, id); err != nil {
		return itemPayload{}, time.Time{}, Internalf("reading workflow selection: %v", err)
	}
	if payload.JudgeMode, err = readJudgeMode(r, id); err != nil {
		return itemPayload{}, time.Time{}, err
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
	// Read-only opens must also work before finished-state receipt columns exist.
	for column, value := range map[string]*string{
		"lifecycle_reason": &payload.LifecycleReason,
		"lifecycle_by":     &payload.LifecycleBy,
		"resolves_via":     &payload.ResolvesVia,
	} {
		var present bool
		if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM pragma_table_info('items') WHERE name = ?)`, column).Scan(&present); err != nil {
			return itemPayload{}, time.Time{}, Internalf("checking finished-state receipt: %v", err)
		}
		if present {
			if err := r.QueryRow(`SELECT `+column+` FROM items WHERE item_id = ?`, id).Scan(value); err != nil {
				return itemPayload{}, time.Time{}, Internalf("reading finished-state receipt: %v", err)
			}
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

// readExpress preserves the default for a read-only, pre-express store.
func readExpress(r *store.Reader, id string) (bool, error) {
	var present, express bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM pragma_table_info('items') WHERE name = 'express')`).Scan(&present); err != nil {
		return false, Internalf("checking express metadata: %v", err)
	}
	if present {
		if err := r.QueryRow(`SELECT express FROM items WHERE item_id=?`, id).Scan(&express); err != nil {
			return false, Internalf("reading express metadata: %v", err)
		}
	}
	return express, nil
}

// readJudgeMode is shared by the show and definition readers. A legacy store
// without the column requested no judge; reading it must not migrate that store.
func readJudgeMode(r *store.Reader, id string) (string, error) {
	var present bool
	if err := r.QueryRow(`SELECT EXISTS (SELECT 1 FROM pragma_table_info('items') WHERE name = 'judge_mode')`).Scan(&present); err != nil {
		return "", Internalf("checking judge metadata: %v", err)
	}
	if !present {
		return "", nil
	}
	var mode string
	if err := r.QueryRow(`SELECT judge_mode FROM items WHERE item_id=?`, id).Scan(&mode); err != nil {
		return "", Internalf("reading judge metadata: %v", err)
	}
	return mode, nil
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
