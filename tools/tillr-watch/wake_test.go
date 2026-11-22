package main

import (
	"bufio"
	"bytes"
	"context"
	"database/sql"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// TestWatchWaitWakesWhenWALPrecedesIndexPublish reproduces the SQLite WAL commit
// split deterministically on both direct and symlinked store paths: SQLite writes
// committed frames to -wal (firing fsnotify) before publishing the new WAL-index
// header in -shm under WAL_WRITE_LOCK without touching -wal again.
func TestWatchWaitWakesWhenWALPrecedesIndexPublish(t *testing.T) {
	for _, name := range []string{"direct", "symlink"} {
		t.Run(name, func(t *testing.T) {
			walPrecedesIndexPublish(t, name == "symlink")
		})
	}
}

func walPrecedesIndexPublish(t *testing.T, useSymlink bool) {
	t.Helper()
	s, rules := fixture(t, false)
	storeArg := "./state.db"
	if useSymlink {
		t.Chdir(t.TempDir())
		storeArg = "./linked.db"
		if err := os.Symlink(s.Path(), storeArg); err != nil {
			t.Fatal(err)
		}
	} else {
		t.Chdir(filepath.Dir(s.Path()))
	}
	fact(t, s, `{"item_id":"item-a"}`)

	entered := installWaitForWriterHook(t)

	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	type waitResult struct {
		code        int
		out, stderr string
	}
	done := make(chan waitResult, 1)
	go func() {
		var out, stderr bytes.Buffer
		code := run(ctx, []string{"wait", "--store", storeArg, "--rules", rules, "--timeout", "400ms"}, &out, &stderr)
		done <- waitResult{code: code, out: out.String(), stderr: stderr.String()}
	}()
	awaitAttached(t, s)

	helper := startSplitWALHelper(t, ctx, s.Path())
	waitedForWriter := false
	select {
	case <-entered:
		waitedForWriter = true
	case <-time.After(100 * time.Millisecond):
	}
	helper.publishAndUnlock(t)

	select {
	case r := <-done:
		if r.code != 0 || r.stderr != "" || !strings.Contains(r.out, "item-a has 2 refusals") {
			t.Fatalf("wait missed split WAL commit: code=%d out=%q stderr=%q", r.code, r.out, r.stderr)
		}
		if !waitedForWriter {
			t.Fatal("wait did not synchronize with the unpublished WAL writer")
		}
	case <-time.After(3 * time.Second):
		t.Fatal("wait did not exit")
	}
}

func TestWatchDaemonWakesWhenWALPrecedesIndexPublish(t *testing.T) {
	s, rules := fixture(t, false)
	alias := filepath.Join(t.TempDir(), "alias.db")
	if err := os.Symlink(s.Path(), alias); err != nil {
		t.Fatal(err)
	}
	fact(t, s, `{"item_id":"item-a"}`)

	entered := installWaitForWriterHook(t)

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan int, 1)
	var out, stderr bytes.Buffer
	ticks := make(chan time.Time)
	go func() {
		done <- runWithTicks(ctx, []string{"daemon", "--store", alias, "--rules", rules}, &out, &stderr, ticks)
	}()
	defer func() {
		cancel()
		<-done
	}()

	// Ensure the daemon completed its initial quiet evaluation pass.
	ticks <- time.Now()

	helper := startSplitWALHelper(t, ctx, s.Path())
	select {
	case <-entered:
	case <-time.After(500 * time.Millisecond):
		helper.close()
		t.Fatal("daemon did not synchronize with the unpublished WAL writer")
	}
	helper.publishAndUnlock(t)

	eventually(t, func() bool {
		return daemonCount(t, s, `SELECT COUNT(*) FROM alert_episodes WHERE state='open'`) == 1
	})
}

func TestWakeDetectsCommitPublishedAfterBeginPass(t *testing.T) {
	s, _ := fixture(t, false)
	other, err := store.Open(t.Context(), s.Path(), false)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { other.Close() })

	wake, err := watchStore(s, make(chan time.Time))
	if err != nil {
		t.Fatal(err)
	}
	defer wake.close()

	if err := wake.beginPass(t.Context()); err != nil {
		t.Fatal(err)
	}
	fact(t, other, `{"item_id":"item-a"}`)
	// Simulate the -wal write notification having been coalesced before the
	// writer published its WAL-index header in -shm.
	if err := wake.drainEvents(); err != nil {
		t.Fatal(err)
	}

	deadline := time.After(200 * time.Millisecond)
	reason, err := wake.next(t.Context(), deadline)
	if err != nil || reason != walWake {
		t.Fatalf("wake.next = (%v, %v), want (walWake, nil)", reason, err)
	}
}

func TestWakeHonorsDeadlineWhileWaitingForWriter(t *testing.T) {
	s, _ := fixture(t, false)
	fact(t, s, `{"item_id":"item-a"}`)

	wake, err := watchStore(s, make(chan time.Time))
	if err != nil {
		t.Fatal(err)
	}
	defer wake.close()

	if err := wake.beginPass(t.Context()); err != nil {
		t.Fatal(err)
	}

	helper := startSplitWALHelper(t, t.Context(), s.Path())
	defer helper.close()
	if err := wake.drainEvents(); err != nil {
		t.Fatal(err)
	}

	timer := time.NewTimer(30 * time.Millisecond)
	defer timer.Stop()
	reason, err := wake.next(t.Context(), timer.C)
	if err != nil || reason != deadlineWake {
		t.Fatalf("wake.next = (%v, %v), want (deadlineWake, nil)", reason, err)
	}
}

func installWaitForWriterHook(t *testing.T) <-chan struct{} {
	t.Helper()
	entered := make(chan struct{})
	var once sync.Once
	fn := func() { once.Do(func() { close(entered) }) }
	onWaitForWriter.Store(&fn)
	t.Cleanup(func() { onWaitForWriter.Store(nil) })
	return entered
}

type splitWALHelper struct {
	cmd       *exec.Cmd
	stdin     io.WriteCloser
	helperErr *bytes.Buffer
	closed    bool
}

func startSplitWALHelper(t *testing.T, ctx context.Context, storePath string) *splitWALHelper {
	t.Helper()
	exe, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stageDir := t.TempDir()
	cmd := exec.CommandContext(ctx, exe, "-test.run=^TestHelperSplitWALCommit$")
	cmd.Env = append(os.Environ(),
		"TILLR_TEST_SPLIT_WAL_STORE="+storePath,
		"TILLR_TEST_SPLIT_WAL_STAGE="+stageDir,
	)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		t.Fatal(err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var helperErr bytes.Buffer
	cmd.Stderr = &helperErr
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	h := &splitWALHelper{cmd: cmd, stdin: stdin, helperErr: &helperErr}
	t.Cleanup(h.close)

	ready, err := bufio.NewReader(stdout).ReadString('\n')
	if err != nil || strings.TrimSpace(ready) != "wal-written" {
		h.close()
		t.Fatalf("helper setup: line=%q err=%v stderr=%s", ready, err, helperErr.String())
	}
	return h
}

func (h *splitWALHelper) publishAndUnlock(t *testing.T) {
	t.Helper()
	if h.closed {
		return
	}
	h.closed = true
	if err := h.stdin.Close(); err != nil {
		t.Fatal(err)
	}
	if err := h.cmd.Wait(); err != nil {
		t.Fatalf("helper publish: %v\n%s", err, h.helperErr.String())
	}
}

func (h *splitWALHelper) close() {
	if h.closed {
		return
	}
	h.closed = true
	_ = h.stdin.Close()
	_ = h.cmd.Wait()
}

func TestHelperSplitWALCommit(t *testing.T) {
	storePath := os.Getenv("TILLR_TEST_SPLIT_WAL_STORE")
	stageDir := os.Getenv("TILLR_TEST_SPLIT_WAL_STAGE")
	if storePath == "" || stageDir == "" {
		return
	}
	ctx := context.Background()

	stagedPath := filepath.Join(stageDir, "state.db")
	for _, suffix := range []string{"", "-wal", "-shm"} {
		data, err := os.ReadFile(storePath + suffix)
		if err != nil {
			fmt.Fprintf(os.Stderr, "read %s%s: %v\n", storePath, suffix, err)
			os.Exit(2)
		}
		if err := os.WriteFile(stagedPath+suffix, data, 0600); err != nil {
			fmt.Fprintf(os.Stderr, "write staged%s: %v\n", suffix, err)
			os.Exit(2)
		}
	}

	beforeWAL, err := os.Stat(stagedPath + "-wal")
	if err != nil {
		fmt.Fprintf(os.Stderr, "stat staged wal: %v\n", err)
		os.Exit(2)
	}
	stagedStore, err := store.Open(ctx, stagedPath, false)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open staged: %v\n", err)
		os.Exit(2)
	}
	err = stagedStore.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Append(event.Event{At: time.Now(), Kind: "drain.refusal", Payload: []byte(`{"item_id":"item-a"}`)})
		return err
	})
	if err != nil {
		fmt.Fprintf(os.Stderr, "append staged: %v\n", err)
		os.Exit(2)
	}
	stagedWALBytes, err := os.ReadFile(stagedPath + "-wal")
	if err != nil {
		fmt.Fprintf(os.Stderr, "read committed staged wal: %v\n", err)
		os.Exit(2)
	}
	stagedSHMBytes, err := os.ReadFile(stagedPath + "-shm")
	if err != nil {
		fmt.Fprintf(os.Stderr, "read committed staged shm: %v\n", err)
		os.Exit(2)
	}
	_ = stagedStore.Close()

	// Hold SQLite's WAL_WRITE_LOCK on the real store without writing any frames
	// of our own, write the committed WAL frames to -wal (triggering fsnotify),
	// wait for the parent, then publish the matching -shm bytes and unlock.
	rawDB, err := sql.Open("sqlite", storePath+"?_pragma=busy_timeout(5000)&_txlock=immediate")
	if err != nil {
		fmt.Fprintf(os.Stderr, "open raw sqlite: %v\n", err)
		os.Exit(2)
	}
	defer rawDB.Close()
	tx, err := rawDB.BeginTx(ctx, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "begin immediate: %v\n", err)
		os.Exit(2)
	}

	walFile, err := os.OpenFile(storePath+"-wal", os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open real wal: %v\n", err)
		os.Exit(2)
	}
	off := beforeWAL.Size()
	if _, err := walFile.WriteAt(stagedWALBytes[off:], off); err != nil {
		fmt.Fprintf(os.Stderr, "write wal tail: %v\n", err)
		os.Exit(2)
	}
	_ = walFile.Close()

	fmt.Println("wal-written")
	_, _ = io.Copy(io.Discard, os.Stdin)

	shmFile, err := os.OpenFile(storePath+"-shm", os.O_WRONLY, 0600)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open real shm: %v\n", err)
		os.Exit(2)
	}
	if _, err := shmFile.WriteAt(stagedSHMBytes, 0); err != nil {
		fmt.Fprintf(os.Stderr, "write shm: %v\n", err)
		os.Exit(2)
	}
	// Do not close shmFile before tx.Rollback: closing a -shm descriptor in this
	// helper process would drop rawDB's POSIX lock prematurely.
	if err := tx.Rollback(); err != nil {
		fmt.Fprintf(os.Stderr, "rollback: %v\n", err)
		os.Exit(2)
	}
	_ = shmFile.Close()
}
