package workflow

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
)

const (
	// ReasonNodeScopeOutsideFence is the refusal reason when a stage's write_scope leaves its fence.
	ReasonNodeScopeOutsideFence = "node-scope-outside-fence"

	// InvalidPolicySeatName formats invalid seat name errors.
	InvalidPolicySeatName = "seat %q must start with a lowercase letter and contain only lowercase letters, digits and hyphens; %s"
	// DefaultSeatDeclaration is the default seat declaration location hint.
	DefaultSeatDeclaration = "seats are declared in the backend policy file"

	tillrSkillPath  = "yolo-packs/tillr/skills/tillr-operating/SKILL.md"
	tillrDocsTest   = "tests/tillr-docs.test.py"
	museBackendTest = "tests/tillr-muse-backend.test.py"
)

// SeatPolicy describes the seat mappings and read-only constraints enforced
// when expanding a workflow. A nil *SeatPolicy skips policy seat validation.
type SeatPolicy struct {
	Seats           map[string]string
	ReadOnlySeats   []string
	SeatDeclaration string
}

// CommandEntry registers a fixed program and argv. Flag types are string, number,
// or path (a relative repository path); workflow data is appended as argv only.
type CommandEntry struct {
	Argv  []string          `json:"argv"`
	Flags map[string]string `json:"flags"`
}

// NodeInput records a producer node and its output file path for a consumer input.
type NodeInput struct {
	Producer string `json:"producer"`
	Path     string `json:"path"`
}

// Workflow is a versioned, pre-approved sequence of agent and command runs.
// Edges order stages and select outcomes; ready readers share one lane.
type Workflow struct {
	MaxParallel    int                         `json:"max_parallel,omitempty"`
	Name           string                      `json:"name"`
	Version        int                         `json:"version"`
	Stages         []WorkflowStage             `json:"stages"`
	Edges          []WorkflowEdge              `json:"edges"`
	Exit           *WorkflowExit               `json:"exit,omitempty"`
	SHA256         string                      `json:"sha256,omitempty"`
	Sources        map[string]string           `json:"sources,omitempty"`
	Args           map[string]WorkflowArgument `json:"args,omitempty"`
	Inputs         map[string]string           `json:"inputs,omitempty"`
	ExpansionDepth int                         `json:"expansion_depth,omitempty"`
	Revision       int                         `json:"revision,omitempty"`
	Groups         map[string]WorkflowGroup    `json:"groups,omitempty"`
	Watch          *WorkflowWatch              `json:"watch,omitempty"`
}

// WorkflowWatch declares optional per-workflow cycle and oscillation bounds.
type WorkflowWatch struct {
	CycleAnchor      *WorkflowCycleAnchor      `json:"cycle_anchor,omitempty"`
	OscillationPairs []WorkflowOscillationPair `json:"oscillation_pairs,omitempty"`
}

// WorkflowCycleAnchor bounds an expected return path between two stages.
type WorkflowCycleAnchor struct {
	From      string `json:"from"`
	To        string `json:"to"`
	MaxCycles int    `json:"max_cycles"`
}

// WorkflowOscillationPair bounds back-and-forth transitions between two stages.
type WorkflowOscillationPair struct {
	A         string `json:"a"`
	B         string `json:"b"`
	Threshold int    `json:"threshold,omitempty"`
	Hint      string `json:"hint,omitempty"`
}

// WorkflowExit names the stages required to pass; omission requires every stage.
type WorkflowExit struct {
	EvaluatorSlot     string                  `json:"evaluator_slot,omitempty"`
	RequiresNodes     []string                `json:"requires_nodes"`
	RequiresCandidate string                  `json:"requires_candidate,omitempty"`
	Outputs           map[string]WorkflowPort `json:"outputs,omitempty"`
}

// WorkflowGroup retains the interface of an expanded stage for later replacements.
type WorkflowGroup struct {
	Stage   WorkflowStage           `json:"stage"`
	Outputs map[string]WorkflowPort `json:"outputs"`
}

// WorkflowStage defines one stage in a workflow graph.
type WorkflowStage struct {
	// CopiesOf is expansion-owned provenance, persisted in graph snapshots but
	// refused in tracked files. Only copies of one stage may share an input.
	CopiesOf     string              `json:"copies_of,omitempty"`
	MayReplace   map[string][]string `json:"may_replace,omitempty"`
	ID           string              `json:"id"`
	Parent       string              `json:"parent,omitempty"`
	Args         map[string]any      `json:"args,omitempty"`
	Repeat       string              `json:"repeat,omitempty"`
	Prompt       string              `json:"prompt,omitempty"`
	WriteScope   WorkflowScope       `json:"write_scope,omitempty"`
	Workflow     string              `json:"workflow,omitempty"`
	Kind         string              `json:"kind"`
	Seat         string              `json:"seat"`
	Access       string              `json:"access"`
	CommandRef   string              `json:"command_ref,omitempty"`
	Flags        map[string]any      `json:"flags,omitempty"`
	Command      []string            `json:"resolved_argv,omitempty"`
	TimeoutMS    int                 `json:"timeout_ms,omitempty"`
	Inputs       map[string]string   `json:"inputs,omitempty"`
	Outputs      map[string]string   `json:"outputs,omitempty"`
	JudgeClasses []string            `json:"judge_classes,omitempty"`
}

// WorkflowEdge defines a dependency and optional outcome/port wiring between stages.
type WorkflowEdge struct {
	From        string `json:"from_node"`
	To          string `json:"to_node"`
	WhenOutcome string `json:"when_outcome,omitempty"`
	FromOutput  string `json:"from_output,omitempty"`
	ToInput     string `json:"to_input,omitempty"`
	// Expanded edges out of a nested workflow require every inner exit node
	// to pass, even though ordinary outcome branches are alternatives.
	RequiresPassed []string `json:"requires_passed,omitempty"`
}

// DecodeWorkflow parses and validates a single workflow JSON document.
func DecodeWorkflow(raw []byte, ref string) (*Workflow, error) {
	if findings := workflowFields(raw); len(findings) != 0 {
		return nil, errors.Join(findings...)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	d.UseNumber()
	var w Workflow
	if err := d.Decode(&w); err != nil {
		rule := "workflow-json"
		if strings.Contains(err.Error(), "unknown field") {
			rule = "workflow-unknown-field"
		}
		return nil, fmt.Errorf("%s: %w", rule, err)
	}
	if err := d.Decode(new(any)); err != io.EOF {
		return nil, fmt.Errorf("workflow-json: trailing content")
	}
	if !item.WorkflowRefValid(ref) || w.Name+"@v"+strconv.Itoa(w.Version) != ref {
		return nil, fmt.Errorf("workflow-identity: name and version must match %s", ref)
	}
	if w.MaxParallel < 0 || w.MaxParallel > 8 {
		return nil, fmt.Errorf("workflow-max-parallel: expected 1 through 8, or omitted")
	}
	if w.MaxParallel == 0 {
		w.MaxParallel = 4
	}
	if w.Edges == nil {
		return nil, fmt.Errorf("workflow-edges: edges must be a list")
	}
	if len(w.Stages) == 0 {
		return nil, fmt.Errorf("workflow-empty-stages: %s", ref)
	}
	ids := map[string]bool{}
	for i := range w.Stages {
		s := &w.Stages[i]
		if s.ID == "" || strings.ContainsAny(s.ID, "/\r\n\t") {
			return nil, fmt.Errorf("workflow-stage-id: %q: id must be nonempty and contain no slash or control whitespace", s.ID)
		}
		if ids[s.ID] {
			return nil, fmt.Errorf("workflow-duplicate-id: %s", s.ID)
		}
		ids[s.ID] = true
		proposalCount := 0
		for _, typ := range s.Outputs {
			if typ == "graph.proposal" {
				proposalCount++
			}
		}
		if proposalCount > 1 {
			return nil, fmt.Errorf("workflow-proposal: %s: at most one proposal output", s.ID)
		}
		for target, refs := range s.MayReplace {
			if target == "" || len(refs) == 0 {
				return nil, fmt.Errorf("workflow-proposal: %s: target and workflows required", s.ID)
			}
			for _, ref := range refs {
				if !item.WorkflowRefValid(ref) {
					return nil, fmt.Errorf("workflow-proposal: %s: invalid workflow %q", s.ID, ref)
				}
			}
		}
		if s.Kind != "agent" && s.Kind != "command" && s.Kind != "workflow" {
			return nil, fmt.Errorf("workflow-kind: %s: expected agent, command or workflow", s.ID)
		}
		if s.Kind == "workflow" {
			if !item.WorkflowRefValid(s.Workflow) {
				return nil, fmt.Errorf("workflow-reference: %s: %q", s.ID, s.Workflow)
			}
			if s.Seat != "" || s.Access != "" || s.CommandRef != "" || s.Flags != nil || s.TimeoutMS != 0 || len(s.Command) != 0 {
				return nil, fmt.Errorf("workflow-kind: %s: workflow stage cannot specify run fields", s.ID)
			}
			continue
		}
		if s.Args != nil {
			return nil, fmt.Errorf("workflow-kind: %s: only workflow stages pass args", s.ID)
		}
		if s.Workflow != "" {
			return nil, fmt.Errorf("workflow-kind: %s: only workflow stages name a workflow", s.ID)
		}
		if len(s.Command) != 0 {
			return nil, fmt.Errorf("workflow-command: resolved_argv is internal, not workflow input")
		}
		if s.Kind == "command" && (s.CommandRef == "" || s.Seat != "") {
			return nil, fmt.Errorf("workflow-kind: %s: command needs command_ref and no seat", s.ID)
		}
		if s.Kind == "agent" && (s.CommandRef != "" || s.Flags != nil) {
			return nil, fmt.Errorf("workflow-command: agent cannot name command flags")
		}
		if s.TimeoutMS < 0 {
			return nil, fmt.Errorf("workflow-timeout: %s", s.ID)
		}
		if s.Kind == "agent" && !item.ValidSeatName(s.Seat) {
			return nil, fmt.Errorf("workflow-unknown-seat: %s: "+InvalidPolicySeatName, s.ID, s.Seat, DefaultSeatDeclaration)
		}
		if s.Access != "read_only" && s.Access != "write" {
			return nil, fmt.Errorf("workflow-access: %s", s.ID)
		}
		if s.WriteScope.explicit && s.Access == "read_only" {
			return nil, fmt.Errorf("workflow-write-scope-read-only: %s", s.ID)
		}
		if s.Access == "write" {
			if _, err := s.ScopePaths(nil); err != nil {
				return nil, err
			}
		}
	}
	if err := ValidateTemplateWiring(&w); err != nil {
		return nil, err
	}
	hasNestedWorkflow := false
	seats := map[string]bool{}
	for _, s := range w.Stages {
		if s.Kind == "workflow" {
			hasNestedWorkflow = true
		}
		if s.Seat != "" {
			seats[s.Seat] = true
		}
	}
	if w.Watch != nil {
		if ca := w.Watch.CycleAnchor; ca != nil {
			if ca.From == "" || ca.To == "" {
				return nil, fmt.Errorf("workflow-watch: watch.cycle_anchor: from and to must be non-empty")
			}
			if ca.From == ca.To {
				return nil, fmt.Errorf("workflow-watch: watch.cycle_anchor: from and to must differ")
			}
			if ca.MaxCycles < 1 {
				return nil, fmt.Errorf("workflow-watch: watch.cycle_anchor.max_cycles: must be >= 1")
			}
		}
		for i := range w.Watch.OscillationPairs {
			pair := &w.Watch.OscillationPairs[i]
			if pair.A == "" || pair.B == "" {
				return nil, fmt.Errorf("workflow-watch: watch.oscillation_pairs[%d]: a and b must be non-empty", i)
			}
			if pair.A == pair.B {
				return nil, fmt.Errorf("workflow-watch: watch.oscillation_pairs[%d]: a and b must differ", i)
			}
			if pair.Threshold == 0 {
				pair.Threshold = 4
			}
			if pair.Threshold < 2 {
				return nil, fmt.Errorf("workflow-watch: watch.oscillation_pairs[%d].threshold: must be >= 2", i)
			}
		}
		if !hasNestedWorkflow {
			if err := ValidateWorkflowWatchRefs(&w, ids, seats); err != nil {
				return nil, err
			}
		}
	}
	if w.Exit != nil {
		if w.Exit.RequiresCandidate != "" && w.Exit.RequiresCandidate != "commit_required" && w.Exit.RequiresCandidate != "commit_optional" && w.Exit.RequiresCandidate != "no_commit" {
			return nil, fmt.Errorf("workflow-exit: exit: requires_candidate must be commit_required, commit_optional or no_commit")
		}
		if len(w.Exit.RequiresNodes) == 0 {
			return nil, fmt.Errorf("workflow-exit: exit: requires_nodes must not be empty")
		}
		for _, id := range w.Exit.RequiresNodes {
			if !ids[id] {
				return nil, fmt.Errorf("workflow-exit: exit: unknown stage %q", id)
			}
		}
	}
	indegree := map[string]int{}
	next := map[string][]string{}
	for _, e := range w.Edges {
		if e.WhenOutcome != "" && e.WhenOutcome != "pass" && e.WhenOutcome != "fail" {
			return nil, fmt.Errorf("workflow-outcome: %s: %s", e.To, e.WhenOutcome)
		}
		if !ids[e.From] || !ids[e.To] {
			return nil, fmt.Errorf("workflow-unknown-node: %s -> %s", e.From, e.To)
		}
		indegree[e.To]++
		next[e.From] = append(next[e.From], e.To)
	}
	ordered := []WorkflowStage{}
	taken := map[string]bool{}
	for len(ordered) < len(w.Stages) {
		found := false
		for _, s := range w.Stages {
			if !taken[s.ID] && indegree[s.ID] == 0 {
				ordered = append(ordered, s)
				taken[s.ID] = true
				for _, id := range next[s.ID] {
					indegree[id]--
				}
				found = true
				break
			}
		}
		if !found {
			return nil, fmt.Errorf("workflow-cycle: %s", ref)
		}
	}
	w.Stages = ordered
	for _, stage := range w.Stages {
		for target := range stage.MayReplace {
			if !ids[target] || !ProposalLater(&w, stage.ID, target) {
				return nil, fmt.Errorf("workflow-proposal: %s: %s is not a later stage", stage.ID, target)
			}
		}
	}
	if err := ValidateEvaluatorJudgeClasses(&w); err != nil {
		return nil, err
	}
	if err := ValidateWorkflowExit(&w); err != nil {
		return nil, err
	}
	return &w, nil
}

// ValidateWorkflowWatchRefs validates that watch references refer to declared stages or seats.
func ValidateWorkflowWatchRefs(w *Workflow, stageIDs, seats map[string]bool) error {
	if w.Watch == nil {
		return nil
	}
	allowedCA := make(map[string]bool, len(stageIDs)+len(seats))
	for id := range stageIDs {
		allowedCA[id] = true
	}
	for seat := range seats {
		allowedCA[seat] = true
	}
	if ca := w.Watch.CycleAnchor; ca != nil {
		if !allowedCA[ca.From] {
			return fmt.Errorf("workflow-watch: watch.cycle_anchor.from: unknown stage %q", ca.From)
		}
		if !allowedCA[ca.To] {
			return fmt.Errorf("workflow-watch: watch.cycle_anchor.to: unknown stage %q", ca.To)
		}
	}
	for i, pair := range w.Watch.OscillationPairs {
		if !allowedCA[pair.A] && pair.A != "design" {
			return fmt.Errorf("workflow-watch: watch.oscillation_pairs[%d].a: unknown stage %q", i, pair.A)
		}
		if !allowedCA[pair.B] && pair.B != "design" {
			return fmt.Errorf("workflow-watch: watch.oscillation_pairs[%d].b: unknown stage %q", i, pair.B)
		}
	}
	return nil
}

// ValidateEvaluatorJudgeClasses validates judge_classes on the evaluator stage.
func ValidateEvaluatorJudgeClasses(w *Workflow) error {
	id := EvaluatorSlot(w)
	for _, stage := range w.Stages {
		if stage.ID != id {
			if stage.JudgeClasses != nil {
				return fmt.Errorf("workflow-evaluator-judge-classes: stage=%s: judge_classes belongs only on the evaluator", stage.ID)
			}
			continue
		}
		if len(stage.JudgeClasses) == 0 {
			return fmt.Errorf("workflow-evaluator-judge-classes: stage=%s: judge_classes must be a nonempty list", id)
		}
		seen := map[string]bool{}
		for _, class := range stage.JudgeClasses {
			if class != item.ClaimVerification && class != item.ClaimRedGateTriage && class != item.ClaimDirection {
				return fmt.Errorf("workflow-evaluator-judge-classes: stage=%s: class %q is not allowed; expected verification-only, red-gate-triage or direction", id, class)
			}
			if seen[class] {
				return fmt.Errorf("workflow-evaluator-judge-classes: stage=%s: duplicate class %q", id, class)
			}
			seen[class] = true
		}
	}
	return nil
}

// LoadWorkflow loads and expands a named workflow under root.
func LoadWorkflow(root, ref string, policy *SeatPolicy) (*Workflow, error) {
	return LoadWorkflowWith(root, ref, policy, nil)
}

// LoadWorkflowWith binds the workflow's top-level args from the item's own record, the
// one source of them outside a containing stage. Validation is the same as for a stage's
// args: a missing, unknown, mistyped or oversized argument refuses the load.
func LoadWorkflowWith(root, ref string, policy *SeatPolicy, args map[string]any) (*Workflow, error) {
	if !item.WorkflowRefValid(ref) {
		return nil, fmt.Errorf("workflow-reference: %q", ref)
	}
	p := filepath.Join(root, "config", "tillr", "workflows", ref+".json")
	w, err := ExpandWorkflow(root, p, policy, map[string]bool{}, args, nil)
	if err == nil {
		err = ValidateOutputWiring(w)
	}
	return w, err
}

// ScopePaths expands a node's explicit paths by the same rules as an item's fence.
// When WriteScope.Paths is nil (inherited) and allowed is empty, it returns nil.
func (s WorkflowStage) ScopePaths(allowed []string) ([]string, error) {
	if s.WriteScope.Paths == nil {
		if len(allowed) == 0 {
			return nil, nil
		}
		return FencePaths(allowed), nil
	}
	paths := append([]string(nil), s.WriteScope.Paths...)
	if len(paths) == 0 {
		return nil, fmt.Errorf("workflow-write-scope-empty: %s", s.ID)
	}
	for i, p := range paths {
		// A staged fence has one path per line; paths cannot inject entries
		// or disappear under its whitespace-trimming parser.
		if strings.TrimSpace(p) == "" || p != strings.TrimSpace(p) || strings.ContainsAny(p, "\r\n\x00") {
			return nil, fmt.Errorf("workflow-write-scope: %s: expected nonempty single-line paths without surrounding whitespace", s.ID)
		}
		paths[i] = path.Clean(p)
		if strings.HasSuffix(p, "/") {
			paths[i] += "/"
		}
	}
	return FencePaths(paths), nil
}

// CheckScopes verifies every write stage's write_scope stays inside allowed.
func (w *Workflow) CheckScopes(allowed []string) error {
	var fence []string
	if len(allowed) > 0 {
		fence = FencePaths(allowed)
		for i := range fence {
			fence[i] = path.Clean(fence[i])
		}
	}
	for _, stage := range w.Stages {
		if stage.Access != "write" {
			continue
		}
		for _, raw := range stage.WriteScope.Paths {
			for _, seg := range strings.Split(filepath.ToSlash(raw), "/") {
				if seg == ".." {
					return fmt.Errorf("%s: node %s: %s traverses parent directory", ReasonNodeScopeOutsideFence, stage.ID, raw)
				}
			}
		}
		scope, err := stage.ScopePaths(allowed)
		if err != nil {
			return err
		}
		for _, p := range scope {
			clean := path.Clean(p)
			if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || (len(fence) > 0 && !InsideFence(clean, fence)) {
				return fmt.Errorf("%s: node %s: %s is outside the item's fence", ReasonNodeScopeOutsideFence, stage.ID, p)
			}
		}
	}
	return nil
}

// EvaluatorSlot returns the configured evaluator node ID, or "" if none.
func EvaluatorSlot(w *Workflow) string {
	if w == nil || w.Exit == nil {
		return ""
	}
	return w.Exit.EvaluatorSlot
}

// ValidateEvaluator checks that a declared evaluator slot exists, is read-only,
// is required by exit, and follows every writing stage.
func ValidateEvaluator(w *Workflow) error {
	id := EvaluatorSlot(w)
	if id == "" {
		return nil
	}
	found := false
	for _, node := range w.Stages {
		if node.ID == id {
			found = true
			if node.Kind != "agent" || node.Access != "read_only" {
				return fmt.Errorf("workflow-evaluator-access: exit: %s must be read-only", id)
			}
		}
	}
	if !found {
		return fmt.Errorf("workflow-evaluator-access: exit: unknown evaluator %s", id)
	}
	required := false
	for _, node := range RequiredWorkflowNodes(w) {
		required = required || node == id
	}
	if !required {
		return fmt.Errorf("workflow-evaluator-required: exit: %s must be required", id)
	}
	for _, node := range w.Stages {
		if node.Access == "write" && !ProposalLater(w, node.ID, id) {
			return fmt.Errorf("workflow-evaluator-order: exit: writer %s must precede %s", node.ID, id)
		}
	}
	return nil
}

// EvaluatorInputFiles formats node input paths for evaluator receipts.
func EvaluatorInputFiles(inputs map[string][]NodeInput) map[string]string {
	files := make(map[string]string, len(inputs))
	for name, copies := range inputs {
		paths := make([]string, 0, len(copies))
		for _, input := range copies {
			paths = append(paths, input.Path)
		}
		files[name] = strings.Join(paths, "\n")
	}
	return files
}

// WorkflowCandidatePolicy returns the effective candidate commit policy for w.
func WorkflowCandidatePolicy(w *Workflow) string {
	if w.Exit == nil || w.Exit.RequiresCandidate == "" {
		return "commit_required"
	}
	return w.Exit.RequiresCandidate
}

// ValidateWorkflowExit checks evaluator validity and ensures all required nodes
// are reachable without requiring another required node to fail.
func ValidateWorkflowExit(w *Workflow) error {
	if err := ValidateEvaluator(w); err != nil {
		return err
	}
	required := map[string]bool{}
	reachable := map[string]bool{}
	for _, id := range RequiredWorkflowNodes(w) {
		required[id] = true
	}
	for _, stage := range w.Stages {
		incoming, possible := false, false
		for _, edge := range w.Edges {
			if edge.To == stage.ID {
				incoming = true
				if reachable[edge.From] && (edge.WhenOutcome != "fail" || !required[edge.From]) {
					possible = true
				}
			}
		}
		reachable[stage.ID] = !incoming || possible
	}
	for _, id := range RequiredWorkflowNodes(w) {
		if !reachable[id] {
			return fmt.Errorf("workflow-exit: exit: required node %s is reachable only after another required node fails", id)
		}
	}
	return nil
}

// WorkflowDone reports whether all required nodes in w have outcome "passed".
func WorkflowDone(w *Workflow, outcomes map[string]string) bool {
	for _, id := range RequiredWorkflowNodes(w) {
		if outcomes[id] != "passed" {
			return false
		}
	}
	return true
}

// InGraphGroup reports whether id equals target or is a child within target's subtree.
func InGraphGroup(id, target string) bool {
	return id == target || strings.HasPrefix(id, target+"/")
}

// ProposalLater reports whether target (or a node in target's group) is strictly
// downstream of proposer in w.Edges.
func ProposalLater(w *Workflow, proposer, target string) bool {
	if InGraphGroup(proposer, target) {
		return false
	}
	seen := map[string]bool{proposer: true}
	for changed := true; changed; {
		changed = false
		for _, e := range w.Edges {
			if seen[e.From] && !seen[e.To] {
				seen[e.To] = true
				changed = true
			}
		}
	}
	for _, s := range w.Stages {
		if InGraphGroup(s.ID, target) && seen[s.ID] {
			return true
		}
	}
	return false
}

// OutputNameValid reports whether name is a valid workflow port/argument identifier.
func OutputNameValid(name string) bool {
	if name == "" || name == "." || name == ".." {
		return false
	}
	for _, c := range name {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '_' || c == '-' || c == '.') {
			return false
		}
	}
	return true
}

// OutputTypeValid reports whether typ is a supported workflow port type.
func OutputTypeValid(typ string) bool {
	return typ == "markdown" || typ == "json" || typ == "patch" || typ == "graph.proposal"
}

// SortedNames returns the sorted keys of m.
func SortedNames[V any](m map[string]V) []string {
	names := make([]string, 0, len(m))
	for name := range m {
		names = append(names, name)
	}
	sort.Strings(names)
	return names
}

// ValidateOutputWiring verifies all declared stage inputs and outputs and edge port bindings.
func ValidateOutputWiring(w *Workflow) error {
	stages := map[string]WorkflowStage{}
	fed := map[string]map[string]WorkflowEdge{}
	seen := map[string]bool{}
	for _, s := range w.Stages {
		stages[s.ID] = s
		fed[s.ID] = map[string]WorkflowEdge{}
		for _, name := range SortedNames(s.Outputs) {
			if !OutputNameValid(name) || !OutputTypeValid(s.Outputs[name]) {
				return fmt.Errorf("workflow-output: %s: invalid name or type for %q", s.ID, name)
			}
		}
		for _, name := range SortedNames(s.Inputs) {
			if !OutputNameValid(name) || !OutputTypeValid(s.Inputs[name]) {
				return fmt.Errorf("workflow-input: %s: invalid name or type for %q", s.ID, name)
			}
		}
	}
	for _, e := range w.Edges {
		producer, present := stages[e.From]
		consumer, exists := stages[e.To]
		if !present || !exists {
			return fmt.Errorf("workflow-unknown-node: %s -> %s", e.From, e.To)
		}
		if e.FromOutput == "" && e.ToInput == "" {
			continue
		}
		if e.FromOutput == "" || e.ToInput == "" {
			return fmt.Errorf("workflow-input: edge %s -> %s must name both from_output and to_input", e.From, e.To)
		}
		typ, declared := producer.Outputs[e.FromOutput]
		if !declared {
			return fmt.Errorf("workflow-output: %s: does not declare %s", e.From, e.FromOutput)
		}
		inputType, declared := consumer.Inputs[e.ToInput]
		if !declared || inputType != typ {
			return fmt.Errorf("workflow-input: %s: %s undeclared or type differs from %s.%s", e.To, e.ToInput, e.From, e.FromOutput)
		}
		key := e.To + "\x00" + e.ToInput + "\x00" + e.From
		if previous, exists := fed[e.To][e.ToInput]; exists {
			if producer.CopiesOf == "" || producer.CopiesOf != stages[previous.From].CopiesOf || e.FromOutput != previous.FromOutput || seen[key] {
				return fmt.Errorf("workflow-input: %s: input %s fed by two edges", e.To, e.ToInput)
			}
		}
		seen[key] = true
		fed[e.To][e.ToInput] = e
	}
	for _, s := range w.Stages {
		for _, name := range SortedNames(s.Inputs) {
			if _, exists := fed[s.ID][name]; !exists {
				return fmt.Errorf("workflow-input: %s: input %s fed by no edge", s.ID, name)
			}
		}
	}
	return nil
}

// ResolveCommand resolves stage.CommandRef and flags against config/tillr/commands.json.
func ResolveCommand(root string, stage *WorkflowStage) error {
	raw, err := os.ReadFile(filepath.Join(root, "config/tillr/commands.json"))
	if err != nil {
		return fmt.Errorf("command-registry: %w", err)
	}
	d := json.NewDecoder(bytes.NewReader(raw))
	d.DisallowUnknownFields()
	var entries map[string]CommandEntry
	if err = d.Decode(&entries); err != nil {
		return fmt.Errorf("command-registry: %w", err)
	}
	if err = d.Decode(new(any)); err != io.EOF {
		return fmt.Errorf("command-registry: trailing content")
	}
	entry, ok := entries[stage.CommandRef]
	if !ok {
		return fmt.Errorf("command-reference: %s", stage.CommandRef)
	}
	if len(entry.Argv) == 0 || entry.Argv[0] == "" {
		return fmt.Errorf("command-registry: %s needs argv", stage.CommandRef)
	}
	// A registered shell would turn otherwise inert flag values into shell input.
	switch filepath.Base(entry.Argv[0]) {
	case "sh", "bash", "dash", "zsh", "fish", "ksh", "csh", "tcsh":
		return fmt.Errorf("command-registry: shell programs are not command nodes")
	}
	for flag, kind := range entry.Flags {
		if !strings.HasPrefix(flag, "--") || strings.ContainsAny(flag, "= \t\n") || (kind != "string" && kind != "number" && kind != "path") {
			return fmt.Errorf("command-registry: invalid flag %q or type %q", flag, kind)
		}
	}
	argv := append([]string(nil), entry.Argv...)
	keys := make([]string, 0, len(stage.Flags))
	for key := range stage.Flags {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, flag := range keys {
		kind, ok := entry.Flags[flag]
		if !ok {
			return fmt.Errorf("command-flag: %s does not accept %s", stage.CommandRef, flag)
		}
		value := stage.Flags[flag]
		var word string
		switch kind {
		case "string", "path":
			var ok bool
			word, ok = value.(string)
			if !ok {
				return fmt.Errorf("command-value: %s requires %s", flag, kind)
			}
		case "number":
			n, ok := value.(json.Number)
			if !ok {
				return fmt.Errorf("command-value: %s requires number", flag)
			}
			word = n.String()
		}
		if strings.ContainsRune(word, 0) {
			return fmt.Errorf("command-value: %s contains NUL", flag)
		}
		if kind == "path" {
			if err := CommandPath(root, word); err != nil {
				return err
			}
		}
		// One argv element, even for strings beginning with '-' or containing shell syntax.
		argv = append(argv, flag+"="+word)
	}
	stage.Command = argv
	return nil
}

// CommandPath verifies that path stays inside root without parent traversal or escaping symlinks.
func CommandPath(root, p string) error {
	// Cleaning before validation but transmitting the original spelling would
	// disagree with filesystem traversal through a symlink followed by '..'.
	for _, component := range strings.Split(p, string(filepath.Separator)) {
		if component == ".." {
			return fmt.Errorf("command-path: parent traversal is not allowed in %q", p)
		}
	}
	clean := filepath.Clean(p)
	if p == "" || filepath.IsAbs(p) || clean == ".." || strings.HasPrefix(clean, ".."+string(filepath.Separator)) {
		return fmt.Errorf("command-path: %q is outside the repository", p)
	}
	// Inspect the nearest existing ancestor too: generated paths may not exist yet.
	candidate := filepath.Join(root, clean)
	for {
		_, err := os.Lstat(candidate)
		if err == nil {
			break
		}
		if !os.IsNotExist(err) {
			return fmt.Errorf("command-path: %w", err)
		}
		candidate = filepath.Dir(candidate)
	}
	resolved, err := filepath.EvalSymlinks(candidate)
	if err != nil {
		return fmt.Errorf("command-path: %w", err)
	}
	base, err := filepath.EvalSymlinks(root)
	if err != nil {
		return fmt.Errorf("command-path: %w", err)
	}
	rel, err := filepath.Rel(base, resolved)
	if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
		return fmt.Errorf("command-path: %q resolves outside the repository", p)
	}
	return nil
}

// FencePaths expands allowed fence prefixes with derived tillr module/doc/test paths.
func FencePaths(allowed []string) []string {
	seen := make(map[string]bool, len(allowed)*2)
	out := make([]string, 0, len(allowed)*2)
	add := func(p string) {
		if p == "" || seen[p] {
			return
		}
		seen[p] = true
		out = append(out, p)
	}
	for _, p := range allowed {
		add(p)
	}
	for _, p := range allowed {
		// A fence spells a directory with or without its trailing slash and means the same
		// path either way, so the rules below match on the path and not on the spelling.
		named := strings.TrimSuffix(p, "/")
		if module, ok := tillrModule(named); ok {
			add("yolo-packs/tillr/files/local/share/tillr/" + module + "/")
			if named != "tools/"+module {
				add("tools/" + module + "/README.md")
			}
			add(tillrSkillPath)
			add(tillrDocsTest)
			if module == "tillr-loop" {
				add("tools/tillr-loop/")
			}
		}
		if named == "tools/tillr-run/backends" || strings.HasPrefix(named, "tools/tillr-run/backends/") {
			add(museBackendTest)
		}
	}
	return out
}

// InsideFence answers whether one changed path is inside the fence.
func InsideFence(p string, fence []string) bool {
	for _, entry := range fence {
		if p == entry || strings.HasPrefix(p, strings.TrimSuffix(entry, "/")+"/") {
			return true
		}
	}
	return false
}

func tillrModule(p string) (string, bool) {
	rest, ok := strings.CutPrefix(p, "tools/")
	if !ok {
		return "", false
	}
	module, _, _ := strings.Cut(rest, "/")
	if !strings.HasPrefix(module, "tillr") {
		return "", false
	}
	return module, true
}
