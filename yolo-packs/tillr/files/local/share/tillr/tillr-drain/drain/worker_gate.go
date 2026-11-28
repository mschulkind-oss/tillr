package drain

import (
	"context"
	"database/sql"
	"fmt"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

type settlementClaim struct {
	decision   Decision
	work       *pendingWork
	tickID     string
	claimed    bool
	lockStart  time.Time
	lockEnd    time.Time
	claimStart time.Time
	claimEnd   time.Time
}

// examineWorkerEnd consumes the same terminal record the pass would settle,
// after the worker boundary has closed. Keep the run active until the handoff
// answers so a concurrent pass cannot also claim the work. The durable claim
// still arbitrates with other loop processes and changes to the item's inputs.
func (l *Loop) examineWorkerEnd(ctx context.Context, d Decision, watched []string, completion *runCompletion) {
	// Join shutdown only once the worker is done. Cancellation and the
	// shutdown barrier prevent adding an examination after the join starts.
	l.mu.Lock()
	if ctx.Err() != nil {
		l.mu.Unlock()
		return
	}
	l.examinations.Add(1)
	l.mu.Unlock()
	defer l.examinations.Done()
	handoffStart := time.Now()
	if stopped, err := l.stopRequested(ctx); stopped || err != nil {
		completion.outcome.err = err
		return
	}
	if d.RunRequest == nil {
		return
	}
	at := l.cfg.Now().UTC()
	claim, err := l.claimWorkerSettlement(ctx, d.ItemID, d.RunRequest.RunID, at)
	if err != nil || !claim.claimed {
		completion.outcome.err = err
		return
	}
	timing := &PassTiming{TickID: claim.tickID, AtMS: at.UnixMilli(), GateBudgetMS: l.gateBudget.Milliseconds()}
	preSpans := []handoffSpan{
		newHandoffSpan("restart_lock_wait", claim.lockStart, claim.lockEnd),
		newHandoffSpan("claim_worker_settlement", claim.claimStart, claim.claimEnd),
	}
	examined := l.examineClaimed(withPreHandoffSpans(ctx, handoffStart, preSpans), claim.decision, claim.work, at, timing, watched)
	timing.Applied = 1
	timing.DecisionCounts = map[string]int{ActionSettle: 1}
	timing.EndMS = l.cfg.Now().UnixMilli()
	examined.dispatchTiming = completion.timing
	if completion.result != nil {
		mergeExamination(examined.result, completion.result)
	}
	*completion = examined
}

// claimWorkerSettlement claims exclusive examination ownership of (itemID, runID)
// by a direct per-item compare-and-set in one WriteTx, without calling planDefinition.
// It applies uniformly to commit, no-commit, parallel, evaluator, and conflict-fix nodes.
func (l *Loop) claimWorkerSettlement(ctx context.Context, itemID, runID string, at time.Time) (settlementClaim, error) {
	var out settlementClaim
	if itemID == "" || runID == "" {
		return out, nil
	}
	if stopped, err := l.stopRequested(ctx); stopped || err != nil {
		return out, err
	}
	out.lockStart = time.Now()
	claimCtx, unlock, err := l.lockRestartMechanical(ctx)
	out.lockEnd = time.Now()
	if err != nil {
		return out, err
	}
	s, err := store.Open(claimCtx, l.cfg.Store, true)
	if err != nil {
		unlock()
		return out, err
	}
	defer s.Close()

	out.claimStart = time.Now()
	err = s.WriteTx(claimCtx, func(tx *store.Tx) error {
		r := tx.Reader()
		works, byRun, err := readPendingHistory(r)
		if err != nil {
			return err
		}
		work := works[itemID]
		if !isWorkSettleEligible(work, runID) {
			return nil
		}
		d := work.Decision
		available, err := availableRotationLanes(r, []string{d.LaneID})
		if err != nil || len(available) == 0 {
			return err
		}
		leases, err := activeLeases(r, at, []string{d.LaneID})
		if err != nil || len(leases) > 0 {
			return err
		}
		if len(work.resumes) != 0 {
			for _, node := range parallelNodes(d) {
				if node.RunRequest == nil {
					continue
				}
				live, err := liveContinuation(r, work.resumes[node.RunRequest.RunID], node, at)
				if err != nil {
					return err
				}
				if live {
					return nil
				}
			}
		}
		if paused, err := pausedPendingWork(r, work); err != nil || paused {
			return err
		}
		var rec *run.Record
		if len(d.Parallel) > 0 {
			finished, err := parallelTerminal(r, d)
			if err != nil || !finished {
				return err
			}
		} else {
			rec, err = terminalRecord(r, runID)
			if err != nil || rec == nil || rec.State == run.StatePaused {
				return err
			}
		}
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if _, faulted := faults[itemID]; faulted {
			return nil
		}
		declared, err := definition.ForReader(r)
		if err != nil {
			return readFailure(err)
		}
		var itemState string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&itemState); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return readFailure(err)
		}
		if !pendingWorkStateEligible(declared.Parsed, work, itemState) {
			return nil
		}
		if held, err := l.settlementHeldByBackendOrWait(r, d, rec, byRun, at); err != nil || held {
			return err
		}

		d.From = "running"
		d.To = "candidate"
		d.Action = ActionSettle
		d.Reason = ActionSettle
		identifyDecision(&d)

		gen, err := allocateGeneration(tx)
		if err != nil {
			return err
		}
		d.PassID = gen
		reqCopy := *d.RunRequest
		reqCopy.CorrelationID = gen
		d.RunRequest = &reqCopy

		copyWork := *work
		claimedWork := &copyWork
		owner, err := currentLoopOwner()
		if err != nil {
			return err
		}
		if d.Workflow != nil {
			claimedWork.Owner, claimedWork.Examining = owner, true
			if err := appendEffect(tx, examinationClaimedKind, d.RunRequest.RunID, owner, at); err != nil {
				return err
			}
		} else {
			claimedWork.Examiner = owner
			if err := appendEffect(tx, examinationOwnerKind, d.RunRequest.RunID, owner, at); err != nil {
				return err
			}
		}
		// Settlement must retain the candidate's worktree too. A terminal record
		// released the run's lease, so reclaim it before inspecting or gating it.
		if _, err := tx.Exec(`INSERT INTO lanes(lane_id,state,run_id,leased_until) VALUES(?,?,?,NULL) ON CONFLICT(lane_id) DO UPDATE SET state=excluded.state,run_id=excluded.run_id,leased_until=NULL`, d.LaneID, run.LaneStateLeased, d.RunRequest.RunID); err != nil {
			return err
		}
		if err := recordPassDecision(tx, d, at); err != nil {
			return err
		}
		out.decision = d
		out.work = claimedWork
		out.tickID = gen
		out.claimed = true
		return nil
	})
	unlock()
	out.claimEnd = time.Now()
	if err != nil {
		out.claimed = false
		return out, err
	}
	return out, nil
}

// shouldExamineInline guards launchRun's inline fallback when the standalone
// examine coordinator is not running (e.g. single-pass unit tests calling
// l.pass directly). In live Loop.Run execution the examine coordinator handles
// every node kind outside the pass without this filter.
func (l *Loop) shouldExamineInline(ctx context.Context, d Decision) bool {
	if d.Workflow == nil {
		return true
	}
	if quick, err := planTookQuickWay(l.cfg.Root, d); quick || err != nil {
		return quick
	}
	if workflowCandidatePolicy(d.Workflow) == "no_commit" {
		return false
	}
	var done bool
	if err := l.readStore(ctx, func(r *store.Reader) error {
		outcomes, err := workflowOutcomes(r, d.ItemID, d.Attempt)
		if err != nil {
			return err
		}
		for _, node := range parallelNodes(d) {
			outcomes[d.Workflow.Stages[node.NodeIndex].ID] = "passed"
		}
		done = workflowDone(d.Workflow, outcomes)
		return nil
	}); err != nil {
		return false
	}
	return done
}

func isWorkSettleEligible(work *pendingWork, runID string) bool {
	if work == nil || work.Examining || work.Examiner != nil {
		return false
	}
	return isWorkSettleEligibleIgnoringClaim(work, runID)
}

func isWorkSettleEligibleIgnoringClaim(work *pendingWork, runID string) bool {
	if work == nil || work.Done || work.Ready || work.Candidate != nil {
		return false
	}
	if work.BoundaryFailed || work.RepairHold != nil || work.Orphaned || work.ResumeAfterRestart {
		return false
	}
	if work.LaneReleased || work.LaneBusyByOther || work.ReopenedPausedJudge {
		return false
	}
	if work.Decision.RunRequest == nil || work.Decision.LaneID == "" {
		return false
	}
	if runID != "" && work.Decision.RunRequest.RunID != runID {
		return false
	}
	return true
}

// holdUnsettledExamination records a single settle-failed fault hold when an
// examination finishes without settling the run and nothing else prevents it
// from being claimed again on the next poll.
func (l *Loop) holdUnsettledExamination(ctx context.Context, d Decision, work *pendingWork, at time.Time, reason string) error {
	if d.ItemID == "" || d.RunRequest == nil || d.RunRequest.RunID == "" || l.cfg.Store == "" {
		return nil
	}
	runID := d.RunRequest.RunID
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	stillUnsettled := false
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		works, byRun, err := readPendingHistory(r)
		if err != nil {
			return err
		}
		w := works[d.ItemID]
		if !isWorkSettleEligibleIgnoringClaim(w, runID) {
			return nil
		}
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		if _, faulted := faults[d.ItemID]; faulted {
			return nil
		}
		declared, err := definition.ForReader(r)
		if err != nil {
			return readFailure(err)
		}
		var itemState string
		if err := r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, d.ItemID).Scan(&itemState); err != nil {
			if err == sql.ErrNoRows {
				return nil
			}
			return readFailure(err)
		}
		if !pendingWorkStateEligible(declared.Parsed, w, itemState) {
			return nil
		}
		if len(w.resumes) != 0 {
			for _, node := range parallelNodes(w.Decision) {
				if node.RunRequest == nil {
					continue
				}
				live, err := liveContinuation(r, w.resumes[node.RunRequest.RunID], node, at)
				if err != nil {
					return err
				}
				if live {
					return nil
				}
			}
		}
		if paused, err := pausedPendingWork(r, w); err != nil || paused {
			return err
		}
		var rec *run.Record
		if len(w.Decision.Parallel) > 0 {
			finished, err := parallelTerminal(r, w.Decision)
			if err != nil || !finished {
				return err
			}
		} else {
			rec, err = terminalRecord(r, runID)
			if err != nil || rec == nil || rec.State == run.StatePaused {
				return err
			}
		}
		if held, err := l.settlementHeldByBackendOrWait(r, w.Decision, rec, byRun, at); err != nil || held {
			return err
		}
		stillUnsettled = true
		return nil
	})
	s.Close()
	if err != nil || !stillUnsettled {
		return err
	}
	detail := strings.TrimSpace(reason)
	if detail == "" {
		detail = fmt.Sprintf("examination released run %s without settling it", runID)
	}
	err = l.holdItemFault(ctx, Candidate{ItemID: d.ItemID, RunID: runID, LaneID: d.LaneID}, ReasonSettleFailed, detail)
	if terminal := asItemTerminal(err); terminal != nil {
		return l.settleTerminalItem(ctx, work, terminal)
	}
	return err
}

func (l *Loop) settlementHeldByBackendOrWait(r *store.Reader, d Decision, rec *run.Record, byRun map[string]*pendingWork, at time.Time) (bool, error) {
	if rec == nil {
		return false, nil
	}
	policy := l.currentPolicy()
	var shut map[string]bool
	loadShut := func() (map[string]bool, error) {
		if shut != nil {
			return shut, nil
		}
		var err error
		shut, err = readQuotaShutBackendsFrom(r, at, policy, byRun)
		return shut, err
	}
	if evaluatorNode(d) && (rec.State == run.StateFailedToStart || judgeQuotaRefusal(*rec)) {
		s, err := loadShut()
		if err != nil {
			return false, err
		}
		if policy != nil {
			target, _, hasBackend := resolveStepBackend(policy, d.ItemID, d.Seat, d.RunRequest.Backend, s)
			if !hasBackend || s[target] {
				return true, nil
			}
		} else if s[d.RunRequest.Backend] {
			return true, nil
		}
	}
	if quotaSingleRun(d) {
		backend, err := quotaPauseBackend(r, rec.RunID)
		if err != nil {
			return false, err
		}
		_, _, _, refused := refusedWindow(*rec)
		if backend != "" && (rec.State == run.StatePaused || refused) {
			if policy == nil {
				return true, nil
			}
			s, err := loadShut()
			if err != nil {
				return false, err
			}
			target, _, hasBackend := resolveStepBackend(policy, d.ItemID, d.Seat, backend, s)
			if !hasBackend || s[target] {
				return true, nil
			}
		}
	}
	failed, err := failedDesignerResume(d, *rec)
	if err != nil {
		return false, err
	}
	if failed {
		wait, _, err := item.ReadWaitingOn(r, d.ItemID)
		if err != nil {
			return false, err
		}
		if wait != nil {
			return true, nil
		}
		s, err := loadShut()
		if err != nil {
			return false, err
		}
		target := d.RunRequest.Backend
		if policy != nil {
			if resolved, _, hasBackend := resolveStepBackend(policy, d.ItemID, "design", d.RunRequest.Backend, s); hasBackend {
				target = resolved
			}
		}
		if s[target] {
			return true, nil
		}
	}
	return false, nil
}
