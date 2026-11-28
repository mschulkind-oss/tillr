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

// ReadOwnerCriteriaForItem reads the first unfinished owner-only criterion for one item.
func ReadOwnerCriteriaForItem(r *store.Reader, itemID string) (*OwnerCriterion, error) {
	var hasClass bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('item_criteria') WHERE name='claim_class')`).Scan(&hasClass); err != nil || !hasClass {
		return nil, err
	}
	rows, err := r.Query(`SELECT criterion_id, text FROM item_criteria WHERE item_id=? AND claim_class='owner-only' AND state <> 'done' ORDER BY criterion_id LIMIT 1`, itemID)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	if !rows.Next() {
		return nil, rows.Err()
	}
	var c OwnerCriterion
	if err := rows.Scan(&c.CriterionID, &c.Text); err != nil {
		return nil, err
	}
	return &c, nil
}

// ReadOwnerCriteriaForItems reads the first unfinished owner-only criterion for each named item.
func ReadOwnerCriteriaForItems(r *store.Reader, itemIDs []string) (map[string]*OwnerCriterion, error) {
	out := map[string]*OwnerCriterion{}
	if len(itemIDs) == 0 {
		return out, nil
	}
	var hasClass bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('item_criteria') WHERE name='claim_class')`).Scan(&hasClass); err != nil || !hasClass {
		return out, err
	}
	const chunk = 400
	for start := 0; start < len(itemIDs); start += chunk {
		end := start + chunk
		if end > len(itemIDs) {
			end = len(itemIDs)
		}
		batch := itemIDs[start:end]
		args := make([]any, len(batch))
		for i, id := range batch {
			args[i] = id
		}
		placeholders := strings.TrimSuffix(strings.Repeat("?,", len(batch)), ",")
		rows, err := r.Query(`SELECT item_id, criterion_id, text FROM item_criteria WHERE item_id IN (`+placeholders+`) AND claim_class='owner-only' AND state <> 'done' ORDER BY item_id, criterion_id`, args...)
		if err != nil {
			return nil, err
		}
		for rows.Next() {
			var id string
			c := new(OwnerCriterion)
			if err := rows.Scan(&id, &c.CriterionID, &c.Text); err != nil {
				rows.Close()
				return nil, err
			}
			if out[id] == nil {
				out[id] = c
			}
		}
		if err := rows.Err(); err != nil {
			rows.Close()
			return nil, err
		}
		rows.Close()
	}
	return out, nil
}
