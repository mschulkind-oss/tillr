package item

import (
	"context"
	"encoding/json"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// VerdictsWithdrawnKind is the event appended in the transaction that withdraws an item's
// criterion states: the record that the verdicts it names no longer stand, and why.
const VerdictsWithdrawnKind = "item.verdicts-withdrawn"

// WithdrawnCriterion is one criterion state a withdrawal cleared, as it stood.
type WithdrawnCriterion struct {
	CriterionID string `json:"criterion_id"`
	State       string `json:"state"`
}

// withdrawnPayload is the item.verdicts-withdrawn payload.
type withdrawnPayload struct {
	ItemID   string               `json:"item_id"`
	Criteria []WithdrawnCriterion `json:"criteria"`
	Reason   string               `json:"reason"`
	By       string               `json:"by"`
}

// WithdrawVerdicts clears every criterion state item itemID carries, and records which
// states it cleared, why and by whom, in the caller's transaction. It is the inverse of
// the verdicts a landing recorded, for the one case where what they judged is gone from
// the trunk: an automatic revert of that landing (the drain's heavy_revert.go). With the
// states cleared the item is exactly as amendable and dispatchable as before it landed:
// sync's definition freeze (judgedCriterion) reads stored states, and a later
// --require-verdicts move counts only verdicts recorded after the withdrawal.
//
// An item with no criterion state is answered with nothing withdrawn and appends no
// event, so a repeated withdrawal records nothing twice.
func WithdrawVerdicts(ctx context.Context, tx *store.Tx, itemID, reason, by string, at time.Time) ([]WithdrawnCriterion, error) {
	if strings.TrimSpace(reason) == "" || strings.TrimSpace(by) == "" {
		return nil, Usagef("withdrawing the verdicts of item %q needs a reason and an actor", itemID)
	}
	if _, _, err := readStoredItem(tx.Reader(), itemID); err != nil {
		return nil, err
	}
	rows, err := tx.Query(`SELECT criterion_id, state FROM item_criteria WHERE item_id = ? AND state != '' ORDER BY criterion_id`, itemID)
	if err != nil {
		return nil, Internalf("reading the criterion states of item %q: %v", itemID, err)
	}
	var cleared []WithdrawnCriterion
	for rows.Next() {
		var c WithdrawnCriterion
		if err := rows.Scan(&c.CriterionID, &c.State); err != nil {
			rows.Close()
			return nil, Internalf("reading the criterion states of item %q: %v", itemID, err)
		}
		cleared = append(cleared, c)
	}
	if err := rows.Close(); err != nil {
		return nil, Internalf("reading the criterion states of item %q: %v", itemID, err)
	}
	if len(cleared) == 0 {
		return nil, nil
	}
	if _, err := tx.Exec(`UPDATE item_criteria SET state = '' WHERE item_id = ?`, itemID); err != nil {
		return nil, Internalf("withdrawing the verdicts of item %q: %v", itemID, err)
	}
	body, err := json.Marshal(withdrawnPayload{ItemID: itemID, Criteria: cleared, Reason: reason, By: by})
	if err != nil {
		return nil, Internalf("encoding the %s event: %v", VerdictsWithdrawnKind, err)
	}
	id := itemID
	if _, err := tx.Append(event.Event{Kind: VerdictsWithdrawnKind, ObjectType: "item", ObjectID: &id, Payload: body, At: at}); err != nil {
		return nil, Internalf("appending the %s event: %v", VerdictsWithdrawnKind, err)
	}
	return cleared, nil
}
