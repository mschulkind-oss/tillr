package drain

import (
	"context"
	"errors"
	"fmt"
	"maps"
	"os"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/gateland"
	"github.com/mschulkind-oss/tillr-drain/internal/stagelaunch"
	"github.com/mschulkind-oss/tillr/store"
)

type launchableRun struct {
	itemID string
	runID  string
	work   *pendingWork
}

func (l *Loop) signalLaunchWake() {
	l.mu.Lock()
	ch := l.stageLaunch.Launch.Wake
	l.mu.Unlock()
	gateland.SignalWake(ch)
}

func (l *Loop) isLaunchCoordinatorActive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stageLaunch.Launch.Active
}

func isWorkLaunchEligible(work *pendingWork) bool {
	return stagelaunch.IsLaunchEligible(workStageLaunchEligibility(work))
}

// startLaunchCoordinator runs Step 2 (worker launch) outside the scheduling
// pass, woken by run.staged / resync relay notifications and reconciled on
// StorePollInterval (15s default) when notifications are absent or disabled.
func (l *Loop) startLaunchCoordinator(ctx context.Context, result *LoopResult) func() {
	coordCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	l.mu.Lock()
	l.stageLaunch.Ensure()
	l.stageLaunch.Launch.Active = true
	l.mu.Unlock()

	go func() {
		defer close(done)
		defer func() {
			l.mu.Lock()
			l.stageLaunch.Launch.Active = false
			l.mu.Unlock()
		}()
		l.runLaunchCoordinator(coordCtx, result)
	}()

	return func() {
		cancel()
		<-done
		l.stageLaunch.LaunchWG.Wait()
	}
}

func (l *Loop) runLaunchCoordinator(ctx context.Context, result *LoopResult) {
	wait := l.cfg.StorePollInterval
	if wait <= 0 {
		wait = DefaultFallbackPollInterval
	}
	gateland.RunLoop(ctx, wait, l.coordinatorFallbackTimer(l.stageLaunch.Launch.Timer), func() <-chan struct{} {
		l.mu.Lock()
		ch := l.stageLaunch.Launch.Wake
		l.mu.Unlock()
		return ch
	}, func() {
		l.scanAndLaunch(ctx, result)
	})
}

func (l *Loop) scanAndLaunch(ctx context.Context, result *LoopResult) {
	if ctx.Err() != nil || l.ownerMigrationItem != "" || l.isRestartReplaced() {
		return
	}
	if stopped, err := l.stopRequested(ctx); stopped || err != nil {
		return
	}
	if held, err := l.runDispatchHeld(ctx); held || err != nil {
		return
	}
	candidates, err := l.findLaunchableRuns(ctx)
	if err != nil || len(candidates) == 0 {
		return
	}
	for _, c := range candidates {
		l.mu.Lock()
		if ctx.Err() != nil {
			l.mu.Unlock()
			return
		}
		l.stageLaunch.Ensure()
		if l.stageLaunch.Launch.Runs[c.runID] || l.decisionAlreadyLaunched(c.work.Decision) || l.active[c.runID] || l.activeBrackets[c.runID] {
			l.mu.Unlock()
			continue
		}
		l.stageLaunch.Launch.MarkStart(c.itemID, c.runID)
		l.stageLaunch.LaunchWG.Add(1)
		l.mu.Unlock()

		go l.runCoordinatedLaunch(ctx, c.itemID, c.runID, c.work, result)
	}
}

func (l *Loop) findLaunchableRuns(ctx context.Context) ([]launchableRun, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	var found []launchableRun
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		l.mu.Lock()
		skip := make(map[string]bool, len(l.stageLaunch.Launch.Runs)+len(l.active)+len(l.activeBrackets))
		for id := range l.stageLaunch.Launch.Runs {
			skip[id] = true
		}
		for id := range l.active {
			skip[id] = true
		}
		for id := range l.activeBrackets {
			skip[id] = true
		}
		launched := maps.Clone(l.stageLaunch.LaunchedRuns)
		l.mu.Unlock()

		for itemID, work := range works {
			if !isWorkLaunchEligible(work) {
				continue
			}
			runID := work.Decision.RunRequest.RunID
			if skip[runID] || decisionAllIn(work.Decision, launched) {
				continue
			}
			paused, err := pausedPendingWork(r, work)
			if err != nil {
				return err
			}
			if paused {
				continue
			}
			exists, err := decisionRunRecordsExist(r, work.Decision)
			if err != nil {
				return err
			}
			if exists {
				continue
			}
			activeResume, err := decisionHasActiveResume(r, work.Decision)
			if err != nil {
				return err
			}
			if activeResume {
				continue
			}
			found = append(found, launchableRun{itemID: itemID, runID: runID, work: work})
		}
		return nil
	})
	if err != nil {
		return nil, err
	}
	if len(found) > 0 && l.cfg.Store != "" {
		live, _, liveErr := liveRunProcessIDs(l.cfg.Store)
		if liveErr != nil {
			return nil, liveErr
		}
		if len(live) > 0 {
			filtered := found[:0]
			for _, c := range found {
				if decisionHasLiveRun(c.work.Decision, live) {
					continue
				}
				filtered = append(filtered, c)
			}
			found = filtered
		}
		// Re-check the store after scanning /proc: an inherited live run can
		// finish, or pause and resume under a new PID not yet visible to /proc,
		// between the first ReadTx and liveRunProcessIDs inspecting its old PID.
		// Once the old PID disappears, its run.finish / run.resume is already
		// committed in the store.
		if len(found) > 0 {
			var confirmed []launchableRun
			err = s.ReadTx(ctx, func(r *store.Reader) error {
				works, err := readPending(r)
				if err != nil {
					return err
				}
				for _, c := range found {
					work := works[c.itemID]
					if work == nil || !isWorkLaunchEligible(work) || work.Decision.RunRequest.RunID != c.runID {
						continue
					}
					paused, err := pausedPendingWork(r, work)
					if err != nil {
						return err
					}
					if paused {
						continue
					}
					exists, err := decisionRunRecordsExist(r, work.Decision)
					if err != nil {
						return err
					}
					if exists {
						continue
					}
					activeResume, err := decisionHasActiveResume(r, work.Decision)
					if err != nil {
						return err
					}
					if activeResume {
						continue
					}
					confirmed = append(confirmed, launchableRun{itemID: c.itemID, runID: c.runID, work: work})
				}
				return nil
			})
			if err != nil {
				return nil, err
			}
			found = confirmed
		}
	}
	sort.Slice(found, func(i, j int) bool {
		if found[i].itemID != found[j].itemID {
			return found[i].itemID < found[j].itemID
		}
		return found[i].runID < found[j].runID
	})
	return found, nil
}

func decisionHasLiveRun(d Decision, live map[string]bool) bool {
	if d.RunRequest != nil && live[d.RunRequest.RunID] {
		return true
	}
	for _, node := range parallelNodes(d) {
		if node.RunRequest != nil && live[node.RunRequest.RunID] {
			return true
		}
	}
	return false
}

func decisionHasActiveResume(r *store.Reader, d Decision) (bool, error) {
	for _, node := range parallelNodes(d) {
		if node.RunRequest == nil || node.RunRequest.RunID == "" {
			continue
		}
		active, err := runHasActiveResume(r, node.RunRequest.RunID)
		if err != nil || active {
			return active, err
		}
	}
	return false, nil
}

func (l *Loop) runCoordinatedLaunch(ctx context.Context, itemID, runID string, work *pendingWork, result *LoopResult) {
	defer l.stageLaunch.LaunchWG.Done()
	defer func() {
		l.mu.Lock()
		l.stageLaunch.Launch.MarkDone(itemID, runID)
		l.mu.Unlock()
	}()

	launchCtx, startHandoff := l.beginStartHandoff(ctx, work.Decision)
	if err := l.launchStagedRun(launchCtx, work.Decision, l.cfg.Now().UTC(), nil, result); err != nil {
		finishStartHandoff(launchCtx)
		l.reportStageLaunchError(ctx, work.Decision, err)
		return
	}
	startHandoff.setDecision(work.Decision)
}

// launchStagedRun performs Step 2 for one staged run: verifies no stop or
// restart is pending, snapshots idle sibling lanes if needed, guards against
// duplicate in-process launch, and invokes launchRun.
func (l *Loop) launchStagedRun(ctx context.Context, d Decision, at time.Time, watch *laneWatch, result *LoopResult) error {
	if d.RunRequest == nil || d.RunRequest.RunID == "" {
		return nil
	}
	endStop := timeHandoffPhase(ctx, "check_stop_requested")
	stopErr := l.checkStopRequested(ctx)
	endStop()
	if stopErr != nil {
		return stopErr
	}
	if l.isRestartReplaced() {
		return errRestartPending
	}
	if pending, err := l.runDispatchHeld(ctx); err != nil {
		return err
	} else if pending {
		return errRestartPending
	}
	if watch == nil {
		endWatch := timeHandoffPhase(ctx, "watch_claimed_siblings")
		var err error
		watch, err = l.watchClaimedSiblings(ctx, d.LaneID)
		endWatch()
		if err != nil {
			return err
		}
	}
	id := d.RunRequest.RunID
	l.mu.Lock()
	if l.decisionAlreadyLaunched(d) || l.active[id] || l.activeBrackets[id] {
		l.mu.Unlock()
		return nil
	}
	l.markDecisionLaunched(d)
	l.mu.Unlock()

	startHandoffFrom(ctx).setDecision(d)
	l.launchRun(ctx, d, at, watch, result)
	return nil
}

func (l *Loop) reportStageLaunchError(ctx context.Context, d Decision, err error) {
	if errors.Is(err, errRestartPending) || stoppedByOperator(ctx, err) {
		return
	}
	// The pass owns loop failure reporting, including when it is waiting idle.
	select {
	case l.completions <- runCompletion{itemID: d.ItemID, step: "start", outcome: laneOutcome{err: fmt.Errorf("start %s: %w", d.ItemID, err)}}:
	case <-ctx.Done():
	}
}

// noteLaneWrite invalidates sibling observations of an authorized lane writer.
func (l *Loop) noteLaneWrite(lane string) {
	if lane == "" {
		return
	}
	l.mu.Lock()
	l.bumpLaneEpoch(lane)
	l.mu.Unlock()
}

// bumpLaneEpoch requires mu.
func (l *Loop) bumpLaneEpoch(lane string) {
	if l.laneChanges == nil {
		l.laneChanges = map[string]int{}
	}
	l.laneChanges[lane]++
}

// idleLaneEpochs excludes local staging writers; callers additionally exclude
// lanes that their store snapshot reports as busy.
func (l *Loop) idleLaneEpochs() map[string]int {
	l.mu.Lock()
	lanes := append([]string(nil), l.lanes()...)
	staging := maps.Clone(l.stagingLanes)
	changes := maps.Clone(l.laneChanges)
	l.mu.Unlock()

	seen := make(map[string]bool, len(lanes))
	epochs := make(map[string]int, len(lanes))
	add := func(lane string, requireWorktree bool) {
		if seen[lane] || staging[lane] != 0 {
			return
		}
		seen[lane] = true
		if requireWorktree {
			if _, err := os.Stat(l.lanePath(lane)); err != nil {
				return
			}
		}
		epochs[lane] = changes[lane]
	}
	for _, lane := range lanes {
		add(lane, l.cfg.LaneRoot != "" && (l.cfg.ItemWorktrees || !isPoolLaneName(lane)))
	}
	if l.cfg.LaneRoot != "" {
		if entries, err := os.ReadDir(l.cfg.LaneRoot); err == nil {
			for _, entry := range entries {
				if !entry.IsDir() || strings.HasPrefix(entry.Name(), ".") {
					continue
				}
				add(entry.Name(), true)
			}
		}
	}
	return epochs
}

type laneWatch struct {
	lanes  []string
	epochs map[string]int
}

func unmovedLanes(watched []string, seen, now map[string]int) []string {
	kept := make([]string, 0, len(watched))
	for _, lane := range watched {
		epoch, seenIdle := seen[lane]
		current, stillIdle := now[lane]
		if seenIdle && stillIdle && epoch == current {
			kept = append(kept, lane)
		}
	}
	return kept
}
