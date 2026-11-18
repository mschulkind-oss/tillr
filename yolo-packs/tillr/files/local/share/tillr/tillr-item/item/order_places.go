package item

import (
	"context"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// InOrder is the place command's membership rule: parked and finished items have no place.
func InOrder(state string) bool {
	return state != "not-started" && state != "landed" && state != "closed" && state != "resolved-elsewhere"
}

// OrderPlaces counts the full order, including empty keys at the end in item id order.
// It is read-only; sync, not opening a store, supplies missing keys.
func OrderPlaces(r *store.Reader) (map[string]int, error) {
	rows, err := r.Query(`SELECT item_id,state FROM items ORDER BY CASE WHEN rank='' THEN 1 ELSE 0 END,rank,item_id`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	places := map[string]int{}
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			return nil, err
		}
		if InOrder(state) {
			places[id] = len(places) + 1
		}
	}
	return places, rows.Err()
}

// keyMissingOrder appends one item update per missing key, in the sync transaction.
// Existing keys are never rewritten, including those outside the order.
func keyMissingOrder(ctx context.Context, tx *store.Tx, at time.Time) ([]string, error) {
	rows, err := tx.Reader().Query(`SELECT item_id,rank,state FROM items ORDER BY CASE WHEN rank='' THEN 1 ELSE 0 END,rank,item_id`)
	if err != nil {
		return nil, err
	}
	missing := []string{}
	last := ""
	for rows.Next() {
		var id, key, state string
		if err := rows.Scan(&id, &key, &state); err != nil {
			rows.Close()
			return nil, err
		}
		if key != "" {
			last = key
		}
		if InOrder(state) && key == "" {
			missing = append(missing, id)
		}
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	keys := appendKeys(last, len(missing))
	for i, id := range missing {
		if _, err := SetRank(ctx, tx, id, keys[i], at); err != nil {
			return nil, err
		}
	}
	return missing, nil
}
