package drain

import (
	"context"
	"database/sql"
	"fmt"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// inDatabaseFile runs query against a copy of the store's database file alone, without its
// write-ahead log: what it answers is what a checkpoint has already copied into that file.
// A database file that holds no schema yet answers "no such table" (see noSchemaYet).
//
// The copy is made by another process (cp): SQLite's locks on the database file are POSIX
// record locks, which belong to the process, so this process closing a descriptor of its
// own on that file would drop the locks its open connections hold there.
func inDatabaseFile(t *testing.T, path, query string, args ...any) *sql.Row {
	t.Helper()
	copyPath := filepath.Join(t.TempDir(), "alone.sqlite3")
	if out, err := exec.Command("cp", path, copyPath).CombinedOutput(); err != nil {
		t.Fatalf("copying the database file: %v: %s", err, out)
	}
	db, err := sql.Open("sqlite", "file:"+copyPath)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { db.Close() })
	return db.QueryRow(query, args...)
}

// noSchemaYet answers whether err says the database file alone holds no schema: nothing
// has copied the log's frames into it since the store was created, so it holds no row
// either. Every handle keeps its own read-only connection, so no close deletes the log and
// copies the store's creation into the file.
func noSchemaYet(err error) bool {
	return err != nil && strings.Contains(err.Error(), "no such table")
}

// thresholdFaceSteps does, during a running Loop's startup sync, a short-lived face's write
// larger than SQLite's automatic checkpoint threshold, and records how many of its rows the
// database file alone held once the face had closed.
type thresholdFaceSteps struct {
	recordingSteps
	store           string
	rowsInDatabase  int
	countedDatabase bool
	t               *testing.T
}

func (s *thresholdFaceSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	face, err := store.Open(ctx, s.store, false)
	if err != nil {
		return SyncOutcome{}, err
	}
	value := strings.Repeat("x", 4000)
	if err := face.WriteTx(ctx, func(tx *store.Tx) error {
		for i := 0; i < 1100; i++ {
			if _, err := tx.Exec(`INSERT INTO meta (key, value) VALUES (?, ?)`, fmt.Sprintf("face-%d", i), value); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		face.Close()
		return SyncOutcome{}, err
	}
	if err := face.Close(); err != nil {
		return SyncOutcome{}, err
	}
	if err := inDatabaseFile(s.t, s.store, `SELECT count(*) FROM meta WHERE key LIKE 'face-%'`).Scan(&s.rowsInDatabase); noSchemaYet(err) {
		s.rowsInDatabase = 0
	} else if err != nil {
		return SyncOutcome{}, err
	}
	s.countedDatabase = true
	return SyncOutcome{Skipped: "a threshold face read no documents"}, nil
}

// While a Loop runs, a short-lived face's commit does not checkpoint, however large it is:
// the running loop is the store's checkpoint owner. A face's checkpoint is what let the next
// writer restart the log, and that restart flushed the log's header and its directory while
// holding SQLite's writer, the hold the heartbeat timed out behind (U1).
func TestARunningLoopKeepsShortLivedFacesFromCheckpointing(t *testing.T) {
	f := newRefusalLoopFixture(t)
	steps := &thresholdFaceSteps{store: f.storePath, t: t}
	l := f.loop(t, steps, &itemContentStager{})
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !steps.countedDatabase {
		t.Fatal("the face's sync never ran")
	}
	if steps.rowsInDatabase != 0 {
		t.Errorf("a short-lived face checkpointed its commit while the loop ran: the database file held %d of its 1100 rows", steps.rowsInDatabase)
	}
}

// Once Run has returned from a clean drain, the database file alone is current: the session's
// end is in it, not only in the write-ahead log. A copy of the database file, a backup, or a
// tool that reads it without the log sees the loop as stopped.
func TestAFinishedRunLeavesTheDatabaseFileCurrent(t *testing.T) {
	f := newRefusalLoopFixture(t)
	l := f.loop(t, &recordingSteps{}, &itemContentStager{})
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	var ended sql.NullInt64
	if err := inDatabaseFile(t, f.storePath, `SELECT ended_at FROM drain_sessions WHERE session_id = ?`, l.sessionID).Scan(&ended); err != nil {
		t.Fatalf("the database file alone does not hold the session: %v", err)
	}
	if !ended.Valid {
		t.Error("the database file alone holds the session but not its end")
	}
}

// checkpointWaitingSteps holds the Loop's startup sync open until the Loop has checkpointed,
// moving the Loop's clock past the checkpoint interval first.
type checkpointWaitingSteps struct {
	recordingSteps
	advance     func()
	checkpoints <-chan struct{}
	t           *testing.T
}

func (s *checkpointWaitingSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	s.advance()
	select {
	case <-s.checkpoints:
	case <-ctx.Done():
		return SyncOutcome{}, ctx.Err()
	case <-time.After(30 * time.Second):
		s.t.Error("the running loop never checkpointed after its clock passed the checkpoint interval")
	}
	return SyncOutcome{Skipped: "a checkpoint-waiting step set reads no documents"}, nil
}

// A running Loop checkpoints on its heartbeat once the checkpoint interval has passed. The
// heartbeat before the checkpoint announces a next pass far enough ahead that the checkpoint's
// disk flushes cannot make the loop read as dead, and the checkpoint runs without the session
// lock, so nothing waiting on that lock waits on the disk.
func TestARunningLoopCheckpointsOnItsHeartbeatWithoutTheSessionLock(t *testing.T) {
	f := newRefusalLoopFixture(t)
	checkpoints := make(chan struct{}, 1)
	var offset atomic.Int64
	base := time.Date(2026, 11, 16, 0, 0, 0, 0, time.UTC)
	ticks := make(chan time.Time, 1)
	steps := &checkpointWaitingSteps{
		advance:     func() { offset.Add(int64(CheckpointInterval)); ticks <- base.Add(time.Duration(offset.Load())) },
		checkpoints: checkpoints,
		t:           t,
	}
	l := f.loop(t, steps, &itemContentStager{})
	l.cfg.Now = func() time.Time { return base.Add(time.Duration(offset.Load())) }
	l.heartbeatClock = func() heartbeatTicker { return heartbeatTicker{C: ticks, Stop: func() {}} }
	var lockFree, announced bool
	l.beforeCheckpoint = func() {
		if l.sessionMu.TryLock() {
			lockFree = true
			l.sessionMu.Unlock()
		}
		r, err := store.OpenRead(context.Background(), f.storePath)
		if err != nil {
			t.Error(err)
			return
		}
		defer r.Close()
		if err := r.ReadTx(context.Background(), func(rd *store.Reader) error {
			var next int64
			if err := rd.QueryRow(`SELECT next_pass_at FROM drain_sessions WHERE session_id = ?`, l.sessionID).Scan(&next); err != nil {
				return err
			}
			announced = next >= l.cfg.Now().Add(CheckpointAllowance).UnixMilli()
			return nil
		}); err != nil {
			t.Error(err)
		}
		select {
		case checkpoints <- struct{}{}:
		default:
		}
	}
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !lockFree {
		t.Error("the checkpoint ran while the session lock was held")
	}
	if !announced {
		t.Error("the heartbeat before the checkpoint did not announce its next pass a checkpoint allowance ahead")
	}
	s, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if writes, err := s.Meta("checkpoint-owner.writes"); err != nil || writes == nil {
		t.Errorf("the checkpoint owner never wrote after a checkpoint: %v / %v", writes, err)
	}
}
