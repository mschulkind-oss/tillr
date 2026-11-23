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

// An already admitted gate can finish through landing, but a waiting roll need
// not take on another settlement. While a settlement owns the read lock, other
// work (including worker claims and staging) can still share it.
func (l *Loop) lockRestartSettlement(ctx context.Context) (context.Context, func(), error) {
	return l.lockRestartStep(ctx, true)
}

func (l *Loop) lockRestartStep(ctx context.Context, settling bool) (context.Context, func(), error) {
	h, nested := ctx.Value(restartMechanicalKey{}).(*restartMechanicalHold)
	if !nested {
		h = &restartMechanicalHold{loop: l}
		h.acquire()
	} else if !settling || h.settling {
		return ctx, func() {}, nil
	}
	var err error
	if l.restartReplaced {
		err = errRestartPending
	} else if !l.restartDefersToSettlement() {
		var pending bool
		pending, err = l.buildRestartPending(ctx)
		if pending && err == nil {
			err = errRestartPending
		}
	}
	if err != nil {
		if !nested {
			h.release()
		}
		return ctx, func() {}, err
	}
	if settling {
		h.settling = true
		l.restartSettlements.Add(1)
	}
	if nested {
		return ctx, func() {
			l.restartSettlements.Add(-1)
			h.settling = false
		}, nil
	}
	return context.WithValue(ctx, restartMechanicalKey{}, h), h.release, nil
}

// restartWork releases the planning pass from a model session or whole-tree
// check. Its result channel is buffered: an interrupted step can finish cleanup
// independently, but its discarded result is never recorded as a work failure.
func restartWork[T any](ctx context.Context, l *Loop, work func(context.Context) (T, error)) (T, error) {
	var zero T
	if l.cfg.Restart == nil {
		return work(ctx)
	}
	if held, ok := ctx.Value(restartMechanicalKey{}).(*restartMechanicalHold); ok {
		held.release()
		defer held.acquire()
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
			if err := l.recordResumedBuild(child, req); err != nil {
				return answer{}, err
			}
		}
		child = context.WithValue(child, runStartAdmissionKey{}, l)
		rec, log, err := l.cfg.Steps.Run(child, req)
		return answer{rec, log}, err
	})
	return out.rec, out.log, err
}
