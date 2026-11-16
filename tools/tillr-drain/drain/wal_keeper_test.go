package drain

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// shortLivedFaceSteps does, during a running Loop's startup sync, what every face that is
// not the Loop does: open the store, write once, close. It records what that close left
// behind and whether an idle connection anyone holds still lets a checkpoint finish.
type shortLivedFaceSteps struct {
	recordingSteps
	store string
	// walKept is whether the write-ahead log file still existed after the face's close.
	walKept bool
	// checkpointBusy is the first column of PRAGMA wal_checkpoint(TRUNCATE): 1 when a
	// reader's snapshot kept the checkpoint from finishing.
	checkpointBusy int
	finish         func(ctx context.Context) error
}

func (s *shortLivedFaceSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	face, err := store.Open(ctx, s.store, true)
	if err != nil {
		return SyncOutcome{}, err
	}
	if err := face.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO meta (key, value) VALUES ('short-lived-face', '1')
		 ON CONFLICT(key) DO UPDATE SET value = excluded.value`)
		return err
	}); err != nil {
		face.Close()
		return SyncOutcome{}, err
	}
	if err := face.Close(); err != nil {
		return SyncOutcome{}, err
	}
	_, statErr := os.Stat(s.store + "-wal")
	s.walKept = statErr == nil
	db, err := sql.Open("sqlite", "file:"+s.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		return SyncOutcome{}, err
	}
	defer db.Close()
	var logFrames, checkpointed int
	if err := db.QueryRowContext(ctx, `PRAGMA wal_checkpoint(TRUNCATE)`).Scan(&s.checkpointBusy, &logFrames, &checkpointed); err != nil {
		return SyncOutcome{}, err
	}
	if s.finish != nil {
		return SyncOutcome{}, s.finish(ctx)
	}
	return SyncOutcome{Skipped: "a short-lived face read no documents"}, nil
}

// openStoreDescriptors answers how many of this process's open files are the store's
// database file. An open connection holds at least one; a closed one holds none.
func openStoreDescriptors(t *testing.T, path string) int {
	t.Helper()
	want, err := filepath.EvalSymlinks(path)
	if err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir("/proc/self/fd")
	if err != nil {
		t.Fatal(err)
	}
	n := 0
	for _, e := range entries {
		if target, err := os.Readlink(filepath.Join("/proc/self/fd", e.Name())); err == nil && target == want {
			n++
		}
	}
	return n
}

// While a Loop runs, a short-lived face's close is not the store's last close, so it
// neither checkpoints nor deletes the write-ahead log. Deleting it was what made the next
// commit recreate the log and flush the disk three times while holding SQLite's writer,
// the hold that timed out the heartbeat (U1 in the lock root-cause record). The keeper
// that makes this so is idle: a full checkpoint still completes beside it.
func TestARunningLoopKeepsTheWriteAheadLogAcrossShortLivedCloses(t *testing.T) {
	f := newRefusalLoopFixture(t)
	steps := &shortLivedFaceSteps{store: f.storePath}
	l := f.loop(t, steps, &itemContentStager{})
	if _, err := l.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	if !steps.walKept {
		t.Error("a short-lived face's close deleted the write-ahead log while the loop was running")
	}
	if steps.checkpointBusy != 0 {
		t.Error("a full checkpoint could not finish beside the running loop: an idle connection held a read snapshot")
	}
}

// Run releases the store on every way out: a clean drain, a fault, and the operator's
// stop, which is how SIGTERM reaches it (main's signal.NotifyContext).
func TestRunReleasesTheStoreOnEveryWayOut(t *testing.T) {
	cases := map[string]func(cancel context.CancelFunc) func(context.Context) error{
		"drained": nil,
		"fault": func(context.CancelFunc) func(context.Context) error {
			return func(context.Context) error { return errors.New("fixture startup failed") }
		},
		"operator stop": func(cancel context.CancelFunc) func(context.Context) error {
			return func(ctx context.Context) error { cancel(); return ctx.Err() }
		},
	}
	for name, finish := range cases {
		t.Run(name, func(t *testing.T) {
			f := newRefusalLoopFixture(t)
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			steps := &shortLivedFaceSteps{store: f.storePath}
			if finish != nil {
				steps.finish = finish(cancel)
			}
			l := f.loop(t, steps, &itemContentStager{})
			result, err := l.Run(ctx)
			switch name {
			case "drained":
				if err != nil {
					t.Fatal(err)
				}
			case "fault":
				if err == nil || result.Stop != StopFault {
					t.Fatalf("fault: %+v / %v", result, err)
				}
			case "operator stop":
				if err != nil || result.Stop != StopOperatorStop {
					t.Fatalf("operator stop: %+v / %v", result, err)
				}
			}
			if n := openStoreDescriptors(t, f.storePath); n != 0 {
				t.Errorf("Run returned with %d file descriptor(s) still open on the store", n)
			}
		})
	}
}

// heartbeatFailingSteps makes the Loop's next heartbeat fail — the store is moved aside,
// so its open finds nothing — waits for that failure to cancel startup, puts the store
// back, and then fails startup with its own error.
type heartbeatFailingSteps struct {
	recordingSteps
	store string
	t     *testing.T
}

func (s *heartbeatFailingSteps) Sync(ctx context.Context) (SyncOutcome, error) {
	aside := s.store + ".aside"
	if err := os.Rename(s.store, aside); err != nil {
		return SyncOutcome{}, err
	}
	select {
	case <-ctx.Done():
	case <-time.After(30 * time.Second):
		s.t.Error("the heartbeat never failed against a missing store")
	}
	if err := os.Rename(aside, s.store); err != nil {
		return SyncOutcome{}, err
	}
	return SyncOutcome{}, fmt.Errorf("fixture startup failed")
}

// A heartbeat failure is reported beside the startup error it interrupted, labelled as the
// heartbeat's, and neither replaces the other: replacing it was why U1 showed a bare
// SQLITE_BUSY with nothing saying the startup sync had been running.
func TestAHeartbeatFailureIsReportedBesideTheStartupError(t *testing.T) {
	f := newRefusalLoopFixture(t)
	l := f.loop(t, &heartbeatFailingSteps{store: f.storePath, t: t}, &itemContentStager{})
	l.cfg.Now = time.Now
	result, err := l.Run(context.Background())
	if err == nil || result.Stop != StopFault {
		t.Fatalf("result: %+v / %v", result, err)
	}
	for _, want := range []string{"fixture startup failed", "session heartbeat: "} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("the loop's error %q does not report %q", err, want)
		}
	}
}

// holdWALKeeper gives a fixture that drives a Loop pass by pass, without Run, the keeper
// Run would hold, so its short-lived opens behave as they do under a running Loop.
func holdWALKeeper(t *testing.T, path string) {
	t.Helper()
	keeper, err := openWALKeeper(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { keeper.Close() })
}
