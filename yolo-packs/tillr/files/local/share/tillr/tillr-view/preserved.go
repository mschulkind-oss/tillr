package main

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/preserved"
	"github.com/mschulkind-oss/tillr/store"
)

// addPreservedHistory keeps the planner's reason and adds the recovery evidence
// to the same row, so CLI, HTTP and browser waiting views all expose it.
func addPreservedHistory(r *store.Reader, rows []waitingRow) error {
	if len(rows) == 0 {
		return nil
	}
	seenItem := map[string]bool{}
	var itemIDs []string
	var extraRunIDs []string
	for _, row := range rows {
		if row.ItemID != "" && !seenItem[row.ItemID] {
			seenItem[row.ItemID] = true
			itemIDs = append(itemIDs, row.ItemID)
		}
		if row.RunID != "" {
			extraRunIDs = append(extraRunIDs, row.RunID)
		}
	}
	saved, err := readPreservedWorkForItems(r, itemIDs, extraRunIDs)
	if err != nil {
		return err
	}
	for i := range rows {
		for _, p := range saved {
			if p.ItemID == rows[i].ItemID && !p.Pruned {
				rows[i].Why += fmt.Sprintf("; earlier work exists at %s: %s (stopped: %s)", p.Ref, p.Summary, p.Reason)
				if p.IndexRef != "" {
					rows[i].Why += "; staged contents at " + p.IndexRef
				}
			}
		}
	}
	return nil
}

func readPreservedWorkForItems(r *store.Reader, itemIDs, extraRunIDs []string) ([]preserved.PreservedWork, error) {
	seenRun := map[string]bool{}
	var runIDs []string
	addRun := func(id string) {
		if id != "" && !seenRun[id] {
			seenRun[id] = true
			runIDs = append(runIDs, id)
		}
	}
	for _, id := range extraRunIDs {
		addRun(id)
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
		for _, query := range []string{
			`SELECT run_id FROM runs WHERE item_id IN (` + placeholders + `)`,
			`SELECT run_id FROM dispatches WHERE item_id IN (` + placeholders + `) AND run_id IS NOT NULL AND run_id != ''`,
		} {
			qRows, err := r.Query(query, args...)
			if isMissingTable(err) {
				continue
			}
			if err != nil {
				return nil, err
			}
			for qRows.Next() {
				var id string
				if err := qRows.Scan(&id); err != nil {
					qRows.Close()
					return nil, err
				}
				addRun(id)
			}
			if err := qRows.Err(); err != nil {
				qRows.Close()
				return nil, err
			}
			qRows.Close()
		}
	}
	if len(runIDs) == 0 {
		return nil, nil
	}
	events, err := r.EventsForObjects(0, runIDs, preserved.Kind, preserved.PrunedKind)
	if isMissingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	out := []preserved.PreservedWork{}
	for _, e := range events {
		switch e.Kind {
		case preserved.Kind:
			var p preserved.PreservedWork
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, err
			}
			out = append(out, p)
		case preserved.PrunedKind:
			var p preserved.PreservedWork
			if err := json.Unmarshal(e.Payload, &p); err != nil {
				return nil, err
			}
			for i := range out {
				if out[i].Ref == p.Ref && out[i].Repository == p.Repository {
					out[i].Pruned = true
				}
			}
		}
	}
	return out, nil
}
