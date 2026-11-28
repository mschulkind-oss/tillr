package drain

import (
	"context"
	"fmt"
	"path/filepath"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Retry only the reader whose harness failed. Finished siblings keep both their
// run identities and their checked outputs, and are not sent to a backend again.
func (l *Loop) holdFailedParallelNode(ctx context.Context, node Decision, rec run.Record, log string, result *LoopResult) (Decision, bool, error) {
	if classifyDecisionAttempt(node, rec, log) != AttemptInfrastructure {
		footer := reportFooter(log)
		if readableFooter(footer) {
			footer = ""
		}
		return l.restartUnreadParallelNode(node, rec, footer)
	}
	if err := l.recordBackendWindow(ctx, rec); err != nil {
		return node, false, err
	}
	l.countInfrastructureFailure(result, node.ItemID)
	if held, err := l.recordInfrastructureFailure(ctx, node, rec, log); held || err != nil {
		return node, false, err
	}
	return l.restartUnreadParallelNode(node, rec, "")
}

func (l *Loop) restartUnreadParallelNode(node Decision, rec run.Record, unreadFooter string) (Decision, bool, error) {
	policy := l.currentPolicy()
	if policy == nil {
		return node, false, Usagef("workflow seat %s has no backend route", node.Seat)
	}
	request := restartBackendRequest(policy, node, markRunID(node.RunRequest.RunID, "restart"), rec.Backend)
	request.ReadOnlyGroup = node.RunRequest.ReadOnlyGroup
	request.NodeID = node.Workflow.Stages[node.NodeIndex].ID
	request.WorkingDir = node.RunRequest.WorkingDir
	request.TimeoutMS = node.Workflow.Stages[node.NodeIndex].TimeoutMS
	request.CorrelationID = node.RunRequest.CorrelationID
	stampDecisionRequest(node, request)
	node.RunRequest = request
	node.WorkflowRestart = true
	node.UnreadFooter = unreadFooter
	node.UnreadReportRestart = true
	node.InfrastructureFrom = rec.RunID
	node.SelectSeat = false
	return node, true, nil
}

func (l *Loop) parallelInterrupted(node Decision, rec run.Record) (Decision, bool, error) {
	policy := l.currentPolicy()
	if policy == nil {
		return node, false, Usagef("workflow seat %s has no backend route", node.Seat)
	}
	request := restartBackendRequest(policy, node, markRunID(node.RunRequest.RunID, "interrupted"), rec.Backend)
	request.ReadOnlyGroup = node.RunRequest.ReadOnlyGroup
	request.NodeID = node.Workflow.Stages[node.NodeIndex].ID
	request.WorkingDir = node.RunRequest.WorkingDir
	request.TimeoutMS = node.Workflow.Stages[node.NodeIndex].TimeoutMS
	request.CorrelationID = node.RunRequest.CorrelationID
	stampDecisionRequest(node, request)
	node.RunRequest = request
	node.WorkflowRestart = true
	node.UnreadFooter = ""
	node.UnreadReportRestart = false
	node.InfrastructureFrom = ""
	return node, true, nil
}

func (l *Loop) advanceParallelRetry(ctx context.Context, work *pendingWork, nodes []Decision, reason string) error {
	d := nodes[0]
	d.Parallel = nodes[1:]
	d.WorkflowRestart = true
	d.Action, d.Reason = ActionDispatch, reason
	next := pendingWork{Decision: d, BaseSHA: work.BaseSHA, Ready: true, ParentRunID: work.Decision.RunRequest.RunID, LoopID: work.LoopID}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		return appendEffect(tx, nodeAdvancedKind, d.RunRequest.RunID, next, l.cfg.Now())
	})
}

// claimParallelReplacement keeps the original dispatch owner and group identity.
// The replacement is durable before staging, and its lease is claimed in the same
// transaction that checks its backend; a closed route remains pending, not run.
func (l *Loop) claimParallelReplacement(ctx context.Context, nodes []Decision, index int, replacement Decision) ([]Decision, bool, error) {
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return nodes, false, err
	}
	defer s.Close()
	here, err := currentLoopOwner()
	if err != nil {
		return nodes, false, err
	}
	updated := append([]Decision(nil), nodes...)
	updated[index] = replacement
	claimed, refused := false, false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		works, byRun, err := readPendingHistory(tx.Reader())
		if err != nil {
			return err
		}
		work := works[replacement.ItemID]
		if work == nil || work.Done || work.Orphaned || work.Owner == nil || *work.Owner != *here || work.LoopID != l.sessionID {
			return fmt.Errorf("parallel replacement lost its dispatch owner for %s", replacement.RunRequest.RunID)
		}
		if work.Examining || work.Examiner != nil {
			// The group is being settled: a replacement would overwrite the node run the
			// examination reads.
			refused = true
			return nil
		}
		faults, err := ReadItemFaults(tx.Reader())
		if err != nil {
			return err
		}
		if _, faulted := faults[replacement.ItemID]; faulted {
			refused = true
			return nil
		}
		current := parallelNodes(work.Decision)
		if index >= len(current) || current[index].RunRequest.RunID != nodes[index].RunRequest.RunID {
			return fmt.Errorf("parallel replacement lost reader %s", nodes[index].RunRequest.RunID)
		}
		replacement.Action = ActionDispatch
		shut, err := readDispatchShutFrom(tx.Reader(), l.cfg.Now(), l.currentPolicy(), replacement, byRun)
		if err != nil {
			return err
		}
		open, err := selectSeatDispatch(tx, &replacement, l.currentPolicy(), shut, l.cfg.Now())
		if err != nil {
			return err
		}
		updated[index] = replacement
		d := updated[0]
		d.Parallel = updated[1:]
		work.Decision = d
		if open {
			stampDecisionRequest(replacement, replacement.RunRequest)
			if _, err := run.ClaimLease(ctx, tx, *replacement.RunRequest, replacement.LaneID, l.cfg.Now()); err != nil {
				return err
			}
			if err := startNode(tx, replacement); err != nil {
				return err
			}
			claimed = true
		}
		return appendEffect(tx, dispatchStartedKind, d.RunRequest.RunID, work, l.cfg.Now())
	})
	if refused {
		return nodes, false, err
	}
	return updated, claimed, err
}

// itemHasOpenFault answers whether an item fault is open for itemID.
func (l *Loop) itemHasOpenFault(ctx context.Context, itemID string) (bool, error) {
	faulted := false
	err := l.readStore(ctx, func(r *store.Reader) error {
		faults, err := ReadItemFaults(r)
		_, faulted = faults[itemID]
		return err
	})
	return faulted, err
}

func (l *Loop) stageParallelReplacement(ctx context.Context, nodes []Decision, index int) error {
	node := nodes[index]
	paths, err := l.nodeInputPaths(ctx, node)
	if err != nil {
		return err
	}
	base := node.NodeBaseSHA
	if stagedBase := l.laneBase(node.LaneID); stagedBase != "" {
		base = stagedBase
	}
	spec := StageSpec{Policy: l.currentPolicy(), Continue: true, Isolated: true, Node: &node.Workflow.Stages[node.NodeIndex], InputPaths: paths, LaneID: node.LaneID, ItemID: node.ItemID, RunID: node.RunRequest.RunID, Backend: node.RunRequest.Backend, BaseSHA: base, WorkingDir: node.RunRequest.WorkingDir, TimeoutMS: node.RunRequest.TimeoutMS, UnreadFooter: node.UnreadFooter}
	if err := l.stageInfrastructureSession(ctx, &node, &spec); err != nil {
		return err
	}
	if err := l.stageLane(ctx, spec); err != nil {
		return err
	}
	node.NodeStartRunID = node.RunRequest.RunID
	if node.Workflow.Stages[node.NodeIndex].Kind == "agent" {
		argv := []string{}
		for i := 0; i < len(node.RunRequest.Command); i++ {
			if node.RunRequest.Command[i] == "--brief" && i+1 < len(node.RunRequest.Command) {
				i++
				continue
			}
			argv = append(argv, node.RunRequest.Command[i])
		}
		node.RunRequest.Command = append(argv, "--brief", filepath.Join(run.NodeDir(l.laneDirPath(node.LaneID), node.RunRequest.RunID), BriefFile))
	}
	nodes[index] = node
	d := nodes[0]
	d.Parallel = nodes[1:]
	return l.recordStagedNode(ctx, d)
}
