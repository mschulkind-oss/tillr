package drain

import (
	"context"
	"maps"
	"reflect"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// A note changes only the published picture, not the watcher's store cursor
// or the legacy planner. Every changed collection is copied before publication.
type startNote func(*startSnapshot)

func (v *startView) publishLocked(next *startSnapshot) {
	v.current.Store(next)
	v.notifyUpdate()
}

func (v *startView) notifyUpdate() {
	select {
	case v.updated <- struct{}{}:
	default:
	}
}

func (v *startView) noteLocked(note startNote) {
	if v.syncing {
		v.journal = append(v.journal, note)
	}
	if cur := v.current.Load(); cur != nil {
		next := *cur
		note(&next)
		v.publishLocked(&next)
	}
}

func withNote[K comparable, V any](m map[K]V, key K, value V) map[K]V {
	next := make(map[K]V, len(m)+1)
	maps.Copy(next, m)
	next[key] = value
	return next
}

func withoutNote[K comparable, V any](m map[K]V, key K) map[K]V {
	if _, ok := m[key]; !ok {
		return m
	}
	next := maps.Clone(m)
	delete(next, key)
	return next
}

func (s *startSnapshot) holdItem(id, reason string) {
	s.holds = withNote(s.holds, id, reason)
	s.readyAt = withoutNote(s.readyAt, id)
	if entry, ok := s.entries[id]; ok {
		if lane := entry.decision.LaneID; s.reservations[lane] == id {
			s.reservations = withoutNote(s.reservations, lane)
		}
		entry.decision = Decision{ItemID: id, Action: ActionHold, Reason: reason}
		s.entries = withNote(s.entries, id, entry)
	}
}

// Comment availability is external, not a content-bound loop hold. Let the
// next watcher refresh reconsider it even if the item did not change.
func (v *startView) noteVantageUnavailable(before *startSnapshot, id string) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.current.Load() == before {
		next := *before
		next.holdItem(id, ReasonVantageUnavailable)
		v.publishLocked(&next)
	}
}

func (v *startView) noteItemHold(id string, hold heldItem) {
	v.mu.Lock()
	defer v.mu.Unlock()
	if v.holds == nil {
		v.holds = map[string]heldItem{}
	}
	v.holds[id] = hold
	v.noteLocked(func(s *startSnapshot) { s.applyLocalHold(id, hold) })
}

func (s *startSnapshot) applyLocalHold(id string, hold heldItem) {
	if version := s.contentVersions[id]; version == "" || hold.contentVersion == "" || version == hold.contentVersion {
		s.holdItem(id, hold.reason)
	}
}

func (v *startView) noteStartClaimed(d Decision, work *pendingWork) {
	if d.RunRequest == nil || d.LaneID == "" {
		return
	}
	id, lane, runID := d.ItemID, d.LaneID, d.RunRequest.RunID
	if work == nil {
		work = &pendingWork{Decision: d}
	}
	// The pass can still change the work it is staging. The snapshot owns a copy.
	claimed := cloneWorkValue(reflect.ValueOf(work)).Interface().(*pendingWork)
	claimed.Optimistic = true
	v.mu.Lock()
	defer v.mu.Unlock()
	oldRun := v.snapshot().storeRuns[lane]
	v.noteLocked(func(s *startSnapshot) {
		if other, ok := s.storeRuns[lane]; ok && other != oldRun && other != runID {
			return // The read already sees a different occupant.
		}
		if s.pending != nil && s.pending.byRun[runID] != nil {
			return // The store has already folded this claim and any later progress.
		}
		s.storeRuns = withNote(s.storeRuns, lane, runID)
		s.occupied = withNote(s.occupied, lane, true)
		if !isExpressLane(lane) && !strings.HasPrefix(lane, "desk-") && !s.itemsByID[id].Express {
			for _, node := range parallelNodes(claimed.Decision) {
				if node.RunRequest != nil && !isCommandDecision(node) {
					s.activeOrdinaryAgents = withNote(s.activeOrdinaryAgents, node.RunRequest.RunID, lane)
				}
			}
		}
		s.setStartWork(claimed, nil)
		s.holdItem(id, ReasonRunLive)
		s.offerLanes(s.rotation, time.Time{})
		// Desk lanes may have ready timestamps without being in rotation.
		s.laneFreeAt = withoutNote(s.laneFreeAt, lane)
	})
}

// Update both pending indexes without mutating the watcher's fold or any
// snapshot a reader may still hold. Only the changed work needs copying.
func (s *startSnapshot) setStartWork(work, replaced *pendingWork) {
	p := s.pending
	if p == nil {
		p = newPendingHistoryState()
	}
	next := *p
	next.works = withNote(p.works, work.Decision.ItemID, work)
	next.byRun = make(map[string]*pendingWork, len(p.byRun)+1)
	maps.Copy(next.byRun, p.byRun)
	for _, node := range parallelNodes(work.Decision) {
		if node.RunRequest != nil {
			id := node.RunRequest.RunID
			if replaced == nil || next.byRun[id] == replaced {
				next.byRun[id] = work
			}
		}
	}
	s.pending = &next
}

func workRunID(w *pendingWork) string {
	if w.Decision.RunRequest == nil {
		return ""
	}
	return w.Decision.RunRequest.RunID
}

func (v *startView) noteLaneReleased(lane, runID string, detached bool, at time.Time) {
	if lane == "" || runID == "" {
		return
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	v.noteLocked(func(s *startSnapshot) {
		s.releaseLane(lane, runID, detached)
		s.offerLanes(s.rotation, at)
	})
}

func (s *startSnapshot) releaseLane(lane, runID string, detached bool) {
	if other, ok := s.storeRuns[lane]; ok && other != runID {
		return
	}
	s.storeRuns = withoutNote(s.storeRuns, lane)
	stillHeld := false
	keepContinuing := false
	if s.pending != nil {
		for _, w := range s.pending.works {
			if w.Decision.LaneID != lane || w.Done || w.LaneReleased {
				continue
			}
			if (detached || w.Optimistic) && workRunID(w) == runID {
				cp := *w
				cp.LaneReleased = true
				s.setStartWork(&cp, w)
			} else {
				stillHeld = true // A lease release alone does not dispose of a candidate.
				if !detached && w.Candidate == nil && !w.BoundaryFailed && w.Decision.Workflow != nil && (w.Ready || workflowHasOutgoingEdge(w.Decision)) {
					keepContinuing = true
				}
			}
		}
	}
	if !stillHeld {
		s.occupied = withoutNote(s.occupied, lane)
	}
	if !keepContinuing {
		for key, activeLane := range s.activeOrdinaryAgents {
			if activeLane == lane {
				s.activeOrdinaryAgents = withoutNote(s.activeOrdinaryAgents, key)
			}
		}
	}
}

func (s *startSnapshot) offerLanes(rotation []string, at time.Time) {
	free := make([]string, 0, len(rotation))
	times := maps.Clone(s.laneFreeAt)
	if times == nil {
		times = map[string]time.Time{}
	}
	for _, lane := range s.rotation {
		delete(times, lane)
	}
	for _, lane := range rotation {
		if !s.occupied[lane] {
			free = append(free, lane)
			times[lane] = since(s.laneFreeAt[lane], at)
		}
	}
	s.rotation, s.freeLanes, s.laneFreeAt = rotation, free, times
}

func (l *Loop) notifyStartLaneReleased(lane, runID string, detached bool, at time.Time) {
	if v := l.startView.Load(); v != nil {
		v.noteLaneReleased(lane, runID, detached, at)
	}
}

// The reaper does not name one release. Read the leases without the view lock,
// and only use that read if no newer publication overtook it. Replay removals
// by run identity, never by copying occupancy over an in-flight refresh.
func (l *Loop) reconcileStartLanes(ctx context.Context, rotation []string, at time.Time) {
	v := l.startView.Load()
	if v == nil {
		return
	}
	before := v.current.Load()
	var leases map[string]string
	var folded *pendingHistoryState
	if before != nil {
		if reader, err := store.OpenRead(ctx, l.cfg.Store); err == nil {
			defer reader.Close()
			if err := reader.ReadTx(ctx, func(r *store.Reader) error {
				if _, err := readLanesWithRuns(r, at, &leases); err != nil {
					return err
				}
				if before.pending != nil && rotation == nil {
					folded = clonePendingHistoryState(before.pending)
					if err := foldPendingHistory(r, before.lastEventSeq, folded, nil); err != nil {
						return err
					}
					return reopenPausedJudgeUnreadable(r, folded)
				}
				return nil
			}); err != nil {
				leases, folded = nil, nil
			}
		}
	}
	v.mu.Lock()
	defer v.mu.Unlock()
	released := map[string]string{}
	detached := map[string]string{}
	if leases != nil && v.current.Load() == before {
		for lane, id := range before.storeRuns {
			if _, stillLeased := leases[lane]; !stillLeased {
				released[lane] = id
			}
		}
		if folded != nil {
			for _, w := range before.pending.works {
				lane, id := w.Decision.LaneID, workRunID(w)
				if _, stillLeased := leases[lane]; stillLeased || w.Done || w.LaneReleased {
					continue
				}
				if fw := folded.byRun[id]; fw != nil && (fw.Done || fw.LaneReleased) {
					detached[lane] = id
				}
			}
		}
	}
	if rotation != nil {
		v.rotation = rotation
	}
	v.noteLocked(func(s *startSnapshot) {
		for lane, id := range released {
			s.releaseLane(lane, id, false)
		}
		for lane, id := range detached {
			s.releaseLane(lane, id, true)
		}
		offers := s.rotation
		if rotation != nil {
			offers = rotation
		}
		s.offerLanes(offers, at)
	})
}

func (l *Loop) noteStartRotation(at time.Time) {
	if v := l.startView.Load(); v != nil {
		rotation := l.rotationLanes()
		v.mu.Lock()
		defer v.mu.Unlock()
		if v.rotation != nil && slices.Equal(v.rotation, rotation) {
			return
		}
		v.rotation = rotation
		v.noteLocked(func(s *startSnapshot) { s.offerLanes(rotation, at) })
	}
}
