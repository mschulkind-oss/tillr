package storelog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// StoreBusyKind records that a drain write was refused as SQLITE_BUSY and the
// loop went on without it, appended on the next successful write.
const StoreBusyKind = "drain.store-busy"

func init() {
	if err := event.Register(StoreBusyKind, drainmodel.Utility); err != nil {
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

// AppendBusyFact appends a single StoreBusyFact event inside tx.
func AppendBusyFact(tx *store.Tx, sessionID string, fact StoreBusyFact, fallbackAt time.Time) error {
	if sessionID == "" {
		sessionID = "loop"
	}
	raw, err := json.Marshal(fact)
	if err != nil {
		return err
	}
	at, err := time.Parse(time.RFC3339Nano, fact.At)
	if err != nil {
		at = fallbackAt.UTC()
	}
	_, err = tx.Append(event.Event{
		Kind:       StoreBusyKind,
		ObjectType: "session",
		ObjectID:   &sessionID,
		At:         at,
		Payload:    raw,
	})
	return err
}

// AppendBusyFacts appends all pending StoreBusyFact events inside tx.
func AppendBusyFacts(tx *store.Tx, sessionID string, pending []StoreBusyFact, fallbackAt time.Time) error {
	for _, fact := range pending {
		if err := AppendBusyFact(tx, sessionID, fact, fallbackAt); err != nil {
			return err
		}
	}
	return nil
}

// WriteTxWithBusyFacts runs fn inside a store write transaction, flushing any
// queued busy facts in the same transaction and restoring them on error.
func WriteTxWithBusyFacts(
	ctx context.Context,
	s *store.Store,
	takePending func() []StoreBusyFact,
	restorePending func([]StoreBusyFact),
	sessionID func() string,
	now func() time.Time,
	fn func(*store.Tx) error,
) error {
	var pending []StoreBusyFact
	if takePending != nil {
		pending = takePending()
	}
	err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if err := fn(tx); err != nil {
			return err
		}
		if len(pending) == 0 {
			return nil
		}
		id := ""
		if sessionID != nil {
			id = sessionID()
		}
		fallback := time.Now().UTC()
		if now != nil {
			fallback = now().UTC()
		}
		return AppendBusyFacts(tx, id, pending, fallback)
	})
	if err != nil && len(pending) > 0 && restorePending != nil {
		restorePending(pending)
	}
	return err
}
