package drain

import (
	"context"
	"io"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestHeavyFreshLoopProvesCurrentTipOnFirstTick(t *testing.T) {
	root, tip := heavyRepo(t)
	s, err := store.InitFixture(context.Background(), filepath.Join(scratchUnderDurable(t), "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	calls := 0
	steps := heavyTestSteps{judge: func(context.Context, string, string) ([]HeavyCheck, error) {
		calls++
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	}}
	// No landing occurred and the new loop has no attempted-tip memory.
	l := &Loop{gatePolicy: GatePolicy{LandingStrategy: "after-merge"}, cfg: LoopConfig{Root: root, Store: s.Path(), LaneRoot: scratchUnderDurable(t), Steps: steps, Now: time.Now, Report: io.Discard}}
	if err := l.maybeRolling(context.Background(), tip, false, &LoopResult{}); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	green, err := heavyGreen(context.Background(), root)
	if err != nil || green != tip || calls != 1 {
		t.Fatalf("green=%s calls=%d err=%v", green, calls, err)
	}
}
