package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func parallelNodes(d Decision) []Decision {
	primary := d
	primary.Parallel = nil
	return append([]Decision{primary}, d.Parallel...)
}

// dispatchQuotaOpen checks only requests this dispatch will start. Retained
// parallel groups keep completed results; those backends cannot block a retry.
func dispatchQuotaOpen(r *store.Reader, d Decision, shut map[string]bool, policy *Policy) (bool, error) {
	for _, node := range parallelNodes(d) {
		if node.RunRequest == nil || node.RunRequest.Backend == "command" {
			continue
		}
		if usesSeatShares(policy, node) {
			// The proposed backend is only a preview; a previous draw is binding.
			saved, err := waitingSeatBackend(r, node.RunRequest.RunID, node.Seat, policy.SeatList(node.Seat))
			if err != nil || shut[saved] {
				return false, err
			}
			continue
		}
		if len(d.Parallel) > 0 {
			rec, err := terminalRecord(r, node.RunRequest.RunID)
			if err != nil {
				return false, err
			}
			if rec != nil {
				continue
			}
		}
		if shut[node.RunRequest.Backend] {
			return false, nil
		}
	}
	return true, nil
}

// claimParallel chooses the ready readers from the same store transaction as the
// item's claim. A writer always keeps the lane to itself.
func (l *Loop) claimParallel(tx *store.Tx, d *Decision) error {
	return l.claimParallelFrom(tx, d, nil)
}

func (l *Loop) claimParallelFrom(tx *store.Tx, d *Decision, byRun map[string]*pendingWork) error {
	return l.claimParallelWithShut(tx, d, byRun, nil)
}

func (l *Loop) claimParallelWithShut(tx *store.Tx, d *Decision, byRun map[string]*pendingWork, shut map[string]bool) error {
	return l.claimParallelWithWorks(tx, d, nil, byRun, shut)
}

func (l *Loop) claimParallelWithWorks(tx *store.Tx, d *Decision, works, byRun map[string]*pendingWork, shut map[string]bool) error {
	if d.Workflow == nil || evaluatorNode(*d) || d.Workflow.Stages[d.NodeIndex].Access != "read_only" || len(d.Parallel) > 0 {
		return nil
	}
	outcomes, err := workflowOutcomes(tx.Reader(), d.ItemID, d.Attempt)
	if err != nil {
		return err
	}
	limit := d.Workflow.MaxParallel
	if limit == 0 {
		limit = 4
	}
	expressByID, err := readExpressByID(tx.Reader())
	if err != nil {
		return err
	}
	ordinary := !expressByID[d.ItemID] && !isExpressLane(d.LaneID) && !strings.HasPrefix(d.LaneID, "desk-")
	agentBudget := limit
	if ordinary {
		if works == nil {
			works, byRun, err = readPendingHistory(tx.Reader())
			if err != nil {
				return err
			}
		}
		active, err := collectActiveOrdinaryAgents(tx.Reader(), l.cfg.Now().UTC(), works, byRun, expressByID)
		if err != nil {
			return err
		}
		used := len(active)
		if d.Workflow.Stages[d.NodeIndex].Kind != "command" && (d.RunRequest == nil || active[d.RunRequest.RunID] == "") {
			used++
		}
		agentBudget = max(0, ordinaryLaneCount(l.rotationLanes())-used)
	}
	indices := []int{d.NodeIndex}
	candidates := []int{}
	if shut == nil {
		shut, err = readQuotaShutBackendsFrom(tx.Reader(), l.cfg.Now(), l.currentPolicy(), byRun)
		if err != nil {
			return err
		}
	}
	for i, s := range d.Workflow.Stages {
		if i == d.NodeIndex || s.ID == evaluatorSlot(d.Workflow) || outcomes[s.ID] != "" || s.Access != "read_only" {
			continue
		}
		ready, incoming, followed := true, false, false
		for _, e := range d.Workflow.Edges {
			if e.To != s.ID {
				continue
			}
			incoming = true
			value := outcomes[e.From]
			if value == "" || value == "running" {
				ready = false
			}
			match := value == "passed"
			if e.WhenOutcome == "fail" {
				match = value == "handled-fail"
			}
			for _, id := range e.RequiresPassed {
				match = match && outcomes[id] == "passed"
				if outcomes[id] == "" || outcomes[id] == "running" {
					ready = false
				}
			}
			followed = followed || match
		}
		if ready && (!incoming || followed) {
			if s.Kind == "agent" {
				backend := l.cfg.Policy.FirstBackend(d.ItemID, s.Seat)
				if !l.cfg.Policy.SeatMayStart(d.ItemID, s.Seat, backend, shut) {
					continue
				}
			}
			candidates = append(candidates, i)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return d.Workflow.Stages[candidates[i]].ID < d.Workflow.Stages[candidates[j]].ID })
	for _, i := range candidates {
		if len(indices) >= limit {
			break
		}
		if ordinary && d.Workflow.Stages[i].Kind != "command" {
			if agentBudget <= 0 {
				continue
			}
			agentBudget--
		}
		indices = append(indices, i)
	}
	if len(indices) < 2 {
		return nil
	}
	group := d.RunRequest.RunID
	for _, i := range indices {
		node := *d
		node.Parallel = nil
		node.NodeIndex = i
		stage := d.Workflow.Stages[i]
		node.Seat = stage.Seat
		if i != d.NodeIndex {
			node.SelectSeat = true
			id := attemptRunID(d.ItemID, d.Attempt) + "-n" + strconv.Itoa(i+1)
			if stage.Kind == "command" {
				node.RunRequest = commandRequest(stage, d.ItemID, d.LaneID, id)
			} else {
				name := l.cfg.Policy.FirstBackend(d.ItemID, stage.Seat)
				node.RunRequest = l.cfg.Policy.Request(d.ItemID, d.LaneID, id, name)
			}
		} else {
			req := *node.RunRequest
			node.RunRequest = &req
		}
		if node.RunRequest == nil {
			return fmt.Errorf("parallel node %s has no backend", stage.ID)
		}
		node.RunRequest.WorkingDir = d.RunRequest.WorkingDir
		node.RunRequest.NodeID = stage.ID
		node.RunRequest.TimeoutMS = stage.TimeoutMS
		node.RunRequest.ReadOnlyGroup = group
		node.RunRequest.CorrelationID = d.RunRequest.CorrelationID
		stampDecisionRequest(node, node.RunRequest)
		if i == d.NodeIndex {
			d.RunRequest = node.RunRequest
		} else {
			d.Parallel = append(d.Parallel, node)
		}
	}
	return nil
}

func (l *Loop) stageParallel(ctx context.Context, d Decision, base string) (Decision, error) {
	if len(d.Parallel) == 0 {
		return d, nil
	}
	// The primary stager already prepared the one worktree and lane base. Copy its
	// metadata before staging siblings; adapters never read the shared lane brief.
	lane := l.laneDirPath(d.LaneID)
	if stagedBase := l.laneBase(d.LaneID); stagedBase != "" {
		base = stagedBase
	}
	if !d.WorkflowRestart {
		dir := run.NodeDir(lane, d.RunRequest.RunID)
		if err := os.MkdirAll(dir, 0755); err != nil {
			return d, err
		}
		names := []string{BriefFile, SessionIDFile}
		if !l.nonLanding {
			names = append(names, FenceFile, BaseSHAFile)
		}
		for _, name := range names {
			body, err := os.ReadFile(filepath.Join(lane, name))
			if err != nil {
				return d, err
			}
			if err = os.WriteFile(filepath.Join(dir, name), body, 0644); err != nil {
				return d, err
			}
		}
		if body, err := os.ReadFile(filepath.Join(lane, braceLiteralsFile)); err == nil {
			if err = os.WriteFile(filepath.Join(dir, braceLiteralsFile), body, 0644); err != nil {
				return d, err
			}
		} else if !os.IsNotExist(err) {
			return d, err
		}
	}
	nodes := parallelNodes(d)
	for i, node := range nodes {
		if i == 0 && !d.WorkflowRestart {
			continue
		}
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return d, err
		}
		var finished, paused bool
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			rec, e := terminalRecord(r, node.RunRequest.RunID)
			finished = rec != nil
			paused = rec != nil && rec.State == run.StatePaused && rec.EndCause == run.EndRestart
			return e
		})
		s.Close()
		if err != nil {
			return d, err
		}
		if paused {
			req := *node.RunRequest
			req.Resume = true
			req.SessionTurn, req.ResumePrompt, req.FreshSession = false, "", false
			node.RunRequest = &req
			nodes[i] = node
			continue
		}
		if finished {
			continue
		}
		node.NodeBaseSHA = d.NodeBaseSHA
		paths, err := l.nodeInputPaths(ctx, node)
		if err != nil {
			return d, err
		}
		spec := StageSpec{Policy: l.currentPolicy(), Continue: true, Isolated: true, Node: &node.Workflow.Stages[node.NodeIndex], InputPaths: paths, LaneID: node.LaneID, ItemID: node.ItemID, RunID: node.RunRequest.RunID, Backend: node.RunRequest.Backend, BaseSHA: base, WorkingDir: node.RunRequest.WorkingDir, TimeoutMS: node.RunRequest.TimeoutMS, UnreadFooter: node.UnreadFooter}
		if err := l.stageInfrastructureSession(ctx, &node, &spec); err != nil {
			return d, err
		}
		if err = l.stageLane(ctx, spec); err != nil {
			return d, err
		}
		node.NodeStartRunID = node.RunRequest.RunID
		nodes[i] = node
	}
	d = nodes[0]
	d.Parallel = nodes[1:]
	for _, node := range parallelNodes(d) {
		if node.Workflow.Stages[node.NodeIndex].Kind == "agent" {
			argv := []string{}
			for i := 0; i < len(node.RunRequest.Command); i++ {
				if node.RunRequest.Command[i] == "--brief" && i+1 < len(node.RunRequest.Command) {
					i++
					continue
				}
				argv = append(argv, node.RunRequest.Command[i])
			}
			node.RunRequest.Command = append(argv, "--brief", filepath.Join(run.NodeDir(lane, node.RunRequest.RunID), BriefFile))
		}
	}
	return d, l.recordStagedNode(ctx, d)
}

// runParallel keeps one outside-boundary bracket around all readers. No sibling
// completion can blame a still-running reader or release the item's lane early.
func (l *Loop) runParallel(ctx context.Context, d Decision, result *LoopResult, timing *PassTiming, watched []string) error {
	nodes := parallelNodes(d)
	type readerCompletion struct {
		index  int
		node   Decision
		reason string
		result LoopResult
		err    error
	}
	completions := make(chan readerCompletion, len(nodes))
	var wg sync.WaitGroup
	defer wg.Wait()
	launch := func(i int) {
		node := nodes[i]
		group := nodes[0]
		group.Parallel = append([]Decision(nil), nodes[1:]...)
		wg.Add(1)
		go func() {
			defer wg.Done()
			c := readerCompletion{index: i, node: node}
			defer func() { completions <- c }()
			var rec *run.Record
			c.err = l.readStore(ctx, func(r *store.Reader) error {
				var err error
				rec, err = terminalRecord(r, node.RunRequest.RunID)
				return err
			})
			if c.err != nil {
				return
			}
			if rec == nil {
				if node.BuildRestartFrom != "" {
					if c.err = l.awaitBuildReader(ctx, node.BuildRestartFrom); c.err != nil {
						return
					}
				}
				req := *node.RunRequest
				stampDecisionRequest(node, &req)
				finished, _, err := l.runRestartable(ctx, req)
				c.err = err
				if err != nil {
					return
				}
				rec = &finished
			}
			// Hook groups retain their completion policy; their final hook is collective.
			if l.nonLanding {
				return
			}
			var outcome string
			c.err = l.readStore(ctx, func(r *store.Reader) error {
				return r.QueryRow(`SELECT outcome FROM node_runs WHERE run_id=?`, node.RunRequest.RunID).Scan(&outcome)
			})
			if c.err != nil || (outcome != "running" && outcome != "interrupted") {
				return
			}
			log := logTail(rec.ArtifactRefs)
			// An adapter refusal can release a lane. Leave that group-level disposition
			// behind the collective boundary, even though the reader has already ended.
			class := ClassifyAttempt(*rec, log)
			if class == AttemptRefused || class == AttemptInterrupted || (rec.State != run.StatePaused && (rec.EndCause == run.EndSignal || rec.EndCause == run.EndLeaseReclaimed || rec.EndCause == run.EndRestart)) {
				return
			}
			c.err = l.recordSettlementSpend(ctx, timing.TickID, l.sessionID, *rec)
			if c.err != nil {
				return
			}
			l.countSettledSpend(&c.result, *rec, l.sessionID)
			work := &pendingWork{Decision: group, BaseSHA: group.NodeBaseSHA, LoopID: l.sessionID}
			updated, _, _, ending, settled := l.settleParallelReader(ctx, work, node, *rec, log, &c.result, timing, watched)
			c.err = settled.err
			if settled.stop != "" {
				c.err = retainedAfter(node.LaneID, settled.stop)
			}
			if strings.HasPrefix(ending, "workflow-node-") {
				c.node, c.reason = updated, ending
			}
		}()
	}
	for i := range nodes {
		launch(i)
	}
	pending := len(nodes)
	var firstErr error
	held := false
	for pending > 0 {
		c := <-completions
		pending--
		mergeExamination(result, &c.result)
		if c.err != nil {
			if firstErr == nil {
				firstErr = fmt.Errorf("reader %s: %w", c.node.RunRequest.RunID, c.err)
			}
			continue
		}
		if c.reason == "" || firstErr != nil {
			continue
		}
		updated, claimed, err := l.claimParallelReplacement(ctx, nodes, c.index, c.node)
		if err != nil {
			firstErr = err
			continue
		}
		nodes = updated
		if !claimed {
			held = true
			continue
		}
		if err = l.stageParallelReplacement(ctx, nodes, c.index); err != nil {
			firstErr = err
			continue
		}
		launch(c.index)
		pending++
	}
	if firstErr != nil {
		return firstErr
	}
	if held {
		work := &pendingWork{Decision: d, BaseSHA: d.NodeBaseSHA, LoopID: l.sessionID}
		return l.advanceParallelRetry(ctx, work, nodes, "workflow-node-restart")
	}
	return nil
}

func parallelTerminal(r *store.Reader, d Decision) (bool, error) {
	for _, node := range parallelNodes(d) {
		rec, err := terminalRecord(r, node.RunRequest.RunID)
		if err != nil || rec == nil {
			return false, err
		}
	}
	return true, nil
}

func (l *Loop) settleParallel(ctx context.Context, work *pendingWork, result *LoopResult, timing *PassTiming, watched []string) laneOutcome {
	if l.nonLanding {
		return l.settleHookGroup(ctx, work, result, timing)
	}
	if err := RecordBackendOutcomes(ctx, l.cfg.Store); err != nil {
		return laneOutcome{err: err}
	}
	d := work.Decision
	if stagedBase := l.laneBase(d.LaneID); stagedBase != "" {
		copy := *work
		copy.BaseSHA = stagedBase
		work = &copy
	}
	nodes := parallelNodes(d)
	target := l.laneTarget(d.LaneID)
	head, err := l.revParse(ctx, target, "HEAD")
	if err != nil {
		return laneOutcome{err: err}
	}
	dirty, err := l.git(ctx, target, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return laneOutcome{err: err}
	}
	recs := make([]run.Record, len(nodes))
	logs := make([]string, len(nodes))
	spend := []run.Record{}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return laneOutcome{err: err}
	}
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		for i, node := range nodes {
			rec, e := terminalRecord(r, node.RunRequest.RunID)
			if e != nil {
				return e
			}
			if rec == nil {
				return fmt.Errorf("parallel node %s has no terminal record", node.RunRequest.RunID)
			}
			recs[i] = *rec
			logs[i] = logTail(rec.ArtifactRefs)
		}
		rows, e := r.Query(`SELECT run_id FROM read_only_runs WHERE group_id=?`, d.RunRequest.ReadOnlyGroup)
		if e != nil {
			return e
		}
		var ids []string
		for rows.Next() {
			var id string
			if e = rows.Scan(&id); e != nil {
				rows.Close()
				return e
			}
			ids = append(ids, id)
		}
		e = rows.Err()
		rows.Close()
		if e != nil {
			return e
		}
		for _, id := range ids {
			rec, e := terminalRecord(r, id)
			if e != nil {
				return e
			}
			if rec != nil {
				spend = append(spend, *rec)
			}
		}
		return nil
	})
	s.Close()
	if err != nil {
		return laneOutcome{err: err}
	}
	for _, rec := range spend {
		if err = l.recordSettlementSpend(ctx, timing.TickID, work.LoopID, rec); err != nil {
			return laneOutcome{err: err}
		}
		l.countSettledSpend(result, rec, work.LoopID)
	}
	if head != d.NodeBaseSHA || strings.TrimSpace(dirty) != "" {
		return laneOutcome{err: l.refuseParallelBoundary(ctx, d, work.BaseSHA, head, result, false)}
	}
	failed, retry, infraRetry := false, false, false
	var missingChecks []int
	for i, node := range nodes {
		var outcome string
		if err := l.readStore(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT outcome FROM node_runs WHERE run_id=?`, node.RunRequest.RunID).Scan(&outcome)
		}); err != nil {
			return laneOutcome{err: err}
		}
		if outcome == "running" || outcome == "interrupted" || (outcome == "unjudged" && ClassifyAttempt(recs[i], logs[i]) != AttemptUnjudged && recs[i].State != run.StatePaused) {
			updated, rec, log, ending, settled := l.settleParallelReader(ctx, work, node, recs[i], logs[i], result, timing, watched)
			if settled.err != nil || settled.stop != "" || ending == "" {
				return settled
			}
			nodes[i], recs[i], logs[i], outcome = updated, rec, log, ending
		}
		if outcome == missingCandidateOutcome {
			missingChecks = append(missingChecks, i)
		}
		retry = retry || strings.HasPrefix(outcome, "workflow-node-")
		infraRetry = infraRetry || outcome == "workflow-node-restart"
		failed = failed || outcome == "failed" || outcome == "fault" || outcome == "blocked" || outcome == "unjudged"
	}
	finalHead, err := l.revParse(ctx, target, "HEAD")
	if err != nil {
		return laneOutcome{err: err}
	}
	finalDirty, err := l.git(ctx, target, "status", "--porcelain", "--untracked-files=all")
	if err != nil {
		return laneOutcome{err: err}
	}
	if finalHead != d.NodeBaseSHA || strings.TrimSpace(finalDirty) != "" {
		return laneOutcome{err: l.refuseParallelBoundary(ctx, d, work.BaseSHA, finalHead, result, false)}
	}
	if failed {
		if stop := l.abandon(ctx, d.LaneID, d.RunRequest.RunID, head, "read-only nodes failed"); stop != "" {
			return laneOutcome{stop: stop}
		}
		return laneOutcome{err: l.markExamined(ctx, d)}
	}
	if retry {
		reason := "workflow-node-interrupted"
		if infraRetry {
			reason = "workflow-node-restart"
		}
		return laneOutcome{err: l.advanceParallelRetry(ctx, work, nodes, reason)}
	}
	for _, i := range missingChecks {
		node := nodes[i]
		writer, finding := missingCandidateFinding(node, work.BaseSHA, head, dirty, logs[i])
		if writer >= 0 {
			ready, err := l.canReturnMissingCandidate(ctx, d, writer)
			if err != nil {
				return laneOutcome{err: err}
			}
			if ready {
				return laneOutcome{err: l.returnMissingCandidate(ctx, d, writer, work.BaseSHA, finding)}
			}
		}
		if err := l.reportedBlock(ctx, node, logs[i]); err != nil {
			return laneOutcome{err: err}
		}
		if err := l.nodeOutcome(ctx, node, "blocked"); err != nil {
			return laneOutcome{err: err}
		}
		if stop := l.abandon(ctx, d.LaneID, d.RunRequest.RunID, head, "read-only nodes failed"); stop != "" {
			return laneOutcome{stop: stop}
		}
		return laneOutcome{err: l.markExamined(ctx, d)}
	}
	next, err := l.nextWorkflowNode(ctx, d)
	if err != nil {
		return laneOutcome{err: err}
	}
	if next >= 0 {
		return laneOutcome{err: l.advanceNode(ctx, d, work.BaseSHA, filepath.Join(RunDir(l.cfg.Root, d.RunRequest.RunID), "node-report.md"))}
	}
	handled, err := l.finishWorkflow(ctx, d, recs[0], work.BaseSHA, head, result)
	if err != nil || handled {
		return laneOutcome{err: err}
	}
	primary := d
	primary.Parallel = nil
	return l.settle(ctx, primary, recs[0], logs[0], work.BaseSHA, d.RunRequest.CorrelationID, l.cfg.Now(), result, timing, nil, true)
}

// settleParallelReader records one reader's answer without advancing or releasing
// the group. Only the collective settlement can inspect the lane boundary.
func (l *Loop) settleParallelReader(ctx context.Context, work *pendingWork, node Decision, rec run.Record, log string, result *LoopResult, timing *PassTiming, watched []string) (Decision, run.Record, string, string, laneOutcome) {
	d := work.Decision
	target, head := l.laneTarget(d.LaneID), d.NodeBaseSHA
	var err error
	stage := node.Workflow.Stages[node.NodeIndex]
	if stage.Kind == "agent" {
		if obs, isObstacle, obsErr := l.offeredObstacle(ctx, rec.RunID); obsErr != nil {
			return node, rec, log, "", laneOutcome{err: obsErr}
		} else if isObstacle {
			log = formatObstacleReport(obs)
		}
	}
	if stage.Kind == "agent" && (ClassifyAttempt(rec, log) == AttemptUnjudged || rec.State == run.StatePaused) {
		resumed, resumedLog, stop, e := l.resumeAfterWall(ctx, node.LaneID, *node.RunRequest, l.lanePath(node.LaneID), rec, timing.TickID, l.cfg.Now(), result, watched)
		if e != nil || stop != "" {
			return node, rec, log, "", laneOutcome{err: e, stop: stop}
		}
		if resumed.State == run.StatePaused {
			// Another replacement (or an operator pause) interrupted the
			// continued segment. The group is still pending, not unjudged.
			return node, rec, log, "", laneOutcome{}
		}
		if resumed.RunID == "" || ClassifyAttempt(resumed, resumedLog) == AttemptUnjudged {
			if err = l.nodeOutcome(ctx, node, "unjudged"); err != nil {
				return node, rec, log, "", laneOutcome{err: err}
			}
			return node, rec, log, "unjudged", laneOutcome{}
		}
		rec, log = resumed, resumedLog
	}
	dir := RunDir(l.cfg.Root, rec.RunID)
	if err = os.MkdirAll(dir, 0755); err != nil {
		return node, rec, log, "", laneOutcome{err: err}
	}
	if err = os.WriteFile(filepath.Join(dir, "node-report.md"), []byte(log), 0644); err != nil {
		return node, rec, log, "", laneOutcome{err: err}
	}
	outcome := "passed"
	workFailure := false
	if stage.Kind == "command" {
		if rec.State != run.StateExited || rec.ExitCode == nil || (*rec.ExitCode != 0 && *rec.ExitCode != 1) {
			outcome = "fault"
		} else if *rec.ExitCode == 1 {
			outcome = "failed"
			workFailure = true
		}
	} else {
		class := ClassifyAttempt(rec, log)
		switch {
		case class == AttemptInterrupted:
			updated, ok, e := l.parallelInterrupted(node, rec)
			if e != nil {
				return node, rec, log, "", laneOutcome{err: e}
			}
			if ok {
				return updated, rec, log, "workflow-node-interrupted", laneOutcome{}
			}
			outcome = "fault"
		case class == AttemptRefused:
			stop, e := l.holdAdapterRefusal(ctx, node.LaneID, *node.RunRequest, rec, log)
			if e == nil && stop == "" {
				e = l.markExamined(ctx, d)
			}
			return node, rec, log, "", laneOutcome{stop: stop, err: e}
		case class == AttemptInfrastructure:
			updated, ok, e := l.holdFailedParallelNode(ctx, node, rec, log, result)
			if e != nil {
				return node, rec, log, "", laneOutcome{err: e}
			}
			if ok {
				return updated, rec, log, "workflow-node-restart", laneOutcome{}
			}
			outcome = "fault"
		case !readableFooter(reportFooter(log)):
			// No footer, or one naming no ending, is no answer: this reader alone runs
			// again and its finished siblings are kept, without counting a normal exit
			// as an infrastructure failure.
			updated, ok, e := l.restartUnreadParallelNode(node, rec, reportFooter(log))
			if e != nil {
				return node, rec, log, "", laneOutcome{err: e}
			}
			if ok {
				return updated, rec, log, "workflow-node-restart", laneOutcome{}
			}
			outcome = "fault"
		case reportFooter(log) == "failed":
			outcome = "failed"
			workFailure = true
		case reportFooter(log) != "done":
			outcome = "blocked"
			if reportFooter(log) == "blocked" {
				if writer, _ := missingCandidateFinding(node, work.BaseSHA, head, "", log); writer >= 0 {
					outcome = missingCandidateOutcome
				} else if err = l.reportedBlock(ctx, node, log); err != nil {
					return node, rec, log, "", laneOutcome{err: err}
				}
			}
		}
	}
	if outcome == "passed" {
		artifacts, e := checkNodeOutputs(l.cfg.Root, node)
		if e != nil {
			outcome = "failed"
			c := Candidate{ItemID: d.ItemID, LaneID: d.LaneID, RunID: rec.RunID, Worktree: target, BaseSHA: work.BaseSHA, HeadSHA: head, RefusalDetail: e.Error()}
			if err = l.refuse(ctx, c, result, RefusalNodeOutput); err != nil {
				return node, rec, log, "", laneOutcome{err: err}
			}
		} else if err = l.acceptNodeOutputs(ctx, node, artifacts); err != nil {
			return node, rec, log, "", laneOutcome{err: err}
		}
	}
	if workFailure {
		for _, edge := range d.Workflow.Edges {
			if edge.From == stage.ID && edge.WhenOutcome == "fail" {
				outcome = "handled-fail"
				break
			}
		}
	}
	if outcome != "passed" {
		if err = l.nodeOutcome(ctx, node, outcome); err != nil {
			return node, rec, log, "", laneOutcome{err: err}
		}
	}
	return node, rec, log, outcome, laneOutcome{}
}
