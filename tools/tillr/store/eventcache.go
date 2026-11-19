package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/mschulkind-oss/tillr/event"
)

// The committed-event cache keeps, for one process, the event log it has already read, so
// a later Reader.Events reads only the events committed since: a read inside a write
// transaction is then a short tail read, not a scan of the whole history while every
// other writer waits.
//
// It exists because of the 2026-11-18 drain stops. Every application of a plan
// (applyPlanGeneration in tillr-drain) re-reads the pending runs by folding the whole
// event log -- 228,806 events that night -- two or three times inside its write
// transaction, so each one held the store's write lock for 2 to 3 seconds; a pass applies
// its plans back to back, and the loop's session heartbeat, waiting on SQLite's busy
// handler for the gap between them, waited out its 5-second busy timeout and stopped
// the loop.
//
// The log is append-only: no statement in tillr updates or deletes an event, and SQLite
// assigns each new seq under the write lock, so the committed events any transaction
// sees are a seq-ordered prefix of every later transaction's. That is what makes a cached
// prefix exact. The cache is extended only from a read-only transaction's reads, which
// see committed rows alone; a write transaction's tail can hold its own uncommitted
// events, which a rollback would leave the cache holding, so it reads through the cache
// without extending it. Each read also checks the newest cached event it uses against the
// row at that seq (time, kind and payload), so a store replaced at the same path (a recovery, a restore) is read
// afresh rather than answered from another file's history.
//
// It is off unless a program turns it on (CacheCommittedEvents): it holds the log in
// memory for the life of the process, which suits the long-lived drain loop that reads
// the log many times a second, and not a short CLI that reads it once.
//
// A selected read (Reader.EventsOfKind, Reader.EventsSelected) is answered from an index of
// the cached events by kind and by object type, so its cost is the events it answers, not the
// log: on 2026-11-19 the log held 1,294,976 events, 1,267,132 of them run.child-session
// worker records no fold reads, and a plan application that copied the whole cached log
// several times held the write lock about half a second, back to back with the next, until a
// landing's BEGIN waited out its busy timeout.
type eventCache struct {
	mu     sync.Mutex
	events []event.Event
	// index holds, per selection key (selectionKeys), the positions in events of the events
	// that key selects, for events[:indexed].
	index   map[string][]int
	indexed int
}

var (
	eventCacheEnabled atomic.Bool
	eventCaches       sync.Map // absolute store path -> *eventCache
	// eventRowsRead counts the event rows read from SQLite, cached or not: the test
	// seam that shows a read under a write lock is a tail read (export_test.go).
	eventRowsRead atomic.Int64
)

// CacheCommittedEvents turns on the committed-event cache for every store this process
// opens from now on. A long-lived program that reads the event log repeatedly calls it
// once at startup; see eventCache for what it keeps and why a cached read is exact.
func CacheCommittedEvents() { eventCacheEnabled.Store(true) }

// eventCacheFor answers the process's cache for the store at path, or nil when the cache
// is off.
func eventCacheFor(path string) *eventCache {
	if !eventCacheEnabled.Load() {
		return nil
	}
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	c, _ := eventCaches.LoadOrStore(filepath.Clean(path), &eventCache{})
	return c.(*eventCache)
}

// read answers the events with seq greater than since that the transaction q sees. The
// cached prefix the transaction can see is used as is; only the rows after it are read.
// committed says q is a read-only transaction, whose tail may extend the cache.
func (c *eventCache) read(ctx context.Context, q queryer, since int64, committed bool) ([]event.Event, error) {
	return c.readSelected(ctx, q, since, committed, nil)
}

// readSelected is read narrowed to the events sel selects, or every event when sel is nil.
// The cached part is taken from the index; the tail after it is read from SQLite, whole when
// the read may extend the cache, and narrowed by sel otherwise.
func (c *eventCache) readSelected(ctx context.Context, q queryer, since int64, committed bool, sel *EventSelection) ([]event.Event, error) {
	c.mu.Lock()
	cached := c.events
	var positions [][]int
	if sel != nil {
		c.extendIndex()
		for _, key := range sel.keys() {
			positions = append(positions, c.index[key])
		}
	}
	c.mu.Unlock()
	var head int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM events`).Scan(&head); err != nil {
		return nil, fmt.Errorf("store: reading the events: %w", err)
	}
	// An older snapshot than the cache's newest read sees only a prefix of it.
	n := sort.Search(len(cached), func(i int) bool { return cached[i].Seq > head })
	prefix := cached[:n]
	if n > 0 {
		same, err := sameEvent(ctx, q, prefix[n-1])
		if err != nil {
			return nil, err
		}
		if !same {
			c.mu.Lock()
			c.events, c.index, c.indexed = nil, nil, 0
			c.mu.Unlock()
			prefix, n, positions = nil, 0, nil
		}
	}
	from := since
	if n > 0 && prefix[n-1].Seq > from {
		from = prefix[n-1].Seq
	}
	var tail []event.Event
	var err error
	if sel == nil || committed {
		tail, err = events(ctx, q, from)
	} else {
		tail, err = selectedEvents(ctx, q, from, *sel)
	}
	if err != nil {
		return nil, err
	}
	start := sort.Search(len(prefix), func(i int) bool { return prefix[i].Seq > since })
	var out []event.Event
	if sel == nil {
		out = make([]event.Event, 0, len(prefix)-start+len(tail))
		out = append(out, prefix[start:]...)
		out = append(out, tail...)
	} else {
		out = selectPositions(prefix, start, positions)
		for _, e := range tail {
			if sel.selects(e) {
				out = append(out, e)
			}
		}
	}
	// Only a read that began at the cache's end continues it: a read from a later since
	// skipped events, and a cache another read has extended meanwhile already holds these.
	if committed && len(tail) > 0 && from == lastSeq(prefix) {
		c.mu.Lock()
		if len(c.events) == n && lastSeq(c.events) == lastSeq(prefix) {
			c.events = append(c.events, tail...)
			if c.index != nil {
				c.extendIndex()
			}
		}
		c.mu.Unlock()
	}
	return out, nil
}

// extendIndex indexes the cached events not indexed yet. The caller holds c.mu.
func (c *eventCache) extendIndex() {
	if c.index == nil {
		c.index, c.indexed = map[string][]int{}, 0
	}
	for i := c.indexed; i < len(c.events); i++ {
		e := c.events[i]
		c.index[kindKey(e.Kind)] = append(c.index[kindKey(e.Kind)], i)
		if e.ObjectType != "" {
			c.index[objectTypeKey(e.ObjectType)] = append(c.index[objectTypeKey(e.ObjectType)], i)
		}
	}
	c.indexed = len(c.events)
}

// selectPositions answers, in seq order and once each, the events of prefix at or after
// start whose positions one of the lists holds.
func selectPositions(prefix []event.Event, start int, positions [][]int) []event.Event {
	var picked []int
	for _, list := range positions {
		from := sort.SearchInts(list, start)
		for _, i := range list[from:] {
			if i >= len(prefix) {
				break
			}
			picked = append(picked, i)
		}
	}
	sort.Ints(picked)
	out := make([]event.Event, 0, len(picked))
	for j, i := range picked {
		if j > 0 && picked[j-1] == i {
			continue
		}
		out = append(out, prefix[i])
	}
	return out
}

func lastSeq(events []event.Event) int64 {
	if len(events) == 0 {
		return 0
	}
	return events[len(events)-1].Seq
}

// sameEvent answers whether the row at e's seq is still e -- its time, kind and payload:
// the check that the cached history is this store's. It is one primary-key read.
func sameEvent(ctx context.Context, q queryer, e event.Event) (bool, error) {
	var ts int64
	var kind, payload string
	err := q.QueryRowContext(ctx, `SELECT ts, kind, payload FROM events WHERE seq = ?`, e.Seq).Scan(&ts, &kind, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: reading the events: %w", err)
	}
	return ts == e.At.UnixMilli() && kind == e.Kind && payload == string(e.Payload), nil
}
