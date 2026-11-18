package drain

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"sort"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

const singleWorkerWorkflow = "single-worker@v1"

// WorkflowDefaultDiagnostics validates all defaults at startup, even unused seats.
// Invalid defaults hold affected items rather than stopping unrelated seats.
func WorkflowDefaultDiagnostics(root string, defaults map[string]string, policy *Policy) []string {
	seats := make([]string, 0, len(defaults))
	for seat := range defaults {
		seats = append(seats, seat)
	}
	sort.Strings(seats)
	var lines []string
	for _, seat := range seats {
		if _, err := loadDefaultWorkflow(root, seat, defaults[seat], policy); err != nil {
			lines = append(lines, err.Error())
		}
	}
	return lines
}

func loadDefaultWorkflow(root, seat, ref string, policy *Policy) (*Workflow, error) {
	key := "workflows.default." + seat
	if !item.WorkflowRefValid(ref) {
		return nil, fmt.Errorf("%s: workflow-reference: %q", key, ref)
	}
	// The built-in opt-out has no file and is always a single worker.
	if ref == singleWorkerWorkflow {
		return nil, nil
	}
	path := filepath.Join("config", "tillr", "workflows", ref+".json")
	if err := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", path).Run(); err != nil {
		return nil, fmt.Errorf("%s: workflow-missing-reference: %s: no tracked workflow defines %s", key, path, ref)
	}
	w, err := loadWorkflow(root, ref, policy)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", key, err)
	}
	return w, nil
}

func selectWorkflow(r *store.Reader, root string, row itemRow, defaults map[string]string, policy *Policy) (*Workflow, string, string, error) {
	// Repair runs deliberately bypass workflow selection.
	if IsGeneratedRepair(row.ItemID) {
		return nil, singleWorkerWorkflow, "repair", nil
	}
	selected, source, graph, err := item.ReadWorkflowSelection(r, row.ItemID)
	if err != nil {
		return nil, "", "", err
	}
	if source != "" {
		if graph == "" {
			return nil, selected, source, nil
		}
		var w Workflow
		if err := json.Unmarshal([]byte(graph), &w); err != nil {
			return nil, selected, source, fmt.Errorf("stored workflow graph: %w", err)
		}
		return &w, selected, source, nil
	}
	selected, source = row.Workflow, "block"
	if selected == "" {
		selected, source = defaults[row.Seat], "default"
	}
	if selected == "" {
		return nil, singleWorkerWorkflow, "implicit", nil
	}
	if selected == singleWorkerWorkflow {
		return nil, selected, source, nil
	}
	var w *Workflow
	if source == "default" {
		w, err = loadDefaultWorkflow(root, row.Seat, selected, policy)
	} else {
		w, err = loadWorkflow(root, selected, policy)
	}
	return w, selected, source, err
}

func recordWorkflowSelection(tx *store.Tx, d Decision) error {
	if d.WorkflowSource == "" {
		return nil
	}
	graph := ""
	if d.Workflow != nil {
		raw, err := json.Marshal(d.Workflow)
		if err != nil {
			return err
		}
		graph = string(raw)
	}
	_, err := tx.Exec(`INSERT INTO workflow_selections(item_id,workflow,source,graph) VALUES(?,?,?,?) ON CONFLICT(item_id) DO NOTHING`, d.ItemID, d.SelectedWorkflow, d.WorkflowSource, graph)
	return err
}
