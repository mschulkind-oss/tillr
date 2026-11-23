package drain

import (
	"context"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

const handoffTimingFile = "handoff.jsonl"

type handoffSpan struct {
	Phase   string `json:"phase"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
}

func newHandoffSpan(phase string, start, end time.Time) handoffSpan {
	return handoffSpan{phase, start.UnixMilli(), end.UnixMilli()}
}

type handoffTiming struct {
	handoffSpan
	Phases       []handoffSpan `json:"phases,omitempty"`
	PrimaryRunID string        `json:"primary_run_id,omitempty"`
}

type examinationTimer struct {
	mu     sync.Mutex
	phases []handoffSpan
}

type examinationTimerKey struct{}

type preHandoffKey struct{}

type preHandoffState struct {
	start  time.Time
	phases []handoffSpan
}

func withPreHandoffSpans(ctx context.Context, start time.Time, phases []handoffSpan) context.Context {
	if start.IsZero() && len(phases) == 0 {
		return ctx
	}
	return context.WithValue(ctx, preHandoffKey{}, preHandoffState{
		start:  start,
		phases: append([]handoffSpan(nil), phases...),
	})
}

func timeHandoffPhase(ctx context.Context, phase string) func() {
	timer, ok := ctx.Value(examinationTimerKey{}).(*examinationTimer)
	if !ok {
		return func() {}
	}
	start := time.Now()
	return func() { timer.add(phase, start, time.Now()) }
}

func (t *examinationTimer) add(phase string, start, end time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.phases = append(t.phases, newHandoffSpan(phase, start, end))
}

func (l *Loop) recordHandoffTiming(d Decision, timing handoffTiming) {
	// A parallel group has one boundary and examination. Record the shared
	// interval on each member, whose own finish may have been much earlier.
	for _, node := range parallelNodes(d) {
		if node.RunRequest != nil {
			id := node.RunRequest.RunID
			if d.RunRequest != nil && id != d.RunRequest.RunID {
				timing.PrimaryRunID = d.RunRequest.RunID
			}
			if err := appendRunLine(l.cfg.Root, id, handoffTimingFile, timing); err != nil {
				l.reportf("loop: handoff timing for %s: %v\n", id, err)
			}
		}
	}
}

type startHandoff struct {
	loop     *Loop
	decision Decision
	start    time.Time
	timer    *examinationTimer
	once     sync.Once
}

type startHandoffKey struct{}

// workerProcessStarter marks a Steps implementation that calls
// finishStartHandoff right after spawning the worker process (before waiting
// for it to exit). Stub Steps implementations that do not implement it have
// their start handoff closed right before Run is called.
type workerProcessStarter interface {
	reportsWorkerProcessStart()
}

func (l *Loop) beginStartHandoff(ctx context.Context, d Decision) (context.Context, *startHandoff) {
	sh := &startHandoff{
		loop:     l,
		decision: d,
		start:    time.Now(),
		timer:    &examinationTimer{},
	}
	return withStartHandoff(ctx, sh), sh
}

func startHandoffFrom(ctx context.Context) *startHandoff {
	sh, _ := ctx.Value(startHandoffKey{}).(*startHandoff)
	return sh
}

func withStartHandoff(ctx context.Context, sh *startHandoff) context.Context {
	if sh == nil {
		return ctx
	}
	ctx = context.WithValue(ctx, startHandoffKey{}, sh)
	ctx = context.WithValue(ctx, examinationTimerKey{}, sh.timer)
	ctx = store.ObserveWrites(ctx, func(start, end time.Time) { sh.timer.add("store_write", start, end) })
	return ctx
}

func (sh *startHandoff) setDecision(d Decision) {
	if sh == nil {
		return
	}
	sh.timer.mu.Lock()
	sh.decision = d
	sh.timer.mu.Unlock()
}

func (sh *startHandoff) finish() {
	if sh == nil {
		return
	}
	sh.once.Do(func() {
		end := time.Now()
		sh.timer.mu.Lock()
		d := sh.decision
		timing := handoffTiming{handoffSpan: newHandoffSpan("start", sh.start, end), Phases: sh.timer.phases}
		sh.timer.phases = nil
		sh.timer.mu.Unlock()
		sh.loop.recordHandoffTiming(d, timing)
	})
}

func finishStartHandoff(ctx context.Context) {
	if sh := startHandoffFrom(ctx); sh != nil {
		sh.finish()
	}
}

func (l *Loop) timeHandoff(ctx context.Context, d Decision, phase string) (context.Context, func()) {
	t := &examinationTimer{}
	start := time.Now()
	if pre, ok := ctx.Value(preHandoffKey{}).(preHandoffState); ok {
		if !pre.start.IsZero() {
			start = pre.start
		}
		if len(pre.phases) > 0 {
			t.phases = append(t.phases, pre.phases...)
		}
		ctx = context.WithValue(ctx, preHandoffKey{}, preHandoffState{})
	}
	ctx = context.WithValue(ctx, examinationTimerKey{}, t)
	ctx = store.ObserveWrites(ctx, func(start, end time.Time) { t.add("store_write", start, end) })
	return ctx, func() {
		end := time.Now()
		t.mu.Lock()
		timing := handoffTiming{handoffSpan: newHandoffSpan(phase, start, end), Phases: t.phases}
		// Detach the snapshot from an interrupted subprocess or continuation.
		t.phases = nil
		t.mu.Unlock()
		l.recordHandoffTiming(d, timing)
	}
}
