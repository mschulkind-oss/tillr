package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestStopWaitsForProcessExitNotSessionReceipt(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	dir := t.TempDir()
	ready, signaled, release := filepath.Join(dir, "ready"), filepath.Join(dir, "signaled"), filepath.Join(dir, "release")
	cmd := exec.Command("sh", "-c", `trap 'touch "$2"' TERM
 touch "$1"
 while [ ! -f "$3" ]; do sleep 0.01; done`, "fixture", ready, signaled, release)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = cmd.Process.Kill(); _ = cmd.Wait() })
	waitForMarker := func(path string) {
		t.Helper()
		deadline := time.Now().Add(5 * time.Second)
		for {
			if _, err := os.Stat(path); err == nil {
				return
			}
			if time.Now().After(deadline) {
				t.Fatalf("did not see %s", path)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	waitForMarker(ready)
	owner, err := currentLoopOwner()
	if err != nil {
		t.Fatal(err)
	}
	owner.PID = cmd.Process.Pid
	owner.Start, err = processStart(owner.PID)
	if err != nil {
		t.Fatal(err)
	}
	insertSession(t, f, "stopping", *owner, time.Now().Add(time.Second))
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	done := make(chan error, 1)
	go func() {
		_, err := Stop(ctx, StopConfig{Store: f.storePath})
		done <- err
	}()
	waitForMarker(signaled)
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE drain_sessions SET ended_at=? WHERE session_id='stopping'`, time.Now().UnixMilli())
		return err
	})
	select {
	case err := <-done:
		t.Fatalf("stop returned before supervisor exit: %v", err)
	case <-time.After(200 * time.Millisecond):
	}
	if err := os.WriteFile(release, nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := <-done; err != nil {
		t.Fatal(err)
	}
}

func TestShutdownDoesNotWaitForCompletionChannelSpace(t *testing.T) {
	l := &Loop{completions: make(chan runCompletion, 1)}
	l.completions <- runCompletion{id: "previous"}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		defer close(done)
		l.sendCompletion(ctx, runCompletion{id: "examination"})
	}()
	cancel()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("examination could not exit because the stopped pass was not draining notifications")
	}
}
