package workflow

import (
	"bytes"
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

	"github.com/mschulkind-oss/tillr-item/item"
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
workflow-evaluator-judge-classes,
workflow-file, workflow-output-promise, workflow-write-scope,
workflow-argument-missing, workflow-argument-type, workflow-argument-most,
workflow-argument-unknown, workflow-repeat, workflow-proposal, workflow-watch; command-registry, command-reference, command-flag,
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
	check(object, "name version stages edges exit args inputs max_parallel watch", "graph")
	if rawWatch, present := object["watch"]; present {
		findings = append(findings, checkWatchFields(rawWatch)...)
	}
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
				check(obj, "id kind seat access command_ref flags resolved_argv timeout_ms workflow inputs outputs args repeat prompt write_scope may_replace judge_classes", "stage="+stage)
				if raw, present := obj["judge_classes"]; present {
					var classes []string
					if json.Unmarshal(raw, &classes) != nil {
						findings = append(findings, fmt.Errorf("workflow-evaluator-judge-classes: stage=%s: judge_classes must be a list of strings", stage))
					}
				}
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

func checkWatchFields(raw json.RawMessage) []error {
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		return nil
	}
	watchObj, errs := decodeStrictJSONObject(raw, "watch", map[string]bool{
		"cycle_anchor":      true,
		"oscillation_pairs": true,
	})
	if len(errs) > 0 {
		return errs
	}
	var findings []error
	if rawAnchor, present := watchObj["cycle_anchor"]; present && !bytes.Equal(bytes.TrimSpace(rawAnchor), []byte("null")) {
		anchorObj, aErrs := decodeStrictJSONObject(rawAnchor, "watch.cycle_anchor", map[string]bool{
			"from":       true,
			"to":         true,
			"max_cycles": true,
		})
		if len(aErrs) > 0 {
			findings = append(findings, aErrs...)
		} else {
			from, okFrom := decodeStrictJSONString(anchorObj["from"], "watch.cycle_anchor.from", &findings)
			to, okTo := decodeStrictJSONString(anchorObj["to"], "watch.cycle_anchor.to", &findings)
			if okFrom && from == "" {
				findings = append(findings, fmt.Errorf("workflow-watch: watch.cycle_anchor.from: must be non-empty"))
			}
			if okTo && to == "" {
				findings = append(findings, fmt.Errorf("workflow-watch: watch.cycle_anchor.to: must be non-empty"))
			}
			if okFrom && okTo && from != "" && to != "" && from == to {
				findings = append(findings, fmt.Errorf("workflow-watch: watch.cycle_anchor: from and to must differ"))
			}
			if maxCycles, okMax := decodeStrictJSONInt(anchorObj["max_cycles"], "watch.cycle_anchor.max_cycles", &findings); okMax && maxCycles < 1 {
				findings = append(findings, fmt.Errorf("workflow-watch: watch.cycle_anchor.max_cycles: must be >= 1"))
			}
		}
	}
	if rawPairs, present := watchObj["oscillation_pairs"]; present && !bytes.Equal(bytes.TrimSpace(rawPairs), []byte("null")) {
		var rawList []json.RawMessage
		if json.Unmarshal(rawPairs, &rawList) != nil {
			findings = append(findings, fmt.Errorf("workflow-watch: watch.oscillation_pairs: expected a list"))
		} else {
			for i, rawPair := range rawList {
				prefix := fmt.Sprintf("watch.oscillation_pairs[%d]", i)
				pairObj, pErrs := decodeStrictJSONObject(rawPair, prefix, map[string]bool{
					"a":         true,
					"b":         true,
					"threshold": true,
					"hint":      true,
				})
				if len(pErrs) > 0 {
					findings = append(findings, pErrs...)
					continue
				}
				a, okA := decodeStrictJSONString(pairObj["a"], prefix+".a", &findings)
				b, okB := decodeStrictJSONString(pairObj["b"], prefix+".b", &findings)
				if okA && a == "" {
					findings = append(findings, fmt.Errorf("workflow-watch: %s.a: must be non-empty", prefix))
				}
				if okB && b == "" {
					findings = append(findings, fmt.Errorf("workflow-watch: %s.b: must be non-empty", prefix))
				}
				if okA && okB && a != "" && b != "" && a == b {
					findings = append(findings, fmt.Errorf("workflow-watch: %s: a and b must differ", prefix))
				}
				if rawThreshold, hasThreshold := pairObj["threshold"]; hasThreshold {
					if threshold, okT := decodeStrictJSONInt(rawThreshold, prefix+".threshold", &findings); okT && threshold < 2 {
						findings = append(findings, fmt.Errorf("workflow-watch: %s.threshold: must be >= 2", prefix))
					}
				}
				if rawHint, hasHint := pairObj["hint"]; hasHint {
					decodeStrictJSONString(rawHint, prefix+".hint", &findings)
				}
			}
		}
	}
	return findings
}

func decodeStrictJSONObject(raw json.RawMessage, prefix string, allowed map[string]bool) (map[string]json.RawMessage, []error) {
	decoder := json.NewDecoder(bytes.NewReader(raw))
	opening, err := decoder.Token()
	if err != nil || opening != json.Delim('{') {
		return nil, []error{fmt.Errorf("workflow-watch: %s: expected an object", prefix)}
	}
	fields := make(map[string]json.RawMessage)
	var findings []error
	for decoder.More() {
		tok, err := decoder.Token()
		if err != nil {
			findings = append(findings, fmt.Errorf("workflow-watch: %s: %w", prefix, err))
			return nil, findings
		}
		key, ok := tok.(string)
		if !ok {
			findings = append(findings, fmt.Errorf("workflow-watch: %s: expected a key", prefix))
			return nil, findings
		}
		var val json.RawMessage
		if err := decoder.Decode(&val); err != nil {
			findings = append(findings, fmt.Errorf("workflow-watch: %s.%s: %w", prefix, key, err))
			return nil, findings
		}
		if _, seen := fields[key]; seen {
			findings = append(findings, fmt.Errorf("workflow-watch: %s: duplicate key %q", prefix, key))
			continue
		}
		if !allowed[key] {
			findings = append(findings, fmt.Errorf("workflow-unknown-field: %s: unknown field %q", prefix, key))
			continue
		}
		fields[key] = val
	}
	return fields, findings
}

func decodeStrictJSONString(raw json.RawMessage, key string, findings *[]error) (string, bool) {
	if len(raw) == 0 {
		*findings = append(*findings, fmt.Errorf("workflow-watch: %s: missing required string", key))
		return "", false
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		*findings = append(*findings, fmt.Errorf("workflow-watch: %s: null is not a string", key))
		return "", false
	}
	var s string
	if json.Unmarshal(raw, &s) != nil {
		*findings = append(*findings, fmt.Errorf("workflow-watch: %s: expected a string", key))
		return "", false
	}
	return s, true
}

func decodeStrictJSONInt(raw json.RawMessage, key string, findings *[]error) (int, bool) {
	if len(raw) == 0 {
		*findings = append(*findings, fmt.Errorf("workflow-watch: %s: missing required integer", key))
		return 0, false
	}
	if bytes.Equal(bytes.TrimSpace(raw), []byte("null")) {
		*findings = append(*findings, fmt.Errorf("workflow-watch: %s: null is not an integer", key))
		return 0, false
	}
	var n int
	if json.Unmarshal(raw, &n) != nil {
		*findings = append(*findings, fmt.Errorf("workflow-watch: %s: expected an integer", key))
		return 0, false
	}
	return n, true
}

// ReadWorkflow expands a workflow file at path and validates its output wiring.
func ReadWorkflow(root, p string, policy *SeatPolicy, stack map[string]bool) (*Workflow, error) {
	w, err := ExpandWorkflow(root, p, policy, stack, nil, nil)
	if err == nil {
		err = ValidateOutputWiring(w)
	}
	return w, err
}

// ExpandWorkflow expands a workflow file at path with optional arguments and symbolic paths.
func ExpandWorkflow(root, p string, policy *SeatPolicy, stack map[string]bool, args map[string]any, symbols map[string]bool) (*Workflow, error) {
	w, _, _, err := ExpandWorkflowWithWatch(root, p, policy, stack, args, symbols)
	return w, err
}

// ExpandWorkflowWithWatch expands a workflow file and returns the collected stage IDs and seats for watch validation.
func ExpandWorkflowWithWatch(root, p string, policy *SeatPolicy, stack map[string]bool, args map[string]any, symbols map[string]bool) (*Workflow, map[string]bool, map[string]bool, error) {
	ref := strings.TrimSuffix(filepath.Base(p), ".json")
	if stack[ref] {
		return nil, nil, nil, fmt.Errorf("%s: workflow-expansion-cycle: graph: %s expands into itself", p, ref)
	}
	stack[ref] = true
	defer delete(stack, ref)
	info, err := os.Lstat(p)
	if err != nil || info.Mode()&os.ModeSymlink != 0 || !info.Mode().IsRegular() || filepath.Ext(p) != ".json" {
		return nil, nil, nil, fmt.Errorf("%s: workflow-file: graph: expected a regular .json file (%v)", p, err)
	}
	raw, err := os.ReadFile(p)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s: workflow-file: graph: %w", p, err)
	}
	if findings := workflowFields(raw); len(findings) > 0 {
		return nil, nil, nil, fmt.Errorf("%s: %w", p, errors.Join(findings...))
	}
	bound, err := BindWorkflow(root, raw, args)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", p, err)
	}
	w, err := DecodeWorkflow(bound, ref)
	if err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", p, err)
	}
	watchIDs := map[string]bool{}
	watchSeats := map[string]bool{}
	for _, s := range w.Stages {
		if s.ID != "" {
			watchIDs[s.ID] = true
		}
		if s.Seat != "" {
			watchSeats[s.Seat] = true
		}
	}
	if err := RemoveEmptyRepeats(w, args); err != nil {
		return nil, nil, nil, err
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
		for _, wp := range stage.WriteScope.Paths {
			if symbols[wp] {
				stage.WriteScope.symbolic = true
			}
		}
		if stage.Kind == "workflow" {
			composed[stage.ID] = true
			innerPath, fromRepo, err := item.ResolveWorkflowFile(root, stage.Workflow)
			if err != nil {
				findings = append(findings, fmt.Errorf("workflow-missing-reference: %s: no tracked workflow defines %s", stage.ID, stage.Workflow))
				continue
			}
			if fromRepo {
				rel := filepath.Join("config", "tillr", "workflows", stage.Workflow+".json")
				tracked := exec.Command("git", "-C", root, "ls-files", "--error-unmatch", "--", rel)
				if err := tracked.Run(); err != nil {
					findings = append(findings, fmt.Errorf("workflow-missing-reference: %s: no tracked workflow defines %s", stage.ID, stage.Workflow))
					continue
				}
			}
			inner, innerIDs, innerSeats, err := ExpandWorkflowWithWatch(root, innerPath, policy, stack, stage.Args, symbols)
			if err != nil {
				findings = append(findings, fmt.Errorf("stage=%s: %w", stage.ID, err))
				continue
			}
			for id := range innerIDs {
				watchIDs[id] = true
			}
			for seat := range innerSeats {
				watchSeats[seat] = true
			}
			if innerEvaluator := EvaluatorSlot(inner); innerEvaluator != "" {
				if EvaluatorSlot(w) != "" {
					return nil, nil, nil, fmt.Errorf("workflow-evaluator-access: exit: graph must have one evaluator run")
				}
				if w.Exit == nil {
					w.Exit = &WorkflowExit{RequiresNodes: RequiredWorkflowNodes(w)}
				}
				w.Exit.EvaluatorSlot = stage.ID + "/" + innerEvaluator
			}
			if err := InheritWorkflowScope(inner, stage.WriteScope); err != nil {
				return nil, nil, nil, err
			}
			if inner.ExpansionDepth+1 > w.ExpansionDepth {
				w.ExpansionDepth = inner.ExpansionDepth + 1
			}
			inputPorts[stage.ID] = map[string][]string{}
			outputPorts[stage.ID] = map[string][]WorkflowPort{}
			for name, typ := range stage.Inputs {
				if inner.Inputs[name] != typ {
					return nil, nil, nil, fmt.Errorf("workflow-input: %s: inner workflow does not declare %s", stage.ID, name)
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
					return nil, nil, nil, fmt.Errorf("workflow-input: %s: unused inner input %s", stage.ID, name)
				}
			}
			for name, typ := range stage.Outputs {
				port, ok := inner.Exit.Outputs[name]
				if !ok {
					return nil, nil, nil, fmt.Errorf("workflow-output-promise: %s: inner exit does not export %s", stage.ID, name)
				}
				found := false
				for _, node := range inner.Stages {
					if node.ID == port.Node && node.Outputs[port.Output] == typ {
						found = true
					}
				}
				if !found {
					return nil, nil, nil, fmt.Errorf("workflow-output-promise: %s: inner output %s has wrong type", stage.ID, name)
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
			for _, id := range RequiredWorkflowNodes(inner) {
				exits[stage.ID] = append(exits[stage.ID], prefix+id)
			}
			for key, digest := range inner.Sources {
				w.Sources[key] = digest
			}
		} else {
			if stage.Kind == "command" {
				if err := ResolveCommand(root, &stage); err != nil {
					findings = append(findings, fmt.Errorf("stage=%s: %w", stage.ID, err))
				}
			} else if stage.Kind == "join" {
				// join has no seat or command_ref
			} else if policy != nil && policy.Seats[stage.Seat] == "" {
				findings = append(findings, fmt.Errorf("workflow-unknown-seat: %s: seat %s is not mapped by policy; %s", stage.ID, stage.Seat, policy.SeatDeclaration))
			} else if policy != nil && stage.Access == "write" && slices.Contains(policy.ReadOnlySeats, stage.Seat) {
				findings = append(findings, fmt.Errorf("workflow-read-only-seat-writes: %s: seat %s is read-only under policy; %s", stage.ID, stage.Seat, policy.SeatDeclaration))
			}
			copies := []WorkflowStage{stage}
			var pieceEdges []WorkflowEdge
			if stage.Repeat != "" {
				repeated[stage.ID] = true
				values, _ := args[stage.Repeat].([]any)
				copies = nil
				if w.Args[stage.Repeat].Type == "pieces" {
					pieces := make([]WorkflowPiece, 0, len(values))
					pieceByID := make(map[string]int, len(values))
					for _, value := range values {
						piece, err := ParseWorkflowPiece(root, value)
						if err != nil {
							return nil, nil, nil, fmt.Errorf("workflow-argument-type: %s: %w", stage.Repeat, err)
						}
						if _, dup := pieceByID[piece.ID]; dup {
							return nil, nil, nil, fmt.Errorf("workflow-argument-type: %s: duplicate piece id %q", stage.Repeat, piece.ID)
						}
						pieceByID[piece.ID] = len(pieces)
						pieces = append(pieces, piece)
						node := stage
						node.ID = stage.ID + "-" + piece.ID
						node.Seat, node.Access, node.Prompt, node.Outputs = piece.Seat, piece.Access, piece.Prompt, piece.Outputs
						node.WriteScope = WorkflowScope{Paths: piece.WriteScope, explicit: piece.Access == "write"}
						if policy != nil && policy.Seats[node.Seat] == "" {
							findings = append(findings, fmt.Errorf("workflow-unknown-seat: %s: seat %s is not mapped by policy; %s", node.ID, node.Seat, policy.SeatDeclaration))
						} else if policy != nil && node.Access == "write" && slices.Contains(policy.ReadOnlySeats, node.Seat) {
							findings = append(findings, fmt.Errorf("workflow-read-only-seat-writes: %s: seat %s is read-only under policy; %s", node.ID, node.Seat, policy.SeatDeclaration))
						}
						node.Repeat = ""
						node.CopiesOf = stage.ID
						copies = append(copies, node)
					}
					var err error
					pieceEdges, err = buildPieceEdges(stage.ID, pieces, pieceByID, copies)
					if err != nil {
						return nil, nil, nil, fmt.Errorf("workflow-argument-type: %s: %w", stage.Repeat, err)
					}
				} else {
					for i, value := range values {
						node := stage
						node.ID = fmt.Sprintf("%s-%d", stage.ID, i+1)
						node.Prompt = strings.ReplaceAll(stage.Prompt, "${item}", value.(string))
						node.Repeat = ""
						node.CopiesOf = stage.ID
						copies = append(copies, node)
					}
				}
			}
			inputPorts[stage.ID] = map[string][]string{}
			outputPorts[stage.ID] = map[string][]WorkflowPort{}
			if len(pieceEdges) > 0 {
				hasIn := map[string]bool{}
				hasOut := map[string]bool{}
				for _, pe := range pieceEdges {
					hasOut[pe.From] = true
					hasIn[pe.To] = true
				}
				for _, node := range copies {
					if !hasIn[node.ID] {
						entries[stage.ID] = append(entries[stage.ID], node.ID)
					}
					if !hasOut[node.ID] {
						exits[stage.ID] = append(exits[stage.ID], node.ID)
					}
					for name := range node.Inputs {
						inputPorts[stage.ID][name] = append(inputPorts[stage.ID][name], node.ID)
					}
					for name := range node.Outputs {
						outputPorts[stage.ID][name] = append(outputPorts[stage.ID][name], WorkflowPort{Node: node.ID, Output: name})
					}
					stages = append(stages, node)
				}
				edges = append(edges, pieceEdges...)
			} else {
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
	}
	if len(findings) > 0 {
		return nil, nil, nil, fmt.Errorf("%s: %w", p, errors.Join(findings...))
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
					return nil, nil, nil, fmt.Errorf("workflow-output-promise: %s: output %s has no producer", edge.From, edge.FromOutput)
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
		for _, to := range SortedNames(barriers) {
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
	for _, id := range RequiredWorkflowNodes(w) {
		required = append(required, exits[id]...)
	}
	ports := map[string]WorkflowPort{}
	if w.Exit != nil {
		for name, port := range w.Exit.Outputs {
			resolved := outputPorts[port.Node][port.Output]
			if repeated[port.Node] || len(resolved) != 1 {
				return nil, nil, nil, fmt.Errorf("workflow-output-promise: exit: %s must name one non-repeated producer", name)
			}
			ports[name] = resolved[0]
		}
	}
	candidate := WorkflowCandidatePolicy(w)
	evaluator := EvaluatorSlot(w)
	if evaluator != "" {
		if strings.Contains(evaluator, "/") {
			// An inherited evaluator already names an expanded node.
		} else {
			if len(entries[evaluator]) != 1 {
				return nil, nil, nil, fmt.Errorf("workflow-evaluator-access: exit: evaluator must name one read-only run")
			}
			evaluator = entries[evaluator][0]
		}
	}
	w.Stages, w.Edges, w.Exit = stages, edges, &WorkflowExit{RequiresNodes: required, RequiresCandidate: candidate, Outputs: ports, EvaluatorSlot: evaluator}
	SynthesizeJoinStages(w)
	ids := map[string]bool{}
	for _, s := range w.Stages {
		if ids[s.ID] {
			return nil, nil, nil, fmt.Errorf("workflow-duplicate-id: %s", s.ID)
		}
		ids[s.ID] = true
		watchIDs[s.ID] = true
		if s.Seat != "" {
			watchSeats[s.Seat] = true
		}
	}
	if err := ValidateEvaluatorJudgeClasses(w); err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", p, err)
	}
	if err := ValidateWorkflowExit(w); err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", p, err)
	}
	if err := ValidateTemplateWiring(w); err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", p, err)
	}
	if err := ValidateWorkflowWatchRefs(w, watchIDs, watchSeats); err != nil {
		return nil, nil, nil, fmt.Errorf("%s: %w", p, err)
	}
	return w, watchIDs, watchSeats, nil
}

// RequiredWorkflowNodes returns the node IDs required by w.Exit, or all stage IDs when Exit is nil.
func RequiredWorkflowNodes(w *Workflow) []string {
	if w.Exit != nil {
		return w.Exit.RequiresNodes
	}
	ids := []string{}
	for _, s := range w.Stages {
		ids = append(ids, s.ID)
	}
	return ids
}

// WorkflowSize counts executable nodes, longest dependency path (in nodes), and
// greatest number of outgoing edges at any node. Expansion has already finished.
func WorkflowSize(w *Workflow) (nodes, depth, fanout int) {
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

func writeWorkflowFinding(out io.Writer, p, line string) {
	stage := "graph"
	if i := strings.Index(line, "stage="); i >= 0 {
		stage = strings.SplitN(line[i+6:], ":", 2)[0]
	}
	location := workflowRulePattern.FindStringIndex(line)
	if location == nil {
		fmt.Fprintf(out, "%s stage=%s workflow-file: %s\n", p, stage, line)
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
	fmt.Fprintf(out, "%s stage=%s %s: %s\n", p, stage, rule, detail)
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
		err = filepath.WalkDir(target, func(p string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if !entry.IsDir() && strings.HasSuffix(p, ".json") {
				paths = append(paths, p)
			}
			return nil
		})
		if err != nil {
			fmt.Fprintf(out, "%s stage=graph workflow-file: %v\n", target, err)
			return false
		}
	}
	ok := true
	for _, p := range paths {
		cases, err := WorkflowCheckArguments(root, p)
		if err != nil {
			ok = false
			for _, line := range strings.Split(err.Error(), "\n") {
				writeWorkflowFinding(out, p, line)
			}
			continue
		}
		for _, args := range cases {
			symbols := map[string]bool{}
			for _, value := range args {
				values, _ := value.([]any)
				for _, value := range values {
					s, ok := value.(string)
					if ok && strings.HasPrefix(s, WorkflowCheckPathPrefix) {
						symbols[s] = true
					}
				}
			}
			w, err := ExpandWorkflow(root, p, nil, map[string]bool{}, args, symbols)
			if err != nil {
				ok = false
				for _, line := range strings.Split(err.Error(), "\n") {
					writeWorkflowFinding(out, p, line)
				}
			} else {
				nodes, depth, fanout := WorkflowSize(w)
				fmt.Fprintf(out, "%s nodes=%d depth=%d fan-out=%d expansion-depth=%d\n", p, nodes, depth, fanout, w.ExpansionDepth)
			}
		}
	}
	return ok
}

func buildPieceEdges(stageID string, pieces []WorkflowPiece, pieceByID map[string]int, copies []WorkflowStage) ([]WorkflowEdge, error) {
	var edges []WorkflowEdge
	edgeSeen := map[string]bool{}
	addEdge := func(fromIdx, toIdx int) {
		fromID := copies[fromIdx].ID
		toID := copies[toIdx].ID
		key := fromID + "\x00" + toID
		if edgeSeen[key] {
			return
		}
		edgeSeen[key] = true
		edges = append(edges, WorkflowEdge{From: fromID, To: toID})
	}

	hasOutgoingWrite := make([]bool, len(pieces))
	lastWriteIdx := -1
	var pendingReads []int

	for i, piece := range pieces {
		if piece.After != nil {
			for _, dep := range *piece.After {
				depIdx, ok := pieceByID[dep]
				if !ok {
					return nil, fmt.Errorf("piece %s after unknown piece %s", piece.ID, dep)
				}
				addEdge(depIdx, i)
				if piece.Access == "write" && pieces[depIdx].Access == "write" {
					hasOutgoingWrite[depIdx] = true
				}
			}
			if piece.Access == "write" {
				lastWriteIdx = i
				pendingReads = nil
			} else {
				pendingReads = append(pendingReads, i)
			}
			continue
		}
		if piece.Access == "read_only" {
			if lastWriteIdx >= 0 {
				addEdge(lastWriteIdx, i)
			}
			pendingReads = append(pendingReads, i)
			continue
		}
		// Sequential write piece (After == nil): depends on preceding write(s) without an
		// outgoing write edge plus any read_only pieces since the last write.
		for j := 0; j < i; j++ {
			if pieces[j].Access == "write" && !hasOutgoingWrite[j] {
				addEdge(j, i)
				hasOutgoingWrite[j] = true
			}
		}
		for _, rIdx := range pendingReads {
			addEdge(rIdx, i)
		}
		lastWriteIdx = i
		pendingReads = nil
	}

	// Validate acyclicity across piece edges.
	inDeg := make(map[string]int, len(copies))
	adj := make(map[string][]string, len(copies))
	for _, c := range copies {
		inDeg[c.ID] = 0
	}
	for _, e := range edges {
		adj[e.From] = append(adj[e.From], e.To)
		inDeg[e.To]++
	}
	var queue []string
	for _, c := range copies {
		if inDeg[c.ID] == 0 {
			queue = append(queue, c.ID)
		}
	}
	visited := 0
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		visited++
		for _, nxt := range adj[cur] {
			inDeg[nxt]--
			if inDeg[nxt] == 0 {
				queue = append(queue, nxt)
			}
		}
	}
	if visited != len(copies) {
		return nil, fmt.Errorf("piece dependencies contain a cycle")
	}
	return edges, nil
}

// SynthesizeJoinStages inserts a built-in kind: "join" stage whenever two or more "write"
// stages are unordered in a parallel fan-out without an explicit reconverging join stage.
func SynthesizeJoinStages(w *Workflow) {
	if w == nil || len(w.Stages) < 2 {
		return
	}
	writeIDs := []string{}
	joinIDs := []string{}
	existingIDs := make(map[string]bool, len(w.Stages))
	for _, s := range w.Stages {
		existingIDs[s.ID] = true
		if s.Kind == "join" {
			joinIDs = append(joinIDs, s.ID)
		} else if s.Access == "write" {
			writeIDs = append(writeIDs, s.ID)
		}
	}
	if len(writeIDs) < 2 {
		return
	}

	adj := make(map[string][]string, len(w.Stages))
	for _, e := range w.Edges {
		if e.WhenOutcome == "fail" {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
	}
	reach := make(map[string]map[string]bool, len(w.Stages))
	for _, s := range w.Stages {
		seen := map[string]bool{}
		stack := append([]string(nil), adj[s.ID]...)
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if seen[cur] {
				continue
			}
			seen[cur] = true
			stack = append(stack, adj[cur]...)
		}
		reach[s.ID] = seen
	}

	unreconverged := make(map[string]map[string]bool, len(writeIDs))
	for i := 0; i < len(writeIDs); i++ {
		for j := i + 1; j < len(writeIDs); j++ {
			a, b := writeIDs[i], writeIDs[j]
			if reach[a][b] || reach[b][a] {
				continue
			}
			hasJoin := false
			for _, jID := range joinIDs {
				if reach[a][jID] && reach[b][jID] {
					hasJoin = true
					break
				}
			}
			if hasJoin {
				continue
			}
			if unreconverged[a] == nil {
				unreconverged[a] = map[string]bool{}
			}
			if unreconverged[b] == nil {
				unreconverged[b] = map[string]bool{}
			}
			unreconverged[a][b] = true
			unreconverged[b][a] = true
		}
	}
	if len(unreconverged) == 0 {
		return
	}

	visited := map[string]bool{}
	var components [][]string
	for _, id := range writeIDs {
		if len(unreconverged[id]) == 0 || visited[id] {
			continue
		}
		compSet := map[string]bool{}
		queue := []string{id}
		visited[id] = true
		for len(queue) > 0 {
			cur := queue[0]
			queue = queue[1:]
			compSet[cur] = true
			for nb := range unreconverged[cur] {
				if !visited[nb] {
					visited[nb] = true
					queue = append(queue, nb)
				}
			}
		}
		var comp []string
		for _, wid := range writeIDs {
			if compSet[wid] {
				comp = append(comp, wid)
			}
		}
		if len(comp) >= 2 {
			components = append(components, comp)
		}
	}

	nextJoinID := func() string {
		if !existingIDs["join"] {
			existingIDs["join"] = true
			return "join"
		}
		for n := 2; ; n++ {
			cand := fmt.Sprintf("join-%d", n)
			if !existingIDs[cand] {
				existingIDs[cand] = true
				return cand
			}
		}
	}

	for _, comp := range components {
		compSet := make(map[string]bool, len(comp))
		for _, id := range comp {
			compSet[id] = true
		}
		joinID := nextJoinID()
		joinStage := WorkflowStage{
			ID:     joinID,
			Kind:   "join",
			Access: "write",
		}
		lastCompIdx := -1
		for idx, s := range w.Stages {
			if compSet[s.ID] {
				lastCompIdx = idx
			}
		}
		if lastCompIdx >= 0 && lastCompIdx+1 < len(w.Stages) {
			nextStages := make([]WorkflowStage, 0, len(w.Stages)+1)
			nextStages = append(nextStages, w.Stages[:lastCompIdx+1]...)
			nextStages = append(nextStages, joinStage)
			nextStages = append(nextStages, w.Stages[lastCompIdx+1:]...)
			w.Stages = nextStages
		} else {
			w.Stages = append(w.Stages, joinStage)
		}

		edgeKey := func(e WorkflowEdge) string {
			return e.From + "\x00" + e.To + "\x00" + e.WhenOutcome + "\x00" + e.FromOutput + "\x00" + e.ToInput
		}
		seenEdges := make(map[string]bool, len(w.Edges)+len(comp))
		var nextEdges []WorkflowEdge
		addUniqueEdge := func(e WorkflowEdge) {
			k := edgeKey(e)
			if seenEdges[k] {
				return
			}
			seenEdges[k] = true
			nextEdges = append(nextEdges, e)
		}

		for _, e := range w.Edges {
			if compSet[e.From] && !compSet[e.To] && e.To != joinID && e.WhenOutcome != "fail" {
				if e.FromOutput != "" {
					addUniqueEdge(e)
					addUniqueEdge(WorkflowEdge{From: joinID, To: e.To, ToInput: e.ToInput})
				} else {
					rewired := e
					rewired.From = joinID
					rewired.RequiresPassed = nil
					addUniqueEdge(rewired)
				}
				continue
			}
			addUniqueEdge(e)
		}
		for _, id := range comp {
			addUniqueEdge(WorkflowEdge{From: id, To: joinID})
		}
		w.Edges = nextEdges

		if w.Exit != nil {
			seenReq := map[string]bool{}
			var nextReq []string
			for _, id := range w.Exit.RequiresNodes {
				target := id
				if compSet[id] {
					target = joinID
				}
				if !seenReq[target] {
					seenReq[target] = true
					nextReq = append(nextReq, target)
				}
			}
			w.Exit.RequiresNodes = nextReq
		}
	}
}

// HasConcurrentWriter reports whether stageID is a non-join "write" stage in w that has
// at least one other non-join "write" stage with no ordering path between them.
func HasConcurrentWriter(w *Workflow, stageID string) bool {
	if w == nil || stageID == "" {
		return false
	}
	var target *WorkflowStage
	for i := range w.Stages {
		if w.Stages[i].ID == stageID {
			target = &w.Stages[i]
			break
		}
	}
	if target == nil || target.Access != "write" || target.Kind == "join" {
		return false
	}
	adj := make(map[string][]string, len(w.Stages))
	for _, e := range w.Edges {
		if e.WhenOutcome == "fail" {
			continue
		}
		adj[e.From] = append(adj[e.From], e.To)
	}
	reaches := func(from, to string) bool {
		seen := map[string]bool{}
		stack := append([]string(nil), adj[from]...)
		for len(stack) > 0 {
			cur := stack[len(stack)-1]
			stack = stack[:len(stack)-1]
			if cur == to {
				return true
			}
			if seen[cur] {
				continue
			}
			seen[cur] = true
			stack = append(stack, adj[cur]...)
		}
		return false
	}
	for _, s := range w.Stages {
		if s.ID == stageID || s.Access != "write" || s.Kind == "join" {
			continue
		}
		if !reaches(stageID, s.ID) && !reaches(s.ID, stageID) {
			return true
		}
	}
	return false
}

// UnionWriteScopes merges write scopes across parallel siblings. If any sibling uses
// inherit (Paths == nil), the union is inherit.
func UnionWriteScopes(scopes []WorkflowScope) WorkflowScope {
	seen := map[string]bool{}
	var paths []string
	for _, sc := range scopes {
		if sc.Paths == nil {
			return WorkflowScope{}
		}
		for _, p := range sc.Paths {
			if !seen[p] {
				seen[p] = true
				paths = append(paths, p)
			}
		}
	}
	if len(paths) == 0 {
		return WorkflowScope{}
	}
	return WorkflowScope{Paths: paths, explicit: true}
}

// SpliceReconcileStage inserts an intra-item <joinID>-reconcile build writer stage
// immediately after joinID when join hits overlapping non-mirror conflicts.
func SpliceReconcileStage(w *Workflow, joinID, prompt string, scope WorkflowScope) (*Workflow, string) {
	raw, err := json.Marshal(w)
	if err != nil {
		return w, ""
	}
	var next Workflow
	if err := json.Unmarshal(raw, &next); err != nil {
		return w, ""
	}
	existing := make(map[string]bool, len(next.Stages))
	joinIdx := -1
	for i, s := range next.Stages {
		existing[s.ID] = true
		if s.ID == joinID {
			joinIdx = i
		}
	}
	if joinIdx < 0 {
		return &next, ""
	}
	reconcileID := joinID + "-reconcile"
	if existing[reconcileID] {
		for n := 2; ; n++ {
			cand := fmt.Sprintf("%s-reconcile-%d", joinID, n)
			if !existing[cand] {
				reconcileID = cand
				break
			}
		}
	}
	reconcileStage := WorkflowStage{
		ID:         reconcileID,
		Kind:       "agent",
		Seat:       "build",
		Access:     "write",
		WriteScope: scope,
		Prompt:     prompt,
	}
	stages := make([]WorkflowStage, 0, len(next.Stages)+1)
	stages = append(stages, next.Stages[:joinIdx+1]...)
	stages = append(stages, reconcileStage)
	stages = append(stages, next.Stages[joinIdx+1:]...)
	next.Stages = stages

	var edges []WorkflowEdge
	for _, e := range next.Edges {
		if e.From == joinID && e.WhenOutcome != "fail" {
			e.From = reconcileID
			e.RequiresPassed = nil
		}
		edges = append(edges, e)
	}
	edges = append(edges, WorkflowEdge{From: joinID, To: reconcileID, WhenOutcome: "fail"})
	next.Edges = edges

	if next.Exit != nil {
		for i, id := range next.Exit.RequiresNodes {
			if id == joinID {
				next.Exit.RequiresNodes[i] = reconcileID
			}
		}
	}
	return &next, reconcileID
}
