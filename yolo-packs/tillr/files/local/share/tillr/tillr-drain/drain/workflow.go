package drain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"

	"github.com/mschulkind-oss/tillr-drain/workflow"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

type Workflow = workflow.Workflow
type WorkflowWatch = workflow.WorkflowWatch
type WorkflowCycleAnchor = workflow.WorkflowCycleAnchor
type WorkflowOscillationPair = workflow.WorkflowOscillationPair
type WorkflowExit = workflow.WorkflowExit
type WorkflowGroup = workflow.WorkflowGroup
type WorkflowStage = workflow.WorkflowStage
type WorkflowEdge = workflow.WorkflowEdge
type WorkflowArgument = workflow.WorkflowArgument
type WorkflowPort = workflow.WorkflowPort
type WorkflowScope = workflow.WorkflowScope

const (
	// WorkflowRules lists the loader's refusal names; the command prints them in help.
	WorkflowRules = workflow.WorkflowRules
	// ReasonNodeScopeOutsideFence is the refusal reason when a stage's write_scope leaves its fence.
	ReasonNodeScopeOutsideFence = workflow.ReasonNodeScopeOutsideFence
	workflowCheckPathPrefix     = workflow.WorkflowCheckPathPrefix
)

// CheckWorkflows validates a file or directory without opening the item store.
func CheckWorkflows(root, target string, out io.Writer) bool {
	return workflow.CheckWorkflows(root, target, out)
}

func seatPolicyFor(policy *Policy) *workflow.SeatPolicy {
	if policy == nil {
		return nil
	}
	return &workflow.SeatPolicy{
		Seats:           policy.Seats,
		ReadOnlySeats:   policy.ReadOnlySeats,
		SeatDeclaration: policy.SeatDeclaration(),
	}
}

func decodeWorkflow(raw []byte, ref string) (*Workflow, error) {
	return workflow.DecodeWorkflow(raw, ref)
}

func loadWorkflow(root, ref string, policy *Policy) (*Workflow, error) {
	return workflow.LoadWorkflow(root, ref, seatPolicyFor(policy))
}

func loadWorkflowWith(root, ref string, policy *Policy, args map[string]any) (*Workflow, error) {
	return workflow.LoadWorkflowWith(root, ref, seatPolicyFor(policy), args)
}

func readWorkflow(root, p string, policy *Policy, stack map[string]bool) (*Workflow, error) {
	return workflow.ReadWorkflow(root, p, seatPolicyFor(policy), stack)
}

func expandWorkflow(root, p string, policy *Policy, stack map[string]bool, args map[string]any, symbols map[string]bool) (*Workflow, error) {
	return workflow.ExpandWorkflow(root, p, seatPolicyFor(policy), stack, args, symbols)
}

func bindWorkflow(root string, raw []byte, supplied map[string]any) ([]byte, error) {
	return workflow.BindWorkflow(root, raw, supplied)
}

func scopeWithin(inner, outer []string) bool {
	return workflow.ScopeWithin(inner, outer)
}

func inheritWorkflowScope(w *Workflow, scope WorkflowScope) error {
	return workflow.InheritWorkflowScope(w, scope)
}

func scopeForLane(paths []string, lane string) []string {
	return workflow.ScopeForLane(paths, lane)
}

func validateWorkflowWatchRefs(w *Workflow, stageIDs, seats map[string]bool) error {
	return workflow.ValidateWorkflowWatchRefs(w, stageIDs, seats)
}

func validateEvaluatorJudgeClasses(w *Workflow) error {
	return workflow.ValidateEvaluatorJudgeClasses(w)
}

func requiredWorkflowNodes(w *Workflow) []string {
	return workflow.RequiredWorkflowNodes(w)
}

func workflowSize(w *Workflow) (nodes, depth, fanout int) {
	return workflow.WorkflowSize(w)
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
	next.UnreadFooter = ""
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
		next.RunRequest = commandRequest(stage, d.ItemID, d.LaneID, runID)
	} else {
		next.RunRequest = l.cfg.Policy.Request(d.ItemID, d.LaneID, runID, l.cfg.Policy.FirstBackend(d.ItemID, stage.Seat))
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
	hasKind, err := hasItemEdgesKindColumn(r)
	if err != nil {
		return false, err
	}
	query := `SELECT COALESCE(i.state, ''), '` + item.EdgeKindBlockedBy + `' FROM item_edges e LEFT JOIN items i ON i.item_id = e.blocked_by WHERE e.item_id = ?`
	if hasKind {
		query = `SELECT COALESCE(i.state, ''), COALESCE(NULLIF(e.kind, ''), '` + item.EdgeKindBlockedBy + `') FROM item_edges e LEFT JOIN items i ON i.item_id = e.blocked_by WHERE e.item_id = ?`
	}
	rows, err := r.Query(query, itemID)
	if err != nil {
		return false, err
	}
	defer rows.Close()
	for rows.Next() {
		var state, kind string
		if err := rows.Scan(&state, &kind); err != nil {
			return false, err
		}
		if state == "" || !edgeSatisfied(declared.Parsed, kind, state) {
			return true, nil
		}
	}
	return false, rows.Err()
}

const (
	quickWayFile     = "do-it-now"
	planQuickWayKind = "drain.plan-quick"
	noChangeFile     = "no-change.json"
	noChangeKind     = "drain.no-change"
)

type noChangeRecord struct {
	ItemID      string `json:"item_id"`
	Attempt     int    `json:"attempt"`
	NodeID      string `json:"node_id"`
	RunID       string `json:"run_id"`
	BaseSHA     string `json:"base_sha"`
	Cause       string `json:"cause"`
	Evidence    string `json:"evidence"`
	WhyNoChange string `json:"why_no_change"`
}

func (l *Loop) allowsNoChangeDecision(d Decision) bool {
	var stage *WorkflowStage
	if d.Workflow != nil {
		if d.NodeIndex < 0 || d.NodeIndex >= len(d.Workflow.Stages) {
			return false
		}
		stage = &d.Workflow.Stages[d.NodeIndex]
	}
	isFork := l.laneForkTarget(d.LaneID) != ""
	return allowsSingleWorkerNoChange(d.Workflow, stage, d.SelectedWorkflow, d.ItemID, isFork, d.BlockConflict, nil, d.DeskDocument) ||
		allowsPlanNoChange(d.Workflow, stage, d.ItemID, isFork, d.BlockConflict, nil, d.DeskDocument)
}

func (l *Loop) settleNoChangeNode(ctx context.Context, d Decision, rec run.Record, base string, finding landingOfferFinding) error {
	stage := d.Workflow.Stages[d.NodeIndex]
	record := noChangeRecord{
		ItemID:      d.ItemID,
		Attempt:     d.Attempt,
		NodeID:      stage.ID,
		RunID:       rec.RunID,
		BaseSHA:     base,
		Cause:       strings.TrimSpace(finding.Cause),
		Evidence:    strings.TrimSpace(finding.Evidence),
		WhyNoChange: strings.TrimSpace(finding.WhyNoChange),
	}
	scratchDir := run.DurableDir(l.laneDirPath(d.LaneID), rec.RunID)
	if err := writeLandingArtifact(scratchDir, noChangeFile, record); err != nil {
		return err
	}
	runDir := RunDir(l.cfg.Root, rec.RunID)
	if err := os.MkdirAll(runDir, 0755); err != nil {
		return err
	}
	if err := writeLandingArtifact(runDir, noChangeFile, record); err != nil {
		return err
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		seen, err := judgmentSeen(tx, noChangeKind, "run", rec.RunID)
		if err != nil {
			return err
		}
		if seen == 0 {
			if err := appendEffect(tx, noChangeKind, rec.RunID, map[string]any{
				"item_id":  record.ItemID,
				"attempt":  record.Attempt,
				"node_id":  record.NodeID,
				"run_id":   record.RunID,
				"base_sha": record.BaseSHA,
				"finding": landingOfferFinding{
					Cause:       record.Cause,
					Evidence:    record.Evidence,
					WhyNoChange: record.WhyNoChange,
				},
			}, l.cfg.Now().UTC()); err != nil {
				return err
			}
		}
		if _, err := tx.Exec(`UPDATE node_runs SET outcome='passed' WHERE run_id=?`, rec.RunID); err != nil {
			return err
		}
		if stage.ID == "plan" {
			for i, st := range d.Workflow.Stages {
				if st.ID == "work" {
					if _, err := tx.Exec(`INSERT INTO node_runs(item_id,attempt,node_id,run_id,outcome) VALUES(?,?,?,?, 'skipped') ON CONFLICT(item_id,attempt,node_id) DO NOTHING`, d.ItemID, d.Attempt, st.ID, attemptRunID(d.ItemID, d.Attempt)+"-n"+strconv.Itoa(i+1)); err != nil {
						return err
					}
					break
				}
			}
		}
		return nil
	})
}

func checkNoChangePlanOutputs(root string, d Decision) ([]nodeArtifact, error) {
	stage := d.Workflow.Stages[d.NodeIndex]
	if _, hasDelivery := stage.Outputs[deliveryOutput]; hasDelivery && classificationNode(stage) {
		w := *d.Workflow
		w.Stages = append([]WorkflowStage(nil), d.Workflow.Stages...)
		outputs := map[string]string{}
		for name, typ := range stage.Outputs {
			if name != deliveryOutput {
				outputs[name] = typ
			}
		}
		w.Stages[d.NodeIndex].Outputs = outputs
		d.Workflow = &w
	}
	return checkNodeOutputs(root, d)
}

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
	var offeredFinding *landingOfferFinding
	if offered {
		if obs, isObstacle, obsErr := l.offeredObstacle(ctx, rec.RunID); obsErr != nil {
			return true, obsErr
		} else if isObstacle {
			offered = false
			footer = "blocked"
			log = formatObstacleReport(obs)
		} else if nc, isNoChange, ncErr := l.offeredNoChange(ctx, rec.RunID); ncErr != nil {
			return true, ncErr
		} else if isNoChange {
			footer = "no_change"
			offeredFinding = &nc
		} else {
			footer = "done"
		}
	}
	noChangeAllowed := footer == "no_change" && l.allowsNoChangeDecision(d)
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
				d.UnreadFooter = ""
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
		if classifyDecisionAttempt(d, rec, log) == AttemptInfrastructure {
			return true, l.holdFailedWorkflowNode(ctx, d, base, rec, result)
		}
		if rec.State == run.StateExited && !readableFooter(footer) && !noChangeAllowed {
			return true, l.restartUnreadWorkflowNode(ctx, d, base, rec, footer)
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
		if verifyBuildReturn(d.Returned) || gateBuildReturn(d.Returned) {
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
		if footer != "done" && footer != "failed" && !noChangeAllowed {
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
	if noChangeAllowed {
		if allowsPlanNoChange(d.Workflow, &stage, d.ItemID, l.laneForkTarget(d.LaneID) != "", d.BlockConflict, nil, d.DeskDocument) {
			if !l.nonLanding && (head != base || head != nodeBase || strings.TrimSpace(dirty) != "") {
				if err := l.nodeOutcome(ctx, d, "refused"); err != nil {
					return true, err
				}
				detail := fmt.Sprintf("node %s reported no_change without a clean worktree at base", stage.ID)
				c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, Worktree: target, BaseSHA: base, HeadSHA: head, RefusalDetail: detail}
				if err := l.refuse(ctx, c, result, RefusalFence); err != nil {
					return true, err
				}
				if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, detail); stop != "" {
					return true, retainedAfter(d.LaneID, stop)
				}
				return true, l.markExamined(ctx, d)
			}
			var finding landingOfferFinding
			var findingErr string
			if offeredFinding != nil {
				finding = *offeredFinding
			} else {
				finding, findingErr = parseNoChangeFinding(log)
			}
			if findingErr == "" && quickWay {
				findingErr = fmt.Sprintf("no_change-conflict: %s cannot both record quick way and report no_change", stage.ID)
			}
			if findingErr == "" {
				artifacts, outErr := checkNoChangePlanOutputs(l.cfg.Root, d)
				if outErr != nil {
					findingErr = outErr.Error()
				} else {
					for _, a := range artifacts {
						if a.Type == "graph.proposal" {
							findingErr = fmt.Sprintf("no_change-conflict: %s cannot both propose a graph replacement and report no_change", stage.ID)
							break
						}
					}
				}
			}
			if findingErr != "" {
				if err := l.nodeOutcome(ctx, d, "failed"); err != nil {
					return true, err
				}
				c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, Worktree: target, BaseSHA: base, HeadSHA: head, RefusalDetail: findingErr}
				if err := l.refuse(ctx, c, result, RefusalNodeOutput); err != nil {
					return true, err
				}
				if stop := l.abandon(ctx, d.LaneID, rec.RunID, head, findingErr); stop != "" {
					return true, retainedAfter(d.LaneID, stop)
				}
				return true, l.markExamined(ctx, d)
			}
			if err := l.settleNoChangeNode(ctx, d, rec, base, finding); err != nil {
				return true, err
			}
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
		if err := l.nodeOutcome(ctx, d, "passed"); err != nil {
			return true, err
		}
		return false, nil
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
		unformatted, err := l.unformattedRepoFiles(ctx, target)
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
		if err := l.pinLane(ctx, d.LaneID, d.ItemID, rec.RunID, head); err != nil {
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
