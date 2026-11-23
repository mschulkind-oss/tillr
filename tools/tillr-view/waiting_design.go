// Architecture and invariants: docs/reference/tillr-queue-persistence.md
package main

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// designRoadmapScope is the roadmaps the repository's settings select for the sync, as the
// slash-separated repository-relative paths the store records. Nil means unrestricted.
func designRoadmapScope(root string) ([]string, error) {
	read, err := settings.Read(root)
	if err != nil {
		return nil, err
	}
	var scope []string
	for _, path := range read.Sync.Roadmaps {
		if filepath.IsAbs(path) {
			if rel, err := filepath.Rel(root, path); err == nil {
				path = rel
			}
		}
		scope = append(scope, filepath.ToSlash(filepath.Clean(path)))
	}
	return scope, nil
}

// Sync owns the document grammar and these observations. Waiting only reads
// the optional table; it never rescans documents or invents an item for them.
//
// scope names the roadmaps the drain works (settings sync.roadmaps, read by
// designRoadmapScope); a design document from any other roadmap is not shown. A nil scope
// is every roadmap, which is what an unrestricted sync reads.
func waitingDesignWithoutItems(r *store.Reader, scope []string) ([]waitingRow, error) {
	query := `SELECT document,roadmap,roadmap_line,first_seen FROM design_without_item`
	var args []any
	if len(scope) > 0 {
		marks := make([]string, len(scope))
		for i, path := range scope {
			marks[i] = "?"
			args = append(args, path)
		}
		query += ` WHERE roadmap IN (` + strings.Join(marks, ",") + `)`
	}
	rows, err := r.Query(query+` ORDER BY document`, args...)
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
	designs, err := waitingDesignWithoutItems(r, nil)
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
		if (rows[i].Class == "alarm") != (rows[j].Class == "alarm") {
			return rows[i].Class == "alarm"
		}
		if rows[i].Document == "" {
			return rows[j].Document != ""
		}
		if rows[j].Document == "" {
			return false
		}
		return rows[i].Document < rows[j].Document
	})
}
