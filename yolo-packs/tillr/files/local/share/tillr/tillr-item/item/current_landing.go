package item

import (
	"database/sql"
	"errors"

	"github.com/mschulkind-oss/tillr/store"
)

func landingTables(r *store.Reader) (landings, rewrites bool, err error) {
	err = r.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='landings'), EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='trunk_rewrites')`).Scan(&landings, &rewrites)
	return
}

// FinishExistingLandingAction identifies the operator's explicit disposition in item.update.
const FinishExistingLandingAction = "finish-existing-landing"

func hasCurrentLanding(r *store.Reader, id string) (bool, error) {
	query, err := currentLandingQuery(r)
	if err != nil || query == "" {
		return false, err
	}
	var found bool
	err = r.QueryRow(`SELECT EXISTS (SELECT 1 `+query+`)`, id).Scan(&found)
	return found, err
}

// LandingReference identifies an original landing receipt, not the operator's
// evidence for disposition of the remaining work.
type LandingReference struct {
	LandingID  string `json:"landing_id"`
	RunID      string `json:"run_id"`
	TrunkAfter string `json:"trunk_after"`
}

// CurrentLanding probes only this item's rows and the corresponding commits'
// receipts. Close runs under the store write lock, so unrelated history must not
// make it slower. Nil means there is no unreverted landing.
func CurrentLanding(r *store.Reader, id string) (*LandingReference, error) {
	query, err := currentLandingQuery(r)
	if err != nil || query == "" {
		return nil, err
	}
	var receipt LandingReference
	err = r.QueryRow(`SELECT l.landing_id, l.run_id, l.trunk_after `+query+
		` ORDER BY l.event_seq DESC, l.landing_id DESC LIMIT 1`, id).Scan(&receipt.LandingID, &receipt.RunID, &receipt.TrunkAfter)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return &receipt, nil
}

func currentLandingQuery(r *store.Reader) (string, error) {
	hasLandings, hasRewrites, err := landingTables(r)
	if err != nil || !hasLandings {
		return "", err
	}
	commitMatch := `e.object_id = l.trunk_after`
	if hasRewrites {
		// Walk forward from the landing to the aliases a later revert could name.
		commitMatch = `e.object_id IN (
			WITH RECURSIVE aliases(sha) AS (
				SELECT l.trunk_after
				UNION SELECT w.new_sha FROM trunk_rewrites w JOIN aliases a ON w.old_sha = a.sha
			) SELECT sha FROM aliases
		)`
	}
	// Unary + removes column affinity so a numeric JSON field cannot match a text ID.
	return `FROM landings l WHERE l.item_id = ? AND l.result = 'landed'
		AND NOT EXISTS (
			SELECT 1 FROM events e INDEXED BY events_by_object WHERE ` + commitMatch + `
			AND e.kind = 'drain.revert' AND e.object_type = 'landing' AND e.object_id <> ''
			AND json_extract(e.payload, '$.result') = 'reverted'
			AND json_extract(e.payload, '$.landing') = +e.object_id
			AND json_extract(e.payload, '$.item_id') = +l.item_id AND l.item_id <> ''
			AND json_extract(e.payload, '$.run_id') = +l.run_id AND l.run_id <> ''
		)`, nil
}
