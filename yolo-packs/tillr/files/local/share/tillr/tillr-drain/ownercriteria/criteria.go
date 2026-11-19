package ownercriteria

import (
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// OwnerCriterion identifies the first unfinished owner-only criterion in an item's
// criterion-id order. Its text explains which ruling the owner still owes.
type OwnerCriterion struct {
	CriterionID string
	Text        string
}

// Explanation names the criterion and only the first line of its text.
func (c *OwnerCriterion) Explanation() string {
	first, _, _ := strings.Cut(c.Text, "\n")
	return "owner-only-owed: " + c.CriterionID + ": " + strings.TrimSpace(first)
}

// Mover names the owner and the criterion that needs a ruling.
func (c *OwnerCriterion) Mover() string { return "the owner; rule on " + c.CriterionID }

// ReadOwnerCriteria reads current criterion state, not the last planner decision:
// a human verdict must lift the hold immediately. Stores predating claim classes
// have no owner-only criteria; other read failures are returned to the caller.
func ReadOwnerCriteria(r *store.Reader) (map[string]*OwnerCriterion, error) {
	out := map[string]*OwnerCriterion{}
	var hasClass bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('item_criteria') WHERE name='claim_class')`).Scan(&hasClass); err != nil {
		return nil, err
	}
	if !hasClass {
		return out, nil
	}
	rows, err := r.Query(`SELECT item_id, criterion_id, text FROM item_criteria WHERE claim_class='owner-only' AND state <> 'done' ORDER BY item_id, criterion_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	for rows.Next() {
		var id string
		c := new(OwnerCriterion)
		if err := rows.Scan(&id, &c.CriterionID, &c.Text); err != nil {
			return nil, err
		}
		if out[id] == nil {
			out[id] = c
		}
	}
	return out, rows.Err()
}
