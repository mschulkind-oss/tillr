package drain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// StoreBusyKind records that a drain write was refused as SQLITE_BUSY and the
// loop went on without it, appended on the next successful write.
const StoreBusyKind = "drain.store-busy"

func init() {
	if err := event.Register(StoreBusyKind, Utility); err != nil {
		panic(err)
	}
}

// StoreBusyFact is the payload of a StoreBusyKind event.
type StoreBusyFact struct {
	Operation string `json:"operation"`
	WaitedMS  int64  `json:"waited_ms"`
	SessionID string `json:"session_id,omitempty"`
	At        string `json:"at"`
}

func (l *Loop) recordBusyWrite(operation string, waited time.Duration, at time.Time) {
	l.mu.Lock()
	sessionID := l.sessionID
	l.mu.Unlock()
	fact := StoreBusyFact{
		Operation: operation,
		WaitedMS:  waited.Milliseconds(),
		SessionID: sessionID,
		At:        at.UTC().Format(time.RFC3339Nano),
	}
	l.busyWritesMu.Lock()
	l.pendingBusyWrites = append(l.pendingBusyWrites, fact)
	l.busyWritesMu.Unlock()
}

func (l *Loop) writeTx(ctx context.Context, s *store.Store, fn func(*store.Tx) error) error {
	l.busyWritesMu.Lock()
	pending := l.pendingBusyWrites
	l.pendingBusyWrites = nil
	l.busyWritesMu.Unlock()

	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		l.mu.Lock()
		id := l.sessionID
		l.mu.Unlock()
		if id == "" {
			id = "loop"
		}
		for _, fact := range pending {
			raw, err := json.Marshal(fact)
			if err != nil {
				return err
			}
			at, err := time.Parse(time.RFC3339Nano, fact.At)
			if err != nil {
				at = l.cfg.Now().UTC()
			}
			if _, err := tx.Append(event.Event{
				Kind:       StoreBusyKind,
				ObjectType: "session",
				ObjectID:   &id,
				At:         at,
				Payload:    raw,
			}); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil && len(pending) > 0 {
		l.busyWritesMu.Lock()
		l.pendingBusyWrites = append(pending, l.pendingBusyWrites...)
		l.busyWritesMu.Unlock()
	}
	return err
}
