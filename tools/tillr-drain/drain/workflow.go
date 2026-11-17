package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Workflow is a versioned, pre-approved sequence of agent and command runs.
// Edges order stages and select outcomes; ready stages run serially on one lane.
type Workflow struct {
	Name    string          `json:"name"`
	Version int             `json:"version"`
	Stages  []WorkflowStage `json:"stages"`
	Edges   []WorkflowEdge  `json:"edges"`
}
type WorkflowStage struct {
	ID         string            `json:"id"`
	Kind       string            `json:"kind"`
	Seat       string            `json:"seat"`
	Access     string            `json:"access"`
	CommandRef string            `json:"command_ref,omitempty"`
	Flags      map[string]any    `json:"flags,omitempty"`
	Command    []string          `json:"resolved_argv,omitempty"`
	TimeoutMS  int               `json:"timeout_ms,omitempty"`
	Inputs     map[string]string `json:"inputs,omitempty"`
	Outputs    map[string]string `json:"outputs,omitempty"`
}
type WorkflowEdge struct {
	From        string `json:"from_node"`
	To          string `json:"to_node"`
	WhenOutcome string `json:"when_outcome,omitempty"`
	FromOutput  string `json:"from_output,omitempty"`
	ToInput     string `json:"to_input,omitempty"`
}

func decodeWorkflow(raw []byte, ref string) (*Workflow, error) {
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
	if w.Edges == nil {
		return nil, fmt.Errorf("workflow-edges: edges must be a list")
	}
	if len(w.Stages) == 0 {
		return nil, fmt.Errorf("workflow-empty-stages: %s", ref)
	}
	ids := map[string]bool{}
	for _, s := range w.Stages {
		if s.ID == "" {
			return nil, fmt.Errorf("workflow-stage-id: empty id")
		}
		if ids[s.ID] {
			return nil, fmt.Errorf("workflow-duplicate-id: %s", s.ID)
		}
		ids[s.ID] = true
		if s.Kind != "agent" && s.Kind != "command" {
			return nil, fmt.Errorf("workflow-kind: %s: expected agent or command", s.ID)
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
		if s.Kind == "agent" && !item.KnownSeat(s.Seat) {
			return nil, fmt.Errorf("workflow-unknown-seat: %s: %s", s.ID, s.Seat)
		}
		if s.Access != "read_only" && s.Access != "write" {
			return nil, fmt.Errorf("workflow-access: %s", s.ID)
		}
	}
	if err := validateOutputWiring(&w); err != nil {
		return nil, err
	}
	indegree := map[string]int{}
	next := map[string][]string{}
	for _, e := range w.Edges {
		if e.WhenOutcome != "" && e.WhenOutcome != "pass" && e.WhenOutcome != "fail" {
			return nil, fmt.Errorf("workflow-outcome: %s", e.WhenOutcome)
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
	return &w, nil
}

func loadWorkflow(root, ref string, policy *Policy) (*Workflow, error) {
	if !item.WorkflowRefValid(ref) {
		return nil, fmt.Errorf("workflow-reference: %q", ref)
	}
	path := filepath.Join(root, "config", "tillr", "workflows", ref+".json")
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("workflow-file: %s: %w", path, err)
	}
	w, err := decodeWorkflow(raw, ref)
	if err != nil {
		return nil, fmt.Errorf("%s: %w", path, err)
	}
	for i := range w.Stages {
		s := &w.Stages[i]
		if s.Kind == "command" {
			if err := resolveCommand(root, s); err != nil {
				return nil, err
			}
			continue
		}
		if policy == nil || policy.Seats[s.Seat] == "" {
			return nil, fmt.Errorf("workflow-unknown-seat: %s: seat %s is not mapped by policy", path, s.Seat)
		}
	}
	return w, nil
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
	result, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES(?,?,?,?, 'running')
 ON CONFLICT(item_id,attempt,node_id) DO UPDATE SET run_id=excluded.run_id,outcome='running' WHERE node_runs.outcome='running'`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID, d.RunRequest.RunID)
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
	index, err := l.nextWorkflowNode(ctx, d)
	if err != nil {
		return err
	}
	if index < 0 {
		return fmt.Errorf("workflow-next-node: no ready successor")
	}
	next.NodeIndex = index
	next.PreviousReport = report
	head, err := l.revParse(ctx, l.laneTarget(d.LaneID), "HEAD")
	if err != nil {
		return err
	}
	next.NodeBaseSHA = head
	next.NodeStartRunID = ""
	next.WorkflowRestart = false
	next.WorkflowRelease = false
	stage := next.Workflow.Stages[next.NodeIndex]
	next.Seat = stage.Seat
	next.Action, next.Reason = ActionDispatch, "workflow-next-node"
	next.From, next.To = "running", "running"
	runID := attemptRunID(d.ItemID, d.Attempt) + "-n" + strconv.Itoa(next.NodeIndex+1)
	if stage.Kind == "command" {
		next.RunRequest = stage.commandRequest(d.ItemID, d.LaneID, runID)
	} else {
		next.RunRequest = l.cfg.Policy.request(d.ItemID, d.LaneID, runID, l.cfg.Policy.firstBackend(itemRow{ItemID: d.ItemID, Seat: stage.Seat}))
		next.RunRequest.TimeoutMS = stage.TimeoutMS
	}
	if next.RunRequest == nil {
		return fmt.Errorf("workflow seat %s cannot build a request", stage.Seat)
	}
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
	head, err := l.revParse(ctx, target, "HEAD")
	if err != nil {
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
			return true, Externalf("lane %s retained after preservation failed", d.LaneID)
		}
		return true, l.markExamined(ctx, d)
	}
	if stage.Kind == "command" && (rec.State != run.StateExited || rec.ExitCode == nil || (*rec.ExitCode != 0 && *rec.ExitCode != 1)) {
		if err := l.nodeOutcome(ctx, d, "fault"); err != nil {
			return true, err
		}
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, "command node machinery failed: "+stage.ID); stop != "" {
			return true, Externalf("lane %s retained after preservation failed", d.LaneID)
		}
		return true, l.markExamined(ctx, d)
	}
	failedCommand := stage.Kind == "command" && *rec.ExitCode == 1
	if failedCommand || (stage.Kind == "agent" && (rec.ExitCode == nil || *rec.ExitCode != 0 || rec.State != run.StateExited || reportFooter(log) != "done")) {
		if err := l.nodeOutcome(ctx, d, "failed"); err != nil {
			return true, err
		}
		if failedCommand {
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
		if stage.Kind == "agent" && reportFooter(log) == "blocked" {
			if err := l.reportedBlock(ctx, d, log); err != nil {
				return true, err
			}
		}
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, "workflow node ended with "+reportFooter(log)); stop != "" {
			return true, Externalf("lane %s retained after preservation failed", d.LaneID)
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
			return true, Externalf("lane %s retained after preservation failed", d.LaneID)
		}
		return true, l.markExamined(ctx, d)
	}
	if err := l.acceptNodeOutputs(ctx, d, artifacts); err != nil {
		return true, err
	}
	next, err := l.nextWorkflowNode(ctx, d)
	if err != nil {
		return true, err
	}
	if next < 0 {
		return false, nil
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
