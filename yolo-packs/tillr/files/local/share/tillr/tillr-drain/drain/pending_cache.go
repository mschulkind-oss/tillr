package drain

import (
	"encoding/json"
	"maps"
	"path/filepath"
	"reflect"
	"slices"
	"sync"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

type pendingHistoryReadOnceKey struct{}

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
	base, err := loadFoldedPendingHistoryStateCached(r, readOnly)
	if err != nil {
		return nil, err
	}
	state, _, err := reopenPausedJudgeUnreadableSnapshot(r, base, false)
	if err != nil {
		return nil, err
	}
	return state, nil
}

func loadFoldedPendingHistoryStateCached(r *store.Reader, readOnly bool) (*pendingHistoryState, error) {
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
	byParent := make(map[string][]*pendingWork, len(prev.byParent))
	if prev.byParent != nil {
		for parent, list := range prev.byParent {
			byParent[parent] = slices.Clone(list)
		}
	} else {
		for _, w := range prev.works {
			if w != nil && w.Ready && w.ParentRunID != "" {
				byParent[w.ParentRunID] = append(byParent[w.ParentRunID], w)
			}
		}
	}
	next := &pendingHistoryState{
		works:     maps.Clone(prev.works),
		byRun:     maps.Clone(prev.byRun),
		byParent:  byParent,
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
		case dispatchStartedKind, nodeAdvancedKind, graphChangedKind:
			// Creates a fresh pendingWork.
		case "land.landing":
			var landing struct {
				RunID string `json:"run_id"`
			}
			if json.Unmarshal(e.Payload, &landing) == nil && landing.RunID != "" {
				cloneWork(next.byRun[landing.RunID])
			}
		case examinationReleasedKind:
			cloneWork(next.byRun[*e.ObjectID])
			for _, w := range next.byParent[*e.ObjectID] {
				if next.works[w.Decision.ItemID] == w && w.Ready && w.ParentRunID == *e.ObjectID {
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
		for _, list := range next.byParent {
			for i, w := range list {
				if cp, ok := cloned[w]; ok {
					list[i] = cp
				}
			}
		}
	}
	return next
}
