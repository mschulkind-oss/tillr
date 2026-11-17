package main

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// This read mirrors drain's live planner presentation for history/API reads.
// Generated trunk-green criteria carry their check list as `(red: <ids>)`.
func waitingTrunkFixer(r *store.Reader, id string, conflicts map[string]waitingConflict) (string, bool, error) {
	var text string
	err := r.QueryRow(`SELECT text FROM item_criteria WHERE item_id=? AND criterion_id='trunk-green'`, id).Scan(&text)
	if err != nil && !errors.Is(err, sql.ErrNoRows) && !isMissingTable(err) {
		return "", false, err
	}
	checks := "check not recorded"
	if _, rest, ok := strings.Cut(text, "(red: "); ok {
		if list, _, ok := strings.Cut(rest, ")"); ok && list != "" {
			checks = "check: " + list
		}
	}
	c := conflicts[id]
	owner := c.WaitingOn == "owner" && c.Status != "closed" && c.Status != "answered"
	mover := "waiting on repair (not waiting on owner)"
	if owner {
		mover = "waiting on owner: " + strings.Join(strings.Fields(c.Text), " ")
	}
	return id + "; " + checks + "; " + mover, owner, nil
}
