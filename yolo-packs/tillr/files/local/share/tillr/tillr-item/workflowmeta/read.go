// Package workflowmeta reads workflow selections without registering any schema.
// Observatory readers must remain safe to link when opening legacy stores.
package workflowmeta

import (
	"database/sql"
	"errors"

	"github.com/mschulkind-oss/tillr/store"
)

// ReadSelection reads the workflow reference, its source, and the graph stored
// by drain at first dispatch. Sources are block (explicit item selection),
// default (seat setting), implicit (no selection), or repair (single fixer).
// Missing tables or rows return no selection without migrating the store.
func ReadSelection(r *store.Reader, id string) (workflow, source, graph string, err error) {
	var exists bool
	if err = r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='workflow_selections')`).Scan(&exists); err != nil || !exists {
		return
	}
	err = r.QueryRow(`SELECT workflow,source,graph FROM workflow_selections WHERE item_id=?`, id).Scan(&workflow, &source, &graph)
	if errors.Is(err, sql.ErrNoRows) {
		err = nil
	}
	return
}
