package drain

import (
	"context"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
)

var errRestartPending = errors.New("loop replacement pending")

func (l *Loop) buildRestartPending(ctx context.Context) (bool, error) {
	if l.cfg.Restart == nil || l.rollAction.busy() || l.piRollAction.busy() || l.pushAction.busy() {
		return false, nil
	}
	return l.restarter().Pending(ctx)
}

type runStartAdmissionKey struct{}
type restartMechanicalKey struct{}
type restartMechanicalHold struct {
	loop *Loop
	held bool
}

func (h *restartMechanicalHold) acquire() { h.loop.restartMechanical.RLock(); h.held = true }
func (h *restartMechanicalHold) release() {
	if h.held {
		h.held = false
		h.loop.restartMechanical.RUnlock()
	}
}

// Mechanical gate and landing steps finish before exec. A model session nested
// inside one temporarily gives up this place; its lifetime never bounds restart.
func (l *Loop) lockRestartMechanical(ctx context.Context) (context.Context, func(), error) {
	if _, ok := ctx.Value(restartMechanicalKey{}).(*restartMechanicalHold); ok {
		return ctx, func() {}, nil
	}
	h := &restartMechanicalHold{loop: l}
	h.acquire()
	pending, err := l.buildRestartPending(ctx)
	if pending && err == nil {
		err = errRestartPending
	}
	if err != nil {
		h.release()
		return ctx, func() {}, err
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
	settle := func() error {
		if err := l.checkStopRequested(ctx); err != nil {
			return err
		}
		_, stop, err := l.gateRestart(ctx, &LoopResult{})
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
	ticker := time.NewTicker(50 * time.Millisecond)
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
			if err := settle(); err != nil {
				return zero, err
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
