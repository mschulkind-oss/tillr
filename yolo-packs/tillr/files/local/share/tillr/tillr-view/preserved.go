package main

import (
	"fmt"

	"github.com/mschulkind-oss/tillr-drain/preserved"
	"github.com/mschulkind-oss/tillr/store"
)

// addPreservedHistory keeps the planner's reason and adds the recovery evidence
// to the same row, so CLI, HTTP and browser waiting views all expose it.
func addPreservedHistory(r *store.Reader, rows []waitingRow) error {
	saved, err := preserved.ReadPreservedWork(r)
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
