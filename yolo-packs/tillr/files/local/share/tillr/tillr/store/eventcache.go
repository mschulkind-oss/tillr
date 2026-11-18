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
type eventCache struct {
	mu     sync.Mutex
	events []event.Event
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
	c.mu.Lock()
	cached := c.events
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
			c.events = nil
			c.mu.Unlock()
			prefix, n = nil, 0
		}
	}
	from := since
	if n > 0 && prefix[n-1].Seq > from {
		from = prefix[n-1].Seq
	}
	tail, err := events(ctx, q, from)
	if err != nil {
		return nil, err
	}
	start := sort.Search(len(prefix), func(i int) bool { return prefix[i].Seq > since })
	out := make([]event.Event, 0, len(prefix)-start+len(tail))
	out = append(out, prefix[start:]...)
	out = append(out, tail...)
	// Only a read that began at the cache's end continues it: a read from a later since
	// skipped events, and a cache another read has extended meanwhile already holds these.
	if committed && len(tail) > 0 && from == lastSeq(prefix) {
		c.mu.Lock()
		if len(c.events) == n && lastSeq(c.events) == lastSeq(prefix) {
			c.events = append(c.events, tail...)
		}
		c.mu.Unlock()
	}
	return out, nil
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
