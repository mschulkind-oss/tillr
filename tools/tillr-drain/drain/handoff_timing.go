package drain

import (
	"context"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
)

func (l *Loop) recordHandoffTiming(d Decision, timing handoffTiming) {
	var ids []string
	for _, node := range parallelNodes(d) {
		if node.RunRequest != nil {
			ids = append(ids, node.RunRequest.RunID)
		}
	}
	primary := ""
	if d.RunRequest != nil {
		primary = d.RunRequest.RunID
	}
	storelog.RecordHandoffTimingRuns(l.cfg.Root, primary, ids, timing, l.reportf)
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
	return storelog.AttachTimerContext(ctx, sh.timer)
}

func (sh *startHandoff) setDecision(d Decision) {
	if sh == nil {
		return
	}
	sh.timer.Lock()
	sh.decision = d
	sh.timer.Unlock()
}

func (sh *startHandoff) finish() {
	if sh == nil {
		return
	}
	sh.once.Do(func() {
		end := time.Now()
		sh.timer.Lock()
		d := sh.decision
		sh.timer.Unlock()
		timing := handoffTiming{HandoffSpan: newHandoffSpan("start", sh.start, end), Phases: sh.timer.Drain()}
		sh.loop.recordHandoffTiming(d, timing)
	})
}

func finishStartHandoff(ctx context.Context) {
	if sh := startHandoffFrom(ctx); sh != nil {
		sh.finish()
	}
}

func (l *Loop) timeHandoff(ctx context.Context, d Decision, phase string) (context.Context, func()) {
	ctx, finish := storelog.BeginHandoffTimer(ctx, phase)
	return ctx, func() {
		l.recordHandoffTiming(d, finish())
	}
}
