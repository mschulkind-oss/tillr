package drain

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// CheckpointNotRestartedKind records a checkpoint cycle that ended
// log.not-restarted (neither restarted nor skipped for WriterBusy/PinBusy).
// A checkpoint that restarts the log appends nothing new and resets the loop's
// consecutive-cycle count.
const CheckpointNotRestartedKind = "drain.checkpoint-not-restarted"

func init() {
	if err := event.Register(CheckpointNotRestartedKind, Utility); err != nil {
		panic(err)
	}
}

// CheckpointNotRestartedFact is the payload of a CheckpointNotRestartedKind event.
type CheckpointNotRestartedFact struct {
	LogFrames         int    `json:"log_frames"`
	LogBytes          int64  `json:"log_bytes"`
	ConsecutiveCycles int    `json:"consecutive_cycles"`
	SessionID         string `json:"session_id,omitempty"`
}

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

// LogReportFrames is the write-ahead log length, in frames since the last restart (about
// 64 MiB at the store's 4 KiB page size), past which a Loop whose checkpoint cycles keep
// missing the log's restart reports it, and again each time the frame count doubles.
// Judging frames rather than the file's size avoids false warnings after a restart: SQLite
// keeps the -wal file at its historical peak size until explicitly truncated. A restart
// needs no commit between the owner's pin and its write; writers that never leave that gap
// keep the log growing, and this is how an operator hears about it.
const LogReportFrames = 16384

// checkpointRetryDelay answers how long checkpointIfDue waits before retrying after
// notRestarted consecutive log.not-restarted cycles: 0 after the first miss so the next
// heartbeat tries again at once, and then exponential backoff (2s, 4s, 8s, 16s, 32s)
// capped at CheckpointInterval while misses repeat.
func checkpointRetryDelay(notRestarted int) time.Duration {
	if notRestarted <= 1 {
		return 0
	}
	delay := PassInterval
	for i := 1; i < notRestarted; i++ {
		if delay >= CheckpointInterval/2 {
			return CheckpointInterval
		}
		delay *= 2
	}
	return min(delay, CheckpointInterval)
}

func (l *Loop) checkpointDueLocked(now time.Time) bool {
	if l.walKeeper == nil {
		return false
	}
	if !l.restartPending {
		return now.Sub(l.lastCheckpoint) >= CheckpointInterval
	}
	return l.retryWait <= 0 || now.Sub(l.lastCheckpoint) >= l.retryWait
}

func (l *Loop) recordCheckpointLocked(now time.Time, c store.Checkpointed) int {
	l.lastCheckpoint, l.restartPending = now, !c.Restarted
	switch {
	case c.Restarted:
		l.notRestarted, l.retryWait = 0, 0
		return 0
	case c.WriterBusy || c.PinBusy:
		l.retryWait = 0
		return 0
	default:
		l.notRestarted++
		l.retryWait = checkpointRetryDelay(l.notRestarted)
		return l.notRestarted
	}
}

// checkpointIfDue runs one checkpoint cycle when CheckpointInterval has passed since the
// last one, or when a retry is due after one that did not restart the log. It runs on the
// heartbeat's goroutine, after the heartbeat, so the heartbeat never waits on a checkpoint's
// flushes behind another goroutine; and it holds sessionMu only to read and record, never
// across the checkpoint, so a pass refreshing the session does not wait on the disk either.
//
// A cycle is maintenance. One whose own write waited out SQLite's busy timeout behind
// another writer, or whose pin's read did, is reported and tried again at the next
// heartbeat; one that did not restart the log is reported, tried again at the next
// heartbeat, and backed off up to CheckpointInterval while log.not-restarted repeats. Only
// an error that is none of these faults the loop.
func (l *Loop) checkpointIfDue(ctx context.Context) error {
	l.sessionMu.Lock()
	owner, now := l.walKeeper, l.cfg.Now()
	due := l.checkpointDueLocked(now)
	l.sessionMu.Unlock()
	if owner != nil && !l.reportedLockError && owner.LockError() != nil {
		l.reportedLockError = true
		l.reportf("loop: other programs cannot see this loop's checkpoint ownership, so they keep checkpointing themselves: %v\n", owner.LockError())
	}
	if !due {
		return nil
	}
	if err := claimGateFor(l.cfg.Store).wait(ctx); err != nil && ctx.Err() != nil {
		return err
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
	consecutive := l.recordCheckpointLocked(now, c)
	l.sessionMu.Unlock()
	if consecutive > 0 {
		l.recordCheckpointNotRestarted(ctx, c, consecutive, now.UTC())
	}
	l.reportCheckpoint(c)
	return nil
}

// recordCheckpointNotRestarted appends one CheckpointNotRestartedKind event for a
// checkpoint cycle that ended log.not-restarted. A failed write is logged and never
// faults the loop.
func (l *Loop) recordCheckpointNotRestarted(ctx context.Context, c store.Checkpointed, consecutive int, at time.Time) {
	l.mu.Lock()
	id := l.sessionID
	initialized := l.sessionInitialized
	l.mu.Unlock()
	if id == "" {
		id = "loop"
	}
	body, err := json.Marshal(CheckpointNotRestartedFact{
		LogFrames:         c.LogFrames,
		LogBytes:          c.LogBytes,
		ConsecutiveCycles: consecutive,
		SessionID:         id,
	})
	if err != nil {
		if ctx.Err() == nil {
			l.reportf("loop: the missed checkpoint restart was not recorded: %v\n", err)
		}
		return
	}
	s, err := store.Open(ctx, l.cfg.Store, !initialized, l.sessionOptions...)
	if err != nil {
		if ctx.Err() == nil {
			l.reportf("loop: the missed checkpoint restart was not recorded: %v\n", err)
		}
		return
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{
			Kind:       CheckpointNotRestartedKind,
			ObjectType: "session",
			ObjectID:   &id,
			Payload:    body,
			At:         at,
		})
		return err
	})
	if closeErr := s.Close(); err == nil {
		err = closeErr
	}
	if err != nil && ctx.Err() == nil {
		l.reportf("loop: the missed checkpoint restart was not recorded: %v\n", err)
	}
}

// reportCheckpoint says what an operator needs to hear about one cycle: a skipped cycle, the
// first miss after a restart, a log that has passed LogReportFrames (and each doubling after
// that), and the restart that ends a run of misses. It holds no lock.
func (l *Loop) reportCheckpoint(c store.Checkpointed) {
	switch {
	case c.Restarted:
		if l.missedRestarts > 0 {
			l.reportf("loop: the write-ahead log restarted after %d missed checkpoint cycles (log file %d bytes)\n", l.missedRestarts, c.LogBytes)
		}
		l.missedRestarts, l.reportedLogFrames = 0, 0
		return
	case c.WriterBusy:
		l.reportf("loop: the checkpoint's own write waited out SQLite's busy timeout behind another writer (TILLR_SQLITE_TRACE=1 names it); the cycle is skipped and tried again at the next heartbeat\n")
	case c.PinBusy:
		l.reportf("loop: the checkpoint's read pin waited out SQLite's busy timeout (another connection held the store's exclusive lock, as a recovery does); the cycle is skipped and tried again at the next heartbeat\n")
	case l.missedRestarts == 0:
		l.reportf("loop: the checkpoint did not restart the write-ahead log (%d log frames, %d copied, log file %d bytes): a commit came between the checkpoint and the loop's write, or a reader held frames back; trying again at the next heartbeat\n", c.LogFrames, c.CopiedFrames, c.LogBytes)
	}
	l.missedRestarts++
	if threshold := max(l.reportedLogFrames*2, LogReportFrames); c.LogFrames >= threshold {
		l.reportedLogFrames = c.LogFrames
		l.reportf("loop: the write-ahead log has grown to %d log frames (log file %d bytes) without a restart in %d checkpoint cycles: writers are leaving no gap for one\n", c.LogFrames, c.LogBytes, l.missedRestarts)
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
	l.restartPending, l.notRestarted, l.retryWait = false, 0, 0
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
