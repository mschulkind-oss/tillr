package drain

import (
	"context"
	"errors"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
)

var errRestartPending = errors.New("loop replacement pending")

func (l *Loop) buildRestartPending(ctx context.Context) (bool, error) {
	if l.cfg.Restart == nil || l.restartDeferred() {
		return false, nil
	}
	return l.restarter().Pending(ctx)
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
	} else if settling || !l.restartDefersToSettlement() {
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
