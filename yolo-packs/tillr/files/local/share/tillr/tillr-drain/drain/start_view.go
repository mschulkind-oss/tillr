package drain

import (
	"context"
	"database/sql"
	"maps"
	"reflect"
	"strings"
	"sync/atomic"
	"time"

	"github.com/mschulkind-oss/tillr-drain/faults"
	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/definition"
	"github.com/mschulkind-oss/tillr/store"
)

const startViewRefreshInterval = 15 * time.Second

type startQueueRow struct{ id, rank string }

// A snapshot and its collections are immutable after publication. The rows use
// planner order, but carry no decision about whether any item can start.
type startSnapshot struct {
	items    []startQueueRow
	occupied map[string]bool

	def                               definition.Definition
	definitionSHA                     string
	lastEventSeq                      int64
	trunkWaiters, defectWaiters       map[string]string
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

// watch seeds once and then refreshes on the timer, all off the dispatch path.
// It owns publication; a failed read leaves the last good snapshot in place.
// The returned function cancels and joins the watcher.
func (v *startView) watch(parent context.Context, interval time.Duration, read func(context.Context) (startSnapshot, error)) func() {
	ctx, cancel := context.WithCancel(parent)
	done := make(chan struct{})
	go func() {
		defer close(done)
		timer := time.NewTimer(interval)
		defer timer.Stop()
		for ctx.Err() == nil {
			next, err := read(ctx)
			if err == nil && ctx.Err() == nil {
				prev := v.current.Load()
				if !reflect.DeepEqual(prev, &next) {
					v.current.Store(&next)
					select {
					case v.updated <- struct{}{}:
					default:
					}
				}
			}
			timer.Reset(interval)
			select {
			case <-ctx.Done():
				return
			case <-timer.C:
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}

func readStartSnapshot(ctx context.Context, path string, at time.Time, policy *Policy) (startSnapshot, error) {
	s, err := store.OpenRead(ctx, path)
	if err != nil {
		return startSnapshot{}, err
	}
	defer s.Close()
	var next startSnapshot
	err = s.ReadTx(ctx, func(r *store.Reader) error { return next.seed(r, at, policy) })
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
	for _, row := range rows {
		if row.JudgeMode == "agent" {
			row.JudgeGrant, err = judgeGrant(r, row.ItemID, at)
			if err != nil {
				return err
			}
		}
		s.items = append(s.items, startQueueRow{id: row.ItemID, rank: row.Rank})
		s.itemsByID[row.ItemID], s.states[row.ItemID] = row, row.State
		if s.def.Completes == "landing" && row.State == s.def.Blocked {
			wait, _, err := item.ReadWaitingOn(r, row.ItemID)
			if err != nil {
				return err
			}
			if wait != nil {
				if id, ok := strings.CutPrefix(wait.Waiter, "item:"); ok {
					switch {
					case IsTrunkFixer(id):
						s.trunkWaiters[row.ItemID] = id
					case IsDefectItem(id):
						s.defectWaiters[row.ItemID] = id
					}
				}
			}
		}
	}
	if s.edges, err = readEdges(r); err != nil {
		return err
	}
	s.reverseEdges = map[string][]string{}
	// Iterate in queue order so a reseed of the same facts is identical.
	for _, row := range rows {
		for _, blocker := range s.edges[row.ItemID] {
			s.reverseEdges[blocker] = append(s.reverseEdges[blocker], row.ItemID)
		}
	}
	if s.occupied, err = readLanes(r, at); err != nil {
		return err
	}
	if s.history, err = readRunHistory(r); err != nil {
		return err
	}
	if s.priors, err = readPriorRuns(r); err != nil {
		return err
	}
	s.attemptPriors = make(map[string][]priorRun, len(s.priors))
	for id, runs := range s.priors {
		for _, prior := range runs {
			if !acquittedRefusal(s.history.refusals[prior.RunID]) {
				s.attemptPriors[id] = append(s.attemptPriors[id], prior)
			}
		}
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
	if s.pending, err = readPendingHistoryState(r); err != nil {
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
	s.attempts, s.nodeCounts = map[string]int{}, map[string]int{}
	for _, row := range rows {
		id := row.ItemID
		s.attempts[id], s.nodeCounts[id], err = readAttemptInputs(r, s.hasNodes, id, s.priors[id])
		if err != nil {
			return err
		}
	}
	if s.deskOwners, err = readDeskOwners(r, s.occupied); err != nil {
		return err
	}
	if s.forks, err = liveForks(r, at, s.pending.works); err != nil {
		return err
	}
	if s.shut, err = readQuotaShutBackendsFrom(r, at, policy, s.pending.byRun); err != nil {
		return err
	}
	if s.quotaShut, err = readQuotaOnlyShut(r, at, policy); err != nil {
		return err
	}
	return s.readExpiries(r, at)
}

func (s *startSnapshot) readExpiries(r *store.Reader, at time.Time) error {
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
