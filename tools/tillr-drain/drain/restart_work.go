package drain

import (
	"context"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr-drain/restart"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

var errRestartPending = heavyrun.ErrRestartPending

const (
	restartPollInterval = 50 * time.Millisecond
	restartPollCoalesce = restartPollInterval / 2
)

type pollReaderScope struct {
	reader *store.Store
}

func (s *pollReaderScope) close() {
	if s != nil && s.reader != nil {
		_ = s.reader.Close()
		s.reader = nil
	}
}

func (l *Loop) openPollStore(ctx context.Context) (*store.Store, error) {
	if l.pollOpenRead != nil {
		return l.pollOpenRead(ctx, l.cfg.Store)
	}
	return store.OpenRead(ctx, l.cfg.Store)
}

func (l *Loop) enablePollReader() {
	l.pollReaderMu.Lock()
	l.pollReaderEnabled = true
	l.pollReaderMu.Unlock()
}

func (l *Loop) dropMatchingPollReader(target *store.Store) {
	l.pollReaderMu.Lock()
	var r *store.Store
	if l.pollReader == target {
		r = l.pollReader
		l.pollReader = nil
	}
	l.pollReaderMu.Unlock()
	if r != nil {
		_ = r.Close()
	}
}

func (l *Loop) disableAndClosePollReader() bool {
	l.pollReaderMu.Lock()
	wasEnabled := l.pollReaderEnabled
	l.pollReaderEnabled = false
	r := l.pollReader
	l.pollReader = nil
	l.pollReaderMu.Unlock()
	if r != nil {
		_ = r.Close()
	}
	return wasEnabled
}

func (l *Loop) withPollReader(ctx context.Context, scope *pollReaderScope, fn func(*store.Reader) error) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	l.pollReaderMu.RLock()
	if l.pollReaderEnabled && l.pollReader != nil {
		r := l.pollReader
		err := r.ReadTx(ctx, fn)
		l.pollReaderMu.RUnlock()
		if err != nil && ctx.Err() == nil {
			l.dropMatchingPollReader(r)
		}
		return err
	}
	enabled := l.pollReaderEnabled
	l.pollReaderMu.RUnlock()

	if enabled {
		l.pollReaderMu.Lock()
		if l.pollReaderEnabled && l.pollReader == nil {
			r, err := l.openPollStore(ctx)
			if err != nil {
				l.pollReaderMu.Unlock()
				return err
			}
			l.pollReader = r
		}
		l.pollReaderMu.Unlock()

		l.pollReaderMu.RLock()
		if l.pollReaderEnabled && l.pollReader != nil {
			r := l.pollReader
			err := r.ReadTx(ctx, fn)
			l.pollReaderMu.RUnlock()
			if err != nil && ctx.Err() == nil {
				l.dropMatchingPollReader(r)
			}
			return err
		}
		l.pollReaderMu.RUnlock()
	}

	if scope != nil {
		if scope.reader == nil {
			r, err := l.openPollStore(ctx)
			if err != nil {
				return err
			}
			scope.reader = r
		}
		err := scope.reader.ReadTx(ctx, fn)
		if err != nil && ctx.Err() == nil {
			scope.close()
		}
		return err
	}

	s, err := l.openPollStore(ctx)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.ReadTx(ctx, fn)
}

func (l *Loop) restarterPendingWith(ctx context.Context, scope *pollReaderScope) (bool, error) {
	pending := false
	err := l.withPollReader(ctx, scope, func(r *store.Reader) error {
		state, err := restart.ReadState(r, restart.RoleDrain)
		pending = len(state.Pending) > 0
		return err
	})
	return pending, err
}

func (l *Loop) newRestartTicker() heartbeatTicker {
	if l.restartClock != nil {
		return l.restartClock()
	}
	t := time.NewTicker(restartPollInterval)
	return heartbeatTicker{C: t.C, Stop: t.Stop}
}

func (l *Loop) buildRestartPending(ctx context.Context) (bool, error) {
	if l.cfg.Restart == nil || l.restartDeferred() || l.restartDefersToSettlement() {
		return false, nil
	}
	return l.restarterPendingWith(ctx, nil)
}

func (l *Loop) restartDefersToSettlement() bool {
	return l.restartSettlements.Load() > 0
}

func (l *Loop) markRestartPending() {
	l.restartGateMu.Lock()
	if l.restartPendingWait == nil {
		l.restartPendingWait = make(chan struct{})
	}
	l.restartGateMu.Unlock()
}

func (l *Loop) clearRestartPending() {
	l.restartGateMu.Lock()
	ch := l.restartPendingWait
	l.restartPendingWait = nil
	l.restartAnswerGen++
	l.restartGateMu.Unlock()
	if ch != nil {
		close(ch)
	}
}

func (l *Loop) restartWaitState() (uint64, <-chan struct{}) {
	l.restartGateMu.Lock()
	gen, ch := l.restartAnswerGen, l.restartPendingWait
	l.restartGateMu.Unlock()
	return gen, ch
}

func (l *Loop) armRestartPending(gen uint64) (<-chan struct{}, bool) {
	l.restartGateMu.Lock()
	defer l.restartGateMu.Unlock()
	if l.restartAnswerGen != gen {
		return nil, true
	}
	if l.restartPendingWait == nil {
		l.restartPendingWait = make(chan struct{})
	}
	return l.restartPendingWait, false
}

// waitForRestartAnswer blocks until any pending non-deferred restart request
// has been answered (replaced or answered without replacing) or deferred.
func (l *Loop) waitForRestartAnswer(ctx context.Context) error {
	for {
		if l.restartReplaced.Load() {
			return errRestartPending
		}
		if l.cfg.Restart == nil || l.restartDeferred() {
			return nil
		}
		gen, waitCh := l.restartWaitState()
		if waitCh == nil {
			pending, err := l.restarterPendingWith(ctx, nil)
			if err != nil {
				return err
			}
			if !pending {
				return nil
			}
			var retry bool
			waitCh, retry = l.armRestartPending(gen)
			if retry {
				continue
			}
		}
		if l.restartWaitObs != nil {
			l.restartWaitObs()
		}
		select {
		case <-waitCh:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

type runStartAdmissionKey struct{}
type restartMechanicalKey struct{}
type restartMechanicalHold struct {
	loop     *Loop
	held     bool
	settling bool
}

func (h *restartMechanicalHold) acquire() {
	h.loop.restartMechanical.RLock()
	h.held = true
	if h.settling {
		h.loop.restartSettlements.Add(1)
	}
}

func (h *restartMechanicalHold) acquireContext(ctx context.Context) error {
	for {
		if h.settling {
			if err := h.loop.waitForRestartAnswer(ctx); err != nil {
				return err
			}
		} else {
			if h.loop.restartReplaced.Load() {
				return errRestartPending
			}
			if !h.loop.restartDefersToSettlement() {
				if pending, err := h.loop.buildRestartPending(ctx); err != nil {
					return err
				} else if pending {
					return errRestartPending
				}
			}
		}
		h.acquire()
		if h.loop.restartReplaced.Load() {
			h.release()
			return errRestartPending
		}
		if h.settling && h.loop.cfg.Restart != nil && !h.loop.restartDeferred() {
			if _, waitCh := h.loop.restartWaitState(); waitCh != nil {
				// A restart became pending between waitForRestartAnswer and RLock.
				// Release immediately so the restart is not starved, then wait.
				h.release()
				continue
			}
		} else if !h.settling && !h.loop.restartDefersToSettlement() {
			if pending, err := h.loop.buildRestartPending(ctx); err != nil {
				h.release()
				return err
			} else if pending {
				h.release()
				return errRestartPending
			}
		}
		return nil
	}
}

func (h *restartMechanicalHold) release() {
	if h.held {
		if h.settling {
			h.loop.restartSettlements.Add(-1)
		}
		h.held = false
		h.loop.restartMechanical.RUnlock()
	}
}

// Mechanical gate and landing steps finish before exec. A model session nested
// inside one temporarily gives up this place; its lifetime never bounds restart.
func (l *Loop) lockRestartMechanical(ctx context.Context) (context.Context, func(), error) {
	return l.lockRestartStep(ctx, false)
}

// An already admitted gate can finish through landing, while a pending restart
// stops new gates and landings from taking a new read hold until it is answered.
func (l *Loop) lockRestartSettlement(ctx context.Context) (context.Context, func(), error) {
	return l.lockRestartStep(ctx, true)
}

func (l *Loop) lockRestartStep(ctx context.Context, settling bool) (context.Context, func(), error) {
	h, nested := ctx.Value(restartMechanicalKey{}).(*restartMechanicalHold)
	if !nested {
		h = &restartMechanicalHold{loop: l, settling: settling}
		if err := h.acquireContext(ctx); err != nil {
			return ctx, func() {}, err
		}
		return context.WithValue(ctx, restartMechanicalKey{}, h), h.release, nil
	}
	if !settling || h.settling {
		return ctx, func() {}, nil
	}
	if l.restartReplaced.Load() {
		return ctx, func() {}, errRestartPending
	}
	h.settling = true
	l.restartSettlements.Add(1)
	return ctx, func() {
		l.restartSettlements.Add(-1)
		h.settling = false
	}, nil
}

// restartWork releases the planning pass from a model session or whole-tree
// check. Its result channel is buffered: an interrupted step can finish cleanup
// independently, but its discarded result is never recorded as a work failure.
func restartWork[T any](ctx context.Context, l *Loop, work func(context.Context) (T, error)) (res T, err error) {
	var zero T
	if l.cfg.Restart == nil {
		return work(ctx)
	}
	if held, ok := ctx.Value(restartMechanicalKey{}).(*restartMechanicalHold); ok {
		held.release()
		defer func() {
			if waitErr := held.acquireContext(ctx); waitErr != nil && err == nil {
				res = zero
				err = waitErr
			}
		}()
	}
	var scope pollReaderScope
	defer scope.close()
	settle := func() error {
		if err := l.checkStopRequestedWith(ctx, &scope); err != nil {
			return err
		}
		_, stop, err := l.gateRestartWith(ctx, &LoopResult{}, true, &scope)
		if err != nil {
			return err
		}
		if stop == StopRestarted {
			return errRestartPending
		}
		if stop != "" {
			return context.Canceled
		}
		return nil
	}
	settleTick := func() (bool, error) {
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if operatorStopped(ctx) {
			l.signalSessionStop()
			if err := ctx.Err(); err != nil {
				return false, err
			}
			return false, errStopRequested
		}
		if l.isRestartReplaced() {
			return false, errRestartPending
		}
		arrivedAt := time.Now()
		if l.restartTickArriveObs != nil {
			l.restartTickArriveObs()
		}
		l.restartPollMu.Lock()
		defer l.restartPollMu.Unlock()
		if err := ctx.Err(); err != nil {
			return false, err
		}
		if operatorStopped(ctx) {
			l.signalSessionStop()
			if err := ctx.Err(); err != nil {
				return false, err
			}
			return false, errStopRequested
		}
		if l.isRestartReplaced() {
			return false, errRestartPending
		}
		if !l.lastRestartPoll.IsZero() && (!arrivedAt.After(l.lastRestartPoll) || time.Since(l.lastRestartPoll) < restartPollCoalesce) {
			return true, nil
		}
		if err := settle(); err != nil {
			return false, err
		}
		l.lastRestartPoll = time.Now()
		return false, nil
	}
	// A request that is refused or already current answers without canceling a
	// running step. Only successful replacement relinquishes its ownership.
	if err := settle(); err != nil {
		return zero, err
	}
	child, cancel := context.WithCancel(ctx)
	defer cancel()
	type answer struct {
		value T
		err   error
	}
	start := func() <-chan answer {
		done := make(chan answer, 1)
		go func() { value, err := work(child); done <- answer{value, err} }()
		return done
	}
	done := start()
	ticker := l.newRestartTicker()
	defer ticker.Stop()
	for {
		select {
		case a := <-done:
			if err := settle(); err != nil {
				return zero, err
			}
			if errors.Is(a.err, errRestartPending) {
				// Handoff refused a request filed after initial admission. Its
				// answer is now known: retry launch only if no replacement occurred.
				done = start()
				continue
			}
			return a.value, a.err
		case <-ctx.Done():
			return zero, ctx.Err()
		case <-ticker.C:
			coalesced, err := settleTick()
			if err != nil {
				return zero, err
			}
			if l.restartTickDoneObs != nil {
				l.restartTickDoneObs(coalesced)
			}
		}
	}
}

func (l *Loop) runRestartable(ctx context.Context, req run.Request) (run.Record, string, error) {
	type answer struct {
		rec run.Record
		log string
	}
	out, err := restartWork(ctx, l, func(child context.Context) (answer, error) {
		if req.Resume {
			endResume := timeHandoffPhase(child, "record_resumed_build")
			err := l.recordResumedBuild(child, req)
			endResume()
			if err != nil {
				return answer{}, err
			}
		}
		if _, ok := l.cfg.Steps.(workerProcessStarter); !ok {
			finishStartHandoff(child)
		}
		child = context.WithValue(child, runStartAdmissionKey{}, l)
		rec, log, err := l.cfg.Steps.Run(child, req)
		return answer{rec, log}, err
	})
	return out.rec, out.log, err
}
