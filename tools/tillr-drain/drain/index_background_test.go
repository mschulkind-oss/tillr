package drain

import (
	"context"
	"sync"
	"testing"
)

// unfinishedIndexSteps is an indexer that never finishes on its own: each call returns only
// when its context is canceled. Measured on 2026-11-17, the live `tillr-trace ingest` ran
// for half an hour on a whole-corpus rebuild, so "never finishes" is the shape a loop has to
// survive, not a contrivance.
type unfinishedIndexSteps struct {
	triggerSteps
	started     chan struct{}
	startedOnce sync.Once
	mu          sync.Mutex
	calls       int
	canceled    int
}

func (s *unfinishedIndexSteps) Index(ctx context.Context) error {
	s.mu.Lock()
	s.calls++
	s.mu.Unlock()
	s.startedOnce.Do(func() { close(s.started) })
	<-ctx.Done()
	s.mu.Lock()
	s.canceled++
	s.mu.Unlock()
	return ctx.Err()
}

func (s *unfinishedIndexSteps) counts() (int, int) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.calls, s.canceled
}

func newUnfinishedIndexSteps(storePath string) *unfinishedIndexSteps {
	steps := &unfinishedIndexSteps{
		triggerSteps: triggerSteps{started: make(chan string, 8), release: make(chan struct{}), store: storePath},
		started:      make(chan struct{}),
	}
	close(steps.release)
	return steps
}

// TestAnUnfinishedIndexHoldsUpNeitherDispatchNorTheDrainedStop pins that the trace index is
// a read model the loop asks for, never a step it waits on. The indexer here starts after
// the first applied pass and never returns; the loop must still dispatch the second item,
// drain the queue and stop, and it must cancel the indexer on the way out rather than leave
// it running or wait for it to finish.
//
// Before the indexer moved off the pass goroutine, the pass's deferred index and every
// completion's index ran `tillr-trace ingest` synchronously — the completion's under
// `context.Background()` — so this loop never dispatched item-b and never returned.
func TestAnUnfinishedIndexHoldsUpNeitherDispatchNorTheDrainedStop(t *testing.T) {
	f, l := passFixture(t, "item-a", "item-b")
	steps := newUnfinishedIndexSteps(f.storePath)
	l.cfg.Steps = steps
	result, err := l.Run(context.Background())
	if err != nil || result.Stop != StopQueueDrained {
		t.Fatalf("loop: %+v / %v", result, err)
	}
	dispatched := map[string]bool{}
	for len(steps.triggerSteps.started) > 0 {
		dispatched[<-steps.triggerSteps.started] = true
	}
	if !dispatched["item-a"] || !dispatched["item-b"] {
		t.Fatalf("dispatched %v, want both items while the index was still running", dispatched)
	}
	calls, canceled := steps.counts()
	if calls == 0 {
		t.Fatal("the loop never asked for an index: an applied pass must still request one")
	}
	if canceled != calls {
		t.Fatalf("%d index call(s), %d canceled: the loop returned with an indexer still running", calls, canceled)
	}
}

// TestAnOperatorStopIsNotHeldByAnUnfinishedIndex is the operator's half: a stop that arrives
// while an index is running ends the loop, and the indexer is canceled with it.
//
// Before, the pass's deferred index swapped a canceled context for one that cannot be
// canceled, so `tillr drain stop` waited on the ingest — the 2026-11-17 loop stopped only
// after the operator killed each completion's ingest child by hand.
func TestAnOperatorStopIsNotHeldByAnUnfinishedIndex(t *testing.T) {
	f, l := passFixture(t, "item-a")
	l.cfg.ExitWhenDrained = false
	steps := newUnfinishedIndexSteps(f.storePath)
	l.cfg.Steps = steps
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	type outcome struct {
		result LoopResult
		err    error
	}
	ended := make(chan outcome, 1)
	go func() {
		result, err := l.Run(ctx)
		ended <- outcome{result, err}
	}()
	<-steps.started
	cancel()
	got := <-ended
	if got.err != nil || got.result.Stop != StopOperatorStop {
		t.Fatalf("loop: %+v / %v", got.result, got.err)
	}
	if calls, canceled := steps.counts(); canceled != calls {
		t.Fatalf("%d index call(s), %d canceled: the stop left an indexer running", calls, canceled)
	}
}
