package main

import (
	"encoding/json"
	"github.com/mschulkind-oss/tillr/store"
)

// The view reads the conflict event without importing its writer or migrating a store.
type waitingConflict struct {
	ConflictID string `json:"conflict_id"`
	ItemID     string `json:"item_id"`
	Text       string `json:"text"`
	WaitingOn  string `json:"waiting_on"`
	Status     string `json:"status"`
	Since      int64
}

func waitingConflicts(r *store.Reader) (map[string]waitingConflict, error) {
	rows, err := r.Query(`SELECT ts, payload FROM events WHERE kind = 'item.conflict' ORDER BY seq`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := map[string]waitingConflict{}
	for rows.Next() {
		var c waitingConflict
		var raw []byte
		if err := rows.Scan(&c.Since, &raw); err != nil {
			return nil, err
		}
		if err := json.Unmarshal(raw, &c); err != nil {
			return nil, err
		}
		out[c.ItemID] = c
	}
	return out, rows.Err()
}
