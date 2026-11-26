package drain

import (
	"bytes"
	"crypto/sha256"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/workflow"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

const graphChangedKind = "drain.graph-changed"

type graphProposal struct {
	Target   string         `json:"target"`
	Workflow string         `json:"workflow"`
	Args     map[string]any `json:"args,omitempty"`
}

func workflowNodeRunID(d Decision, index int) string {
	id := attemptRunID(d.ItemID, d.Attempt) + "-n" + strconv.Itoa(index+1)
	if d.Workflow.Revision > 0 {
		sum := sha256.Sum256([]byte(d.Workflow.Stages[index].ID))
		id += fmt.Sprintf("-g%d-%x", d.Workflow.Revision, sum[:8])
	}
	return id
}

func inGraphGroup(id, target string) bool { return workflow.InGraphGroup(id, target) }

func proposalLater(w *Workflow, proposer, target string) bool {
	return workflow.ProposalLater(w, proposer, target)
}

// replaceProposalGraph changes only the not-yet-started target subtree. Boundary
// dependencies and file interfaces are projected onto the replacement's entries
// and required exit nodes, just as for static workflow expansion.
func replaceProposalGraph(w *Workflow, target string, inner *Workflow) (*Workflow, error) {
	raw, err := json.Marshal(w)
	if err != nil {
		return nil, err
	}
	var next Workflow
	if err := json.Unmarshal(raw, &next); err != nil {
		return nil, err
	}
	group, grouped := w.Groups[target]
	oldPorts := map[string]WorkflowPort{}
	original := group.Stage
	if grouped {
		oldPorts = group.Outputs
	} else {
		found := false
		for _, s := range w.Stages {
			if s.ID == target {
				original = s
				found = true
				for name := range s.Outputs {
					oldPorts[name] = WorkflowPort{Node: target, Output: name}
				}
			}
		}
		if !found {
			return nil, fmt.Errorf("proposal-target: target %s does not exist", target)
		}
	}
	if err := inheritWorkflowScope(inner, original.WriteScope); err != nil {
		return nil, fmt.Errorf("proposal-scope-outside-fence: %w", err)
	}
	ports := map[string]WorkflowPort{}
	for name, typ := range original.Outputs {
		port, ok := inner.Exit.Outputs[name]
		valid := false
		for _, node := range inner.Stages {
			if node.ID == port.Node && node.Outputs[port.Output] == typ {
				valid = true
			}
		}
		if !ok || !valid {
			return nil, fmt.Errorf("proposal-output-promise: replacement does not export %s of type %s", name, typ)
		}
		port.Node = target + "/" + port.Node
		ports[name] = port
	}
	inputs := map[string][]string{}
	incoming := map[string]bool{}
	for _, e := range inner.Edges {
		incoming[e.To] = true
	}
	entries := []string{}
	for _, node := range inner.Stages {
		if !incoming[node.ID] {
			entries = append(entries, target+"/"+node.ID)
		}
		for name, typ := range original.Inputs {
			if inner.Inputs[name] != typ {
				return nil, fmt.Errorf("proposal-input-promise: replacement does not accept %s", name)
			}
			if node.Inputs[name] != typ {
				continue
			}
			fed := false
			for _, e := range inner.Edges {
				if e.To == node.ID && e.ToInput == name {
					fed = true
				}
			}
			if !fed {
				inputs[name] = append(inputs[name], target+"/"+node.ID)
			}
		}
	}
	for name := range original.Inputs {
		if len(inputs[name]) == 0 {
			return nil, fmt.Errorf("proposal-input-promise: unused input %s", name)
		}
	}
	required := []string{}
	for _, id := range requiredWorkflowNodes(inner) {
		required = append(required, target+"/"+id)
	}
	prefixEdge := func(e WorkflowEdge) WorkflowEdge {
		e.From, e.To = target+"/"+e.From, target+"/"+e.To
		refs := []string{}
		for _, id := range e.RequiresPassed {
			refs = append(refs, target+"/"+id)
		}
		e.RequiresPassed = refs
		return e
	}
	edges := []WorkflowEdge{}
	for _, e := range inner.Edges {
		edges = append(edges, prefixEdge(e))
	}
	for _, e := range w.Edges {
		from, to := inGraphGroup(e.From, target), inGraphGroup(e.To, target)
		if from && to {
			continue
		}
		refs := []string{}
		replaced := false
		for _, id := range e.RequiresPassed {
			if inGraphGroup(id, target) {
				replaced = true
			} else {
				refs = append(refs, id)
			}
		}
		if replaced {
			refs = append(refs, required...)
		}
		e.RequiresPassed = refs
		if from {
			if e.WhenOutcome != "fail" {
				seen := map[string]bool{}
				for _, id := range e.RequiresPassed {
					seen[id] = true
				}
				for _, id := range required {
					if !seen[id] {
						e.RequiresPassed = append(e.RequiresPassed, id)
						seen[id] = true
					}
				}
			}
			if e.FromOutput != "" {
				name := ""
				for n, port := range oldPorts {
					if port.Node == e.From && port.Output == e.FromOutput {
						name = n
					}
				}
				port, ok := ports[name]
				if !ok {
					return nil, fmt.Errorf("proposal-output-promise: missing boundary output %s.%s", e.From, e.FromOutput)
				}
				e.From, e.FromOutput = port.Node, port.Output
				edges = append(edges, e)
			} else {
				for _, id := range required {
					edge := e
					edge.From = id
					edges = append(edges, edge)
				}
			}
		} else if to {
			if e.ToInput != "" {
				for _, id := range inputs[e.ToInput] {
					edge := e
					edge.To = id
					edges = append(edges, edge)
				}
			}
			// A file edge also orders every entry, not only the consuming node.
			for _, id := range entries {
				edge := e
				edge.To = id
				edge.FromOutput, edge.ToInput = "", ""
				edges = append(edges, edge)
			}
		} else {
			edges = append(edges, e)
		}
	}
	stages := []WorkflowStage{}
	inserted := false
	for _, s := range w.Stages {
		if !inGraphGroup(s.ID, target) {
			stages = append(stages, s)
			continue
		}
		if inserted {
			continue
		}
		inserted = true
		for _, node := range inner.Stages {
			node.ID = target + "/" + node.ID
			if node.CopiesOf != "" {
				node.CopiesOf = target + "/" + node.CopiesOf
			}
			if node.Parent == "" {
				node.Parent = target
			} else {
				node.Parent = target + "/" + node.Parent
			}
			allowed := map[string][]string{}
			for id, refs := range node.MayReplace {
				allowed[target+"/"+id] = refs
			}
			node.MayReplace = allowed
			stages = append(stages, node)
		}
	}
	// Expanded input fan-out contains one boundary edge per old consumer.
	// Projecting those onto new consumers can yield the same edge repeatedly.
	// Coalesce exact projections so each named input still has one producer.
	uniqueEdges := []WorkflowEdge{}
	seenEdges := map[string]bool{}
	for _, edge := range edges {
		raw, err := json.Marshal(edge)
		if err != nil {
			return nil, err
		}
		key := string(raw)
		if !seenEdges[key] {
			uniqueEdges = append(uniqueEdges, edge)
			seenEdges[key] = true
		}
	}
	next.Stages, next.Edges = stages, uniqueEdges
	if next.Groups == nil {
		next.Groups = map[string]WorkflowGroup{}
	}
	for id := range next.Groups {
		if inGraphGroup(id, target) {
			delete(next.Groups, id)
		}
	}
	// An enclosing group's exported ports must follow a replacement inside it.
	for id, g := range next.Groups {
		out := map[string]WorkflowPort{}
		for name, port := range g.Outputs {
			if inGraphGroup(port.Node, target) {
				found := false
				for n, old := range oldPorts {
					if old == port {
						port = ports[n]
						found = true
						break
					}
				}
				if !found {
					return nil, fmt.Errorf("proposal-output-promise: missing enclosing output %s.%s", id, name)
				}
			}
			out[name] = port
		}
		g.Outputs = out
		next.Groups[id] = g
	}
	next.Groups[target] = WorkflowGroup{Stage: original, Outputs: ports}
	for id, g := range inner.Groups {
		g.Stage.ID = target + "/" + g.Stage.ID
		out := map[string]WorkflowPort{}
		for name, p := range g.Outputs {
			p.Node = target + "/" + p.Node
			out[name] = p
		}
		g.Outputs = out
		next.Groups[target+"/"+id] = g
	}
	if existing := evaluatorSlot(w); existing != "" && inGraphGroup(existing, target) {
		return nil, fmt.Errorf("proposal-exit: cannot replace the selected evaluator %s", existing)
	}
	if next.Exit == nil {
		next.Exit = &WorkflowExit{RequiresNodes: requiredWorkflowNodes(w), RequiresCandidate: workflowCandidatePolicy(w)}
	}
	if evaluator := evaluatorSlot(inner); evaluator != "" {
		if existing := evaluatorSlot(&next); existing != "" && !inGraphGroup(existing, target) {
			return nil, fmt.Errorf("proposal-exit: workflow-evaluator-access: graph must have one evaluator run")
		}
		next.Exit.EvaluatorSlot = target + "/" + evaluator
	}
	keep := []string{}
	replaceExit := false
	for _, id := range next.Exit.RequiresNodes {
		if inGraphGroup(id, target) {
			replaceExit = true
		} else {
			keep = append(keep, id)
		}
	}
	if replaceExit {
		keep = append(keep, required...)
	}
	next.Exit.RequiresNodes = keep
	for name, port := range next.Exit.Outputs {
		if !inGraphGroup(port.Node, target) {
			continue
		}
		found := false
		for n, old := range oldPorts {
			if old == port {
				next.Exit.Outputs[name] = ports[n]
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("proposal-output-promise: missing graph exit %s", name)
		}
	}
	if next.Sources == nil {
		next.Sources = map[string]string{}
	}
	for ref, digest := range inner.Sources {
		next.Sources[ref] = digest
	}
	next.Revision++
	if err := validateEvaluatorJudgeClasses(&next); err != nil {
		return nil, fmt.Errorf("proposal-exit: %w", err)
	}
	if err := validateWorkflowExit(&next); err != nil {
		return nil, fmt.Errorf("proposal-exit: %w", err)
	}
	if err := validateOutputWiring(&next); err != nil {
		return nil, fmt.Errorf("proposal-output-promise: %w", err)
	}
	// A workflow file is checked before expansion; the boundary splice must also
	// be acyclic, including dependencies carried by expanded exit barriers.
	seen := map[string]bool{}
	for len(seen) < len(next.Stages) {
		progress := false
		for _, node := range next.Stages {
			if seen[node.ID] {
				continue
			}
			ready := true
			for _, e := range next.Edges {
				if e.To == node.ID {
					if !seen[e.From] {
						ready = false
					}
					for _, id := range e.RequiresPassed {
						if !seen[id] {
							ready = false
						}
					}
				}
			}
			if ready {
				seen[node.ID] = true
				progress = true
			}
		}
		if !progress {
			return nil, fmt.Errorf("proposal-cycle: replacement creates a cycle")
		}
	}
	return &next, nil
}

func (l *Loop) settleProposal(tx *store.Tx, d *Decision, artifacts []nodeArtifact) error {
	var proposal *nodeArtifact
	for i := range artifacts {
		if artifacts[i].Type == "graph.proposal" {
			if proposal != nil {
				return fmt.Errorf("proposal-not-allowed: at most one proposal")
			}
			proposal = &artifacts[i]
		}
	}
	if proposal == nil {
		return nil
	}
	stage := d.Workflow.Stages[d.NodeIndex]
	if len(stage.MayReplace) == 0 {
		return fmt.Errorf("proposal-not-allowed: %s", stage.ID)
	}
	var recorded, rule string
	err := tx.QueryRow(`SELECT after_graph,rule FROM graph_revisions WHERE proposing_run=?`, d.RunRequest.RunID).Scan(&recorded, &rule)
	if err == nil {
		if rule == "" {
			var w Workflow
			if err := json.Unmarshal([]byte(recorded), &w); err != nil {
				return err
			}
			d.Workflow = &w
		}
		return nil
	}
	if err != sql.ErrNoRows {
		return err
	}
	works, err := readPending(tx.Reader())
	if err != nil {
		return err
	}
	work := works[d.ItemID]
	if work != nil {
		if work.Decision.RunRequest == nil || work.Decision.RunRequest.RunID != d.RunRequest.RunID {
			return fmt.Errorf("proposal-owner: proposing run no longer owns graph")
		}
		// Validate against the snapshot owned by this settlement transaction, not a
		// stale caller copy. The proposer itself cannot have been replaced.
		if work.Decision.Workflow == nil || work.Decision.Workflow.Stages[work.Decision.NodeIndex].ID != stage.ID {
			return fmt.Errorf("proposal-owner: proposing node changed")
		}
		d.Workflow, d.NodeIndex = work.Decision.Workflow, work.Decision.NodeIndex
		stage = d.Workflow.Stages[d.NodeIndex]
	}
	before, err := json.Marshal(d.Workflow)
	if err != nil {
		return err
	}
	raw, err := os.ReadFile(proposal.Location)
	if err != nil {
		return err
	}
	p := graphProposal{}
	detail := ""
	refuse := func(r string, err error) { rule = r; detail = err.Error() }
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	decoder.UseNumber()
	if err := uniqueJSON(raw); err != nil {
		refuse("proposal-json", err)
	} else if err := decoder.Decode(&p); err != nil {
		refuse("proposal-json", err)
	} else if err := decoder.Decode(new(any)); err != io.EOF {
		refuse("proposal-json", fmt.Errorf("trailing content"))
	}
	var next *Workflow
	if rule == "" {
		if !proposalLater(d.Workflow, stage.ID, p.Target) {
			refuse("proposal-target-order", fmt.Errorf("%s is not after %s", p.Target, stage.ID))
		}
	}
	if rule == "" {
		rows, err := tx.Query(`SELECT node_id FROM node_runs WHERE item_id=? AND attempt=?`, d.ItemID, d.Attempt)
		if err != nil {
			return err
		}
		started := false
		for rows.Next() {
			var id string
			if err := rows.Scan(&id); err != nil {
				rows.Close()
				return err
			}
			if inGraphGroup(id, p.Target) {
				started = true
			}
		}
		err = rows.Err()
		rows.Close()
		if err != nil {
			return err
		}
		if started {
			refuse("proposal-target-started", fmt.Errorf("%s or a descendant has a run", p.Target))
		}
	}
	if rule == "" {
		allowed := false
		for _, ref := range stage.MayReplace[p.Target] {
			if ref == p.Workflow {
				allowed = true
			}
		}
		if !allowed || !item.WorkflowRefValid(p.Workflow) {
			refuse("proposal-workflow-not-allowed", fmt.Errorf("%s is not allowed for %s", p.Workflow, p.Target))
		}
	}
	if rule == "" {
		innerPath, fromRepo, err := item.ResolveWorkflowFile(l.cfg.Root, p.Workflow)
		if err != nil {
			refuse("proposal-workflow-untracked", err)
		} else if fromRepo {
			rel := filepath.Join("config/tillr/workflows", p.Workflow+".json")
			if err := exec.Command("git", "-C", l.cfg.Root, "ls-files", "--error-unmatch", "--", rel).Run(); err != nil {
				refuse("proposal-workflow-untracked", err)
			}
		}
		if rule == "" {
			inner, err := expandWorkflow(l.cfg.Root, innerPath, l.cfg.Policy, map[string]bool{}, p.Args, nil)
			if err != nil {
				r := "proposal-workflow-invalid"
				if strings.Contains(err.Error(), "workflow-argument-") {
					r = "proposal-argument-bounds"
				}
				if strings.Contains(err.Error(), "workflow-write-scope") {
					r = "proposal-scope-outside-fence"
				}
				if strings.Contains(err.Error(), "workflow-unknown-seat") {
					r = "proposal-seat-unmapped"
				} else if strings.Contains(err.Error(), "workflow-read-only-seat-writes") {
					r = "proposal-read-only-seat-writes"
				} else if strings.Contains(err.Error(), "workflow-task-outside-scope") {
					r = "proposal-task-outside-scope"
				} else if strings.Contains(err.Error(), "workflow-task-forbids-commit") {
					r = "proposal-task-forbids-commit"
				}
				refuse(r, err)
			} else {
				if err := inner.CheckScopes(nil); err != nil {
					refuse("proposal-scope-outside-fence", err)
				} else if err := inner.CheckNodeTasks(l.cfg.Root); err != nil {
					r := "proposal-task-outside-scope"
					if strings.Contains(err.Error(), "workflow-task-forbids-commit") {
						r = "proposal-task-forbids-commit"
					}
					refuse(r, err)
				} else {
					_, _, pinned, err := item.ReadWorkflowSelection(tx.Reader(), d.ItemID)
					if err != nil {
						return err
					}
					if compatibilityReviewSlot(pinned) != "" && evaluatorSlot(inner) != "" {
						refuse("proposal-exit", fmt.Errorf("cannot replace the selected compatibility evaluator"))
					} else if next, err = replaceProposalGraph(d.Workflow, p.Target, inner); err != nil {
						r := strings.SplitN(err.Error(), ":", 2)[0]
						refuse(r, err)
					}
				}
			}
		}
	}
	after := before
	afterCount := len(d.Workflow.Stages)
	if rule == "" {
		after, err = json.Marshal(next)
		if err != nil {
			return err
		}
		afterCount = len(next.Stages)
	}
	if _, err := tx.Exec(`INSERT INTO graph_revisions(item_id,attempt,proposing_run,proposing_node,target,workflow,rule,detail,before_graph,after_graph,before_nodes,after_nodes,recorded_at) VALUES(?,?,?,?,?,?,?,?,?,?,?,?,?)`, d.ItemID, d.Attempt, d.RunRequest.RunID, stage.ID, p.Target, p.Workflow, rule, detail, string(before), string(after), len(d.Workflow.Stages), afterCount, l.cfg.Now().UnixMilli()); err != nil {
		return err
	}
	if rule != "" {
		return nil
	}
	// Compatibility review is removed only from the execution graph. Keep that
	// authority in the persisted revision, and do not let an inner graph replace it.
	_, _, pinned, err := item.ReadWorkflowSelection(tx.Reader(), d.ItemID)
	if err != nil {
		return err
	}
	judgeGraph := string(after)
	if compatibilityReviewSlot(pinned) != "" {
		judgeGraph = store.CompatibilityJudgeGraph(d.SelectedWorkflow, judgeGraph)
	}
	if _, err := tx.Exec(`UPDATE workflow_selections SET judge_graph=?, judge_attempt=? WHERE item_id=?`, judgeGraph, d.Attempt, d.ItemID); err != nil {
		return err
	}
	d.Workflow = next
	if work != nil {
		work.Decision = *d
		return appendEffect(tx, graphChangedKind, d.RunRequest.RunID, work, l.cfg.Now().UTC())
	}
	return nil
}
