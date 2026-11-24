package drain

import (
	"encoding/json"
	"fmt"
	"os/exec"
	"path/filepath"
	"slices"
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

type storedWorkflowSelection struct {
	selected string
	source   string
	graph    string
}

func selectWorkflow(r *store.Reader, root string, row itemRow, defaults map[string]string, policy *Policy) (*Workflow, string, string, error) {
	// Generated repairs and rebase requests already specify their work and delivery.
	if IsGeneratedRepair(row.ItemID) || isForkRebaseItem(row.ItemID) {
		return nil, singleWorkerWorkflow, "repair", nil
	}
	selected, source, graph, err := item.ReadWorkflowSelection(r, row.ItemID)
	if err != nil {
		return nil, "", "", err
	}
	return selectWorkflowFromStored(storedWorkflowSelection{selected: selected, source: source, graph: graph}, root, row, defaults, policy)
}

func selectWorkflowFromStored(stored storedWorkflowSelection, root string, row itemRow, defaults map[string]string, policy *Policy) (*Workflow, string, string, error) {
	if IsGeneratedRepair(row.ItemID) || isForkRebaseItem(row.ItemID) {
		return nil, singleWorkerWorkflow, "repair", nil
	}
	selected, source, graph := stored.selected, stored.source, stored.graph
	var err error
	if source != "" {
		if graph == "" {
			return nil, selected, source, nil
		}
		var w Workflow
		if err := json.Unmarshal([]byte(graph), &w); err != nil {
			return nil, selected, source, fmt.Errorf("stored workflow graph: %w", err)
		}
		// The compatibility step uses the existing post-worker review transport;
		// the graph runner must not dispatch it a second time.
		if review := compatibilityReviewSlot(graph); review != "" && evaluatorSlot(&w) == review {
			w.Stages = slices.DeleteFunc(w.Stages, func(s WorkflowStage) bool { return s.ID == review })
			w.Edges = slices.DeleteFunc(w.Edges, func(e WorkflowEdge) bool { return e.To == review })
			w.Exit.RequiresNodes = slices.DeleteFunc(w.Exit.RequiresNodes, func(s string) bool { return s == review })
			w.Exit.EvaluatorSlot = ""
			if len(w.Stages) == 0 && selected == singleWorkerWorkflow {
				return nil, selected, source, nil
			}
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
		w, err = loadWorkflowWith(root, selected, policy, row.WorkflowArgs)
	}
	return w, selected, source, err
}

func compatibilityReviewSlot(graph string) string {
	var marker struct {
		Review string `json:"compatibility_review"`
	}
	if json.Unmarshal([]byte(graph), &marker) != nil {
		return ""
	}
	return marker.Review
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
	var mode string
	if err := tx.QueryRow(`SELECT judge_mode FROM items WHERE item_id=?`, d.ItemID).Scan(&mode); err != nil {
		return err
	}
	if mode == "agent" {
		graph = store.CompatibilityJudgeGraph(d.SelectedWorkflow, graph)
	}
	revised := ""
	if d.Workflow != nil && d.Workflow.Revision > 0 {
		revised = graph
	}
	_, err := tx.Exec(`INSERT INTO workflow_selections(item_id,workflow,source,graph,judge_graph,judge_attempt) VALUES(?,?,?,?,?,?)
ON CONFLICT(item_id) DO UPDATE SET judge_graph=excluded.judge_graph,judge_attempt=excluded.judge_attempt
WHERE workflow_selections.judge_attempt <> excluded.judge_attempt AND (workflow_selections.judge_graph <> '' OR excluded.judge_graph <> '')`,
		d.ItemID, d.SelectedWorkflow, d.WorkflowSource, graph, revised, d.Attempt)
	return err
}
