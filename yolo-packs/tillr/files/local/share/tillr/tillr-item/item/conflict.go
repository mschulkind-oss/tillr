package item

import (
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// ConflictKind records an item-linked specification conflict and each routing change.
const ConflictKind = "item.conflict"

// Conflict retains the report, its origin and the document the design seat may amend.
// Status is raised, awaiting, owner, answered, or closed. Closed means the ordinary
// worker has been dispatched against the answer, not that its criteria have passed.
type Conflict struct {
	ConflictID      string   `json:"conflict_id"`
	ItemID          string   `json:"item_id"`
	RunID           string   `json:"run_id"`
	BodyRef         string   `json:"body_ref"`
	Text            string   `json:"text"`
	BlockedCriteria []string `json:"blocked_criteria"`
	WaitingOn       string   `json:"waiting_on"`
	Status          string   `json:"status"`
	DesignRunID     string   `json:"design_run_id,omitempty"`
	AnswerHead      string   `json:"answer_head,omitempty"`
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
// same transaction. Criteria and verdicts are never written by this operation.
func RecordConflict(tx *store.Tx, c Conflict, at time.Time) error {
	if c.ConflictID == "" || c.ItemID == "" || c.RunID == "" || c.Text == "" || c.WaitingOn == "" {
		return Usagef("a conflict needs its identity, item, run, report, and waiting seat")
	}
	switch c.Status {
	case "raised", "awaiting", "owner", "answered", "closed":
	default:
		return Usagef("unknown conflict status %q", c.Status)
	}
	state := "blocked"
	if c.Status == "answered" || c.Status == "closed" {
		state = "in-queue"
	}
	if _, err := SetState(nil, tx, c.ItemID, state, at); err != nil {
		return err
	}
	body, err := json.Marshal(c)
	if err != nil {
		return err
	}
	_, err = tx.Append(event.Event{Kind: ConflictKind, ObjectType: "item", ObjectID: &c.ItemID, Payload: body, At: at})
	return err
}
