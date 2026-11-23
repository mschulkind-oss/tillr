package fixer

import (
	"database/sql"
	"errors"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

// FixerChecks reads the check list in generated trunk-green criteria. Older fixer
// items already carry this spelling, so unresolved repairs survive an upgrade.
func FixerChecks(text string) []string {
	_, rest, ok := strings.Cut(text, "(red: ")
	if !ok {
		return nil
	}
	list, _, ok := strings.Cut(rest, ")")
	if !ok {
		return nil
	}
	var checks []string
	for _, id := range strings.Split(list, ",") {
		if id = strings.TrimSpace(id); id != "" {
			checks = append(checks, id)
		}
	}
	return checks
}

// TrunkFixerWait names the repair, its checks and its next mover in both waiting
// faces. Owner conflicts preserve the complete report, including requested paths.
func TrunkFixerWait(r *store.Reader, id string) (string, bool, error) {
	var text string
	err := r.QueryRow(`SELECT text FROM item_criteria WHERE item_id=? AND criterion_id='trunk-green'`, id).Scan(&text)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return "", false, err
	}
	checks := "check not recorded"
	if ids := FixerChecks(text); len(ids) > 0 {
		checks = "check: " + strings.Join(ids, ", ")
	}
	conflicts, err := item.ReadConflicts(r)
	if err != nil {
		return "", false, err
	}
	c := conflicts[id]
	owner := c.WaitingOn == "owner" && c.Status != "closed" && c.Status != "answered"
	mover := "waiting on repair (not waiting on owner)"
	if owner {
		mover = "waiting on owner: " + strings.Join(strings.Fields(c.Text), " ")
	}
	return id + "; " + checks + "; " + mover, owner, nil
}
