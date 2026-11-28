// Architecture and invariants: docs/reference/tillr-drain-successor.md
// Scheduling and notifications: docs/design/tillr-scheduling-and-notifications.md (D-SN5, D-SN6, D-SN7)
package drain

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/notify"
)

// DefaultFallbackPollInterval is 15 seconds: the fallback store reconciliation
// interval when no relayed store-change notification arrives (D-SN6). Polling
// bounds observation latency, never the amount or duration of work.
const DefaultFallbackPollInterval = 15 * time.Second

const (
	notifyReconnectInitial = 100 * time.Millisecond
	notifyReconnectCap     = time.Second
	notifyAutoStartWait    = time.Second
)

type triggerNotify struct {
	wake   chan struct{}
	cancel context.CancelFunc
	done   chan struct{}
}

func (l *Loop) notifyEnabled() bool {
	return !l.pollOnly && !l.cfg.NoNotify
}

func (l *Loop) notifyIdleExit() time.Duration {
	if l.cfg.NotifyIdleExit > 0 {
		return l.cfg.NotifyIdleExit
	}
	return notify.DefaultIdleExit
}

// startTriggerNotify establishes the loop's long-lived subscription to the local
// Unix-socket relay (auto-starting `tillr notify serve` if absent, per D-SN5) and
// runs a reconnect loop with jittered exponential backoff (100 ms to 1 s) and
// immediate resync wake on reconnect. Returning the stop function cleanly closes
// the subscription and joins the reader goroutine.
func (l *Loop) startTriggerNotify(ctx context.Context) func() {
	if !l.notifyEnabled() {
		return func() {}
	}
	subCtx, cancel := context.WithCancel(ctx)
	tn := &triggerNotify{
		wake:   make(chan struct{}, 1),
		cancel: cancel,
		done:   make(chan struct{}),
	}

	// Best-effort synchronous initial subscribe so frames emitted immediately
	// after Loop.Run starts are not missed before the first waitForTrigger.
	initCtx, initCancel := context.WithTimeout(subCtx, notifyAutoStartWait)
	initialSub, _ := l.subscribeWithAutoStart(initCtx)
	initCancel()

	l.mu.Lock()
	l.triggerNotify = tn
	l.mu.Unlock()

	go l.runTriggerNotifyLoop(subCtx, initialSub, tn)

	return func() {
		cancel()
		<-tn.done
		l.mu.Lock()
		if l.triggerNotify == tn {
			l.triggerNotify = nil
		}
		l.mu.Unlock()
	}
}

func (l *Loop) runTriggerNotifyLoop(ctx context.Context, initialSub *notify.Subscription, tn *triggerNotify) {
	defer close(tn.done)
	sub := initialSub
	connectedOnce := initialSub != nil
	backoff := notifyReconnectInitial

	defer func() {
		if sub != nil {
			_ = sub.Close()
		}
	}()

	for ctx.Err() == nil {
		if sub == nil {
			if !waitFor(ctx, jitteredBackoff(backoff)) {
				return
			}
			backoff *= 2
			if backoff > notifyReconnectCap {
				backoff = notifyReconnectCap
			}
			attemptCtx, attemptCancel := context.WithTimeout(ctx, notifyAutoStartWait)
			nextSub, err := l.subscribeWithAutoStart(attemptCtx)
			attemptCancel()
			if err != nil {
				continue
			}
			sub = nextSub
			backoff = notifyReconnectInitial
			if connectedOnce {
				// Reconnected after a disconnect: immediately run the fallback
				// poll query so events committed during the gap do not wait 15 s.
				signalWake(tn.wake)
				l.signalStageWake()
				l.signalLaunchWake()
				l.signalExamineWake()
				l.signalGateWake()
				l.signalLandWake()
				l.signalSyncWake()
				l.signalTrunkProofWake()
				l.signalGCWake()
			}
			connectedOnce = true
		}

		for {
			frame, err := sub.Next(ctx)
			if err != nil {
				_ = sub.Close()
				sub = nil
				break
			}
			if l.notifyFrameObs != nil {
				l.notifyFrameObs(frame)
			}
			switch frame.Topic {
			case notify.TopicRunAssigned:
				l.signalStageWake()
			case notify.TopicRunStaged:
				l.signalLaunchWake()
			case notify.TopicRunExited:
				l.signalExamineWake()
				l.signalGCWake()
				signalWake(tn.wake)
			case notify.TopicCandidateReady, notify.TopicGateFinished:
				l.signalGateWake()
				signalWake(tn.wake)
			case notify.TopicGatePassed:
				l.signalLandWake()
				signalWake(tn.wake)
			case notify.TopicTrunkMoved:
				l.signalLandWake()
				l.signalTrunkProofWake()
				l.signalSyncWake()
				signalWake(tn.wake)
			case notify.TopicProofGreen:
				l.signalLandWake()
				l.signalTrunkProofWake()
				signalWake(tn.wake)
			case notify.TopicDocsChanged:
				l.signalSyncWake()
			case notify.TopicItemEnded, notify.TopicAgentSlotFreed:
				l.signalGCWake()
				signalWake(tn.wake)
			case notify.TopicResync:
				signalWake(tn.wake)
				l.signalStageWake()
				l.signalLaunchWake()
				l.signalExamineWake()
				l.signalGateWake()
				l.signalLandWake()
				l.signalSyncWake()
				l.signalTrunkProofWake()
				l.signalGCWake()
			default:
				signalWake(tn.wake)
			}
		}
	}
}

func signalWake(ch chan struct{}) {
	select {
	case ch <- struct{}{}:
	default:
	}
}

func jitteredBackoff(base time.Duration) time.Duration {
	if base <= 0 {
		return 0
	}
	// +/- 25% jitter around base, preserving [100ms..1s] bounds roughly.
	quarter := int64(base / 4)
	if quarter <= 0 {
		return base
	}
	delta := rand.Int64N(2*quarter+1) - quarter
	return base + time.Duration(delta)
}

func (l *Loop) subscribeWithAutoStart(ctx context.Context) (*notify.Subscription, error) {
	sub, err := notify.Subscribe(ctx, l.cfg.Store, "*")
	if err == nil {
		return sub, nil
	}
	if !isRelayAbsent(err) {
		return nil, err
	}
	if startErr := l.ensureRelayStarted(ctx); startErr != nil {
		return nil, startErr
	}
	delay := 5 * time.Millisecond
	for {
		sub, err = notify.Subscribe(ctx, l.cfg.Store, "*")
		if err == nil {
			return sub, nil
		}
		if !isRelayAbsent(err) || ctx.Err() != nil {
			return nil, err
		}
		if !waitFor(ctx, delay) {
			return nil, ctx.Err()
		}
		if delay < 100*time.Millisecond {
			delay *= 2
			if delay > 100*time.Millisecond {
				delay = 100 * time.Millisecond
			}
		}
	}
}

func isRelayAbsent(err error) bool {
	return errors.Is(err, os.ErrNotExist) ||
		errors.Is(err, syscall.ENOENT) ||
		errors.Is(err, syscall.ECONNREFUSED)
}

func (l *Loop) ensureRelayStarted(ctx context.Context) error {
	idleExit := l.notifyIdleExit()
	if l.notifyStartRelay != nil {
		return l.notifyStartRelay(ctx, l.cfg.Store, idleExit)
	}
	sockPath, err := notify.SocketPath(l.cfg.Store)
	if err != nil {
		return err
	}
	dir := filepath.Dir(sockPath)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	lockPath := notify.LockPath(sockPath)
	lockFile, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	flockErr := syscall.Flock(int(lockFile.Fd()), syscall.LOCK_EX|syscall.LOCK_NB)
	if flockErr != nil {
		_ = lockFile.Close()
		if errors.Is(flockErr, syscall.EWOULDBLOCK) || errors.Is(flockErr, syscall.EAGAIN) {
			// Another relay already holds the lock and is starting or listening.
			return nil
		}
		return flockErr
	}
	// Release the lock before spawning so the child relay does not inherit the
	// descriptor and can acquire <sock>.lock immediately. If two subscribers
	// race here, one spawned relay wins <sock>.lock and the other exits cleanly.
	_ = syscall.Flock(int(lockFile.Fd()), syscall.LOCK_UN)
	_ = lockFile.Close()

	bin, err := resolveNotifyBin(l.cfg.NotifyBin)
	if err != nil {
		return err
	}
	cmd := exec.Command(bin, "notify", "serve", "--store", l.cfg.Store, "--idle-exit", idleExit.String())
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil {
		return err
	}
	go func() { _ = cmd.Wait() }()
	return nil
}

func resolveNotifyBin(configured string) (string, error) {
	if strings.TrimSpace(configured) != "" {
		return exec.LookPath(configured)
	}
	if self, err := os.Executable(); err == nil {
		candidate := filepath.Join(filepath.Dir(self), "tillr")
		if info, statErr := os.Stat(candidate); statErr == nil && !info.IsDir() && info.Mode()&0o111 != 0 {
			return candidate, nil
		}
	}
	return exec.LookPath("tillr")
}

func (l *Loop) coordinatorFallbackTimer(specific func(time.Duration) (<-chan time.Time, func())) func(time.Duration) (<-chan time.Time, func()) {
	if specific != nil {
		return specific
	}
	if l.triggerTimer != nil {
		// When a test injects triggerTimer, waitForTrigger owns the stepped
		// fallback clock and fans out fallback wakes to every coordinator on
		// each timer tick so coordinators do not spin independent wall-clock
		// timers or compete for the test's stepped-clock channel.
		return func(time.Duration) (<-chan time.Time, func()) {
			return make(chan time.Time), func() {}
		}
	}
	return nil
}

func (l *Loop) signalAllCoordinatorFallbackWakes() {
	l.signalStageWake()
	l.signalLaunchWake()
	l.signalExamineWake()
	l.signalGateWake()
	l.signalLandWake()
	l.signalSyncWake()
	l.signalTrunkProofWake()
	l.signalGCWake()
}

func (l *Loop) waitForTrigger(ctx context.Context, leases []LaneLease, at time.Time, result *LoopResult) (string, error) {
	defer l.startPassPhase("wait_for_trigger")()
	if err := l.checkStopRequested(ctx); err != nil {
		if stoppedByOperator(ctx, err) {
			result.Detail = "the loop was asked to stop while waiting for a trigger"
			return StopOperatorStop, nil
		}
		return "", err
	}
	wait := l.cfg.StorePollInterval
	for _, lease := range leases {
		if !l.pollOnly && lease.LeasedUntil != nil {
			until := lease.LeasedUntil.Sub(at)
			if until < wait {
				wait = until
			}
		}
	}
	if wait < 0 {
		wait = 0
	}
	var timerC <-chan time.Time
	var stopTimer func()
	if l.triggerTimer != nil {
		timerC, stopTimer = l.triggerTimer(wait)
	} else {
		timer := time.NewTimer(wait)
		timerC, stopTimer = timer.C, func() { timer.Stop() }
	}
	defer stopTimer()

	var notifyWake <-chan struct{}
	passWake := l.actors.PassWakeChan()
	if l.notifyEnabled() {
		l.mu.Lock()
		tn := l.triggerNotify
		l.mu.Unlock()
		if tn == nil {
			stopLocal := l.startTriggerNotify(ctx)
			defer stopLocal()
			l.mu.Lock()
			tn = l.triggerNotify
			l.mu.Unlock()
		}
		if tn != nil {
			notifyWake = tn.wake
		}
	}

	completions := l.completions
	// Tests can suppress completion/expiry accelerators; polling alone must
	// converge on the same durable outcomes. The next pass drains completions.
	if l.pollOnly {
		completions = nil
	}
	_, restartWaitCh := l.restartWaitState()
	rollIdle := l.rollAction.idle()
	handleCompletion := func(c runCompletion) (bool, string, error) {
		before := l.dispatched(result)
		stop, err := l.finishRunStep(ctx, c, result)
		l.mu.Lock()
		advanced := result.Dispatched != before || (l.activeItems[c.itemID] == "" && l.examiningItems[c.itemID] == 0 && !l.gateLand.HasItem(c.itemID))
		l.mu.Unlock()
		if stop != "" || err != nil || advanced {
			return true, stop, err
		}
		// The handoff began this item's examination. Keep listening for its
		// answer on completions without letting the handoff's own store writes
		// wake notifyWake prematurely (any external wake remains buffered on
		// tn.wake for the next waitForTrigger).
		notifyWake = nil
		passWake = nil
		return false, "", nil
	}
	for {
		select {
		case c := <-completions:
			if done, stop, err := handleCompletion(c); done {
				return stop, err
			}
			continue
		default:
		}
		select {
		case c := <-completions:
			if done, stop, err := handleCompletion(c); done {
				return stop, err
			}
		case <-restartWaitCh:
			if err := l.checkStopRequested(ctx); err != nil {
				if stoppedByOperator(ctx, err) {
					result.Detail = "the loop was asked to stop while waiting for a trigger"
					return StopOperatorStop, nil
				}
				return "", err
			}
			return "", nil
		case <-rollIdle:
			if err := l.checkStopRequested(ctx); err != nil {
				if stoppedByOperator(ctx, err) {
					result.Detail = "the loop was asked to stop while waiting for a trigger"
					return StopOperatorStop, nil
				}
				return "", err
			}
			return "", nil
		case <-l.triageWake:
			if err := l.checkStopRequested(ctx); err != nil {
				if stoppedByOperator(ctx, err) {
					result.Detail = "the loop was asked to stop while waiting for a trigger"
					return StopOperatorStop, nil
				}
				return "", err
			}
			l.dispatchAlertTriage(ctx)
			return "", nil
		case <-notifyWake:
			if err := l.checkStopRequested(ctx); err != nil {
				if stoppedByOperator(ctx, err) {
					result.Detail = "the loop was asked to stop while waiting for a trigger"
					return StopOperatorStop, nil
				}
				return "", err
			}
			return "", nil
		case <-passWake:
			if err := l.checkStopRequested(ctx); err != nil {
				if stoppedByOperator(ctx, err) {
					result.Detail = "the loop was asked to stop while waiting for a trigger"
					return StopOperatorStop, nil
				}
				return "", err
			}
			return "", nil
		case <-timerC:
			l.signalAllCoordinatorFallbackWakes()
			if err := l.checkStopRequested(ctx); err != nil {
				if stoppedByOperator(ctx, err) {
					result.Detail = "the loop was asked to stop while waiting for a trigger"
					return StopOperatorStop, nil
				}
				return "", err
			}
			return "", nil
		case <-ctx.Done():
			result.Detail = "the loop was asked to stop while waiting for a trigger"
			return StopOperatorStop, nil
		}
	}
}
