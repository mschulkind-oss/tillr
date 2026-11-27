package item

import (
	"context"
	"encoding/json"
	"regexp"
	"slices"

	"github.com/mschulkind-oss/tillr/store"
)

// EvaluatorBinding identifies the evaluator in the exact persisted expanded graph.
// GraphSHA256 hashes stored bytes, not the source definition or its sha256 field.
type EvaluatorBinding struct {
	Workflow      string `json:"workflow,omitempty"`
	EvaluatorNode string `json:"evaluator_node,omitempty"`
	GraphSHA256   string `json:"graph_sha256,omitempty"`
}

type evaluatorGraph struct {
	Stages []struct {
		ID           string   `json:"id"`
		Kind         string   `json:"kind"`
		Access       string   `json:"access"`
		JudgeClasses []string `json:"judge_classes"`
	} `json:"stages"`
	Exit struct {
		EvaluatorSlot string   `json:"evaluator_slot"`
		RequiresNodes []string `json:"requires_nodes"`
	} `json:"exit"`
}

// ReadEvaluatorBinding derives caller-owned provenance from the selected graph.
// It never selects, upgrades, or repairs a workflow as a side effect of intake.
func ReadEvaluatorBinding(r *store.Reader, itemID string) (EvaluatorBinding, error) {
	binding, _, err := readEvaluator(r, itemID)
	return binding, err
}

func readEvaluator(r *store.Reader, id string) (EvaluatorBinding, []string, error) {
	workflow, _, raw, err := ReadWorkflowSelection(r, id)
	if err != nil {
		return EvaluatorBinding{}, nil, Internalf("reading evaluator selection: %v", err)
	}
	reject := func(reason string) (EvaluatorBinding, []string, error) {
		return EvaluatorBinding{}, nil, Rejectionf("evaluator-selection", id, "workflow %q: %s", workflow, reason)
	}
	// A reviewed replacement may change the expanded graph for this attempt.
	// The selection retains its pinned base for future attempts.
	var revised bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM pragma_table_info('workflow_selections') WHERE name='judge_graph')`).Scan(&revised); err != nil {
		return EvaluatorBinding{}, nil, Internalf("reading evaluator graph columns: %v", err)
	}
	if revised && workflow != "" {
		if err := r.QueryRow(`SELECT COALESCE(NULLIF(judge_graph,''),graph) FROM workflow_selections WHERE item_id=?`, id).Scan(&raw); err != nil {
			return EvaluatorBinding{}, nil, Internalf("reading effective evaluator graph: %v", err)
		}
	}
	if workflow == "" || raw == "" {
		return reject("no persisted judging step; select a workflow with an evaluator")
	}
	var graph evaluatorGraph
	if err := json.Unmarshal([]byte(raw), &graph); err != nil {
		return reject("persisted graph is invalid JSON")
	}
	slot := graph.Exit.EvaluatorSlot
	if slot == "" || !slices.Contains(graph.Exit.RequiresNodes, slot) {
		return reject("evaluator must be named and required")
	}
	var classes []string
	matches := 0
	for _, stage := range graph.Stages {
		if stage.ID != slot {
			continue
		}
		matches++
		if stage.Kind != "agent" || stage.Access != "read_only" {
			return reject("evaluator must be a read-only agent")
		}
		classes = stage.JudgeClasses
	}
	if matches != 1 || len(classes) == 0 {
		return reject("evaluator must be unique and declare judge_classes")
	}
	seen := map[string]bool{}
	for _, class := range classes {
		if class != ClaimVerification && class != ClaimRedGateTriage && class != ClaimDirection || seen[class] {
			return reject("evaluator has incompatible judge_classes")
		}
		seen[class] = true
	}
	return EvaluatorBinding{Workflow: workflow, EvaluatorNode: slot, GraphSHA256: CriterionHash(raw)}, classes, nil
}

var gitObjectSHA = regexp.MustCompile(`^(?:[0-9a-f]{40}|[0-9a-f]{64})$`)

// checkEvaluator is shared by complete judge intake and direct verdict writes.
// The caller resolves candidate objects; the store requires their exact identities.
func checkEvaluator(_ context.Context, r *store.Reader, v verdictPayload) error {
	if v.ActorKind != ActorAgent {
		return nil
	}
	var (
		binding EvaluatorBinding
		classes []string
		err     error
	)
	if v.EvaluatorBinding.Workflow == SplitClosingWorkflow {
		binding, err = SplitClosingEvaluatorBinding(r, v.ItemID)
		classes = []string{ClaimVerification, ClaimRedGateTriage, ClaimDirection}
	} else {
		binding, classes, err = readEvaluator(r, v.ItemID)
	}
	if err != nil {
		return err
	}
	if v.EvaluatorBinding != binding {
		return Rejectionf("evaluator-binding", v.ItemID, "workflow %q evaluator %q must match the exact persisted graph digest", binding.Workflow, binding.EvaluatorNode)
	}
	if !gitObjectSHA.MatchString(v.EvaluatedHead) {
		return Rejectionf("evaluated-head", v.ItemID, "agent verdict needs the full exact candidate head SHA")
	}
	if !gitObjectSHA.MatchString(v.EvaluatedTree) {
		return Rejectionf("evaluated-tree", v.ItemID, "agent verdict needs the full exact candidate tree SHA")
	}
	if !slices.Contains(classes, v.ClaimClass) {
		return Rejectionf("evaluator-class", v.ItemID, "workflow %q evaluator %q names classes %v, not stored class %q", binding.Workflow, binding.EvaluatorNode, classes, v.ClaimClass)
	}
	return nil
}
