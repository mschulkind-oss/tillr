package drain

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// Gathered I/O: how a write transaction whose decision needs git or a file gets those
// answers without holding the store's write lock while they are fetched.
//
// The owner's rule (2026-11-18) is that a process holding an open store write does no I/O
// but the store's own. Some writes decide from git and file facts that depend on rows read
// in the same transaction — which item a lane is cut for decides which commit is asked
// about — so the facts cannot simply be read before the write begins. writeGathered runs
// such a write with a gathered set in its context. Inside it, every git command and file
// read the drain's wrappers make (gather) is answered from the set; one the set does not
// hold is not started: the wrapper answers a gatherMiss, the write rolls back having
// changed nothing, the missing fact is fetched with nothing held, and the write runs again
// from its first statement. Each attempt the write needs a fact it has not been given, the
// set grows by that fact, so the write converges once its decision asks for nothing new.
//
// What a miss costs, and what keeps it from starving another writer. A write takes the
// store's write lock when it begins (BEGIN IMMEDIATE), before fn has read anything, and fn
// reads the store it decides from — on a large store that is the pending runs' whole
// history, about half a second — before it reaches the fact it lacks. The lock is therefore
// held for those reads even though the attempt then rolls back and writes nothing; a miss
// cannot be answered without the lock because fn takes a *store.Tx, which only a write
// transaction supplies. What changes between two attempts is the set: every attempt that
// misses adds the one fact it missed, so the next attempt either misses a different fact or
// commits; an attempt never repeats a miss. Because each retry begins the instant the last
// one rolled back, a loop that needs several facts would hold the lock back to back, and
// SQLite's busy wait is not a queue (a waiting process polls, at most every 100 ms), so
// another process asking for the lock in the instants between attempts never sees it free:
// on 2026-11-20 a run's lane claim waited out its five-second busy timeout behind ten such
// attempts. After a miss the write therefore waits as long as that attempt held the lock
// before it begins again, so the lock is held at most half the time across consecutive
// attempts and every other writer meets it free for as long as the attempt before held it.
//
// What a gathered answer may be stale against is git or a file, never the store: the store
// rows are read again by every attempt. Commits are immutable, so an ancestry answer about
// two shas cannot go stale; a ref or a file read before the write is as current as one read
// inside it, because the store's write lock never excluded a ref move or a file write.
//
// A wrapper reached outside writeGathered runs its I/O as before, and when its goroutine
// holds a store write it reports the fact through the store's I/O trace (store.ReportIO):
// the sites not yet converted are observed, not refused.

type gatheredAnswer struct {
	out string
	err error
}

type gathered struct {
	mu      sync.Mutex
	answers map[string]gatheredAnswer
	// recording is a read phase with no write open: gather runs the I/O and keeps the
	// answer, so the write that follows normally needs nothing more.
	recording bool
	// missed is the first miss of the current attempt. The attempt is rolled back and the
	// fact fetched whatever fn returned, so code that swallows or rewraps an error cannot
	// commit a decision made without the fact.
	missed *gatherMiss
}

type gatheredKey struct{}

// gatheringFor answers a context in which the drain's wrappers run their I/O and keep each
// answer in g, for a read phase that precedes writeGatheredFrom(g).
func gatheringFor(ctx context.Context) (context.Context, *gathered) {
	g := &gathered{answers: map[string]gatheredAnswer{}, recording: true}
	return context.WithValue(ctx, gatheredKey{}, g), g
}

// gatherMiss is the answer a wrapper gives inside a gathered write for a fact the set does
// not hold. It carries how to fetch the fact, which writeGathered does with nothing held.
type gatherMiss struct {
	key string
	run func(context.Context) (string, error)
}

func (m *gatherMiss) Error() string {
	return fmt.Sprintf("drain: %s was not gathered before the write; the write rolls back and runs again once it is", m.key)
}

// isGatherMiss reports whether err is, or wraps, a gatherMiss. A decision that branches on
// an error must test this first: a miss is not the fact, so it must be returned, never read
// as a negative answer.
func isGatherMiss(err error) bool {
	var miss *gatherMiss
	return errors.As(err, &miss)
}

// gather answers the I/O named key. Inside a gathered write it answers from the set or
// answers a gatherMiss and starts nothing; elsewhere it runs the I/O.
func gather(ctx context.Context, key string, run func(context.Context) (string, error)) (string, error) {
	g, _ := ctx.Value(gatheredKey{}).(*gathered)
	if g == nil {
		store.ReportIO(key)
		return run(ctx)
	}
	g.mu.Lock()
	a, ok := g.answers[key]
	recording := g.recording
	g.mu.Unlock()
	if ok {
		return a.out, a.err
	}
	if recording {
		if err := store.CheckIO(key); err != nil {
			return "", err
		}
		out, err := run(ctx)
		g.mu.Lock()
		g.answers[key] = gatheredAnswer{out: out, err: err}
		g.mu.Unlock()
		return out, err
	}
	miss := &gatherMiss{key: key, run: run}
	g.mu.Lock()
	if g.missed == nil {
		g.missed = miss
	}
	g.mu.Unlock()
	return "", miss
}

// writeGatheredHooks lets a test see each attempt and each fetch.
var writeGatheredHooks struct {
	// Fetch runs after a missing fact was fetched with no write open.
	Fetch func(key string)
	// Held runs with how long an attempt held the write lock, and Yield with the pause
	// that follows a miss, both before the next attempt begins.
	Held  func(d time.Duration)
	Yield func(d time.Duration)
}

// yieldAfterMiss waits d with the lock released, so another writer is served in the gap. It
// answers the context's error when the context ends first.
func yieldAfterMiss(ctx context.Context, d time.Duration) error {
	if writeGatheredHooks.Yield != nil {
		writeGatheredHooks.Yield(d)
	}
	if d <= 0 {
		return ctx.Err()
	}
	timer := time.NewTimer(d)
	defer timer.Stop()
	select {
	case <-timer.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// writeGathered runs fn as one store write whose git and file reads are gathered with no
// write open. fn may run more than once, so it must start each run from nothing: every
// value it assigns outside the transaction is assigned again by the run that commits.
func writeGathered(ctx context.Context, s *store.Store, fn func(context.Context, *store.Tx) error) error {
	return writeGatheredFrom(ctx, s, &gathered{answers: map[string]gatheredAnswer{}}, fn)
}

// writeGatheredFrom is writeGathered starting from the answers a read phase gathered
// (gatheringFor). The set stops recording: from here a missing answer is a miss.
func writeGatheredFrom(ctx context.Context, s *store.Store, g *gathered, fn func(context.Context, *store.Tx) error) error {
	g.mu.Lock()
	g.recording = false
	g.mu.Unlock()
	gctx := context.WithValue(ctx, gatheredKey{}, g)
	gate := claimGateFor(s.Path())
	for {
		// A run this process just started is claiming its lane: its write goes first.
		if err := gate.wait(ctx); err != nil {
			return err
		}
		g.mu.Lock()
		g.missed = nil
		g.mu.Unlock()
		var held time.Duration
		err := s.WriteTx(ctx, func(tx *store.Tx) error {
			began := time.Now()
			defer func() { held = time.Since(began) }()
			ferr := fn(gctx, tx)
			g.mu.Lock()
			missed := g.missed
			g.mu.Unlock()
			if missed != nil {
				// Returning the miss rolls the attempt back, even when fn swallowed it.
				return missed
			}
			return ferr
		})
		var miss *gatherMiss
		if !errors.As(err, &miss) {
			return err
		}
		if writeGatheredHooks.Held != nil {
			writeGatheredHooks.Held(held)
		}
		if err := store.CheckIO(miss.key); err != nil {
			// A gathered write opened inside another write: fetching here would hold the
			// outer write's lock, which is the fault this exists to remove.
			return err
		}
		out, rerr := miss.run(ctx)
		if cerr := ctx.Err(); cerr != nil {
			return cerr
		}
		g.mu.Lock()
		g.answers[miss.key] = gatheredAnswer{out: out, err: rerr}
		g.mu.Unlock()
		if writeGatheredHooks.Fetch != nil {
			writeGatheredHooks.Fetch(miss.key)
		}
		if err := yieldAfterMiss(ctx, held); err != nil {
			return err
		}
	}
}
