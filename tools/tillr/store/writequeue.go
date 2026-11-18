package store

import (
	"bytes"
	"context"
	"path/filepath"
	"runtime"
	"strconv"
	"sync"
	"sync/atomic"
	"time"
)

// The write queue orders one process's write transactions on one store before they ask
// SQLite for its write lock, so goroutines in one program wait their turn in the
// process instead of racing each other through SQLite's busy handler.
//
// Why it exists: every Open is its own connection, and a program like the drain loop
// opens one per operation from many goroutines. Between those connections the only
// arbiter was SQLite's busy handler, which is not a queue: a waiting connection sleeps
// and polls (up to 100 ms between polls), so a goroutine that commits and begins again
// within a few milliseconds wins every gap. On 2026-11-18 the loop's pass applied its
// plans in back-to-back write transactions with 4 to 6 ms between them, and the loop's
// own session heartbeat, polling from another connection, never saw the lock free and
// failed with SQLITE_BUSY after its 5-second busy timeout. In the queue a waiter is
// served first come, first served (a Go channel's waiting senders are FIFO), so a
// heartbeat waits for at most the transaction in front of it.
//
// What it is not: a lock anything depends on for correctness. SQLite's write lock still
// decides, and another process is still met only through the busy handler. So the queue
// can reorder this process's writers but never make one fail or hang where SQLite alone
// would have let it through: a writer that has waited one busy timeout in the queue
// begins anyway (and meets SQLite's own wait, as before the queue), a cancelled context
// leaves the queue, and a goroutine that already holds the turn -- a write transaction
// opened inside another's callback -- does not wait for itself.
type writeQueue struct {
	turn   chan struct{}
	holder atomic.Int64 // goroutine id holding the turn, 0 when none
	// waiting counts writers waiting for the turn: the test seam that orders a FIFO
	// test without sleeping (export_test.go).
	waiting atomic.Int64
}

var writeQueues sync.Map // absolute store path -> *writeQueue

func writeQueueFor(path string) *writeQueue {
	if abs, err := filepath.Abs(path); err == nil {
		path = abs
	}
	q, _ := writeQueues.LoadOrStore(filepath.Clean(path), &writeQueue{turn: make(chan struct{}, 1)})
	return q.(*writeQueue)
}

// enter waits for this process's turn to write the store, at most wait. It answers the
// function that gives the turn back, which is a no-op when the writer began without the
// turn, and an error only when ctx ended first.
func (q *writeQueue) enter(ctx context.Context, wait time.Duration) (func(), bool, error) {
	me := goroutineID()
	if me != 0 && q.holder.Load() == me {
		return func() {}, false, nil
	}
	acquired := func() (func(), bool, error) {
		q.holder.Store(me)
		return func() { q.holder.Store(0); <-q.turn }, false, nil
	}
	select {
	case q.turn <- struct{}{}:
		return acquired()
	default:
	}
	if wait <= 0 {
		return func() {}, true, nil
	}
	q.waiting.Add(1)
	defer q.waiting.Add(-1)
	timer := time.NewTimer(wait)
	defer timer.Stop()
	select {
	case q.turn <- struct{}{}:
		release, _, err := acquired()
		return release, true, err
	case <-ctx.Done():
		return nil, true, ctx.Err()
	case <-timer.C:
		return func() {}, true, nil
	}
}

// goroutineID answers the calling goroutine's id from its stack header, 0 if unreadable.
func goroutineID() int64 {
	var buf [64]byte
	b := buf[:runtime.Stack(buf[:], false)]
	b = bytes.TrimPrefix(b, []byte("goroutine "))
	if i := bytes.IndexByte(b, ' '); i > 0 {
		if id, err := strconv.ParseInt(string(b[:i]), 10, 64); err == nil {
			return id
		}
	}
	return 0
}
