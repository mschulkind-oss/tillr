package main_test

import (
	"bytes"
	"context"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// A traced child that fails SQLITE_BUSY and exits at once must still deliver its own
// failing span. The store queues trace records for a background writer, so without an
// exit flush the process can end before the outcome line reaches stderr, leaving a
// capture that shows the wait but not how it ended. A real write lock is held here (a
// WriteTx in this process), and several children run against it at once, because the
// loss is a race the delivery goroutine usually loses but sometimes wins.
func TestTracedSyncChildReportsItsBusyFailureBeforeExit(t *testing.T) {
	t.Setenv("TILLR_SQLITE_TRACE", "")
	path := filepath.Join(scratch(t, "trace-exit"), "state.db")
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	held, release := make(chan struct{}), make(chan struct{})
	var releaseOnce sync.Once
	unblock := func() { releaseOnce.Do(func() { close(release) }) }
	holder := make(chan error, 1)
	go func() {
		holder <- s.WriteTx(context.Background(), func(*store.Tx) error { close(held); <-release; return nil })
	}()
	defer func() {
		unblock()
		if err := <-holder; err != nil {
			t.Error(err)
		}
	}()
	<-held
	root := syncRepo(t, map[string]string{"docs/queue.md": syncGood})

	const children = 4
	type outcome struct {
		code   int
		stderr string
	}
	results := make([]outcome, children)
	var wg sync.WaitGroup
	for i := range results {
		wg.Add(1)
		go func(i int) {
			defer wg.Done()
			cmd := exec.Command(bin, "sync", "--store", path, "--root", root, "--json")
			cmd.Env = append(cmd.Environ(), "TILLR_SQLITE_TRACE=1", "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
			var stderr bytes.Buffer
			cmd.Stderr = &stderr
			err := cmd.Run()
			code := 0
			if exit, ok := err.(*exec.ExitError); ok {
				code = exit.ExitCode()
			} else if err != nil {
				code = -1
			}
			results[i] = outcome{code, stderr.String()}
		}(i)
	}
	wg.Wait()
	for i, r := range results {
		if r.code == 0 || !strings.Contains(r.stderr, "SQLITE_BUSY") {
			t.Fatalf("child %d did not fail on the held write lock (exit %d): %s", i, r.code, r.stderr)
		}
		found := false
		for _, line := range strings.Split(r.stderr, "\n") {
			// The failing span is the sync's write transaction: an open of a current
			// store reads and takes no write lock, so it is no longer the migration.
			if strings.HasPrefix(line, "tillr-sqlite ") && strings.Contains(line, ` phase=end `) && strings.Contains(line, `error="sqlite_code=5"`) {
				found = true
			}
		}
		if !found {
			t.Errorf("child %d exited SQLITE_BUSY without delivering its failing span's end record:\n%s", i, r.stderr)
		}
	}
}
