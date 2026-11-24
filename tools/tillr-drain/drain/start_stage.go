package drain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// stageAndLaunchClaimed consumes an already recorded claim at its decided base.
// It runs separately from selection so a slow stage does not hold up other lanes.
func (l *Loop) stageAndLaunchClaimed(ctx context.Context, d Decision, base string, at time.Time, result *LoopResult) (err error) {
	id := d.RunRequest.RunID
	l.mu.Lock()
	l.markStepActive(d.ItemID, id)
	l.mu.Unlock()
	end := l.beginLaneStaging(d.LaneID)
	defer end()

	launched, refused := false, false
	start := time.Now()
	defer func() {
		stageMS := time.Since(start).Milliseconds()
		if !launched {
			if !refused {
				l.restartMechanical.RLock()
				replaced := l.isRestartReplaced()
				var retractErr error
				if !replaced {
					retractErr = l.cancelStaging(context.WithoutCancel(ctx), d)
				}
				l.restartMechanical.RUnlock()
				if retractErr != nil {
					err = errors.Join(err, fmt.Errorf("cancel staging: %w", retractErr))
				}
			}
			l.mu.Lock()
			delete(l.active, id)
			if l.activeItems[d.ItemID] == id {
				delete(l.activeItems, d.ItemID)
			}
			l.mu.Unlock()
		}
		if d.PassID != "" && !l.isRestartReplaced() {
			timing := &PassTiming{TickID: d.PassID, StageMS: stageMS}
			if timingErr := recordAccumulatedPassTiming(context.WithoutCancel(ctx), l.cfg.Store, timing); timingErr != nil {
				l.reportf("loop: staging timing for %s unavailable: %v\n", d.ItemID, timingErr)
			}
		}
	}()

	endStop1 := timeHandoffPhase(ctx, "check_stop_requested")
	stopErr := l.checkStopRequested(ctx)
	endStop1()
	if stopErr != nil {
		return stopErr
	}
	staged, refused, err := l.prepareClaimedStage(ctx, &d, &base, result)
	if err != nil || len(staged) == 0 {
		return err
	}
	endParallel := timeHandoffPhase(ctx, "stage_parallel")
	ready, err := l.stageParallel(ctx, staged[0], base)
	endParallel()
	if err != nil {
		if l.isRestartReplaced() {
			return errRestartPending
		}
		return l.holdStageFailed(ctx, d, "stage parallel", err)
	}
	endWatch := timeHandoffPhase(ctx, "watch_claimed_siblings")
	watch, err := l.watchClaimedSiblings(ctx, d.LaneID)
	endWatch()
	if err != nil {
		return err
	}
	endStop2 := timeHandoffPhase(ctx, "check_stop_requested")
	stopErr = l.checkStopRequested(ctx)
	endStop2()
	if stopErr != nil {
		return stopErr
	}
	if l.isRestartReplaced() {
		return errRestartPending
	}
	startHandoffFrom(ctx).setDecision(ready)
	l.launchRun(ctx, ready, at, watch, result)
	launched = true
	return nil
}

// Sample epochs before the store: if a sibling starts or finishes in between,
// the run's boundary drops it rather than attributing those writes to this run.
func (l *Loop) watchClaimedSiblings(ctx context.Context, target string) (*laneWatch, error) {
	watch := &laneWatch{epochs: l.idleLaneEpochs()}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		busy, err := readLanes(r, l.cfg.Now())
		if err != nil {
			return err
		}
		works, err := readPending(r)
		if err != nil {
			return err
		}
		for _, work := range works {
			if !work.Done && !work.LaneReleased {
				busy[work.Decision.LaneID] = true
			}
		}
		for _, lane := range l.cfg.Lanes {
			if _, idle := watch.epochs[lane]; !idle || lane == target || busy[lane] {
				continue
			}
			if l.hasLaneCondition(lane, "") {
				if _, err := os.Stat(l.lanePath(lane)); os.IsNotExist(err) {
					continue
				}
			}
			watch.lanes = append(watch.lanes, lane)
		}
		return nil
	})
	return watch, err
}
