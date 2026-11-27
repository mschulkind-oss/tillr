package watch

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"math"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr/setupcheck"
)

type lintWorkflow struct {
	MaxParallel int                        `json:"max_parallel,omitempty"`
	Name        string                     `json:"name"`
	Version     int                        `json:"version"`
	Stages      []lintWorkflowStage        `json:"stages"`
	Edges       []lintWorkflowEdge         `json:"edges"`
	Exit        *lintWorkflowExit          `json:"exit,omitempty"`
	Args        map[string]lintWorkflowArg `json:"args,omitempty"`
	Inputs      map[string]string          `json:"inputs,omitempty"`
	Watch       *lintWorkflowWatch         `json:"watch,omitempty"`
}

type lintWorkflowWatch struct {
	CycleAnchor      *lintWorkflowCycleAnchor      `json:"cycle_anchor,omitempty"`
	OscillationPairs []lintWorkflowOscillationPair `json:"oscillation_pairs,omitempty"`
}

type lintWorkflowCycleAnchor struct {
	From      string `json:"from"`
	To        string `json:"to"`
	MaxCycles int    `json:"max_cycles"`
}

type lintWorkflowOscillationPair struct {
	A         string `json:"a"`
	B         string `json:"b"`
	Threshold int    `json:"threshold,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

type lintWorkflowExit struct {
	EvaluatorSlot     string         `json:"evaluator_slot,omitempty"`
	RequiresNodes     []string       `json:"requires_nodes"`
	RequiresCandidate string         `json:"requires_candidate,omitempty"`
	Outputs           map[string]any `json:"outputs,omitempty"`
}

type lintWorkflowArg struct {
	Type string `json:"type"`
	Most int    `json:"most,omitempty"`
}

type lintWorkflowStage struct {
	ID           string              `json:"id"`
	Kind         string              `json:"kind"`
	Seat         string              `json:"seat"`
	Access       string              `json:"access"`
	CommandRef   string              `json:"command_ref,omitempty"`
	Flags        map[string]any      `json:"flags,omitempty"`
	Command      []string            `json:"resolved_argv,omitempty"`
	TimeoutMS    int                 `json:"timeout_ms,omitempty"`
	Workflow     string              `json:"workflow,omitempty"`
	Args         map[string]any      `json:"args,omitempty"`
	Repeat       string              `json:"repeat,omitempty"`
	Prompt       string              `json:"prompt,omitempty"`
	WriteScope   json.RawMessage     `json:"write_scope,omitempty"`
	Inputs       map[string]string   `json:"inputs,omitempty"`
	Outputs      map[string]string   `json:"outputs,omitempty"`
	MayReplace   map[string][]string `json:"may_replace,omitempty"`
	JudgeClasses []string            `json:"judge_classes,omitempty"`
}

type lintWorkflowEdge struct {
	From        string `json:"from_node"`
	To          string `json:"to_node"`
	WhenOutcome string `json:"when_outcome,omitempty"`
	FromOutput  string `json:"from_output,omitempty"`
	ToInput     string `json:"to_input,omitempty"`
}

type lintCommandEntry struct {
	Argv  []string          `json:"argv"`
	Flags map[string]string `json:"flags"`
}

type loadedWorkflow struct {
	source   string
	ref      string
	wf       lintWorkflow
	acyclic  bool
	hasWrite bool
	stageIDs map[string]bool
	seats    map[string]bool
}

func discoverAndLoadWorkflows(absRoot, workflowsPath string, fc *findingCollector) ([]loadedWorkflow, string) {
	var paths []string
	var wfDir string

	if workflowsPath != "" {
		linfo, err := os.Lstat(workflowsPath)
		if err != nil {
			fc.add(workflowsPath, CodeWatchLintInput, fmt.Sprintf("workflows %q: %v", workflowsPath, err))
			return nil, ""
		}
		if linfo.Mode()&fs.ModeSymlink != 0 {
			fc.add(workflowsPath, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow path %q must not be a symlink", workflowsPath))
			return nil, ""
		}
		if linfo.IsDir() {
			wfDir = workflowsPath
			paths = walkWorkflowFiles(workflowsPath, fc)
		} else {
			wfDir = filepath.Dir(workflowsPath)
			paths = []string{workflowsPath}
		}
	} else {
		defaultDir := filepath.Join(absRoot, "config", "tillr", "workflows")
		linfo, err := os.Lstat(defaultDir)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, ""
			}
			fc.add(defaultDir, CodeWatchLintInput, fmt.Sprintf("workflows %q: %v", defaultDir, err))
			return nil, ""
		}
		if !linfo.IsDir() {
			fc.add(defaultDir, CodeWatchLintInput, fmt.Sprintf("workflows %q must be a directory", defaultDir))
			return nil, ""
		}
		wfDir = defaultDir
		paths = walkWorkflowFiles(defaultDir, fc)
	}

	cmds, cmdErr := loadCommandsRegistry(absRoot)
	var out []loadedWorkflow
	for _, p := range paths {
		if lw, ok := loadAndValidateWorkflowFile(absRoot, wfDir, p, cmds, cmdErr, fc); ok {
			out = append(out, lw)
		}
	}
	return out, wfDir
}

func walkWorkflowFiles(dir string, fc *findingCollector) []string {
	var files []string
	_ = filepath.WalkDir(dir, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			fc.add(path, CodeWatchLintInput, err.Error())
			return nil
		}
		if entry.Type()&fs.ModeSymlink != 0 {
			if filepath.Ext(path) == ".json" || entry.IsDir() {
				fc.add(path, CodeWatchLintWorkflowSchema, "workflow file must not be a symlink")
			}
			return nil
		}
		if entry.IsDir() || filepath.Ext(path) != ".json" {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files)
	return files
}

func loadCommandsRegistry(absRoot string) (map[string]lintCommandEntry, error) {
	cmdPath := filepath.Join(absRoot, "config", "tillr", "commands.json")
	raw, err := os.ReadFile(cmdPath)
	if err != nil {
		return nil, err
	}
	if err := checkDuplicateJSONKeys(raw); err != nil {
		return nil, err
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var entries map[string]lintCommandEntry
	if err := dec.Decode(&entries); err != nil {
		return nil, err
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("trailing content in commands.json")
	}
	for name, entry := range entries {
		if len(entry.Argv) == 0 || entry.Argv[0] == "" {
			return nil, fmt.Errorf("command %q needs non-empty argv", name)
		}
		switch filepath.Base(entry.Argv[0]) {
		case "sh", "bash", "dash", "zsh", "fish", "ksh", "csh", "tcsh":
			return nil, fmt.Errorf("command %q uses disallowed shell %q", name, entry.Argv[0])
		}
		for flag, kind := range entry.Flags {
			if !strings.HasPrefix(flag, "--") || strings.ContainsAny(flag, "= \t\n") || (kind != "string" && kind != "number" && kind != "path") {
				return nil, fmt.Errorf("command %q has invalid flag %q or type %q", name, flag, kind)
			}
		}
	}
	return entries, nil
}

func loadAndValidateWorkflowFile(absRoot, wfDir, path string, cmds map[string]lintCommandEntry, cmdErr error, fc *findingCollector) (loadedWorkflow, bool) {
	linfo, err := os.Lstat(path)
	if err != nil {
		fc.add(path, CodeWatchLintInput, fmt.Sprintf("workflow %q: %v", path, err))
		return loadedWorkflow{}, false
	}
	if linfo.Mode()&fs.ModeSymlink != 0 {
		fc.add(path, CodeWatchLintWorkflowSchema, "workflow file must not be a symlink")
		return loadedWorkflow{}, false
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		fc.add(path, CodeWatchLintInput, fmt.Sprintf("reading workflow %q: %v", path, err))
		return loadedWorkflow{}, false
	}
	if err := checkDuplicateJSONKeys(raw); err != nil {
		fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("duplicate or invalid JSON: %v", err))
		return loadedWorkflow{}, false
	}
	fieldErrs := checkWorkflowJSONFields(raw)
	for _, fe := range fieldErrs {
		fc.add(path, CodeWatchLintWorkflowSchema, fe)
	}

	dec := json.NewDecoder(bytes.NewReader(raw))
	if len(fieldErrs) == 0 {
		dec.DisallowUnknownFields()
	}
	dec.UseNumber()
	var w lintWorkflow
	if err := dec.Decode(&w); err != nil {
		fc.add(path, CodeWatchLintWorkflowSchema, err.Error())
		return loadedWorkflow{}, false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		fc.add(path, CodeWatchLintWorkflowSchema, "trailing JSON content")
		return loadedWorkflow{}, false
	}

	ref := strings.TrimSuffix(filepath.Base(path), ".json")
	if !validWorkflowRef(ref) || w.Name+"@v"+strconv.Itoa(w.Version) != ref {
		fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-identity: name %q and version %d must match %q", w.Name, w.Version, ref))
	}
	if w.MaxParallel < 0 || w.MaxParallel > 8 {
		fc.add(path, CodeWatchLintWorkflowSchema, "workflow-max-parallel: expected 1 through 8, or omitted")
	}
	if w.Edges == nil {
		fc.add(path, CodeWatchLintWorkflowSchema, "workflow-edges: edges must be a list")
	}
	if len(w.Stages) == 0 {
		fc.add(path, CodeWatchLintWorkflowSchema, "workflow-empty-stages: stages must not be empty")
	}

	ids := make(map[string]bool, len(w.Stages))
	seats := make(map[string]bool)
	for _, s := range w.Stages {
		if s.ID == "" || strings.ContainsAny(s.ID, "/\r\n\t") {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-stage-id: %q must be nonempty and contain no slash or control whitespace", s.ID))
		} else if ids[s.ID] {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-duplicate-id: %s", s.ID))
		}
		ids[s.ID] = true
		if s.Seat != "" {
			seats[s.Seat] = true
		}

		if s.Kind != "agent" && s.Kind != "command" && s.Kind != "workflow" && s.Kind != "join" {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-kind: stage %q: expected agent, command, workflow or join", s.ID))
			continue
		}
		if s.Repeat != "" {
			arg, ok := w.Args[s.Repeat]
			if !ok || (arg.Type != "strings" && arg.Type != "pieces") {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-repeat: stage %q repeat %q must reference a strings or pieces arg", s.ID, s.Repeat))
			}
		}
		if s.Kind == "workflow" {
			if !validWorkflowRef(s.Workflow) {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-reference: stage %q: invalid workflow ref %q", s.ID, s.Workflow))
			}
			if s.Seat != "" || s.Access != "" || s.CommandRef != "" || s.Flags != nil || s.TimeoutMS != 0 || len(s.Command) != 0 {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-kind: stage %q: workflow stage cannot specify run fields", s.ID))
			}
			continue
		}
		if s.Args != nil {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-kind: stage %q: only workflow stages pass args", s.ID))
		}
		if s.Workflow != "" {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-kind: stage %q: only workflow stages name a workflow", s.ID))
		}
		if len(s.Command) != 0 {
			fc.add(path, CodeWatchLintWorkflowSchema, "workflow-command: resolved_argv is internal, not workflow input")
		}
		if s.Kind == "command" {
			if s.CommandRef == "" || s.Seat != "" {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-kind: stage %q: command needs command_ref and no seat", s.ID))
			} else {
				validateCommandStage(path, s, w.Args, cmds, cmdErr, fc)
			}
		}
		if s.Kind == "join" {
			if s.CommandRef != "" || s.Flags != nil || s.Seat != "" || s.Access != "write" {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-kind: stage %q: join requires access write and no seat or command_ref", s.ID))
			}
		}
		if s.Kind == "agent" {
			if s.CommandRef != "" || s.Flags != nil {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-command: stage %q: agent cannot name command flags", s.ID))
			}
			if !validSeatName(s.Seat) {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-unknown-seat: stage %q: invalid seat name %q", s.ID, s.Seat))
			}
		}
		if s.TimeoutMS < 0 {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-timeout: stage %q: negative timeout_ms", s.ID))
		}
		if s.Access != "read_only" && s.Access != "write" {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-access: stage %q: access must be read_only or write", s.ID))
		}
		if len(s.WriteScope) > 0 && string(bytes.TrimSpace(s.WriteScope)) != "null" && s.Access == "read_only" {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-write-scope-read-only: stage %q: write_scope disallowed on read_only stage", s.ID))
		}
		for target, refs := range s.MayReplace {
			if target == "" || len(refs) == 0 {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-proposal: stage %q: may_replace target and workflows required", s.ID))
			}
			for _, mref := range refs {
				if !validWorkflowRef(mref) {
					fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-proposal: stage %q: invalid workflow %q", s.ID, mref))
				}
			}
		}
	}

	for name, arg := range w.Args {
		if name == "" {
			fc.add(path, CodeWatchLintWorkflowSchema, "workflow-argument-unknown: empty arg name")
		}
		switch arg.Type {
		case "paths", "strings", "pieces", "command":
		default:
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-argument-type: arg %q has unknown type %q", name, arg.Type))
		}
		if arg.Most < 0 {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-argument-most: arg %q has negative most", name))
		}
	}

	if w.Exit != nil {
		if w.Exit.RequiresCandidate != "" && w.Exit.RequiresCandidate != "commit_required" && w.Exit.RequiresCandidate != "commit_optional" && w.Exit.RequiresCandidate != "no_commit" {
			fc.add(path, CodeWatchLintWorkflowSchema, "workflow-exit: requires_candidate must be commit_required, commit_optional or no_commit")
		}
		if len(w.Exit.RequiresNodes) == 0 {
			fc.add(path, CodeWatchLintWorkflowSchema, "workflow-exit: requires_nodes must not be empty")
		}
		for _, id := range w.Exit.RequiresNodes {
			if !ids[id] {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-exit: unknown stage %q in requires_nodes", id))
			}
		}
		if w.Exit.EvaluatorSlot != "" {
			var evalStage *lintWorkflowStage
			for i := range w.Stages {
				if w.Stages[i].ID == w.Exit.EvaluatorSlot {
					evalStage = &w.Stages[i]
					break
				}
			}
			if evalStage == nil {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-evaluator-access: unknown evaluator %q", w.Exit.EvaluatorSlot))
			} else {
				if evalStage.Kind != "agent" || evalStage.Access != "read_only" {
					fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-evaluator-access: evaluator %q must be read_only agent", evalStage.ID))
				}
				if !slices.Contains(w.Exit.RequiresNodes, evalStage.ID) {
					fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-evaluator-required: evaluator %q must be in requires_nodes", evalStage.ID))
				}
				if len(evalStage.JudgeClasses) == 0 {
					fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-evaluator-judge-classes: stage %q judge_classes must be a nonempty list", evalStage.ID))
				} else {
					seenClass := map[string]bool{}
					for _, cls := range evalStage.JudgeClasses {
						if cls != "verification-only" && cls != "red-gate-triage" && cls != "direction" {
							fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-evaluator-judge-classes: stage %q invalid class %q", evalStage.ID, cls))
						}
						if seenClass[cls] {
							fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-evaluator-judge-classes: stage %q duplicate class %q", evalStage.ID, cls))
						}
						seenClass[cls] = true
					}
				}
			}
		}
	}
	for _, s := range w.Stages {
		if (w.Exit == nil || s.ID != w.Exit.EvaluatorSlot) && s.JudgeClasses != nil {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-evaluator-judge-classes: stage %q judge_classes belongs only on the evaluator", s.ID))
		}
	}

	for _, e := range w.Edges {
		if e.WhenOutcome != "" && e.WhenOutcome != "pass" && e.WhenOutcome != "fail" {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-outcome: %s -> %s: expected pass or fail", e.From, e.To))
		}
		if !ids[e.From] || !ids[e.To] {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-unknown-node: %s -> %s", e.From, e.To))
		}
	}

	acyclic := isWorkflowAcyclic(w)
	if !acyclic {
		fc.add(path, CodeWatchLintWorkflowSchema, "workflow-cycle: graph contains a cycle")
	}

	if w.Watch != nil {
		if ca := w.Watch.CycleAnchor; ca != nil {
			if ca.From == "" || ca.To == "" || ca.From == ca.To || ca.MaxCycles < 1 {
				fc.add(path, CodeWatchLintWorkflowSchema, "workflow-watch: invalid cycle_anchor")
			}
		}
		for i, pair := range w.Watch.OscillationPairs {
			if pair.A == "" || pair.B == "" || pair.A == pair.B || (pair.Threshold != 0 && pair.Threshold < 2) {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-watch: invalid oscillation_pairs[%d]", i))
			}
		}
	}

	// Resolve nested workflows recursively for stage/seat/write-capability metadata.
	visited := map[string]bool{ref: true}
	hasWrite := computeWorkflowMetadata(absRoot, wfDir, path, w, ids, seats, visited, fc)

	return loadedWorkflow{
		source:   path,
		ref:      ref,
		wf:       w,
		acyclic:  acyclic,
		hasWrite: hasWrite,
		stageIDs: ids,
		seats:    seats,
	}, true
}

func validateCommandStage(path string, s lintWorkflowStage, args map[string]lintWorkflowArg, cmds map[string]lintCommandEntry, cmdErr error, fc *findingCollector) {
	if cmdErr != nil {
		fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("command-registry: stage %q: %v", s.ID, cmdErr))
		return
	}
	if strings.HasPrefix(s.CommandRef, "{") && strings.HasSuffix(s.CommandRef, "}") {
		argName := strings.TrimSuffix(strings.TrimPrefix(s.CommandRef, "{"), "}")
		if arg, ok := args[argName]; ok && arg.Type == "command" {
			if len(cmds) == 0 {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("command-registry: stage %q: no registered commands", s.ID))
			}
			return
		}
	}
	entry, ok := cmds[s.CommandRef]
	if !ok {
		fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("command-reference: stage %q: unknown command_ref %q", s.ID, s.CommandRef))
		return
	}
	for flag, val := range s.Flags {
		kind, ok := entry.Flags[flag]
		if !ok {
			fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("command-flag: stage %q: %s does not accept %s", s.ID, s.CommandRef, flag))
			continue
		}
		switch kind {
		case "string", "path":
			str, ok := val.(string)
			if !ok {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("command-value: stage %q: %s requires %s", s.ID, flag, kind))
			} else if strings.ContainsRune(str, 0) {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("command-value: stage %q: %s contains NUL", s.ID, flag))
			}
		case "number":
			if _, ok := val.(json.Number); !ok {
				fc.add(path, CodeWatchLintWorkflowSchema, fmt.Sprintf("command-value: stage %q: %s requires number", s.ID, flag))
			}
		}
	}
}

func computeWorkflowMetadata(absRoot, wfDir, sourcePath string, w lintWorkflow, ids, seats map[string]bool, visiting map[string]bool, fc *findingCollector) bool {
	hasWrite := false
	for _, s := range w.Stages {
		if s.Access == "write" {
			hasWrite = true
		}
		if s.Repeat != "" {
			if arg, ok := w.Args[s.Repeat]; ok && arg.Type == "pieces" {
				hasWrite = true
			}
		}
		if s.Kind == "workflow" && validWorkflowRef(s.Workflow) {
			if visiting[s.Workflow] {
				fc.add(sourcePath, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-expansion-cycle: %s -> %s", w.Name, s.Workflow))
				continue
			}
			nestedPath := resolveWorkflowFile(absRoot, wfDir, s.Workflow)
			if nestedPath == "" {
				fc.add(sourcePath, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-missing-reference: stage %q references %q which was not found", s.ID, s.Workflow))
				continue
			}
			raw, err := os.ReadFile(nestedPath)
			if err != nil {
				fc.add(sourcePath, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-missing-reference: %s: %v", s.Workflow, err))
				continue
			}
			var nw lintWorkflow
			if err := json.Unmarshal(raw, &nw); err != nil {
				fc.add(sourcePath, CodeWatchLintWorkflowSchema, fmt.Sprintf("workflow-reference: %s: %v", s.Workflow, err))
				continue
			}
			for _, ns := range nw.Stages {
				if ns.ID != "" {
					ids[ns.ID] = true
				}
				if ns.Seat != "" {
					seats[ns.Seat] = true
				}
			}
			visiting[s.Workflow] = true
			if computeWorkflowMetadata(absRoot, wfDir, sourcePath, nw, ids, seats, visiting, fc) {
				hasWrite = true
			}
			delete(visiting, s.Workflow)
		}
	}
	return hasWrite
}

func resolveWorkflowFile(absRoot, wfDir, ref string) string {
	candidates := []string{}
	if wfDir != "" {
		candidates = append(candidates, filepath.Join(wfDir, ref+".json"))
	}
	defaultDir := filepath.Join(absRoot, "config", "tillr", "workflows")
	if defaultDir != wfDir {
		candidates = append(candidates, filepath.Join(defaultDir, ref+".json"))
	}
	for _, c := range candidates {
		info, err := os.Lstat(c)
		if err == nil && info.Mode().IsRegular() && info.Mode()&fs.ModeSymlink == 0 {
			return c
		}
	}
	return ""
}

func isWorkflowAcyclic(w lintWorkflow) bool {
	indegree := make(map[string]int, len(w.Stages))
	next := make(map[string][]string, len(w.Stages))
	validID := make(map[string]bool, len(w.Stages))
	for _, s := range w.Stages {
		validID[s.ID] = true
	}
	for _, e := range w.Edges {
		if !validID[e.From] || !validID[e.To] {
			continue
		}
		indegree[e.To]++
		next[e.From] = append(next[e.From], e.To)
	}
	taken := 0
	queue := make([]string, 0, len(w.Stages))
	for _, s := range w.Stages {
		if indegree[s.ID] == 0 {
			queue = append(queue, s.ID)
		}
	}
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		taken++
		for _, nxt := range next[cur] {
			indegree[nxt]--
			if indegree[nxt] == 0 {
				queue = append(queue, nxt)
			}
		}
	}
	return taken == len(w.Stages)
}

func checkWorkflowJSONFields(raw []byte) []string {
	var object map[string]json.RawMessage
	if json.Unmarshal(raw, &object) != nil {
		return nil
	}
	var errs []string
	check := func(obj map[string]json.RawMessage, allowed, ctxName string) {
		var keys []string
		for k := range obj {
			if !strings.Contains(" "+allowed+" ", " "+k+" ") {
				keys = append(keys, k)
			}
		}
		sort.Strings(keys)
		for _, k := range keys {
			errs = append(errs, fmt.Sprintf("workflow-unknown-field: %s: unknown field %q", ctxName, k))
		}
	}
	check(object, "name version stages edges exit args inputs max_parallel watch", "graph")
	if rawWatch, ok := object["watch"]; ok && string(bytes.TrimSpace(rawWatch)) != "null" {
		var watchObj map[string]json.RawMessage
		if json.Unmarshal(rawWatch, &watchObj) != nil {
			errs = append(errs, "workflow-watch: watch must be an object")
		} else {
			check(watchObj, "cycle_anchor oscillation_pairs", "watch")
			if rawCA, ok := watchObj["cycle_anchor"]; ok && string(bytes.TrimSpace(rawCA)) != "null" {
				var caObj map[string]json.RawMessage
				if json.Unmarshal(rawCA, &caObj) != nil {
					errs = append(errs, "workflow-watch: watch.cycle_anchor must be an object")
				} else {
					check(caObj, "from to max_cycles", "watch.cycle_anchor")
				}
			}
			if rawPairs, ok := watchObj["oscillation_pairs"]; ok && string(bytes.TrimSpace(rawPairs)) != "null" {
				var pairList []map[string]json.RawMessage
				if json.Unmarshal(rawPairs, &pairList) != nil {
					errs = append(errs, "workflow-watch: watch.oscillation_pairs must be a list of objects")
				} else {
					for i, pObj := range pairList {
						check(pObj, "a b threshold hint", fmt.Sprintf("watch.oscillation_pairs[%d]", i))
					}
				}
			}
		}
	}
	return errs
}

func lintWorkflowsWatchRefs(absRoot string, workflows []loadedWorkflow, wfDir string, fc *findingCollector) {
	for _, lw := range workflows {
		if lw.wf.Watch == nil {
			continue
		}
		allowedCA := make(map[string]bool, len(lw.stageIDs)+len(lw.seats))
		for id := range lw.stageIDs {
			allowedCA[id] = true
		}
		for seat := range lw.seats {
			allowedCA[seat] = true
		}
		if ca := lw.wf.Watch.CycleAnchor; ca != nil {
			if ca.From != "" && !allowedCA[ca.From] {
				fc.add(lw.source, CodeWatchLintUnknownStage, fmt.Sprintf("watch.cycle_anchor.from %q is not a declared stage or seat in %s", ca.From, lw.ref))
			}
			if ca.To != "" && !allowedCA[ca.To] {
				fc.add(lw.source, CodeWatchLintUnknownStage, fmt.Sprintf("watch.cycle_anchor.to %q is not a declared stage or seat in %s", ca.To, lw.ref))
			}
		}
		for i, pair := range lw.wf.Watch.OscillationPairs {
			if pair.A != "" && !allowedCA[pair.A] && pair.A != "design" {
				fc.add(lw.source, CodeWatchLintUnknownStage, fmt.Sprintf("watch.oscillation_pairs[%d].a %q is not a declared stage or seat in %s", i, pair.A, lw.ref))
			}
			if pair.B != "" && !allowedCA[pair.B] && pair.B != "design" {
				fc.add(lw.source, CodeWatchLintUnknownStage, fmt.Sprintf("watch.oscillation_pairs[%d].b %q is not a declared stage or seat in %s", i, pair.B, lw.ref))
			}
		}
	}
}

func lintWorkflowsWithCharter(workflows []loadedWorkflow, charter charterData, fc *findingCollector) {
	for _, lw := range workflows {
		rawPolicy := ""
		if lw.wf.Exit != nil {
			rawPolicy = lw.wf.Exit.RequiresCandidate
		}
		switch charter.def.Completes {
		case "hook":
			if rawPolicy == "commit_required" {
				fc.add(lw.source, CodeWatchLintCharterCandidate, fmt.Sprintf("charter %q completes by hook, but workflow %s requires exit.requires_candidate = commit_required", charter.def.Name, lw.ref))
			}
		case "landing":
			effPolicy := rawPolicy
			if effPolicy == "" {
				effPolicy = "commit_required"
			}
			if effPolicy == "commit_required" && !lw.hasWrite {
				fc.add(lw.source, CodeWatchLintCharterCandidate, fmt.Sprintf("workflow %s requires a candidate commit (effective policy %q) but has no write-capable stage", lw.ref, effPolicy))
			}
			if effPolicy == "no_commit" && lw.hasWrite {
				fc.add(lw.source, CodeWatchLintCharterCandidate, fmt.Sprintf("workflow %s has write-capable stages but declares exit.requires_candidate = no_commit", lw.ref))
			}
		}
	}
}

type lintBackend struct {
	Quota          []string `json:"quota,omitempty"`
	Reserve        float64  `json:"reserve,omitempty"`
	Adapter        string   `json:"adapter"`
	Model          string   `json:"model,omitempty"`
	Models         []string `json:"models,omitempty"`
	Effort         string   `json:"effort,omitempty"`
	Account        string   `json:"account,omitempty"`
	WorkingDir     string   `json:"working_dir,omitempty"`
	TimeoutSeconds int      `json:"timeout_seconds,omitempty"`
	HarnessArgs    []string `json:"harness_args,omitempty"`
}

type lintPolicy struct {
	source         string
	About          string                     `json:"about,omitempty"`
	DefaultBackend string                     `json:"default_backend"`
	Seats          map[string]json.RawMessage `json:"seats,omitempty"`
	ReadOnlySeats  []string                   `json:"read_only_seats,omitempty"`
	SeatPurposes   map[string]string          `json:"seat_purposes,omitempty"`
	Overrides      map[string]string          `json:"overrides,omitempty"`
	Escalate       map[string][]string        `json:"escalate,omitempty"`
	Backends       map[string]lintBackend     `json:"backends"`
	seatLists      map[string][]string
}

func loadAndLintPolicies(absRoot string, policyPaths []string, fc *findingCollector) ([]lintPolicy, bool) {
	var paths []string
	if len(policyPaths) > 0 {
		for _, p := range policyPaths {
			info, err := os.Stat(p)
			if err != nil {
				fc.add(p, CodeWatchLintInput, fmt.Sprintf("policy %q: %v", p, err))
				continue
			}
			if info.IsDir() {
				fc.add(p, CodeWatchLintInput, fmt.Sprintf("policy %q must be a file, not a directory", p))
				continue
			}
			paths = append(paths, p)
		}
	} else {
		defaultPath := filepath.Join(absRoot, "config", "tillr", "backend-policy.json")
		info, err := os.Stat(defaultPath)
		if err != nil {
			if os.IsNotExist(err) {
				return nil, true
			}
			fc.add(defaultPath, CodeWatchLintInput, fmt.Sprintf("policy %q: %v", defaultPath, err))
			return nil, false
		}
		if info.IsDir() {
			fc.add(defaultPath, CodeWatchLintInput, fmt.Sprintf("policy %q must be a file, not a directory", defaultPath))
			return nil, false
		}
		paths = []string{defaultPath}
	}

	var policies []lintPolicy
	for _, p := range paths {
		if lp, ok := loadAndValidatePolicyFile(absRoot, p, fc); ok {
			policies = append(policies, lp)
		}
	}
	return policies, false
}

func loadAndValidatePolicyFile(absRoot, path string, fc *findingCollector) (lintPolicy, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		fc.add(path, CodeWatchLintInput, fmt.Sprintf("reading policy %q: %v", path, err))
		return lintPolicy{}, false
	}
	if err := checkDuplicateJSONKeys(raw); err != nil {
		fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("invalid policy JSON: %v", err))
		return lintPolicy{}, false
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.DisallowUnknownFields()
	var lp lintPolicy
	if err := dec.Decode(&lp); err != nil {
		fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("policy schema: %v", err))
		return lintPolicy{}, false
	}
	if err := dec.Decode(new(any)); err != io.EOF {
		fc.add(path, CodeWatchLintSeatUnmapped, "policy must be a single JSON object")
		return lintPolicy{}, false
	}
	lp.source = path
	lp.seatLists = make(map[string][]string)

	valid := true
	if len(lp.Backends) == 0 {
		fc.add(path, CodeWatchLintSeatUnmapped, "policy backends must not be empty")
		valid = false
	}
	for name, b := range lp.Backends {
		if name == "" {
			fc.add(path, CodeWatchLintSeatUnmapped, "policy backend name must not be empty")
			valid = false
		}
		if b.Adapter == "" {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("backend %q names no adapter", name))
			valid = false
		} else if _, err := setupcheck.CheckAdapter(absRoot, b.Adapter); err != nil {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("backend %q adapter %q: %v", name, b.Adapter, err))
			valid = false
		}
		if b.Model == "" && len(b.Models) == 0 {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("backend %q must set model or models", name))
			valid = false
		}
		for _, m := range b.Models {
			if m == "" {
				fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("backend %q models contains empty string", name))
				valid = false
			}
		}
		for _, q := range b.Quota {
			if q == "" {
				fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("backend %q quota contains empty string", name))
				valid = false
			}
		}
		if math.IsNaN(b.Reserve) || math.IsInf(b.Reserve, 0) || b.Reserve < 0 || b.Reserve > 1 {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("backend %q reserve must be in [0, 1]", name))
			valid = false
		}
		if b.TimeoutSeconds < 0 {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("backend %q timeout_seconds must be >= 0", name))
			valid = false
		}
	}

	if lp.DefaultBackend == "" {
		fc.add(path, CodeWatchLintSeatUnmapped, "policy default_backend must not be empty")
		valid = false
	} else if _, ok := lp.Backends[lp.DefaultBackend]; !ok {
		fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("policy default_backend %q is not in backends", lp.DefaultBackend))
		valid = false
	}

	for seat, rawVal := range lp.Seats {
		if !validSeatName(seat) {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("invalid seat name %q", seat))
			valid = false
		}
		names, err := parseSeatBackends(rawVal)
		if err != nil {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("seat %q: %v", seat, err))
			valid = false
			continue
		}
		if len(names) == 0 {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("seat %q has no backends", seat))
			valid = false
			continue
		}
		for _, bname := range names {
			if _, ok := lp.Backends[bname]; !ok {
				fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("seat %q references unknown backend %q", seat, bname))
				valid = false
			}
		}
		lp.seatLists[seat] = names
	}

	seenRO := make(map[string]bool)
	for _, seat := range lp.ReadOnlySeats {
		if seenRO[seat] {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("duplicate read_only_seats entry %q", seat))
			valid = false
		}
		seenRO[seat] = true
		if len(lp.seatLists[seat]) == 0 {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("read_only_seats names unmapped seat %q", seat))
			valid = false
		}
	}
	for seat := range lp.SeatPurposes {
		if len(lp.seatLists[seat]) == 0 {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("seat_purposes names unmapped seat %q", seat))
			valid = false
		}
	}
	for itemID, bname := range lp.Overrides {
		if _, ok := lp.Backends[bname]; !ok {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("override %q references unknown backend %q", itemID, bname))
			valid = false
		}
	}
	for seat, list := range lp.Escalate {
		if len(lp.seatLists[seat]) == 0 {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("escalate names unmapped seat %q", seat))
			valid = false
		}
		if seat == "triage" && len(list) > 0 {
			fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("escalate[%q] must be empty: triage never escalates to another model tier", seat))
			valid = false
		}
		for _, bname := range list {
			if _, ok := lp.Backends[bname]; !ok {
				fc.add(path, CodeWatchLintSeatUnmapped, fmt.Sprintf("escalate[%q] references unknown backend %q", seat, bname))
				valid = false
			}
		}
	}
	if len(lp.seatLists["triage"]) > 0 && !seenRO["triage"] {
		fc.add(path, CodeWatchLintSeatUnmapped, `policy declares seat "triage", which must be listed in read_only_seats`)
		valid = false
	}
	return lp, valid
}

func parseSeatBackends(raw json.RawMessage) ([]string, error) {
	var single string
	if json.Unmarshal(raw, &single) == nil {
		if single == "" {
			return nil, fmt.Errorf("backend name must not be empty")
		}
		return []string{single}, nil
	}
	trimmed := bytes.TrimSpace(raw)
	if len(trimmed) > 0 && trimmed[0] == '{' {
		var cfg struct {
			Backends json.RawMessage `json:"backends"`
			Pick     string          `json:"pick"`
		}
		dec := json.NewDecoder(bytes.NewReader(raw))
		dec.DisallowUnknownFields()
		if err := dec.Decode(&cfg); err != nil {
			return nil, err
		}
		if cfg.Backends == nil {
			return nil, fmt.Errorf("seat object requires backends")
		}
		if cfg.Pick != "" && cfg.Pick != "in-order" && cfg.Pick != "round-robin" && cfg.Pick != "random" {
			return nil, fmt.Errorf("invalid pick %q", cfg.Pick)
		}
		return parseSeatBackends(cfg.Backends)
	}
	var entries []json.RawMessage
	if err := json.Unmarshal(raw, &entries); err != nil {
		return nil, fmt.Errorf("expected backend string, list, or {backends, pick}")
	}
	if len(entries) == 0 {
		return nil, fmt.Errorf("backend list must not be empty")
	}
	var names []string
	seen := make(map[string]bool)
	shared, unshared := false, false
	for _, entry := range entries {
		var name string
		if json.Unmarshal(entry, &name) == nil {
			unshared = true
		} else {
			var obj struct {
				Backend string          `json:"backend"`
				Share   json.RawMessage `json:"share"`
			}
			dec := json.NewDecoder(bytes.NewReader(entry))
			dec.DisallowUnknownFields()
			if err := dec.Decode(&obj); err != nil {
				return nil, err
			}
			name = obj.Backend
			if obj.Share == nil {
				unshared = true
			} else {
				shared = true
				var share float64
				if string(bytes.TrimSpace(obj.Share)) == "null" || json.Unmarshal(obj.Share, &share) != nil || math.IsNaN(share) || math.IsInf(share, 0) || share <= 0 {
					return nil, fmt.Errorf("backend %q share must be a positive finite number", name)
				}
			}
		}
		if name == "" {
			return nil, fmt.Errorf("backend name must not be empty")
		}
		if seen[name] {
			return nil, fmt.Errorf("duplicate backend %q", name)
		}
		seen[name] = true
		names = append(names, name)
	}
	if shared && unshared {
		return nil, fmt.Errorf("seat list must give every backend a share or none")
	}
	if len(names) >= 2 && !shared {
		return nil, fmt.Errorf("seat list with 2+ backends must specify share on every entry")
	}
	return names, nil
}

func lintWorkflowsWithPolicies(absRoot string, workflows []loadedWorkflow, policies []lintPolicy, missingDefaultPolicy bool, fc *findingCollector) {
	if len(policies) == 0 {
		if !missingDefaultPolicy {
			return
		}
		for _, lw := range workflows {
			if workflowHasAgentStage(absRoot, lw.source, lw.wf, map[string]bool{lw.ref: true}) {
				defaultPolicy := filepath.Join(absRoot, "config", "tillr", "backend-policy.json")
				fc.add(defaultPolicy, CodeWatchLintSeatUnmapped, fmt.Sprintf("no backend policy loaded, but workflow %s declares agent stages", lw.ref))
				break
			}
		}
		return
	}
	for _, lw := range workflows {
		checkWorkflowStagesAgainstPolicies(absRoot, lw.source, lw.ref, lw.wf, policies, map[string]bool{lw.ref: true}, fc)
	}
}

func workflowHasAgentStage(absRoot, sourcePath string, w lintWorkflow, visiting map[string]bool) bool {
	wfDir := filepath.Dir(sourcePath)
	for _, s := range w.Stages {
		if s.Kind == "agent" {
			return true
		}
		if s.Kind == "workflow" && validWorkflowRef(s.Workflow) && !visiting[s.Workflow] {
			if nestedPath := resolveWorkflowFile(absRoot, wfDir, s.Workflow); nestedPath != "" {
				if raw, err := os.ReadFile(nestedPath); err == nil {
					var nw lintWorkflow
					if json.Unmarshal(raw, &nw) == nil {
						visiting[s.Workflow] = true
						has := workflowHasAgentStage(absRoot, nestedPath, nw, visiting)
						delete(visiting, s.Workflow)
						if has {
							return true
						}
					}
				}
			}
		}
	}
	return false
}

func checkWorkflowStagesAgainstPolicies(absRoot, sourcePath, topRef string, w lintWorkflow, policies []lintPolicy, visiting map[string]bool, fc *findingCollector) {
	wfDir := filepath.Dir(sourcePath)
	for _, s := range w.Stages {
		if s.Kind == "agent" {
			for _, p := range policies {
				if len(p.seatLists[s.Seat]) == 0 {
					fc.add(sourcePath, CodeWatchLintSeatUnmapped, fmt.Sprintf("stage %q in %s targets seat %q which is not mapped in %s", s.ID, topRef, s.Seat, fc.formatSource(p.source)))
				}
				if s.Access == "write" && slices.Contains(p.ReadOnlySeats, s.Seat) {
					fc.add(sourcePath, CodeWatchLintSeatUnmapped, fmt.Sprintf("stage %q in %s assigns access=write to read_only_seat %q in %s", s.ID, topRef, s.Seat, fc.formatSource(p.source)))
				}
			}
		} else if s.Kind == "workflow" && validWorkflowRef(s.Workflow) && !visiting[s.Workflow] {
			if nestedPath := resolveWorkflowFile(absRoot, wfDir, s.Workflow); nestedPath != "" {
				if raw, err := os.ReadFile(nestedPath); err == nil {
					var nw lintWorkflow
					if json.Unmarshal(raw, &nw) == nil {
						visiting[s.Workflow] = true
						checkWorkflowStagesAgainstPolicies(absRoot, sourcePath, topRef, nw, policies, visiting, fc)
						delete(visiting, s.Workflow)
					}
				}
			}
		}
	}
}

func lintWorkflowsWithRules(workflows []loadedWorkflow, rules []lintedRule, fc *findingCollector) {
	for _, lw := range workflows {
		for _, v := range lw.wf.Stages {
			if v.Kind != "agent" || v.Seat != "verify" {
				continue
			}
			ancestors := workflowAncestors(lw.wf, v.ID)
			var planTargets, writeTargets []lintWorkflowStage
			for _, u := range lw.wf.Stages {
				if !ancestors[u.ID] {
					continue
				}
				if u.Seat == "plan" {
					planTargets = append(planTargets, u)
				}
				if stageCanWriteStatic(lw.wf, u) {
					writeTargets = append(writeTargets, u)
				}
			}
			var derived []lintWorkflowStage
			if len(planTargets) == 1 {
				derived = append(derived, planTargets[0])
			}
			if len(writeTargets) == 1 {
				derived = append(derived, writeTargets[0])
			}
			for _, target := range derived {
				if !isReturnPathBounded(lw, v, target, rules) {
					fc.add(lw.source, CodeWatchLintUnboundedReturn, fmt.Sprintf("verifier return path (%s -> %s) in %s has no workflow.watch bound or standing per-item watch rule", v.ID, target.ID, lw.ref))
				}
			}
		}
	}
}

func workflowAncestors(w lintWorkflow, targetID string) map[string]bool {
	rev := make(map[string][]string)
	for _, e := range w.Edges {
		rev[e.To] = append(rev[e.To], e.From)
	}
	seen := make(map[string]bool)
	queue := append([]string(nil), rev[targetID]...)
	for len(queue) > 0 {
		cur := queue[0]
		queue = queue[1:]
		if cur == targetID || seen[cur] {
			continue
		}
		seen[cur] = true
		queue = append(queue, rev[cur]...)
	}
	return seen
}

func stageCanWriteStatic(w lintWorkflow, s lintWorkflowStage) bool {
	if s.Access == "write" {
		return true
	}
	if s.Repeat != "" {
		if arg, ok := w.Args[s.Repeat]; ok && arg.Type == "pieces" {
			return true
		}
	}
	return false
}

func isReturnPathBounded(lw loadedWorkflow, v, target lintWorkflowStage, rules []lintedRule) bool {
	if lw.wf.Watch != nil {
		if ca := lw.wf.Watch.CycleAnchor; ca != nil && ca.MaxCycles >= 1 {
			if (ca.From == v.ID && ca.To == target.ID) || (target.Seat != "" && ca.From == v.Seat && ca.To == target.Seat) {
				return true
			}
		}
		for _, pair := range lw.wf.Watch.OscillationPairs {
			if pair.Threshold != 0 && pair.Threshold < 2 {
				continue
			}
			if (pair.A == v.ID && pair.B == target.ID) || (pair.A == target.ID && pair.B == v.ID) {
				return true
			}
			if target.Seat != "" && ((pair.A == v.Seat && pair.B == target.Seat) || (pair.A == target.Seat && pair.B == v.Seat)) {
				return true
			}
		}
	}

	boundingEvents := map[string]bool{
		"drain.refusal":       true,
		"run.allocate":        true,
		"run.record":          true,
		"run.finish":          true,
		"drain.started":       true,
		"drain.node-advanced": true,
	}
	for _, lr := range rules {
		r := lr.rule
		if !r.Enabled {
			continue
		}
		if r.Scope != "core" && r.Scope != "workflow:*" && r.Scope != "workflow:"+lw.wf.Name && r.Scope != "workflow:"+lw.ref {
			continue
		}
		if r.When.Pattern == AlternatingPair {
			return true
		}
		if r.When.Pattern == CountInWindow && r.When.DistinctBy == "" {
			perItem := r.When.GroupBy == "item_id" || r.When.GroupBy == "object_id" ||
				slices.Contains(r.When.GroupByList, "item_id") || slices.Contains(r.When.GroupByAny, "item_id")
			if !perItem {
				continue
			}
			for _, ev := range r.Reads.Events {
				if boundingEvents[ev] {
					return true
				}
			}
		}
	}
	return false
}

func validWorkflowRef(ref string) bool {
	name, verStr, ok := strings.Cut(ref, "@v")
	if !ok || name == "" || verStr == "" {
		return false
	}
	for i := 0; i < len(name); i++ {
		c := name[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	if verStr[0] < '1' || verStr[0] > '9' {
		return false
	}
	for i := 1; i < len(verStr); i++ {
		if verStr[i] < '0' || verStr[i] > '9' {
			return false
		}
	}
	return true
}

func validSeatName(seat string) bool {
	if len(seat) == 0 || seat[0] < 'a' || seat[0] > 'z' {
		return false
	}
	for i := 1; i < len(seat); i++ {
		c := seat[i]
		if !((c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') || c == '-') {
			return false
		}
	}
	return true
}
