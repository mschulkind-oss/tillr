package store_test

import (
	"context"
	"database/sql"
	"encoding/binary"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// checkpointStore is a fixture store with no connection left open.
func checkpointStore(t *testing.T) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "board.sqlite3")
	s, err := store.InitFixture(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	return path
}

// writePastTheThreshold commits, in one write transaction, more log frames than SQLite's
// default automatic checkpoint threshold (1000 pages): each value fills most of a page.
func writePastTheThreshold(t *testing.T, s *store.Store, prefix string) {
	t.Helper()
	value := strings.Repeat("x", 4000)
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for i := 0; i < 1100; i++ {
			if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, fmt.Sprintf("%s-%d", prefix, i), value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func writeOne(t *testing.T, s *store.Store, key string) {
	t.Helper()
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, '1') ON CONFLICT(key) DO UPDATE SET value = excluded.value`, key)
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func openWritable(t *testing.T, path string) *store.Store {
	t.Helper()
	s, err := store.Open(context.Background(), path, false)
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// rowsInDatabaseFile answers how many meta rows whose key starts with prefix the database
// file holds on its own: it copies only that file, not the log, and counts in the copy.
// A row is there only once a checkpoint has copied it out of the log.
//
// The copy is made by another process (cp): SQLite's locks on the database file are POSIX
// record locks, which belong to the process, so this process closing a descriptor of its
// own on that file would drop the locks its open handles hold there.
func rowsInDatabaseFile(t *testing.T, path, prefix string) int {
	t.Helper()
	copyPath := filepath.Join(t.TempDir(), "alone.sqlite3")
	if out, err := exec.Command("cp", path, copyPath).CombinedOutput(); err != nil {
		t.Fatalf("copying the database file: %v: %s", err, out)
	}
	db, err := sql.Open("sqlite", "file:"+copyPath)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var n int
	err = db.QueryRow(`SELECT count(*) FROM meta WHERE key LIKE ?`, prefix+"-%").Scan(&n)
	if err != nil && strings.Contains(err.Error(), "no such table") {
		// The database file alone does not hold even the schema yet: nothing has copied
		// the log's frames into it since the store was created.
		return 0
	}
	if err != nil {
		t.Fatal(err)
	}
	return n
}

// logSalt answers the first salt in the write-ahead log's header. SQLite gives the log a
// new salt each time a writer restarts it at the top of the file, so a changed salt is a
// restart (https://www.sqlite.org/walformat.html, the WAL header).
func logSalt(t *testing.T, path string) uint32 {
	t.Helper()
	f, err := os.Open(path + "-wal")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	header := make([]byte, 32)
	if _, err := f.ReadAt(header, 0); err != nil {
		t.Fatal(err)
	}
	return binary.BigEndian.Uint32(header[16:20])
}

func ownCheckpoints(t *testing.T, path string) *store.CheckpointOwner {
	t.Helper()
	owner, err := store.OwnCheckpoints(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return owner
}

// While a checkpoint owner holds the store, no other handle checkpoints after its commits,
// not even one opened before the owner (as a run's handle is, for the whole run). A
// checkpoint is what lets the next writer restart the log, and a restart flushes the log
// header (and, on a fresh handle, its directory) while holding SQLite's writer: the hold
// a heartbeat timed out behind.
func TestAHandleLeavesCheckpointsToTheOwnerWhileOneRuns(t *testing.T) {
	path := checkpointStore(t)
	early := openWritable(t, path)
	defer early.Close()
	owner := ownCheckpoints(t, path)
	defer owner.Close(context.Background())

	writePastTheThreshold(t, early, "early")
	late := openWritable(t, path)
	writePastTheThreshold(t, late, "late")
	late.Close()

	for _, prefix := range []string{"early", "late"} {
		if n := rowsInDatabaseFile(t, path, prefix); n != 0 {
			t.Errorf("a %s handle checkpointed its commit while a checkpoint owner ran: the database file holds %d of its rows", prefix, n)
		}
	}
}

// With no checkpoint owner, command-line writes with no loop running still bound the log:
// the handle's close checkpoints it past CheckpointFrames. That includes a handle that was
// open while an owner ran and outlived it.
func TestAHandleCheckpointsAtCloseWhenNoOwnerRuns(t *testing.T) {
	path := checkpointStore(t)
	owner := ownCheckpoints(t, path)
	s := openWritable(t, path)
	if _, err := owner.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	writePastTheThreshold(t, s, "after")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := rowsInDatabaseFile(t, path, "after"); n != 1100 {
		t.Errorf("after the checkpoint owner closed, the close of a handle it outlived left %d of 1100 rows in the database file", n)
	}
}

// The owner's checkpoint restarts the log only inside the owner's own write, and never in
// another writer's: between the checkpoint and the owner's write, the owner's pin keeps a
// short-lived handle's commit from restarting it. That write is the one in-writer flush the
// cycle has left (the new log header); the owner's long-lived handle has already synced the
// log once, so it does not also flush the directory.
func TestTheOwnersCheckpointRestartsTheLogOnlyInTheOwnersWrite(t *testing.T) {
	path := checkpointStore(t)
	owner := ownCheckpoints(t, path)
	defer owner.Close(context.Background())
	face := openWritable(t, path)
	defer face.Close()
	ctx := context.Background()

	// A short-lived handle writes inside the window: it must not restart the log.
	writeOne(t, face, "before-1")
	restore := store.SetAfterCheckpoint(func() {
		salt := logSalt(t, path)
		other := openWritable(t, path)
		writeOne(t, other, "inside-window")
		other.Close()
		if logSalt(t, path) != salt {
			t.Error("a short-lived handle's commit restarted the log between the owner's checkpoint and its write")
		}
	})
	if _, err := owner.Checkpoint(ctx); err != nil {
		t.Fatal(err)
	}
	restore()

	// Nobody writes inside the window: the owner's own commit restarts the log, every
	// time, so the log stays as short as the writes between two checkpoints.
	for cycle := 0; cycle < 3; cycle++ {
		writeOne(t, face, fmt.Sprintf("before-%d", cycle))
		before := logSalt(t, path)
		var afterCheckpoint uint32
		restore := store.SetAfterCheckpoint(func() { afterCheckpoint = logSalt(t, path) })
		checkpointed, err := owner.Checkpoint(ctx)
		restore()
		if err != nil {
			t.Fatal(err)
		}
		if !checkpointed.Complete() {
			t.Errorf("cycle %d: the owner's checkpoint did not copy the whole log: %+v", cycle, checkpointed)
		}
		if afterCheckpoint != before {
			t.Errorf("cycle %d: the log restarted before the owner's write", cycle)
		}
		if logSalt(t, path) == before {
			t.Errorf("cycle %d: the owner's write after a complete checkpoint did not restart the log", cycle)
		}
	}
}

// Closing the owner checkpoints the whole log, outside any writer, so the database file
// alone is current again once a loop has stopped.
func TestClosingTheOwnerLeavesTheDatabaseFileCurrent(t *testing.T) {
	path := checkpointStore(t)
	owner := ownCheckpoints(t, path)
	face := openWritable(t, path)
	writeOne(t, face, "closing-1")
	face.Close()
	checkpointed, err := owner.Close(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if !checkpointed.Complete() {
		t.Errorf("the closing checkpoint did not copy the whole log: %+v", checkpointed)
	}
	if n := rowsInDatabaseFile(t, path, "closing"); n != 1 {
		t.Errorf("after the owner closed, the database file alone holds %d of the 1 row written", n)
	}
}
