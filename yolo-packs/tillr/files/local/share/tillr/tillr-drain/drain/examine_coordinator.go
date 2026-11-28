// Scheduling slice 3: outside-pass worker examination coordinator.
// Design: docs/design/tillr-scheduling-and-notifications.md (Section 5.4, D-SN3, D-SN4).
package drain

import (
	"context"
	"database/sql"
	"errors"
	"sort"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

type examinableRun struct {
	itemID string
	runID  string
}

func (l *Loop) ensureExamineStateLocked() {
	if l.examineWake == nil {
		l.examineWake = make(chan struct{}, 1)
	}
	if l.activeBrackets == nil {
		l.activeBrackets = map[string]bool{}
	}
	if l.examiningRuns == nil {
		l.examiningRuns = map[string]bool{}
	}
	if l.examiningItems == nil {
		l.examiningItems = map[string]int{}
	}
}

func (l *Loop) signalExamineWake() {
	l.mu.Lock()
	ch := l.examineWake
	l.mu.Unlock()
	if ch != nil {
		signalWake(ch)
	}
}

func (l *Loop) isExamineCoordinatorActive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.examineCoordinatorActive
}

func (l *Loop) markBracketActive(runID string) {
	if runID == "" {
		return
	}
	l.mu.Lock()
	l.ensureExamineStateLocked()
	l.activeBrackets[runID] = true
	l.mu.Unlock()
}

func (l *Loop) clearBracketActive(runID string) {
	if runID == "" {
		return
	}
	l.mu.Lock()
	delete(l.activeBrackets, runID)
	l.mu.Unlock()
	if l.notifyEnabled() {
		l.signalExamineWake()
	}
}

// startExamineCoordinator runs Step 3 (worker examination) outside the scheduling
// pass, woken by run.exited / resync relay notifications and reconciled on
// StorePollInterval (15s default) when notifications are absent or disabled.
func (l *Loop) startExamineCoordinator(ctx context.Context, result *LoopResult) func() {
	coordCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	l.mu.Lock()
	l.ensureExamineStateLocked()
	l.examineCoordinatorActive = true
	l.mu.Unlock()

	go func() {
		defer close(done)
		defer func() {
			l.mu.Lock()
			l.examineCoordinatorActive = false
			l.mu.Unlock()
		}()
		l.runExamineCoordinator(coordCtx, result)
	}()

	return func() {
		cancel()
		<-done
	}
}

func (l *Loop) runExamineCoordinator(ctx context.Context, result *LoopResult) {
	l.scanAndExamine(ctx, result)

	wait := l.cfg.StorePollInterval
	if wait <= 0 {
		wait = DefaultFallbackPollInterval
	}

	for ctx.Err() == nil {
		var timerC <-chan time.Time
		var stopTimer func()
		if l.examineTimer != nil {
			timerC, stopTimer = l.examineTimer(wait)
		} else {
			timer := time.NewTimer(wait)
			timerC, stopTimer = timer.C, func() { timer.Stop() }
		}

		var wake <-chan struct{}
		if l.notifyEnabled() {
			l.mu.Lock()
			wake = l.examineWake
			l.mu.Unlock()
		}

		select {
		case <-ctx.Done():
			stopTimer()
			return
		case <-wake:
			stopTimer()
			l.scanAndExamine(ctx, result)
		case <-timerC:
			stopTimer()
			l.scanAndExamine(ctx, result)
		}
	}
}

func (l *Loop) scanAndExamine(ctx context.Context, result *LoopResult) {
	if ctx.Err() != nil || l.ownerMigrationItem != "" {
		return
	}
	if stopped, err := l.stopRequested(ctx); stopped || err != nil {
		return
	}
	candidates, err := l.findExaminableRuns(ctx)
	if err != nil || len(candidates) == 0 {
		return
	}
	for _, c := range candidates {
		l.mu.Lock()
		if ctx.Err() != nil {
			l.mu.Unlock()
			return
		}
		l.ensureExamineStateLocked()
		if l.activeBrackets[c.runID] || l.examiningRuns[c.runID] {
			l.mu.Unlock()
			continue
		}
		l.examiningRuns[c.runID] = true
		l.examiningItems[c.itemID]++
		l.examinations.Add(1)
		l.mu.Unlock()

		go l.runCoordinatedExamination(ctx, c.itemID, c.runID, result)
	}
}

func (l *Loop) findExaminableRuns(ctx context.Context) ([]examinableRun, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	at := l.cfg.Now().UTC()
	var found []examinableRun
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		works, byRun, err := readPendingHistory(r)
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
		l.mu.Lock()
		brackets := make(map[string]bool, len(l.activeBrackets))
		for id := range l.activeBrackets {
			brackets[id] = true
		}
		examining := make(map[string]bool, len(l.examiningRuns))
		for id := range l.examiningRuns {
			examining[id] = true
		}
		l.mu.Unlock()

		for itemID, work := range works {
			if _, faulted := faults[itemID]; faulted {
				continue
			}
			if !isWorkSettleEligible(work, "") {
				continue
			}
			runID := work.Decision.RunRequest.RunID
			if brackets[runID] || examining[runID] {
				continue
			}
			var itemState string
			if err := r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, itemID).Scan(&itemState); err != nil {
				if err == sql.ErrNoRows {
					continue
				}
				return readFailure(err)
			}
			if !pendingWorkStateEligible(declared.Parsed, work, itemState) {
				continue
			}
			if len(work.resumes) != 0 {
				liveFound := false
				for _, node := range parallelNodes(work.Decision) {
					if node.RunRequest == nil {
						continue
					}
					live, err := liveContinuation(r, work.resumes[node.RunRequest.RunID], node, at)
					if err != nil {
						return err
					}
					if live {
						liveFound = true
						break
					}
				}
				if liveFound {
					continue
				}
			}
			paused, err := pausedPendingWork(r, work)
			if err != nil {
				return err
			}
			if paused {
				continue
			}
			var finished bool
			var rec *run.Record
			if len(work.Decision.Parallel) > 0 {
				finished, err = parallelTerminal(r, work.Decision)
				if err != nil {
					return err
				}
			} else {
				rec, err = terminalRecord(r, runID)
				if err != nil {
					return err
				}
				finished = rec != nil && rec.State != run.StatePaused
			}
			if !finished {
				continue
			}
			if held, err := l.settlementHeldByBackendOrWait(r, work.Decision, rec, byRun, at); err != nil {
				return err
			} else if held {
				continue
			}
			found = append(found, examinableRun{itemID: itemID, runID: runID})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].itemID != found[j].itemID {
			return found[i].itemID < found[j].itemID
		}
		return found[i].runID < found[j].runID
	})
	return found, nil
}

func (l *Loop) runCoordinatedExamination(ctx context.Context, itemID, runID string, _ *LoopResult) {
	defer l.examinations.Done()
	defer func() {
		l.mu.Lock()
		delete(l.examiningRuns, runID)
		if n := l.examiningItems[itemID]; n <= 1 {
			delete(l.examiningItems, itemID)
		} else {
			l.examiningItems[itemID] = n - 1
		}
		l.mu.Unlock()
		if l.notifyEnabled() && l.isGateCoordinatorActive() {
			l.signalGateWake()
		}
	}()

	handoffStart := time.Now()
	at := l.cfg.Now().UTC()
	claim, err := l.claimWorkerSettlement(ctx, itemID, runID, at)
	if err != nil {
		if errors.Is(err, errRestartPending) || ctx.Err() != nil {
			return
		}
		l.sendCompletion(ctx, runCompletion{
			id:                     runID,
			itemID:                 itemID,
			step:                   ActionSettle,
			outcome:                laneOutcome{err: err},
			fromExamineCoordinator: true,
		})
		return
	}
	if !claim.claimed {
		return
	}

	timing := &PassTiming{TickID: claim.tickID, AtMS: at.UnixMilli(), GateBudgetMS: l.gateBudget.Milliseconds()}
	preSpans := []handoffSpan{
		newHandoffSpan("restart_lock_wait", claim.lockStart, claim.lockEnd),
		newHandoffSpan("claim_worker_settlement", claim.claimStart, claim.claimEnd),
	}
	examined := l.examineClaimed(withPreHandoffSpans(ctx, handoffStart, preSpans), claim.decision, claim.work, at, timing, nil)
	timing.Applied = 1
	timing.DecisionCounts = map[string]int{ActionSettle: 1}
	timing.EndMS = l.cfg.Now().UnixMilli()
	examined.fromExamineCoordinator = true
	l.sendCompletion(ctx, examined)
}
