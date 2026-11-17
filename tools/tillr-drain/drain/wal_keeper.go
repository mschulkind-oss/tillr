package drain

import (
	"context"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// CheckpointInterval is how often a running Loop's heartbeat checkpoints the store. While
// the Loop runs it is the store's only checkpointer, so this bounds how far the write-ahead
// log grows between two checkpoints.
const CheckpointInterval = time.Minute

// CheckpointAllowance is how far ahead the heartbeat just before a checkpoint announces the
// next pass. The checkpoint flushes the disk on the heartbeat's goroutine, so the next
// heartbeat is late by that long; the announcement keeps a slow disk from making a live loop
// read as dead to `tillr-drain stop` and the observatory (SessionGrace is 5 s past the
// announced pass).
const CheckpointAllowance = 30 * time.Second

// The WAL keeper names this package's use of the store's checkpoint owner
// (store.CreateAndOwnCheckpoints): a running Loop takes it before its first session write
// opens the store, so before that open migrates (creating the empty store itself if there
// is none), and holds it until Run returns. Every other face opens the store, does one
// thing and closes it.
//
// While it is held, no other tillr handle's close checkpoints, so none restarts the log in
// a writer that waits behind a flush: the restart's one header flush happens in the owner's
// own write (checkpointIfDue, every CheckpointInterval). Without a Loop, a handle's close
// checkpoints instead once the log has grown past store.CheckpointFrames, and
// `tillr-store checkpoint` does it on demand. Every handle also keeps its own read-only
// connection open across its life, so no close is the store's last and none deletes the
// write-ahead log (WAL) under SQLite's exclusive lock; the keeper's idle reader no longer
// carries that alone.
func openWALKeeper(ctx context.Context, path string) (*store.CheckpointOwner, error) {
	return store.CreateAndOwnCheckpoints(ctx, path)
}

// keepWALWhileRunning makes the Loop's first session write take the keeper first.
// Only Run asks for it, so a Loop driven pass by pass holds no connection between calls.
func (l *Loop) keepWALWhileRunning() {
	l.sessionMu.Lock()
	defer l.sessionMu.Unlock()
	l.keepWAL = true
}

// LogReportBytes is the write-ahead log size past which a Loop whose checkpoint cycles keep
// missing the log's restart reports it, and again each time the log doubles. A restart needs
// no commit between the owner's pin and its write; writers that never leave that gap keep
// the log growing, and this is how an operator hears about it.
const LogReportBytes = 64 << 20

// checkpointIfDue runs one checkpoint cycle when CheckpointInterval has passed since the
// last one, or at once when the last one did not restart the log. It runs on the
// heartbeat's goroutine, after the heartbeat, so the heartbeat never waits on a checkpoint's
// flushes behind another goroutine; and it holds sessionMu only to read and record, never
// across the checkpoint, so a pass refreshing the session does not wait on the disk either.
//
// A cycle is maintenance. One that did not restart the log, whose own write waited out
// SQLite's busy timeout behind another writer, or whose pin's read did, is reported and
// tried again at the next heartbeat; only an error that is none of these faults the loop.
func (l *Loop) checkpointIfDue(ctx context.Context) error {
	l.sessionMu.Lock()
	owner, now := l.walKeeper, l.cfg.Now()
	due := owner != nil && (l.restartPending || now.Sub(l.lastCheckpoint) >= CheckpointInterval)
	l.sessionMu.Unlock()
	if owner != nil && !l.reportedLockError && owner.LockError() != nil {
		l.reportedLockError = true
		l.reportf("loop: other programs cannot see this loop's checkpoint ownership, so they keep checkpointing themselves: %v\n", owner.LockError())
	}
	if !due {
		return nil
	}
	if err := l.refreshSession(ctx, now.Add(CheckpointAllowance)); err != nil {
		return err
	}
	if l.beforeCheckpoint != nil {
		l.beforeCheckpoint()
	}
	c, err := owner.Checkpoint(ctx)
	if l.checkpointed != nil {
		l.checkpointed(c)
	}
	if err != nil {
		return err
	}
	l.sessionMu.Lock()
	l.lastCheckpoint, l.restartPending = now, !c.Restarted
	l.sessionMu.Unlock()
	l.reportCheckpoint(c)
	return nil
}

// reportCheckpoint says what an operator needs to hear about one cycle: a skipped cycle, the
// first miss after a restart, a log that has passed LogReportBytes (and each doubling after
// that), and the restart that ends a run of misses. It holds no lock.
func (l *Loop) reportCheckpoint(c store.Checkpointed) {
	switch {
	case c.Restarted:
		if l.missedRestarts > 0 {
			l.reportf("loop: the write-ahead log restarted after %d missed checkpoint cycles (log file %d bytes)\n", l.missedRestarts, c.LogBytes)
		}
		l.missedRestarts, l.reportedLogBytes = 0, 0
		return
	case c.WriterBusy:
		l.reportf("loop: the checkpoint's own write waited out SQLite's busy timeout behind another writer (TILLR_SQLITE_TRACE=1 names it); the cycle is skipped and tried again at the next heartbeat\n")
	case c.PinBusy:
		l.reportf("loop: the checkpoint's read pin waited out SQLite's busy timeout (another connection held the store's exclusive lock, as a recovery does); the cycle is skipped and tried again at the next heartbeat\n")
	case l.missedRestarts == 0:
		l.reportf("loop: the checkpoint did not restart the write-ahead log (%d log frames, %d copied, log file %d bytes): a commit came between the checkpoint and the loop's write, or a reader held frames back; trying again at the next heartbeat\n", c.LogFrames, c.CopiedFrames, c.LogBytes)
	}
	l.missedRestarts++
	if threshold := max(l.reportedLogBytes*2, LogReportBytes); c.LogBytes >= threshold {
		l.reportedLogBytes = threshold
		l.reportf("loop: the write-ahead log has grown to %d bytes (%d log frames) without a restart in %d checkpoint cycles: writers are leaving no gap for one\n", c.LogBytes, c.LogFrames, l.missedRestarts)
	}
}

// closeWALKeeper closes the keeper, if one was opened. Run defers it just before
// endSession, so it runs after the session's end record, on every way out of Run,
// including a panic. Closing runs one last checkpoint cycle, so the database file alone
// holds that end record; a cycle that could not copy everything, or whose write waited
// out the busy timeout, is reported, and what it left stays safe in the log for the next.
func (l *Loop) closeWALKeeper() {
	l.sessionMu.Lock()
	owner := l.walKeeper
	l.walKeeper, l.keepWAL = nil, false
	l.sessionMu.Unlock()
	if owner == nil {
		return
	}
	checkpointed, err := owner.Close(context.Background())
	switch {
	case err != nil:
		l.reportf("loop: the closing checkpoint failed: %v\n", err)
	case checkpointed.WriterBusy:
		l.reportf("loop: the closing checkpoint's own write waited out SQLite's busy timeout behind another writer (TILLR_SQLITE_TRACE=1 names it); the log stays for the next checkpoint\n")
	case checkpointed.PinBusy:
		l.reportf("loop: the closing checkpoint's read pin waited out SQLite's busy timeout (another connection held the store's exclusive lock); the log stays for the next checkpoint\n")
	case !checkpointed.Complete():
		l.reportf("loop: the closing checkpoint copied %d of %d log frames; the rest stay in the write-ahead log\n",
			checkpointed.CopiedFrames, checkpointed.LogFrames)
	}
}
