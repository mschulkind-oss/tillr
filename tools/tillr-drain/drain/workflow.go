package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
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
	CopiesOf   string              `json:"copies_of,omitempty"`
	MayReplace map[string][]string `json:"may_replace,omitempty"`
	ID         string              `json:"id"`
	Parent     string              `json:"parent,omitempty"`
	Args       map[string]any      `json:"args,omitempty"`
	Repeat     string              `json:"repeat,omitempty"`
	Prompt     string              `json:"prompt,omitempty"`
	WriteScope WorkflowScope       `json:"write_scope,omitempty"`
	Workflow   string              `json:"workflow,omitempty"`
	Kind       string              `json:"kind"`
	Seat       string              `json:"seat"`
	Access     string              `json:"access"`
	CommandRef string              `json:"command_ref,omitempty"`
	Flags      map[string]any      `json:"flags,omitempty"`
	Command    []string            `json:"resolved_argv,omitempty"`
	TimeoutMS  int                 `json:"timeout_ms,omitempty"`
	Inputs     map[string]string   `json:"inputs,omitempty"`
	Outputs    map[string]string   `json:"outputs,omitempty"`
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
	if err := validateWorkflowExit(&w); err != nil {
		return nil, err
	}
	return &w, nil
}

func loadWorkflow(root, ref string, policy *Policy) (*Workflow, error) {
	if !item.WorkflowRefValid(ref) {
		return nil, fmt.Errorf("workflow-reference: %q", ref)
	}
	path := filepath.Join(root, "config", "tillr", "workflows", ref+".json")
	return readWorkflow(root, path, policy, map[string]bool{})
}

// scopePaths expands a node's explicit paths by the same rules as an item's fence.
// An omitted scope also inherits for decisions recorded before scopes were supported.
func (s WorkflowStage) scopePaths(allowed []string) ([]string, error) {
	if s.WriteScope.Paths == nil {
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
	fence := fencePaths(allowed)
	for i := range fence {
		fence[i] = path.Clean(fence[i])
	}
	for _, stage := range w.Stages {
		if stage.Access != "write" {
			continue
		}
		scope, err := stage.scopePaths(allowed)
		if err != nil {
			return err
		}
		for _, p := range scope {
			clean := path.Clean(p)
			if clean == "." || clean == ".." || strings.HasPrefix(clean, "../") || strings.HasPrefix(clean, "/") || !insideFence(clean, fence) {
				return fmt.Errorf("%s: node %s: %s is outside the item's fence", ReasonNodeScopeOutsideFence, stage.ID, p)
			}
		}
	}
	return nil
}

const nodeAdvancedKind = "drain.node-advanced"

// stagedNodeRun retains proof of staging across repeated restart dispatches.
// Archives cover a crash after staging but before recordStagedNode; an unstaged
// first claim owns no preexisting lane contents.
func (l *Loop) stagedNodeRun(d Decision) (string, error) {
	if d.NodeStartRunID != "" {
		return d.NodeStartRunID, nil
	}
	for id := d.RunRequest.RunID; ; id = strings.TrimSuffix(id, "-restart") {
		_, err := os.Stat(filepath.Join(RunDir(l.cfg.Root, id), BaseSHAFile))
		if err == nil {
			return id, nil
		}
		if !os.IsNotExist(err) {
			return "", err
		}
		if !strings.HasSuffix(id, "-restart") {
			return "", nil
		}
	}
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
 ON CONFLICT(item_id,attempt,node_id) DO UPDATE SET run_id=excluded.run_id,outcome='running',seat=excluded.seat,model=excluded.model WHERE node_runs.outcome='running'`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID, d.RunRequest.RunID, d.Seat, d.RunRequest.Route())
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
	// The refusal has been answered by this node. Successors keep the repaired
	// checkout and accepted-input references, not the return dispatch mode.
	next.Returned = nil
	next.WorkerSessionReason = ""
	index, err := l.nextWorkflowNode(ctx, d)
	if err != nil {
		return err
	}
	if index < 0 {
		return fmt.Errorf("workflow-next-node: no ready successor")
	}
	next.NodeIndex = index
	next.PreviousReport = report
	if !l.nonLanding {
		head, err := l.revParse(ctx, l.laneTarget(d.LaneID), "HEAD")
		if err != nil {
			return err
		}
		next.NodeBaseSHA = head
	}
	next.NodeStartRunID = ""
	next.WorkflowRestart = false
	next.NodeInfraFailures = 0
	next.WorkflowRelease = false
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
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		return appendEffect(tx, nodeAdvancedKind, next.RunRequest.RunID, pendingWork{Decision: next, BaseSHA: base, Ready: true, ParentRunID: d.RunRequest.RunID, Owner: owner, Examining: true}, l.cfg.Now().UTC())
	})
}

func (l *Loop) settleNode(ctx context.Context, d Decision, rec run.Record, log, base string, result *LoopResult) (bool, error) {
	if evaluatorNode(d) {
		return l.settleEvaluator(ctx, d, rec, base, result)
	}
	stage := d.Workflow.Stages[d.NodeIndex]
	if stage.Kind == "command" {
		if handled, err := l.replayCommandHold(ctx, d); handled || err != nil {
			return true, err
		}
	}
	if stagedBase := l.laneBase(d.LaneID); stagedBase != "" {
		base = stagedBase
	}
	target := l.laneTarget(d.LaneID)
	var head string
	var err error
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
		nodeBase := d.NodeBaseSHA
		if nodeBase == "" {
			nodeBase = l.laneBase(d.LaneID)
			if nodeBase == "" {
				nodeBase = base
			}
		}
		dirty, err := l.git(ctx, target, "status", "--porcelain", "--untracked-files=all")
		if err != nil {
			return true, err
		}
		if (stage.Access == "read_only" && head != nodeBase) || ((stage.Access == "read_only" || stage.Kind == "command") && strings.TrimSpace(dirty) != "") {
			if err := l.nodeOutcome(ctx, d, "refused"); err != nil {
				return true, err
			}
			c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, Worktree: target, BaseSHA: base, HeadSHA: head, RefusalDetail: "node " + stage.ID + " violated its " + stage.Access + " worktree boundary"}
			if err := l.refuse(ctx, c, result, RefusalFence); err != nil {
				return true, err
			}
			if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, "workflow node violated its worktree boundary: "+stage.ID); stop != "" {
				return true, retainedAfter(d.LaneID, stop)
			}
			return true, l.markExamined(ctx, d)
		}
		if stage.Access == "write" {
			c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, Worktree: target, BaseSHA: nodeBase, HeadSHA: head}
			escape, err := l.fenceViolationAt(ctx, c, filepath.Join(l.laneDirPath(d.LaneID), FenceFile))
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
	if stage.Kind == "agent" {
		if ClassifyAttempt(rec, log) == AttemptUnjudged {
			return true, Externalf("unjudged workflow node requires continuation or inspection")
		}
		if ClassifyAttempt(rec, log) == AttemptInfrastructure || (rec.State == run.StateExited && reportFooter(log) == "") {
			return true, l.retryWorkflowNode(ctx, d, base, rec, result)
		}
		if reportFooter(log) != "done" && reportFooter(log) != "failed" {
			if reportFooter(log) == "blocked" {
				if err := l.reportedBlock(ctx, d, log); err != nil {
					return true, err
				}
			}
			if err := l.nodeOutcome(ctx, d, "blocked"); err != nil {
				return true, err
			}
			if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, "workflow node ended with "+reportFooter(log)); stop != "" {
				return true, retainedAfter(d.LaneID, stop)
			}
			return true, l.markExamined(ctx, d)
		}
	}
	failedCommand := stage.Kind == "command" && *rec.ExitCode == 1
	if failedCommand || (stage.Kind == "agent" && reportFooter(log) == "failed") {
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
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, "workflow node ended with "+reportFooter(log)); stop != "" {
			return true, retainedAfter(d.LaneID, stop)
		}
		return true, l.markExamined(ctx, d)
	}
	artifacts, outputErr := checkNodeOutputs(l.cfg.Root, d)
	if outputErr != nil {
		if err := l.nodeOutcome(ctx, d, "failed"); err != nil {
			return true, err
		}
		c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, Worktree: target, BaseSHA: base, HeadSHA: head, RefusalDetail: outputErr.Error()}
		if err := l.refuse(ctx, c, result, RefusalNodeOutput); err != nil {
			return true, err
		}
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, outputErr.Error()); stop != "" {
			return true, retainedAfter(d.LaneID, stop)
		}
		return true, l.markExamined(ctx, d)
	}
	if err := l.acceptNodeSettlement(ctx, &d, artifacts); err != nil {
		return true, err
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
