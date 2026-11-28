package storelog

import (
	"testing"
	"time"
)

func TestLoopPassTimerStepsAreSeparateFromPhases(t *testing.T) {
	now := time.Date(2026, 11, 27, 12, 0, 0, 0, time.UTC)
	pt := NewLoopPassTimer(func() time.Time { return now })
	if pt.Steps() != nil {
		t.Fatalf("Steps before any step = %v, want nil", pt.Steps())
	}
	endPhase := pt.StartPhase("drain_completions")
	endWait := pt.StartStep("collect_lock_wait")
	now = now.Add(3 * time.Second)
	endWait()
	endWalk := pt.StartStep("collect_walk")
	now = now.Add(2 * time.Second)
	endWalk()
	endPhase()

	steps := pt.Steps()
	if steps["collect_lock_wait"] != 3000 || steps["collect_walk"] != 2000 {
		t.Fatalf("steps = %v, want lock wait 3000 and walk 2000", steps)
	}
	_, wall, phases := pt.Finish()
	var sum int64
	for _, ms := range phases {
		sum += ms
	}
	if sum != wall || phases["drain_completions"] != 5000 {
		t.Fatalf("phases = %v wall = %d: steps must not disturb the phase invariant", phases, wall)
	}
	if _, ok := phases["collect_walk"]; ok {
		t.Fatalf("a step leaked into phases_ms: %v", phases)
	}
}

func TestLoopPassTimerNilStepIsNoop(t *testing.T) {
	var pt *LoopPassTimer
	pt.StartStep("x")()
	if pt.Steps() != nil {
		t.Fatal("nil timer reported steps")
	}
}
