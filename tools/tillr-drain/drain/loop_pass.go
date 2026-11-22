package drain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// LoopPassKind records that the drain loop started a pass, throttled to at most once per
// loopPassInterval per loop process so tillr-watch can tell a silent stopped loop from an
// idle running one without writing an event on every empty pass.
const LoopPassKind = "drain.loop-pass"

func init() {
	if err := event.Register(LoopPassKind, Utility); err != nil {
		panic(err)
	}
}

// LoopPassFact is the payload of a LoopPassKind event.
type LoopPassFact struct {
	SessionID string `json:"session_id"`
}

const loopPassInterval = time.Minute

// recordLoopPass appends LoopPassKind before a pass when this loop has recorded none yet or
// at least loopPassInterval has elapsed since the last recorded pass. A failed write is
// logged and never stops or fails the pass.
func (l *Loop) recordLoopPass(ctx context.Context) {
	at := l.cfg.Now().UTC()
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
	body, err := json.Marshal(LoopPassFact{SessionID: id})
	if err != nil {
		if ctx.Err() == nil {
			l.reportf("loop: the loop pass was not recorded: %v\n", err)
		}
		return
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
