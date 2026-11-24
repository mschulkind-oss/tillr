package drain

import (
	"database/sql"
	"encoding/json"
	"errors"
	"maps"
	"path/filepath"
	"reflect"
	"sync"
	"sync/atomic"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// pendingEventsFolded counts how many events foldPendingHistory actually processed.
// Tests and benchmarks use it to verify incremental caching avoids re-folding
// unchanged history on every pass.
var pendingEventsFolded atomic.Int64

type pendingHistoryReadOnceKey struct{}
type pendingHistoryReadOnlyProbeKey struct{}

type pendingHistoryCacheEntry struct {
	mu            sync.Mutex
	lastEventHead int64
	lastPending   int64
	anchor        event.Event
	state         *pendingHistoryState
}

var pendingHistoryCaches sync.Map // cleaned absolute store path -> *pendingHistoryCacheEntry

func pendingHistoryCacheFor(path string) *pendingHistoryCacheEntry {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	entry, _ := pendingHistoryCaches.LoadOrStore(filepath.Clean(path), &pendingHistoryCacheEntry{})
	return entry.(*pendingHistoryCacheEntry)
}

// isReadOnlyReader reports whether r belongs to a read-only ReadTx (which caches
// ReadOnce values for the transaction) rather than a writable Tx.Reader() (which
// always invokes ReadOnce's loader).
func isReadOnlyReader(r *store.Reader) bool {
	calls := 0
	load := func() (any, error) {
		calls++
		return struct{}{}, nil
	}
	_, _ = r.ReadOnce(pendingHistoryReadOnlyProbeKey{}, load)
	_, _ = r.ReadOnce(pendingHistoryReadOnlyProbeKey{}, load)
	return calls < 2
}

// readerStorePath returns the file path of the main SQLite database backing r,
// or empty for in-memory / temporary databases.
func readerStorePath(r *store.Reader) (string, error) {
	rows, err := r.Query(`PRAGMA database_list`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return "", err
		}
		if name == "main" {
			return file, nil
		}
	}
	return "", rows.Err()
}

func samePendingAnchor(r *store.Reader, anchor event.Event) (bool, error) {
	var ts int64
	var kind, payload string
	err := r.QueryRow(`SELECT ts, kind, payload FROM events WHERE seq = ?`, anchor.Seq).Scan(&ts, &kind, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return ts == anchor.At.UnixMilli() && kind == anchor.Kind && payload == string(anchor.Payload), nil
}

// readPendingHistoryStateCached returns an immutable snapshot of the folded pending
// history for r. Callers must not mutate the returned maps or *pendingWork entries.
// Within a single ReadTx, repeated calls reuse the ReadOnce value with zero queries.
// Across transactions on the same store path, the committed prefix is extended
// incrementally from seq > lastEventHead using copy-on-write on touched work records.
func readPendingHistoryStateCached(r *store.Reader) (*pendingHistoryState, error) {
	readOnly := isReadOnlyReader(r)
	if readOnly {
		v, err := r.ReadOnce(pendingHistoryReadOnceKey{}, func() (any, error) {
			return loadPendingHistoryStateCached(r, true)
		})
		if err != nil {
			return nil, err
		}
		return v.(*pendingHistoryState), nil
	}
	return loadPendingHistoryStateCached(r, false)
}

func loadPendingHistoryStateCached(r *store.Reader, readOnly bool) (*pendingHistoryState, error) {
	path, err := readerStorePath(r)
	if err != nil {
		return nil, err
	}
	if path == "" {
		state := newPendingHistoryState()
		if err := foldPendingHistory(r, 0, state, nil); err != nil {
			return nil, err
		}
		return state, nil
	}
	entry := pendingHistoryCacheFor(path)
	entry.mu.Lock()
	lastEventHead := entry.lastEventHead
	lastPending := entry.lastPending
	anchor := entry.anchor
	cachedState := entry.state
	entry.mu.Unlock()

	eventHead, err := r.EventHeadSeq()
	if err != nil {
		return nil, err
	}
	replaced := false
	if cachedState != nil && lastPending > 0 && eventHead >= lastEventHead {
		same, err := samePendingAnchor(r, anchor)
		if err != nil {
			return nil, err
		}
		if same {
			if eventHead == lastEventHead {
				return cachedState, nil
			}
			meta := pendingFoldMeta{cow: true}
			if err := foldPendingHistoryWithMeta(r, lastEventHead, cachedState, nil, &meta); err != nil {
				return nil, err
			}
			if meta.count == 0 {
				if readOnly {
					entry.mu.Lock()
					if entry.state == cachedState && eventHead > entry.lastEventHead {
						entry.lastEventHead = eventHead
					}
					entry.mu.Unlock()
				}
				return cachedState, nil
			}
			if readOnly {
				entry.mu.Lock()
				if entry.state == cachedState || eventHead > entry.lastEventHead {
					entry.state = meta.state
					entry.lastEventHead = eventHead
					entry.lastPending = meta.last.Seq
					entry.anchor = meta.last
				}
				entry.mu.Unlock()
			}
			return meta.state, nil
		}
		replaced = true
	}

	state := newPendingHistoryState()
	meta := pendingFoldMeta{}
	if err := foldPendingHistoryWithMeta(r, 0, state, nil, &meta); err != nil {
		return nil, err
	}
	if readOnly {
		entry.mu.Lock()
		if meta.count > 0 && (replaced || entry.state == nil || eventHead > entry.lastEventHead) {
			entry.state = state
			entry.lastEventHead = eventHead
			entry.lastPending = meta.last.Seq
			entry.anchor = meta.last
		} else if replaced && meta.count == 0 {
			entry.state = nil
			entry.lastEventHead = 0
			entry.lastPending = 0
			entry.anchor = event.Event{}
		}
		entry.mu.Unlock()
	}
	return state, nil
}

// clonePendingHistoryForDelta returns a shallow copy of prev with only the
// *pendingWork entries that delta can mutate deep-cloned, so folding delta into
// the returned state leaves prev untouched for concurrent readers.
func clonePendingHistoryForDelta(prev *pendingHistoryState, delta []event.Event) *pendingHistoryState {
	next := &pendingHistoryState{
		works:     maps.Clone(prev.works),
		byRun:     maps.Clone(prev.byRun),
		judged:    maps.Clone(prev.judged),
		segments:  maps.Clone(prev.segments),
		discarded: maps.Clone(prev.discarded),
	}
	cloned := map[*pendingWork]*pendingWork{}
	cloneWork := func(w *pendingWork) {
		if w == nil {
			return
		}
		if _, ok := cloned[w]; ok {
			return
		}
		cp := cloneWorkValue(reflect.ValueOf(w)).Interface().(*pendingWork)
		cloned[w] = cp
	}
	for _, e := range delta {
		if e.ObjectID == nil {
			continue
		}
		switch e.Kind {
		case dispatchStartedKind, nodeAdvancedKind, graphChangedKind, "land.landing":
			// Creates a fresh pendingWork or updates judged only.
		case examinationReleasedKind:
			cloneWork(next.byRun[*e.ObjectID])
			for _, w := range next.works {
				if w.Ready && w.ParentRunID == *e.ObjectID {
					cloneWork(w)
				}
			}
		case BoundaryCheckKind:
			var check boundaryCheckPayload
			if json.Unmarshal(e.Payload, &check) == nil && !check.Clean {
				for _, ref := range check.Runs {
					cloneWork(next.byRun[ref.RunID])
				}
			}
		case itemFaultClearedKind:
			var fault ItemFault
			if json.Unmarshal(e.Payload, &fault) == nil {
				cloneWork(next.byRun[fault.RunID])
			}
		default:
			cloneWork(next.byRun[*e.ObjectID])
		}
	}
	if len(cloned) > 0 {
		for id, w := range next.works {
			if cp, ok := cloned[w]; ok {
				next.works[id] = cp
			}
		}
		for id, w := range next.byRun {
			if cp, ok := cloned[w]; ok {
				next.byRun[id] = cp
			}
		}
	}
	return next
}
