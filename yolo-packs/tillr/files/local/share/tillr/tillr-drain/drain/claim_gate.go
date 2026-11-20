package drain

import (
	"context"
	"path/filepath"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// The claim gate keeps the loop's own repeating writers off the store's write lock while a
// run it just started is claiming its lane.
//
// A run face's first act is one write transaction that claims the lane (tillr-run
// claimLane). SQLite's busy wait is not a queue, so that child, polling at most every
// 100 ms, is served only if the lock is ever free at one of its polls. On 2026-11-20 it was
// not: the loop's gathered writes, its session heartbeat and its spend records took the lock
// back to back for five seconds and the child gave up with SQLITE_BUSY. The loop started the
// child, so the loop owes it the lock for that moment. Between Start and the claim, a
// gathered write does not begin an attempt and the heartbeat does not write; both wait for
// the gate, which every started run opens and closes by itself.
//
// The gate is a courtesy and never a correctness lock: it closes when the run's lease
// carries its expiry (the claim happened), when the face exits, or after runClaimWindow,
// whichever is first. A run with no timeout leaves no mark when it claims, so for it the
// window is the whole bound. Writers elsewhere in the loop (settlement spend, judgments)
// are not gated; they are single writes, not retry loops.
const runClaimWindow = 2 * time.Second

type claimGate struct {
	mu   sync.Mutex
	open int
	idle chan struct{} // closed when open returns to zero
}

var claimGates sync.Map // absolute store path -> *claimGate

func claimGateFor(path string) *claimGate {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	g, _ := claimGates.LoadOrStore(filepath.Clean(path), &claimGate{})
	return g.(*claimGate)
}

// begin opens the gate for one run. The function it answers closes that run's hold, and
// may be called more than once.
func (g *claimGate) begin() func() {
	g.mu.Lock()
	if g.open == 0 {
		g.idle = make(chan struct{})
	}
	g.open++
	g.mu.Unlock()
	var once sync.Once
	return func() {
		once.Do(func() {
			g.mu.Lock()
			g.open--
			if g.open == 0 {
				close(g.idle)
			}
			g.mu.Unlock()
		})
	}
}

// wait returns once no started run is still claiming, or when ctx ends. A goroutine that
// itself holds a store write does not wait: the claim cannot proceed while it holds the lock,
// so waiting would only run the window out.
func (g *claimGate) wait(ctx context.Context) error {
	if store.HoldsWrite() {
		return nil
	}
	for {
		g.mu.Lock()
		if g.open == 0 {
			g.mu.Unlock()
			return nil
		}
		idle := g.idle
		g.mu.Unlock()
		select {
		case <-idle:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}

// holdForClaim opens the gate for the run just started and closes it when the claim is
// visible, the face has exited (exited closes), or the window has passed.
func (s *ProcessSteps) holdForClaim(ctx context.Context, req run.Request, exited <-chan struct{}) {
	end := claimGateFor(s.Store).begin()
	go func() {
		defer end()
		ctx, cancel := context.WithTimeout(ctx, runClaimWindow)
		defer cancel()
		var db *store.Store
		if req.TimeoutMS > 0 {
			if opened, err := store.OpenRead(ctx, s.Store); err == nil {
				db = opened
				defer db.Close()
			}
		}
		ticker := time.NewTicker(25 * time.Millisecond)
		defer ticker.Stop()
		for {
			select {
			case <-exited:
				return
			case <-ctx.Done():
				return
			case <-ticker.C:
				if db == nil {
					continue
				}
				claimed := false
				_ = db.ReadTx(ctx, func(r *store.Reader) error {
					var n int
					if err := r.QueryRow(`SELECT COUNT(*) FROM lanes WHERE run_id = ? AND leased_until IS NOT NULL`, req.RunID).Scan(&n); err != nil {
						return err
					}
					claimed = n > 0
					return nil
				})
				if claimed {
					return
				}
			}
		}
	}()
}
