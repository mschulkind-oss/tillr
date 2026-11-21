package drain

import (
	"context"
	"database/sql"
	"maps"
	"reflect"
	"sync/atomic"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	startViewRefreshInterval = 15 * time.Second
	// One filesystem operation can emit several inotify records, and SQLite
	// emits writes before the transaction is visible. Batch without extending
	// the window on every write, so a busy store cannot starve refresh.
	startViewWatchBatch = 100 * time.Millisecond
)

type startQueueRow struct{ id, rank string }

// A snapshot and its collections are immutable after publication. Its
// proposals are advisory; the legacy pass alone decides what starts.
type startSnapshot struct {
	items      []startQueueRow
	occupied   map[string]bool
	storeLanes map[string]bool // raw leases, before overlaying pending work

	entries                         map[string]startViewEntry
	holds, reservations             map[string]string
	readyAt, laneFreeAt             map[string]time.Time
	freeLanes                       []string
	tip, green                      string
	unshared                        map[string]bool
	rewriteHolds, rewriteOwnerHolds map[string]string

	def                               definition.Definition
	definitionSHA                     string
	lastEventSeq                      int64
	trunkWaiters, defectWaiters       map[string]string
	waits                             map[string]item.WaitingOn
	itemsByID                         map[string]itemRow
	states                            map[string]string
	edges, reverseEdges               map[string][]string
	history                           runHistory
	priors, attemptPriors             map[string][]priorRun
	retries                           map[string]retryRow
	inputDecisions                    map[string]Decision
	escalated                         map[string]string
	versionSchema                     versionSchema
	contentVersions                   map[string]string
	pending                           *pendingHistoryState
	faultsByItemReason                map[string]map[string]ItemFault
	faults                            map[string]ItemFault
	conflicts                         map[string]item.Conflict
	hasNodes                          bool
	attempts, nodeCounts              map[string]int
	requests, shut, quotaShut         map[string]bool
	deskOwners                        map[string]string
	forks                             []plannedFork
	nextLeaseExpiry, nextWindowExpiry int64 // Unix milliseconds, zero when none.
}

type startView struct {
	current atomic.Pointer[startSnapshot]
	updated chan struct{} // Coalesced notification; nothing in dispatch waits on it.
}

func newStartView() *startView {
	return &startView{updated: make(chan struct{}, 1)}
}

func (v *startView) snapshot() startSnapshot {
	if current := v.current.Load(); current != nil {
		return *current
	}
	return startSnapshot{}
}

type startWatchChange uint8

const (
	startWatchStore startWatchChange = 1 << iota
	startWatchExternal
)

type startWatchPaths struct {
	root, store, index, gitDir, commonDir string
}

type startWatcher interface {
	events() <-chan startWatchChange
	updateDocs(map[string]itemRow)
	close()
}

// watch seeds once, then refreshes on filesystem wakes or the safety timer,
// all off the dispatch path. The returned function cancels and joins both readers.
func (v *startView) watch(parent context.Context, interval time.Duration, open func(context.Context) startWatcher, read func(context.Context, startWatchChange) (startSnapshot, error)) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		var w startWatcher
		if open != nil && ctx.Err() == nil {
			w = open(ctx)
		}
		if w != nil {
			defer w.close()
		}
		v.runWatcherLoop(ctx, interval, w, read)
	}()
	return func() {
		cancel()
		<-done
	}
}

func (v *startView) runWatcherLoop(ctx context.Context, interval time.Duration, w startWatcher, read func(context.Context, startWatchChange) (startSnapshot, error)) {
	var events <-chan startWatchChange
	if w != nil {
		events = w.events()
	}
	safety := time.NewTicker(interval)
	defer safety.Stop()
	batch := time.NewTimer(startViewWatchBatch)
	batch.Stop()
	defer batch.Stop()
	var batchDue <-chan time.Time
	var pending startWatchChange
	refresh := func() {
		change := pending
		pending, batchDue = 0, nil
		batch.Stop()
		if ctx.Err() != nil {
			return
		}
		next, err := read(ctx, change)
		previous := v.current.Load()
		if err == nil && ctx.Err() == nil && !reflect.DeepEqual(previous, &next) {
			if w != nil && (previous == nil || !reflect.DeepEqual(previous.itemsByID, next.itemsByID)) {
				w.updateDocs(next.itemsByID)
			}
			v.current.Store(&next)
			select {
			case v.updated <- struct{}{}:
			default:
			}
		}
	}
	refresh() // seed
	for ctx.Err() == nil {
		select {
		case <-ctx.Done():
			return
		case <-safety.C:
			pending |= startWatchExternal // Recheck even when a store wake is pending.
			refresh()
		case <-batchDue:
			refresh()
		case event, ok := <-events:
			if !ok {
				events = nil // Keep polling after a watcher failure.
				continue
			}
			pending |= event
			if batchDue == nil {
				batch.Reset(startViewWatchBatch)
				batchDue = batch.C
			}
		}
	}
}

func readStartSnapshot(ctx context.Context, path string, at time.Time, policy *Policy) (startSnapshot, error) {
	return readStartSnapshotWith(ctx, path, at, policy, nil)
}

func readStartSnapshotWith(ctx context.Context, path string, at time.Time, policy *Policy, rebuild func(*store.Reader, *startSnapshot) error) (startSnapshot, error) {
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return startSnapshot{}, err
	}
	defer s.Close()
	var next startSnapshot
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		if err := next.seed(r, at, policy); err != nil {
			return err
		}
		if rebuild != nil {
			return rebuild(r, &next)
		}
		return nil
	})
	if err != nil {
		return startSnapshot{}, err
	}
	return next, nil
}

// seed gathers the store-side inputs at one read boundary. It does not evaluate
// a start, read Git or documents, or feed the legacy pass.
func (s *startSnapshot) seed(r *store.Reader, at time.Time, policy *Policy) error {
	declared, err := definition.ForReader(r)
	if err != nil {
		return err
	}
	s.def, s.definitionSHA = declared.Parsed, declared.SHA256
	if s.lastEventSeq, err = r.EventHeadSeq(); err != nil {
		return err
	}
	rows, err := readItems(r)
	if err != nil {
		return err
	}
	s.itemsByID = make(map[string]itemRow, len(rows))
	s.states = make(map[string]string, len(rows))
	s.trunkWaiters, s.defectWaiters = map[string]string{}, map[string]string{}
	s.waits = map[string]item.WaitingOn{}
	for _, row := range rows {
		if row.JudgeMode == "agent" {
			row.JudgeGrant, err = judgeGrant(r, row.ItemID, at)
			if err != nil {
				return err
			}
		}
		s.items = append(s.items, startQueueRow{id: row.ItemID, rank: row.Rank})
		s.itemsByID[row.ItemID], s.states[row.ItemID] = row, row.State
		if err := s.refreshWait(r, row.ItemID, row.State); err != nil {
			return err
		}
	}
	if s.edges, err = readEdges(r); err != nil {
		return err
	}
	s.rebuildReverseEdges()
	if s.pending, err = readPendingHistoryState(r); err != nil {
		return err
	}
	if err := s.refreshRunFacts(r, true, true); err != nil {
		return err
	}
	if s.retries, err = readRetries(r); err != nil {
		return err
	}
	if s.inputDecisions, err = readInputDecisions(r); err != nil {
		return err
	}
	escalations, err := readEscalations(r)
	if err != nil {
		return err
	}
	s.escalated = make(map[string]string, len(escalations))
	for id, list := range escalations {
		s.escalated[id] = escalatedBackend(list)
	}
	if s.versionSchema, err = readVersionSchema(r); err != nil {
		return err
	}
	if s.contentVersions, err = readAllItemContentVersions(r, s.versionSchema); err != nil {
		return err
	}
	s.faultsByItemReason = map[string]map[string]ItemFault{}
	if err := faults.Fold(r, 0, s.faultsByItemReason, nil); err != nil {
		return err
	}
	s.faults = faults.Resolve(s.faultsByItemReason)
	if s.conflicts, err = item.ReadConflicts(r); err != nil {
		return err
	}
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='node_runs')`).Scan(&s.hasNodes); err != nil {
		return err
	}
	if s.requests, err = readRequestIDs(r); err != nil {
		return err
	}
	if err := s.refreshAttemptOrdinals(r); err != nil {
		return err
	}
	if err := s.refreshLaneFacts(r, at, true); err != nil {
		return err
	}
	if err := s.refreshBackends(r, at, policy); err != nil {
		return err
	}
	return s.readExpiries(r, at)
}

func (s *startSnapshot) refreshAttemptOrdinals(r *store.Reader) error {
	s.attempts, s.nodeCounts = map[string]int{}, map[string]int{}
	for _, row := range s.items {
		if err := s.refreshAttempt(r, row.id); err != nil {
			return err
		}
	}
	return nil
}

func (s *startSnapshot) refreshRunFacts(r *store.Reader, history, priors bool) error {
	var err error
	if history {
		if s.history, err = readRunHistory(r); err != nil {
			return err
		}
	}
	if priors {
		if s.priors, err = readPriorRunsFrom(r, s.pending.byRun); err != nil {
			return err
		}
	}
	s.attemptPriors = make(map[string][]priorRun, len(s.priors))
	for id, runs := range s.priors {
		for _, prior := range runs {
			if !acquittedRefusal(s.history.refusals[prior.RunID]) {
				s.attemptPriors[id] = append(s.attemptPriors[id], prior)
			}
		}
	}
	return nil
}

func (s *startSnapshot) refreshLaneFacts(r *store.Reader, at time.Time, leases bool) error {
	var err error
	if leases {
		if s.storeLanes, err = readLanes(r, at); err != nil {
			return err
		}
		if s.deskOwners, err = readDeskOwners(r, s.storeLanes); err != nil {
			return err
		}
	}
	s.occupied = s.storeLanes
	s.forks, err = liveForks(r, at, s.pending.works)
	return err
}

func (s *startSnapshot) refreshBackends(r *store.Reader, at time.Time, policy *Policy) error {
	var err error
	if s.shut, err = readQuotaShutBackendsFrom(r, at, policy, s.pending.byRun); err != nil {
		return err
	}
	s.quotaShut, err = readQuotaOnlyShut(r, at, policy)
	return err
}

func (s *startSnapshot) readExpiries(r *store.Reader, at time.Time) error {
	s.nextWindowExpiry = 0
	// Reuse the window reader's compatibility with stores predating windows.
	windows, err := readBackendWindows(r, at)
	if err != nil {
		return err
	}
	for _, expiry := range windows {
		if ms := expiry.UnixMilli(); s.nextWindowExpiry == 0 || ms < s.nextWindowExpiry {
			s.nextWindowExpiry = ms
		}
	}
	var lease sql.NullInt64
	if err := r.QueryRow(`SELECT MIN(leased_until) FROM lanes WHERE state=? AND leased_until>?`, LaneStateLeased, at.UnixMilli()).Scan(&lease); err != nil {
		return err
	}
	s.nextLeaseExpiry = lease.Int64
	return nil
}

// Clone the fold's work records while preserving the aliases between indexes.
// Replay of an assignment decodes into existing work, including nested fields.
func clonePendingHistoryState(s *pendingHistoryState) *pendingHistoryState {
	if s == nil {
		return newPendingHistoryState()
	}
	out := &pendingHistoryState{
		works: make(map[string]*pendingWork, len(s.works)), byRun: make(map[string]*pendingWork, len(s.byRun)),
		judged: maps.Clone(s.judged), segments: maps.Clone(s.segments), discarded: maps.Clone(s.discarded),
	}
	seen := make(map[*pendingWork]*pendingWork, len(s.byRun))
	clone := func(w *pendingWork) *pendingWork {
		if w == nil {
			return nil
		}
		if copy := seen[w]; copy != nil {
			return copy
		}
		copy := cloneWorkValue(reflect.ValueOf(w)).Interface().(*pendingWork)
		seen[w] = copy
		return copy
	}
	for id, w := range s.works {
		out.works[id] = clone(w)
	}
	for id, w := range s.byRun {
		out.byRun[id] = clone(w)
	}
	return out
}

// Pending work records are acyclic value trees. Copy their exported fields
// without a JSON round trip, which would lose replay-only fields tagged json:"-".
func cloneWorkValue(v reflect.Value) reflect.Value {
	switch v.Kind() {
	case reflect.Interface:
		if !v.IsNil() {
			out := reflect.New(v.Type()).Elem()
			out.Set(cloneWorkValue(v.Elem()))
			return out
		}
	case reflect.Pointer:
		if !v.IsNil() {
			out := reflect.New(v.Type().Elem())
			out.Elem().Set(cloneWorkValue(v.Elem()))
			return out
		}
	case reflect.Struct:
		out := reflect.New(v.Type()).Elem()
		out.Set(v)
		for i := 0; i < out.NumField(); i++ {
			if out.Field(i).CanSet() {
				out.Field(i).Set(cloneWorkValue(v.Field(i)))
			}
		}
		return out
	case reflect.Slice:
		if !v.IsNil() {
			out := reflect.MakeSlice(v.Type(), v.Len(), v.Len())
			for i := 0; i < v.Len(); i++ {
				out.Index(i).Set(cloneWorkValue(v.Index(i)))
			}
			return out
		}
	case reflect.Map:
		if !v.IsNil() {
			out := reflect.MakeMapWithSize(v.Type(), v.Len())
			for it := v.MapRange(); it.Next(); {
				out.SetMapIndex(it.Key(), cloneWorkValue(it.Value()))
			}
			return out
		}
	}
	return v
}
