package main

import (
	"fmt"

	"github.com/mschulkind-oss/tillr/store"
)

// itemProgress is what an item's work amounts to, beside the bare attempt number. The
// attempt number is an ordinal the planner names the next run with; it is not a count of
// work done. These three are:
//
//   - RunsStarted: runs that started a worker, that is, runs the store holds a row for. A
//     dispatch that ended before a worker started (a lane refused staging, a claim was
//     cancelled) has no such row and is not counted.
//   - VerifyCycles: the attempts in which a node with the verify seat started a worker,
//     that is, the cycles that reached verify.
//   - VerifyRefusals: the times the loop refused a candidate because its verify step did
//     not finish (the drain.refusal event with step verify-not-done).
type itemProgress struct {
	RunsStarted    int `json:"runs_started"`
	VerifyCycles   int `json:"verify_cycles"`
	VerifyRefusals int `json:"verify_refusals"`
}

// Text is the progress as one operator-readable clause.
func (p itemProgress) Text() string {
	return fmt.Sprintf("%d runs started · %d reached verify · %d verify refusals", p.RunsStarted, p.VerifyCycles, p.VerifyRefusals)
}

// readItemProgress answers each item's progress. A table a store does not carry leaves
// its figure at zero rather than failing the read.
func readItemProgress(r *store.Reader) (map[string]itemProgress, error) {
	out := map[string]itemProgress{}
	fill := func(query string, set func(p *itemProgress, n int)) error {
		rows, err := r.Query(query)
		if err != nil {
			if isMissingTable(err) || isMissingColumn(err) {
				return nil
			}
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var id string
			var n int
			if err := rows.Scan(&id, &n); err != nil {
				return err
			}
			p := out[id]
			set(&p, n)
			out[id] = p
		}
		return rows.Err()
	}
	if err := fill(`SELECT item_id, COUNT(*) FROM runs WHERE item_id IS NOT NULL GROUP BY item_id`,
		func(p *itemProgress, n int) { p.RunsStarted = n }); err != nil {
		return nil, err
	}
	if err := fill(`SELECT n.item_id, COUNT(DISTINCT n.attempt) FROM node_runs n
		WHERE n.seat = 'verify' AND EXISTS (SELECT 1 FROM runs WHERE runs.run_id = n.run_id)
		GROUP BY n.item_id`,
		func(p *itemProgress, n int) { p.VerifyCycles = n }); err != nil {
		return nil, err
	}
	// A refusal is keyed by the run it refused, or by the item; the payload's own item id
	// is not always set, so the run's row names the item.
	if err := fill(`SELECT COALESCE(runs.item_id, e.object_id), COUNT(*) FROM events e
		LEFT JOIN runs ON e.object_type = 'run' AND runs.run_id = e.object_id
		WHERE e.kind = 'drain.refusal' AND json_extract(e.payload, '$.step') = 'verify-not-done'
		  AND COALESCE(runs.item_id, e.object_id) IS NOT NULL
		GROUP BY COALESCE(runs.item_id, e.object_id)`,
		func(p *itemProgress, n int) { p.VerifyRefusals = n }); err != nil {
		return nil, err
	}
	return out, nil
}
