package storelog

import (
	"context"
	"encoding/json"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// CheckpointNotRestartedKind records a checkpoint cycle that ended
// log.not-restarted (neither restarted nor skipped for WriterBusy/PinBusy).
// A checkpoint that restarts the log appends nothing new and resets the loop's
// consecutive-cycle count.
const CheckpointNotRestartedKind = "drain.checkpoint-not-restarted"

func init() {
	if err := event.Register(CheckpointNotRestartedKind, drainmodel.Utility); err != nil {
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

// CheckpointInterval is how often a running Loop's heartbeat checkpoints the store.
const CheckpointInterval = time.Minute

// CheckpointAllowance is how far ahead the heartbeat just before a checkpoint announces the
// next pass.
const CheckpointAllowance = 30 * time.Second

// LogReportFrames is the write-ahead log length, in frames since the last restart (about
// 64 MiB at the store's 4 KiB page size), past which a Loop whose checkpoint cycles keep
// missing the log's restart reports it, and again each time the frame count doubles.
const LogReportFrames = 16384

// OpenWALKeeper takes checkpoint ownership of the store at path.
func OpenWALKeeper(ctx context.Context, path string) (*store.CheckpointOwner, error) {
	return store.CreateAndOwnCheckpoints(ctx, path)
}

// CheckpointRetryDelay answers how long CheckpointIfDue waits before retrying after
// notRestarted consecutive log.not-restarted cycles: 0 after the first miss so the next
// heartbeat tries again at once, and then exponential backoff (2s, 4s, 8s, 16s, 32s)
// capped at CheckpointInterval while misses repeat.
func CheckpointRetryDelay(notRestarted int, passInterval time.Duration) time.Duration {
	if notRestarted <= 1 {
		return 0
	}
	delay := passInterval
	for i := 1; i < notRestarted; i++ {
		if delay >= CheckpointInterval/2 {
			return CheckpointInterval
		}
		delay *= 2
	}
	return min(delay, CheckpointInterval)
}

// CheckpointDue reports whether a checkpoint cycle should run at now.
func CheckpointDue(hasKeeper, restartPending bool, lastCheckpoint time.Time, retryWait time.Duration, now time.Time) bool {
	if !hasKeeper {
		return false
	}
	if !restartPending {
		return now.Sub(lastCheckpoint) >= CheckpointInterval
	}
	return retryWait <= 0 || now.Sub(lastCheckpoint) >= retryWait
}

// RecordCheckpoint computes the updated checkpoint tracking state after one cycle.
func RecordCheckpoint(now time.Time, c store.Checkpointed, notRestarted int, passInterval time.Duration) (lastCheckpoint time.Time, restartPending bool, nextNotRestarted int, retryWait time.Duration, consecutive int) {
	lastCheckpoint, restartPending = now, !c.Restarted
	switch {
	case c.Restarted:
		return lastCheckpoint, restartPending, 0, 0, 0
	case c.WriterBusy || c.PinBusy:
		return lastCheckpoint, restartPending, notRestarted, 0, 0
	default:
		nextNotRestarted = notRestarted + 1
		retryWait = CheckpointRetryDelay(nextNotRestarted, passInterval)
		return lastCheckpoint, restartPending, nextNotRestarted, retryWait, nextNotRestarted
	}
}

// CheckpointParams supplies the state accessors and callbacks for CheckpointIfDue.
type CheckpointParams struct {
	Owner            *store.CheckpointOwner
	Due              bool
	Now              time.Time
	WaitClaimGate    func(ctx context.Context) error
	RefreshSession   func(ctx context.Context, next time.Time) error
	BeforeCheckpoint func()
	AfterCheckpoint  func(store.Checkpointed)
	RecordState      func(now time.Time, c store.Checkpointed) int
	RecordMissed     func(ctx context.Context, c store.Checkpointed, consecutive int, at time.Time)
	Report           func(c store.Checkpointed)
}

// CheckpointIfDue runs one checkpoint cycle when due, using callbacks for
// claim-gate waiting, session refresh, state recording, and reporting.
func CheckpointIfDue(ctx context.Context, p CheckpointParams) error {
	if !p.Due || p.Owner == nil {
		return nil
	}
	if p.WaitClaimGate != nil {
		if err := p.WaitClaimGate(ctx); err != nil && ctx.Err() != nil {
			return err
		}
	}
	if p.RefreshSession != nil {
		if err := p.RefreshSession(ctx, p.Now.Add(CheckpointAllowance)); err != nil {
			return err
		}
	}
	if p.BeforeCheckpoint != nil {
		p.BeforeCheckpoint()
	}
	c, err := p.Owner.Checkpoint(ctx)
	if p.AfterCheckpoint != nil {
		p.AfterCheckpoint(c)
	}
	if err != nil {
		return err
	}
	var consecutive int
	if p.RecordState != nil {
		consecutive = p.RecordState(p.Now, c)
	}
	if consecutive > 0 && p.RecordMissed != nil {
		p.RecordMissed(ctx, c, consecutive, p.Now.UTC())
	}
	if p.Report != nil {
		p.Report(c)
	}
	return nil
}

// RecordCheckpointNotRestarted appends one CheckpointNotRestartedKind event for a
// checkpoint cycle that ended log.not-restarted. A failed write is logged and never
// faults the loop.
func RecordCheckpointNotRestarted(ctx context.Context, storePath string, initialized bool, sessionOptions []store.OpenOption, sessionID string, c store.Checkpointed, consecutive int, at time.Time, reportf func(string, ...any)) {
	id := sessionID
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
		if ctx.Err() == nil && reportf != nil {
			reportf("loop: the missed checkpoint restart was not recorded: %v\n", err)
		}
		return
	}
	s, err := store.Open(ctx, storePath, !initialized, sessionOptions...)
	if err != nil {
		if ctx.Err() == nil && reportf != nil {
			reportf("loop: the missed checkpoint restart was not recorded: %v\n", err)
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
	if err != nil && ctx.Err() == nil && reportf != nil {
		reportf("loop: the missed checkpoint restart was not recorded: %v\n", err)
	}
}

// ReportCheckpoint reports one checkpoint cycle's outcome and returns the updated
// missedRestarts and reportedLogFrames counters.
func ReportCheckpoint(c store.Checkpointed, missedRestarts, reportedLogFrames int, reportf func(string, ...any)) (int, int) {
	switch {
	case c.Restarted:
		if missedRestarts > 0 && reportf != nil {
			reportf("loop: the write-ahead log restarted after %d missed checkpoint cycles (log file %d bytes)\n", missedRestarts, c.LogBytes)
		}
		return 0, 0
	case c.WriterBusy:
		if reportf != nil {
			reportf("loop: the checkpoint's own write waited out SQLite's busy timeout behind another writer (TILLR_SQLITE_TRACE=1 names it); the cycle is skipped and tried again at the next heartbeat\n")
		}
	case c.PinBusy:
		if reportf != nil {
			reportf("loop: the checkpoint's read pin waited out SQLite's busy timeout (another connection held the store's exclusive lock, as a recovery does); the cycle is skipped and tried again at the next heartbeat\n")
		}
	case missedRestarts == 0:
		if reportf != nil {
			reportf("loop: the checkpoint did not restart the write-ahead log (%d log frames, %d copied, log file %d bytes): a commit came between the checkpoint and the loop's write, or a reader held frames back; trying again at the next heartbeat\n", c.LogFrames, c.CopiedFrames, c.LogBytes)
		}
	}
	missedRestarts++
	if threshold := max(reportedLogFrames*2, LogReportFrames); c.LogFrames >= threshold {
		reportedLogFrames = c.LogFrames
		if reportf != nil {
			reportf("loop: the write-ahead log has grown to %d log frames (log file %d bytes) without a restart in %d checkpoint cycles: writers are leaving no gap for one\n", c.LogFrames, c.LogBytes, missedRestarts)
		}
	}
	return missedRestarts, reportedLogFrames
}

// CloseWALKeeper closes owner if non-nil and reports any incomplete closing checkpoint.
func CloseWALKeeper(owner *store.CheckpointOwner, reportf func(string, ...any)) {
	if owner == nil {
		return
	}
	checkpointed, err := owner.Close(context.Background())
	if reportf == nil {
		return
	}
	switch {
	case err != nil:
		reportf("loop: the closing checkpoint failed: %v\n", err)
	case checkpointed.WriterBusy:
		reportf("loop: the closing checkpoint's own write waited out SQLite's busy timeout behind another writer (TILLR_SQLITE_TRACE=1 names it); the log stays for the next checkpoint\n")
	case checkpointed.PinBusy:
		reportf("loop: the closing checkpoint's read pin waited out SQLite's busy timeout (another connection held the store's exclusive lock); the log stays for the next checkpoint\n")
	case !checkpointed.Complete():
		reportf("loop: the closing checkpoint copied %d of %d log frames; the rest stay in the write-ahead log\n",
			checkpointed.CopiedFrames, checkpointed.LogFrames)
	}
}
