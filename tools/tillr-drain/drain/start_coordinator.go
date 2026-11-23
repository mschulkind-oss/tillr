package drain

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/store"
)

func (l *Loop) startBackgroundCoordinator(ctx context.Context, view *startView, result *LoopResult) func() {
	ctx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	var staging sync.WaitGroup
	go func() {
		defer close(done)
		l.runFastStart(ctx, view, result, &staging)
	}()
	return func() {
		cancel()
		<-done // No further staging admissions can race Wait.
		staging.Wait()
	}
}

func (l *Loop) runFastStart(ctx context.Context, view *startView, result *LoopResult, staging *sync.WaitGroup) {
	// Retry a rejected proposal only after a new publication or a safety wake.
	var rejected *startSnapshot
	timer := time.NewTicker(startViewRefreshInterval)
	defer timer.Stop()
	for ctx.Err() == nil {
		snapshot := view.current.Load()
		l.mu.Lock()
		waitingForPass := len(l.stageFaults) > 0 || l.stageBackoff
		l.mu.Unlock()
		if snapshot == nil || snapshot == rejected || waitingForPass {
			select {
			case <-ctx.Done():
			case <-view.updated:
			case <-timer.C:
				rejected = nil
			}
			continue
		}
		e, ok := snapshot.nextFreshStartWithGates(l.gateCount())
		if !ok {
			rejected = snapshot
			continue
		}
		at := l.cfg.Now().UTC()
		d := e.decision
		if d.DeskDocument != "" && item.IsCommentItem(d.ItemID) {
			p := plannedApplication{decision: d, version: e.version, definition: &snapshot.def, definitionSHA: snapshot.definitionSHA}
			if p.checksComments() {
				ready, closed, err := l.checkCommentDispatch(ctx, p, at)
				if err != nil {
					l.reportStartError(ctx, d, err)
					return
				}
				if closed || !ready {
					if !closed {
						view.noteVantageUnavailable(snapshot, d.ItemID)
					}
					rejected = snapshot
					continue
				}
			}
		}
		mechCtx, unlock, err := l.lockRestartMechanical(ctx)
		if errors.Is(err, errRestartPending) {
			// A pending request may be refused or become deferred. Do not
			// shut down starts until the loop itself ends.
			rejected = snapshot
			continue
		}
		if err != nil {
			l.reportStartError(ctx, d, err)
			return
		}
		if err := l.checkStopRequested(mechCtx); err != nil {
			unlock()
			l.reportStartError(ctx, d, err)
			return
		}
		applied, work, generation, d, err := l.claimFastStart(mechCtx, view, *snapshot, e, at)
		unlock()
		if err != nil {
			l.reportStartError(ctx, e.decision, err)
			return
		}
		if !applied {
			rejected = snapshot
			continue
		}
		startCtx, startHandoff := l.beginStartHandoff(ctx, d)
		endNote := timeHandoffPhase(startCtx, "note_start_claimed")
		claimedAt := l.cfg.Now().UTC()
		ready := snapshot.readyAt[d.ItemID]
		if free := snapshot.laneFreeAt[d.LaneID]; free.After(ready) {
			ready = free
		}
		if ready.IsZero() {
			ready = at
		}
		l.mu.Lock()
		l.markStepActive(d.ItemID, d.RunRequest.RunID)
		l.mu.Unlock()
		view.noteStartClaimed(d, work)
		l.countGeneration(result, generation)
		endNote()
		timing := PassTiming{TickID: generation, AtMS: at.UnixMilli(), EndMS: claimedAt.UnixMilli(),
			ClaimMS: max(0, claimedAt.Sub(ready).Milliseconds()), Claims: 1, Applied: 1,
			GateBudgetMS: l.gateBudget.Milliseconds(), DecisionCounts: map[string]int{d.Action: 1}}
		// Record the observation before staging can add its duration or send a completion.
		endPassTiming := timeHandoffPhase(startCtx, "record_pass_timing")
		passErr := recordPassTiming(context.WithoutCancel(startCtx), l.cfg.Store, timing)
		endPassTiming()
		if passErr != nil {
			retractErr := l.cancelStaging(context.WithoutCancel(ctx), d)
			l.reportStartError(ctx, d, errors.Join(passErr, retractErr))
			return
		}
		staging.Go(func() {
			if err := l.stageAndLaunchClaimed(withStartHandoff(ctx, startHandoff), d, work.BaseSHA, at, result); err != nil {
				l.reportStartError(ctx, d, err)
			}
		})
	}
}

func (l *Loop) reportStartError(ctx context.Context, d Decision, err error) {
	if errors.Is(err, errRestartPending) || stoppedByOperator(ctx, err) {
		return
	}
	// The pass owns loop failure reporting, including when it is waiting idle.
	select {
	case l.completions <- runCompletion{itemID: d.ItemID, step: "start", outcome: laneOutcome{err: fmt.Errorf("start %s: %w", d.ItemID, err)}}:
	case <-ctx.Done():
	}
}

// Pending-only planning leaves fresh holds in the view. Keep the existing
// durable actions (in particular, parking a request for an owner decision).
func startHoldWrites(view *startView, at time.Time) ([]func(*store.Tx) error, error) {
	if view == nil {
		return nil, nil
	}
	s := view.snapshot()
	var writes []func(*store.Tx) error
	for _, q := range s.items {
		id, e := q.id, s.entries[q.id]
		if work := s.pending.works[id]; work != nil && !work.Done {
			continue
		}
		d := e.decision
		if d.Reason == ReasonNoPolicy {
			return nil, Usagef("the loop's policy cannot build a request for item %s (seat %s)", id, d.Seat)
		}
		if d.Action == ActionHold && e.version != "" {
			p := plannedApplication{decision: d, version: e.version, definitionSHA: s.definitionSHA}
			if write := holdWriter(p, at); write != nil {
				writes = append(writes, write)
			}
		}
	}
	return writes, nil
}

// The idle pass checks for a publication that covers the store's event cursor.
// An unseeded or stale picture cannot establish a queue-drained fixed point.
func (l *Loop) startIdle(ctx context.Context, view *startView, holds []Decision) (bool, []Decision, error) {
	if view == nil {
		return true, holds, nil // Standalone pending-only pass, without a coordinator.
	}
	s := view.current.Load()
	if s == nil {
		return false, holds, nil
	}
	if _, proposed := s.nextFreshStartWithGates(l.gateCount()); proposed {
		return false, holds, nil
	}
	r, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, holds, err
	}
	defer r.Close()
	var head int64
	if err := r.ReadTx(ctx, func(r *store.Reader) error {
		var err error
		head, err = r.EventHeadSeq()
		return err
	}); err != nil {
		return false, holds, err
	}
	if head != s.lastEventSeq || view.current.Load() != s || l.activeCount() > 0 {
		return false, holds, nil
	}
	for _, q := range s.items {
		if work := s.pending.works[q.id]; work != nil && !work.Done {
			continue // The pass has the current holds for unfinished attempts.
		}
		d := s.entries[q.id].decision
		if isDispatch(d) {
			// A ready entry without an eligible lane is deferred, not absent.
			d.Action, d.Reason = ActionHold, ReasonNoLane
		}
		if d.Action == ActionHold {
			holds = append(holds, d)
		}
	}
	return true, holds, nil
}
