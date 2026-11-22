package drain

import (
	"bytes"
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

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

func decodeWorkflow(raw []byte, ref string) (*Workflow, error) {
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
			return nil, fmt.Errorf("workflow-unknown-seat: %s: "+invalidPolicySeatName, s.ID, s.Seat, defaultSeatDeclaration)
		}
		if s.Access != "read_only" && s.Access != "write" {
			return nil, fmt.Errorf("workflow-access: %s", s.ID)
		}
		if s.WriteScope.explicit && s.Access == "read_only" {
			return nil, fmt.Errorf("workflow-write-scope-read-only: %s", s.ID)
		}
		if s.Access == "write" {
			if _, err := s.scopePaths(nil); err != nil {
				return nil, err
			}
		}
	}
	if err := validateTemplateWiring(&w); err != nil {
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
			if err := validateWorkflowWatchRefs(&w, ids, seats); err != nil {
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
			if !ids[target] || !proposalLater(&w, stage.ID, target) {
				return nil, fmt.Errorf("workflow-proposal: %s: %s is not a later stage", stage.ID, target)
			}
		}
	}
	if err := validateEvaluatorJudgeClasses(&w); err != nil {
		return nil, err
	}
	if err := validateWorkflowExit(&w); err != nil {
		return nil, err
	}
	return &w, nil
}

func validateWorkflowWatchRefs(w *Workflow, stageIDs, seats map[string]bool) error {
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

func validateEvaluatorJudgeClasses(w *Workflow) error {
	id := evaluatorSlot(w)
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

func loadWorkflow(root, ref string, policy *Policy) (*Workflow, error) {
	return loadWorkflowWith(root, ref, policy, nil)
}

// loadWorkflowWith binds the workflow's top-level args from the item's own record, the
// one source of them outside a containing stage. Validation is the same as for a stage's
// args: a missing, unknown, mistyped or oversized argument refuses the load.
func loadWorkflowWith(root, ref string, policy *Policy, args map[string]any) (*Workflow, error) {
	if !item.WorkflowRefValid(ref) {
		return nil, fmt.Errorf("workflow-reference: %q", ref)
	}
	path := filepath.Join(root, "config", "tillr", "workflows", ref+".json")
	w, err := expandWorkflow(root, path, policy, map[string]bool{}, args, nil)
	if err == nil {
		err = validateOutputWiring(w)
	}
	return w, err
}

// scopePaths expands a node's explicit paths by the same rules as an item's fence.
// When WriteScope.Paths is nil (inherited) and allowed is empty, it returns nil.
func (s WorkflowStage) scopePaths(allowed []string) ([]string, error) {
	if s.WriteScope.Paths == nil {
		if len(allowed) == 0 {
			return nil, nil
		}
		return fencePaths(allowed), nil
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
	return fencePaths(paths), nil
}

const ReasonNodeScopeOutsideFence = "node-scope-outside-fence"

func (w *Workflow) checkScopes(allowed []string) error {
	var fence []string
	if len(allowed) > 0 {
		fence = fencePaths(allowed)
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
		scope, err := stage.scopePaths(allowed)
		if err != nil {
			return err
		}
		for _, p := range scope {
			clean := path.Clean(p)
			if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || (len(fence) > 0 && !insideFence(clean, fence)) {
				return fmt.Errorf("%s: node %s: %s is outside the item's fence", ReasonNodeScopeOutsideFence, stage.ID, p)
			}
		}
	}
	return nil
}

const nodeAdvancedKind = "drain.node-advanced"

// Only a loop restart continues a writer's existing candidate. Failover and
// other node retries still restart at the node boundary.
func restartWriter(d Decision) bool {
	if !d.WorkflowRestart || d.Workflow == nil {
		return false
	}
	_, _, cause := splitRunID(d.RunRequest.RunID)
	node := d.Workflow.Stages[d.NodeIndex]
	return cause == "restart" && node.Kind == "agent" && node.Access == "write"
}

func (l *Loop) committedRestartBases(ctx context.Context, lane, nodeBase string) (itemBase, scopeBase string, err error) {
	// Prefer the live item boundary: a successful rebase updates the lane's
	// metadata, not the original run's archive.
	base := l.laneBase(lane)
	target := l.laneTarget(lane)
	head, err := l.revParse(ctx, target, "HEAD")
	if err != nil || base == "" || base == head {
		return "", nodeBase, err
	}
	ok, err := containsCommit(ctx, target, base, head)
	if err != nil || !ok {
		return "", nodeBase, err
	}
	if base != nodeBase {
		// A node that started from the old trunk must not count the new
		// trunk's changes against its own fence after a rebase.
		onTrunk, err := containsCommit(ctx, target, nodeBase, base)
		if err != nil {
			return "", nodeBase, err
		}
		if onTrunk {
			return base, base, nil
		}
		ok, err := containsCommit(ctx, target, nodeBase, head)
		if err != nil || !ok {
			return "", nodeBase, err
		}
	}
	return base, nodeBase, nil
}

// stagedNodeRun retains proof of staging across repeated restart dispatches.
// Archives cover a crash after staging but before recordStagedNode; an unstaged
// first claim owns no preexisting lane contents.
func (l *Loop) stagedNodeRun(d Decision) (string, error) {
	if d.NodeStartRunID != "" {
		return d.NodeStartRunID, nil
	}
	check := func(id string) (bool, error) {
		if id == "" {
			return false, nil
		}
		_, err := os.Stat(filepath.Join(RunDir(l.cfg.Root, id), BaseSHAFile))
		if err == nil {
			return true, nil
		}
		if os.IsNotExist(err) {
			return false, nil
		}
		return false, err
	}
	runID := d.RunRequest.RunID
	if ok, err := check(runID); err != nil || ok {
		if ok {
			return runID, nil
		}
		return "", err
	}
	base, turns, kind := splitRunID(runID)
	if turns == 0 {
		return "", nil
	}
	stems := []string{base}
	if bounded := boundRunIDBase(base); bounded != base {
		stems = append(stems, bounded)
	}
	if d.Workflow != nil && d.ItemID != "" && d.Attempt > 0 {
		nodeStem := boundRunIDBase(attemptRunID(d.ItemID, d.Attempt) + "-n" + strconv.Itoa(d.NodeIndex+1))
		if !slices.Contains(stems, nodeStem) {
			stems = append(stems, nodeStem)
		}
	}
	if turns <= 64 {
		for n := turns - 1; n >= 1; n-- {
			for _, stem := range stems {
				cand := markRunIDTurns(stem, kind, n)
				if ok, err := check(cand); err != nil || ok {
					if ok {
						return cand, nil
					}
					return "", err
				}
				for _, other := range runIDContinuationKinds {
					if other == kind {
						continue
					}
					alt := markRunIDTurns(stem, other, n)
					if ok, err := check(alt); err != nil || ok {
						if ok {
							return alt, nil
						}
						return "", err
					}
				}
			}
		}
	} else {
		entries, err := os.ReadDir(LoopDir(l.cfg.Root))
		if err != nil && !os.IsNotExist(err) {
			return "", err
		}
		type priorCandidate struct {
			id    string
			turns int
			same  bool
		}
		var priors []priorCandidate
		for _, entry := range entries {
			if !entry.IsDir() {
				continue
			}
			eb, et, ek := splitRunID(entry.Name())
			if et >= 1 && et < turns && slices.Contains(stems, eb) {
				priors = append(priors, priorCandidate{id: entry.Name(), turns: et, same: ek == kind})
			}
		}
		sort.Slice(priors, func(i, j int) bool {
			if priors[i].turns != priors[j].turns {
				return priors[i].turns > priors[j].turns
			}
			if priors[i].same != priors[j].same {
				return priors[i].same
			}
			return priors[i].id < priors[j].id
		})
		for _, p := range priors {
			if ok, err := check(p.id); err != nil || ok {
				if ok {
					return p.id, nil
				}
				return "", err
			}
		}
	}
	for _, stem := range stems {
		if ok, err := check(stem); err != nil || ok {
			if ok {
				return stem, nil
			}
			return "", err
		}
	}
	return "", nil
}

// recordStagedNode retains proof that this node was staged. The first node's
// base names the actual target, including a fork checkout staging materialized.
// The archived base also covers a crash between staging and this write; no
// worker starts before this write.
func (l *Loop) recordStagedNode(ctx context.Context, d Decision) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		work := works[d.ItemID]
		if work == nil || work.Done || work.Decision.RunRequest.RunID != d.RunRequest.RunID {
			return fmt.Errorf("workflow staging lost its claim for %s", d.RunRequest.RunID)
		}
		work.Decision = d
		return appendEffect(tx, dispatchStartedKind, d.RunRequest.RunID, work, l.cfg.Now().UTC())
	})
}

func startNode(tx *store.Tx, d Decision) error {
	if d.Workflow == nil {
		return nil
	}
	result, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome,seat,model) VALUES(?,?,?,?, 'running',?,?)
 ON CONFLICT(item_id,attempt,node_id) DO UPDATE SET run_id=excluded.run_id,outcome='running',seat=excluded.seat,model=excluded.model WHERE node_runs.outcome IN ('running','interrupted')`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID, d.RunRequest.RunID, d.Seat, d.RunRequest.Route())
	if err != nil {
		return err
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count != 1 {
		return fmt.Errorf("workflow node %s already finished in attempt %d", d.Workflow.Stages[d.NodeIndex].ID, d.Attempt)
	}
	return nil
}

func (l *Loop) nodeOutcome(ctx context.Context, d Decision, outcome string) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE node_runs SET outcome=? WHERE run_id=?`, outcome, d.RunRequest.RunID)
		return err
	})
}

// advanceNode retains the lane and base while replacing the current pending run.
// The scheduler dispatches it on the next pass, only after settlement releases its
// examination lease. It never resets the worktree between nodes.
func (l *Loop) advanceNode(ctx context.Context, d Decision, base, report string) error {
	next := d
	next.Parallel = nil
	// Successors normally keep the repaired checkout, not the return dispatch mode.
	next.Returned = nil
	next.RetryCandidate = nil
	next.RepeatedCall = nil
	next.WorkerSessionReason = ""
	next.ConflictID = ""
	if classificationNode(d.Workflow.Stages[d.NodeIndex]) {
		delivery, err := l.recordedDelivery(ctx, d.ItemID)
		if err != nil {
			return err
		}
		next.Workflow = workflowForDelivery(d.Workflow, delivery)
	}
	index, err := l.nextWorkflowNode(ctx, next)
	if err != nil {
		return err
	}
	if index < 0 {
		return fmt.Errorf("workflow-next-node: no ready successor")
	}
	next.NodeIndex = index
	next.PreviousReport = report
	if classificationNode(d.Workflow.Stages[d.NodeIndex]) {
		next.NodeBaseSHA = ""
	} else if !l.nonLanding {
		head, err := l.revParse(ctx, l.laneTarget(d.LaneID), "HEAD")
		if err != nil {
			return err
		}
		next.NodeBaseSHA = head
	}
	next.NodeStartRunID = ""
	next.WorkflowRestart = false
	next.NodeInfraFailures = 0
	next.InfrastructureFrom = ""
	next.WorkflowRelease = false
	next.FreshNodeStage = false
	// The kept readers were recorded at the attempt's claim; a later node continues the
	// lane, so it is no fresh retry start.
	next.ReusedNodes, next.ReusedAttempt = nil, 0
	stage := next.Workflow.Stages[next.NodeIndex]
	next.Seat = stage.Seat
	next.Action, next.Reason = ActionDispatch, "workflow-next-node"
	next.From, next.To = "running", "running"
	runID := workflowNodeRunID(next, next.NodeIndex)
	if stage.Kind == "command" {
		next.RunRequest = stage.commandRequest(d.ItemID, d.LaneID, runID)
	} else {
		next.RunRequest = l.cfg.Policy.request(d.ItemID, d.LaneID, runID, l.cfg.Policy.firstBackend(itemRow{ItemID: d.ItemID, Seat: stage.Seat}))
		next.RunRequest.TimeoutMS = stage.TimeoutMS
	}
	if next.RunRequest == nil {
		return fmt.Errorf("workflow seat %s cannot build a request", stage.Seat)
	}
	next.RunRequest.NodeID = stage.ID
	next.RunRequest.WorkingDir = d.RunRequest.WorkingDir
	stampDecisionRequest(next, next.RunRequest)
	identifyDecision(&next)
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	owner, err := currentLoopOwner()
	if err != nil {
		return err
	}
	var released bool
	at := l.cfg.Now().UTC()
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		blocked, err := itemDependencyBlocked(tx.Reader(), d.ItemID)
		if err != nil {
			return err
		}
		nextBase := base
		finishedStage := d.Workflow.Stages[d.NodeIndex]
		if blocked && (l.nonLanding || (finishedStage.Access == "read_only" && (classificationNode(finishedStage) || next.NodeBaseSHA == base))) {
			// Free the lane while waiting for the dependency so the dependency itself
			// can run on this lane. When no write stage has committed yet, clear the
			// recorded base so the next step starts from the trunk after the dependency lands.
			released = true
			if classificationNode(finishedStage) || (d.NodeIndex == 0 && finishedStage.Access == "read_only") {
				nextBase = ""
				next.NodeBaseSHA = ""
				next.FreshNodeStage = true
			}
		}
		return appendEffect(tx, nodeAdvancedKind, next.RunRequest.RunID, pendingWork{Decision: next, BaseSHA: nextBase, Ready: true, LaneReleased: released, ParentRunID: d.RunRequest.RunID, Owner: owner, Examining: true}, at)
	})
	if err == nil && released {
		l.notifyStartLaneReleased(d.LaneID, d.RunRequest.RunID, true, at)
	}
	return err
}

func itemDependencyBlocked(r *store.Reader, itemID string) (bool, error) {
	hasEdges, err := tableExists(r, "item_edges")
	if err != nil || !hasEdges {
		return false, err
	}
	declared, err := definition.ForReader(r)
	if err != nil {
		return false, err
	}
	rows, err := r.Query(`SELECT COALESCE(i.state, '') FROM item_edges e LEFT JOIN items i ON i.item_id = e.blocked_by WHERE e.item_id = ?`, itemID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var state string
		if err := rows.Scan(&state); err != nil {
			return false, err
		}
		if state == "" || !blockerSatisfied(declared.Parsed, state) {
			return true, nil
		}
	}
	return false, rows.Err()
}

const (
	quickWayFile     = "do-it-now"
	planQuickWayKind = "drain.plan-quick"
)

func planAllowsQuickWay(stage WorkflowStage) bool {
	return (stage.Kind == "" || stage.Kind == "agent") && stage.Seat == "plan" && stage.Access == "read_only" && !(stage.ID == "classify" && classificationNode(stage))
}

func planTookQuickWay(root string, d Decision) (bool, error) {
	if d.Workflow == nil || d.NodeIndex < 0 || d.NodeIndex >= len(d.Workflow.Stages) {
		return false, nil
	}
	stage := d.Workflow.Stages[d.NodeIndex]
	if !planAllowsQuickWay(stage) {
		return false, nil
	}
	path := filepath.Join(nodeOutputsDir(root, d.RunRequest.RunID), quickWayFile)
	info, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	if !info.Mode().IsRegular() {
		return false, fmt.Errorf("plan-quick-invalid: %s is not a regular file", quickWayFile)
	}
	return true, nil
}

func (l *Loop) settleQuickWay(ctx context.Context, d Decision) error {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		var alreadyQuick int
		if err := tx.QueryRow(`SELECT quick_way FROM node_runs WHERE run_id=?`, d.RunRequest.RunID).Scan(&alreadyQuick); err != nil && !errors.Is(err, sql.ErrNoRows) {
			return err
		}
		if _, err := tx.Exec(`UPDATE node_runs SET outcome='passed', quick_way=1 WHERE run_id=?`, d.RunRequest.RunID); err != nil {
			return err
		}
		outcomes, err := workflowOutcomes(tx.Reader(), d.ItemID, d.Attempt)
		if err != nil {
			return err
		}
		for i, stage := range d.Workflow.Stages {
			if outcomes[stage.ID] == "" {
				if _, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES(?,?,?,?, 'skipped') ON CONFLICT(item_id,attempt,node_id) DO NOTHING`, d.ItemID, d.Attempt, stage.ID, attemptRunID(d.ItemID, d.Attempt)+"-n"+strconv.Itoa(i+1)); err != nil {
					return err
				}
			}
		}
		if alreadyQuick == 0 {
			stage := d.Workflow.Stages[d.NodeIndex]
			if err := appendEffect(tx, planQuickWayKind, d.RunRequest.RunID, map[string]any{
				"item_id": d.ItemID,
				"attempt": d.Attempt,
				"node_id": stage.ID,
				"run_id":  d.RunRequest.RunID,
			}, l.cfg.Now().UTC()); err != nil {
				return err
			}
		}
		return nil
	})
}

func (l *Loop) settleNode(ctx context.Context, d Decision, rec run.Record, log, base string, result *LoopResult, offered bool) (bool, error) {
	return l.settleNodeFormat(ctx, d, rec, log, base, result, offered, nil)
}

func (l *Loop) settleNodeFormat(ctx context.Context, d Decision, rec run.Record, log, base string, result *LoopResult, offered bool, formatResumePrompt *string) (bool, error) {
	if evaluatorNode(d) {
		return l.settleEvaluator(ctx, d, rec, base, result)
	}
	stage := d.Workflow.Stages[d.NodeIndex]
	quickWay, err := planTookQuickWay(l.cfg.Root, d)
	if err != nil {
		return true, err
	}
	effectiveAccess := stage.Access
	if quickWay {
		effectiveAccess = "write"
	}
	if stage.Kind == "command" {
		if handled, err := l.replayCommandHold(ctx, d); handled || err != nil {
			return true, err
		}
	}
	stagedBase := l.laneBase(d.LaneID)
	if stagedBase != "" {
		base = stagedBase
	}
	target := l.laneTarget(d.LaneID)
	var head, nodeBase, dirty string
	if !l.nonLanding {
		head, err = l.revParse(ctx, target, "HEAD")
		if err != nil {
			return true, err
		}
		if _, err := os.Stat(filepath.Join(run.DurableDir(l.laneDirPath(d.LaneID), rec.RunID), graphExitFile)); err == nil {
			if head != base {
				return true, Externalf("recorded no-commit graph head changed: %s", d.ItemID)
			}
			c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, BaseSHA: base, HeadSHA: head, Worktree: target}
			return true, l.settleGraphExit(ctx, d, c)
		} else if !os.IsNotExist(err) {
			return true, err
		}
		nodeBase = d.NodeBaseSHA
		if nodeBase == "" {
			nodeBase = l.laneBase(d.LaneID)
			if nodeBase == "" {
				nodeBase = base
			}
		}
		dirty, err = l.git(ctx, target, "status", "--porcelain", "--untracked-files=all")
		if err != nil {
			return true, err
		}
		uncleanNoChange := plannedWritePiece(stage) && head == nodeBase && reportFooter(log) == "done" && strings.TrimSpace(dirty) != ""
		if (effectiveAccess == "read_only" && head != nodeBase) || ((effectiveAccess == "read_only" || stage.Kind == "command") && strings.TrimSpace(dirty) != "") || uncleanNoChange {
			if err := l.nodeOutcome(ctx, d, "refused"); err != nil {
				return true, err
			}
			detail := "node " + stage.ID + " violated its " + effectiveAccess + " worktree boundary"
			if uncleanNoChange {
				detail = "node " + stage.ID + " reported done without a commit but left uncommitted changes"
			}
			c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, Worktree: target, BaseSHA: base, HeadSHA: head, RefusalDetail: detail}
			if err := l.refuse(ctx, c, result, RefusalFence); err != nil {
				return true, err
			}
			if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, "workflow node violated its worktree boundary: "+stage.ID); stop != "" {
				return true, retainedAfter(d.LaneID, stop)
			}
			return true, l.markExamined(ctx, d)
		}
	}
	if stage.Kind == "command" && (rec.State != run.StateExited || rec.ExitCode == nil || (*rec.ExitCode != 0 && *rec.ExitCode != 1)) {
		if err := l.nodeOutcome(ctx, d, "fault"); err != nil {
			return true, err
		}
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, "command node machinery failed: "+stage.ID); stop != "" {
			return true, retainedAfter(d.LaneID, stop)
		}
		return true, l.markExamined(ctx, d)
	}
	footer := reportFooter(log)
	if offered {
		if obs, isObstacle, obsErr := l.offeredObstacle(ctx, rec.RunID); obsErr != nil {
			return true, obsErr
		} else if isObstacle {
			offered = false
			footer = "blocked"
			log = formatObstacleReport(obs)
		} else {
			footer = "done"
		}
	}
	if stage.Kind == "agent" {
		if ClassifyAttempt(rec, log) == AttemptUnjudged {
			return true, Externalf("unjudged workflow node requires continuation or inspection")
		}
		if rec.EndCause == run.EndRepeatingCall && !offered {
			return true, l.restartRepeatingNode(ctx, d, base, rec)
		}
		if ClassifyAttempt(rec, log) == AttemptInterrupted && !offered {
			return true, l.restartInterruptedNode(ctx, d, base, rec)
		}
		if !offered {
			empty, err := l.emptyQuotaRefusal(d, rec, stagedBase, head, dirty)
			if err != nil {
				return true, err
			}
			if empty {
				if err := l.recordBackendWindow(ctx, rec); err != nil {
					return true, err
				}
				return true, l.recordWorkflowRestart(ctx, d, base, rec, "fresh", "", true)
			}
		}
		if ClassifyAttempt(rec, log) == AttemptRefused {
			stop, err := l.holdAdapterRefusal(ctx, d.LaneID, *d.RunRequest, rec, log)
			if err != nil {
				return true, err
			}
			if stop != "" {
				return true, retainedAfter(d.LaneID, stop)
			}
			return true, l.markExamined(ctx, d)
		}
		if classifyDecisionAttempt(d, rec, log) == AttemptInfrastructure || (rec.State == run.StateExited && !readableFooter(footer)) {
			return true, l.holdFailedWorkflowNode(ctx, d, base, rec, result)
		}
	}
	if !l.nonLanding && effectiveAccess == "write" {
		c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, Worktree: target, BaseSHA: nodeBase, HeadSHA: head, BlockConflict: d.BlockConflict}
		blocked := stage.Kind == "agent" && footer == "blocked" && parseObstacle(log).Problem == ""
		// An inherited scope on an ordinary node no longer uses the legacy
		// fence file, including for already dispatched runs.
		var scopeFile string
		if stage.WriteScope.Paths != nil {
			scopeFile = filepath.Join(l.laneDirPath(d.LaneID), FenceFile)
		}
		escape, err := l.changeFenceViolationAt(ctx, c, scopeFile, blocked)
		if err != nil {
			return true, err
		}
		if escape != "" {
			c.BaseSHA = base
			c.RefusalDetail = "node " + stage.ID + ": " + escape
			if err := l.nodeOutcome(ctx, d, "refused"); err != nil {
				return true, err
			}
			if err := l.refuse(ctx, c, result, RefusalFence); err != nil {
				return true, err
			}
			if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, c.RefusalDetail); stop != "" {
				return true, retainedAfter(d.LaneID, stop)
			}
			return true, l.markExamined(ctx, d)
		}
	}
	if stage.Kind == "agent" {
		if verifyBuildReturn(d.Returned) {
			if why, wrong := l.builderPlanWrong(d, stage, footer, log); wrong {
				// The build step cannot meet the findings within the plan: the plan step runs
				// again, with the builder's explanation and the findings it was given.
				if err := l.nodeOutcome(ctx, d, "refused"); err != nil {
					return true, err
				}
				if strings.TrimSpace(d.Returned.Detail) != "" {
					why += "\n\nThe findings this build step was given:\n" + strings.TrimSpace(d.Returned.Detail)
				}
				c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, Worktree: target, BaseSHA: base, HeadSHA: head, RefusalDetail: why}
				if err := l.refuse(ctx, c, result, RefusalPlanWrong); err != nil {
					return true, err
				}
				if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, "build step reported plan-wrong: "+stage.ID); stop != "" {
					return true, retainedAfter(d.LaneID, stop)
				}
				return true, l.markExamined(ctx, d)
			}
		}
		if footer != "done" && footer != "failed" {
			if footer == "blocked" {
				if writer, finding := missingCandidateFinding(d, base, head, dirty, log); writer >= 0 {
					ready, err := l.canReturnMissingCandidate(ctx, d, writer)
					if err != nil {
						return true, err
					}
					if ready {
						if err := l.nodeOutcome(ctx, d, missingCandidateOutcome); err != nil {
							return true, err
						}
						return true, l.returnMissingCandidate(ctx, d, writer, base, finding)
					}
				}
				if err := l.reportedBlock(ctx, d, log); err != nil {
					return true, err
				}
			}
			if err := l.nodeOutcome(ctx, d, "blocked"); err != nil {
				return true, err
			}
			if stop := l.abandonFromBase(ctx, d.LaneID, rec.RunID, head, l.settleBase(d.LaneID, base), "workflow node ended with "+footer); stop != "" {
				return true, retainedAfter(d.LaneID, stop)
			}
			return true, l.markExamined(ctx, d)
		}
	}
	failedCommand := stage.Kind == "command" && *rec.ExitCode == 1
	if failedCommand || (stage.Kind == "agent" && footer == "failed") {
		if err := l.nodeOutcome(ctx, d, "failed"); err != nil {
			return true, err
		}
		{
			for _, edge := range d.Workflow.Edges {
				if edge.From == stage.ID && edge.WhenOutcome == "fail" {
					if err := l.nodeOutcome(ctx, d, "handled-fail"); err != nil {
						return true, err
					}
					next, err := l.nextWorkflowNode(ctx, d)
					if err != nil {
						return true, err
					}
					if next >= 0 {
						return true, l.advanceNode(ctx, d, base, "")
					}
					if err := l.nodeOutcome(ctx, d, "failed"); err != nil {
						return true, err
					}
					break
				}
			}
		}
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, "workflow node ended with "+footer); stop != "" {
			return true, retainedAfter(d.LaneID, stop)
		}
		return true, l.markExamined(ctx, d)
	}
	artifacts, outputErr := checkNodeOutputs(l.cfg.Root, d)
	var delivery *deliveryDecision
	if outputErr == nil && classificationNode(stage) {
		delivery, outputErr = classifyDelivery(l.cfg.Root, artifacts, quickWay)
	}
	if outputErr == nil && quickWay {
		for _, a := range artifacts {
			if a.Type == "graph.proposal" {
				outputErr = fmt.Errorf("plan-quick-conflict: %s cannot both record quick way and propose a graph replacement", stage.ID)
				break
			}
		}
	}
	if outputErr != nil {
		if err := l.nodeOutcome(ctx, d, "failed"); err != nil {
			return true, err
		}
		refusalStep := RefusalNodeOutput
		var parseErr deliveryParseError
		if errors.As(outputErr, &parseErr) && classificationNode(stage) {
			refusalStep = RefusalPlanOutput
		}
		c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, Worktree: target, BaseSHA: base, HeadSHA: head, RefusalDetail: outputErr.Error()}
		if err := l.refuse(ctx, c, result, refusalStep); err != nil {
			return true, err
		}
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, outputErr.Error()); stop != "" {
			return true, retainedAfter(d.LaneID, stop)
		}
		return true, l.markExamined(ctx, d)
	}
	if !l.nonLanding && effectiveAccess == "write" && l.lanePatchRef(d.LaneID) == "" && head != "" && head != nodeBase && strings.TrimSpace(dirty) == "" {
		unformatted, err := unformattedRepoFiles(ctx, target)
		if err != nil {
			return true, err
		}
		if len(unformatted) > 0 {
			if !rec.Resumed && d.RunRequest != nil {
				rec.Resumed, err = l.continuationStarted(ctx, *d.RunRequest)
				if err != nil {
					return true, err
				}
			}
			if formatResumePrompt != nil && !offered && stage.Kind == "agent" && !rec.Resumed && eligibleFormatContinuation(rec) {
				*formatResumePrompt = unformattedContinuationPrompt(unformatted)
				return true, nil
			}
			detail := "node " + stage.ID + ": " + unformattedRefusalDetail(unformatted)
			c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, Worktree: target, BaseSHA: base, HeadSHA: head, RefusalDetail: detail}
			if err := l.nodeOutcome(ctx, d, "refused"); err != nil {
				return true, err
			}
			if err := l.refuse(ctx, c, result, RefusalFence); err != nil {
				return true, err
			}
			if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, detail); stop != "" {
				return true, retainedAfter(d.LaneID, stop)
			}
			return true, l.markExamined(ctx, d)
		}
	}
	if err := l.acceptNodeSettlementWithDelivery(ctx, &d, artifacts, delivery); err != nil {
		return true, err
	}
	if !l.nonLanding && effectiveAccess == "write" && head != "" && head != nodeBase {
		if err := l.pinLane(ctx, d.LaneID, rec.RunID, head); err != nil {
			return true, err
		}
	}
	if quickWay {
		if err := l.settleQuickWay(ctx, d); err != nil {
			return true, err
		}
		if l.nonLanding {
			outcome := l.settleHook(ctx, d, result)
			return true, outcome.err
		}
		return false, nil
	}
	next, err := l.nextWorkflowNode(ctx, d)
	if err != nil {
		return true, err
	}
	if next < 0 {
		if l.nonLanding {
			outcome := l.settleHook(ctx, d, result)
			return true, outcome.err
		}
		return l.finishWorkflow(ctx, d, rec, base, head, result)
	}
	// Preserve the full extracted report before staging overwrites the lane's report.
	report := []byte(log)
	for _, ref := range rec.ArtifactRefs {
		if ref.Kind == "report" {
			report, err = os.ReadFile(ref.Ref)
			if err != nil {
				return true, err
			}
			break
		}
	}
	dir := RunDir(l.cfg.Root, rec.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return true, err
	}
	path := filepath.Join(dir, "node-report.md")
	if err := os.WriteFile(path, report, 0644); err != nil {
		return true, err
	}
	return true, l.advanceNode(ctx, d, base, path)
}
