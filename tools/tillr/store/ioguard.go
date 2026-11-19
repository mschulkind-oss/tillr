package store

import (
	"errors"
	"fmt"
	"os"
	"sync"
	"sync/atomic"
	"time"
)

// The I/O guard: a goroutine that holds an open write transaction does no I/O but the
// store's own. The owner's rule (2026-11-18): either a lock is a write lock, held for the
// statements that write, or it is not one; there is no third kind that is also held while
// git, a subprocess, a file or the network answers. A writer that waits on such I/O makes
// every other writer — the loop's heartbeat, a close, a landing's receipt — wait on it too,
// and the wait ends as SQLITE_BUSY after the busy timeout.
//
// What it checks: a wrapper that starts git or another subprocess, or reads or writes a file
// outside the store, calls CheckIO first, and CheckIO refuses when the calling goroutine is
// inside a WriteTx callback. The bookkeeping is per goroutine rather than per process, so a
// goroutine running git while another goroutine of the same program writes is not a fault:
// it holds nothing.
//
// What it cannot see: I/O a write's callback hands to a goroutine of its own and waits for,
// and I/O that does not pass through a wrapper that calls CheckIO. It is a guard on the
// wrappers, not a sandbox.
//
// Cost: when no write is open anywhere in the process it is one atomic load; otherwise it
// reads the calling goroutine's id from its stack header, as the write queue already does
// on every write.

// ErrIOUnderWrite is CheckIO's refusal: the calling goroutine holds an open write
// transaction, and the I/O it was about to start would hold the store's write lock for as
// long as that I/O takes.
var ErrIOUnderWrite = errors.New("store: I/O while holding the store's write lock")

var (
	openWrites   atomic.Int64 // write transactions open in this process
	writeHolders sync.Map     // goroutine id -> *atomic.Int64, its open write transactions
)

// enterWrite records that the calling goroutine opened a write transaction, and answers the
// function that records its end.
func enterWrite() func() {
	gid := goroutineID()
	v, _ := writeHolders.LoadOrStore(gid, &atomic.Int64{})
	n := v.(*atomic.Int64)
	n.Add(1)
	openWrites.Add(1)
	return func() {
		openWrites.Add(-1)
		// Only this goroutine changes its own entry, so an entry at zero is removed
		// without racing a re-entry: a long-lived program does not keep one per goroutine
		// that ever wrote.
		if n.Add(-1) == 0 {
			writeHolders.Delete(gid)
		}
	}
}

// OpenWriteTransactions answers how many write transactions are open in this process now.
// It is observation: a test asserts a landing's git command runs with none open.
func OpenWriteTransactions() int64 { return openWrites.Load() }

// HoldsWrite answers whether the calling goroutine is inside a write transaction.
func HoldsWrite() bool {
	if openWrites.Load() == 0 {
		return false
	}
	v, ok := writeHolders.Load(goroutineID())
	return ok && v.(*atomic.Int64).Load() > 0
}

// ReportIO is CheckIO's observing twin, for a wrapper whose callers are not all converted
// yet: it never refuses, and when the calling goroutine holds an open write transaction and
// TILLR_SQLITE_TRACE=1, it writes one `tillr-sqlite ... operation="io-under-write"` line to
// stderr naming what, so the remaining sites are measured in the log the loop already keeps.
func ReportIO(what string) {
	if !HoldsWrite() || os.Getenv("TILLR_SQLITE_TRACE") != "1" {
		return
	}
	fmt.Fprintf(os.Stderr, "tillr-sqlite at=%s pid=%d operation=%q phase=io detail=%q\n",
		time.Now().UTC().Format(time.RFC3339Nano), os.Getpid(), "io-under-write", what)
}

// CheckIO is what a wrapper calls before it starts I/O that is not the store's own — a git
// command, another subprocess, a file read or write. It answers ErrIOUnderWrite, naming
// what, when the calling goroutine holds an open write transaction, and nil otherwise. A
// refused wrapper must not start the I/O: the refusal is the defect, found where it happens.
func CheckIO(what string) error {
	if !HoldsWrite() {
		return nil
	}
	return fmt.Errorf("%w: %s; read and decide before the write, or mark the work begun in one short write and record it in another", ErrIOUnderWrite, what)
}
