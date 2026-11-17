package store_test

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// logExists answers whether the store's write-ahead log file is there.
func logExists(t *testing.T, path string) bool {
	t.Helper()
	_, err := os.Stat(path + "-wal")
	if err != nil && !errors.Is(err, os.ErrNotExist) {
		t.Fatal(err)
	}
	return err == nil
}

// deleteTheLog leaves the store with no write-ahead log, the way any SQLite connection
// that is the store's last does when it closes: it checkpoints the log and deletes it.
func deleteTheLog(t *testing.T, path string) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO meta (key, value) VALUES ('raw', '1') ON CONFLICT(key) DO UPDATE SET value = excluded.value`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	if logExists(t, path) {
		t.Fatal("the fixture's last close left the log in place; the test needs a store with none")
	}
}

// A short-lived program's close leaves the log in place and checkpoints nothing, even as
// the store's last connection. That close used to take SQLite's exclusive lock, copy the
// log into the database file (two flushes) and delete the log, and every other
// connection's next lock waited behind it; the next writer then created the log again,
// flushing its header and the directory while it held the writer.
func TestAShortLivedCloseLeavesTheLogAndCheckpointsNothing(t *testing.T) {
	path := checkpointStore(t)
	s := openWritable(t, path)
	writeOne(t, s, "closing-1")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if !logExists(t, path) {
		t.Fatal("the store's last close deleted the write-ahead log")
	}
	if n := rowsInDatabaseFile(t, path, "closing"); n != 0 {
		t.Errorf("the store's last close checkpointed: the database file alone holds %d of the 1 row written", n)
	}
}

// A program started with no loop running and no log present creates the log once, and
// from then on no program's close checkpoints: so no program can queue behind another's
// closing checkpoint. A close checkpoints only under SQLite's exclusive lock, and only if
// it is the store's last connection; that it copied nothing and deleted nothing, even
// when it was the last, is the proof that it never held that lock to do so.
func TestAProgramStartedWithNoLogCannotQueueBehindAClosingCheckpoint(t *testing.T) {
	path := checkpointStore(t)
	deleteTheLog(t, path)
	first := openWritable(t, path)
	writeOne(t, first, "nolog-first")
	second := openWritable(t, path)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}
	writeOne(t, second, "nolog-second")
	if err := second.Close(); err != nil {
		t.Fatal(err)
	}
	if !logExists(t, path) {
		t.Fatal("a short-lived program's close deleted the write-ahead log")
	}
	if n := rowsInDatabaseFile(t, path, "nolog"); n != 0 {
		t.Errorf("a short-lived program's close checkpointed: the database file alone holds %d of the 2 rows written", n)
	}
}

// The maintenance checkpoint (tillr-store checkpoint) makes the database file alone hold
// every commit made before it, and leaves the log holding only its own counter row, so the
// next writer appends rather than restarting a fully copied log.
func TestTheMaintenanceCheckpointMakesTheDatabaseFileAloneCurrent(t *testing.T) {
	path := checkpointStore(t)
	s := openWritable(t, path)
	for _, key := range []string{"kept-1", "kept-2", "kept-3"} {
		writeOne(t, s, key)
	}
	s.Close()
	if n := rowsInDatabaseFile(t, path, "kept"); n != 0 {
		t.Fatalf("the database file alone already held %d rows before the maintenance checkpoint", n)
	}
	// Another program's handle stays open throughout, as one does on a busy store: it keeps
	// the log's shared index alive. With every connection closed, the next opener rebuilds
	// that index from the log and counts no frame as copied, so a fully copied log would
	// go unnoticed here.
	bystander := openWritable(t, path)
	defer bystander.Close()
	before := logSalt(t, path)
	checkpointed, err := store.Checkpoint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if !checkpointed.Complete() || !checkpointed.Restarted {
		t.Errorf("the maintenance checkpoint was not complete and restarted: %+v", checkpointed)
	}
	if n := rowsInDatabaseFile(t, path, "kept"); n != 3 {
		t.Errorf("after the maintenance checkpoint, the database file alone holds %d of the 3 rows", n)
	}
	if logSalt(t, path) == before {
		t.Error("the maintenance checkpoint did not restart the log")
	}
	if log := store.ReadLog(path); !log.Known || log.Frames == 0 {
		t.Errorf("the maintenance checkpoint left no frame of its own in the log: %+v", log)
	}
	// Its own frame is uncopied, so the next writer appends after it rather than
	// restarting a fully copied log while it holds the writer.
	restarted := logSalt(t, path)
	next := openWritable(t, path)
	writeOne(t, next, "next")
	next.Close()
	if logSalt(t, path) != restarted {
		t.Error("the first writer after the maintenance checkpoint restarted the log")
	}
}

// While a drain loop owns the store's checkpoints, the maintenance checkpoint refuses: a
// complete checkpoint from outside the owner's cycle leaves a fully copied log, which the
// next short-lived writer restarts while it holds the writer.
func TestTheMaintenanceCheckpointRefusesWhileAnOwnerRuns(t *testing.T) {
	path := checkpointStore(t)
	owner := ownCheckpoints(t, path)
	defer owner.Close(context.Background())
	if running, err := store.OwnerRunning(path); err != nil || !running {
		t.Fatalf("OwnerRunning answered %v, %v with an owner running", running, err)
	}
	if _, err := store.Checkpoint(context.Background(), path); !errors.Is(err, store.ErrOwnerRunning) {
		t.Fatalf("the maintenance checkpoint answered %v with an owner running, want ErrOwnerRunning", err)
	}
}

// With no loop running, a writable handle that finds the log past CheckpointFrames when it
// closes runs one checkpoint cycle, after its own transactions committed and outside every
// lock: its own commits never checkpoint, and the cycle's one in-writer flush is in its own
// restart write. While an owner runs, the close leaves the log to it.
func TestALogPastTheThresholdIsCheckpointedWhenAHandleCloses(t *testing.T) {
	path := checkpointStore(t)
	s := openWritable(t, path)
	writePastTheThreshold(t, s, "grown")
	if n := rowsInDatabaseFile(t, path, "grown"); n != 0 {
		t.Fatalf("a commit past the threshold checkpointed inside the program's write: the database file holds %d rows", n)
	}
	if log := store.ReadLog(path); !log.Known || log.Frames < store.CheckpointFrames {
		t.Fatalf("the log did not read as past the threshold: %+v", log)
	}
	before := logSalt(t, path)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := rowsInDatabaseFile(t, path, "grown"); n != 1100 {
		t.Errorf("the close past the threshold left %d of 1100 rows in the database file", n)
	}
	if logSalt(t, path) == before {
		t.Error("the close's checkpoint cycle did not restart the log")
	}
	if log := store.ReadLog(path); log.Frames >= store.CheckpointFrames {
		t.Errorf("the log is still past the threshold after the close: %+v", log)
	}

	owner := ownCheckpoints(t, path)
	defer owner.Close(context.Background())
	s = openWritable(t, path)
	writePastTheThreshold(t, s, "owned")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if n := rowsInDatabaseFile(t, path, "owned"); n != 0 {
		t.Errorf("a close checkpointed while an owner ran: the database file holds %d rows", n)
	}
}

// cycleWrites answers the count the checkpoint cycles' writes keep in meta: each cycle
// that reaches its restart write adds two.
func cycleWrites(t *testing.T, path string) string {
	t.Helper()
	r, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	v, err := r.Meta("checkpoint-owner.writes")
	if err != nil {
		t.Fatal(err)
	}
	if v == nil {
		return ""
	}
	return *v
}

// holdARead begins a read transaction on its own read-only handle and holds it until the
// returned release is called. A reader keeps the log from restarting: SQLite restarts it
// only when no reader is using it.
func holdARead(t *testing.T, path string) (release func()) {
	t.Helper()
	r, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	reading, done, finished := make(chan error, 1), make(chan struct{}), make(chan struct{})
	go func() {
		defer close(finished)
		r.ReadTx(context.Background(), func(rd *store.Reader) error {
			rows, err := rd.Query(`SELECT count(*) FROM meta`)
			if err == nil {
				for rows.Next() {
				}
				err = rows.Close()
			}
			reading <- err
			<-done
			return nil
		})
	}()
	if err := <-reading; err != nil {
		t.Fatal(err)
	}
	var once bool
	release = func() {
		if !once {
			once = true
			close(done)
			<-finished
			r.Close()
		}
	}
	t.Cleanup(release)
	return release
}

// A cycle that could not restart the log, because another program was reading it, leaves
// the log past CheckpointFrames. The next closes must not each run another cycle: under
// continuous use that made every close past the threshold checkpoint and write twice more.
// A close cycles again once the log has grown CheckpointFrames past the last cycle, or once
// it has restarted.
func TestACloseDoesNotRepeatACheckpointCycleTheLogHasNotGrownSince(t *testing.T) {
	path := checkpointStore(t)
	s := openWritable(t, path)
	writePastTheThreshold(t, s, "grown")
	release := holdARead(t, path)
	before := logSalt(t, path)
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if logSalt(t, path) != before {
		t.Fatal("the log restarted while another program was reading it")
	}
	ran := cycleWrites(t, path)
	if ran == "" {
		t.Fatal("the close past the threshold ran no checkpoint cycle")
	}

	next := openWritable(t, path)
	writeOne(t, next, "next")
	if err := next.Close(); err != nil {
		t.Fatal(err)
	}
	if got := cycleWrites(t, path); got != ran {
		t.Errorf("a close ran another cycle on a log that had grown one write since the last (writes %s, then %s)", ran, got)
	}

	release()
	grown := openWritable(t, path)
	writePastTheThreshold(t, grown, "grown-again")
	if err := grown.Close(); err != nil {
		t.Fatal(err)
	}
	if got := cycleWrites(t, path); got == ran {
		t.Error("a close ran no cycle on a log that had grown CheckpointFrames past the last")
	}
	if logSalt(t, path) == before {
		t.Error("the cycle with no reader left did not restart the log")
	}
}

// A program that will create the store, as a drain loop's first session write does, takes
// the checkpoint owner before anything is migrated: on a missing store the owner creates
// only the empty WAL database, and the migration that follows runs with the owner held.
func TestAnOwnerThatCreatesTheStoreLeavesTheMigrationToTheOpenThatFollows(t *testing.T) {
	path := filepath.Join(t.TempDir(), "new", "board.sqlite3")
	ctx := context.Background()
	if _, err := store.OwnCheckpoints(ctx, path); err == nil {
		t.Fatal("OwnCheckpoints created a missing store; only CreateAndOwnCheckpoints may")
	}
	owner, err := store.CreateAndOwnCheckpoints(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer owner.Close(ctx)
	if running, err := store.OwnerRunning(path); err != nil || !running {
		t.Fatalf("the creating owner is not visible to other programs: %v %v", running, err)
	}
	r, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	counts, err := r.TableCounts()
	r.Close()
	if err != nil {
		t.Fatal(err)
	}
	if len(counts) != 0 {
		t.Fatalf("the owner migrated the store it created: %v", counts)
	}
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if !logExists(t, path) {
		t.Error("the migrating open's close deleted the log while the owner held the store")
	}
}
