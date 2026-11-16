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
	"syscall"

	"modernc.org/sqlite"
)

// A checkpoint owner is a name this package coins: a process (a running drain loop) that
// copies the store's write-ahead log (WAL) back into the database while it runs, so that no
// other writer restarts the log.
//
// Why it matters is where SQLite flushes the disk. With synchronous=NORMAL (dsn) a commit
// flushes nothing, and a checkpoint flushes the log and then the database without taking
// SQLite's writer. What remains is the log restart: once a checkpoint has copied every
// frame, the next writer starts the log again at the top of the file and flushes the new
// header before writing any frame, and a handle whose log descriptor is new (every
// short-lived face) flushes the store's directory too. Both flushes happen while that
// writer holds SQLite's single write lock, so every other writer waits behind them, against
// its five-second busy timeout. Measured on this disk, one flush has taken 2.1 s.
//
// The header flush cannot be removed while keeping SQLite's protection against power loss
// (only synchronous=OFF or a filesystem SQLite trusts to write in order skips it), so the
// owner decides who pays it and when:
//
//   - While an owner holds the store, every other writable handle runs with
//     wal_autocheckpoint=0, decided again before each write transaction, so none of their
//     commits checkpoints.
//   - Each cycle (Checkpoint), the owner first makes a small write of its own, so the log
//     holds at least one frame no checkpoint has copied. It then begins a read transaction
//     on its keeper (the pin) and checkpoints PASSIVE (no writer lock) on its own long-lived
//     handle. A pin begun while some frame is uncopied holds one of SQLite's reader slots
//     1 to 4, and SQLite restarts the log only when it can take all four, so while the pin
//     is held no other writer can restart it. (A pin begun on a fully copied log would take
//     slot 0, which a restart does not need, and protect nothing; that is what the first
//     write rules out.) The pin is released inside the owner's restart write, which then
//     restarts the log: one flush, the header, never the directory.
//   - The restart needs no commit between the pin and the owner's write. A writer that
//     commits in that window keeps the log appending; the cycle answers Restarted false,
//     with the log's length, and the loop tries again at its next heartbeat.
//   - Closing the owner checkpoints the log, so the database file alone is current once the
//     loop has stopped, if no reader held frames back and until the next write. The keeper
//     closes last; it is read-only, so its close neither checkpoints nor deletes the log.
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

// Checkpointed is what one owner cycle did. Busy, LogFrames and CopiedFrames are the PASSIVE
// checkpoint's answer, as SQLite's wal_checkpoint pragma gives it: whether another checkpoint
// held the checkpoint lock, the frames in the log when it ran, and the frames copied into the
// database. Restarted is whether the owner's write restarted the log; WriterBusy is whether
// one of the owner's writes waited out SQLite's busy timeout behind another writer, which
// skips the cycle. LogBytes is the log file's size afterwards: SQLite does not shrink the
// file when it restarts it, so it is the largest the log has been since it was created.
type Checkpointed struct {
	Busy         bool
	LogFrames    int
	CopiedFrames int
	Restarted    bool
	WriterBusy   bool
	LogBytes     int64
}

// Complete answers whether the database file now holds every frame the log held when the
// checkpoint ran.
func (c Checkpointed) Complete() bool { return !c.Busy && c.LogFrames == c.CopiedFrames }

// afterCheckpoint is a test seam: it runs between the checkpoint and the owner's write.
var afterCheckpoint func()

// defaultAutoCheckpoint is SQLite's own default for wal_autocheckpoint, in log pages.
const defaultAutoCheckpoint = 1000

// The two policy pragmas, as constants so that deciding the policy formats nothing.
const (
	autoCheckpointOff     = `PRAGMA wal_autocheckpoint=0`
	autoCheckpointDefault = `PRAGMA wal_autocheckpoint=1000`
)

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
// that costs one commit its automatic checkpoint and nothing else. With no lock file open
// (nil), no owner can be seen.
func ownedBy(lock *os.File) (bool, error) {
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

// applyCheckpointPolicy sets wal_autocheckpoint on conn for the commits it is about to make:
// 0 while a checkpoint owner runs, SQLite's default otherwise. It runs before the write
// transaction begins, outside every SQLite lock: two flock calls on the lock file the handle
// opened once, and the pragma only when the decision differs from the one this connection
// last ran under. It answers whether an owner was seen; a failed probe is answered as no
// owner, with its error, because a store whose lock cannot be read is still a store to write.
func (s *Store) applyCheckpointPolicy(ctx context.Context, conn *sql.Conn) (owned bool, probeErr error, err error) {
	owned, probeErr = ownedBy(s.lock)
	pages := defaultAutoCheckpoint
	if owned {
		pages = 0
	}
	var driverConn any
	conn.Raw(func(c any) error { driverConn = c; return nil })
	if driverConn == s.policyConn && pages == s.policyPages {
		return owned, probeErr, nil
	}
	pragma := autoCheckpointDefault
	if owned {
		pragma = autoCheckpointOff
	}
	if _, err := conn.ExecContext(ctx, pragma); err != nil {
		s.policyConn = nil
		return owned, probeErr, fmt.Errorf("store: setting the automatic checkpoint: %w", err)
	}
	// The connection is held, so no other transaction on this handle reads these meanwhile.
	// Holding the driver connection keeps it from being freed, so a replacement connection
	// can never compare equal to it.
	s.policyConn, s.policyPages = driverConn, pages
	return owned, probeErr, nil
}

// applyPolicyOn decides the policy on conn and records it into trace: one phase naming the
// decision, carrying a failed probe's error (the decision is then "no owner").
func (s *Store) applyPolicyOn(ctx context.Context, conn *sql.Conn, trace *sqliteTraceSpan) error {
	owned, probeErr, err := s.applyCheckpointPolicy(ctx, conn)
	if err == nil {
		err = probeErr
	}
	trace.record(policyPhase(owned), err)
	if err != nil && err == probeErr {
		return nil
	}
	return err
}

// decidePolicy is applyPolicyOn for a handle being opened, on its one connection.
func (s *Store) decidePolicy(ctx context.Context, trace *sqliteTraceSpan) error {
	conn, err := s.db.Conn(ctx)
	if err != nil {
		return fmt.Errorf("store: setting the automatic checkpoint: %w", err)
	}
	defer conn.Close()
	return s.applyPolicyOn(ctx, conn, trace)
}

// policyPhase names, for the trace, the policy a handle's next commits run under.
func policyPhase(owned bool) string {
	if owned {
		return "autocheckpoint.off"
	}
	return "autocheckpoint.default"
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

// outcomePhase names, for the trace, how one owner cycle ended.
func outcomePhase(c Checkpointed) string {
	switch {
	case c.WriterBusy:
		return "writer.busy"
	case c.Restarted:
		return "log.restarted"
	default:
		return "log.not-restarted"
	}
}

// IsBusy answers whether err is SQLite's SQLITE_BUSY (5, with any extended code): a
// connection waited out its busy timeout for a lock another connection held.
func IsBusy(err error) bool {
	var e *sqlite.Error
	return errors.As(err, &e) && e.Code()&0xff == 5
}

// OwnCheckpoints makes this process a checkpoint owner of the existing store at path, and
// answers the owner. It takes the shared lock before it opens its handles, so its own
// writer is opened with automatic checkpoints off. A lock file it cannot open does not stop
// it: it then checkpoints as an owner nobody else can see (LockError), and other handles go
// on checkpointing by SQLite's default.
func OwnCheckpoints(ctx context.Context, path string) (*CheckpointOwner, error) {
	if _, err := os.Stat(path); err != nil {
		return nil, fmt.Errorf("store: %w", err)
	}
	o := &CheckpointOwner{}
	o.lock, o.lockErr = openOwnerLock(path)
	if o.lock != nil {
		if err := flock(o.lock, syscall.LOCK_SH); err != nil {
			o.lock.Close()
			o.lock, o.lockErr = nil, fmt.Errorf("store: taking the checkpoint owner's lock: %w", err)
		}
	}
	var err error
	if o.keeper, err = OpenRead(ctx, path); err == nil {
		// The read maps the log's shared index, which attaches the keeper to it.
		err = o.keeper.ReadTx(ctx, func(r *Reader) error {
			var n int
			return r.QueryRow(`SELECT count(*) FROM sqlite_master`).Scan(&n)
		})
	}
	if err == nil {
		o.writer, err = Open(ctx, path, false)
	}
	if err != nil {
		o.release()
		return nil, err
	}
	return o, nil
}

// LockError answers why other handles cannot see this owner, or nil when they can.
func (o *CheckpointOwner) LockError() error { return o.lockErr }

// restartKey is the meta row the owner's writes change. A write must change a row's
// bytes: SQLite commits an update that leaves a page as it was without writing a frame,
// and only a commit that writes a frame restarts the log or leaves a frame to pin behind.
const restartKey = "checkpoint-owner.writes"

// Checkpoint runs one owner cycle: the owner's first write, the pin, a PASSIVE checkpoint,
// and the owner's restart write. Nothing here holds SQLite's writer except the owner's two
// small writes, and the only flush either can make is the restart's header.
//
// A cycle that did not restart the log answers Restarted false and is not an error. Neither
// is one whose write waited out the busy timeout behind another writer (WriterBusy): a
// checkpoint is maintenance, and the next cycle tries again. The trace of the writer it
// waited behind, if that process traces, is the span whose acquired-to-end interval covers
// the owner's write's begin.
//
// With TILLR_SQLITE_TRACE=1 the cycle is the span "checkpoint-owner.checkpoint" (phases
// pin.begin, pin.end, checkpoint.begin, then checkpoint.complete, .partial or .busy, then
// log.restarted, log.not-restarted or writer.busy, which carries the log's frames and
// bytes). The owner's writes are their own write spans inside it,
// "checkpoint-owner.pre-write" and "checkpoint-owner.restart-write", the only two that
// hold SQLite's writer.
func (o *CheckpointOwner) Checkpoint(ctx context.Context) (Checkpointed, error) {
	trace := startSQLiteTrace(o.writer.path, "checkpoint-owner.checkpoint")
	c, err := o.cycle(ctx, trace)
	if err == nil {
		c.LogBytes = logBytes(o.writer.path)
		if trace != nil {
			trace.recordDetail(outcomePhase(c), fmt.Sprintf("log_frames=%d copied_frames=%d log_bytes=%d", c.LogFrames, c.CopiedFrames, c.LogBytes), nil)
		}
	}
	trace.finish(err)
	return c, err
}

func (o *CheckpointOwner) cycle(ctx context.Context, trace *sqliteTraceSpan) (Checkpointed, error) {
	var c Checkpointed
	if busy, err := o.write(ctx, "checkpoint-owner.pre-write", nil); busy || err != nil {
		c.WriterBusy = busy
		return c, err
	}
	pin, c, err := o.pinAndCheckpoint(ctx, trace)
	if pin != nil {
		defer pin.Rollback()
	}
	if err != nil {
		return c, err
	}
	if afterCheckpoint != nil {
		afterCheckpoint()
	}
	salt := logSalt(o.writer.path)
	busy, err := o.write(ctx, "checkpoint-owner.restart-write", func() error {
		// The writer holds SQLite's write lock now, so no other writer can commit before
		// this transaction does; releasing the pin lets this commit take every reader slot.
		if err := pin.Rollback(); err != nil {
			return fmt.Errorf("store: releasing the log's pin: %w", err)
		}
		return nil
	})
	c.WriterBusy = busy
	if err != nil {
		return c, err
	}
	c.Restarted = !busy && logSalt(o.writer.path) != salt
	return c, nil
}

// write commits the owner's counter row, after running inside (with the writer held) if it
// is not nil. It answers busy, and no error, when the transaction waited out SQLite's busy
// timeout for the writer.
func (o *CheckpointOwner) write(ctx context.Context, operation string, inside func() error) (bool, error) {
	err := o.writer.writeTx(ctx, operation, func(tx *Tx) error {
		if inside != nil {
			if err := inside(); err != nil {
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
		return false, fmt.Errorf("store: the checkpoint owner's write (%s): %w", operation, err)
	}
	return false, nil
}

// logSalt answers the first salt in the log's header, which SQLite changes each time a
// writer restarts the log at the top of the file (https://www.sqlite.org/walformat.html,
// the WAL header), or 0 when there is no header to read.
func logSalt(path string) uint32 {
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
	info, err := os.Stat(path + "-wal")
	if err != nil {
		return 0
	}
	return info.Size()
}

// pinAndCheckpoint begins the pin and runs the checkpoint under it, recording each step into
// trace. It answers the pin, still open, whenever it began.
func (o *CheckpointOwner) pinAndCheckpoint(ctx context.Context, trace *sqliteTraceSpan) (*sql.Tx, Checkpointed, error) {
	trace.record("pin.begin", nil)
	pin, err := o.keeper.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err == nil {
		var n int
		err = pin.QueryRowContext(ctx, `SELECT count(*) FROM sqlite_master`).Scan(&n)
	}
	trace.record("pin.end", err)
	if err != nil {
		return pin, Checkpointed{}, fmt.Errorf("store: pinning the log: %w", err)
	}
	trace.record("checkpoint.begin", nil)
	checkpointed, err := o.writer.checkpoint(ctx)
	if err == nil {
		trace.record(checkpointPhase(checkpointed), nil)
	}
	return pin, checkpointed, err
}

// Close releases ownership, checkpoints the log on the owner's writer, and closes the writer
// and then the keeper. Releasing first means a handle that commits from here on
// checkpoints by SQLite's default again. A checkpoint that was not complete (another
// checkpoint held its lock, or a reader's older snapshot held frames back) is answered, not
// an error: the frames are safe in the log, and the next checkpoint copies them.
//
// With TILLR_SQLITE_TRACE=1 it is one span, "checkpoint-owner.close" (phases
// owner-lock.released, checkpoint.begin, checkpoint.complete, .partial or .busy,
// close.begin, close.end), so a slow closing checkpoint is attributed rather than lost. A
// program flushes the trace after this returns (FlushSQLiteTraceAtExit).
func (o *CheckpointOwner) Close(ctx context.Context) (Checkpointed, error) {
	trace := startSQLiteTrace(o.writer.path, "checkpoint-owner.close")
	if o.lock != nil {
		flock(o.lock, syscall.LOCK_UN)
	}
	trace.record("owner-lock.released", nil)
	trace.record("checkpoint.begin", nil)
	checkpointed, err := o.writer.checkpoint(ctx)
	if err == nil {
		trace.record(checkpointPhase(checkpointed), nil)
	}
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

// checkpoint runs one PASSIVE checkpoint: it takes SQLite's checkpoint lock but never its
// write lock, so its flushes delay only its caller.
func (s *Store) checkpoint(ctx context.Context) (Checkpointed, error) {
	var busy int
	var c Checkpointed
	if err := s.db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &c.LogFrames, &c.CopiedFrames); err != nil {
		return Checkpointed{}, fmt.Errorf("store: checkpointing the log: %w", err)
	}
	c.Busy = busy != 0
	return c, nil
}
