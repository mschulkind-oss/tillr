package main

import (
	"fmt"
	"sort"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// Sync owns the document grammar and these observations. Waiting only reads
// the optional table; it never rescans documents or invents an item for them.
func waitingDesignWithoutItems(r *store.Reader) ([]waitingRow, error) {
	rows, err := r.Query(`SELECT document,roadmap,roadmap_line,first_seen FROM design_without_item ORDER BY document`)
	if isMissingTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []waitingRow
	for rows.Next() {
		var document, roadmap string
		var line int
		var since int64
		if err := rows.Scan(&document, &roadmap, &line, &since); err != nil {
			return nil, err
		}
		out = append(out, waitingRow{Document: document, Class: "needs you", Where: "queue", Why: fmt.Sprintf("design-without-item: %s:%d", roadmap, line), Mover: "owner: file the document's item block", Since: time.UnixMilli(since).UTC().Format(time.RFC3339Nano), ClassSource: "rule"})
	}
	return out, rows.Err()
}

func appendWaitingDesigns(r *store.Reader, rows []waitingRow) ([]waitingRow, error) {
	designs, err := waitingDesignWithoutItems(r)
	if err != nil {
		return nil, err
	}
	rows = append(rows, designs...)
	sortWaitingDocuments(rows)
	return rows, nil
}

func sortWaitingDocuments(rows []waitingRow) {
	// Keep the existing order for all non-document rows, including auxiliary rows.
	sort.SliceStable(rows, func(i, j int) bool {
		if rows[i].Document == "" {
			return rows[j].Document != ""
		}
		if rows[j].Document == "" {
			return false
		}
		return rows[i].Document < rows[j].Document
	})
}
