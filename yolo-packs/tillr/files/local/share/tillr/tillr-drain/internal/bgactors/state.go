package bgactors

import (
	"maps"
	"sync"
	"sync/atomic"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/gateland"
	"github.com/mschulkind-oss/tillr-drain/internal/worktreebudget"
	"github.com/mschulkind-oss/tillr/vantage"
)

// Slot tracks one background actor's wake channel, active state, optional test
// timer seam, and run counter.
type Slot struct {
	Wake   chan struct{}
	Active bool
	Timer  func(time.Duration) (<-chan time.Time, func())
	Runs   atomic.Int64
}

// Ensure initializes the buffered wake channel if nil.
func (s *Slot) Ensure() {
	if s.Wake == nil {
		s.Wake = make(chan struct{}, 1)
	}
}

// TrunkCache holds the trunk-proof actor's latest cached refs and unproved
// blocker state read by the pure scheduling pass without spawning git.
type TrunkCache struct {
	Tip         string
	Green       string
	UnprovedSet map[string]bool
	BlockBases  map[string]string
	Valid       bool
}

// DocPrereqsCache holds the sync actor's latest cached document-sharing
// prerequisite results read by the pure scheduling pass without spawning git or
// Vantage.
type DocPrereqsCache struct {
	Unshared          map[string]bool
	RewriteHolds      map[string]string
	RewriteOwnerHolds map[string]string
	Valid             bool
}

// State groups all loop-free background actor state, caches, and internal
// locks so drain.Loop holds one field without adding direct mutexes to Loop.
type State struct {
	Active   atomic.Bool
	PassWake chan struct{}

	Sync       Slot
	TrunkProof Slot
	GC         Slot

	// PassMu serializes scheduling-pass assignment WriteTx (D-SN1).
	PassMu  sync.Mutex
	SyncMu  sync.Mutex
	TrunkMu sync.Mutex
	GCMu    sync.Mutex

	cacheMu          sync.RWMutex
	trunkCache       TrunkCache
	wtSnap           *worktreebudget.Snapshot
	commentReady     map[string]bool
	docPrereqs       DocPrereqsCache
	vantageQuestions *vantage.VantageQuestions

	DocsGen  atomic.Int64
	TrunkGen atomic.Int64
	GCGen    atomic.Int64
}

// Ensure initializes the pass wake channel and every actor slot's wake channel.
func (s *State) Ensure() {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	if s.PassWake == nil {
		s.PassWake = make(chan struct{}, 1)
	}
	s.Sync.Ensure()
	s.TrunkProof.Ensure()
	s.GC.Ensure()
}

// PassWakeChan returns the pass wake channel, initializing it if needed.
func (s *State) PassWakeChan() <-chan struct{} {
	s.Ensure()
	s.cacheMu.RLock()
	ch := s.PassWake
	s.cacheMu.RUnlock()
	return ch
}

// SignalPass wakes the scheduling pass.
func (s *State) SignalPass() {
	s.Ensure()
	s.cacheMu.RLock()
	ch := s.PassWake
	s.cacheMu.RUnlock()
	gateland.SignalWake(ch)
}

// SignalSync wakes the document/comment sync actor.
func (s *State) SignalSync() {
	s.Ensure()
	s.cacheMu.RLock()
	ch := s.Sync.Wake
	s.cacheMu.RUnlock()
	gateland.SignalWake(ch)
}

// SignalTrunkProof wakes the trunk-proof actor.
func (s *State) SignalTrunkProof() {
	s.Ensure()
	s.cacheMu.RLock()
	ch := s.TrunkProof.Wake
	s.cacheMu.RUnlock()
	gateland.SignalWake(ch)
}

// SignalGC wakes the lease/ref/worktree collection actor.
func (s *State) SignalGC() {
	s.Ensure()
	s.cacheMu.RLock()
	ch := s.GC.Wake
	s.cacheMu.RUnlock()
	gateland.SignalWake(ch)
}

// SyncWakeChan returns the sync actor's wake channel.
func (s *State) SyncWakeChan() <-chan struct{} {
	s.Ensure()
	s.cacheMu.RLock()
	ch := s.Sync.Wake
	s.cacheMu.RUnlock()
	return ch
}

// TrunkProofWakeChan returns the trunk-proof actor's wake channel.
func (s *State) TrunkProofWakeChan() <-chan struct{} {
	s.Ensure()
	s.cacheMu.RLock()
	ch := s.TrunkProof.Wake
	s.cacheMu.RUnlock()
	return ch
}

// GCWakeChan returns the gc actor's wake channel.
func (s *State) GCWakeChan() <-chan struct{} {
	s.Ensure()
	s.cacheMu.RLock()
	ch := s.GC.Wake
	s.cacheMu.RUnlock()
	return ch
}

// CachedTrunk returns a copy of the cached trunk state.
func (s *State) CachedTrunk() TrunkCache {
	s.cacheMu.RLock()
	c := s.trunkCache
	s.cacheMu.RUnlock()
	return c
}

// CachedTrunkTip returns the cached trunk tip SHA.
func (s *State) CachedTrunkTip() string {
	s.cacheMu.RLock()
	tip := s.trunkCache.Tip
	s.cacheMu.RUnlock()
	return tip
}

// CachedTrunkAndGreen returns cached tip, green, and validity.
func (s *State) CachedTrunkAndGreen() (string, string, bool) {
	s.cacheMu.RLock()
	c := s.trunkCache
	s.cacheMu.RUnlock()
	return c.Tip, c.Green, c.Valid
}

// SetCachedTrunk updates the cached trunk state and reports whether anything
// changed, incrementing TrunkGen on change.
func (s *State) SetCachedTrunk(next TrunkCache) bool {
	s.cacheMu.Lock()
	prev := s.trunkCache
	changed := !prev.Valid || prev.Tip != next.Tip || prev.Green != next.Green ||
		!maps.Equal(prev.UnprovedSet, next.UnprovedSet) ||
		!maps.Equal(prev.BlockBases, next.BlockBases)
	s.trunkCache = TrunkCache{
		Tip:         next.Tip,
		Green:       next.Green,
		UnprovedSet: maps.Clone(next.UnprovedSet),
		BlockBases:  maps.Clone(next.BlockBases),
		Valid:       next.Valid,
	}
	s.cacheMu.Unlock()
	if changed {
		s.TrunkGen.Add(1)
	}
	return changed
}

// CachedWorktreeSnapshot returns a copy of the cached worktree budget snapshot,
// or nil if not yet populated.
func (s *State) CachedWorktreeSnapshot() *worktreebudget.Snapshot {
	s.cacheMu.RLock()
	snap := s.wtSnap
	s.cacheMu.RUnlock()
	if snap == nil {
		return nil
	}
	cp := *snap
	return &cp
}

// SetCachedWorktreeSnapshot stores a copy of the latest worktree budget snapshot.
func (s *State) SetCachedWorktreeSnapshot(snap worktreebudget.Snapshot) {
	cp := snap
	s.cacheMu.Lock()
	s.wtSnap = &cp
	s.cacheMu.Unlock()
}

// CachedCommentReady returns a cloned map of comment item readiness.
func (s *State) CachedCommentReady() map[string]bool {
	s.cacheMu.RLock()
	m := maps.Clone(s.commentReady)
	s.cacheMu.RUnlock()
	return m
}

// SetCachedCommentReady updates the comment readiness map and reports whether it changed.
func (s *State) SetCachedCommentReady(ready map[string]bool) bool {
	s.cacheMu.Lock()
	changed := !maps.Equal(s.commentReady, ready)
	s.commentReady = maps.Clone(ready)
	s.cacheMu.Unlock()
	return changed
}

// CachedDocPrereqs returns a cloned copy of the cached document prerequisites,
// or nil if not yet populated.
func (s *State) CachedDocPrereqs() *DocPrereqsCache {
	s.cacheMu.RLock()
	defer s.cacheMu.RUnlock()
	if !s.docPrereqs.Valid {
		return nil
	}
	return &DocPrereqsCache{
		Unshared:          maps.Clone(s.docPrereqs.Unshared),
		RewriteHolds:      maps.Clone(s.docPrereqs.RewriteHolds),
		RewriteOwnerHolds: maps.Clone(s.docPrereqs.RewriteOwnerHolds),
		Valid:             true,
	}
}

// SetCachedDocPrereqs updates the cached document prerequisites and reports whether they changed.
func (s *State) SetCachedDocPrereqs(next DocPrereqsCache) bool {
	s.cacheMu.Lock()
	defer s.cacheMu.Unlock()
	prev := s.docPrereqs
	if prev.Valid == next.Valid &&
		maps.Equal(prev.Unshared, next.Unshared) &&
		maps.Equal(prev.RewriteHolds, next.RewriteHolds) &&
		maps.Equal(prev.RewriteOwnerHolds, next.RewriteOwnerHolds) {
		return false
	}
	s.docPrereqs = DocPrereqsCache{
		Unshared:          maps.Clone(next.Unshared),
		RewriteHolds:      maps.Clone(next.RewriteHolds),
		RewriteOwnerHolds: maps.Clone(next.RewriteOwnerHolds),
		Valid:             next.Valid,
	}
	return true
}

// CachedVantageQuestions returns the cached Vantage question index, or nil.
func (s *State) CachedVantageQuestions() *vantage.VantageQuestions {
	s.cacheMu.RLock()
	q := s.vantageQuestions
	s.cacheMu.RUnlock()
	return q
}

// SetCachedVantageQuestions stores the cached Vantage question index.
func (s *State) SetCachedVantageQuestions(q *vantage.VantageQuestions) {
	s.cacheMu.Lock()
	s.vantageQuestions = q
	s.cacheMu.Unlock()
}
