package main_test

import (
	"bytes"
	"context"
	"errors"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/notify"
)

func TestNotifyHelpAndUsageRefusals(t *testing.T) {
	emptyPath := t.TempDir()

	code, stdout, stderr := runWrapper(t, emptyPath, "", "notify", "--help")
	if code != 0 || !strings.Contains(stdout, "serve") || stderr != "" {
		t.Fatalf("notify --help: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	code, stdout, stderr = runWrapper(t, emptyPath, "", "notify", "serve", "--help")
	if code != 0 || !strings.Contains(stdout, "serve") || !strings.Contains(stdout, "idle-exit") || stderr != "" {
		t.Fatalf("notify serve --help: code=%d stdout=%q stderr=%q", code, stdout, stderr)
	}

	for _, argv := range [][]string{
		{"notify"},
		{"notify", "bogus"},
		{"notify", "serve"},
		{"notify", "serve", "--store", filepath.Join(t.TempDir(), "state.db"), "--idle-exit", "0s"},
		{"notify", "serve", "--store", filepath.Join(t.TempDir(), "state.db"), "extra"},
	} {
		code, stdout, stderr := runWrapper(t, emptyPath, "", argv...)
		if code != 2 || stdout != "" || stderr == "" {
			t.Fatalf("argv %v: code=%d stdout=%q stderr=%q, want exit 2 on stderr", argv, code, stdout, stderr)
		}
	}
}

func TestNotifyServeSubcommandRelaysFramesAndExitsOnIdle(t *testing.T) {
	t.Setenv("XDG_RUNTIME_DIR", "")
	storePath := filepath.Join(t.TempDir(), "state.db")

	sockPath, err := notify.SocketPath(storePath)
	if err != nil {
		t.Fatal(err)
	}
	_ = os.Remove(sockPath)

	// Start `tillr notify serve` with an empty PATH to prove it runs in-process
	// inside the static `tillr` binary without an external `tillr-notify` binary.
	emptyPath := t.TempDir()
	cmd := exec.Command(wrapperBin, "notify", "serve", "--store", storePath, "--idle-exit", "250ms")
	cmd.Env = append(os.Environ(), "PATH="+emptyPath, "XDG_RUNTIME_DIR=")
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}

	waitDone := make(chan error, 1)
	go func() {
		waitDone <- cmd.Wait()
	}()

	// Connect subscriber as soon as <sock> binds.
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var sub *notify.Subscription
	for {
		sub, err = notify.Subscribe(ctx, storePath, "queue.*", "candidate.*")
		if err == nil {
			break
		}
		select {
		case waitErr := <-waitDone:
			t.Fatalf("tillr notify serve exited before subscribe: %v (stderr=%s)", waitErr, stderr.String())
		case <-ctx.Done():
			t.Fatalf("timed out connecting to %s: %v (stderr=%s)", sockPath, err, stderr.String())
		case <-time.After(5 * time.Millisecond):
		}
	}

	// Publish a frame to the subprocess relay and confirm the subscriber receives it.
	if err := notify.Publish(storePath, notify.Frame{
		Topic:  notify.TopicCandidateReady,
		ItemID: "item-cli",
		RunID:  "run-cli",
		Seq:    99,
	}); err != nil {
		t.Fatal(err)
	}

	got, err := sub.Next(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if got.Topic != notify.TopicCandidateReady || got.ItemID != "item-cli" || got.RunID != "run-cli" || got.Seq != 99 {
		t.Fatalf("relayed frame = %+v, want candidate.ready item-cli/run-cli seq 99", got)
	}

	// Close the sole subscriber; `tillr notify serve` must exit 0 after --idle-exit
	// and unlink its socket.
	_ = sub.Close()

	select {
	case waitErr := <-waitDone:
		if waitErr != nil {
			t.Fatalf("tillr notify serve exited with error: %v (stderr=%s)", waitErr, stderr.String())
		}
	case <-time.After(30 * time.Second):
		_ = cmd.Process.Kill()
		t.Fatal("tillr notify serve did not exit after --idle-exit")
	}

	if _, statErr := os.Stat(sockPath); !errors.Is(statErr, os.ErrNotExist) {
		t.Fatalf("socket %s was not unlinked on idle exit: %v", sockPath, statErr)
	}
	_ = os.Remove(notify.LockPath(sockPath))
}
