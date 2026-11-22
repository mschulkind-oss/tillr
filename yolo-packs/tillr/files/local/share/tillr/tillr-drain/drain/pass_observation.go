package drain

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// PassInterval bounds the wait for an asynchronous completion or a leased lane.
// It keeps session deadlines current without recording repeated, unchanged answers.
const PassInterval = time.Second

const SessionDDL = `
CREATE TABLE IF NOT EXISTS drain_sessions (
 session_id TEXT PRIMARY KEY,
 started_at INTEGER NOT NULL,
 refreshed_at INTEGER NOT NULL,
 next_pass_at INTEGER NOT NULL,
 ended_at INTEGER
);
`

func nullablePassID(id string) any {
	if id == "" {
		return nil
	}
	return id
}

func (l *Loop) refreshSession(ctx context.Context, next time.Time) error {
	l.sessionMu.Lock()
	defer l.sessionMu.Unlock()
	at := l.cfg.Now().UTC()
	// A heartbeat during a scheduled backoff must not shorten the next-pass time.
	if next.UnixMilli() < l.sessionNextPass {
		next = time.UnixMilli(l.sessionNextPass)
	}
	l.sessionNextPass = next.UnixMilli()
	if l.sessionID == "" {
		id, err := NewSessionID()
		if err != nil {
			return err
		}
		l.mu.Lock()
		l.sessionID, l.sessionStarted = id, at.UnixMilli()
		l.mu.Unlock()
	}
	// Only a successful first refresh proves this Loop initialized all registered
	// owners and legacy data. Later heartbeats open that existing store, not a
	// globally version-skipped migration path.
	if l.keepWAL && l.walKeeper == nil {
		// The keeper first, so the migration below, and every other program's close
		// meanwhile, runs with the owner held: startup is never a stretch without one.
		openKeeper := l.keeperOpen
		if openKeeper == nil {
			openKeeper = openWALKeeper
		}
		keeper, err := openKeeper(ctx, l.cfg.Store)
		if err != nil {
			return Incompatiblef("%v", err)
		}
		l.walKeeper, l.lastCheckpoint = keeper, at
	}
	if l.beforeSessionOpen != nil {
		l.beforeSessionOpen(!l.sessionInitialized)
	}
	s, err := store.Open(ctx, l.cfg.Store, !l.sessionInitialized, l.sessionOptions...)
	if err != nil {
		return Incompatiblef("%v", err)
	}
	defer s.Close()
	owner, err := currentLoopOwner()
	if err != nil {
		return err
	}
	// The presence file is held before the session is recorded, so a loop in another
	// process domain never sees this session open with no writer behind it.
	if err := holdLoopPresence(l.cfg.Store); err != nil {
		return err
	}
	raw, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	lanesRaw, err := json.Marshal(l.lanes())
	if err != nil {
		return err
	}
	stopFlag := 0
	err = l.writeTx(ctx, s, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, loop_owner, lanes)
   VALUES (?, ?, ?, ?, ?, ?) ON CONFLICT(session_id) DO UPDATE SET
   refreshed_at = excluded.refreshed_at, next_pass_at = excluded.next_pass_at, lanes = excluded.lanes`,
			l.sessionID, l.sessionStarted, at.UnixMilli(), next.UnixMilli(), string(raw), string(lanesRaw)); err != nil {
			return err
		}
		return tx.QueryRow(`SELECT COALESCE(stop_requested, 0) FROM drain_sessions WHERE session_id = ?`, l.sessionID).Scan(&stopFlag)
	})
	if err == nil {
		l.mu.Lock()
		l.sessionInitialized = true
		l.mu.Unlock()
		if stopFlag > 0 {
			l.signalSessionStop()
		}
	}
	return err
}

func (l *Loop) currentSessionID() string {
	l.mu.Lock()
	defer l.mu.Unlock()
	return l.sessionID
}

func (l *Loop) setSessionStop(stop func()) {
	l.mu.Lock()
	l.sessionStop = stop
	l.mu.Unlock()
}

func (l *Loop) signalSessionStop() {
	l.mu.Lock()
	stop := l.sessionStop
	l.mu.Unlock()
	if stop != nil {
		stop()
	}
}

func (l *Loop) stopRequested(ctx context.Context) (bool, error) {
	l.mu.Lock()
	id, initialized := l.sessionID, l.sessionInitialized
	l.mu.Unlock()
	if id == "" || !initialized {
		return false, nil
	}
	if operatorStopped(ctx) {
		return true, nil
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return false, err
	}
	defer s.Close()
	flag := 0
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		err := r.QueryRow(`SELECT COALESCE(stop_requested, 0) FROM drain_sessions WHERE session_id = ?`, id).Scan(&flag)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		return err
	})
	if err != nil {
		return false, err
	}
	return flag > 0, nil
}

func (l *Loop) checkStopRequested(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	stopped, err := l.stopRequested(ctx)
	if err != nil {
		return err
	}
	if stopped {
		l.signalSessionStop()
		if err := ctx.Err(); err != nil {
			return err
		}
		return errStopRequested
	}
	return nil
}

// heartbeatTicker is the clock the heartbeat waits on: C delivers one tick per
// PassInterval until Stop is called. newHeartbeatTicker's default is a time.Ticker.
type heartbeatTicker struct {
	C    <-chan time.Time
	Stop func()
}

// newHeartbeatTicker answers the production ticker unless a test supplied its own.
func (l *Loop) newHeartbeatTicker() heartbeatTicker {
	if l.heartbeatClock != nil {
		return l.heartbeatClock()
	}
	t := time.NewTicker(PassInterval)
	return heartbeatTicker{C: t.C, Stop: t.Stop}
}

// beat is one heartbeat of a running loop: refreshSession, except that a store too busy
// to take the write is reported and not a fault. The heartbeat is liveness bookkeeping.
// A beat that could not be written leaves refreshed_at where it was, so every reader of
// the session -- the view's "missed heartbeat", another process domain's stop judging
// the session stale after SessionGrace -- sees exactly what happened: a stale beat, never
// a false one. The next tick, a second later, writes the next beat; that is the
// heartbeat's ordinary schedule, not a retry of this one. What a failed beat must not do
// is stop the loop: on 2026-11-18 a beat that waited out the busy timeout behind the
// loop's own plan applications stopped the loop twice, and each stop cancelled the
// full-suite proof trunk was waiting on. Whatever held the lock is the defect, and the
// store's trace records it; the loop's own work goes on and meets the same store with
// its own errors. Any other refresh failure -- a store this program cannot open, an
// unrecordable presence -- is still a fault.
func (l *Loop) beat(ctx context.Context, next time.Time) error {
	// A run just started is claiming its lane with a write of its own (claim_gate.go); the
	// heartbeat is the loop's most frequent writer, so it takes its turn after the claim.
	if err := claimGateFor(l.cfg.Store).wait(ctx); err != nil && ctx.Err() != nil {
		return err
	}
	start := time.Now()
	err := l.refreshSession(ctx, next)
	if err != nil && store.IsBusy(err) && ctx.Err() == nil {
		l.recordBusyWrite("session-heartbeat", time.Since(start), l.cfg.Now())
		l.reportf("loop: the session heartbeat was not recorded, the store was busy: %v\n", err)
		return nil
	}
	return err
}

// keepSessionAlive refreshes the same session while synchronous startup, staging,
// planning, or rolling work prevents the pass goroutine from reaching its next read.
// A failed heartbeat cancels that work and returns a fault, never false liveness; a
// busy store is not a failed heartbeat (beat).
func (l *Loop) keepSessionAlive(ctx context.Context, cancel context.CancelFunc) func() error {
	stop := make(chan struct{})
	done := make(chan error, 1)
	go func() {
		ticker := l.newHeartbeatTicker()
		defer ticker.Stop()
		for {
			select {
			case <-stop:
				done <- nil
				return
			case <-ctx.Done():
				done <- nil
				return
			case <-ticker.C:
				err := l.beat(ctx, l.cfg.Now().Add(PassInterval))
				if err == nil {
					err = l.checkpointIfDue(ctx)
				}
				if err != nil {
					if ctx.Err() != nil {
						done <- nil
					} else {
						done <- err
						cancel()
					}
					return
				}
			}
		}
	}()
	return func() error { close(stop); return <-done }
}

func (l *Loop) endSession() {
	if l.sessionID == "" {
		return
	}
	ctx := context.Background()
	s, err := store.Open(ctx, l.cfg.Store, !l.sessionInitialized)
	if err == nil {
		defer s.Close()
		err = s.WriteTx(ctx, func(tx *store.Tx) error {
			_, err := tx.Exec(`UPDATE drain_sessions SET ended_at = ? WHERE session_id = ?`, l.cfg.Now().UnixMilli(), l.sessionID)
			return err
		})
	}
	if err != nil {
		l.reportf("loop: the session stop was not recorded: %v\n", err)
	}
}

// passAnswers excludes observation identity: seeing the same successor answers at
// another time or in another pass must not create another measurement.
func passAnswers(plans []plannedApplication) (string, map[string]int, error) {
	answers := make([]Decision, 0, len(plans))
	counts := map[string]int{}
	for _, p := range plans {
		d := p.decision
		d.PassID = ""
		if d.RunRequest != nil {
			request := *d.RunRequest
			request.CorrelationID = ""
			d.RunRequest = &request
		}
		answers = append(answers, d)
		counts[d.Action]++
	}
	raw, err := json.Marshal(answers)
	return string(raw), counts, err
}

func (l *Loop) observePass(timing *PassTiming, answers string) error {
	// The caller records only allocated generations. Equal planning answers do
	// not make an applied transition (such as another lease reap) a no-op.
	timing.EndMS = l.cfg.Now().UnixMilli()
	timing.OverBudget = timing.IsOverBudget()
	if err := recordPassTiming(context.Background(), l.cfg.Store, *timing); err != nil {
		return fmt.Errorf("recording pass %s: %w", timing.TickID, err)
	}
	l.previousAnswers, l.hasAnswers = answers, true
	return nil
}
