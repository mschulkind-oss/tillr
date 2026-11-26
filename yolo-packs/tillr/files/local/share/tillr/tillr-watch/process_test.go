package main

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

// Re-exec the test binary to exercise main's actual signal handling and POSIX
// record locks (which cannot be observed from another goroutine in this process).
func TestWatchCLIChild(t *testing.T) {
	if os.Getenv("TEST_WATCH_CLI_CHILD") != "1" {
		return
	}
	sep := slices.Index(os.Args, "--")
	os.Args = append([]string{"tillr-watch"}, os.Args[sep+1:]...)
	main()
}

type cliChild struct {
	cmd         *exec.Cmd
	out, stderr bytes.Buffer
	done        chan error
}

func startCLIChild(t *testing.T, args ...string) *cliChild {
	t.Helper()
	c := &cliChild{done: make(chan error, 1)}
	c.cmd = exec.Command(os.Args[0], append([]string{"-test.run=^TestWatchCLIChild$", "--"}, args...)...)
	c.cmd.Env = append(os.Environ(), "TEST_WATCH_CLI_CHILD=1", "TILLR_SQLITE_TRACE=")
	c.cmd.Stdout, c.cmd.Stderr = &c.out, &c.stderr
	if err := c.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		_ = c.cmd.Process.Kill()
		// Wait finishes copying both output streams before the test returns.
		for range c.done {
		}
	})
	go func() { c.done <- c.cmd.Wait(); close(c.done) }()
	return c
}

func (c *cliChild) wait(t *testing.T) error {
	t.Helper()
	select {
	case err := <-c.done:
		return err
	case <-time.After(hangGuard):
		t.Fatal("watch process did not exit")
		return nil
	}
}

func eventually(t *testing.T, check func() bool) {
	t.Helper()
	for deadline := time.Now().Add(hangGuard); time.Now().Before(deadline); time.Sleep(5 * time.Millisecond) {
		if check() {
			return
		}
	}
	t.Fatal("watch state did not arrive")
}

func TestWatchStatusProcessAttachment(t *testing.T) {
	s, rules := fixture(t, false)
	args := []string{"--store", s.Path(), "--rules", rules, "--no-shipped-rules"}
	ctx := context.Background()
	since := func() *int64 {
		var ms *int64
		err := s.ReadTx(ctx, func(rd *store.Reader) error {
			return rd.QueryRow(`SELECT MAX(waiting_since) FROM foreground_delivery WHERE owner_handle='foreground'`).Scan(&ms)
		})
		if err != nil {
			t.Fatal(err)
		}
		return ms
	}
	type ownerStatus struct {
		Attached bool
		Since    *string
		LastWait *string `json:"last_wait"`
		Pending  int
	}
	status := func() ownerStatus {
		var out, stderr bytes.Buffer
		if code := run(ctx, append([]string{"status", "--json"}, args...), &out, &stderr); code != 0 {
			t.Fatalf("status: %d %s", code, &stderr)
		}
		var report struct{ Owners []ownerStatus }
		if err := json.Unmarshal(out.Bytes(), &report); err != nil || len(report.Owners) != 1 {
			t.Fatalf("bad status %s: %v", &out, err)
		}
		return report.Owners[0]
	}
	waitArgs := append([]string{"wait", "--follow"}, args...)
	older := startCLIChild(t, waitArgs...)
	eventually(t, func() bool { return since() != nil })
	oldSince := *since()
	if o := status(); !o.Attached || o.Since == nil || o.LastWait == nil {
		t.Fatalf("older: %+v", o)
	}
	// Open one foreground episode; the follow process prints it but does not mark it seen.
	for i := 0; i < 2; i++ {
		fact(t, s, `{"item_id":"pending"}`)
	}
	eventually(t, func() bool { return status().Pending == 1 })
	newer := startCLIChild(t, waitArgs...)
	eventually(t, func() bool { ms := since(); return ms != nil && *ms != oldSince })
	if err := newer.cmd.Process.Signal(syscall.SIGTERM); err != nil {
		t.Fatal(err)
	}
	var exit *exec.ExitError
	if err := newer.wait(t); !errors.As(err, &exit) || exit.ExitCode() != 1 {
		t.Fatalf("wait did not clean up after cancellation: %v %s", err, &newer.stderr)
	}
	if o := status(); !o.Attached || o.Since != nil || o.LastWait == nil || o.Pending != 1 {
		t.Fatalf("survivor: %+v", o)
	}
	var out bytes.Buffer
	if code := run(ctx, append([]string{"status"}, args...), &out, io.Discard); code != 0 || !strings.Contains(out.String(), "owner foreground watcher=attached since=- last_wait=") {
		t.Fatalf("survivor text: %d %s", code, &out)
	}

	// Start another newer waiter so SIGKILL leaves a stale start time in the row.
	newest := startCLIChild(t, waitArgs...)
	eventually(t, func() bool { return since() != nil })
	for _, c := range []*cliChild{older, newest} {
		if err := c.cmd.Process.Kill(); err != nil {
			t.Fatal(err)
		}
		if err := c.wait(t); !errors.As(err, &exit) || exit.Sys().(syscall.WaitStatus).Signal() != syscall.SIGKILL {
			t.Fatalf("wait not killed: %v", err)
		}
	}
	if since() == nil {
		t.Fatal("kill erroneously cleared the row")
	}
	if o := status(); o.Attached || o.Since != nil || o.LastWait == nil || o.Pending != 1 {
		t.Fatalf("stale row: %+v", o)
	}
	seedStatusOverride(t, s, "refusals", strings.Replace(testRule, `receiver="foreground"`, `receiver="subscribers"`, 1))
	if o := status(); o.Pending != 0 {
		t.Fatalf("status counted an alert whose effective rule delivers to subscribers: %+v", o)
	}
	seedStatusOverride(t, s, "refusals", testRule)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE foreground_delivery SET seen_seq=(SELECT MAX(seq) FROM events)`)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if o := status(); o.Pending != 0 {
		t.Fatalf("status counted a confirmed alert: %+v", o)
	}
}

func TestWatchWaitRefusesConflictingLock(t *testing.T) {
	s, rules := fixture(t, false)
	fact(t, s, `{"item_id":"unobserved"}`)
	fact(t, s, `{"item_id":"unobserved"}`)
	path := filepath.Join(s.Path()+".watch-attached", fmt.Sprintf("%x.lock", sha256.Sum256([]byte("foreground"))))
	if err := os.MkdirAll(filepath.Dir(path), 0700); err != nil {
		t.Fatal(err)
	}
	f, err := os.OpenFile(path, os.O_CREATE|os.O_RDWR, 0600)
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()
	lock := syscall.Flock_t{Type: syscall.F_WRLCK, Len: 1}
	if err := syscall.FcntlFlock(f.Fd(), syscall.F_SETLK, &lock); err != nil {
		t.Fatal(err)
	}
	child := startCLIChild(t, "wait", "--store", s.Path(), "--rules", rules, "--no-shipped-rules", "--timeout", "1s")
	var exit *exec.ExitError
	if err := child.wait(t); !errors.As(err, &exit) || exit.ExitCode() != 1 || !strings.Contains(child.stderr.String(), path) || child.out.Len() != 0 {
		t.Fatalf("wait did not refuse conflicting lock: %v stdout=%q stderr=%q", err, &child.out, &child.stderr)
	}
	if head, err := s.EventHeadSeq(); err != nil || head != 2 {
		t.Fatalf("wait evaluated without a lock: head=%d err=%v", head, err)
	}
	var owners int
	if err := s.ReadTx(t.Context(), func(rd *store.Reader) error {
		return rd.QueryRow(`SELECT COUNT(*) FROM foreground_delivery`).Scan(&owners)
	}); err != nil {
		t.Fatal(err)
	}
	if owners != 0 {
		t.Fatal("wait recorded an owner without a lock")
	}
}
