package store

import (
	"context"
	"fmt"
	"sort"
	"sync/atomic"

	"github.com/mschulkind-oss/tillr/event"
)

var narrowCacheEnabled atomic.Bool

// CacheSelectedEvents turns on the narrow committed-event cache for every store this process
// opens from now on. Unlike CacheCommittedEvents it never holds the whole log: it keeps only
// the events of the kinds and object types a selected read (EventsOfKind, EventsSelected) has
// asked for, each key read forward from where it last stopped, so a repeated read costs the
// events committed since, not the log's length. A whole-log read (Events) is not cached. It
// suits a long-lived reader of a log that is mostly records no fold reads, and that must not
// hold the log in memory: the observatory. The exactness argument is eventCache's: the log is
// append-only, so a key's events up to a seq are the same in every later transaction.
func CacheSelectedEvents() { narrowCacheEnabled.Store(true) }

// narrowEntry holds the events one selection key (a kind or an object type) selects, for
// every seq up to through.
type narrowEntry struct {
	events  []event.Event
	through int64
}

// readNarrow answers a selected read from the per-key cache: each key's cached events plus
// the rows committed after the key's last read. committed says q is a read-only transaction,
// whose tail may extend the cache.
func (c *eventCache) readNarrow(ctx context.Context, q queryer, since int64, committed bool, sel EventSelection) ([]event.Event, error) {
	var head int64
	if err := q.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) FROM events`).Scan(&head); err != nil {
		return nil, fmt.Errorf("store: reading the events: %w", err)
	}
	var picked []event.Event
	one := func(key string, single EventSelection) error {
		c.mu.Lock()
		if c.narrow == nil {
			c.narrow = map[string]*narrowEntry{}
		}
		ent := c.narrow[key]
		if ent == nil {
			ent = &narrowEntry{}
			c.narrow[key] = ent
		}
		cached, through := ent.events, ent.through
		c.mu.Unlock()
		// An older snapshot than the cache's newest read sees only a prefix of it.
		n := sort.Search(len(cached), func(i int) bool { return cached[i].Seq > head })
		prefix := cached[:n]
		if n > 0 {
			same, err := sameEvent(ctx, q, prefix[n-1])
			if err != nil {
				return err
			}
			if !same {
				// Another store at this path: start the key over.
				c.mu.Lock()
				ent.events, ent.through = nil, 0
				c.mu.Unlock()
				prefix, cached, through = nil, nil, 0
			}
		}
		var tail []event.Event
		if through < head {
			var err error
			if tail, err = selectedEvents(ctx, q, through, single); err != nil {
				return err
			}
			if committed {
				c.mu.Lock()
				if ent.through == through && len(ent.events) == len(cached) {
					ent.events = append(ent.events, tail...)
					ent.through = head
				}
				c.mu.Unlock()
			}
		}
		for _, e := range prefix {
			if e.Seq > since {
				picked = append(picked, e)
			}
		}
		for _, e := range tail {
			if e.Seq > since {
				picked = append(picked, e)
			}
		}
		return nil
	}
	for _, kind := range sel.Kinds {
		if err := one(kindKey(kind), EventSelection{Kinds: []string{kind}}); err != nil {
			return nil, err
		}
	}
	for _, objectType := range sel.ObjectTypes {
		if err := one(objectTypeKey(objectType), EventSelection{ObjectTypes: []string{objectType}}); err != nil {
			return nil, err
		}
	}
	sort.SliceStable(picked, func(i, j int) bool { return picked[i].Seq < picked[j].Seq })
	out := make([]event.Event, 0, len(picked))
	for i, e := range picked {
		if i > 0 && picked[i-1].Seq == e.Seq {
			continue
		}
		out = append(out, e)
	}
	return out, nil
}
