package stagelaunch

import (
	"context"
	"errors"
	"math/rand/v2"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/gateland"
	"github.com/mschulkind-oss/tillr/store"
)

const (
	gitConfigLockMinBackoff = 10 * time.Millisecond
	gitConfigLockMaxBackoff = 50 * time.Millisecond
	gitConfigLockMaxWait    = 2 * time.Second
)

// State groups Step 1 (Stage) and Step 2 (Launch) coordinator state plus
// pass-boundary staging fault/backoff tracking so drain.Loop holds one
// field for staging and launch. Callers guard mutations under the loop
// mutex.
type State struct {
	Stage        gateland.LaneState
	Launch       gateland.LaneState
	StageWG      sync.WaitGroup
	LaunchWG     sync.WaitGroup
	LaunchedRuns map[string]bool
	Faults       map[string]struct{} // faulting run IDs, including claims still retracting
	Backoff      bool
}

// NewState returns an initialized State with ready wake channels and maps.
func NewState() *State {
	st := &State{}
	st.Ensure()
	return st
}

// Ensure initializes nil channels and maps on a zero-valued State.
func (s *State) Ensure() {
	s.Stage.Ensure()
	s.Launch.Ensure()
	if s.LaunchedRuns == nil {
		s.LaunchedRuns = map[string]bool{}
	}
}

// HasRun reports whether runID is currently tracked by either coordinator.
func (s *State) HasRun(runID string) bool {
	return s.Stage.Runs[runID] || s.Launch.Runs[runID]
}

// HasItem reports whether itemID has an in-flight run on either coordinator.
func (s *State) HasItem(itemID string) bool {
	return s.Stage.Items[itemID] > 0 || s.Launch.Items[itemID] > 0
}

// RunCount returns the sum of tracked run map sizes across both coordinators
// (for capacity hints).
func (s *State) RunCount() int {
	return len(s.Stage.Runs) + len(s.Launch.Runs)
}

// MarkLaunched records that runID has been handed to launchRun in this process.
func (s *State) MarkLaunched(runID string) {
	if s.LaunchedRuns == nil {
		s.LaunchedRuns = map[string]bool{}
	}
	s.LaunchedRuns[runID] = true
}

// IsLaunched reports whether runID has already been handed to launchRun in
// this process.
func (s *State) IsLaunched(runID string) bool {
	return s.LaunchedRuns[runID]
}

// RecordFault records a faulting run ID for pass-boundary backoff handling.
func (s *State) RecordFault(runID string) {
	if s.Faults == nil {
		s.Faults = make(map[string]struct{})
	}
	s.Faults[runID] = struct{}{}
}

// ClearFaults clears the recorded pass-boundary staging faults.
func (s *State) ClearFaults() {
	s.Faults = nil
}

// HasFaults reports whether any pass-boundary staging faults are pending.
func (s *State) HasFaults() bool {
	return len(s.Faults) > 0
}

// Eligibility carries the pure boolean and identifier fields needed to decide
// whether a pending work record is eligible for Step 1 (stage) or Step 2
// (launch).
type Eligibility struct {
	Done         bool
	Ready        bool
	Staged       bool
	HasCandidate bool
	Examining    bool
	HasExaminer  bool
	LaneReleased bool
	RunID        string
	IsDispatch   bool
}

// IsStageEligible reports whether a pending work record is an assigned,
// unstaged dispatch that Step 1 should stage.
func IsStageEligible(e Eligibility) bool {
	if e.Done || e.Ready || e.Staged {
		return false
	}
	if e.HasCandidate || e.Examining || e.HasExaminer || e.LaneReleased {
		return false
	}
	if e.RunID == "" {
		return false
	}
	return e.IsDispatch
}

// IsLaunchEligible reports whether a pending work record has committed
// drain.staged and is waiting for Step 2 to launch its worker.
func IsLaunchEligible(e Eligibility) bool {
	if e.Done || e.Ready || !e.Staged {
		return false
	}
	if e.HasCandidate || e.Examining || e.HasExaminer || e.LaneReleased {
		return false
	}
	if e.RunID == "" {
		return false
	}
	return e.IsDispatch
}

// RunRecordExists reports whether runs already holds a row for runID.
// A pre-existing drain.started whose runs row already exists is implicitly
// staged and launched (design Section 7.3).
func RunRecordExists(r *store.Reader, runID string) (bool, error) {
	if runID == "" {
		return false, nil
	}
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM runs WHERE run_id = ?)`, runID).Scan(&exists); err != nil {
		return false, err
	}
	return exists, nil
}

// AcquireStageLock takes a non-blocking exclusive flock on <worktree>.stage.lock
// so only one staging actor writes to a worktree directory at a time.
func AcquireStageLock(worktreePath string) (func(), bool, error) {
	if worktreePath == "" {
		return func() {}, true, nil
	}
	lockPath := worktreePath + ".stage.lock"
	if err := os.MkdirAll(filepath.Dir(lockPath), 0o755); err != nil {
		return nil, false, err
	}
	f, err := os.OpenFile(lockPath, os.O_CREATE|os.O_RDWR, 0o600)
	if err != nil {
		return nil, false, err
	}
	if err := syscall.Flock(int(f.Fd()), syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		_ = f.Close()
		if errors.Is(err, syscall.EWOULDBLOCK) || errors.Is(err, syscall.EAGAIN) {
			return nil, false, nil
		}
		return nil, false, err
	}
	return func() {
		_ = syscall.Flock(int(f.Fd()), syscall.LOCK_UN)
		_ = f.Close()
	}, true, nil
}

// IsGitConfigLockErr reports whether err indicates transient Git
// .git/config.lock contention.
func IsGitConfigLockErr(err error) bool {
	if err == nil {
		return false
	}
	msg := strings.ToLower(err.Error())
	return strings.Contains(msg, "config.lock") || strings.Contains(msg, "could not lock config file")
}

// RetryGitConfigLock retries transient Git .git/config.lock contention with
// 10-50 ms jittered backoff up to 2 s (design Section 5.2, Section 7.1).
func RetryGitConfigLock(ctx context.Context, waitFor func(context.Context, time.Duration) bool, fn func() error) error {
	deadline := time.Now().Add(gitConfigLockMaxWait)
	for {
		err := fn()
		if err == nil || !IsGitConfigLockErr(err) || ctx.Err() != nil {
			return err
		}
		remaining := time.Until(deadline)
		if remaining <= 0 {
			return err
		}
		delay := gitConfigLockMinBackoff + time.Duration(rand.Int64N(int64(gitConfigLockMaxBackoff-gitConfigLockMinBackoff+1)))
		if delay > remaining {
			delay = remaining
		}
		if !waitFor(ctx, delay) {
			return err
		}
	}
}
