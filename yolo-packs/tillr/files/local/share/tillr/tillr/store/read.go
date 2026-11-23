package store

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/event"
)

// queryer is the read surface a store query needs: *sql.DB for one query on its own,
// *sql.Tx for a set of them that must describe one moment.
type queryer interface {
	QueryContext(ctx context.Context, query string, args ...any) (*sql.Rows, error)
	QueryRowContext(ctx context.Context, query string, args ...any) *sql.Row
}

// Snapshot is one consistent read of a store: the events after a cursor, and the
// description of the store at the same moment. A report assembled from separate reads
// describes no moment at all -- a writer appending between the events read and the
// head read yields a head greater than the file's last seq, and a table count that
// disagrees with the events beside it.
type Snapshot struct {
	Events        []event.Event
	SchemaVersion int
	EventHeadSeq  int64
	TableCounts   map[string]int
	Integrity     string
	Fixture       bool
}

// ReadSnapshot reads the events after since and the store's description inside one
// read transaction, so the report describes the same moment as the events.
func (s *Store) ReadSnapshot(ctx context.Context, since int64) (Snapshot, error) {
	return s.snapshot(ctx, &since)
}

// Describe reads the store's description inside one read transaction, without the
// events. It is the report for a face whose record is the description alone.
func (s *Store) Describe(ctx context.Context) (Snapshot, error) {
	return s.snapshot(ctx, nil)
}

// Reader is one read transaction: every read it makes describes the same moment, so a
// report assembled from several of them is a snapshot rather than a set of unrelated
// answers.
type Reader struct {
	ctx        context.Context
	q          queryer
	readValues map[any]readValue
	// events is the process's committed-event cache for this store, nil when it is off
	// (eventcache.go). Only a read-only transaction's reads extend it: readValues is
	// non-nil exactly for those.
	events *eventCache
}

type readValue struct {
	value any
	err   error
}

// ReadOnce loads a value at most once per key in an immutable ReadTx. Keys must
// be comparable and values must not be mutated by callers. A Reader obtained
// from a writable Tx always reloads, because that transaction can change its rows.
// Errors are retained too: every consumer of one snapshot sees the same answer.
// Like Query, this method is used serially within the transaction's callback.
func (r *Reader) ReadOnce(key any, load func() (any, error)) (any, error) {
	if r.readValues == nil {
		return load()
	}
	if saved, ok := r.readValues[key]; ok {
		return saved.value, saved.err
	}
	value, err := load()
	r.readValues[key] = readValue{value: value, err: err}
	return value, err
}

// ReadTx runs fn inside one read transaction. A deferred transaction takes its read
// lock at the first read and holds it to the end, so every read fn makes sees the same
// state however a writer is scheduled.
//
// The read-only option is what keeps the begin deferred. A writable handle's connection
// carries _txlock=immediate (dsn), and the driver applies that mode to every transaction
// that is not read-only, so a read on a writable handle without the option would take the
// store's write lock and turn every reader into a writer's competitor. Every read path
// goes through here, directly or through snapshot.
func (s *Store) ReadTx(ctx context.Context, fn func(*Reader) error) error {
	return s.readTx(ctx, s.db, fn)
}

func (s *Store) readTx(ctx context.Context, db interface {
	BeginTx(context.Context, *sql.TxOptions) (*sql.Tx, error)
}, fn func(*Reader) error) error {
	tx, err := db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return fmt.Errorf("store: beginning the read transaction: %w", err)
	}
	defer tx.Rollback()
	return fn(&Reader{ctx: ctx, q: tx, readValues: map[any]readValue{}, events: s.events})
}

// Query runs one query inside the read transaction. It is the domain-row half of the
// read surface: a utility reads its own tables through it, so the utility depends on
// the substrate's transaction and not on the database handle.
func (r *Reader) Query(query string, args ...any) (*sql.Rows, error) {
	return r.q.QueryContext(r.ctx, query, args...)
}

// QueryRow runs one query inside the read transaction, answering at most one row.
func (r *Reader) QueryRow(query string, args ...any) *sql.Row {
	return r.q.QueryRowContext(r.ctx, query, args...)
}

// Events answers the events with seq greater than since, in seq order.
// With the committed-event cache on (CacheCommittedEvents), only the events committed
// since this process last read the log are read from the store.
//
// Events(0) is the whole log, a read whose cost grows with the log's whole history. Inside a
// write transaction every other writer waits on it, so it is counted and, with
// TILLR_SQLITE_TRACE=1, reported (reportWholeLogUnderWrite); outside one it is counted and
// reported apart (reportWholeLogRead), because a pass that folds the whole log repeatedly is
// slow even with no lock held. A fold that reads some kinds asks for those (EventsOfKind,
// EventsSelected).
func (r *Reader) Events(since int64) ([]event.Event, error) {
	var out []event.Event
	var err error
	if r.events != nil {
		out, err = r.events.read(r.ctx, r.q, since, r.readValues != nil)
	} else {
		out, err = events(r.ctx, r.q, since)
	}
	eventLogReads.Add(1)
	if err == nil && since == 0 {
		if r.readValues == nil {
			reportWholeLogUnderWrite(len(out))
		} else {
			reportWholeLogRead(len(out))
		}
	}
	return out, err
}

// EventSelection names the events a selected read answers: those whose kind is one of Kinds,
// and those whose object type is one of ObjectTypes.
type EventSelection struct {
	Kinds       []string
	ObjectTypes []string
}

func kindKey(kind string) string       { return "kind\x00" + kind }
func objectTypeKey(kind string) string { return "type\x00" + kind }

func (sel EventSelection) keys() []string {
	keys := make([]string, 0, len(sel.Kinds)+len(sel.ObjectTypes))
	for _, kind := range sel.Kinds {
		keys = append(keys, kindKey(kind))
	}
	for _, objectType := range sel.ObjectTypes {
		keys = append(keys, objectTypeKey(objectType))
	}
	return keys
}

func (sel EventSelection) selects(e event.Event) bool {
	for _, kind := range sel.Kinds {
		if e.Kind == kind {
			return true
		}
	}
	for _, objectType := range sel.ObjectTypes {
		if e.ObjectType == objectType {
			return true
		}
	}
	return false
}

// EventsSelected answers the events with seq greater than since that sel selects, in seq
// order: exactly the events Events(since) answers that sel selects. With the committed-event
// cache on, its cost is the events it answers rather than the log's length, which is what a
// fold inside a write transaction needs: the log is mostly worker records few folds read.
func (r *Reader) EventsSelected(since int64, sel EventSelection) ([]event.Event, error) {
	eventLogReads.Add(1)
	if len(sel.Kinds) == 0 && len(sel.ObjectTypes) == 0 {
		return nil, nil
	}
	if r.events != nil {
		return r.events.readSelected(r.ctx, r.q, since, r.readValues != nil, &sel)
	}
	if r.readValues != nil {
		return r.selectedOnce(since, sel)
	}
	return selectedEvents(r.ctx, r.q, since, sel)
}

type selectedKey struct {
	since int64
	key   string
}

// selectedOnce answers a selected read in a read-only snapshot, which cannot change, from
// one read per key (a kind or an object type) after since: a second fold that selects a
// kind the first already read does not read it again. The result is what selectedEvents
// answers: the union of the keys' events in seq order, each once. Like ReadOnce, the
// events are shared between callers and must not be mutated.
func (r *Reader) selectedOnce(since int64, sel EventSelection) ([]event.Event, error) {
	var parts [][]event.Event
	total := 0
	for _, kind := range sel.Kinds {
		part, err := r.selectedKey(since, kindKey(kind), EventSelection{Kinds: []string{kind}})
		if err != nil {
			return nil, err
		}
		parts, total = append(parts, part), total+len(part)
	}
	for _, objectType := range sel.ObjectTypes {
		part, err := r.selectedKey(since, objectTypeKey(objectType), EventSelection{ObjectTypes: []string{objectType}})
		if err != nil {
			return nil, err
		}
		parts, total = append(parts, part), total+len(part)
	}
	if len(parts) == 1 {
		return parts[0][:len(parts[0]):len(parts[0])], nil
	}
	out := make([]event.Event, 0, total)
	for _, part := range parts {
		out = append(out, part...)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seq < out[j].Seq })
	// An event selected by two keys (its kind and its object type) appears once.
	unique := out[:0]
	for i, e := range out {
		if i == 0 || e.Seq != out[i-1].Seq {
			unique = append(unique, e)
		}
	}
	return unique, nil
}

func (r *Reader) selectedKey(since int64, key string, sel EventSelection) ([]event.Event, error) {
	value, err := r.ReadOnce(selectedKey{since, key}, func() (any, error) {
		return selectedEvents(r.ctx, r.q, since, sel)
	})
	if err != nil {
		return nil, err
	}
	return value.([]event.Event), nil
}

// EventsOfKind is EventsSelected for the events of the named kinds.
func (r *Reader) EventsOfKind(since int64, kinds ...string) ([]event.Event, error) {
	return r.EventsSelected(since, EventSelection{Kinds: kinds})
}

// SchemaVersion answers the highest migration the store has applied.
func (r *Reader) SchemaVersion() (int, error) { return schemaVersion(r.ctx, r.q) }

// EventHeadSeq answers the highest event seq in the store.
func (r *Reader) EventHeadSeq() (int64, error) { return eventHeadSeq(r.ctx, r.q) }

// TableCounts answers the row count of every logical table in the store.
func (r *Reader) TableCounts() (map[string]int, error) { return tableCounts(r.ctx, r.q) }

// Integrity answers sqlite's integrity check.
func (r *Reader) Integrity() (string, error) { return integrity(r.ctx, r.q) }

// Fixture answers whether the store carries the fixture mark.
func (r *Reader) Fixture() (bool, error) { return fixture(r.ctx, r.q) }

// snapshot is the one read transaction both faces share.
func (s *Store) snapshot(ctx context.Context, since *int64) (Snapshot, error) {
	var snap Snapshot
	err := s.ReadTx(ctx, func(r *Reader) error {
		var err error
		if since != nil {
			if snap.Events, err = r.Events(*since); err != nil {
				return err
			}
		}
		if snap.SchemaVersion, err = r.SchemaVersion(); err != nil {
			return err
		}
		if snap.EventHeadSeq, err = r.EventHeadSeq(); err != nil {
			return err
		}
		if snap.TableCounts, err = r.TableCounts(); err != nil {
			return err
		}
		if snap.Integrity, err = r.Integrity(); err != nil {
			return err
		}
		if snap.Fixture, err = r.Fixture(); err != nil {
			return err
		}
		return nil
	})
	if err != nil {
		return Snapshot{}, err
	}
	return snap, nil
}

// TableCounts answers the row count of every table in the store, keyed by table name.
func (s *Store) TableCounts() (map[string]int, error) {
	return tableCounts(context.Background(), s.db)
}

// tableCounts answers the row count of every logical table in the store. sqlite's own
// bookkeeping tables are excluded: they are not store state, and a caller comparing
// two reports would see sqlite_sequence move for reasons the store did not choose. The
// monolith's rename-temp is excluded for the same reason -- it is a rebuild's scratch
// table, not a logical one, and a forensic check against a store that died mid-rebuild
// would otherwise report it as store state.
func tableCounts(ctx context.Context, q queryer) (map[string]int, error) {
	// `_` is a LIKE wildcard, so the prefix test escapes it: sqlite reserves the
	// "sqlite_" prefix, not the word, and a table named sqliteXtra is store state.
	rows, err := q.QueryContext(ctx,
		`SELECT name FROM sqlite_master
		  WHERE type = 'table'
		    AND name NOT LIKE 'sqlite\_%' ESCAPE '\'
		    AND name <> 'items_v4'
		  ORDER BY name`)
	if err != nil {
		return nil, fmt.Errorf("store: listing the tables: %w", err)
	}
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			rows.Close()
			return nil, fmt.Errorf("store: listing the tables: %w", err)
		}
		names = append(names, name)
	}
	if err := rows.Err(); err != nil {
		rows.Close()
		return nil, fmt.Errorf("store: listing the tables: %w", err)
	}
	rows.Close()

	counts := make(map[string]int, len(names))
	for _, name := range names {
		var count int
		// The name comes from sqlite_master, not from a caller, and it is quoted
		// because a table name is an identifier and cannot be a bound parameter.
		query := `SELECT COUNT(*) FROM "` + strings.ReplaceAll(name, `"`, `""`) + `"`
		if err := q.QueryRowContext(ctx, query).Scan(&count); err != nil {
			return nil, fmt.Errorf("store: counting %q: %w", name, err)
		}
		counts[name] = count
	}
	return counts, nil
}

// EventHeadSeq answers the highest event seq in the store, and 0 when it holds no
// events.
func (s *Store) EventHeadSeq() (int64, error) {
	return eventHeadSeq(context.Background(), s.db)
}

// eventHeadSeq answers the highest event seq in the store. "No events yet" is an
// answer rather than an error: a caller compares two snapshots by it, and a caller
// that cannot tell it from a failure cannot decide whether a report is current.
func eventHeadSeq(ctx context.Context, q queryer) (int64, error) {
	var head int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM events`).Scan(&head); err != nil {
		return 0, fmt.Errorf("store: reading the event head: %w", err)
	}
	return head, nil
}

// Integrity runs sqlite's integrity check and answers "ok" when the store is clean,
// otherwise the first row it reports -- which is the first failure, because sqlite
// emits "ok" alone on a clean store and one row per problem otherwise. A store that
// cannot be read at all is reported as an error rather than as a failure string,
// because the two need different handling: one is a damaged store, the other is a
// store this process could not open.
func (s *Store) Integrity() (string, error) {
	return integrity(context.Background(), s.db)
}

func integrity(ctx context.Context, q queryer) (string, error) {
	rows, err := q.QueryContext(ctx, `PRAGMA integrity_check`)
	if err != nil {
		return "", fmt.Errorf("store: checking integrity: %w", err)
	}
	defer rows.Close()
	if !rows.Next() {
		if err := rows.Err(); err != nil {
			return "", fmt.Errorf("store: checking integrity: %w", err)
		}
		return "", fmt.Errorf("store: integrity check returned no result")
	}
	var first string
	if err := rows.Scan(&first); err != nil {
		return "", fmt.Errorf("store: checking integrity: %w", err)
	}
	return first, nil
}

// Events answers the events with seq greater than since, in seq order.
func (s *Store) Events(ctx context.Context, since int64) ([]event.Event, error) {
	return events(ctx, s.db, since)
}

// events answers the events with seq greater than since, in seq order. since is
// exclusive, so a caller that passes the head it last saw reads only what is new.
func events(ctx context.Context, q queryer, since int64) ([]event.Event, error) {
	return queryEvents(ctx, q, `SELECT seq, ts, kind, object_type, object_id, revision, payload
		   FROM events WHERE seq > ? ORDER BY seq`, since)
}

// selectedEvents is events narrowed to what sel selects, in SQL: the kind index answers a
// kind, so the read does not visit the events it does not answer.
func selectedEvents(ctx context.Context, q queryer, since int64, sel EventSelection) ([]event.Event, error) {
	var terms []string
	args := []any{since}
	if len(sel.Kinds) > 0 {
		terms = append(terms, "kind IN ("+strings.TrimSuffix(strings.Repeat("?,", len(sel.Kinds)), ",")+")")
		for _, kind := range sel.Kinds {
			args = append(args, kind)
		}
	}
	if len(sel.ObjectTypes) > 0 {
		terms = append(terms, "object_type IN ("+strings.TrimSuffix(strings.Repeat("?,", len(sel.ObjectTypes)), ",")+")")
		for _, objectType := range sel.ObjectTypes {
			args = append(args, objectType)
		}
	}
	return queryEvents(ctx, q, `SELECT seq, ts, kind, object_type, object_id, revision, payload
		   FROM events WHERE seq > ? AND (`+strings.Join(terms, " OR ")+`) ORDER BY seq`, args...)
}

func queryEvents(ctx context.Context, q queryer, query string, args ...any) ([]event.Event, error) {
	rows, err := q.QueryContext(ctx, query, args...)
	if err != nil {
		return nil, fmt.Errorf("store: reading the events: %w", err)
	}
	defer rows.Close()

	var events []event.Event
	for rows.Next() {
		var (
			seq        int64
			ts         int64
			kind       string
			objectType sql.NullString
			objectID   sql.NullString
			revision   sql.NullInt64
			payload    string
		)
		if err := rows.Scan(&seq, &ts, &kind, &objectType, &objectID, &revision, &payload); err != nil {
			return nil, fmt.Errorf("store: reading the events: %w", err)
		}
		e := event.Event{
			Seq:     seq,
			Kind:    kind,
			Payload: json.RawMessage(payload),
			At:      time.UnixMilli(ts).UTC(),
		}
		if objectType.Valid {
			e.ObjectType = objectType.String
		}
		if objectID.Valid {
			id := objectID.String
			e.ObjectID = &id
		}
		if revision.Valid {
			rev := revision.Int64
			e.Revision = &rev
		}
		events = append(events, e)
		eventRowsRead.Add(1)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("store: reading the events: %w", err)
	}
	return events, nil
}

// fixture answers whether the store carries the fixture mark. It is separate from
// IsFixture because a report must not answer "not a fixture" for a store it could not
// read: a corrupted fixture would then report false beside its integrity failure.
func fixture(ctx context.Context, q queryer) (bool, error) {
	var value string
	err := q.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, fixtureKey).Scan(&value)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: reading the fixture mark: %w", err)
	}
	return value == "1", nil
}
