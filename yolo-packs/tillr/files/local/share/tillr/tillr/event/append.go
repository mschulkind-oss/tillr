package event

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"time"
)

// EventsDDL is the events table and its two append-only triggers, moved from the
// monolith's schema. The triggers are the invariant the append primitive relies on:
// an event is written once and never rewritten.
const EventsDDL = `
CREATE TABLE IF NOT EXISTS events (
  seq         INTEGER PRIMARY KEY AUTOINCREMENT,
  ts          INTEGER NOT NULL,
  kind        TEXT NOT NULL,
  -- Retained but never written: one writer, no reader, and who acted
  -- human-versus-agent lives in lane steer's --actor-kind.
  actor       TEXT,
  seat        TEXT,
  session_id  TEXT,
  object_type TEXT,
  object_id   TEXT,
  revision    INTEGER,
  payload     TEXT NOT NULL DEFAULT '{}'
);

CREATE TRIGGER IF NOT EXISTS events_no_update BEFORE UPDATE ON events
BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;

CREATE TRIGGER IF NOT EXISTS events_no_delete BEFORE DELETE ON events
BEGIN SELECT RAISE(ABORT, 'events are append-only'); END;

-- Every reader of one object's history, or of one kind's, finds its rows through these
-- instead of scanning the log. Without them, every worker's pause poll (pendingPause,
-- ten times a second) was a backward scan of the whole log; on 2026-11-18, ten workers
-- polling at once kept a WAL reader slot share-locked for minutes at a time, no
-- checkpoint could copy past that slot's mark, and the log never restarted.
CREATE INDEX IF NOT EXISTS events_by_object ON events(object_id, seq);
CREATE INDEX IF NOT EXISTS events_by_kind ON events(kind, seq);
`

// Event is one append-only event row. At is the injected clock: fixture and test
// modes pass it so a record is reproducible.
//
// Seq is the event's position in the log. Append ignores it -- the store assigns it,
// and a caller that could set it could write two events at one position or skip ahead
// -- and a read sets it. It is carried on the event rather than returned beside it
// because a reader's cursor is the last seq it saw, and an event without one cannot
// be resumed from.
//
// Payload is the kind-specific body, carried verbatim. It is a RawMessage rather than
// a decoded value because a forensic log must return what it was given: decoding into
// `any` routes every number through float64, so an integer above 2^53 comes back
// changed, silently, in the one artifact a reader trusts to say what happened.
type Event struct {
	Seq        int64
	Kind       string
	ObjectType string
	ObjectID   *string
	Revision   *int64
	Payload    json.RawMessage
	At         time.Time
}

// Append writes one event inside the caller's transaction, so the event and the
// state transition it describes commit together. An unregistered kind is refused:
// a kind the conformance suite cannot check is not recorded.
func Append(ctx context.Context, tx *sql.Tx, e Event) (int64, error) {
	if _, err := Lookup(e.Kind); err != nil {
		return 0, err
	}
	payload := "{}"
	if e.Payload != nil {
		// Marshalling a RawMessage validates it and compacts it, so a malformed body
		// is refused here rather than stored as text nothing can read back.
		encoded, err := json.Marshal(e.Payload)
		if err != nil {
			return 0, fmt.Errorf("event: encoding payload: %w", err)
		}
		payload = string(encoded)
	}
	var objectType any
	if e.ObjectType != "" {
		objectType = e.ObjectType
	}
	result, err := tx.ExecContext(ctx,
		`INSERT INTO events (ts, kind, object_type, object_id, revision, payload)
		 VALUES (?, ?, ?, ?, ?, ?)`,
		e.At.UnixMilli(), e.Kind, objectType, nullString(e.ObjectID), nullInt(e.Revision), payload,
	)
	if err != nil {
		return 0, fmt.Errorf("event: appending %q: %w", e.Kind, err)
	}
	return result.LastInsertId()
}

func nullString(value *string) any {
	if value == nil {
		return nil
	}
	return *value
}

func nullInt(value *int64) any {
	if value == nil {
		return nil
	}
	return *value
}
