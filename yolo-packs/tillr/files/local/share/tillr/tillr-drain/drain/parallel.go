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
		if policy != nil && !node.WorkflowRestart && node.SelectSeat {
			_, override := policy.Overrides[node.ItemID]
			pick := policy.SeatPicks[node.Seat]
			if !override && (len(policy.SeatShares[node.Seat]) > 0 || pick == "round-robin" || pick == "random") {
				// The proposed backend is only a preview; the claim checks the draw.
				continue
			}
		}
		if node.RunRequest == nil || node.RunRequest.Backend == "command" {
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
	indices := []int{d.NodeIndex}
	candidates := []int{}
	shut, err := readQuotaShutBackendsFrom(tx.Reader(), l.cfg.Now(), l.currentPolicy(), byRun)
	if err != nil {
		return err
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
				_, _, ok := l.cfg.Policy.availableBackend(l.cfg.Policy.firstBackend(itemRow{ItemID: d.ItemID, Seat: s.Seat}), s.Seat, shut)
				if !ok {
					continue
				}
			}
			candidates = append(candidates, i)
		}
	}
	sort.Slice(candidates, func(i, j int) bool { return d.Workflow.Stages[candidates[i]].ID < d.Workflow.Stages[candidates[j]].ID })
	for _, i := range candidates {
		if len(indices) < limit {
			indices = append(indices, i)
		}
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
				node.RunRequest = stage.commandRequest(d.ItemID, d.LaneID, id)
			} else {
				name, degraded, _ := l.cfg.Policy.availableBackend(l.cfg.Policy.firstBackend(itemRow{ItemID: d.ItemID, Seat: stage.Seat}), stage.Seat, shut)
				node.RunRequest = l.cfg.Policy.request(d.ItemID, d.LaneID, id, name)
				if degraded != "" {
					node.Reason = seatDegradedReason(degraded, name)
				}
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
		var finished bool
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			rec, e := terminalRecord(r, node.RunRequest.RunID)
			finished = rec != nil
			return e
		})
		s.Close()
		if err != nil {
			return d, err
		}
		if finished {
			continue
		}
		node.NodeBaseSHA = d.NodeBaseSHA
		paths, err := l.nodeInputPaths(ctx, node)
		if err != nil {
			return d, err
		}
		spec := StageSpec{Policy: l.currentPolicy(), Continue: true, Isolated: true, Node: &node.Workflow.Stages[node.NodeIndex], InputPaths: paths, LaneID: node.LaneID, ItemID: node.ItemID, RunID: node.RunRequest.RunID, Backend: node.RunRequest.Backend, BaseSHA: base, WorkingDir: node.RunRequest.WorkingDir, TimeoutMS: node.RunRequest.TimeoutMS}
		if err = l.cfg.Stage.Stage(ctx, spec); err != nil {
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
func (l *Loop) runParallel(ctx context.Context, d Decision) error {
	nodes := parallelNodes(d)
	var wg sync.WaitGroup
	defer wg.Wait()
	errs := make([]error, len(nodes))
	for i, node := range nodes {
		s, err := store.OpenRead(ctx, l.cfg.Store)
		if err != nil {
			return err
		}
		var finished bool
		err = s.ReadTx(ctx, func(r *store.Reader) error {
			rec, e := terminalRecord(r, node.RunRequest.RunID)
			finished = rec != nil
			return e
		})
		s.Close()
		if err != nil {
			return err
		}
		if finished {
			continue
		}
		wg.Add(1)
		go func(i int, node Decision) {
			defer wg.Done()
			if node.BuildRestartFrom != "" {
				if errs[i] = l.awaitBuildReader(ctx, node.BuildRestartFrom); errs[i] != nil {
					return
				}
			}
			req := *node.RunRequest
			stampDecisionRequest(node, &req)
			_, _, errs[i] = l.runRestartable(ctx, req)
		}(i, node)
	}
	wg.Wait()
	for i, err := range errs {
		if err != nil {
			return fmt.Errorf("reader %s: %w", nodes[i].RunRequest.RunID, err)
		}
	}
	return nil
}

func parallelTerminal(r *store.Reader, d Decision) (bool, error) {
	for _, node := range parallelNodes(d) {
		rec, err := terminalRecord(r, node.RunRequest.RunID)
		if err != nil {
			return false, err
		}
		if rec == nil {
			return false, nil
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
	for i, node := range nodes {
		stage := node.Workflow.Stages[node.NodeIndex]
		rec := recs[i]
		log := logs[i]
		if stage.Kind == "agent" {
			if obs, isObstacle, obsErr := l.offeredObstacle(ctx, rec.RunID); obsErr != nil {
				return laneOutcome{err: obsErr}
			} else if isObstacle {
				log = formatObstacleReport(obs)
				logs[i] = log
			}
		}
		if stage.Kind == "agent" && (ClassifyAttempt(rec, log) == AttemptUnjudged || rec.State == run.StatePaused) {
			resumed, resumedLog, stop, e := l.resumeAfterWall(ctx, node.LaneID, *node.RunRequest, l.lanePath(node.LaneID), rec, timing.TickID, l.cfg.Now(), result, watched)
			if e != nil || stop != "" {
				return laneOutcome{err: e, stop: stop}
			}
			if resumed.RunID == "" || ClassifyAttempt(resumed, resumedLog) == AttemptUnjudged || resumed.State == run.StatePaused {
				if err = l.nodeOutcome(ctx, node, "unjudged"); err != nil {
					return laneOutcome{err: err}
				}
				failed = true
				continue
			}
			rec, log = resumed, resumedLog
			recs[i], logs[i] = rec, log
		}
		dir := RunDir(l.cfg.Root, rec.RunID)
		if err = os.MkdirAll(dir, 0755); err != nil {
			return laneOutcome{err: err}
		}
		if err = os.WriteFile(filepath.Join(dir, "node-report.md"), []byte(log), 0644); err != nil {
			return laneOutcome{err: err}
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
					return laneOutcome{err: e}
				}
				if ok {
					nodes[i] = updated
					retry = true
					continue
				}
				outcome = "fault"
			case class == AttemptRefused:
				stop, e := l.holdAdapterRefusal(ctx, node.LaneID, *node.RunRequest, log)
				if e == nil && stop == "" {
					e = l.markExamined(ctx, d)
				}
				return laneOutcome{stop: stop, err: e}
			case class == AttemptInfrastructure || !readableFooter(reportFooter(log)):
				// No footer, or one naming no ending, is no answer: this reader alone runs
				// again and its finished siblings are kept.
				updated, ok, e := l.parallelFailover(ctx, node, rec, result)
				if e != nil {
					return laneOutcome{err: e}
				}
				if ok {
					nodes[i] = updated
					retry = true
					infraRetry = true
					continue
				}
				outcome = "fault"
			case reportFooter(log) == "failed":
				outcome = "failed"
				workFailure = true
			case reportFooter(log) != "done":
				outcome = "blocked"
				if reportFooter(log) == "blocked" {
					if err = l.reportedBlock(ctx, node, log); err != nil {
						return laneOutcome{err: err}
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
					return laneOutcome{err: err}
				}
			} else if err = l.acceptNodeOutputs(ctx, node, artifacts); err != nil {
				return laneOutcome{err: err}
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
				return laneOutcome{err: err}
			}
		}
		failed = failed || outcome == "failed" || outcome == "fault" || outcome == "blocked"
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
			reason = "workflow-node-failover"
		}
		return laneOutcome{err: l.advanceParallelRetry(ctx, work, nodes, reason)}
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
