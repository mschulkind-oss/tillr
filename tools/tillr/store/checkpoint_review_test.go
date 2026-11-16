package store_test

import (
	"context"
	"database/sql"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// A full checkpoint made outside the policy (an older tillr build, the sqlite3 command-line
// tool, a second loop's closing checkpoint) leaves the log fully copied. A pin begun then
// takes SQLite's reader slot 0, which a log restart does not need, so it would protect
// nothing: a short-lived writer inside the owner's window would restart the log, flushing
// the header and the directory while holding the writer. The owner writes before it pins,
// so the pin always holds a slot a restart needs, and the cycle's restart is still its own.
func TestTheOwnersPinHoldsAfterAnOutsideFullCheckpoint(t *testing.T) {
	path := checkpointStore(t)
	owner := ownCheckpoints(t, path)
	defer owner.Close(context.Background())
	face := openWritable(t, path)
	writeOne(t, face, "before")
	face.Close()

	outside, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	var busy, logFrames, copied int
	if err := outside.QueryRow(`PRAGMA wal_checkpoint(PASSIVE)`).Scan(&busy, &logFrames, &copied); err != nil {
		t.Fatal(err)
	}
	outside.Close()
	if busy != 0 || logFrames != copied {
		t.Fatalf("the outside checkpoint did not copy the whole log: busy=%d log=%d copied=%d", busy, logFrames, copied)
	}

	restore := store.SetAfterCheckpoint(func() {
		salt := logSalt(t, path)
		other := openWritable(t, path)
		writeOne(t, other, "inside-window")
		other.Close()
		if logSalt(t, path) != salt {
			t.Error("a short-lived handle restarted the log beneath the owner's pin")
		}
	})
	defer restore()
	if _, err := owner.Checkpoint(context.Background()); err != nil {
		t.Fatal(err)
	}
}

// The owner says whether its write restarted the log. A writer that commits between the
// checkpoint and the owner's write keeps the log from restarting, so the answer is "not
// restarted", with the log's length, rather than a complete checkpoint read as success.
func TestTheOwnerReportsWhetherTheLogRestarted(t *testing.T) {
	path := checkpointStore(t)
	owner := ownCheckpoints(t, path)
	defer owner.Close(context.Background())
	face := openWritable(t, path)
	defer face.Close()
	ctx := context.Background()

	writeOne(t, face, "missed-1")
	restore := store.SetAfterCheckpoint(func() { writeOne(t, face, "inside-window") })
	missed, err := owner.Checkpoint(ctx)
	restore()
	if err != nil {
		t.Fatal(err)
	}
	if missed.Restarted {
		t.Errorf("a commit inside the owner's window still reads as a restart: %+v", missed)
	}
	if missed.LogFrames == 0 || missed.LogBytes == 0 {
		t.Errorf("a missed restart does not report the log's length: %+v", missed)
	}

	restarted, err := owner.Checkpoint(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if !restarted.Restarted {
		t.Errorf("a quiet cycle did not report its restart: %+v", restarted)
	}
}

// A checkpoint is maintenance. When the owner's write waits out SQLite's busy timeout behind
// another writer, the cycle is skipped and says so; it is not an error, so it never faults
// the loop that runs it.
func TestABusyOwnerWriteSkipsTheCycleWithoutAnError(t *testing.T) {
	path := checkpointStore(t)
	owner := ownCheckpoints(t, path)
	defer owner.Close(context.Background())
	face := openWritable(t, path)
	writeOne(t, face, "busy-1")
	face.Close()

	holder, err := sql.Open("sqlite", "file:"+path+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer holder.Close()
	conn, err := holder.Conn(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	defer conn.Close()
	held := false
	restore := store.SetAfterCheckpoint(func() {
		if _, err := conn.ExecContext(context.Background(), `BEGIN IMMEDIATE`); err != nil {
			t.Error(err)
			return
		}
		held = true
	})
	defer restore()
	c, err := owner.Checkpoint(context.Background())
	if held {
		conn.ExecContext(context.Background(), `ROLLBACK`)
	}
	if err != nil {
		t.Fatalf("a busy owner write was an error: %v", err)
	}
	if !c.WriterBusy || c.Restarted {
		t.Errorf("a busy owner write did not read as a skipped cycle: %+v", c)
	}
}

// The owner's lock file takes the database file's permissions, not the process's umask, so
// a loop started under a tight umask does not lock other users of the store out of it.
func TestTheOwnersLockFileTakesTheDatabaseFilesMode(t *testing.T) {
	path := checkpointStore(t)
	if err := os.Chmod(path, 0o666); err != nil {
		t.Fatal(err)
	}
	for name, create := range map[string]func(){
		"an owner": func() {
			if _, err := ownCheckpoints(t, path).Close(context.Background()); err != nil {
				t.Fatal(err)
			}
		},
		"a writable handle": func() { openWritable(t, path).Close() },
	} {
		os.Remove(path + "-checkpoint-owner")
		create()
		info, err := os.Stat(path + "-checkpoint-owner")
		if err != nil {
			t.Errorf("%s: %v", name, err)
			continue
		}
		if info.Mode().Perm() != 0o666 {
			t.Errorf("%s created the lock file with mode %v, not the database file's 0666", name, info.Mode().Perm())
		}
	}
}

// A lock file the process cannot open means "no owner can be seen", not a store that cannot
// be written: the handle checkpoints by SQLite's default, as it would with no loop running.
func TestAnUnopenableLockFileMeansNoOwner(t *testing.T) {
	path := checkpointStore(t)
	lock := path + "-checkpoint-owner"
	os.Remove(lock)
	if err := os.Symlink(filepath.Base(lock), lock); err != nil { // a link to itself: ELOOP
		t.Fatal(err)
	}
	s, err := store.Open(context.Background(), path, false)
	if err != nil {
		t.Fatalf("a writable open failed on an unopenable lock file: %v", err)
	}
	defer s.Close()
	writePastTheThreshold(t, s, "unowned")
	if n := rowsInDatabaseFile(t, path, "unowned"); n == 0 {
		t.Error("with the lock file unopenable, the handle did not checkpoint by SQLite's default")
	}
}
