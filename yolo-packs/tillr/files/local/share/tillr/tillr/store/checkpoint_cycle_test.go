package store

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

// cycleStore answers a migrated store with no handle open.
func cycleStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

// growLog commits rows of nearly a page each until the log holds at least frames frames
// since it last restarted.
func growLog(t *testing.T, s *Store, prefix string, frames int) {
	t.Helper()
	value := strings.Repeat("x", 4000)
	for i := 0; ReadLog(s.path).Frames < frames; i++ {
		if err := s.WriteTx(context.Background(), func(tx *Tx) error {
			for j := 0; j < 100; j++ {
				if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, fmt.Sprintf("%s-%d-%d", prefix, i, j), value); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
}

// cycleWrites answers the counter every cycle's two writes increment (restartKey): 0 before
// any cycle, and 2 more for every cycle that ran both its writes.
func cycleWrites(t *testing.T, path string) int {
	t.Helper()
	r, err := OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	var n int
	err = r.db.QueryRow(`SELECT CAST(value AS INTEGER) FROM meta WHERE key = ?`, restartKey).Scan(&n)
	if errors.Is(err, sql.ErrNoRows) {
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// realBusy answers a genuine SQLITE_BUSY from the driver: a second writer with no busy
// timeout meets the first's write lock.
func realBusy(t *testing.T, path string) error {
	t.Helper()
	ctx := context.Background()
	holder, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	conn, err := holder.Conn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	if _, err := conn.ExecContext(ctx, `BEGIN IMMEDIATE`); err != nil {
		t.Fatal(err)
	}
	defer conn.ExecContext(ctx, `ROLLBACK`)
	other, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(0)")
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	_, err = other.Exec(`BEGIN IMMEDIATE`)
	if !IsBusy(err) {
		t.Fatalf("a second writer with no busy timeout answered %v, not SQLITE_BUSY", err)
	}
	return err
}

// Two closes' cycles at once defeat each other's restart: each pin holds a reader slot the
// other's restart write needs. So a close takes the owner's lock file exclusively, without
// waiting, for the length of its cycle, and a second close that finds it held skips cleanly,
// leaving no record of a cycle it did not run. Here the second close happens inside the
// first's cycle, between its checkpoint and its restart write.
func TestASecondCloseSkipsWhileAnotherCloseRunsItsCycle(t *testing.T) {
	path := cycleStore(t)
	_, read := captureSQLiteTrace(t, true)
	first, err := Open(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	second, err := Open(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	growLog(t, first, "grown", CheckpointFrames)
	inside := false
	previous := afterCheckpoint
	afterCheckpoint = func() {
		if inside {
			return
		}
		inside = true
		if err := second.Close(); err != nil {
			t.Error(err)
		}
	}
	defer func() { afterCheckpoint = previous }()
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	if !inside {
		t.Fatal("the first close ran no cycle, so the second close never met one")
	}
	if got := cycleWrites(t, path); got != 2 {
		t.Fatalf("the cycle counter is %d after two closes past the threshold, want 2: the second close ran a cycle inside the first's", got)
	}
	if log := read(); !strings.Contains(log, "reason=owner-or-cycle-running") {
		t.Errorf("the second close's skip was not traced:\n%s", log)
	}
	// The claim is released with the cycle: the lock file is free again.
	lock, err := os.Open(lockPath(path))
	if err != nil {
		t.Fatal(err)
	}
	defer lock.Close()
	if err := flock(lock, syscall.LOCK_EX|syscall.LOCK_NB); err != nil {
		t.Fatalf("the lock file is still held after the cycle ended: %v", err)
	}
	flock(lock, syscall.LOCK_UN)
}

// A close records the log it cycled at (lastCycleKey) and does not cycle again until the
// log has grown CheckpointFrames past that record, or restarted since: the record's salt
// is what tells a new log from the old one. Here a cycle that a reader kept from restarting
// records a long log; a maintenance checkpoint then restarts it, and the log grows again
// past the threshold but by less than CheckpointFrames past the old record. Without the
// salt that close would read "cycled at this log" and skip.
func TestACloseCyclesAgainOnceTheLogHasRestartedSinceItsRecord(t *testing.T) {
	path := cycleStore(t)
	ctx := context.Background()
	s, err := Open(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	growLog(t, s, "first", CheckpointFrames)
	// A reader's snapshot keeps the checkpoint from copying the newest frames, so the
	// restart write finds a log that is not fully copied and does not restart it.
	reader, err := OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	pin, err := reader.db.BeginTx(ctx, &sql.TxOptions{ReadOnly: true})
	if err != nil {
		t.Fatal(err)
	}
	var n int
	if err := pin.QueryRow(`SELECT count(*) FROM meta`).Scan(&n); err != nil {
		t.Fatal(err)
	}
	saltBefore := logSalt(path)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	pin.Rollback()
	reader.Close()
	if logSalt(path) != saltBefore {
		t.Fatal("the cycle restarted the log although a reader held frames back; the record this test needs is not made")
	}
	recorded := ReadLog(path).Frames
	if cycleWrites(t, path) != 2 {
		t.Fatalf("the first close ran no cycle (counter %d)", cycleWrites(t, path))
	}
	if c, err := Checkpoint(ctx, path); err != nil || !c.Restarted {
		t.Fatalf("the maintenance checkpoint did not restart the log: %+v %v", c, err)
	}
	s, err = Open(ctx, path, false)
	if err != nil {
		t.Fatal(err)
	}
	growLog(t, s, "second", CheckpointFrames)
	if grown := ReadLog(path).Frames; grown-recorded >= CheckpointFrames {
		t.Fatalf("the new log (%d frames) is CheckpointFrames past the record (%d), so the frames clause alone allows the cycle and the salt clause is not what is tested", grown, recorded)
	}
	before := cycleWrites(t, path)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := cycleWrites(t, path) - before; got != 2 {
		t.Fatalf("the close past the threshold on a restarted log ran %d cycle writes, want 2: the record of the old log stopped it", got)
	}
}

// A handle whose sidecar could not be attached runs without one: the field is nil, the open
// traces sidecar.unavailable, its commits still never checkpoint, and its close does not
// run a cycle, whose pin would have been the sidecar.
func TestAHandleWithoutASidecarSkipsTheCloseCycle(t *testing.T) {
	path := cycleStore(t)
	_, read := captureSQLiteTrace(t, true)
	failSidecar = func() error { return errors.New("the sidecar cannot be attached (test)") }
	s, err := Open(context.Background(), path, false)
	failSidecar = nil
	if err != nil {
		t.Fatal(err)
	}
	if s.sidecar != nil {
		t.Fatal("a sidecar that could not be attached is still the handle's sidecar")
	}
	// Another handle keeps this one's close from being the store's last, which would
	// checkpoint the log on SQLite's own account.
	other, err := OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer other.Close()
	if err := other.ReadTx(context.Background(), func(r *Reader) error {
		var n int
		return r.QueryRow(`SELECT count(*) FROM meta`).Scan(&n)
	}); err != nil {
		t.Fatal(err)
	}
	growLog(t, s, "unpinned", CheckpointFrames)
	if log := ReadLog(path); log.Frames < CheckpointFrames {
		t.Fatalf("the log holds %d frames: a commit checkpointed", log.Frames)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if got := cycleWrites(t, path); got != 0 {
		t.Fatalf("a handle without a sidecar ran a close cycle (counter %d)", got)
	}
	log := read()
	for _, want := range []string{"phase=sidecar.unavailable ", "reason=no-sidecar"} {
		if !strings.Contains(log, want) {
			t.Errorf("the trace lacks %q:\n%s", want, log)
		}
	}
}

// A pin whose read waits out the busy timeout skips the cycle and is not an error: the
// cycle answers PinBusy, traces pin.busy, and the next cycle runs normally.
func TestABusyPinSkipsTheCycleWithoutAnError(t *testing.T) {
	path := cycleStore(t)
	ctx := context.Background()
	busy := realBusy(t, path)
	_, read := captureSQLiteTrace(t, true)
	owner, err := OwnCheckpoints(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	beforePin = func() error { return busy }
	c, err := owner.Checkpoint(ctx)
	beforePin = nil
	if err != nil {
		owner.Close(ctx)
		t.Fatalf("a busy pin failed the cycle: %v", err)
	}
	if !c.PinBusy || c.Restarted || c.ran {
		t.Fatalf("a busy pin answered %+v, want PinBusy and no checkpoint", c)
	}
	if !strings.Contains(read(), "phase=pin.busy ") {
		t.Error("the busy pin was not traced as pin.busy")
	}
	if c, err := owner.Checkpoint(ctx); err != nil || !c.Restarted {
		t.Fatalf("the next cycle did not restart the log: %+v %v", c, err)
	}
	if _, err := owner.Close(ctx); err != nil {
		t.Fatal(err)
	}
}
