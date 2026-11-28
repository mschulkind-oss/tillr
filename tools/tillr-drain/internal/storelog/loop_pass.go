package storelog

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// LoopPassKind records that the drain loop completed a pass, throttled to at most once per
// LoopPassInterval per loop process so tillr-watch can tell a silent stopped loop from an
// idle running one without writing an event on every empty pass.
const LoopPassKind = "drain.loop-pass"

// LoopPassRemainderPhase is the phase key in LoopPassFact.PhasesMS for pass wall time
// spent outside any named phase.
const LoopPassRemainderPhase = "other"

func init() {
	if err := event.Register(LoopPassKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

// LoopPassFact is the payload of a LoopPassKind event.
type LoopPassFact struct {
	SessionID string           `json:"session_id"`
	WallMS    int64            `json:"wall_ms"`
	PhasesMS  map[string]int64 `json:"phases_ms"`
	// CompletionStepsMS names the steps inside the drain_completions phase. It is
	// separate from PhasesMS and is not part of the sum invariant: the steps
	// subdivide a phase, and a step run outside the pass goroutine may overlap.
	CompletionStepsMS map[string]int64 `json:"completion_steps_ms,omitempty"`
}

// LoopPassInterval is the minimum spacing between recorded LoopPassKind events.
const LoopPassInterval = time.Minute

// LoopPassTimer records wall time for one drain loop pass and each of its named phases.
type LoopPassTimer struct {
	now    func() time.Time
	start  time.Time
	phases map[string]time.Duration
	mu     sync.Mutex // guards steps; a step may end on another goroutine
	steps  map[string]time.Duration
}

// NewLoopPassTimer constructs a LoopPassTimer anchored at now().
func NewLoopPassTimer(now func() time.Time) *LoopPassTimer {
	if now == nil {
		now = time.Now
	}
	return &LoopPassTimer{
		now:    now,
		start:  now(),
		phases: map[string]time.Duration{},
	}
}

// StartPhase begins timing a named phase and returns a closure that ends it.
func (t *LoopPassTimer) StartPhase(name string) func() {
	if t == nil {
		return func() {}
	}
	begin := t.now()
	return func() {
		d := t.now().Sub(begin)
		if d < 0 {
			d = 0
		}
		t.phases[name] += d
	}
}

// StartStep begins timing a named step inside a phase and returns a closure that
// ends it. Steps are reported by Steps, apart from the phases.
func (t *LoopPassTimer) StartStep(name string) func() {
	if t == nil {
		return func() {}
	}
	begin := t.now()
	return func() {
		d := t.now().Sub(begin)
		if d < 0 {
			d = 0
		}
		t.mu.Lock()
		defer t.mu.Unlock()
		if t.steps == nil {
			t.steps = map[string]time.Duration{}
		}
		t.steps[name] += d
	}
}

// Steps returns per-step wall times in milliseconds, or nil when none was timed.
func (t *LoopPassTimer) Steps() map[string]int64 {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if len(t.steps) == 0 {
		return nil
	}
	out := make(map[string]int64, len(t.steps))
	for name, d := range t.steps {
		out[name] = d.Milliseconds()
	}
	return out
}

type passStepTimerKey struct{}

// WithPassStepTimer carries the pass timer to code that may run off the pass
// goroutine, so it need not read the loop's passTimer directly.
func WithPassStepTimer(ctx context.Context, pt *LoopPassTimer) context.Context {
	if pt == nil {
		return ctx
	}
	return context.WithValue(ctx, passStepTimerKey{}, pt)
}

// StartPassStep times a named step inside a phase; a no-op without a carried timer.
func StartPassStep(ctx context.Context, name string) func() {
	pt, _ := ctx.Value(passStepTimerKey{}).(*LoopPassTimer)
	return pt.StartStep(name)
}

// Finish returns the pass completion timestamp, total wall time in milliseconds, and
// per-phase wall times in milliseconds, including LoopPassRemainderPhase so the entries
// in phasesMS sum to wallMS.
func (t *LoopPassTimer) Finish() (at time.Time, wallMS int64, phasesMS map[string]int64) {
	if t == nil {
		return time.Now().UTC(), 0, map[string]int64{LoopPassRemainderPhase: 0}
	}
	end := t.now()
	total := end.Sub(t.start)
	if total < 0 {
		total = 0
	}
	wallMS = total.Milliseconds()
	phasesMS = make(map[string]int64, len(t.phases)+1)
	var sum int64
	for name, d := range t.phases {
		if d < 0 {
			d = 0
		}
		ms := d.Milliseconds()
		phasesMS[name] = ms
		sum += ms
	}
	remainder := wallMS - sum
	if remainder < 0 {
		wallMS = sum
		remainder = 0
	}
	phasesMS[LoopPassRemainderPhase] = remainder
	return end.UTC(), wallMS, phasesMS
}

// LoopPassParams holds the inputs for RecordLoopPass.
type LoopPassParams struct {
	StorePath      string
	SessionID      string
	Initialized    bool
	SessionOptions []store.OpenOption
	LastLoopPassAt time.Time
	Now            func() time.Time
	Timer          *LoopPassTimer
	BeforeWrite    func()
	Reportf        func(string, ...any)
}

// RecordLoopPass appends LoopPassKind after a pass when none has been recorded yet or
// at least LoopPassInterval has elapsed since LastLoopPassAt. A failed write is logged
// and never stops or fails the pass.
func RecordLoopPass(ctx context.Context, p LoopPassParams) (time.Time, bool) {
	if ctx.Err() != nil {
		return time.Time{}, false
	}
	var (
		at       time.Time
		wallMS   int64
		phasesMS map[string]int64
	)
	if p.Timer != nil {
		at, wallMS, phasesMS = p.Timer.Finish()
	} else {
		now := p.Now
		if now == nil {
			now = time.Now
		}
		at = now().UTC()
		phasesMS = map[string]int64{LoopPassRemainderPhase: 0}
	}
	if !p.LastLoopPassAt.IsZero() && at.Sub(p.LastLoopPassAt) < LoopPassInterval {
		return time.Time{}, false
	}
	id := p.SessionID
	if id == "" {
		id = "loop"
	}
	body, err := json.Marshal(LoopPassFact{
		SessionID: id,
		WallMS:    wallMS,
		PhasesMS:  phasesMS,

		CompletionStepsMS: p.Timer.Steps(),
	})
	if err != nil {
		if ctx.Err() == nil && p.Reportf != nil {
			p.Reportf("loop: the loop pass was not recorded: %v\n", err)
		}
		return time.Time{}, false
	}
	if p.BeforeWrite != nil {
		p.BeforeWrite()
	}
	s, err := store.Open(ctx, p.StorePath, !p.Initialized, p.SessionOptions...)
	if err != nil {
		if ctx.Err() == nil && p.Reportf != nil {
			p.Reportf("loop: the loop pass was not recorded: %v\n", err)
		}
		return time.Time{}, false
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       LoopPassKind,
			ObjectType: "session",
			ObjectID:   &id,
			Payload:    body,
			At:         at,
		})
		return err
	})
	if closeErr := s.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		if ctx.Err() == nil && p.Reportf != nil {
			p.Reportf("loop: the loop pass was not recorded: %v\n", err)
		}
		return time.Time{}, false
	}
	return at, true
}
