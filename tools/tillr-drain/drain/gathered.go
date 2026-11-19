package drain

import (
	"context"
	"errors"
	"fmt"
	"sync"

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
	for {
		g.mu.Lock()
		g.missed = nil
		g.mu.Unlock()
		err := s.WriteTx(ctx, func(tx *store.Tx) error {
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
	}
}
