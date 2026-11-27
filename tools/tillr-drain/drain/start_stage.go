package drain

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/gateland"
	"github.com/mschulkind-oss/tillr-drain/internal/stagelaunch"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// dispatchStaged is the payload of a drain.staged event committed by Step 1
// (the staging coordinator) after preparing the worktree and rendering the
// run.request file under <worktree>.stage.lock.
type dispatchStaged struct {
	RunID        string   `json:"run_id"`
	ItemID       string   `json:"item_id,omitempty"`
	LaneID       string   `json:"lane_id,omitempty"`
	WorktreePath string   `json:"worktree_path,omitempty"`
	BaseSHA      string   `json:"base_sha,omitempty"`
	RequestPath  string   `json:"request_path,omitempty"`
	Decision     Decision `json:"decision"`
}

type stageableRun struct {
	itemID string
	runID  string
	work   *pendingWork
}

func workStageLaunchEligibility(work *pendingWork) stagelaunch.Eligibility {
	if work == nil {
		return stagelaunch.Eligibility{}
	}
	runID := ""
	if work.Decision.RunRequest != nil {
		runID = work.Decision.RunRequest.RunID
	}
	return stagelaunch.Eligibility{
		Done:         work.Done,
		Ready:        work.Ready,
		Staged:       work.Staged,
		HasCandidate: work.Candidate != nil,
		Examining:    work.Examining,
		HasExaminer:  work.Examiner != nil,
		LaneReleased: work.LaneReleased,
		RunID:        runID,
		IsDispatch:   isDispatch(work.Decision),
	}
}

func isWorkStageEligible(work *pendingWork) bool {
	return stagelaunch.IsStageEligible(workStageLaunchEligibility(work))
}

func (l *Loop) signalStageWake() {
	l.mu.Lock()
	ch := l.stageLaunch.Stage.Wake
	l.mu.Unlock()
	gateland.SignalWake(ch)
}

func (l *Loop) isStageCoordinatorActive() bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.stageLaunch.Stage.Active
}

// startStageCoordinator runs Step 1 (worktree staging) outside the scheduling
// pass, woken by run.assigned / resync relay notifications and reconciled on
// StorePollInterval (15s default) when notifications are absent or disabled.
func (l *Loop) startStageCoordinator(ctx context.Context, result *LoopResult) func() {
	coordCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})

	l.mu.Lock()
	l.stageLaunch.Ensure()
	l.stageLaunch.Stage.Active = true
	l.mu.Unlock()

	go func() {
		defer close(done)
		defer func() {
			l.mu.Lock()
			l.stageLaunch.Stage.Active = false
			l.mu.Unlock()
		}()
		l.runStageCoordinator(coordCtx, result)
	}()

	return func() {
		cancel()
		<-done
		l.stageLaunch.StageWG.Wait()
	}
}

func (l *Loop) runStageCoordinator(ctx context.Context, result *LoopResult) {
	wait := l.cfg.StorePollInterval
	if wait <= 0 {
		wait = DefaultFallbackPollInterval
	}
	gateland.RunLoop(ctx, wait, l.stageLaunch.Stage.Timer, func() <-chan struct{} {
		if !l.notifyEnabled() {
			return nil
		}
		l.mu.Lock()
		ch := l.stageLaunch.Stage.Wake
		l.mu.Unlock()
		return ch
	}, func() {
		l.scanAndStage(ctx, result)
	})
}

func (l *Loop) scanAndStage(ctx context.Context, result *LoopResult) {
	if ctx.Err() != nil || l.ownerMigrationItem != "" || l.isRestartReplaced() {
		return
	}
	if stopped, err := l.stopRequested(ctx); stopped || err != nil {
		return
	}
	candidates, err := l.findStageableRuns(ctx)
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
		if l.stageLaunch.HasRun(c.runID) || l.stageLaunch.IsLaunched(c.runID) || l.active[c.runID] || l.activeBrackets[c.runID] {
			l.mu.Unlock()
			continue
		}
		l.stageLaunch.Stage.MarkStart(c.itemID, c.runID)
		l.stageLaunch.StageWG.Add(1)
		l.mu.Unlock()

		go l.runCoordinatedStage(ctx, c.itemID, c.runID, c.work, result)
	}
}

func (l *Loop) findStageableRuns(ctx context.Context) ([]stageableRun, error) {
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return nil, err
	}
	defer s.Close()

	var found []stageableRun
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		works, err := readPending(r)
		if err != nil {
			return err
		}
		l.mu.Lock()
		skip := make(map[string]bool, l.stageLaunch.RunCount()+len(l.stageLaunch.LaunchedRuns)+len(l.active)+len(l.activeBrackets))
		for id := range l.stageLaunch.Stage.Runs {
			skip[id] = true
		}
		for id := range l.stageLaunch.Launch.Runs {
			skip[id] = true
		}
		for id := range l.stageLaunch.LaunchedRuns {
			skip[id] = true
		}
		for id := range l.active {
			skip[id] = true
		}
		for id := range l.activeBrackets {
			skip[id] = true
		}
		l.mu.Unlock()

		for itemID, work := range works {
			if !isWorkStageEligible(work) {
				continue
			}
			runID := work.Decision.RunRequest.RunID
			if skip[runID] {
				continue
			}
			paused, err := pausedPendingWork(r, work)
			if err != nil {
				return err
			}
			if paused {
				continue
			}
			exists, err := stagelaunch.RunRecordExists(r, runID)
			if err != nil {
				return err
			}
			if exists {
				continue
			}
			found = append(found, stageableRun{itemID: itemID, runID: runID, work: work})
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

func (l *Loop) runCoordinatedStage(ctx context.Context, itemID, runID string, work *pendingWork, result *LoopResult) {
	defer l.stageLaunch.StageWG.Done()
	defer func() {
		l.mu.Lock()
		l.stageLaunch.Stage.MarkDone(itemID, runID)
		l.mu.Unlock()
	}()

	stageCtx, startHandoff := l.beginStartHandoff(ctx, work.Decision)
	ready, _, err := l.stageAssignedRun(stageCtx, work.Decision, work.BaseSHA, l.cfg.Now().UTC(), result)
	if ready != nil {
		startHandoff.setDecision(*ready)
	}
	finishStartHandoff(stageCtx)
	if err != nil {
		l.reportStageLaunchError(ctx, work.Decision, err)
		return
	}
	if ready != nil && l.notifyEnabled() {
		l.signalLaunchWake()
	}
}

// stageAssignedRun performs Step 1 for one assigned run: acquires the
// worktree's non-blocking stage lock, prepares the checkout and parallel nodes
// without holding the scheduling lock or restartMechanical, renders the
// run.request file, and commits drain.staged guarded by unlaunched/unstaged.
func (l *Loop) stageAssignedRun(ctx context.Context, d Decision, base string, at time.Time, result *LoopResult) (*Decision, bool, error) {
	unlockLock, acquired, lockErr := stagelaunch.AcquireStageLock(l.lanePath(d.LaneID))
	if lockErr != nil {
		return nil, false, lockErr
	}
	if !acquired {
		// Another worker/process holds <worktree>.stage.lock; let it finish
		// without retracting the assignment.
		return nil, false, nil
	}
	defer unlockLock()

	end := l.beginLaneStaging(d.LaneID)
	defer end()

	endStop1 := timeHandoffPhase(ctx, "check_stop_requested")
	stopErr := l.checkStopRequested(ctx)
	endStop1()
	if stopErr != nil {
		if !l.isRestartReplaced() {
			if retractErr := l.cancelStaging(context.WithoutCancel(ctx), d); retractErr != nil {
				return nil, false, errors.Join(stopErr, fmt.Errorf("cancel staging: %w", retractErr))
			}
		}
		return nil, false, stopErr
	}

	l.mu.Lock()
	l.laneRefusedNow = false
	l.mu.Unlock()

	staged, refused, err := l.prepareClaimedStage(ctx, &d, &base, result)
	if refused {
		return nil, false, err
	}
	if err != nil {
		if l.isRestartReplaced() {
			return nil, false, errRestartPending
		}
		if retractErr := l.cancelStagingIfNotReplaced(ctx, d); retractErr != nil {
			err = errors.Join(err, fmt.Errorf("cancel staging: %w", retractErr))
		}
		return nil, false, err
	}
	if len(staged) == 0 {
		if retractErr := l.cancelStagingIfNotReplaced(ctx, d); retractErr != nil {
			return nil, false, fmt.Errorf("cancel staging: %w", retractErr)
		}
		return nil, true, nil
	}

	endParallel := timeHandoffPhase(ctx, "stage_parallel")
	ready, err := l.stageParallel(ctx, staged[0], base)
	endParallel()
	if err != nil {
		if l.isRestartReplaced() {
			return nil, false, errRestartPending
		}
		if stoppedByOperator(ctx, err) || ctx.Err() != nil {
			if retractErr := l.cancelStagingIfNotReplaced(ctx, d); retractErr != nil {
				err = errors.Join(err, fmt.Errorf("cancel staging: %w", retractErr))
			}
			return nil, false, err
		}
		holdErr := l.holdStageFailed(ctx, d, "stage parallel", err)
		if retractErr := l.cancelStagingIfNotReplaced(ctx, d); retractErr != nil {
			return nil, false, errors.Join(holdErr, fmt.Errorf("cancel staging: %w", retractErr))
		}
		if holdErr != nil {
			return nil, false, holdErr
		}
		return nil, true, nil
	}

	requestPath, reqErr := l.renderStagedRequest(ready, at)
	if reqErr != nil {
		if l.isRestartReplaced() {
			return nil, false, errRestartPending
		}
		holdErr := l.holdStageFailed(ctx, d, "render run request", reqErr)
		if retractErr := l.cancelStagingIfNotReplaced(ctx, d); retractErr != nil {
			return nil, false, errors.Join(holdErr, fmt.Errorf("cancel staging: %w", retractErr))
		}
		if holdErr != nil {
			return nil, false, holdErr
		}
		return nil, true, nil
	}

	endStop2 := timeHandoffPhase(ctx, "check_stop_requested")
	stopErr = l.checkStopRequested(ctx)
	endStop2()
	if stopErr != nil {
		if retractErr := l.cancelStagingIfNotReplaced(ctx, d); retractErr != nil {
			return nil, false, errors.Join(stopErr, fmt.Errorf("cancel staging: %w", retractErr))
		}
		return nil, false, stopErr
	}
	if l.isRestartReplaced() {
		return nil, false, errRestartPending
	}
	if pending, err := l.runDispatchHeld(ctx); err != nil {
		return nil, false, err
	} else if pending {
		return nil, false, errRestartPending
	}

	committed, err := l.recordDispatchStaged(ctx, ready, base, requestPath, at)
	if err != nil {
		return nil, false, err
	}
	if !committed {
		return nil, false, nil
	}
	return &ready, false, nil
}

func (l *Loop) cancelStagingIfNotReplaced(ctx context.Context, d Decision) error {
	l.restartMechanical.RLock()
	replaced := l.isRestartReplaced()
	var retractErr error
	if !replaced {
		retractErr = l.cancelStaging(context.WithoutCancel(ctx), d)
	}
	l.restartMechanical.RUnlock()
	return retractErr
}

func (l *Loop) renderStagedRequest(d Decision, at time.Time) (string, error) {
	if l.cfg.LaneRoot == "" || d.RunRequest == nil || d.RunRequest.RunID == "" || d.RunRequest.Backend == "" || len(d.RunRequest.Command) == 0 {
		return "", nil
	}
	req := *d.RunRequest
	stampDecisionRequest(d, &req)
	env, err := requestEnvelope(Decision{ItemID: d.ItemID, RunRequest: &req}, at.UTC())
	if err != nil {
		return "", err
	}
	body, err := record.Encode(env)
	if err != nil {
		return "", Internalf("encoding the run.request record: %v", err)
	}
	requests := filepath.Join(l.cfg.LaneRoot, ".requests")
	if err := os.MkdirAll(requests, 0o755); err != nil {
		return "", Externalf("creating the request directory %s: %v", requests, err)
	}
	input := filepath.Join(requests, req.RunID+".request.json")
	if err := os.WriteFile(input, body, 0o644); err != nil {
		return "", Externalf("writing %s: %v", input, err)
	}
	return input, nil
}

// recordDispatchStaged commits drain.staged in one short WriteTx guarded by
// run_id still being unstaged and unlaunched. Post-commit, store.WriteTx
// publishes run.staged on the notification relay.
func (l *Loop) recordDispatchStaged(ctx context.Context, d Decision, base, requestPath string, at time.Time) (bool, error) {
	if l.cfg.Store == "" || d.RunRequest == nil || d.RunRequest.RunID == "" {
		return true, nil
	}
	l.restartMechanical.RLock()
	defer l.restartMechanical.RUnlock()
	if l.isRestartReplaced() {
		return false, errRestartPending
	}
	s, err := store.Open(ctx, l.cfg.Store, true)
	if err != nil {
		return false, err
	}
	defer s.Close()

	committed := false
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		works, err := readPending(tx.Reader())
		if err != nil {
			return err
		}
		work := works[d.ItemID]
		if !isWorkStageEligible(work) || work.Decision.RunRequest.RunID != d.RunRequest.RunID {
			return nil
		}
		exists, err := stagelaunch.RunRecordExists(tx.Reader(), d.RunRequest.RunID)
		if err != nil {
			return err
		}
		if exists {
			return nil
		}
		payload := dispatchStaged{
			RunID:        d.RunRequest.RunID,
			ItemID:       d.ItemID,
			LaneID:       d.LaneID,
			WorktreePath: l.laneTarget(d.LaneID),
			BaseSHA:      base,
			RequestPath:  requestPath,
			Decision:     d,
		}
		if err := appendEffect(tx, dispatchStagedKind, d.RunRequest.RunID, payload, at.UTC()); err != nil {
			return err
		}
		committed = true
		return nil
	})
	return committed, err
}

// stageAndLaunchClaimed runs Step 1 (stageAssignedRun) followed by Step 2
// (launchStagedRun) for an already recorded assignment. Live loops run the two
// steps in their own coordinators (start_stage.go and start_coordinator.go);
// this helper composes them for direct unit tests.
func (l *Loop) stageAndLaunchClaimed(ctx context.Context, d Decision, base string, at time.Time, result *LoopResult) error {
	ready, _, err := l.stageAssignedRun(ctx, d, base, at, result)
	if err != nil || ready == nil {
		return err
	}
	return l.launchStagedRun(ctx, *ready, at, nil, result)
}

// stageAndLaunchInline runs Step 1 and Step 2 when a caller invokes pass()
// directly without starting Loop.Run's outside-pass coordinators.
func (l *Loop) stageAndLaunchInline(ctx context.Context, d Decision, base string, at time.Time, watch *laneWatch, result *LoopResult) (bool, error) {
	startCtx, startHandoff := l.beginStartHandoff(ctx, d)
	ready, cancelled, err := l.stageAssignedRun(startCtx, d, base, at, result)
	if errors.Is(err, errRestartPending) {
		finishStartHandoff(startCtx)
		return false, nil
	}
	if err != nil || ready == nil {
		finishStartHandoff(startCtx)
		return cancelled, err
	}
	startHandoff.setDecision(*ready)
	if err := l.launchStagedRun(withStartHandoff(ctx, startHandoff), *ready, at, watch, result); err != nil {
		finishStartHandoff(startCtx)
		if errors.Is(err, errRestartPending) {
			return false, nil
		}
		return false, err
	}
	return false, nil
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
