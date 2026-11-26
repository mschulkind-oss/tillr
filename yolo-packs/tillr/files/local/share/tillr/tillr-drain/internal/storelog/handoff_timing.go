package storelog

import (
	"context"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/store"
)

// HandoffTimingFile is the per-run JSONL filename for handoff timing records.
const HandoffTimingFile = "handoff.jsonl"

// HandoffSpan records one named phase interval in Unix milliseconds.
type HandoffSpan struct {
	Phase   string `json:"phase"`
	StartMS int64  `json:"start_ms"`
	EndMS   int64  `json:"end_ms"`
}

// NewHandoffSpan constructs a HandoffSpan from start and end timestamps.
func NewHandoffSpan(phase string, start, end time.Time) HandoffSpan {
	return HandoffSpan{Phase: phase, StartMS: start.UnixMilli(), EndMS: end.UnixMilli()}
}

// HandoffTiming is one JSONL record written to handoff.jsonl.
type HandoffTiming struct {
	HandoffSpan
	Phases       []HandoffSpan `json:"phases,omitempty"`
	PrimaryRunID string        `json:"primary_run_id,omitempty"`
}

// ExaminationTimer collects concurrent handoff phase spans for one examination or start.
type ExaminationTimer struct {
	mu     sync.Mutex
	phases []HandoffSpan
}

// Add appends one phase span to t.
func (t *ExaminationTimer) Add(phase string, start, end time.Time) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.phases = append(t.phases, NewHandoffSpan(phase, start, end))
}

// Drain returns all recorded phases and clears the timer's slice.
func (t *ExaminationTimer) Drain() []HandoffSpan {
	t.mu.Lock()
	defer t.mu.Unlock()
	phases := t.phases
	t.phases = nil
	return phases
}

// Lock locks the timer mutex (for callers coordinating extra fields alongside phases).
func (t *ExaminationTimer) Lock() { t.mu.Lock() }

// Unlock unlocks the timer mutex.
func (t *ExaminationTimer) Unlock() { t.mu.Unlock() }

type examinationTimerKey struct{}

type preHandoffKey struct{}

type preHandoffState struct {
	start  time.Time
	phases []HandoffSpan
}

// WithPreHandoffSpans attaches pre-handoff phase spans to ctx for BeginHandoffTimer.
func WithPreHandoffSpans(ctx context.Context, start time.Time, phases []HandoffSpan) context.Context {
	if start.IsZero() && len(phases) == 0 {
		return ctx
	}
	return context.WithValue(ctx, preHandoffKey{}, preHandoffState{
		start:  start,
		phases: append([]HandoffSpan(nil), phases...),
	})
}

// AttachTimerContext attaches timer to ctx and observes store writes into timer.
func AttachTimerContext(ctx context.Context, timer *ExaminationTimer) context.Context {
	ctx = context.WithValue(ctx, examinationTimerKey{}, timer)
	ctx = store.ObserveWrites(ctx, func(start, end time.Time) { timer.Add("store_write", start, end) })
	return ctx
}

// TimeHandoffPhase times a named phase against the ExaminationTimer on ctx, if any.
func TimeHandoffPhase(ctx context.Context, phase string) func() {
	timer, ok := ctx.Value(examinationTimerKey{}).(*ExaminationTimer)
	if !ok {
		return func() {}
	}
	start := time.Now()
	return func() { timer.Add(phase, start, time.Now()) }
}

// BeginHandoffTimer starts a timed handoff context, absorbing any pre-handoff spans on ctx,
// and returns the derived context and a finish function producing the HandoffTiming record.
func BeginHandoffTimer(ctx context.Context, phase string) (context.Context, func() HandoffTiming) {
	t := &ExaminationTimer{}
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
	ctx = AttachTimerContext(ctx, t)
	return ctx, func() HandoffTiming {
		end := time.Now()
		return HandoffTiming{HandoffSpan: NewHandoffSpan(phase, start, end), Phases: t.Drain()}
	}
}

// RecordHandoffTimingRuns appends timing to handoff.jsonl for each run in runIDs.
func RecordHandoffTimingRuns(root, primaryRunID string, runIDs []string, timing HandoffTiming, reportf func(string, ...any)) {
	for _, id := range runIDs {
		t := timing
		if primaryRunID != "" && id != primaryRunID {
			t.PrimaryRunID = primaryRunID
		}
		if err := drainmodel.AppendRunLine(root, id, HandoffTimingFile, t); err != nil && reportf != nil {
			reportf("loop: handoff timing for %s: %v\n", id, err)
		}
	}
}
