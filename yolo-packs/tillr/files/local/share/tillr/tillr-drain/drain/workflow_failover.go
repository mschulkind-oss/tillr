package drain

import (
	"context"
	"fmt"
	"os"
	"strings"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Queue only this node, on its original backend. Admission waits for recorded
// recovery; staging preserves earlier node commits and the graph's original base.
func (l *Loop) holdFailedWorkflowNode(ctx context.Context, d Decision, base string, rec run.Record, result *LoopResult) error {
	log := logTail(rec.ArtifactRefs)
	if classifyDecisionAttempt(d, rec, log) != AttemptInfrastructure {
		footer := reportFooter(log)
		if readableFooter(footer) {
			footer = ""
		}
		return l.restartUnreadWorkflowNode(ctx, d, base, rec, footer)
	}
	if err := l.recordBackendWindow(ctx, rec); err != nil {
		return err
	}
	l.countInfrastructureFailure(result, d.ItemID)
	if held, err := l.recordInfrastructureFailure(ctx, d, rec, log); err != nil {
		return err
	} else if held {
		if stop := l.abandon(ctx, d.LaneID, rec.RunID, l.headOf(ctx, d.LaneID), ReasonInfrastructureRepeat); stop != "" {
			return retainedAfter(d.LaneID, stop)
		}
		return l.markExamined(ctx, d)
	}
	d.InfrastructureFrom = rec.RunID
	d.UnreadFooter = ""
	return l.restartWorkflowNode(ctx, d, base, rec, "restart", "workflow infrastructure failure")
}

// Restart a workflow node that exited normally without a readable RESULT footer,
// preserving its work and resumable session without counting an infrastructure failure.
func (l *Loop) restartUnreadWorkflowNode(ctx context.Context, d Decision, base string, rec run.Record, unreadFooter string) error {
	d.InfrastructureFrom = rec.RunID
	d.UnreadFooter = unreadFooter
	preserveReason := "workflow node answer unread"
	if unreadFooter != "" {
		preserveReason = unreadFooterReason(unreadFooter)
	}
	return l.restartWorkflowNode(ctx, d, base, rec, "restart", preserveReason)
}

// A refused first message is not a paused session. Release only when the
// staged base proves that no checkout or node output needs to follow the retry.
func (l *Loop) emptyQuotaRefusal(d Decision, rec run.Record, base, head, dirty string) (bool, error) {
	if _, _, _, refused := refusedWindow(rec); !refused || l.nonLanding || d.NodeIndex != 0 || len(d.Parallel) != 0 ||
		len(d.ReusedNodes) != 0 || d.Returned != nil || d.RetryCandidate != nil || d.RepeatedCall != nil ||
		d.WorkflowRestart || rec.Resumed || d.RunRequest.Resume || d.RunRequest.SessionTurn || l.laneForkTarget(d.LaneID) != "" ||
		base == "" || head != base || d.NodeBaseSHA != base || strings.TrimSpace(dirty) != "" {
		return false, nil
	}
	entries, err := os.ReadDir(nodeOutputsDir(l.cfg.Root, rec.RunID))
	if os.IsNotExist(err) {
		return true, nil
	}
	return len(entries) == 0 && err == nil, err
}

func (l *Loop) restartInterruptedNode(ctx context.Context, d Decision, base string, rec run.Record) error {
	d.UnreadFooter = ""
	return l.restartWorkflowNode(ctx, d, base, rec, "interrupted", "run interrupted by signal")
}

func (l *Loop) restartWorkflowNode(ctx context.Context, d Decision, base string, rec run.Record, reason, preserveReason string) error {
	return l.recordWorkflowRestart(ctx, d, base, rec, reason, preserveReason, false)
}

func (l *Loop) recordWorkflowRestart(ctx context.Context, d Decision, base string, rec run.Record, reason, preserveReason string, released bool) error {
	if !l.nonLanding && !released {
		head := l.headOf(ctx, d.LaneID)
		if err := l.preserveWork(ctx, d.LaneID, rec.RunID, preserveReason); err != nil {
			if stop := l.retainUnpreserved(ctx, d.LaneID, rec.RunID, head, err); stop != "" {
				return retainedAfter(d.LaneID, stop)
			}
		}
		if err := l.pinLane(ctx, d.LaneID, rec.RunID, head); err != nil {
			return err
		}
	}
	policy := l.currentPolicy()
	if policy == nil {
		return Usagef("workflow seat %s has no backend route", d.Seat)
	}
	runID := markRunID(d.RunRequest.RunID, reason)
	request := restartBackendRequest(policy, d, runID, rec.Backend)
	request.NodeID = d.Workflow.Stages[d.NodeIndex].ID
	request.WorkingDir = d.RunRequest.WorkingDir
	stampDecisionRequest(d, request)
	d.RunRequest = request
	d.WorkflowRestart = true
	d.SelectSeat = false
	if d.InfrastructureFrom != rec.RunID {
		d.InfrastructureFrom = ""
	}
	if released {
		d.NodeStartRunID = ""
		d.NodeInfraFailures = 0 // The quota retry is pinned to the refusing backend.
	}
	d.Action, d.Reason = ActionDispatch, "workflow-node-"+reason
	if d.Workflow.Stages[d.NodeIndex].TimeoutMS > 0 {
		request.TimeoutMS = d.Workflow.Stages[d.NodeIndex].TimeoutMS
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		changed, err := tx.Exec(`UPDATE node_runs SET outcome='running' WHERE item_id=? AND attempt=? AND node_id=?`, d.ItemID, d.Attempt, d.Workflow.Stages[d.NodeIndex].ID)
		if err != nil {
			return err
		}
		n, err := changed.RowsAffected()
		if err != nil {
			return err
		}
		if n != 1 {
			return fmt.Errorf("lost workflow node during %s restart", reason)
		}
		return appendEffect(tx, nodeAdvancedKind, request.RunID, pendingWork{Decision: d, BaseSHA: base, Ready: true, ParentRunID: rec.RunID, LaneReleased: released}, l.cfg.Now().UTC())
	})
}

func restartBackendRequest(policy *Policy, d Decision, runID, backend string) *run.Request {
	if policyHasBackend(policy, backend) {
		return policy.request(d.ItemID, d.LaneID, runID, backend)
	}
	retained := *d.RunRequest
	retained.RunID = runID
	retained.Backend = backend
	return &retained
}
