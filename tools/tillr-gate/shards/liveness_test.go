package shards

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
)

func scratchHasEvent(scratchDir, testName, action string) bool {
	info, err := os.Stat(scratchDir)
	if err != nil || !info.IsDir() {
		return false
	}
	found := false
	_ = filepath.WalkDir(scratchDir, func(path string, d os.DirEntry, werr error) error {
		if found || werr != nil || d.IsDir() {
			return nil
		}
		raw, rerr := os.ReadFile(path)
		if rerr != nil {
			return nil
		}
		lines := strings.Split(string(raw), "\n")
		// Only inspect complete newline-terminated lines.
		for _, line := range lines[:len(lines)-1] {
			if line == "" {
				continue
			}
			var ev TestEvent
			if json.Unmarshal([]byte(line), &ev) == nil {
				if ev.Test == testName && ev.Action == action {
					found = true
					return filepath.SkipAll
				}
			}
		}
		return nil
	})
	return found
}

func TestStalledTestIsNamedAndProcessGroupIsKilled(t *testing.T) {
	root := requireTempDir(t)
	childFIFO := filepath.Join(root, "child-fifo")
	if err := syscall.Mkfifo(childFIFO, 0o600); err != nil {
		t.Fatal(err)
	}
	childFD, err := syscall.Open(childFIFO, syscall.O_RDONLY|syscall.O_NONBLOCK, 0)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Close(childFD) })

	childCode := `import sys, time
with open(sys.argv[1], 'wb', buffering=0):
    print('ready', flush=True)
    while True:
        time.sleep(60)
`
	binary := writeFakeScript(t, root, "fake-test", "#!/usr/bin/env python3", fmt.Sprintf(`
import subprocess, sys, time
# Reproduce startup slower than the shard's one-second stall deadline.
time.sleep(1.2)
child = subprocess.Popen([sys.executable, '-c', %q, %q], stdout=subprocess.PIPE)
assert child.stdout.readline() == b'ready\n'
print('=== RUN   TestHung', flush=True)
while True:
    print('still spinning', flush=True)
    time.sleep(0.05)
`, childCode, childFIFO))

	scratch := filepath.Join(root, "scratch")
	livenessDeadline := time.Now().Add(hangGuard)
	var mu sync.Mutex
	cur := time.Unix(1000, 0)
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return cur
	}
	tracker := NewProgressTracker(filepath.Join(root, "unused-progress.json"), 0.02, now)
	tracker.onMaybePublish = func(p *ProgressTracker) {
		if time.Now().After(livenessDeadline) {
			t.Fatal("liveness guard expired waiting for TestHung to start")
		}
		if scratchHasEvent(scratch, "TestHung", "run") {
			mu.Lock()
			cur = time.Unix(1002, 0)
			mu.Unlock()
		}
	}

	_, runErr := RunShard("fixture", root, binary, []string{"TestHung"}, 2, scratch, "0", nil, RunOptions{
		ShardTimeout: 1.0,
		Now:          now,
		PollInterval: 20 * time.Millisecond,
	}, tracker)
	if runErr == nil {
		t.Fatal("expected ShardError")
	}
	var se *ShardError
	if !errors.As(runErr, &se) {
		t.Fatalf("expected *ShardError, got %T: %v", runErr, runErr)
	}
	if !strings.Contains(se.Message, "fixture shard 2") ||
		!strings.Contains(se.Message, "running: TestHung") ||
		!strings.Contains(se.Message, "timed out") {
		t.Fatalf("unexpected error message: %q", se.Message)
	}
	if !se.TimedOut {
		t.Fatalf("expected TimedOut=true")
	}
	sawRun := false
	for _, ev := range se.Events {
		if ev.Action == "run" && ev.Test == "TestHung" {
			sawRun = true
			break
		}
	}
	if !sawRun {
		t.Fatalf("expected TestHung run event in %+v", se.Events)
	}

	// The ready child holds the FIFO open indefinitely. EOF proves the
	// process-group kill closed its descriptor and stopped it.
	eofDeadline := time.Now().Add(hangGuard)
	buf := make([]byte, 1)
	gotEOF := false
	for time.Now().Before(eofDeadline) {
		n, rerr := syscall.Read(childFD, buf)
		if n == 0 && rerr == nil {
			gotEOF = true
			break
		}
		if rerr != nil && rerr != syscall.EAGAIN && rerr != syscall.EWOULDBLOCK {
			t.Fatalf("unexpected read error on child FIFO: %v", rerr)
		}
		time.Sleep(20 * time.Millisecond)
	}
	if !gotEOF {
		t.Fatal("child still holds the FIFO open after process-group kill")
	}
}

func TestProgressingShardCanOutliveStallDeadline(t *testing.T) {
	root := requireTempDir(t)
	names := []string{"TestOne", "TestTwo", "TestThree"}
	gatesDir := filepath.Join(root, "gates")
	if err := os.MkdirAll(gatesDir, 0o755); err != nil {
		t.Fatal(err)
	}
	gateOrder := []string{"start", "0", "1"}
	gateFDs := make(map[string]int, len(gateOrder))
	for _, gname := range gateOrder {
		fifo := filepath.Join(gatesDir, gname)
		if err := syscall.Mkfifo(fifo, 0o600); err != nil {
			t.Fatal(err)
		}
		fd, err := syscall.Open(fifo, syscall.O_RDWR, 0)
		if err != nil {
			t.Fatal(err)
		}
		gateFDs[gname] = fd
		t.Cleanup(func() { _ = syscall.Close(fd) })
	}

	binary := writeFakeScript(t, root, "fake-test", "#!/usr/bin/env python3", `
import pathlib, sys
gates = pathlib.Path(sys.argv[0]).parent / 'gates'
with (gates / 'start').open('rb') as gate:
    gate.read(1)
for index, name in enumerate(('TestOne', 'TestTwo', 'TestThree')):
    print('=== RUN   ' + name, flush=True)
    if index > 0:
        with (gates / str(index - 1)).open('rb') as gate:
            gate.read(1)
    print('--- PASS: ' + name + ' (0.80s)', flush=True)
print('PASS', flush=True)
`)

	livenessDeadline := time.Now().Add(hangGuard)
	var mu sync.Mutex
	baseTime := time.Unix(1000, 0)
	cur := baseTime
	phase := 0
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		return cur
	}
	tracker := NewProgressTracker(filepath.Join(root, "unused-progress.json"), 0.02, now)
	tracker.onMaybePublish = func(p *ProgressTracker) {
		if time.Now().After(livenessDeadline) {
			t.Fatal("liveness guard expired waiting for fake-test progress")
		}
		p.mu.Lock()
		completed := p.completedTests
		p.mu.Unlock()

		mu.Lock()
		defer mu.Unlock()
		step := phase / 2
		if step >= len(gateOrder) {
			return
		}
		if phase%2 == 0 {
			if step == 0 || completed >= step {
				// Advance the stall clock while fake-test is still blocked on its
				// current gate so RunShard checks `remaining` before releasing it.
				cur = cur.Add(1500 * time.Millisecond)
				phase++
			}
		} else {
			_, _ = syscall.Write(gateFDs[gateOrder[step]], []byte("\n"))
			phase++
		}
	}

	events, err := RunShard("fixture", root, binary, names, 0, filepath.Join(root, "scratch"), "0", nil, RunOptions{
		ShardTimeout: 2.0,
		Now:          now,
		PollInterval: 20 * time.Millisecond,
	}, tracker)
	if err != nil {
		t.Fatalf("unexpected RunShard error: %v", err)
	}
	if phase != 2*len(gateOrder) {
		t.Fatalf("expected phase=%d, got %d", 2*len(gateOrder), phase)
	}
	if got := cur.Sub(baseTime); got != 4500*time.Millisecond {
		t.Fatalf("expected elapsed 4.5s, got %v", got)
	}
	passed := map[string]bool{}
	for _, ev := range events {
		if ev.Action == "pass" && ev.Test != "" {
			passed[ev.Test] = true
		}
	}
	want := map[string]bool{"TestOne": true, "TestTwo": true, "TestThree": true}
	if !reflect.DeepEqual(passed, want) {
		t.Fatalf("passed=%v, want %v", passed, want)
	}
}

func TestCLIReportsStallAndPreservesCompletedEvents(t *testing.T) {
	root := requireTempDir(t)
	if err := os.WriteFile(filepath.Join(root, "go.mod"), []byte("module fixture\n\ngo 1.23\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	src := `package fixture
import ("testing"; "time")
func TestPass(t *testing.T) {}
func TestHung(t *testing.T) { for { time.Sleep(time.Second) } }
`
	if err := os.WriteFile(filepath.Join(root, "fixture_test.go"), []byte(src), 0o644); err != nil {
		t.Fatal(err)
	}
	eventsPath := filepath.Join(root, "events.jsonl")
	code, stdout, stderr := runCLIInSubprocess(t, []string{root, "-k", "1", "--shard-timeout", "2", "--events", eventsPath}, nil, "")
	if code == 0 {
		t.Fatalf("expected non-zero exit code; stdout=%s stderr=%s", stdout, stderr)
	}
	if !strings.Contains(stderr, "fixture shard 0 timed out") {
		t.Fatalf("expected stall headline in stderr, got %q", stderr)
	}
	if !strings.Contains(stderr, "running: TestHung") {
		t.Fatalf("expected running: TestHung in stderr, got %q", stderr)
	}
	raw, err := os.ReadFile(eventsPath)
	if err != nil {
		t.Fatal(err)
	}
	var sawPass, sawHungRun bool
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line == "" {
			continue
		}
		var ev TestEvent
		if json.Unmarshal([]byte(line), &ev) == nil {
			if ev.Test == "TestPass" && ev.Action == "pass" {
				sawPass = true
			}
			if ev.Test == "TestHung" && ev.Action == "run" {
				sawHungRun = true
			}
		}
	}
	if !sawPass || !sawHungRun {
		t.Fatalf("expected TestPass pass (%v) and TestHung run (%v) in events", sawPass, sawHungRun)
	}
}
