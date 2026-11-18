// Package identityhistory reads trunk identity holds without registering lander tables.
package identityhistory

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"strings"
)

const ReasonForeignIdentity = "trunk-foreign-identity"

// ForeignCommit records both raw identities (not mailmap substitutions).
type ForeignCommit struct {
	SHA       string `json:"sha"`
	Author    string `json:"author"`
	Committer string `json:"committer"`
}

// IdentityHistory is the checked tip and the unanswered commits still reachable from it.
type IdentityHistory struct {
	Tip     string
	Pending []ForeignCommit
}

func (h IdentityHistory) Reason() string {
	var details []string
	for _, c := range h.Pending {
		details = append(details, fmt.Sprintf("%s author %s; committer %s", c.SHA, c.Author, c.Committer))
	}
	return ReasonForeignIdentity + ": " + strings.Join(details, "; ")
}

// ReadIdentityHistory tolerates old read-only stores that predate this check.
func ReadIdentityHistory(r interface{ QueryRow(string, ...any) *sql.Row }) (IdentityHistory, error) {
	var h IdentityHistory
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE name='trunk_identity' AND type='table')`).Scan(&exists); err != nil {
		return h, err
	}
	if !exists {
		return h, nil
	}
	var raw string
	err := r.QueryRow(`SELECT tip,pending FROM trunk_identity WHERE id=1`).Scan(&h.Tip, &raw)
	if err == sql.ErrNoRows {
		return h, nil
	}
	if err != nil {
		return h, err
	}
	return h, json.Unmarshal([]byte(raw), &h.Pending)
}
