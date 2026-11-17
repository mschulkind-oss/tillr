package store_test

import (
	"os"
	"syscall"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// fOFDGetLk is Linux's F_OFD_GETLK. An open-file-description lock query reports a
// conflicting POSIX lock even when this process holds it, which a plain F_GETLK does not.
const fOFDGetLk = 36

// shmDMSByte is the byte of the shared-memory file each SQLite process holds a read lock
// on while it has the store open (UNIX_SHM_DMS, sqlite3.c: WALINDEX_LOCK_OFFSET 120 + 8). A
// process that finds no other process holding it truncates and re-initializes the file.
const shmDMSByte = 128

// dmsHeld answers whether a POSIX lock on the DMS byte is held, asked through probe, an
// open file description of the -shm file the test keeps open until the test ends: closing
// a descriptor is the very act this test is about.
func dmsHeld(t *testing.T, probe *os.File) bool {
	t.Helper()
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Whence: 0, Start: shmDMSByte, Len: 1}
	if err := syscall.FcntlFlock(probe.Fd(), fOFDGetLk, &lock); err != nil {
		t.Fatal(err)
	}
	return lock.Type != syscall.F_UNLCK
}

// Reading the log's state must not open and close the store's shared-memory (-shm) file:
// POSIX record locks belong to the process, and closing ANY descriptor of a file drops
// every lock the process holds on it. A ReadLog that opened -shm took away SQLite's own
// locks on it while this process's connections still used it: another process then saw
// nobody attached, truncated the file under this one's mapping, and the next access
// faulted (SQLITE_IOERR_IN_PAGE, 8714, seen in the tillr-drain suite).
func TestReadingTheLogStateKeepsSQLitesSharedMemoryLocks(t *testing.T) {
	path := checkpointStore(t)
	s := openWritable(t, path)
	writeOne(t, s, "locked")
	probe, err := os.OpenFile(path+"-shm", os.O_RDWR, 0)
	if err != nil {
		t.Fatal(err)
	}
	defer probe.Close()
	if !dmsHeld(t, probe) {
		t.Fatal("with a handle open, no SQLite lock is held on the shared-memory file; the probe cannot tell")
	}
	if log := store.ReadLog(path); !log.Known || log.Frames == 0 {
		t.Fatalf("the log's state was not read: %+v", log)
	}
	if !dmsHeld(t, probe) {
		t.Error("reading the log's state dropped this process's SQLite locks on the shared-memory file")
	}
	// The same holds for every close: a close that reads the log to decide whether it is
	// due must leave the other handles' locks in place.
	other := openWritable(t, path)
	writeOne(t, other, "other")
	if err := other.Close(); err != nil {
		t.Fatal(err)
	}
	if !dmsHeld(t, probe) {
		t.Error("a handle's close dropped the other open handle's SQLite locks on the shared-memory file")
	}
	// s closes before the probe does: the probe's own close drops the process's locks too.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
}
