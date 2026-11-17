package drain

import (
	"context"
	"encoding/json"
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
		l.sessionID, l.sessionStarted = id, at.UnixMilli()
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
	s, err := store.Open(ctx, l.cfg.Store, !l.sessionInitialized)
	if err != nil {
		return Incompatiblef("%v", err)
	}
	defer s.Close()
	owner, err := currentLoopOwner()
	if err != nil {
		return err
	}
	raw, err := json.Marshal(owner)
	if err != nil {
		return err
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO drain_sessions(session_id, started_at, refreshed_at, next_pass_at, loop_owner)
   VALUES (?, ?, ?, ?, ?) ON CONFLICT(session_id) DO UPDATE SET
   refreshed_at = excluded.refreshed_at, next_pass_at = excluded.next_pass_at`,
			l.sessionID, l.sessionStarted, at.UnixMilli(), next.UnixMilli(), string(raw))
		return err
	})
	if err == nil {
		l.sessionInitialized = true
	}
	return err
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

// keepSessionAlive refreshes the same session while synchronous startup, staging,
// planning, or rolling work prevents the pass goroutine from reaching its next read.
// A failed heartbeat cancels that work and returns a fault, never false liveness.
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
				err := l.refreshSession(ctx, l.cfg.Now().Add(PassInterval))
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
