package store

import (
	"context"
	"database/sql"
	"encoding/binary"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sync/atomic"
	"syscall"
)

// This file decides who checkpoints the store's write-ahead log (WAL), the copy of the
// log's frames back into the database file, and so who pays the flushes that go with it.
//
// Why it matters is where SQLite flushes the disk. With synchronous=NORMAL (dsn) a commit
// flushes nothing, and a checkpoint flushes the log and then the database without taking
// SQLite's writer. Three things flush inside a lock instead:
//
//   - The log restart. Once a checkpoint has copied every frame, the next writer starts the
//     log again at the top of the file and flushes the new header before writing any frame,
//     and a handle whose log descriptor is new (every short-lived face) flushes the store's
//     directory too, both while it holds SQLite's single write lock. Measured on this disk,
//     one flush has taken 2.1 s; every other writer waits behind them against its
//     five-second busy timeout.
//   - The log's creation, the same two flushes, by the first writer of a store with no log.
//   - A last close. A connection that closes as the store's last takes SQLite's exclusive
//     lock, checkpoints and deletes the log; every other connection's next lock waits.
//
// So no program checkpoints as a side effect of its own work:
//
//   - Every writable handle runs with wal_autocheckpoint=0 (dsn): no commit checkpoints.
//   - Every writable handle opens a read-only connection before its writer and closes it
//     after (the sidecar, a name this package coins). A read-only connection cannot take
//     the exclusive lock a closing checkpoint needs, so no handle's close is the store's
//     last: none checkpoints or deletes the log, and the log, once created, stays.
//
// Checkpoints happen in exactly three places, each as a checkpoint cycle (below):
//
//   - a checkpoint owner, a name this package coins for a process (a running drain loop)
//     that checkpoints the store while it runs, on its heartbeat and when it stops;
//   - a writable handle's close, when no owner runs and the log has reached CheckpointFrames
//     (checkpointIfDue), after the handle's own transactions committed;
//   - the maintenance checkpoint, Checkpoint (`tillr-store checkpoint`), or TruncateLog (its
//     --truncate), which refuse while an owner runs.
//
// A checkpoint cycle, a name this package coins, is what puts the one restart flush in the
// checkpointing program's own writer. First that program makes a small write, so the log
// holds at least one frame no checkpoint has copied. It then begins a read transaction on a
// read-only connection (the pin) and checkpoints PASSIVE (no writer lock) on its writer's
// connection, whose first flush of the log, and with it the directory's, happens there,
// outside every lock. A pin begun while some frame is uncopied holds one of SQLite's reader
// slots 1 to 4, and SQLite restarts the log only when it can take all four, so while the
// pin is held no other writer can restart it. (A pin begun on a fully copied log would take
// slot 0, which a restart does not need, and protect nothing; that is what the first write
// rules out.) The pin is released inside a second small write, which then restarts the log:
// one flush, the header, never the directory. The cycle leaves the log holding that write's
// frame alone, uncopied, so the database file holds every commit made before the cycle and
// no later writer finds a fully copied log to restart.
//
// The restart needs no commit between the pin and the restart write. A writer that commits
// in that window keeps the log appending; the cycle answers Restarted false, with the log's
// length, and its caller tries again later.
//
// The owner's presence is a shared flock on a lock file beside the store (lockPath). The
// kernel drops it when the owning process dies, so a crashed loop leaves no owner behind.
// Nothing makes the owner unique: two loops on one store are two owners, each of whose pins
// can defer the other's restart.
type CheckpointOwner struct {
	lock    *os.File // nil when the lock file could not be opened (lockErr)
	lockErr error
	writer  *Store
	keeper  *Store
}

// Checkpointed is what one checkpoint cycle did. Busy, LogFrames and CopiedFrames are the
// PASSIVE checkpoint's answer, as SQLite's wal_checkpoint pragma gives it: whether another
// checkpoint held the checkpoint lock, the frames in the log when it ran, and the frames
// copied into the database. Restarted is whether the cycle's restart write restarted the
// log; WriterBusy is whether one of the cycle's writes waited out SQLite's busy timeout
// behind another writer, which skips the rest of the cycle. PinBusy is whether the pin's
// read waited out the busy timeout, as a read does while another connection holds SQLite's
// exclusive lock (a recovery, or a store's last close); it skips the checkpoint and the
// restart write. LogBytes is the log file's size afterwards: SQLite does not shrink the
// file when it restarts it, so it is the largest the log has been since it was created,
// unless Truncated says TruncateLog emptied it.
type Checkpointed struct {
	Busy         bool
	LogFrames    int
	CopiedFrames int
	Restarted    bool
	WriterBusy   bool
	PinBusy      bool
	Truncated    bool
	LogBytes     int64
	ran          bool // whether the checkpoint ran at all
}

// Complete answers whether the checkpoint ran and the database file then held every frame
// the log held when it ran.
func (c Checkpointed) Complete() bool {
	return c.ran && !c.Busy && c.LogFrames == c.CopiedFrames
}

// CheckpointFrames is the log length, in frames, at which a writable handle's close runs a
// checkpoint cycle when no owner runs: SQLite's own default automatic checkpoint, 1000
// pages, about 4 MiB at the store's 4 KiB page size. With no loop running the log is
// copied no less often than SQLite would have copied it; the difference is who pays.
const CheckpointFrames = 1000

// ErrOwnerRunning is the maintenance checkpoint's refusal while a checkpoint owner runs, or
// while another program's close is running its own cycle: both hold the owner's lock file
// (lockPath), and a flock cannot say which kind of holder it met.
var ErrOwnerRunning = errors.New("store: a drain loop owns this store's checkpoints (it checkpoints on its heartbeat and when it stops), or another program is running a checkpoint cycle now")

// afterCheckpoint is a test seam: it runs between a cycle's checkpoint and its restart write.
var afterCheckpoint func()

// beforePin is a test seam: an error it answers is the pin's, as if the pin's read had
// answered it.
var beforePin func() error

// checkpointWork counts entries into this file's checkpoint path: every function that reads
// the log, probes or opens the owner's lock, decides whether a checkpoint is due, or runs a
// cycle. It is a test seam, so a test can say WriteTx does none of that work by a count that
// background allocation and scheduling cannot move.
var checkpointWork atomic.Int64

// lockPath names the checkpoint owner's lock file. It is resolved through symlinks the way
// SQLite resolves the database's name before naming its -wal and -shm files, so every
// spelling of one store finds one lock.
func lockPath(path string) string {
	if resolved, err := filepath.EvalSymlinks(path); err == nil {
		path = resolved
	}
	return path + "-checkpoint-owner"
}

// openOwnerLock opens the owner's lock file for the store at path, creating it if it is
// missing with the database file's permissions rather than the process's umask, so a loop
// started under a tight umask cannot lock other users of the store out of it.
func openOwnerLock(path string) (*os.File, error) {
	checkpointWork.Add(1)
	name := lockPath(path)
	mode := fs.FileMode(0o644)
	if info, err := os.Stat(path); err == nil {
		mode = info.Mode().Perm()
	}
	f, err := os.OpenFile(name, os.O_RDONLY|os.O_CREATE|os.O_EXCL, mode)
	if err == nil {
		if err := f.Chmod(mode); err != nil {
			f.Close()
			return nil, fmt.Errorf("store: setting the checkpoint owner's lock file's mode: %w", err)
		}
		return f, nil
	}
	if !errors.Is(err, fs.ErrExist) {
		return nil, fmt.Errorf("store: creating the checkpoint owner's lock file: %w", err)
	}
	if f, err = os.Open(name); err != nil {
		return nil, fmt.Errorf("store: opening the checkpoint owner's lock file: %w", err)
	}
	return f, nil
}

// flock applies how to f, again if a signal interrupted it.
func flock(f *os.File, how int) error {
	for {
		err := syscall.Flock(int(f.Fd()), how)
		if !errors.Is(err, syscall.EINTR) {
			return err
		}
	}
}

// ownedBy answers whether a checkpoint owner holds lock. The probe is a non-blocking
// exclusive flock, released at once: it fails only while some owner holds its shared lock.
// Two probes at the same instant can make one of them read "owned" with no owner running;
// that costs one close its checkpoint and nothing else. With no lock file open (nil), no
// owner can be seen.
func ownedBy(lock *os.File) (bool, error) {
	checkpointWork.Add(1)
	if lock == nil {
		return false, nil
	}
	switch err := flock(lock, syscall.LOCK_EX|syscall.LOCK_NB); {
	case err == nil:
		return false, flock(lock, syscall.LOCK_UN)
	case errors.Is(err, syscall.EWOULDBLOCK):
		return true, nil
	default:
		return false, fmt.Errorf("store: probing the checkpoint owner's lock: %w", err)
	}
}

// claimCycle takes lock exclusively without waiting, for the length of a close's cycle, and
// answers whether the close may run its cycle. It may not (false, nil) while an owner holds
// its shared lock or another close holds the exclusive one for its own cycle: two cycles at
// once defeat each other's restart, because each one's pin holds a reader slot the other's
// restart write needs. That second close skips, and writes no record; the retry is the next
// close's, as lastCycleAllows decides from the record the running cycle leaves. A loop that
// starts meanwhile waits for its shared lock until the claimed cycle ends. With no lock file
// open (nil), or a flock that fails for another reason (answered as the error), the cycle
// runs unclaimed, as it would on a store whose owner nobody can see. The caller releases a
// claimed lock (LOCK_UN) when its cycle ends.
func claimCycle(lock *os.File) (bool, error) {
	checkpointWork.Add(1)
	if lock == nil {
		return true, nil
	}
	switch err := flock(lock, syscall.LOCK_EX|syscall.LOCK_NB); {
	case err == nil:
		return true, nil
	case errors.Is(err, syscall.EWOULDBLOCK):
		return false, nil
	default:
		return true, fmt.Errorf("store: claiming the checkpoint owner's lock: %w", err)
	}
}

// OwnerRunning answers whether a checkpoint owner holds the store at path, or another
// program's close is running its cycle under the same lock (claimCycle). A store with no
// lock file has never had one.
func OwnerRunning(path string) (bool, error) {
	checkpointWork.Add(1)
	f, err := os.Open(lockPath(path))
	if errors.Is(err, fs.ErrNotExist) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: opening the checkpoint owner's lock file: %w", err)
	}
	defer f.Close()
	return ownedBy(f)
}

// IsBusy answers whether err is SQLite's SQLITE_BUSY (5, with any extended code) or
// SQLITE_PROTOCOL (15, with any extended code): a connection waited out its busy timeout
// for a lock another connection held, or hit SQLite's WAL locking-protocol race.
func IsBusy(err error) bool {
	var e interface{ Code() int }
	if !errors.As(err, &e) {
		return false
	}
	code := e.Code() & 0xff
	return code == 5 || code == 15
}

// OwnCheckpoints makes this process a checkpoint owner of the existing store at path, and
// answers the owner. It takes the shared lock before it opens its handles. A lock file it
// cannot open does not stop it: it then checkpoints as an owner nobody else can see
// (LockError), and other handles' closes go on checkpointing past CheckpointFrames.
func OwnCheckpoints(ctx context.Context, path string, options ...OpenOption) (*CheckpointOwner, error) {
	return ownCheckpoints(ctx, path, false, nil, options...)
}

// CreateAndOwnCheckpoints is OwnCheckpoints for a program that will create the store if
// it is missing, as a drain loop's first session write does: a missing store is created
// as an empty WAL database, not migrated, so the migration that follows (Open with create)
// runs while this owner already holds the store. A loop's startup is then never a stretch
// with no owner, in which other programs' closes would checkpoint.
func CreateAndOwnCheckpoints(ctx context.Context, path string, options ...OpenOption) (*CheckpointOwner, error) {
	return ownCheckpoints(ctx, path, true, nil, options...)
}

// ownCheckpoints makes the owner. held, when not nil, is the owner's lock file already
// locked exclusively by the caller (the maintenance checkpoint), which the owner then holds
// in place of a shared lock: a second descriptor's shared lock would wait on the first's
// exclusive one forever, because flocks on two open files conflict even in one process.
// On failure the owner releases held too.
func ownCheckpoints(ctx context.Context, path string, create bool, held *os.File, options ...OpenOption) (*CheckpointOwner, error) {
	if _, err := storeOpenConfig(options); err != nil {
		if held != nil {
			held.Close()
		}
		return nil, err
	}
	missing := false
	if _, err := os.Stat(path); err != nil {
		if !create || !errors.Is(err, os.ErrNotExist) {
			if held != nil {
				held.Close()
			}
			return nil, fmt.Errorf("store: %w", err)
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			return nil, fmt.Errorf("store: creating the store directory: %w", err)
		}
		missing = true
	}
	o := &CheckpointOwner{}
	if held != nil {
		o.lock = held
	} else if o.lock, o.lockErr = openOwnerLock(path); o.lock != nil {
		if err := flock(o.lock, syscall.LOCK_SH); err != nil {
			o.lock.Close()
			o.lock, o.lockErr = nil, fmt.Errorf("store: taking the checkpoint owner's lock: %w", err)
		}
	}
	var err error
	if missing {
		// The writer creates the file and selects WAL; the keeper can then attach to it.
		o.writer, err = open(ctx, path, true, false, options...)
	}
	if err == nil {
		if o.keeper, err = OpenRead(ctx, path, options...); err == nil {
			// The read maps the log's shared index, which attaches the keeper to it.
			err = o.keeper.ReadTx(ctx, func(r *Reader) error {
				var n int
				return r.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n)
			})
		}
	}
	if err == nil && o.writer == nil {
		o.writer, err = Open(ctx, path, false, options...)
	}
	if err != nil {
		o.release()
		return nil, err
	}
	// The owner checkpoints by its cycles, never by its writer's close.
	o.writer.owner = true
	return o, nil
}

// LockError answers why other handles cannot see this owner, or nil when they can.
func (o *CheckpointOwner) LockError() error { return o.lockErr }

// restartKey is the meta row a cycle's writes change. A write must change a row's bytes:
// SQLite commits an update that leaves a page as it was without writing a frame, and only
// a commit that writes a frame restarts the log or leaves a frame to pin behind.
const restartKey = "checkpoint-owner.writes"

// lastCycleKey is the meta row where a cycle's restart write records the log it found: its
// salt and its frames, read just before that write. A cycle cannot restart the log while
// another program is reading it or appending to it, so under continuous use the log stays
// past CheckpointFrames; without this record every close would then run another cycle,
// each with its own flushes and two more writes. A close cycles again only once the log
// has restarted since (another salt) or has grown CheckpointFrames past the record
// (lastCycleAllows).
const lastCycleKey = "checkpoint.last-cycle"

// ownerCycle is the owner's checkpoint cycle: its writer, its keeper as the pin, and the
// names its two writes are traced under.
func (o *CheckpointOwner) ownerCycle() cycle {
	return cycle{writer: o.writer, pin: o.keeper.db, preWrite: "checkpoint-owner.pre-write", restartWrite: "checkpoint-owner.restart-write"}
}

// Checkpoint runs one owner cycle. Nothing here holds SQLite's writer except the owner's
// two small writes, and the only flush either makes is the restart's header.
//
// A cycle that did not restart the log answers Restarted false and is not an error. Neither
// is one whose write waited out the busy timeout behind another writer (WriterBusy): a
// checkpoint is maintenance, and the next cycle tries again. The trace of the writer it
// waited behind, if that process traces, is the span whose acquired-to-end interval covers
// the owner's write's begin.
//
// With TILLR_SQLITE_TRACE=1 the cycle is the span "checkpoint-owner.checkpoint" (phases
// pin.begin, pin.end, checkpoint.begin, then checkpoint.complete, .partial or .busy, then
// log.restarted, log.not-restarted, writer.busy or pin.busy, which carries the log's frames
// and bytes; a busy pin has no checkpoint phases). The owner's writes are their own write spans inside it,
// "checkpoint-owner.pre-write" and "checkpoint-owner.restart-write", the only two that
// hold SQLite's writer.
func (o *CheckpointOwner) Checkpoint(ctx context.Context) (Checkpointed, error) {
	trace := startSQLiteTrace(o.writer.path, "checkpoint-owner.checkpoint")
	c, err := o.ownerCycle().run(ctx, trace)
	trace.finish(err)
	return c, err
}

// Close runs the owner's closing cycle, then releases ownership and closes the writer and
// then the keeper. The closing cycle leaves the database file holding everything committed
// before it, including the loop's last session write, and the log holding only the
// cycle's own counter row. A cycle that could not copy everything (another checkpoint held
// its lock, a reader's older snapshot held frames back, or its write was busy) is answered,
// not an error: the frames are safe in the log, and the next checkpoint copies them.
//
// With TILLR_SQLITE_TRACE=1 it is one span, "checkpoint-owner.close" (the cycle's phases
// and writes, then owner-lock.released, close.begin, close.end), so a slow closing
// checkpoint is attributed rather than lost. A program flushes the trace after this
// returns (FlushSQLiteTraceAtExit).
func (o *CheckpointOwner) Close(ctx context.Context) (Checkpointed, error) {
	trace := startSQLiteTrace(o.writer.path, "checkpoint-owner.close")
	checkpointed, err := o.ownerCycle().run(ctx, trace)
	if o.lock != nil {
		flock(o.lock, syscall.LOCK_UN)
	}
	trace.record("owner-lock.released", nil)
	trace.record("close.begin", nil)
	o.release()
	trace.record("close.end", nil)
	trace.finish(err)
	return checkpointed, err
}

// release closes whatever the owner holds: the writer, the keeper last, then the lock.
func (o *CheckpointOwner) release() {
	if o.writer != nil {
		o.writer.Close()
	}
	if o.keeper != nil {
		o.keeper.Close()
	}
	if o.lock != nil {
		o.lock.Close()
	}
}

// Checkpoint is the maintenance checkpoint, `tillr-store checkpoint`: one checkpoint cycle
// on the store at path, as a brief owner, so that the database file alone holds every
// commit made before it. It refuses with ErrOwnerRunning while a drain loop owns the
// store's checkpoints or another program's close is running its cycle; it holds the owner's
// lock exclusively for its own cycle, so neither can start meanwhile. Its flushes delay only
// this caller, except the restart write's one header flush, which a writer arriving then
// waits behind.
//
// With TILLR_SQLITE_TRACE=1 it is the span "store.checkpoint" (owner.absent or
// owner.running), around the owner's own spans.
func Checkpoint(ctx context.Context, path string) (result Checkpointed, resultErr error) {
	trace := startSQLiteTrace(path, "store.checkpoint")
	defer func() { trace.finish(resultErr) }()
	owner, err := claimMaintenance(ctx, path, trace)
	if err != nil {
		return Checkpointed{}, err
	}
	return owner.Close(ctx)
}

// claimMaintenance makes the maintenance checkpoint's brief owner: it takes the owner's lock
// file exclusively without waiting, refusing with ErrOwnerRunning when an owner or a close's
// cycle holds it, and the owner then holds that lock until it closes.
func claimMaintenance(ctx context.Context, path string, trace *sqliteTraceSpan) (*CheckpointOwner, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	lock, err := openOwnerLock(path)
	if err != nil {
		return nil, err
	}
	claimed, err := claimCycle(lock)
	if err != nil {
		lock.Close()
		return nil, err
	}
	if !claimed {
		lock.Close()
		trace.record("owner.running", nil)
		return nil, ErrOwnerRunning
	}
	trace.record("owner.absent", nil)
	return ownCheckpoints(ctx, path, false, lock)
}

// ErrStoreInUse is TruncateLog's refusal while another process has the store open.
var ErrStoreInUse = errors.New("store: another process has the store open")

// TruncateLog is `tillr-store checkpoint --truncate`: it copies the whole log into the
// database file and shrinks the log file to one frame, which a restart never does (the file
// otherwise stays as large as the longest log it has held). It refuses with ErrOwnerRunning
// as Checkpoint does, and with ErrStoreInUse (naming the processes) while any other process
// holds a lock on the store's database or shared-memory file, as every process with the
// store open does; it never waits for one. So the only writer that can be queued behind its
// flushes is a program that opened the store in the instant between that check and the
// truncate, and that program's lock then makes the truncate answer busy at once (Busy,
// Truncated false) rather than wait: the truncate runs with no busy timeout.
//
// After the truncate it commits the cycle's counter row, so the log's new header, and its
// flush, are written by this call's own writer and not by the next program's; the log is
// left holding that one frame, uncopied, as a cycle leaves it.
//
// With TILLR_SQLITE_TRACE=1 it is the span "store.truncate" (owner.absent or owner.running,
// in-use or not-in-use, truncate.begin, truncate.end, and the counter row's write span).
func TruncateLog(ctx context.Context, path string) (result Checkpointed, resultErr error) {
	trace := startSQLiteTrace(path, "store.truncate")
	defer func() { trace.finish(resultErr) }()
	owner, err := claimMaintenance(ctx, path, trace)
	if err != nil {
		return Checkpointed{}, err
	}
	defer owner.release()
	holders, err := otherProcessesHolding(path)
	if err != nil {
		return Checkpointed{}, fmt.Errorf("store: telling whether another process has the store open: %w", err)
	}
	if len(holders) > 0 {
		trace.recordDetail("in-use", fmt.Sprintf("pids=%v", holders), nil)
		return Checkpointed{}, fmt.Errorf("%w (processes %v)", ErrStoreInUse, holders)
	}
	trace.record("not-in-use", nil)
	var c Checkpointed
	var busy int
	db := owner.writer.db
	trace.record("truncate.begin", nil)
	_, err = db.ExecContext(ctx, `PRAGMA busy_timeout=0`)
	if err == nil {
		err = db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&busy, &c.LogFrames, &c.CopiedFrames)
	}
	trace.record("truncate.end", err)
	if _, resetErr := db.ExecContext(ctx, fmt.Sprintf("PRAGMA busy_timeout=%d", owner.writer.busyTimeout.Milliseconds())); err == nil {
		err = resetErr
	}
	if err != nil {
		return Checkpointed{}, fmt.Errorf("store: truncating the log: %w", err)
	}
	c.ran, c.Busy = true, busy != 0
	c.Truncated = !c.Busy && logBytes(path) == 0
	if c.Truncated {
		busy, err := owner.ownerCycle().write(ctx, "store.truncate.write", nil)
		if err != nil {
			return c, err
		}
		c.WriterBusy = busy
	}
	c.LogBytes = logBytes(path)
	return c, nil
}

// checkpointIfDue is a writable handle's closing check, run after its own transactions
// committed and before it closes its writer: when no owner runs and the log has reached
// CheckpointFrames, the handle runs one checkpoint cycle, pinned on its own sidecar. A busy
// cycle is skipped (the next close tries again); only another failure is an error.
//
// It records one phase into the close's span: log-checkpoint.not-due (with the log's
// frames), log-checkpoint.skipped (with the reason) or log-checkpoint.due, which is
// followed by the cycle's own span, "log-checkpoint".
func (s *Store) checkpointIfDue(ctx context.Context, trace *sqliteTraceSpan) error {
	checkpointWork.Add(1)
	if s.owner || s.sidecar == nil {
		if trace != nil {
			reason := "reason=owner-writer"
			if s.sidecar == nil {
				reason = "reason=no-sidecar"
			}
			trace.recordDetail("log-checkpoint.skipped", reason, nil)
		}
		return nil
	}
	log := ReadLog(s.path)
	if !log.Known || log.Frames < CheckpointFrames {
		if trace != nil {
			trace.recordDetail("log-checkpoint.not-due", fmt.Sprintf("log_frames=%d known=%t", log.Frames, log.Known), nil)
		}
		return nil
	}
	claimed, probeErr := claimCycle(s.lock)
	if !claimed {
		if trace != nil {
			trace.recordDetail("log-checkpoint.skipped", "reason=owner-or-cycle-running", nil)
		}
		return nil
	}
	if probeErr == nil && s.lock != nil {
		defer flock(s.lock, syscall.LOCK_UN)
	}
	if !s.lastCycleAllows(ctx, log) {
		if trace != nil {
			trace.recordDetail("log-checkpoint.skipped", fmt.Sprintf("reason=cycled-at-this-log log_frames=%d", log.Frames), nil)
		}
		return nil
	}
	trace.record("log-checkpoint.due", probeErr)
	span := startSQLiteTrace(s.path, "log-checkpoint")
	_, err := cycle{writer: s, pin: s.sidecar, preWrite: "log-checkpoint.pre-write", restartWrite: "log-checkpoint.restart-write"}.run(ctx, span)
	span.finish(err)
	return err
}

// lastCycleAllows answers whether a close may run a cycle on log, given the last cycle's
// record (lastCycleKey), read through the handle's sidecar: a read, which waits behind no
// writer. No record, or one it cannot read, allows the cycle.
func (s *Store) lastCycleAllows(ctx context.Context, log LogState) bool {
	checkpointWork.Add(1)
	var value string
	if err := s.sidecar.QueryRowContext(ctx, `SELECT value FROM meta WHERE key = ?`, lastCycleKey).Scan(&value); err != nil {
		return true
	}
	var salt uint32
	var frames int
	if _, err := fmt.Sscanf(value, "%d %d", &salt, &frames); err != nil {
		return true
	}
	return salt != logSalt(s.path) || log.Frames-frames >= CheckpointFrames
}

// cycle is one checkpoint cycle's parts: the writer whose connection writes and
// checkpoints, the read-only connection the pin is begun on, and the trace names of the
// two writes.
type cycle struct {
	writer                 *Store
	pin                    *sql.DB
	preWrite, restartWrite string
}

// run runs the cycle and records its outcome into trace, with the log's frames and bytes.
func (y cycle) run(ctx context.Context, trace *sqliteTraceSpan) (Checkpointed, error) {
	checkpointWork.Add(1)
	c, err := y.steps(ctx, trace)
	if err == nil {
		c.LogBytes = logBytes(y.writer.path)
		if trace != nil {
			trace.recordDetail(outcomePhase(c), fmt.Sprintf("log_frames=%d copied_frames=%d log_bytes=%d", c.LogFrames, c.CopiedFrames, c.LogBytes), nil)
		}
	}
	return c, err
}

func (y cycle) steps(ctx context.Context, trace *sqliteTraceSpan) (Checkpointed, error) {
	checkpointWork.Add(1)
	var c Checkpointed
	if busy, err := y.write(ctx, y.preWrite, nil); busy || err != nil {
		c.WriterBusy = busy
		return c, err
	}
	pin, c, err := y.pinAndCheckpoint(ctx, trace)
	if pin != nil {
		defer pin.Rollback()
	}
	if err != nil || c.PinBusy {
		return c, err
	}
	if afterCheckpoint != nil {
		afterCheckpoint()
	}
	salt := logSalt(y.writer.path)
	// Read here, before the write takes the writer, so the record costs the writer only its
	// own row (lastCycleKey).
	at := fmt.Sprintf("%d %d", salt, ReadLog(y.writer.path).Frames)
	busy, err := y.write(ctx, y.restartWrite, func(tx *Tx) error {
		// The writer holds SQLite's write lock now, so no other writer can commit before
		// this transaction does; releasing the pin lets this commit take every reader slot.
		if err := pin.Rollback(); err != nil {
			return fmt.Errorf("store: releasing the log's pin: %w", err)
		}
		_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`, lastCycleKey, at)
		return err
	})
	c.WriterBusy = busy
	if err != nil {
		return c, err
	}
	c.Restarted = !busy && logSalt(y.writer.path) != salt
	return c, nil
}

// write commits the cycle's counter row, after running inside (with the writer held) if it
// is not nil. It answers busy, and no error, when the transaction waited out SQLite's busy
// timeout for the writer.
func (y cycle) write(ctx context.Context, operation string, inside func(*Tx) error) (bool, error) {
	checkpointWork.Add(1)
	err := y.writer.writeTx(ctx, operation, func(tx *Tx) error {
		if inside != nil {
			if err := inside(tx); err != nil {
				return err
			}
		}
		_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, '1')
		 ON CONFLICT(key) DO UPDATE SET value = CAST(meta.value AS INTEGER) + 1`, restartKey)
		return err
	})
	if IsBusy(err) {
		return true, nil
	}
	if err != nil {
		return false, fmt.Errorf("store: the checkpoint cycle's write (%s): %w", operation, err)
	}
	return false, nil
}

// pinAndCheckpoint begins the pin and runs the checkpoint under it, recording each step into
// trace. It answers the pin, still open, whenever it began.
func (y cycle) pinAndCheckpoint(ctx context.Context, trace *sqliteTraceSpan) (*sql.Tx, Checkpointed, error) {
	checkpointWork.Add(1)
	trace.record("pin.begin", nil)
	pin, err := y.pin.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err == nil && beforePin != nil {
		err = beforePin()
	}
	if err == nil {
		var n int
		err = pin.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master`).Scan(&n)
	}
	trace.record("pin.end", err)
	if IsBusy(err) {
		// Maintenance, like a busy write: the cycle is skipped and its caller tries again.
		return pin, Checkpointed{PinBusy: true}, nil
	}
	if err != nil {
		return pin, Checkpointed{}, fmt.Errorf("store: pinning the log: %w", err)
	}
	trace.record("checkpoint.begin", nil)
	checkpointed, err := y.writer.checkpoint(ctx)
	if err == nil {
		trace.record(checkpointPhase(checkpointed), nil)
	}
	return pin, checkpointed, err
}

// checkpoint runs one PASSIVE checkpoint: it takes SQLite's checkpoint lock but never its
// write lock, so its flushes delay only its caller.
func (s *Store) checkpoint(ctx context.Context) (Checkpointed, error) {
	checkpointWork.Add(1)
	var busy int
	var c Checkpointed
	if err := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &c.LogFrames, &c.CopiedFrames); err != nil {
		return Checkpointed{}, fmt.Errorf("store: checkpointing the log: %w", err)
	}
	c.Busy, c.ran = busy != 0, true
	return c, nil
}

// checkpointPhase names, for the trace, what one checkpoint achieved.
func checkpointPhase(c Checkpointed) string {
	switch {
	case c.Busy:
		return "checkpoint.busy"
	case c.Complete():
		return "checkpoint.complete"
	default:
		return "checkpoint.partial"
	}
}

// outcomePhase names, for the trace, how one cycle ended.
func outcomePhase(c Checkpointed) string {
	switch {
	case c.WriterBusy:
		return "writer.busy"
	case c.PinBusy:
		return "pin.busy"
	case c.Restarted:
		return "log.restarted"
	default:
		return "log.not-restarted"
	}
}

// LogState is the write-ahead log as its own file shows it: Frames is how many frames have
// been written since the log last restarted, Bytes the file's size (the largest the log
// has been since it was created). Known is false when there is no log or its header is
// not one SQLite wrote, and then Frames is zero.
//
// Frames counts what was written, not only what committed: a transaction that spilled
// frames and then rolled back leaves them until the next commit overwrites them. It is
// therefore an upper bound on the log's committed length, which is all a "has the log
// grown past CheckpointFrames" decision needs. How much of the log a checkpoint copied is
// in the checkpoint's own answer (Checkpointed), not here.
type LogState struct {
	Frames int
	Bytes  int64
	Known  bool
}

// ReadLog reads the log's state from the -wal file alone, without opening the store or
// taking any lock (https://www.sqlite.org/walformat.html, the WAL header and frame
// headers, big-endian). It never opens the shared-memory (-shm) file: SQLite's locks live
// on that file as POSIX record locks, which belong to the process, so closing any
// descriptor of it would drop every lock this process's connections hold there. Another
// process would then see none attached, truncate the file under this one's mapping, and
// this one's next access would fault (SQLITE_IOERR_IN_PAGE). SQLite takes no lock on the
// -wal file, so opening and closing it here is safe.
//
// A restart writes a new header with new salts and overwrites frames from the top, so the
// frames carrying the header's salts are exactly a prefix of the file; a binary search over
// frame headers finds its end in a few small reads.
func ReadLog(path string) LogState {
	checkpointWork.Add(1)
	state := LogState{Bytes: logBytes(path)}
	f, err := os.Open(path + "-wal")
	if err != nil {
		return state
	}
	defer f.Close()
	var header [32]byte
	if _, err := f.ReadAt(header[:], 0); err != nil {
		return state
	}
	if magic := binary.BigEndian.Uint32(header[0:4]); magic&^1 != 0x377f0682 {
		return state
	}
	pageSize := int64(binary.BigEndian.Uint32(header[8:12]))
	if pageSize == 1 { // the encoding of 65536
		pageSize = 65536
	}
	if pageSize < 512 || pageSize&(pageSize-1) != 0 {
		return state
	}
	salts := header[16:24]
	frameSize := 24 + pageSize
	current := func(frame int64) bool {
		var h [24]byte
		if _, err := f.ReadAt(h[:], 32+(frame-1)*frameSize); err != nil {
			return false
		}
		return string(h[8:16]) == string(salts)
	}
	// Frames 1..lo carry the header's salts; frame hi+1 does not (or is past the end).
	lo, hi := int64(0), (state.Bytes-32)/frameSize
	for lo < hi {
		mid := lo + (hi-lo+1)/2
		if current(mid) {
			lo = mid
		} else {
			hi = mid - 1
		}
	}
	state.Frames, state.Known = int(lo), true
	return state
}

// logSalt answers the first salt in the log's header, which SQLite changes each time a
// writer restarts the log at the top of the file (https://www.sqlite.org/walformat.html,
// the WAL header), or 0 when there is no header to read.
func logSalt(path string) uint32 {
	checkpointWork.Add(1)
	f, err := os.Open(path + "-wal")
	if err != nil {
		return 0
	}
	defer f.Close()
	var header [32]byte
	if _, err := f.ReadAt(header[:], 0); err != nil {
		return 0
	}
	return binary.BigEndian.Uint32(header[16:20])
}

// logBytes answers the log file's size, or 0 when there is none.
func logBytes(path string) int64 {
	checkpointWork.Add(1)
	info, err := os.Stat(path + "-wal")
	if err != nil {
		return 0
	}
	return info.Size()
}
