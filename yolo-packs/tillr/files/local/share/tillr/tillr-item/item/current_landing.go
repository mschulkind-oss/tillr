package item

import "github.com/mschulkind-oss/tillr/store"

func landingTables(r *store.Reader) (landings, rewrites bool, err error) {
	err = r.QueryRow(`SELECT EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='landings'), EXISTS (SELECT 1 FROM sqlite_master WHERE type='table' AND name='trunk_rewrites')`).Scan(&landings, &rewrites)
	return
}

// hasCurrentLanding probes only this item's rows and the corresponding commits'
// receipts. Close runs under the store write lock, so unrelated history must not
// make it slower.
func hasCurrentLanding(r *store.Reader, id string) (bool, error) {
	hasLandings, hasRewrites, err := landingTables(r)
	if err != nil || !hasLandings {
		return false, err
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
	var landed bool
	err = r.QueryRow(`SELECT EXISTS (
		SELECT 1 FROM landings l WHERE l.item_id = ? AND l.result = 'landed'
		AND NOT EXISTS (
			SELECT 1 FROM events e INDEXED BY events_by_object WHERE `+commitMatch+`
			AND e.kind = 'drain.revert' AND e.object_type = 'landing' AND e.object_id <> ''
			AND json_extract(e.payload, '$.result') = 'reverted'
			AND json_extract(e.payload, '$.landing') = +e.object_id
			AND json_extract(e.payload, '$.item_id') = +l.item_id AND l.item_id <> ''
			AND json_extract(e.payload, '$.run_id') = +l.run_id AND l.run_id <> ''
		)
	)`, id).Scan(&landed)
	return landed, err
}
