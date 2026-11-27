package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/gateland"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

// A handed-off judge may outlive the supervisor and mutate its checkout before
// terminating. Recover that run before generic worker-head or retirement guards.
// A completed checked receipt instead follows the ordinary prepared-head guards.
func (l *Loop) recoverJudgeHandoff(ctx context.Context, c Candidate, result *LoopResult) (Candidate, bool, error) {
	raw, err := os.ReadFile(filepath.Join(RunDir(l.cfg.Root, c.RunID), judgeReceiptFile))
	if os.IsNotExist(err) {
		return c, true, nil
	}
	if err != nil {
		return c, false, err
	}
	var receipt JudgeReceipt
	if err := json.Unmarshal(raw, &receipt); err != nil {
		return c, false, err
	}
	if len(receipt.Raw) != 0 {
		return c, true, nil
	}
	return l.judgeCandidate(ctx, c, result)
}

// checkJudgeForLanding also covers gated work resumed across the migration.
// Adopt legacy receipts in place, validating their saved verdict bytes without
// rerunning the evaluator or the gate.
func (l *Loop) checkJudgeForLanding(ctx context.Context, c Candidate) (*JudgeReceipt, error) {
	receipt := *c.Judge
	head := c.HeadSHA
	if c.WorkerHeadSHA != "" {
		head = c.WorkerHeadSHA
	}
	tree, err := l.revParse(ctx, c.Worktree, head+"^{tree}")
	if err != nil {
		return nil, err
	}
	if receipt.Options.ItemID != c.ItemID || receipt.Options.Head != head || receipt.Options.Tree != tree {
		return nil, item.Rejectionf("evaluator-candidate", c.ItemID, "saved judgment belongs to a different candidate")
	}
	err = l.readStore(ctx, func(r *store.Reader) error {
		if receipt.Options.EvaluatorBinding == (item.EvaluatorBinding{}) {
			if err := bindSavedJudge(r, c, c.Judge, &receipt.Options); err != nil {
				return err
			}
			checked, err := item.CheckJudge(ctx, r, receipt.Raw, receipt.Options)
			if err != nil {
				return err
			}
			if checked.Outcome != "pass" {
				return item.Rejectionf("evaluator-outcome", c.ItemID, "saved judgment did not pass")
			}
			return nil
		}
		binding, err := item.ReadEvaluatorBinding(r, c.ItemID)
		if err == nil && binding != receipt.Options.EvaluatorBinding {
			return item.Rejectionf("evaluator-binding", c.ItemID, "saved judgment does not match workflow %q evaluator %q", binding.Workflow, binding.EvaluatorNode)
		}
		return err
	})
	return &receipt, err
}

// The successful landing's exact commit is retained before retirement intake.
// Recovery settles that publication only; it never judges, gates or lands again.
func (l *Loop) recoverJudgePublication(ctx context.Context, c Candidate, result *LoopResult) (bool, error) {
	db, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	var raw []byte
	err = db.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind=? AND object_type='run' AND object_id=? ORDER BY seq DESC LIMIT 1`, judgeLandedKind, c.RunID)
		if err != nil {
			return err
		}
		defer rows.Close()
		if rows.Next() {
			return rows.Scan(&raw)
		}
		return rows.Err()
	})
	db.Close()
	if err != nil || raw == nil {
		return false, err
	}
	var published Candidate
	if err := json.Unmarshal(raw, &published); err != nil {
		return true, err
	}
	if published.ItemID != c.ItemID || published.Judge == nil || published.Judge.Options.Landed == "" {
		return true, Internalf("invalid saved judge publication for %s", c.RunID)
	}
	if err := l.recordLandedJudge(ctx, published); err != nil {
		return true, err
	}
	l.mu.Lock()
	result.Landed++
	l.mu.Unlock()
	if err := l.bumpLandings(ctx); err != nil {
		l.reportf("loop: settled landed %s but could not record the landing count: %v\n", c.ItemID, err)
	}
	return true, nil
}

type landClaim struct {
	decision   Decision
	work       *pendingWork
	tickID     string
	claimed    bool
	lockStart  time.Time
	lockEnd    time.Time
	claimStart time.Time
	claimEnd   time.Time
}

func isCandidateLandEligible(work *pendingWork, runID string) bool {
	return gateland.LandEligible(pendingEligibility(work), runID)
}

func (l *Loop) signalLandWake() {
	l.mu.Lock()
	ch := l.gateLand.Land.Wake
	l.mu.Unlock()
	gateland.SignalWake(ch)
}

func (l *Loop) isLandCoordinatorActive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.gateLand.Land.Active
}

// startLandCoordinator runs Step 5 (FIFO landing via tillr-land) outside the
// scheduling pass, woken by gate.passed / trunk.moved / proof.green / resync
// relay notifications and reconciled on StorePollInterval (15s default) when
// notifications are absent or disabled.
func (l *Loop) startLandCoordinator(ctx context.Context, result *LoopResult) func() {
	coordCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	l.mu.Lock()
	l.gateLand.Land.Ensure()
	l.gateLand.Land.Active = true
	l.mu.Unlock()

	go func() {
		defer close(done)
		defer func() {
			l.mu.Lock()
			l.gateLand.Land.Active = false
			l.mu.Unlock()
		}()
		wait := l.cfg.StorePollInterval
		if wait <= 0 {
			wait = DefaultFallbackPollInterval
		}
		gateland.RunLoop(
			coordCtx,
			wait,
			l.gateLand.Land.Timer,
			func() <-chan struct{} {
				if !l.notifyEnabled() {
					return nil
				}
				l.mu.Lock()
				ch := l.gateLand.Land.Wake
				l.mu.Unlock()
				return ch
			},
			func() { l.scanAndLand(coordCtx, result) },
		)
	}()

	return func() {
		cancel()
		<-done
	}
}

// scanAndLand drains all currently landable candidates serially in FIFO order
// on the single landing coordinator goroutine (Section 5.6).
func (l *Loop) scanAndLand(ctx context.Context, result *LoopResult) {
	if ctx.Err() != nil || l.nonLanding || l.ownerMigrationItem != "" {
		return
	}
	if stopped, err := l.stopRequested(ctx); stopped || err != nil {
		return
	}
	for ctx.Err() == nil {
		candidates, err := l.findLandableCandidates(ctx)
		if err != nil || len(candidates) == 0 {
			return
		}
		progressed := false
		for _, c := range candidates {
			if ctx.Err() != nil {
				return
			}
			if stopped, err := l.stopRequested(ctx); stopped || err != nil {
				return
			}
			l.mu.Lock()
			if ctx.Err() != nil {
				l.mu.Unlock()
				return
			}
			if l.active[c.RunID] || l.activeBrackets[c.RunID] || l.examiningRuns[c.RunID] || l.gateLand.HasRun(c.RunID) {
				l.mu.Unlock()
				continue
			}
			l.gateLand.Land.MarkStart(c.ItemID, c.RunID)
			l.examinations.Add(1)
			l.mu.Unlock()

			if l.runCoordinatedLand(ctx, c.ItemID, c.RunID, result) {
				progressed = true
			}
		}
		if !progressed {
			return
		}
	}
}

func (l *Loop) findLandableCandidates(ctx context.Context) ([]gateland.LandCandidate, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	at := l.cfg.Now().UTC()
	var found []gateland.LandCandidate
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
		gatedSeqs, err := gateland.ReadGatedSeqs(r, candidateGatedKind)
		if err != nil {
			return err
		}

		l.mu.Lock()
		busy := l.busyRunIDsLocked()
		l.mu.Unlock()

		for itemID, work := range works {
			if !isCandidateLandEligible(work, "") {
				continue
			}
			runID := work.Decision.RunRequest.RunID
			if busy[runID] {
				continue
			}
			if _, faulted := faults[itemID]; faulted {
				continue
			}
			if states[itemID] != declared.Parsed.Ready {
				continue
			}
			if work.RedCapHold != nil {
				cap, err := work.RedCapHold.current(ctx, r, at)
				if err != nil {
					return err
				}
				if cap != nil {
					continue
				}
			}
			if work.DirtyHold != nil {
				paths, err := work.DirtyHold.paths(ctx)
				if err != nil {
					return err
				}
				if len(paths) > 0 {
					continue
				}
			}
			found = append(found, gateland.LandCandidate{
				ItemID:   itemID,
				RunID:    runID,
				GatedSeq: gatedSeqs[runID],
			})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	gateland.SortLandCandidates(found)
	return found, nil
}

// claimCandidateLand claims exclusive landing ownership of (itemID, runID) by a
// direct per-item compare-and-set in one WriteTx, without calling planDefinition.
func (l *Loop) claimCandidateLand(ctx context.Context, itemID, runID string, at time.Time) (landClaim, error) {
	var out landClaim
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

	out.claimStart = time.Now()
	err = s.WriteTx(claimCtx, func(tx *store.Tx) error {
		r := tx.Reader()
		works, _, err := readPendingHistory(r)
		if err != nil {
			return err
		}
		work := works[itemID]
		if !isCandidateLandEligible(work, runID) {
			return nil
		}
		if work.RedCapHold != nil {
			cap, err := work.RedCapHold.current(claimCtx, r, at)
			if err != nil || cap != nil {
				return err
			}
		}
		if work.DirtyHold != nil {
			paths, err := work.DirtyHold.paths(claimCtx)
			if err != nil || len(paths) > 0 {
				return err
			}
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
		if itemState != declared.Parsed.Ready {
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

func (l *Loop) runCoordinatedLand(ctx context.Context, itemID, runID string, _ *LoopResult) bool {
	defer l.examinations.Done()
	defer func() {
		l.mu.Lock()
		l.gateLand.Land.MarkDone(itemID, runID)
		l.mu.Unlock()
	}()

	handoffStart := time.Now()
	at := l.cfg.Now().UTC()
	claim, err := l.claimCandidateLand(ctx, itemID, runID, at)
	if err != nil {
		if errors.Is(err, errRestartPending) || ctx.Err() != nil {
			return false
		}
		l.sendCompletion(ctx, runCompletion{
			id:                  runID,
			itemID:              itemID,
			step:                ActionJudge,
			outcome:             laneOutcome{err: err},
			fromLandCoordinator: true,
		})
		return false
	}
	if !claim.claimed {
		return false
	}

	timing := &PassTiming{TickID: claim.tickID, AtMS: at.UnixMilli(), GateBudgetMS: l.gateBudget.Milliseconds()}
	preSpans := []handoffSpan{
		newHandoffSpan("restart_lock_wait", claim.lockStart, claim.lockEnd),
		newHandoffSpan("claim_candidate_land", claim.claimStart, claim.claimEnd),
	}
	comp := l.executeLandCandidate(withPreHandoffSpans(ctx, handoffStart, preSpans), claim.decision, claim.work, timing)
	timing.Applied = 1
	timing.DecisionCounts = map[string]int{ActionJudge: 1}
	timing.EndMS = l.cfg.Now().UnixMilli()
	comp.fromLandCoordinator = true
	l.sendCompletion(ctx, comp)
	return true
}

func (l *Loop) executeLandCandidate(ctx context.Context, d Decision, work *pendingWork, timing *PassTiming) runCompletion {
	local := &LoopResult{}
	completion := runCompletion{id: d.RunRequest.RunID, itemID: d.ItemID, step: ActionJudge, result: local, timing: timing}
	initialForkCheckout := l.laneForkTarget(d.LaneID)
	initialForkRef := l.lanePatchRef(d.LaneID)
	ctx, recordTiming := l.timeHandoff(ctx, work.Decision, "land")
	defer recordTiming()

	completion.outcome = l.runCandidateLandOnly(ctx, work, local, timing)
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

// runCandidateLandOnly executes Step 5 (tillr-land) for a claimed gated candidate.
func (l *Loop) runCandidateLandOnly(ctx context.Context, work *pendingWork, local *LoopResult, timing *PassTiming) laneOutcome {
	c := *work.Candidate
	c.TickID = timing.TickID
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
	stop, landed, err := l.landCandidate(ctx, c, local, timing)
	return l.finishCandidateOutcome(ctx, work, &c, stop, landed, err)
}
