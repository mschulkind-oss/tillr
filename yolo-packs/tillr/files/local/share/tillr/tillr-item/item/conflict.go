package item

import (
	"database/sql"
	"encoding/json"
	"errors"
	"reflect"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// ConflictKind records an item-linked specification conflict and each routing change.
const ConflictKind = "item.conflict"

// Conflict retains the report, its origin and the document the design seat may amend.
// Status is raised, awaiting, owner, question, answered, or closed. Question means
// a decision was published in a document but has not yet been answered there.
// Closed means the ordinary worker was dispatched against the answer, or a legacy
// conflict was migrated into a document question; neither passes its criteria.
type Conflict struct {
	ConflictID          string   `json:"conflict_id"`
	ItemID              string   `json:"item_id"`
	RunID               string   `json:"run_id"`
	BodyRef             string   `json:"body_ref"`
	Text                string   `json:"text"`
	BlockedCriteria     []string `json:"blocked_criteria"`
	WaitingOn           string   `json:"waiting_on"`
	Status              string   `json:"status"`
	DesignRunID         string   `json:"design_run_id,omitempty"`
	QuestionRemovedHead string   `json:"question_removed_head,omitempty"`
	AnswerHead          string   `json:"answer_head,omitempty"`
	// QuestionRef and QuestionHead identify publication, not an owner ruling.
	// The document and shared Vantage reader own whether it is still open.
	QuestionRef   string `json:"question_ref,omitempty"`
	QuestionHead  string `json:"question_head,omitempty"`
	QuestionRunID string `json:"question_run_id,omitempty"`

	// TriageVersion hashes the stored title, body reference, and criteria of a request
	// that has no document block. Empty for an ordinary specification conflict.
	TriageVersion string `json:"triage_version,omitempty"`
}

func init() {
	if err := event.Register(ConflictKind, Utility); err != nil {
		panic(err)
	}
}

// ReadConflicts returns the last recorded transition for each item's conflict.
func ReadConflicts(r *store.Reader) (map[string]Conflict, error) {
	rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? ORDER BY seq`, ConflictKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	result := map[string]Conflict{}
	for rows.Next() {
		var body []byte
		var c Conflict
		if err := rows.Scan(&body); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(body, &c); err != nil {
			return nil, err
		}
		result[c.ItemID] = c
	}
	return result, rows.Err()
}

// RecordConflict appends a transition and changes the item's disposition in the
// same transaction. A record equal in every field to this conflict's last record
// writes nothing. Criteria and verdicts are never written by this operation.
func RecordConflict(tx *store.Tx, c Conflict, at time.Time) error {
	noRunTriage := c.TriageVersion != "" && c.DesignRunID == "" &&
		(c.Status == "owner" || c.Status == "answered" || c.Status == "closed")
	if c.ConflictID == "" || c.ItemID == "" || (c.RunID == "" && !noRunTriage) || c.Text == "" || c.WaitingOn == "" {
		return Usagef("a conflict needs its identity, item, run, report, and waiting seat; owner triage with no run may omit the run")
	}
	switch c.Status {
	case "raised", "awaiting", "owner", "question", "answered", "closed":
	default:
		return Usagef("unknown conflict status %q", c.Status)
	}
	if c.Status == "question" && (c.QuestionRef == "" || c.QuestionHead == "" || c.QuestionRunID == "" || c.WaitingOn != "owner") {
		return Usagef("a published owner question needs its document reference, publication head, and owner waiter")
	}
	var previous []byte
	err := tx.Reader().QueryRow(`SELECT payload FROM events WHERE kind = ?
 AND json_extract(payload, '$.conflict_id') = ? AND json_extract(payload, '$.item_id') = ?
 ORDER BY seq DESC LIMIT 1`, ConflictKind, c.ConflictID, c.ItemID).Scan(&previous)
	if err == nil {
		var last Conflict
		if err := json.Unmarshal(previous, &last); err != nil {
			return err
		}
		if reflect.DeepEqual(last, c) {
			return nil
		}
	} else if !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	state := "blocked"
	if c.Status == "question" || c.Status == "answered" || c.Status == "closed" {
		state = "in-queue"
	}
	reason, _, _ := strings.Cut(c.Text, "\n")
	if _, err := SetState(nil, tx, c.ItemID, state, at, WaitingOn{Waiter: c.WaitingOn, Reason: strings.TrimSpace(reason)}); err != nil {
		return err
	}
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: ConflictKind, ObjectType: "item", ObjectID: &c.ItemID, Payload: body, At: at})
	return err
}
