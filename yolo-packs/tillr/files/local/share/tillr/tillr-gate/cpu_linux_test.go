//go:build linux

package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"syscall"
	"testing"
	"time"
)

func TestProcessAccountingSnapshots(t *testing.T) {
	root := scratch(t, "proc-snapshots")
	log, err := os.CreateTemp(root, "output")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	member := func(pid, start string, ns uint64, children int) {
		dir := filepath.Join(root, pid, "task", pid)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields("S 1 123 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0")
		fields[13], fields[19] = fmt.Sprint(children), start
		if err := os.WriteFile(filepath.Join(root, pid, "stat"), []byte(pid+" (name with ) spaces) "+strings.Join(fields, " ")), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "schedstat"), []byte(fmt.Sprintf("%d 0 0", ns)), 0644); err != nil {
			t.Fatal(err)
		}
	}
	member("123", "1", 1000, 20)
	sampler := processSampler(root, 123, errors.New("counter denied"))
	read := func() watchSample {
		s, err := sampler(123, log)
		if err != nil {
			t.Fatal(err)
		}
		return s
	}
	first := read()
	if first.CPUTimeNS != 200001000 || first.CPUChanged {
		t.Fatalf("bad initial total: %+v", first)
	}
	if read().CPUChanged {
		t.Fatal("idle member marked active")
	}
	// Child exits and is reaped: total must include its CPU, even without growth
	// in the parent's schedstat. More precise runtime then detects tiny starters.
	member("123", "1", 1001, 30)
	grown := read()
	if grown.CPUTimeNS != 300001001 || !grown.CPUChanged {
		t.Fatalf("reaped CPU or starter runtime lost: %+v", grown)
	}
	member("124", "2", 900000000, 0)
	if !read().CPUChanged {
		t.Fatal("new member not progress")
	}
	if err := os.RemoveAll(filepath.Join(root, "124")); err != nil {
		t.Fatal(err)
	}
	exited := read()
	if !exited.CPUChanged || exited.CPUTimeNS != 1200001001 {
		t.Fatalf("lost high-water total or exit progress: %+v", exited)
	}
	// Total stays below its high water; sub-millisecond starter CPU is progress.
	member("123", "1", 1002, 30)
	if !read().CPUChanged {
		t.Fatal("small runtime lost below high-water total")
	}
	member("123", "99", 1002, 30)
	if !read().CPUChanged {
		t.Fatal("reused PID not progress")
	}
	// A second live thread's runtime counts independently.
	dir := filepath.Join(root, "123", "task", "125")
	os.MkdirAll(dir, 0755)
	os.WriteFile(filepath.Join(dir, "schedstat"), []byte("77 0 0"), 0644)
	if !read().CPUChanged {
		t.Fatal("thread CPU not progress")
	}
	os.WriteFile(filepath.Join(dir, "schedstat"), []byte("invalid"), 0644)
	if _, err := sampler(123, log); err == nil {
		t.Fatal("unreadable runtime silently substituted")
	}
	os.Remove(filepath.Join(dir, "schedstat"))
	if _, err := sampler(123, log); err == nil {
		t.Fatal("missing runtime on live task treated as progress")
	}
}

func TestProcessAccountingReapBetweenReads(t *testing.T) {
	root := scratch(t, "proc-reap")
	member := func(pid string, children int) {
		dir := filepath.Join(root, pid, "task", pid)
		if err := os.MkdirAll(dir, 0755); err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields("S 1 123 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 0 1")
		fields[13] = fmt.Sprint(children)
		if err := os.WriteFile(filepath.Join(root, pid, "stat"), []byte(pid+" (check) "+strings.Join(fields, " ")), 0644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "schedstat"), []byte("1000000000 0 0"), 0644); err != nil {
			t.Fatal(err)
		}
	}
	member("123", 0) // Child sorts before parent; traversal order must not matter.
	member("124", 0)
	reaped := false
	read := func(path string) ([]byte, error) {
		if path == filepath.Join(root, "124", "stat") && !reaped {
			reaped = true
			member("124", 100) // Parent has now reaped the child's one second of CPU.
			if err := os.RemoveAll(filepath.Join(root, "123")); err != nil {
				t.Fatal(err)
			}
		}
		return os.ReadFile(path)
	}
	members, total, changed, err := readProcessGroupWithRead(root, 123, read)
	if err != nil {
		t.Fatal(err)
	}
	if total != 2000000000 {
		t.Fatalf("reaped child counted twice: total=%d members=%v", total, members)
	}
	if !changed {
		t.Fatal("disappearance did not count as progress")
	}
}

func TestProcessAccountingFallbackProgress(t *testing.T) {
	for _, mode := range []string{"silent", "cpu", "output", "children", "tiny-children", "discarded-children", "threads"} {
		t.Run(mode, func(t *testing.T) {
			log, err := os.CreateTemp(scratch(t, "fallback-progress"), "output")
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			code := `import os,signal,sys,time,threading
mode = sys.argv[1]
def burn():
    end = time.monotonic() + .04
    while time.monotonic() < end: pass
if mode == 'silent': signal.pause()
if mode == 'discarded-children': signal.signal(signal.SIGCHLD, signal.SIG_IGN)
for n in range(45):
    if mode in ('children', 'tiny-children', 'discarded-children'):
        pid = os.fork()
        if pid == 0:
            if mode == 'tiny-children':
                end = time.monotonic() + .001
                while time.monotonic() < end: pass
            else: burn()
            os._exit(0)
        if mode != 'discarded-children': os.waitpid(pid, 0)
        if mode == 'tiny-children': time.sleep(.04)
        elif mode == 'discarded-children': time.sleep(.05)
    elif mode == 'threads':
        thread = threading.Thread(target=burn)
        thread.start()
        thread.join()
    elif mode == 'output':
        print(n, flush=True)
        time.sleep(.04)
    else: burn()
`
			cmd := exec.Command("python3", "-c", code, mode)
			cmd.Stdout, cmd.Stderr = log, log
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			sampler, closer, err := startWithTaskClock(cmd, func(int) (*os.File, error) { return nil, errors.New("counter denied") })
			if err != nil {
				t.Fatal(err)
			}
			if closer != nil {
				defer closer.Close()
			}
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			sources := &watchSources{Context: ctx, Sample: sampler, Ticks: func() (<-chan time.Time, func()) {
				ticker := time.NewTicker(10 * time.Millisecond)
				return ticker.C, ticker.Stop
			}}
			err, state := watchCommand(cmd, log, sources)
			if state.Unavailable != "" {
				t.Fatalf("fallback unavailable: %+v, %v", state, err)
			}
			if mode == "silent" {
				if state.SilentSamples != WatchdogSilentSamples || err == nil {
					t.Fatalf("idle check not stalled: %+v, %v", state, err)
				}
			} else if err != nil || state.SilentSamples >= WatchdogSilentSamples {
				body, _ := os.ReadFile(log.Name())
				t.Fatalf("active check stopped: %+v, %v; %s", state, err, body)
			}
			if mode != "silent" && mode != "output" && state.previous.CPUTimeMS == 0 {
				t.Fatal("CPU from work was lost")
			}
		})
	}
}

func TestProcessAccountingPreservesExecFailure(t *testing.T) {
	for _, missing := range []bool{true, false} {
		cmd := exec.Command("python3", "-c", "import sys; sys.exit(127)")
		if missing {
			cmd = exec.Command("/nonexistent/checker")
		}
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		_, closer, err := startWithTaskClock(cmd, func(int) (*os.File, error) { return nil, errors.New("counter denied") })
		if closer != nil {
			defer closer.Close()
		}
		if missing {
			if err == nil {
				cmd.Wait()
				t.Fatal("missing command hidden behind successful accounting startup")
			}
		} else {
			if err != nil {
				t.Fatalf("genuine exit 127 classified as exec failure: %v", err)
			}
			cmd.Wait()
			if cmd.ProcessState.ExitCode() != 127 {
				t.Fatalf("exit code changed: %v", cmd.ProcessState)
			}
		}
	}
}

func TestProcessAccountingLifecycle(t *testing.T) {
	for _, mode := range []string{"thread-exec", "sigkill"} {
		t.Run(mode, func(t *testing.T) {
			code := "import os,signal; os.kill(os.getpid(),signal.SIGKILL)"
			if mode == "thread-exec" {
				code = "import os,signal,threading; threading.Thread(target=lambda: os.execlp('true','true')).start(); signal.pause()"
			}
			log, err := os.CreateTemp(scratch(t, "fallback-lifecycle"), "output")
			if err != nil {
				t.Fatal(err)
			}
			defer log.Close()
			cmd := exec.Command("python3", "-c", code)
			cmd.Stdout, cmd.Stderr = log, log
			cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
			sampler, closer, err := startWithTaskClock(cmd, func(int) (*os.File, error) { return nil, errors.New("counter denied") })
			if err != nil {
				t.Fatal(err)
			}
			if closer != nil {
				defer closer.Close()
			}
			// A cleanup guard for an already-exited command, not a check budget.
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			err, state := watchCommand(cmd, log, &watchSources{Context: ctx, Sample: sampler, Ticks: func() (<-chan time.Time, func()) { return nil, func() {} }})
			if errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("watcher hung after thread exec")
			}
			if state.Unavailable != "" {
				t.Fatalf("lifecycle lost accounting: %+v %v", state, err)
			}
			if mode == "thread-exec" && err != nil {
				t.Fatal(err)
			}
			if mode == "sigkill" {
				status, ok := cmd.ProcessState.Sys().(syscall.WaitStatus)
				if !ok || !status.Signaled() || status.Signal() != syscall.SIGKILL {
					t.Fatalf("signal exit changed: %v, %v", cmd.ProcessState, err)
				}
			}
		})
	}
}

func TestNeitherCPUSourceDisablesDetection(t *testing.T) {
	log, err := os.CreateTemp(scratch(t, "fallback-unavailable"), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command("python3", "-c", "import time; time.sleep(.8)")
	cmd.Stdout, cmd.Stderr = log, log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	sampler, closer, err := startWithTaskClock(cmd, func(int) (*os.File, error) { return nil, errors.New("counter denied") })
	if err != nil {
		t.Fatal(err)
	}
	if closer != nil {
		defer closer.Close()
	}
	sampler = processSampler(scratch(t, "no-proc")+"/missing", cmd.Process.Pid, errors.New("counter denied"))
	sources := &watchSources{Sample: sampler, Ticks: func() (<-chan time.Time, func()) {
		ticker := time.NewTicker(time.Millisecond)
		return ticker.C, ticker.Stop
	}}
	err, state := watchCommand(cmd, log, sources)
	if err != nil || !strings.Contains(state.Unavailable, "process accounting") || state.SilentSamples != 0 {
		t.Fatalf("unavailable accounting changed verdict: %+v %v", state, err)
	}
	if state.previous.CPUSource != "" {
		t.Fatal("unavailable source recorded")
	}
	body, _ := os.ReadFile(log.Name())
	if !strings.Contains(string(body), "watchdog unavailable") || strings.Contains(string(body), "watchdog CPU source:") {
		t.Fatalf("unavailability hidden or source falsely advertised: %s", body)
	}
}

func TestTaskClockLaunchThreadsAreIsolated(t *testing.T) {
	old := runtime.GOMAXPROCS(1)
	defer runtime.GOMAXPROCS(old)
	var firstThread int
	open := func(tid int) (*os.File, error) {
		if firstThread == 0 {
			firstThread = tid
		} else if tid == firstThread {
			return nil, errors.New("another check reused an inheritable counter's thread")
		}
		return os.Open("/dev/zero")
	}
	first := exec.Command("true")
	_, held, err := startWithTaskClock(first, open)
	if err != nil {
		t.Fatal(err)
	}
	defer held.Close()
	if err := first.Wait(); err != nil {
		t.Fatal(err)
	}
	for n := 0; n < 20; n++ {
		cmd := exec.Command("true")
		_, closer, err := startWithTaskClock(cmd, open)
		if err != nil {
			t.Fatal(err)
		}
		if closer == nil {
			cmd.Wait()
			t.Fatal("launch thread reused while first counter open")
		}
		cmd.Wait()
		closer.Close()
	}
}

func TestTaskClockCounterExcludesSeparateBusyCheck(t *testing.T) {
	probe, err := openTaskClock(syscall.Gettid())
	if err != nil {
		t.Skipf("kernel counter unavailable: %v", err)
	}
	probe.Close()
	root := scratch(t, "separate-clocks")
	log, err := os.CreateTemp(root, "log")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	input, writer, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer input.Close()
	defer writer.Close()
	idle := exec.Command("sh", "-c", "read unused")
	idle.Stdin = input
	idle.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	sampler, closer, err := startWithTaskClock(idle, openTaskClock)
	if err != nil {
		t.Fatal(err)
	}
	defer closer.Close()
	defer func() { idle.Process.Kill(); idle.Wait() }()
	// Wait for the shell to block in read, not merely for successful startup.
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	for {
		body, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", idle.Process.Pid))
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(body)[strings.LastIndexByte(string(body), ')')+1:])
		if fields[0] == "S" {
			break
		}
		select {
		case <-ctx.Done():
			t.Fatal("shell never blocked in read")
		default:
			runtime.Gosched()
		}
	}
	before, err := sampler(idle.Process.Pid, log)
	if err != nil {
		t.Fatal(err)
	}
	busy := exec.Command("python3", "-c", "import time\nend=time.monotonic()+.1\nwhile time.monotonic()<end: pass")
	busy.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	busySampler, busyCloser, err := startWithTaskClock(busy, openTaskClock)
	if err != nil {
		t.Fatal(err)
	}
	defer busyCloser.Close()
	if err := busy.Wait(); err != nil {
		t.Fatal(err)
	}
	after, err := sampler(idle.Process.Pid, log)
	if err != nil {
		t.Fatal(err)
	}
	worked, err := busySampler(busy.Process.Pid, log)
	if err != nil {
		t.Fatal(err)
	}
	if worked.CPUTimeNS == 0 || after.CPUTimeNS != before.CPUTimeNS {
		t.Fatalf("separate busy command contaminated idle clock: before=%+v after=%+v busy=%+v", before, after, worked)
	}
}

func TestTaskClockInheritsChildrenBeforeExec(t *testing.T) {
	attr := taskClockConfig()
	if attr.Type != 1 || attr.Config != 1 || attr.Size != 64 || attr.Flags&(1|taskClockInherit|1<<12) != 1|taskClockInherit|1<<12 || attr.ReadFormat != 0 {
		t.Fatalf("not an inheritable CPU clock: %+v", attr)
	}
	if attr.Flags&(uint64(1)<<5|uint64(1)<<6) != 0 {
		t.Fatal("kernel CPU was excluded from accounting")
	}
}
func TestDeniedCounterUsesProcessAccounting(t *testing.T) {
	log, err := os.CreateTemp(scratch(t, "watchdog-denied-cpu"), "output")
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd := exec.Command("true")
	cmd.Stdout = log
	cmd.Stderr = log
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	sampler, closer, err := startWithTaskClock(cmd, func(int) (*os.File, error) { return nil, errors.New("counter denied") })
	if err != nil {
		t.Fatal(err)
	}
	if closer != nil {
		defer closer.Close()
	}
	sources := &watchSources{Sample: sampler, Ticks: func() (<-chan time.Time, func()) { return nil, func() {} }}
	err, state := watchCommand(cmd, log, sources)
	if err != nil || state.Unavailable != "" || state.SilentSamples != 0 {
		t.Fatalf("fallback accounting changed verdict: %+v %v", state, err)
	}
	body, err := os.ReadFile(log.Name())
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(body), "watchdog CPU source: process accounting") {
		t.Fatal("fallback CPU source hidden from the live log")
	}
}
