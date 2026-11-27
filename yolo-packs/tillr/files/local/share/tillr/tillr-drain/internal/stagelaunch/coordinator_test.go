package stagelaunch

import (
	"context"
	"errors"
	"path/filepath"
	"testing"
	"time"
)

func TestStateTrackingAndFaults(t *testing.T) {
	st := NewState()
	if st.HasRun("r1") || st.HasItem("i1") || st.RunCount() != 0 {
		t.Fatalf("new state should be empty")
	}

	st.Stage.MarkStart("i1", "r1")
	if !st.HasRun("r1") || !st.HasItem("i1") || st.RunCount() != 1 {
		t.Fatalf("after Stage.MarkStart: HasRun=%v HasItem=%v RunCount=%d", st.HasRun("r1"), st.HasItem("i1"), st.RunCount())
	}
	st.Launch.MarkStart("i2", "r2")
	if !st.HasRun("r2") || !st.HasItem("i2") || st.RunCount() != 2 {
		t.Fatalf("after Launch.MarkStart: HasRun=%v HasItem=%v RunCount=%d", st.HasRun("r2"), st.HasItem("i2"), st.RunCount())
	}
	st.Stage.MarkDone("i1", "r1")
	if st.HasRun("r1") || st.HasItem("i1") || st.RunCount() != 1 {
		t.Fatalf("after Stage.MarkDone: HasRun=%v HasItem=%v RunCount=%d", st.HasRun("r1"), st.HasItem("i1"), st.RunCount())
	}

	if st.IsLaunched("r2") {
		t.Fatalf("r2 should not be launched yet")
	}
	st.MarkLaunched("r2")
	if !st.IsLaunched("r2") {
		t.Fatalf("r2 should be marked launched")
	}

	if st.HasFaults() {
		t.Fatalf("fresh state should have no faults")
	}
	st.RecordFault("r1")
	if !st.HasFaults() {
		t.Fatalf("expected HasFaults after RecordFault")
	}
	st.ClearFaults()
	if st.HasFaults() {
		t.Fatalf("expected no faults after ClearFaults")
	}
}

func TestEligibilityPredicates(t *testing.T) {
	base := Eligibility{RunID: "run-1", IsDispatch: true}
	if !IsStageEligible(base) {
		t.Fatalf("unstaged dispatch should be stage-eligible")
	}
	if IsLaunchEligible(base) {
		t.Fatalf("unstaged dispatch must not be launch-eligible")
	}

	staged := base
	staged.Staged = true
	if IsStageEligible(staged) {
		t.Fatalf("staged dispatch must not be stage-eligible")
	}
	if !IsLaunchEligible(staged) {
		t.Fatalf("staged dispatch should be launch-eligible")
	}

	for name, mut := range map[string]func(*Eligibility){
		"done":          func(e *Eligibility) { e.Done = true },
		"ready":         func(e *Eligibility) { e.Ready = true },
		"has_candidate": func(e *Eligibility) { e.HasCandidate = true },
		"examining":     func(e *Eligibility) { e.Examining = true },
		"has_examiner":  func(e *Eligibility) { e.HasExaminer = true },
		"lane_released": func(e *Eligibility) { e.LaneReleased = true },
		"empty_run_id":  func(e *Eligibility) { e.RunID = "" },
		"not_dispatch":  func(e *Eligibility) { e.IsDispatch = false },
	} {
		eStage := base
		mut(&eStage)
		if IsStageEligible(eStage) {
			t.Errorf("%s: expected IsStageEligible=false", name)
		}
		eLaunch := staged
		mut(&eLaunch)
		if IsLaunchEligible(eLaunch) {
			t.Errorf("%s: expected IsLaunchEligible=false", name)
		}
	}
}

func TestAcquireStageLockExclusion(t *testing.T) {
	wt := filepath.Join(t.TempDir(), "worktree")
	unlock, acquired, err := AcquireStageLock(wt)
	if err != nil || !acquired {
		t.Fatalf("first AcquireStageLock = acquired=%v err=%v", acquired, err)
	}
	unlock2, acquired2, err2 := AcquireStageLock(wt)
	if err2 != nil || acquired2 {
		if unlock2 != nil {
			unlock2()
		}
		unlock()
		t.Fatalf("contended AcquireStageLock = acquired=%v err=%v; want false, nil", acquired2, err2)
	}
	unlock()

	unlock3, acquired3, err3 := AcquireStageLock(wt)
	if err3 != nil || !acquired3 {
		t.Fatalf("re-AcquireStageLock = acquired=%v err=%v; want true, nil", acquired3, err3)
	}
	unlock3()
}

func TestRetryGitConfigLock(t *testing.T) {
	ctx := context.Background()
	wait := func(context.Context, time.Duration) bool { return true }

	attempts := 0
	err := RetryGitConfigLock(ctx, wait, func() error {
		attempts++
		if attempts < 3 {
			return errors.New("error: could not lock config file /repo/.git/config.lock: File exists")
		}
		return nil
	})
	if err != nil || attempts != 3 {
		t.Fatalf("RetryGitConfigLock = err=%v attempts=%d; want nil, 3", err, attempts)
	}

	sentinel := errors.New("unrelated git failure")
	nonLockAttempts := 0
	err = RetryGitConfigLock(ctx, wait, func() error {
		nonLockAttempts++
		return sentinel
	})
	if !errors.Is(err, sentinel) || nonLockAttempts != 1 {
		t.Fatalf("non-lock error = err=%v attempts=%d; want sentinel, 1", err, nonLockAttempts)
	}
}
