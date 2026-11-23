package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"sync"
	"sync/atomic"

	"github.com/mschulkind-oss/tillr/store"
)

// sharedAnswers computes one route's expensive answer once per store change and hands the
// same value to every request that arrives while nothing it depends on has moved. It is
// /why's cache (why.go) generalized to routes whose answer is a Go value: concurrent
// requests for one key share one build, and the answer is reused until the store or a file
// it was read from changes. There is no time-based expiry and no retry count: an answer is
// dropped only because an input it was built from is different.
//
// What an answer depends on, and how each input is kept honest:
//
//   - the store: the stamp is the event head and sqlite's data_version on the server's one
//     store connection. The head alone would miss a write that appends no event (a lane
//     lease, a heartbeat, a drain tick row); data_version moves on any commit by another
//     connection, which is the same signal the live channel's frames are made from.
//   - files the build read (lane logs, step and pipeline records, the loop's drain result
//     files): each is recorded as it was read, and the answer is reused only while a stat
//     of each shows the same size and modification time (or the same absence).
//   - the clock: not keyed. A build never stores an age or a clock comparison; the routes
//     that derive one recompute it per request on top of the shared answer (lanes.go's "log
//     grew N ago", the quota alarms, the loop session's liveness).
//   - process presence (a lane's run process being gone, which marks a run interrupted):
//     not keyed, because reading it replays the event log and walks /proc, which is the cost
//     being avoided. It is read when the answer is built, so a process that dies with no
//     store write after is shown at the next store change; the drain's heartbeat commits
//     within seconds, so that bound is short on a running loop.
//
// The data_version stamp is per connection, so the server's pooled store handle reads it on
// one dedicated connection, just before each answer's transaction begins (Reader.DataVersion):
// the answer is then at least as new as its stamp, and a stale hit is impossible.
type sharedAnswers struct {
	// builds counts the builder's runs, for the tests: sharing is only shown by it.
	builds atomic.Int64

	mu    sync.Mutex
	slots map[string]*sharedSlot
}

// sharedSlotCap bounds the distinct keys kept (a route keyed on a client-chosen query
// could otherwise grow without limit).
const sharedSlotCap = 64

type storeStamp struct {
	head        int64
	dataVersion int64
}

type sharedSlot struct {
	// mu serializes the build: a second request for the key waits here and then finds the
	// first one's answer, instead of building its own.
	mu    sync.Mutex
	built bool
	value any
	deps  []fileDep
	extra string
	stamp storeStamp
	// startedAfter is how many requests had arrived when the build began. A request whose
	// arrival number is no greater arrived before the build started, so the build read
	// every input after the request was made and is at least as fresh as the request asks.
	startedAfter int64
}

// arrivals numbers the requests to the shared routes in the order they reach the server,
// before they wait for the store's one connection. On a store that changes while a build
// runs, "the stamp is unchanged" alone would never hold for the requests that queued behind
// that build, because the store moved while they waited; "the build began after I
// arrived" is what they were owed.
var arrivals atomic.Int64

type arrivalKey struct{}

// arrival numbers a request on its way in and carries the number in its context.
func arrival(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, req *http.Request) {
		n := arrivals.Add(1)
		next(w, req.WithContext(context.WithValue(req.Context(), arrivalKey{}, n)))
	}
}

func arrivalOf(ctx context.Context) int64 {
	n, _ := ctx.Value(arrivalKey{}).(int64)
	return n
}

// readStoreStamp reads the stamp inside the request's own read transaction.
func readStoreStamp(r *store.Reader) (storeStamp, error) {
	var stamp storeStamp
	var err error
	if stamp.head, err = r.EventHeadSeq(); err != nil {
		return stamp, err
	}
	if stamp.dataVersion, err = r.DataVersion(); err != nil {
		return stamp, fmt.Errorf("reading the store's data_version: %w", err)
	}
	return stamp, nil
}

// fileDep is one file as it was when a build read it.
type fileDep struct {
	path    string
	present bool
	size    int64
	mtimeNS int64
}

func depOf(path string, info os.FileInfo) fileDep {
	return fileDep{path: path, present: true, size: info.Size(), mtimeNS: info.ModTime().UnixNano()}
}

func (d fileDep) unchanged() bool {
	info, err := os.Stat(d.path)
	if err != nil {
		return !d.present
	}
	return d.present && info.Size() == d.size && info.ModTime().UnixNano() == d.mtimeNS
}

// loopResultsStamp fingerprints the loop directory's drain result files: their names,
// sizes and modification times. The snapshot and the passes route read them by pattern, so
// a new file is a change as much as a rewritten one.
func loopResultsStamp(loopDir string) string {
	if loopDir == "" {
		return ""
	}
	matches, err := filepath.Glob(filepath.Join(loopDir, "drain-*.json"))
	if err != nil {
		return "glob-error"
	}
	sort.Strings(matches)
	h := sha256.New()
	for _, m := range matches {
		info, err := os.Stat(m)
		if err != nil {
			fmt.Fprintf(h, "%s absent\n", m)
			continue
		}
		fmt.Fprintf(h, "%s %d %d\n", m, info.Size(), info.ModTime().UnixNano())
	}
	return hex.EncodeToString(h.Sum(nil))
}

// shareBuild is what one build answers: the value, the files it read, whether it may be
// kept, or the route's failure.
type shareBuild struct {
	value    any
	deps     []fileDep
	uncached bool
	failure  *apiFailure
}

// get answers key from the shared value and builds it only when the value is not owed to
// this request. A value is reused when either
//
//   - its build began after this request arrived (it read every input after the request
//     was made, whatever has changed since), or
//   - the store stamp, the extra fingerprint and every recorded file are what they were
//     when it was built.
//
// extra is a cheap fingerprint of inputs outside the store that the caller can read
// without building (empty when there are none). A failure, and a build made for a request
// that was cancelled, are returned but never kept.
func (a *sharedAnswers) get(ctx context.Context, r *store.Reader, key string, extra func() string, build func() shareBuild) (any, *apiFailure) {
	stamp, err := readStoreStamp(r)
	if err != nil {
		// No stamp, no key: answer from a build that is not kept.
		a.builds.Add(1)
		out := build()
		return out.value, out.failure
	}
	a.mu.Lock()
	if a.slots == nil || len(a.slots) >= sharedSlotCap {
		a.slots = map[string]*sharedSlot{}
	}
	slot := a.slots[key]
	if slot == nil {
		slot = &sharedSlot{}
		a.slots[key] = slot
	}
	a.mu.Unlock()

	slot.mu.Lock()
	defer slot.mu.Unlock()
	if slot.built && arrivalOf(ctx) <= slot.startedAfter {
		return slot.value, nil
	}
	fingerprint := ""
	if extra != nil {
		fingerprint = extra()
	}
	if slot.built && slot.stamp == stamp && slot.extra == fingerprint && depsUnchanged(slot.deps) {
		return slot.value, nil
	}
	slot.built = false
	started := arrivals.Load()
	a.builds.Add(1)
	out := build()
	if out.failure != nil {
		return nil, out.failure
	}
	if out.uncached || ctx.Err() != nil {
		return out.value, nil
	}
	slot.built, slot.value, slot.deps, slot.extra, slot.stamp, slot.startedAfter = true, out.value, out.deps, fingerprint, stamp, started
	return out.value, nil
}

func depsUnchanged(deps []fileDep) bool {
	for _, d := range deps {
		if !d.unchanged() {
			return false
		}
	}
	return true
}

// laneFileMemo keeps what a lane file parsed to, keyed by the file and the stat it had
// when it was read, so a rebuild re-reads only the files that moved. A run's stream log
// grows while the run is live, but the lane page reads dozens of records and logs, almost
// all of them finished and still. Only the server path (stats not nil) uses it.
var laneFileMemo = struct {
	mu sync.Mutex
	m  map[string]laneMemoEntry
}{m: map[string]laneMemoEntry{}}

type laneMemoEntry struct {
	dep fileDep
	val any
}

const laneFileMemoCap = 4096

func memoLaneFile[T any](kind, path, extra string, stats *laneReadStats, compute func() T) T {
	if stats == nil {
		return compute()
	}
	dep := fileDep{path: path}
	if info, err := os.Stat(path); err == nil {
		dep = depOf(path, info)
	}
	key := kind + "\x00" + path + "\x00" + extra
	laneFileMemo.mu.Lock()
	entry, ok := laneFileMemo.m[key]
	laneFileMemo.mu.Unlock()
	if ok && entry.dep == dep {
		stats.dep(dep)
		return entry.val.(T)
	}
	// The stat above is older than the read inside compute, so a write between them leaves
	// the memo under a stamp the next lookup will not match: a miss, never a stale hit.
	val := compute()
	laneFileMemo.mu.Lock()
	if len(laneFileMemo.m) >= laneFileMemoCap {
		laneFileMemo.m = map[string]laneMemoEntry{}
	}
	laneFileMemo.m[key] = laneMemoEntry{dep: dep, val: val}
	laneFileMemo.mu.Unlock()
	return val
}
