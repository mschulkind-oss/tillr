package run_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/notify"
	"github.com/mschulkind-oss/tillr/store"
)

func TestExecutePublishesRunExitedAfterCommit(t *testing.T) {
	// Keep XDG_RUNTIME_DIR short (under /tmp, independent of deep TMPDIR) so
	// AF_UNIX sun_path stays well under Linux's 108-byte UNIX_PATH_MAX.
	runtimeDir, err := os.MkdirTemp("/tmp", "tillr-run-notify-*")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(runtimeDir) })
	t.Setenv("XDG_RUNTIME_DIR", runtimeDir)

	root := t.TempDir()
	db := filepath.Join(root, "state.db")
	s, err := store.Open(context.Background(), db, true)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = s.Close() }()

	relayCtx, relayCancel := context.WithCancel(context.Background())
	relayDone := make(chan error, 1)
	go func() {
		relayDone <- notify.Serve(relayCtx, notify.ServeConfig{
			StorePath: db,
			IdleExit:  time.Minute,
		})
	}()
	t.Cleanup(func() {
		relayCancel()
		<-relayDone
	})

	subCtx, subCancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer subCancel()
	var sub *notify.Subscription
	for {
		sub, err = notify.Subscribe(subCtx, db, notify.TopicRunExited)
		if err == nil {
			break
		}
		if subCtx.Err() != nil {
			t.Fatalf("subscribing to relay: %v", err)
		}
		time.Sleep(5 * time.Millisecond)
	}
	defer func() { _ = sub.Close() }()

	laneRoot := filepath.Join(root, "lanes")
	req := run.Request{
		RunID:      "run-notify-exited-1",
		ItemID:     "item-notify-1",
		LaneID:     "lane-1",
		Backend:    "fixture",
		Command:    []string{"/bin/sh", "-c", "exit 0"},
		WorkingDir: ".",
		TimeoutMS:  10_000,
	}

	env, err := run.Exec(context.Background(), s, req, run.ExecOptions{
		LaneRoot: laneRoot,
		Now:      time.Now().UTC(),
	})
	if err != nil {
		t.Fatalf("run.Exec: %v", err)
	}
	gotRec := recordOf(t, env)
	if gotRec.State != run.StateExited || gotRec.ExitCode == nil || *gotRec.ExitCode != 0 {
		t.Fatalf("unexpected run record: %+v", gotRec)
	}

	frame, err := sub.Next(subCtx)
	if err != nil {
		t.Fatalf("waiting for %s frame: %v", notify.TopicRunExited, err)
	}
	if frame.Topic != notify.TopicRunExited {
		t.Fatalf("frame topic = %q, want %q", frame.Topic, notify.TopicRunExited)
	}
	if frame.RunID != req.RunID || frame.ItemID != req.ItemID {
		t.Fatalf("frame ids = (item=%q, run=%q), want (item=%q, run=%q)", frame.ItemID, frame.RunID, req.ItemID, req.RunID)
	}
	if frame.Seq == 0 || frame.Seq != gotRec.EventSeq {
		t.Fatalf("frame seq = %d, want terminal event seq %d", frame.Seq, gotRec.EventSeq)
	}

	// Verify post-commit visibility: when run.exited arrives, the terminal runs
	// row and run.finish event are already committed and readable in SQLite.
	rs, err := store.OpenRead(context.Background(), db)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { _ = rs.Close() }()
	rec, found := storedRun(t, rs, req.RunID)
	if !found {
		t.Fatalf("terminal run %q not visible in store after %s", req.RunID, notify.TopicRunExited)
	}
	if rec.State != run.StateExited || rec.FinishedAt == "" {
		t.Fatalf("stored run state = %q finished_at = %q, want exited with finished_at", rec.State, rec.FinishedAt)
	}
	if err := rs.ReadTx(context.Background(), func(r *store.Reader) error {
		var kind string
		if err := r.QueryRow(`SELECT kind FROM events WHERE seq = ?`, frame.Seq).Scan(&kind); err != nil {
			return err
		}
		if kind != run.FinishKind {
			t.Fatalf("event at seq %d has kind %q, want %q", frame.Seq, kind, run.FinishKind)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
