package item

import (
	"github.com/mschulkind-oss/tillr-item/workflowmeta"
	"github.com/mschulkind-oss/tillr/store"
)

// ReadWorkflowSelection reads drain's stored reference, source, and graph.
// The reader is shared with the observatory through a package that registers
// no schema; linking a read-only utility must not migrate a legacy store.
func ReadWorkflowSelection(r *store.Reader, id string) (workflow, source, graph string, err error) {
	return workflowmeta.ReadSelection(r, id)
}
