package gateland

import (
	"context"
	"sort"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr/store"
)

// LaneState holds the in-memory tracking state for one outside-pass
// coordinator (either Step 4 gate or Step 5 land). Callers guard mutations
// under the loop mutex.
type LaneState struct {
	Wake   chan struct{}
	Active bool
	Runs   map[string]bool
	Items  map[string]int
	Timer  func(time.Duration) (<-chan time.Time, func())
}

// Ensure initializes nil channels and maps on a zero-valued LaneState.
func (s *LaneState) Ensure() {
	if s.Wake == nil {
		s.Wake = make(chan struct{}, 1)
	}
	if s.Runs == nil {
		s.Runs = map[string]bool{}
	}
	if s.Items == nil {
		s.Items = map[string]int{}
	}
}

// MarkStart records (itemID, runID) as in flight on this coordinator.
func (s *LaneState) MarkStart(itemID, runID string) {
	s.Ensure()
	s.Runs[runID] = true
	s.Items[itemID]++
}

// MarkDone clears (itemID, runID) from this coordinator's in-flight tracking.
func (s *LaneState) MarkDone(itemID, runID string) {
	delete(s.Runs, runID)
	if n := s.Items[itemID]; n <= 1 {
		delete(s.Items, itemID)
	} else {
		s.Items[itemID] = n - 1
	}
}

// State groups both Step 4 (Gate) and Step 5 (Land) coordinator states so
// drain.Loop holds a single field for both coordinators.
type State struct {
	Gate LaneState
	Land LaneState
}

// NewState returns an initialized State with ready wake channels and maps.
func NewState() State {
	var st State
	st.Gate.Ensure()
	st.Land.Ensure()
	return st
}

// HasRun reports whether runID is currently tracked by either coordinator.
func (s *State) HasRun(runID string) bool {
	return s.Gate.Runs[runID] || s.Land.Runs[runID]
}

// HasItem reports whether itemID has an in-flight run on either coordinator.
func (s *State) HasItem(itemID string) bool {
	return s.Gate.Items[itemID] > 0 || s.Land.Items[itemID] > 0
}

// AnyRunning reports whether either coordinator has any in-flight runs.
func (s *State) AnyRunning() bool {
	return len(s.Gate.Runs) > 0 || len(s.Land.Runs) > 0
}

// RunCount returns the sum of tracked run map sizes across both coordinators
// (for capacity hints).
func (s *State) RunCount() int {
	return len(s.Gate.Runs) + len(s.Land.Runs)
}

// SignalWake performs a non-blocking send on ch if non-nil.
func SignalWake(ch chan struct{}) {
	if ch == nil {
		return
	}
	select {
	case ch <- struct{}{}:
	default:
	}
}

// RunLoop executes scan immediately, then repeats scan whenever wakeFn's
// channel fires or wait elapses, until ctx is canceled.
func RunLoop(
	ctx context.Context,
	wait time.Duration,
	timerFn func(time.Duration) (<-chan time.Time, func()),
	wakeFn func() <-chan struct{},
	scan func(),
) {
	scan()
	for ctx.Err() == nil {
		var timerC <-chan time.Time
		var stopTimer func()
		if timerFn != nil {
			timerC, stopTimer = timerFn(wait)
		} else {
			timer := time.NewTimer(wait)
			timerC, stopTimer = timer.C, func() { timer.Stop() }
		}

		var wake <-chan struct{}
		if wakeFn != nil {
			wake = wakeFn()
		}

		select {
		case <-ctx.Done():
			stopTimer()
			return
		case <-wake:
			stopTimer()
			scan()
		case <-timerC:
			stopTimer()
			scan()
		}
	}
}

// Eligibility carries the pure boolean and identifier fields needed to decide
// whether a pending candidate is eligible for Step 4 (gate) or Step 5 (land).
type Eligibility struct {
	Done                bool
	HasCandidate        bool
	Gated               bool
	HasDirtyHold        bool
	HasRedCapHold       bool
	HasRepairHold       bool
	LaneReleased        bool
	NeedsStage          bool
	ReopenedPausedJudge bool
	HasNoChange         bool
	HasExaminer         bool
	Examining           bool
	RunID               string
	LaneID              string
}

// GateEligible reports whether e describes an unclaimed, un-gated candidate
// ready for Step 4 (tillr-gate) on its own worktree.
func GateEligible(e Eligibility, wantRunID string) bool {
	if e.Done || !e.HasCandidate {
		return false
	}
	if e.Gated || e.HasDirtyHold || e.HasRedCapHold || e.HasRepairHold {
		return false
	}
	if e.LaneReleased || e.NeedsStage || e.ReopenedPausedJudge {
		return false
	}
	if e.HasNoChange {
		return false
	}
	if e.HasExaminer || e.Examining {
		return false
	}
	if e.RunID == "" || e.LaneID == "" {
		return false
	}
	if wantRunID != "" && e.RunID != wantRunID {
		return false
	}
	return true
}

// LandEligible reports whether e describes an unclaimed, already-gated
// candidate ready for Step 5 (tillr-land) on its own worktree.
func LandEligible(e Eligibility, wantRunID string) bool {
	if e.Done || !e.HasCandidate {
		return false
	}
	if !e.Gated && !e.HasDirtyHold && !e.HasRedCapHold {
		return false
	}
	if e.HasRepairHold {
		return false
	}
	if e.LaneReleased || e.NeedsStage || e.ReopenedPausedJudge {
		return false
	}
	if e.HasExaminer || e.Examining {
		return false
	}
	if e.RunID == "" || e.LaneID == "" {
		return false
	}
	if wantRunID != "" && e.RunID != wantRunID {
		return false
	}
	return true
}

// GateCandidate identifies a candidate ready for Step 4 gating.
type GateCandidate struct {
	ItemID string
	RunID  string
}

// SortGateCandidates sorts candidates deterministically by ItemID then RunID.
func SortGateCandidates(candidates []GateCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].ItemID != candidates[j].ItemID {
			return candidates[i].ItemID < candidates[j].ItemID
		}
		return candidates[i].RunID < candidates[j].RunID
	})
}

// LandCandidate identifies a gated candidate ready for FIFO Step 5 landing.
type LandCandidate struct {
	ItemID   string
	RunID    string
	GatedSeq int64
}

// SortLandCandidates orders gated candidates FIFO by GatedSeq, breaking ties
// by ItemID then RunID.
func SortLandCandidates(candidates []LandCandidate) {
	sort.Slice(candidates, func(i, j int) bool {
		if candidates[i].GatedSeq != candidates[j].GatedSeq {
			return candidates[i].GatedSeq < candidates[j].GatedSeq
		}
		if candidates[i].ItemID != candidates[j].ItemID {
			return candidates[i].ItemID < candidates[j].ItemID
		}
		return candidates[i].RunID < candidates[j].RunID
	})
}

// ReadItemStates reads item_id -> state for all items in the store.
func ReadItemStates(r *store.Reader) (map[string]string, error) {
	rows, err := r.Query(`SELECT item_id, state FROM items`)
	if err != nil {
		return nil, storelog.ReadFailure(err)
	}
	defer rows.Close()
	states := map[string]string{}
	for rows.Next() {
		var id, state string
		if err := rows.Scan(&id, &state); err != nil {
			return nil, storelog.ReadFailure(err)
		}
		states[id] = state
	}
	if err := rows.Err(); err != nil {
		return nil, storelog.ReadFailure(err)
	}
	return states, nil
}

// ReadGatedSeqs returns the latest event sequence number for each run with an
// event of the given kind (candidateGatedKind), used for FIFO landing order.
func ReadGatedSeqs(r *store.Reader, kind string) (map[string]int64, error) {
	events, err := r.EventsOfKind(0, kind)
	if err != nil {
		return nil, storelog.ReadFailure(err)
	}
	seqs := make(map[string]int64, len(events))
	for _, e := range events {
		if e.Kind == kind && e.ObjectType == "run" && e.ObjectID != nil {
			seqs[*e.ObjectID] = e.Seq
		}
	}
	return seqs, nil
}
