package drain

import (
	"context"
	"sync"
	"sync/atomic"
)

// backgroundIndex runs the loop's trace indexer off the pass goroutine.
//
// The trace index is a read model: `tillr-trace ingest` turns what a tick already recorded
// into rows the observatory reads, and nothing the loop decides depends on it. It used to
// run synchronously after every applied pass and every completion, so its cost was paid by
// the tick, by the next dispatch and by the stop. Measured on 2026-11-17, one ingest ran a
// whole-corpus rebuild for more than 28 minutes while the loop dispatched nothing, and
// `tillr drain stop` waited on one ingest per pending completion.
//
// The loop now only asks. A request sets a pending flag and returns; one goroutine runs
// the indexer whenever a request is pending, so requests that arrive while an ingest runs
// collapse into a single follow-up ingest. Stopping cancels the running ingest, which kills
// its process, and waits only for that cancellation to land — never for the work. The
// ingest is resumable (each run is its own transaction), so a canceled one loses at most
// the run it was in, and the next request picks up from there.
type backgroundIndex struct {
	index  func(context.Context) error
	report func(format string, args ...any)
	// wake holds at most one pending request: a request made while one is pending is
	// already covered by it.
	wake chan struct{}
	// requests counts every request, including those a pending one covered, so a test can
	// tell "asked for an index" apart from "an index ran".
	requests atomic.Int64

	mu     sync.Mutex
	cancel context.CancelFunc
	done   chan struct{}
}

func newBackgroundIndex(index func(context.Context) error, report func(string, ...any)) *backgroundIndex {
	return &backgroundIndex{index: index, report: report, wake: make(chan struct{}, 1)}
}

// request asks for an index and returns at once. A request made before start, or after
// stop, stays pending for the next start. A Loop built without NewLoop has no indexer, and
// its requests are nothing.
func (b *backgroundIndex) request() {
	if b == nil {
		return
	}
	b.requests.Add(1)
	select {
	case b.wake <- struct{}{}:
	default:
	}
}

// start runs the indexer goroutine until the answered stop is called. A Loop can be run
// more than once, so each start owns its own goroutine and context.
func (b *backgroundIndex) start() (stop func()) {
	if b == nil {
		return func() {}
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	b.mu.Lock()
	b.cancel, b.done = cancel, done
	b.mu.Unlock()
	go func() {
		defer close(done)
		for {
			select {
			case <-ctx.Done():
				return
			case <-b.wake:
			}
			if ctx.Err() != nil {
				// Stopped while the request was being taken: keep it for the next start.
				b.request()
				return
			}
			if err := b.index(ctx); err != nil && ctx.Err() == nil {
				b.report("loop: the trace index was not updated: %v\n", err)
			}
		}
	}()
	return func() {
		cancel()
		<-done
	}
}
