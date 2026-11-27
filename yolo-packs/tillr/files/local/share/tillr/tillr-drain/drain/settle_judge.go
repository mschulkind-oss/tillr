package drain

import (
	"context"
	"database/sql"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/gateland"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

func (l *Loop) runCandidateExamination(ctx context.Context, work *pendingWork, local *LoopResult, timing *PassTiming) laneOutcome {
	c := *work.Candidate
	c.TickID = timing.TickID
	c.replayRelease = &replayLaneRelease{
		release: func(rctx context.Context, cur Candidate) (bool, error) {
			if IsTrunkFixer(cur.ItemID) {
				return false, nil
			}
			cp := *work
			cp.Candidate = &cur
			released, err := l.releaseCandidateForReplay(rctx, &cp)
			if released {
				work.LaneReleased = true
			}
			return released, err
		},
	}
	if IsTrunkFixer(c.ItemID) {
		if resolved, err := l.resolveKeptProvedFixer(ctx, c.ItemID); err != nil {
			return laneOutcome{err: err}
		} else if resolved {
			stop := l.abandon(ctx, c.LaneID, c.RunID, c.HeadSHA, "resolved elsewhere: rerun passed")
			if stop == "" {
				err = l.markExamined(ctx, work.Decision)
			}
			return laneOutcome{stop: stop, err: err}
		}
	}
	var stop string
	var landed bool
	var err error
	if c.NoChange != nil {
		stop, landed, err = l.verifyNoChange(ctx, work.Decision, &c, local)
	} else if work.RepairHold != nil {
		// Retry the retained repair, not another gate and land of the same
		// original candidate. The original conflict remains the evidence.
		endConflict := timeHandoffPhase(ctx, "conflict_fix")
		stop, landed, err = l.conflictFix(ctx, c, work.RepairHold.Conflict, local, timing, work.RepairHold.TrunkSHA)
		endConflict()
	} else if work.ConflictTrunkSHA != "" && c.BaseRef == "" && (work.ConflictHeadSHA == "" || c.HeadSHA == "" || work.ConflictHeadSHA == c.HeadSHA) {
		mainSHA, trunkErr := l.revParse(ctx, l.cfg.Root, l.trunkRef())
		if trunkErr != nil {
			err = trunkErr
		} else if mainSHA == work.ConflictTrunkSHA {
			endConflict := timeHandoffPhase(ctx, "conflict_fix")
			stop, landed, err = l.conflictFix(ctx, c, LandOutcome{Result: LandSquashConflict, TrunkAfter: mainSHA, ConflictPaths: work.ConflictPaths}, local, timing)
			endConflict()
		} else if work.DirtyHold != nil || work.Gated {
			stop, landed, err = l.landCandidate(ctx, c, local, timing)
		} else {
			stop, landed, err = l.verify(ctx, &c, local, timing)
			if err == nil && stop == "" && c.trunkAcquitted {
				err = errTrunkAcquittedHeld
			}
		}
	} else if work.DirtyHold != nil || work.Gated {
		stop, landed, err = l.landCandidate(ctx, c, local, timing)
	} else {
		stop, landed, err = l.verify(ctx, &c, local, timing)
		if err == nil && stop == "" && c.trunkAcquitted {
			err = errTrunkAcquittedHeld
		}
	}
	return l.finishCandidateOutcome(ctx, work, &c, stop, landed, err)
}

func (l *Loop) finishCandidateOutcome(ctx context.Context, work *pendingWork, c *Candidate, stop string, landed bool, err error) laneOutcome {
	if terminal := asItemTerminal(err); terminal != nil {
		// The item went terminal by another route during this examination.
		endTerminal := timeHandoffPhase(ctx, "settle_terminal")
		termErr := l.settleTerminalItem(ctx, work, terminal)
		endTerminal()
		return laneOutcome{err: termErr}
	}
	if errors.Is(err, errRestartPending) || (l.isRestartReplaced() && errors.Is(err, context.Canceled)) {
		if !c.laneReleasedForReplay() {
			dir := detachedGateDir(l.cfg.Root, l.cfg.Store, c.Worktree, c.RunID)
			if live, done := hasDetachedGateInDir(dir, *c, true); live || done {
				return laneOutcome{stop: stopLaneRetained, err: errRestartPending}
			}
		}
	}
	held, keep := retainLane(err)
	itemFault := errors.Is(err, errItemFaultHeld)
	repairHeld := errors.Is(err, errRepairHeld)
	if held {
		err = nil
		if !keep {
			cp := *work
			cp.Candidate = c
			reason := ""
			if repairHeld {
				reason = ReasonRepairStagingFailed
			}
			// A candidate mismatch must retain the unexpected checkout.
			if itemFault {
				if head, headErr := l.revParse(ctx, c.Worktree, "HEAD"); headErr != nil || head != c.HeadSHA {
					reason = ReasonCandidateMismatch
				}
			}
			endRelease := timeHandoffPhase(ctx, "release_candidate")
			_, err = l.releaseCandidate(ctx, &cp, reason, true)
			endRelease()
		}
	}
	var pinErr *runPinError
	if (err != nil || stop != "") && !errors.As(err, &pinErr) && !c.laneReleasedForReplay() {
		endPin := timeHandoffPhase(ctx, "pin_candidate_head")
		pinFail := l.pinCandidateHead(ctx, *c)
		endPin()
		if pinFail != nil {
			if err == nil {
				return laneOutcome{err: pinFail}
			}
			// Keep the original error when the pin was a secondary failure.
			l.reportf("loop: %v\n", pinFail)
		}
	}
	if err == nil && stop == "" && !held {
		endCleanup := timeHandoffPhase(ctx, "lane_cleanup")
		if c.laneReleasedForReplay() {
			if landed {
				l.reportRefSweepFailure(l.prunePreservedWork(ctx))
			}
		} else if landed {
			stop = l.abandon(ctx, c.LaneID, "", "")
			l.reportRefSweepFailure(l.prunePreservedWork(ctx))
		} else {
			stop = l.abandon(ctx, c.LaneID, c.RunID, c.HeadSHA)
		}
		if stop == "" && err == nil {
			err = l.markExamined(ctx, work.Decision)
		}
		endCleanup()
	}
	return laneOutcome{stop: stop, err: err}
}

// judgeSettledCandidate continues into gate and land when the standalone gate
// coordinator is not active (for example, direct single-pass l.pass callers).
// It claims directly by per-item CAS without calling planDefinition or checking
// numbered-lane preemption (D-SN4).
func (l *Loop) judgeSettledCandidate(ctx context.Context, d Decision, local *LoopResult, timing *PassTiming) (laneOutcome, string) {
	work, claimed, err := l.claimSettledCandidateDirect(ctx, d, timing)
	if err != nil {
		return laneOutcome{err: err}, ActionJudge
	}
	if !claimed {
		return laneOutcome{}, ActionSettle
	}
	return l.runCandidateExamination(ctx, work, local, timing), ActionJudge
}

func (l *Loop) claimSettledCandidateDirect(ctx context.Context, d Decision, timing *PassTiming) (*pendingWork, bool, error) {
	if l.nonLanding || d.RunRequest == nil || d.LaneID == "" {
		return nil, false, nil
	}
	if stopped, err := l.stopRequested(ctx); err != nil || stopped {
		return nil, false, err
	}
	if l.cfg.Restart != nil && !l.restartActionDeferred() {
		if l.restartReplaced.Load() {
			return nil, false, nil
		}
		if pending, err := l.restarterPendingWith(ctx, nil); err != nil || pending {
			return nil, false, err
		}
	}
	here, err := currentLoopOwner()
	if err != nil {
		return nil, false, err
	}
	ws, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return nil, false, err
	}
	defer ws.Close()
	var claimed *pendingWork
	err = ws.WriteTx(ctx, func(tx *store.Tx) error {
		r := tx.Reader()
		works, err := readPending(r)
		if err != nil {
			return err
		}
		w := works[d.ItemID]
		if w == nil || w.Done || w.Candidate == nil || w.LaneReleased || w.Decision.LaneID != d.LaneID || w.Decision.RunRequest == nil || w.Decision.RunRequest.RunID != d.RunRequest.RunID {
			return nil
		}
		owned, err := examinationOwned(r, w, d, here)
		if err != nil || !owned {
			return err
		}
		available, err := availableRotationLanes(r, []string{d.LaneID})
		if err != nil || len(available) == 0 {
			return err
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

		claimAt := l.cfg.Now().UTC()
		passID := timing.TickID
		if passID == "" {
			passID, err = allocateGeneration(tx)
			if err != nil {
				return err
			}
			timing.TickID = passID
		}
		judgeDecision := w.Decision
		judgeDecision.From = "candidate"
		judgeDecision.To = "landed"
		judgeDecision.Action = ActionJudge
		judgeDecision.Reason = ActionJudge
		judgeDecision.PassID = passID
		req := *judgeDecision.RunRequest
		req.CorrelationID = passID
		judgeDecision.RunRequest = &req
		identifyDecision(&judgeDecision)
		if err := recordPassDecision(tx, judgeDecision, claimAt); err != nil {
			return err
		}
		cp := *w
		cp.Decision = judgeDecision
		claimed = &cp
		return nil
	})
	return claimed, claimed != nil && err == nil, err
}

func examinationOwned(r *store.Reader, w *pendingWork, d Decision, here *LoopOwner) (bool, error) {
	if d.Workflow != nil {
		if !w.Examining || w.Owner == nil || *w.Owner != *here {
			return false, nil
		}
	} else {
		if w.Examiner == nil || *w.Examiner != *here {
			return false, nil
		}
	}
	var n int
	err := r.QueryRow(`SELECT COUNT(*) FROM lanes WHERE lane_id=? AND state=? AND run_id=? AND leased_until IS NULL`, d.LaneID, run.LaneStateLeased, d.RunRequest.RunID).Scan(&n)
	return n == 1, err
}

func availableRotationLanes(r *store.Reader, offered []string) ([]string, error) {
	events, err := r.EventsOfKind(0, laneRefusedKind, laneRestoredKind)
	if err != nil {
		return nil, readFailure(err)
	}
	refused := map[string]bool{}
	for _, e := range events {
		if e.ObjectType != "lane" || e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case laneRefusedKind:
			refused[*e.ObjectID] = true
		case laneRestoredKind:
			delete(refused, *e.ObjectID)
		}
	}
	lanes := make([]string, 0, len(offered))
	for _, lane := range offered {
		if !refused[lane] {
			lanes = append(lanes, lane)
		}
	}
	return lanes, nil
}

type gateClaim struct {
	decision   Decision
	work       *pendingWork
	tickID     string
	claimed    bool
	lockStart  time.Time
	lockEnd    time.Time
	claimStart time.Time
	claimEnd   time.Time
}

func pendingEligibility(work *pendingWork) gateland.Eligibility {
	if work == nil {
		return gateland.Eligibility{}
	}
	var runID string
	if work.Decision.RunRequest != nil {
		runID = work.Decision.RunRequest.RunID
	}
	return gateland.Eligibility{
		Done:                work.Done,
		HasCandidate:        work.Candidate != nil,
		Gated:               work.Gated,
		HasDirtyHold:        work.DirtyHold != nil,
		HasRedCapHold:       work.RedCapHold != nil,
		HasRepairHold:       work.RepairHold != nil,
		LaneReleased:        work.LaneReleased,
		NeedsStage:          work.NeedsStage,
		ReopenedPausedJudge: work.ReopenedPausedJudge,
		HasNoChange:         work.Candidate != nil && work.Candidate.NoChange != nil,
		HasExaminer:         work.Examiner != nil,
		Examining:           work.Examining,
		RunID:               runID,
		LaneID:              work.Decision.LaneID,
	}
}

// pendingWorkStateEligible reports whether an already-dispatched pending work item
// may advance through direct settlement, gating, or landing in itemState.
// Finished items settle as terminal in the planner; non-ready workflow items are
// invalidated by the planner; non-workflow work (including design runs answering a
// blocked item's conflict) advances in any non-finished state.
func pendingWorkStateEligible(d definition.Definition, work *pendingWork, itemState string) bool {
	if itemState == "" || finishedState(d, itemState) {
		return false
	}
	if itemState == d.Ready {
		return true
	}
	return work != nil && work.Decision.Workflow == nil
}

func isCandidateGateEligible(work *pendingWork, runID string) bool {
	return gateland.GateEligible(pendingEligibility(work), runID)
}

func (l *Loop) signalGateWake() {
	l.mu.Lock()
	ch := l.gateLand.Gate.Wake
	l.mu.Unlock()
	gateland.SignalWake(ch)
}

func (l *Loop) isGateCoordinatorActive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gateLand.Gate.Active
}

func (l *Loop) coordinatorOwnsJudge(work *pendingWork) bool {
	if l.isGateCoordinatorActive() && isCandidateGateEligible(work, "") {
		return true
	}
	if l.isLandCoordinatorActive() && isCandidateLandEligible(work, "") {
		return true
	}
	return false
}

// startGateCoordinator runs Step 4 (candidate gating) outside the scheduling
// pass, woken by candidate.ready / gate.finished / resync relay notifications
// and reconciled on StorePollInterval (15s default) when notifications are
// absent or disabled.
func (l *Loop) startGateCoordinator(ctx context.Context, result *LoopResult) func() {
	coordCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	l.mu.Lock()
	l.gateLand.Gate.Ensure()
	l.gateLand.Gate.Active = true
	l.mu.Unlock()

	go func() {
		defer close(done)
		defer func() {
			l.mu.Lock()
			l.gateLand.Gate.Active = false
			l.mu.Unlock()
		}()
		wait := l.cfg.StorePollInterval
		if wait <= 0 {
			wait = DefaultFallbackPollInterval
		}
		gateland.RunLoop(
			coordCtx,
			wait,
			l.gateLand.Gate.Timer,
			func() <-chan struct{} {
				if !l.notifyEnabled() {
					return nil
				}
				l.mu.Lock()
				ch := l.gateLand.Gate.Wake
				l.mu.Unlock()
				return ch
			},
			func() { l.scanAndGate(coordCtx, result) },
		)
	}()

	return func() {
		cancel()
		<-done
	}
}

func (l *Loop) busyRunIDsLocked() map[string]bool {
	busy := make(map[string]bool, len(l.active)+len(l.activeBrackets)+len(l.examiningRuns)+l.gateLand.RunCount())
	for id := range l.active {
		busy[id] = true
	}
	for id := range l.activeBrackets {
		busy[id] = true
	}
	for id := range l.examiningRuns {
		busy[id] = true
	}
	for id := range l.gateLand.Gate.Runs {
		busy[id] = true
	}
	for id := range l.gateLand.Land.Runs {
		busy[id] = true
	}
	return busy
}

func (l *Loop) scanAndGate(ctx context.Context, result *LoopResult) {
	if ctx.Err() != nil || l.nonLanding || l.ownerMigrationItem != "" {
		return
	}
	if stopped, err := l.stopRequested(ctx); stopped || err != nil {
		return
	}
	candidates, err := l.findGateableCandidates(ctx)
	if err != nil || len(candidates) == 0 {
		return
	}
	for _, c := range candidates {
		l.mu.Lock()
		if ctx.Err() != nil {
			l.mu.Unlock()
			return
		}
		if l.active[c.RunID] || l.activeBrackets[c.RunID] || l.examiningRuns[c.RunID] || l.gateLand.HasRun(c.RunID) {
			l.mu.Unlock()
			continue
		}
		l.gateLand.Gate.MarkStart(c.ItemID, c.RunID)
		l.examinations.Add(1)
		l.mu.Unlock()

		go l.runCoordinatedGate(ctx, c.ItemID, c.RunID, result)
	}
}

func (l *Loop) findGateableCandidates(ctx context.Context) ([]gateland.GateCandidate, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	var found []gateland.GateCandidate
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		faults, err := ReadItemFaults(r)
		if err != nil {
			return err
		}
		declared, err := definition.ForReader(r)
		if err != nil {
			return readFailure(err)
		}
		states, err := gateland.ReadItemStates(r)
		if err != nil {
			return err
		}

		l.mu.Lock()
		busy := l.busyRunIDsLocked()
		l.mu.Unlock()

		gates := l.gateCount()
		for itemID, work := range works {
			if !isCandidateGateEligible(work, "") {
				continue
			}
			runID := work.Decision.RunRequest.RunID
			if busy[runID] {
				continue
			}
			if _, faulted := faults[itemID]; faulted {
				continue
			}
			if !pendingWorkStateEligible(declared.Parsed, work, states[itemID]) {
				continue
			}
			if work.GateStall != nil && !work.GateStall.canRetry(gates) {
				continue
			}
			found = append(found, gateland.GateCandidate{ItemID: itemID, RunID: runID})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	gateland.SortGateCandidates(found)
	return found, nil
}

// claimCandidateGate claims exclusive gate ownership of (itemID, runID) by a
// direct per-item compare-and-set in one WriteTx, without calling planDefinition
// or checking numbered-lane preemption (D-SN3, D-SN4).
func (l *Loop) claimCandidateGate(ctx context.Context, itemID, runID string, at time.Time) (gateClaim, error) {
	var out gateClaim
	if l.nonLanding || itemID == "" || runID == "" {
		return out, nil
	}
	if stopped, err := l.stopRequested(ctx); stopped || err != nil {
		return out, err
	}
	if l.cfg.Restart != nil && !l.restartActionDeferred() {
		if l.restartReplaced.Load() {
			return out, nil
		}
		if pending, err := l.restarterPendingWith(ctx, nil); err != nil || pending {
			return out, err
		}
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

	gates := l.gateCount()
	out.claimStart = time.Now()
	err = s.WriteTx(claimCtx, func(tx *store.Tx) error {
		r := tx.Reader()
		works, _, err := readPendingHistory(r)
		if err != nil {
			return err
		}
		work := works[itemID]
		if !isCandidateGateEligible(work, runID) {
			return nil
		}
		if work.GateStall != nil && !work.GateStall.canRetry(gates) {
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

		d.From = "candidate"
		d.To = "landed"
		d.Action = ActionJudge
		d.Reason = ActionJudge
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
		copyWork.Decision = d
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

func (l *Loop) runCoordinatedGate(ctx context.Context, itemID, runID string, _ *LoopResult) {
	defer l.examinations.Done()
	defer func() {
		l.mu.Lock()
		l.gateLand.Gate.MarkDone(itemID, runID)
		l.mu.Unlock()
		if l.notifyEnabled() && l.isLandCoordinatorActive() {
			l.signalLandWake()
		}
	}()

	handoffStart := time.Now()
	at := l.cfg.Now().UTC()
	claim, err := l.claimCandidateGate(ctx, itemID, runID, at)
	if err != nil {
		if errors.Is(err, errRestartPending) || ctx.Err() != nil {
			return
		}
		l.sendCompletion(ctx, runCompletion{
			id:                  runID,
			itemID:              itemID,
			step:                ActionJudge,
			outcome:             laneOutcome{err: err},
			fromGateCoordinator: true,
		})
		return
	}
	if !claim.claimed {
		return
	}

	timing := &PassTiming{TickID: claim.tickID, AtMS: at.UnixMilli(), GateBudgetMS: l.gateBudget.Milliseconds()}
	preSpans := []handoffSpan{
		newHandoffSpan("restart_lock_wait", claim.lockStart, claim.lockEnd),
		newHandoffSpan("claim_candidate_gate", claim.claimStart, claim.claimEnd),
	}
	comp := l.executeGateCandidate(withPreHandoffSpans(ctx, handoffStart, preSpans), claim.decision, claim.work, timing)
	timing.Applied = 1
	timing.DecisionCounts = map[string]int{ActionJudge: 1}
	timing.EndMS = l.cfg.Now().UnixMilli()
	comp.fromGateCoordinator = true
	l.sendCompletion(ctx, comp)
}

func (l *Loop) executeGateCandidate(ctx context.Context, d Decision, work *pendingWork, timing *PassTiming) runCompletion {
	local := &LoopResult{}
	completion := runCompletion{id: d.RunRequest.RunID, itemID: d.ItemID, step: ActionJudge, result: local, timing: timing}
	initialForkCheckout := l.laneForkTarget(d.LaneID)
	initialForkRef := l.lanePatchRef(d.LaneID)
	ctx, recordTiming := l.timeHandoff(ctx, work.Decision, "gate")
	defer recordTiming()

	endProc := timeHandoffPhase(ctx, "check_surviving_processes")
	survivingOut, survivingHeld := l.checkSurvivingRunProcesses(ctx, d.ItemID, d.LaneID, examinationRunIDs(work)...)
	endProc()
	if survivingHeld {
		completion.outcome = survivingOut
		completion.laneRefusal = survivingOut.laneRefusal
	} else {
		completion.outcome = l.runCandidateGateOnly(ctx, work, local, timing)
	}
	completion.outcome = retainedOutcome(completion.outcome)
	if completion.outcome.stop == "" && completion.outcome.err != nil {
		endHold := timeHandoffPhase(ctx, "hold_examination_failure")
		completion.outcome.err = l.holdExaminationFailure(ctx, d, work, completion.step, completion.outcome.err)
		endHold()
		completion.outcome = retainedOutcome(completion.outcome)
	}
	if !l.nonLanding && !l.isRestartReplaced() && d.LaneID != "" {
		checkout := l.laneForkTarget(d.LaneID)
		ref := l.lanePatchRef(d.LaneID)
		if checkout == "" {
			checkout, ref = initialForkCheckout, initialForkRef
		}
		if checkout != "" && ref != "" {
			l.reportForkCheckoutLeftover(context.WithoutCancel(ctx), d, checkout, ref, l.cfg.Now().UTC())
		}
	}
	if completion.outcome.stop != stopLaneRetained && !l.isRestartReplaced() {
		endRel := timeHandoffPhase(ctx, "release_examination")
		if err := l.releaseExamination(context.WithoutCancel(ctx), d); err != nil && completion.outcome.err == nil {
			completion.outcome.err = err
		}
		endRel()
	}
	completion.detail = local.Detail
	return completion
}

// runCandidateGateOnly executes Step 4 (gate) for a claimed candidate. When the
// gate passes and records candidateGatedKind, it hands off to Step 5 (land)
// without abandoning the lane or marking the work examined.
func (l *Loop) runCandidateGateOnly(ctx context.Context, work *pendingWork, local *LoopResult, timing *PassTiming) laneOutcome {
	c := *work.Candidate
	c.TickID = timing.TickID
	c.replayRelease = &replayLaneRelease{
		release: func(rctx context.Context, cur Candidate) (bool, error) {
			if IsTrunkFixer(cur.ItemID) {
				return false, nil
			}
			cp := *work
			cp.Candidate = &cur
			released, err := l.releaseCandidateForReplay(rctx, &cp)
			if released {
				work.LaneReleased = true
			}
			return released, err
		},
	}
	if IsTrunkFixer(c.ItemID) {
		if resolved, err := l.resolveKeptProvedFixer(ctx, c.ItemID); err != nil {
			return laneOutcome{err: err}
		} else if resolved {
			stop := l.abandon(ctx, c.LaneID, c.RunID, c.HeadSHA, "resolved elsewhere: rerun passed")
			if stop == "" {
				err = l.markExamined(ctx, work.Decision)
			}
			return laneOutcome{stop: stop, err: err}
		}
	}

	stop, gated, alreadyLanded, err := l.verifyOnly(ctx, &c, local, timing)
	if err == nil && stop == "" && c.trunkAcquitted {
		err = errTrunkAcquittedHeld
	}
	if err == nil && stop == "" && gated {
		// Gate passed and candidateGatedKind is committed (publishing gate.passed).
		// Leave the candidate on its worktree for Step 5 (land coordinator).
		return laneOutcome{}
	}
	return l.finishCandidateOutcome(ctx, work, &c, stop, alreadyLanded, err)
}
