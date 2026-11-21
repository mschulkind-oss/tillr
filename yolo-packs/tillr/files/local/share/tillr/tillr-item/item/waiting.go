package item

import (
	"encoding/json"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

// WaitingOn records who must act and why an item is blocked. Waiter is owner,
// design, plan, or item:<id> for an item held by the same store.
type WaitingOn struct {
	Waiter string `json:"waiter"`
	Reason string `json:"reason"`
	// Source distinguishes a state write from a conflict-owned wait. Empty is legacy.
	Source string `json:"source,omitempty"`
}

// ReadWaitingOn reads blocked metadata without migrating a legacy store. Missing
// columns mean the old writer never recorded it, not a failed new state write.
func ReadWaitingOn(r *store.Reader, id string) (*WaitingOn, string, error) {
	var raw, underlying string
	err := r.QueryRow(`SELECT waiting_on, underlying_state FROM items WHERE item_id = ?`, id).Scan(&raw, &underlying)
	if err != nil && strings.Contains(err.Error(), "no such column") {
		return nil, "", nil
	}
	if err != nil {
		return nil, "", err
	}
	if raw == "" {
		return nil, underlying, nil
	}
	var waiting WaitingOn
	if err := json.Unmarshal([]byte(raw), &waiting); err != nil {
		return nil, "", Internalf("reading blocked metadata for %q: %v", id, err)
	}
	return &waiting, underlying, nil
}

func checkWaitingOn(r *store.Reader, id string, waiting WaitingOn) error {
	if strings.TrimSpace(waiting.Waiter) == "" || strings.TrimSpace(waiting.Reason) == "" {
		return Rejectionf("blocked-waiter-missing", id, "blocking item %q needs both --waiter and --reason", id)
	}
	switch waiting.Waiter {
	case "owner", "design", "plan":
		return nil
	}
	if strings.HasPrefix(waiting.Waiter, "item:") {
		exists, err := itemExists(nil, r, strings.TrimPrefix(waiting.Waiter, "item:"))
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
	}
	return Rejectionf("blocked-waiter-invalid", id, "waiter %q must be owner, design, plan, or item:<id> for a stored item", waiting.Waiter)
}
