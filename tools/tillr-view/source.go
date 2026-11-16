package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The two sources a face can be given. The snapshot names which one a report
// describes, because the two carry different cursors: a store's events have a seq, a
// record stream's have only their position in the file.
const (
	sourceStore   = "store"
	sourceRecords = "records"
)

// snapshot is the moment a report describes. Two reports are comparable only if each
// says which moment it describes, so it is part of the record rather than a rendering
// detail (design §5.8).
//
// A record stream carries no store schema and no event seq, so in records mode
// schema_version and event_head_seq are 0 and record_count is the moment: the length
// of the stream the report was assembled from.
type snapshot struct {
	SchemaVersion int    `json:"schema_version"`
	EventHeadSeq  int64  `json:"event_head_seq"`
	Source        string `json:"source"`
	RecordCount   *int   `json:"record_count,omitempty"`
}

// eventRecord is one event as a report spells it. The substrate's event.Event carries
// no JSON tags, so marshalling it directly would write Go field names into an artifact
// a caller reads.
type eventRecord struct {
	Seq        int64           `json:"seq"`
	Kind       string          `json:"kind"`
	ObjectType string          `json:"object_type,omitempty"`
	ObjectID   *string         `json:"object_id,omitempty"`
	Revision   *int64          `json:"revision,omitempty"`
	Payload    json.RawMessage `json:"payload"`
	At         time.Time       `json:"at"`
}

// view is one consistent read of a source: the moment it describes, the events it
// holds, and, in records mode, the records themselves. Both faces assemble from a
// view, so the store path and the records path cannot drift.
type view struct {
	snapshot snapshot
	events   []eventRecord
	records  []record.Envelope
	// domain is the store's own rows, present when the source is a store: it is what
	// `status --store` assembles from, so the report's components are store rows rather
	// than null (VIEW-2).
	domain *storeDomain
}

// openStore reads the store in one read transaction, so the snapshot and the events
// describe the same moment. It opens read-only: a view that migrated the store it was
// asked to describe would answer a different question, and §5.8 requires it to be safe
// to point at the live store while writers run.
func openStore(ctx context.Context, path string, since int64) (view, error) {
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return view{}, err
	}
	defer s.Close()

	var v view
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		version, err := r.SchemaVersion()
		if err != nil {
			return err
		}
		if !schemaSupported(version) {
			return schemaMismatch{version: version}
		}
		head, err := r.EventHeadSeq()
		if err != nil {
			return err
		}
		events, err := r.Events(since)
		if err != nil {
			return err
		}
		domain, err := readStoreDomain(r)
		if err != nil {
			return err
		}
		v.snapshot = snapshot{SchemaVersion: version, EventHeadSeq: head, Source: sourceStore}
		v.events = eventRecords(events)
		v.domain = domain
		return nil
	})
	if err != nil {
		return view{}, err
	}
	return v, nil
}

// openRecords reads a JSON array of envelopes and assembles the same view with no
// store involved. A record's 1-based position in the file is its seq: the stream has
// no seq of its own, and a cursor is what the events face needs.
func openRecords(path string, since int64) (view, error) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return view{}, err
	}
	var raws []json.RawMessage
	if err := json.Unmarshal(raw, &raws); err != nil {
		return view{}, usageErrorf("%s is not a JSON array of records: %v", path, err)
	}
	records := make([]record.Envelope, 0, len(raws))
	for i, one := range raws {
		env, err := record.Decode(one)
		if err != nil {
			// An unknown record version is incompatible, not malformed: §5.1 gives it
			// the same exit as an unsupported store schema, because both mean "this
			// reader cannot read that".
			if errors.Is(err, record.ErrUnsupportedVersion) {
				return view{}, err
			}
			return view{}, usageErrorf("%s: record %d: %v", path, i+1, err)
		}
		records = append(records, env)
	}

	count := len(records)
	v := view{
		snapshot: snapshot{Source: sourceRecords, RecordCount: &count},
		records:  records,
	}
	for i, env := range records {
		seq := int64(i + 1)
		if seq <= since {
			continue
		}
		// The record's subject is the event's object, so a subject lookup answers the
		// same way over a stream as over a store.
		v.events = append(v.events, eventRecord{
			Seq:        seq,
			Kind:       env.Kind,
			ObjectType: subjectKind(env.Subject),
			ObjectID:   subjectID(env.Subject),
			Payload:    env.Payload,
			At:         env.CreatedAt.UTC(),
		})
	}
	return v, nil
}

// eventRecords is the report's spelling of a store's events.
func eventRecords(events []event.Event) []eventRecord {
	out := make([]eventRecord, 0, len(events))
	for _, e := range events {
		out = append(out, eventRecord{
			Seq:        e.Seq,
			Kind:       e.Kind,
			ObjectType: e.ObjectType,
			ObjectID:   e.ObjectID,
			Revision:   e.Revision,
			// The payload is carried verbatim: re-encoding a decoded value would route
			// its numbers through float64 and change them.
			Payload: e.Payload,
			At:      e.At.UTC(),
		})
	}
	return out
}

func subjectKind(s *record.Subject) string {
	if s == nil {
		return ""
	}
	return s.Kind
}

func subjectID(s *record.Subject) *string {
	if s == nil {
		return nil
	}
	id := s.ID
	return &id
}

// schemaMismatch is a store whose schema this binary does not support. It is a named
// error because §5.1 gives it its own exit code (3) and its own record: the refusal
// carries the version the store holds.
type schemaMismatch struct{ version int }

// schemaSupported answers whether a read-only face may read a store recorded at this
// version. Two ends are refused and the middle is read. Version 0 means no migration row at
// all: whatever file this is, it is not a store these utilities created, and reading it
// would answer "an empty board" for something that is not one. A store NEWER than this
// binary is refused because it may carry rows whose meaning this binary does not know, and
// a read-only face cannot migrate it forward to find out. A store OLDER than this binary is
// read: it is a subset of the columns this binary knows, and a query for one it lacks fails
// loudly at the query rather than answering something wrong. Measured 2026-11-13: while
// the check was `version != store.SchemaVersion` in two places, the observatory built from
// the working tree refused the live store the launched binaries own for as long as a
// relaunch was pending -- and fixing one of the two places left the other refusing.
func schemaSupported(version int) bool {
	return version > 0 && version <= store.SchemaVersion
}

func (e schemaMismatch) Error() string {
	return fmt.Sprintf("store schema version %d is not the %d this binary supports",
		e.version, store.SchemaVersion)
}

// usageError is a caller's mistake about the invocation or the input, which §5.1
// answers with exit 2 and no record.
type usageError struct{ err error }

func (e usageError) Error() string { return e.err.Error() }
func (e usageError) Unwrap() error { return e.err }

func usageErrorf(format string, args ...any) error {
	return usageError{err: fmt.Errorf(format, args...)}
}

// exitFor maps a source error to the exit code the contract gives it. A missing or
// unreadable path is the caller's mistake about the path, not a schema mismatch:
// answering 3 for a typo sends the caller looking for a migration that is not the
// problem.
func exitFor(err error) int {
	var usage usageError
	if errors.As(err, &usage) {
		return usagef("%v", err)
	}
	if errors.Is(err, fs.ErrNotExist) || errors.Is(err, fs.ErrPermission) {
		return usagef("%v", err)
	}
	return storeFailure(err)
}
