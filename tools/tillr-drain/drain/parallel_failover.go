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
func (l *Loop) parallelFailover(ctx context.Context, node Decision, rec run.Record, result *LoopResult) (Decision, bool, error) {
	if err := l.recordBackendWindow(ctx, rec); err != nil {
		return node, false, err
	}
	l.mu.Lock()
	result.InfraFailures++
	l.infraStreak++
	l.mu.Unlock()
	request, ok, err := l.nextAvailableRequest(ctx, node.ItemID, node.LaneID, markRunID(node.RunRequest.RunID, "failover"), rec.Backend, node.Seat)
	if err != nil {
		return node, false, err
	}
	if !ok {
		request, _ = l.currentPolicy().NextRequest(node.ItemID, node.LaneID, markRunID(node.RunRequest.RunID, "failover"), rec.Backend, node.Seat)
		if request == nil {
			return node, false, Usagef("workflow seat %s has no backend route", node.Seat)
		}
	}
	request.ReadOnlyGroup = node.RunRequest.ReadOnlyGroup
	request.NodeID = node.Workflow.Stages[node.NodeIndex].ID
	request.WorkingDir = node.RunRequest.WorkingDir
	request.TimeoutMS = node.Workflow.Stages[node.NodeIndex].TimeoutMS
	request.CorrelationID = node.RunRequest.CorrelationID
	stampDecisionRequest(node, request)
	node.RunRequest = request
	node.NodeInfraFailures++
	node.WorkflowRestart = true
	return node, true, nil
}

func (l *Loop) parallelInterrupted(node Decision, rec run.Record) (Decision, bool, error) {
	policy := l.currentPolicy()
	if policy == nil {
		return node, false, Usagef("workflow seat %s has no backend route", node.Seat)
	}
	request := policy.request(node.ItemID, node.LaneID, markRunID(node.RunRequest.RunID, "interrupted"), rec.Backend)
	if request == nil {
		return node, false, Usagef("workflow seat %s has no backend %s", node.Seat, rec.Backend)
	}
	request.ReadOnlyGroup = node.RunRequest.ReadOnlyGroup
	request.NodeID = node.Workflow.Stages[node.NodeIndex].ID
	request.WorkingDir = node.RunRequest.WorkingDir
	request.TimeoutMS = node.Workflow.Stages[node.NodeIndex].TimeoutMS
	request.CorrelationID = node.RunRequest.CorrelationID
	stampDecisionRequest(node, request)
	node.RunRequest = request
	node.WorkflowRestart = true
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
	claimed := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		works, byRun, err := readPendingHistory(tx.Reader())
		if err != nil {
			return err
		}
		work := works[replacement.ItemID]
		if work == nil || work.Done || work.Orphaned || work.Owner == nil || *work.Owner != *here || work.LoopID != l.sessionID {
			return fmt.Errorf("parallel replacement lost its dispatch owner for %s", replacement.RunRequest.RunID)
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
		if replacement.NodeInfraFailures > 0 {
			if name, open := l.currentPolicy().availableBackend(replacement.RunRequest.Backend, replacement.Seat, shut); open {
				replacement.RunRequest = rerouteNodeRequest(l.currentPolicy(), replacement, name)
			}
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
	return updated, claimed, err
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
	spec := StageSpec{Policy: l.currentPolicy(), Continue: true, Isolated: true, Node: &node.Workflow.Stages[node.NodeIndex], InputPaths: paths, LaneID: node.LaneID, ItemID: node.ItemID, RunID: node.RunRequest.RunID, Backend: node.RunRequest.Backend, BaseSHA: base, WorkingDir: node.RunRequest.WorkingDir, TimeoutMS: node.RunRequest.TimeoutMS}
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
