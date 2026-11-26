package drain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// LoopPassKind records that the drain loop completed a pass, throttled to at most once per
// loopPassInterval per loop process so tillr-watch can tell a silent stopped loop from an
// idle running one without writing an event on every empty pass.
const LoopPassKind = "drain.loop-pass"

// LoopPassRemainderPhase is the phase key in LoopPassFact.PhasesMS for pass wall time
// spent outside any named phase.
const LoopPassRemainderPhase = "other"

func init() {
	if err := event.Register(LoopPassKind, Utility); err != nil {
		panic(err)
	}
}

// LoopPassFact is the payload of a LoopPassKind event.
type LoopPassFact struct {
	SessionID string           `json:"session_id"`
	WallMS    int64            `json:"wall_ms"`
	PhasesMS  map[string]int64 `json:"phases_ms"`
}

const loopPassInterval = time.Minute

// loopPassTimer records wall time for one drain loop pass and each of its named phases.
type loopPassTimer struct {
	now    func() time.Time
	start  time.Time
	phases map[string]time.Duration
}

func newLoopPassTimer(now func() time.Time) *loopPassTimer {
	if now == nil {
		now = time.Now
	}
	return &loopPassTimer{
		now:    now,
		start:  now(),
		phases: map[string]time.Duration{},
	}
}

func (t *loopPassTimer) startPhase(name string) func() {
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

// finish returns the pass completion timestamp, total wall time in milliseconds, and
// per-phase wall times in milliseconds, including LoopPassRemainderPhase so the entries
// in phasesMS sum to wallMS.
func (t *loopPassTimer) finish() (at time.Time, wallMS int64, phasesMS map[string]int64) {
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

func (l *Loop) startPassPhase(name string) func() {
	return l.passTimer.startPhase(name)
}

func (l *Loop) timePassPhase(name string, fn func() error) error {
	end := l.startPassPhase(name)
	err := fn()
	end()
	return err
}

// recordLoopPass appends LoopPassKind after a pass when this loop has recorded none yet or
// at least loopPassInterval has elapsed since the last recorded pass. A failed write is
// logged and never stops or fails the pass.
func (l *Loop) recordLoopPass(ctx context.Context, pt *loopPassTimer) {
	if ctx.Err() != nil {
		return
	}
	var (
		at       time.Time
		wallMS   int64
		phasesMS map[string]int64
	)
	if pt != nil {
		at, wallMS, phasesMS = pt.finish()
	} else {
		at = l.cfg.Now().UTC()
		phasesMS = map[string]int64{LoopPassRemainderPhase: 0}
	}
	if !l.lastLoopPassAt.IsZero() && at.Sub(l.lastLoopPassAt) < loopPassInterval {
		return
	}
	l.mu.Lock()
	id := l.sessionID
	initialized := l.sessionInitialized
	l.mu.Unlock()
	if id == "" {
		id = "loop"
	}
	body, err := json.Marshal(LoopPassFact{
		SessionID: id,
		WallMS:    wallMS,
		PhasesMS:  phasesMS,
	})
	if err != nil {
		if ctx.Err() == nil {
			l.reportf("loop: the loop pass was not recorded: %v\n", err)
		}
		return
	}
	if l.beforeRecordLoopPass != nil {
		l.beforeRecordLoopPass()
	}
	s, err := store.Open(ctx, l.cfg.Store, !initialized, l.sessionOptions...)
	if err != nil {
		if ctx.Err() == nil {
			l.reportf("loop: the loop pass was not recorded: %v\n", err)
		}
		return
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
		if ctx.Err() == nil {
			l.reportf("loop: the loop pass was not recorded: %v\n", err)
		}
		return
	}
	l.lastLoopPassAt = at
}
