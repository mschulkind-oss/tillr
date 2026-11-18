package drain

import (
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"slices"
	"sort"
	"strings"
)

// WorkflowRules lists the loader's refusal names; the command prints them in help.
const WorkflowRules = `workflow-json, workflow-unknown-field, workflow-identity,
workflow-edges, workflow-empty-stages, workflow-stage-id, workflow-duplicate-id,
workflow-kind, workflow-command, workflow-timeout, workflow-unknown-seat,
workflow-read-only-seat-writes,
workflow-access, workflow-max-parallel, workflow-write-scope, workflow-write-scope-empty,
workflow-write-scope-read-only, workflow-input, workflow-output, workflow-outcome,
workflow-unknown-node, workflow-cycle,
workflow-reference, workflow-missing-reference, workflow-expansion-cycle,
workflow-exit, workflow-evaluator-access, workflow-evaluator-order, workflow-evaluator-required,
workflow-file, workflow-output-promise, workflow-write-scope,
workflow-argument-missing, workflow-argument-type, workflow-argument-most,
workflow-argument-unknown, workflow-repeat, workflow-proposal; command-registry, command-reference, command-flag,
command-value, command-path. Graph size is reported, never refused.`

// workflowFields checks each object independently so every unsupported field has
// a finding, with its stage (or graph/exit for fields not belonging to a stage).
func workflowFields(raw []byte) []error {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil
	} // decoder reports syntax errors
	var findings []error
	check := func(obj map[string]json.RawMessage, allowed, stage string) {
		keys := []string{}
		for key := range obj {
			if !strings.Contains(" "+allowed+" ", " "+key+" ") {
				keys = append(keys, key)
			}
		}
		sort.Strings(keys)
		for _, key := range keys {
			findings = append(findings, fmt.Errorf("workflow-unknown-field: %s: unknown field %q", stage, key))
		}
	}
	check(object, "name version stages edges exit args inputs max_parallel", "graph")
	if raw, present := object["max_parallel"]; present {
		var limit int
		if json.Unmarshal(raw, &limit) != nil || limit < 1 || limit > 8 {
			findings = append(findings, fmt.Errorf("workflow-max-parallel: graph: expected an integer from 1 through 8"))
		}
	}
	for _, field := range []string{"stages", "edges"} {
		var objects []map[string]json.RawMessage
		if json.Unmarshal(object[field], &objects) != nil {
			continue
		}
		for _, obj := range objects {
			stage := "graph"
			if field == "stages" {
				json.Unmarshal(obj["id"], &stage)
				check(obj, "id kind seat access command_ref flags resolved_argv timeout_ms workflow inputs outputs args repeat prompt write_scope may_replace", "stage="+stage)
			} else {
				json.Unmarshal(obj["to_node"], &stage)
				check(obj, "from_node to_node when_outcome from_output to_input", "stage="+stage)
				if raw, present := obj["when_outcome"]; present {
					var outcome string
					if json.Unmarshal(raw, &outcome) != nil || (outcome != "pass" && outcome != "fail") {
						findings = append(findings, fmt.Errorf("workflow-outcome: %s: expected pass or fail", stage))
					}
				}
			}
		}
	}
	var exit map[string]json.RawMessage
	if json.Unmarshal(object["exit"], &exit) == nil {
		check(exit, "requires_nodes requires_candidate outputs evaluator_slot", "exit")
		if raw, present := exit["requires_candidate"]; present {
			var candidate string
			if json.Unmarshal(raw, &candidate) != nil || (candidate != "commit_required" && candidate != "commit_optional" && candidate != "no_commit") {
				findings = append(findings, fmt.Errorf("workflow-exit: exit: requires_candidate must be commit_required, commit_optional or no_commit"))
			}
		}
	}
	return findings
}

func readWorkflow(root, path string, policy *Policy, stack map[string]bool) (*Workflow, error) {
	w, err := expandWorkflow(root, path, policy, stack, nil, nil)
	if err == nil {
		err = validateOutputWiring(w)
	}
	return w, err
}

func expandWorkflow(root, path string, policy *Policy, stack map[string]bool, args map[string]any, symbols map[string]bool) (*Workflow, error) {
	ref := strings.TrimSuffix(filepath.Base(path), ".json")
	if stack[ref] {
		return nil, fmt.Errorf("%s: workflow-expansion-cycle: graph: %s expands into itself", path, ref)
	}
	stack[ref] = true
	defer delete(stack, ref)
	info, err := os.Lstat(path)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || filepath.Ext(path) != ".json" {
		return nil, fmt.Errorf("%s: workflow-file: graph: expected a regular .json file (%v)", path, err)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("%s: workflow-file: graph: %w", path, err)
	}
	if findings := workflowFields(raw); len(findings) > 0 {
		return nil, fmt.Errorf("%s: %w", path, errors.Join(findings...))
	}
	bound, err := bindWorkflow(root, raw, args)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	w, err := decodeWorkflow(bound, ref)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := removeEmptyRepeats(w, args); err != nil {
		return nil, err
	}
	w.SHA256 = fmt.Sprintf("%x", sha256.Sum256(raw))
	w.Sources = map[string]string{ref: w.SHA256}
	w.Groups = map[string]WorkflowGroup{}
	entries, exits := map[string][]string{}, map[string][]string{}
	composed := map[string]bool{}
	inputPorts := map[string]map[string][]string{}
	outputPorts := map[string]map[string][]WorkflowPort{}
	repeated := map[string]bool{}
	stages := []WorkflowStage{}
	edges := []WorkflowEdge{}
	var findings []error
	for _, stage := range w.Stages {
		for _, path := range stage.WriteScope.Paths {
			if symbols[path] {
				stage.WriteScope.symbolic = true
			}
		}
		if stage.Kind == "workflow" {
			composed[stage.ID] = true
			rel := filepath.Join("config", "tillr", "workflows", stage.Workflow+".json")
			tracked := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", rel)
			if err := tracked.Run(); err != nil {
				findings = append(findings, fmt.Errorf("workflow-missing-reference: %s: no tracked workflow defines %s", stage.ID, stage.Workflow))
				continue
			}
			inner, err := expandWorkflow(root, filepath.Join(root, rel), policy, stack, stage.Args, symbols)
			if err != nil {
				findings = append(findings, fmt.Errorf("stage=%s: %w", stage.ID, err))
				continue
			}
			if innerEvaluator := evaluatorSlot(inner); innerEvaluator != "" {
				if evaluatorSlot(w) != "" {
					return nil, fmt.Errorf("workflow-evaluator-access: exit: graph must have one evaluator run")
				}
				if w.Exit == nil {
					w.Exit = &WorkflowExit{RequiresNodes: requiredWorkflowNodes(w)}
				}
				w.Exit.EvaluatorSlot = stage.ID + "/" + innerEvaluator
			}
			if err := inheritWorkflowScope(inner, stage.WriteScope); err != nil {
				return nil, err
			}
			if inner.ExpansionDepth+1 > w.ExpansionDepth {
				w.ExpansionDepth = inner.ExpansionDepth + 1
			}
			inputPorts[stage.ID] = map[string][]string{}
			outputPorts[stage.ID] = map[string][]WorkflowPort{}
			for name, typ := range stage.Inputs {
				if inner.Inputs[name] != typ {
					return nil, fmt.Errorf("workflow-input: %s: inner workflow does not declare %s", stage.ID, name)
				}
				for _, node := range inner.Stages {
					if node.Inputs[name] == typ {
						fed := false
						for _, e := range inner.Edges {
							if e.To == node.ID && e.ToInput == name {
								fed = true
							}
						}
						if !fed {
							inputPorts[stage.ID][name] = append(inputPorts[stage.ID][name], stage.ID+"/"+node.ID)
						}
					}
				}
				if len(inputPorts[stage.ID][name]) == 0 {
					return nil, fmt.Errorf("workflow-input: %s: unused inner input %s", stage.ID, name)
				}
			}
			for name, typ := range stage.Outputs {
				port, ok := inner.Exit.Outputs[name]
				if !ok {
					return nil, fmt.Errorf("workflow-output-promise: %s: inner exit does not export %s", stage.ID, name)
				}
				found := false
				for _, node := range inner.Stages {
					if node.ID == port.Node && node.Outputs[port.Output] == typ {
						found = true
					}
				}
				if !found {
					return nil, fmt.Errorf("workflow-output-promise: %s: inner output %s has wrong type", stage.ID, name)
				}
				port.Node = stage.ID + "/" + port.Node
				outputPorts[stage.ID][name] = []WorkflowPort{port}
			}
			groupOutputs := map[string]WorkflowPort{}
			for name, ports := range outputPorts[stage.ID] {
				groupOutputs[name] = ports[0]
			}
			w.Groups[stage.ID] = WorkflowGroup{Stage: stage, Outputs: groupOutputs}
			prefix := stage.ID + "/"
			for id, group := range inner.Groups {
				group.Stage.ID = prefix + group.Stage.ID
				ports := map[string]WorkflowPort{}
				for name, port := range group.Outputs {
					port.Node = prefix + port.Node
					ports[name] = port
				}
				group.Outputs = ports
				w.Groups[prefix+id] = group
			}
			incoming := map[string]bool{}
			for _, edge := range inner.Edges {
				incoming[edge.To] = true
				edge.From = prefix + edge.From
				edge.To = prefix + edge.To
				required := make([]string, len(edge.RequiresPassed))
				for i, id := range edge.RequiresPassed {
					required[i] = prefix + id
				}
				edge.RequiresPassed = required
				edges = append(edges, edge)
			}
			for _, node := range inner.Stages {
				if !incoming[node.ID] {
					entries[stage.ID] = append(entries[stage.ID], prefix+node.ID)
				}
				if node.Parent == "" {
					node.Parent = stage.ID
				} else {
					node.Parent = prefix + node.Parent
				}
				node.ID = prefix + node.ID
				if node.CopiesOf != "" {
					node.CopiesOf = prefix + node.CopiesOf
				}
				if len(node.MayReplace) > 0 {
					allowed := map[string][]string{}
					for target, refs := range node.MayReplace {
						allowed[prefix+target] = refs
					}
					node.MayReplace = allowed
				}
				stages = append(stages, node)
			}
			for _, id := range requiredWorkflowNodes(inner) {
				exits[stage.ID] = append(exits[stage.ID], prefix+id)
			}
			for key, digest := range inner.Sources {
				w.Sources[key] = digest
			}
		} else {
			if stage.Kind == "command" {
				if err := resolveCommand(root, &stage); err != nil {
					findings = append(findings, fmt.Errorf("stage=%s: %w", stage.ID, err))
				}
			} else if policy != nil && policy.Seats[stage.Seat] == "" {
				findings = append(findings, fmt.Errorf("workflow-unknown-seat: %s: seat %s is not mapped by policy; %s", stage.ID, stage.Seat, policy.seatDeclaration()))
			} else if policy != nil && stage.Access == "write" && slices.Contains(policy.ReadOnlySeats, stage.Seat) {
				findings = append(findings, fmt.Errorf("workflow-read-only-seat-writes: %s: seat %s is read-only under policy; %s", stage.ID, stage.Seat, policy.seatDeclaration()))
			}
			copies := []WorkflowStage{stage}
			if stage.Repeat != "" {
				repeated[stage.ID] = true
				values, _ := args[stage.Repeat].([]any)
				copies = nil
				for i, value := range values {
					node := stage
					if w.Args[stage.Repeat].Type == "pieces" {
						piece, err := parseWorkflowPiece(root, value)
						if err != nil {
							return nil, fmt.Errorf("workflow-argument-type: %s: %w", stage.Repeat, err)
						}
						node.ID = stage.ID + "-" + piece.ID
						node.Seat, node.Access, node.Prompt, node.Outputs = piece.Seat, piece.Access, piece.Prompt, piece.Outputs
						node.WriteScope = WorkflowScope{Paths: piece.WriteScope, explicit: piece.Access == "write"}
						if policy != nil && policy.Seats[node.Seat] == "" {
							findings = append(findings, fmt.Errorf("workflow-unknown-seat: %s: seat %s is not mapped by policy; %s", node.ID, node.Seat, policy.seatDeclaration()))
						} else if policy != nil && node.Access == "write" && slices.Contains(policy.ReadOnlySeats, node.Seat) {
							findings = append(findings, fmt.Errorf("workflow-read-only-seat-writes: %s: seat %s is read-only under policy; %s", node.ID, node.Seat, policy.seatDeclaration()))
						}
					} else {
						node.ID = fmt.Sprintf("%s-%d", stage.ID, i+1)
						node.Prompt = strings.ReplaceAll(stage.Prompt, "${item}", value.(string))
					}
					node.Repeat = ""
					node.CopiesOf = stage.ID
					copies = append(copies, node)
				}
			}
			inputPorts[stage.ID] = map[string][]string{}
			outputPorts[stage.ID] = map[string][]WorkflowPort{}
			for _, node := range copies {
				entries[stage.ID] = append(entries[stage.ID], node.ID)
				exits[stage.ID] = append(exits[stage.ID], node.ID)
				for name := range node.Inputs {
					inputPorts[stage.ID][name] = append(inputPorts[stage.ID][name], node.ID)
				}
				for name := range node.Outputs {
					outputPorts[stage.ID][name] = append(outputPorts[stage.ID][name], WorkflowPort{Node: node.ID, Output: name})
				}
				stages = append(stages, node)
			}
		}
	}
	if len(findings) > 0 {
		return nil, fmt.Errorf("%s: %w", path, errors.Join(findings...))
	}
	for _, edge := range w.Edges {
		targets := entries[edge.To]
		if edge.ToInput != "" {
			targets = inputPorts[edge.To][edge.ToInput]
		}
		for _, to := range targets {
			if edge.FromOutput != "" {
				ports := outputPorts[edge.From][edge.FromOutput]
				if len(ports) == 0 {
					return nil, fmt.Errorf("workflow-output-promise: %s: output %s has no producer", edge.From, edge.FromOutput)
				}
				for _, port := range ports {
					expanded := edge
					expanded.From, expanded.FromOutput, expanded.To = port.Node, port.Output, to
					if (composed[edge.From] || repeated[edge.From]) && edge.WhenOutcome != "fail" {
						expanded.RequiresPassed = append([]string(nil), exits[edge.From]...)
					}
					edges = append(edges, expanded)
				}
			}
		}
		barriers := map[string]bool{}
		for _, to := range append(append([]string(nil), entries[edge.To]...), targets...) {
			barriers[to] = true
		}
		for _, to := range sortedNames(barriers) {
			for _, from := range exits[edge.From] {
				expanded := edge
				expanded.From, expanded.To = from, to
				// A file edge from copies promises every file. Its ordering
				// barriers must not release the consumer after just one pass.
				if (composed[edge.From] || repeated[edge.From] && edge.FromOutput != "") && edge.WhenOutcome != "fail" {
					expanded.RequiresPassed = append([]string(nil), exits[edge.From]...)
				}
				expanded.FromOutput, expanded.ToInput = "", ""
				edges = append(edges, expanded)
			}
		}
	}
	required := []string{}
	for _, id := range requiredWorkflowNodes(w) {
		required = append(required, exits[id]...)
	}
	ports := map[string]WorkflowPort{}
	if w.Exit != nil {
		for name, port := range w.Exit.Outputs {
			resolved := outputPorts[port.Node][port.Output]
			if repeated[port.Node] || len(resolved) != 1 {
				return nil, fmt.Errorf("workflow-output-promise: exit: %s must name one non-repeated producer", name)
			}
			ports[name] = resolved[0]
		}
	}
	candidate := workflowCandidatePolicy(w)
	evaluator := evaluatorSlot(w)
	if evaluator != "" {
		if strings.Contains(evaluator, "/") {
			// An inherited evaluator already names an expanded node.
		} else {
			if len(entries[evaluator]) != 1 {
				return nil, fmt.Errorf("workflow-evaluator-access: exit: evaluator must name one read-only run")
			}
			evaluator = entries[evaluator][0]
		}
	}
	w.Stages, w.Edges, w.Exit = stages, edges, &WorkflowExit{RequiresNodes: required, RequiresCandidate: candidate, Outputs: ports, EvaluatorSlot: evaluator}
	ids := map[string]bool{}
	for _, s := range w.Stages {
		if ids[s.ID] {
			return nil, fmt.Errorf("workflow-duplicate-id: %s", s.ID)
		}
		ids[s.ID] = true
	}
	if err := validateWorkflowExit(w); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	if err := validateTemplateWiring(w); err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	return w, nil
}

func requiredWorkflowNodes(w *Workflow) []string {
	if w.Exit != nil {
		return w.Exit.RequiresNodes
	}
	ids := []string{}
	for _, s := range w.Stages {
		ids = append(ids, s.ID)
	}
	return ids
}

// workflowSize counts executable nodes, longest dependency path (in nodes), and
// greatest number of outgoing edges at any node. Expansion has already finished.
func workflowSize(w *Workflow) (nodes, depth, fanout int) {
	levels, outgoing := map[string]int{}, map[string]map[string]bool{}
	for _, s := range w.Stages {
		level := 1
		for _, e := range w.Edges {
			if e.To == s.ID && levels[e.From]+1 > level {
				level = levels[e.From] + 1
			}
		}
		levels[s.ID] = level
		if level > depth {
			depth = level
		}
	}
	for _, e := range w.Edges {
		if outgoing[e.From] == nil {
			outgoing[e.From] = map[string]bool{}
		}
		outgoing[e.From][e.To] = true
		if len(outgoing[e.From]) > fanout {
			fanout = len(outgoing[e.From])
		}
	}
	return len(w.Stages), depth, fanout
}

var workflowRulePattern = regexp.MustCompile(`(?:workflow|command)-[a-z-]+:`)

func writeWorkflowFinding(out io.Writer, path, line string) {
	stage := "graph"
	if i := strings.Index(line, "stage="); i >= 0 {
		stage = strings.SplitN(line[i+6:], ":", 2)[0]
	}
	location := workflowRulePattern.FindStringIndex(line)
	if location == nil {
		fmt.Fprintf(out, "%s stage=%s workflow-file: %s\n", path, stage, line)
		return
	}
	rule := strings.TrimSuffix(line[location[0]:location[1]], ":")
	detail := strings.TrimSpace(line[location[1]:])
	if stage == "graph" {
		switch rule {
		case "workflow-kind", "workflow-access", "workflow-write-scope", "workflow-write-scope-empty", "workflow-write-scope-read-only", "workflow-timeout", "workflow-unknown-seat", "workflow-read-only-seat-writes", "workflow-input", "workflow-output", "workflow-outcome", "workflow-exit", "workflow-missing-reference", "workflow-unknown-field":
			if parts := strings.SplitN(detail, ":", 2); len(parts) == 2 {
				stage = strings.TrimPrefix(parts[0], "stage=")
				detail = strings.TrimSpace(parts[1])
			}
		}
	}
	fmt.Fprintf(out, "%s stage=%s %s: %s\n", path, stage, rule, detail)
}

// CheckWorkflows validates a file or directory without opening the item store.
// Root names the repository used to resolve tracked references and commands.
func CheckWorkflows(root, target string, out io.Writer) bool {
	info, err := os.Lstat(target)
	if err != nil {
		fmt.Fprintf(out, "%s stage=graph workflow-file: %v\n", target, err)
		return false
	}
	paths := []string{target}
	if info.IsDir() {
		paths = nil
		err = filepath.WalkDir(target, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(path, ".json") {
				paths = append(paths, path)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(out, "%s stage=graph workflow-file: %v\n", target, err)
			return false
		}
	}
	ok := true
	for _, path := range paths {
		cases, err := workflowCheckArguments(root, path)
		if err != nil {
			ok = false
			for _, line := range strings.Split(err.Error(), "\n") {
				writeWorkflowFinding(out, path, line)
			}
			continue
		}
		for _, args := range cases {
			symbols := map[string]bool{}
			for _, value := range args {
				values, _ := value.([]any)
				for _, value := range values {
					path, ok := value.(string)
					if ok && strings.HasPrefix(path, workflowCheckPathPrefix) {
						symbols[path] = true
					}
				}
			}
			w, err := expandWorkflow(root, path, nil, map[string]bool{}, args, symbols)
			if err != nil {
				ok = false
				for _, line := range strings.Split(err.Error(), "\n") {
					writeWorkflowFinding(out, path, line)
				}
			} else {
				nodes, depth, fanout := workflowSize(w)
				fmt.Fprintf(out, "%s nodes=%d depth=%d fan-out=%d expansion-depth=%d\n", path, nodes, depth, fanout, w.ExpansionDepth)
			}
		}
	}
	return ok
}
