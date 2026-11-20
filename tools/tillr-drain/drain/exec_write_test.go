package drain

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"syscall"
	"testing"
)

// writeForkSafe is os.WriteFile for a file the test may run, or copy a program from, next.
//
// A program written and then run can fail to start with ETXTBSY ("text file busy"): another
// goroutine of this test binary forks while the file is open for writing, the child holds
// a copy of the write descriptor until it execs, and an exec of the file in that window is
// refused. The suite runs its tests in parallel and forks constantly, so under load the
// window is hit: a run measured 288 refusals in 1,200 write-and-run pairs while 16 other
// goroutines forked, and none with the lock below. Holding syscall.ForkLock for reading
// across the write keeps every fork out of the window, because a fork holds the lock for
// writing from before the fork until the child has exec'd. Neither a retry nor a wait can
// replace it: the refusal is a deterministic outcome of the interleaving, not slowness.
func writeForkSafe(path string, data []byte, perm os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, data, perm)
}

// The 2026-11-19 hang: TestProcessStepsGateStopEndsSeparateSessionDescendants… and
// TestStopDuringRollingProofEndsPromptly… wrote a program, started it through a step, and
// polled for a file the program creates. Under load the start was refused with ETXTBSY, the
// step returned that error on its own channel, and the poll, which looked only at the
// file, spun for 55 minutes in a shard that runs with no test deadline.
func TestAProgramWrittenThenRunNeverMeetsTextFileBusy(t *testing.T) {
	dir := scratchUnderDurable(t)
	stop := make(chan struct{})
	var forkers sync.WaitGroup
	for i := 0; i < 8; i++ { // the rest of the suite, forking while a program is being written
		forkers.Add(1)
		go func() {
			defer forkers.Done()
			for {
				select {
				case <-stop:
					return
				default:
					_ = exec.Command("true").Run()
				}
			}
		}()
	}
	var writers sync.WaitGroup
	var busy atomic.Int64
	for g := 0; g < 4; g++ {
		writers.Add(1)
		go func(g int) {
			defer writers.Done()
			for i := 0; i < 60; i++ {
				path := filepath.Join(dir, fmt.Sprintf("program-%d-%d", g, i))
				if err := writeForkSafe(path, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
					t.Error(err)
					return
				}
				if err := exec.Command(path).Run(); errors.Is(err, syscall.ETXTBSY) {
					busy.Add(1)
				} else if err != nil {
					t.Error(err)
					return
				}
			}
		}(g)
	}
	writers.Wait()
	close(stop)
	forkers.Wait()
	if n := busy.Load(); n != 0 {
		t.Fatalf("%d of 240 programs could not start: text file busy", n)
	}
}

// recordingTB stands in for *testing.T so a test can observe the Fatalf waitForPath makes.
type recordingTB struct {
	testing.TB
	message string
}

func (r *recordingTB) Helper() {}

func (r *recordingTB) Fatalf(format string, args ...any) {
	r.message = fmt.Sprintf(format, args...)
	runtime.Goexit()
}

func TestWaitForPathEndsWhenItsProducerEndsFirst(t *testing.T) {
	ended := make(chan error, 1)
	ended <- errors.New("fork/exec: text file busy")
	recorded := &recordingTB{}
	finished := make(chan struct{})
	go func() {
		defer close(finished)
		waitForPath(recorded, filepath.Join(scratchUnderDurable(t), "never-written"), ended)
	}()
	<-finished // returns only because waitForPath failed; with the old poll it never did
	if !strings.Contains(recorded.message, "text file busy") {
		t.Fatalf("the failure %q does not carry the producer's result", recorded.message)
	}
}

func TestWaitForPathReturnsOnceThePathExists(t *testing.T) {
	path := filepath.Join(scratchUnderDurable(t), "written")
	if err := os.WriteFile(path, nil, 0o644); err != nil {
		t.Fatal(err)
	}
	waitForPath(t, path, make(chan error))
}
