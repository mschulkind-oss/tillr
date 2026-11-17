package store_test

import (
	"bufio"
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// TestHelperHoldsTheStoreOpen is not a test: run as a child process with
// TILLR_TEST_HOLD_STORE set, it opens that store read-only, reads it, says "held" and keeps
// it open until its stdin closes. It is the "other process" a truncate must refuse.
func TestHelperHoldsTheStoreOpen(t *testing.T) {
	path := os.Getenv("TILLR_TEST_HOLD_STORE")
	if path == "" {
		t.Skip("a helper process, run by TestTruncatingRefusesWhileAnotherProcessHasTheStoreOpen")
	}
	r, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	if _, err := r.Describe(context.Background()); err != nil {
		t.Fatal(err)
	}
	os.Stdout.WriteString("held\n")
	bufio.NewReader(os.Stdin).ReadString('\n')
}

// holdInAnotherProcess runs TestHelperHoldsTheStoreOpen against path and answers once the
// child has the store open; the child exits when the test ends.
func holdInAnotherProcess(t *testing.T, path string) {
	t.Helper()
	cmd := exec.Command(os.Args[0], "-test.run=^TestHelperHoldsTheStoreOpen$")
	cmd.Env = append(os.Environ(), "TILLR_TEST_HOLD_STORE="+path, "TILLR_SQLITE_TRACE=")
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		stdin.Close()
		cmd.Wait()
	})
	line, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(line) != "held" {
		t.Fatalf("the holding process did not open the store: %q %v", line, err)
	}
}

// With nothing else holding the store, the truncate copies the whole log into the database
// file and shrinks the log file to its header and one frame: the counter row it writes
// after the truncate, so the log's new header was written, and flushed, by the truncate's
// own writer. The database file alone then holds every row written before it.
func TestTruncatingShrinksTheLogWhenNothingElseHasTheStoreOpen(t *testing.T) {
	path := checkpointStore(t)
	s := openWritable(t, path)
	writePastTheThreshold(t, s, "long")
	writePastTheThreshold(t, s, "longer")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	grown := store.ReadLog(path).Bytes
	if grown < 4<<20 {
		t.Fatalf("the log file is %d bytes; this test needs one longer than the threshold", grown)
	}
	c, err := store.TruncateLog(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !c.Truncated || c.Busy || c.WriterBusy {
		t.Fatalf("the truncate answered %+v, want Truncated", c)
	}
	log := store.ReadLog(path)
	if log.Frames != 1 || log.Bytes >= grown/100 {
		t.Fatalf("the log after the truncate is %+v (it was %d bytes), want one frame", log, grown)
	}
	if rows := rowsInDatabaseFile(t, path, "longer"); rows != 1100 {
		t.Fatalf("the database file alone holds %d of the 1100 rows written last, want all of them", rows)
	}
}

// Another process with the store open is refused, by name, and nothing is truncated: that
// process's next writer would otherwise pay the new log's header flush while it holds
// SQLite's writer.
func TestTruncatingRefusesWhileAnotherProcessHasTheStoreOpen(t *testing.T) {
	path := checkpointStore(t)
	s := openWritable(t, path)
	writePastTheThreshold(t, s, "long")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	holdInAnotherProcess(t, path)
	before := store.ReadLog(path)
	_, err := store.TruncateLog(context.Background(), path)
	if !errors.Is(err, store.ErrStoreInUse) {
		t.Fatalf("the truncate answered %v with another process holding the store, want ErrStoreInUse", err)
	}
	if !strings.Contains(err.Error(), "processes [") {
		t.Errorf("the refusal does not name the processes: %v", err)
	}
	if after := store.ReadLog(path); after != before {
		t.Fatalf("a refused truncate changed the log: %+v then %+v", before, after)
	}
}

// A running drain loop owns the store's checkpoints; the truncate refuses as the
// maintenance checkpoint does.
func TestTruncatingRefusesWhileAnOwnerRuns(t *testing.T) {
	path := checkpointStore(t)
	owner, err := store.OwnCheckpoints(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(context.Background())
	if _, err := store.TruncateLog(context.Background(), path); !errors.Is(err, store.ErrOwnerRunning) {
		t.Fatalf("the truncate answered %v with an owner running, want ErrOwnerRunning", err)
	}
}
