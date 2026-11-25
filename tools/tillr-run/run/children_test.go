package run_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestNormalExitEndsWorkerBackgroundChild(t *testing.T) {
	dir := scratch(t)
	pidFile := filepath.Join(dir, "child.pid")
	st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	req := run.Request{RunID: "children", Backend: "command", Command: []string{"sh", "-c", `sleep 300 >/dev/null 2>&1 & echo $! > "$1"`, "worker", pidFile}, WorkingDir: run.WorktreeDir}
	env, err := run.Exec(context.Background(), st, req, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), Now: time.Now()})
	if err != nil {
		t.Fatal(err)
	}
	var rec run.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.EndCause != run.EndNormalExit {
		t.Fatalf("end cause: %s", rec.EndCause)
	}
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	// A killed orphan can remain a zombie until its new parent reaps it. Zombies
	// cannot write in the lane, and are not live processes for lane rotation.
	for deadline := time.Now().Add(hangGuard); time.Now().Before(deadline); {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(stat)[strings.LastIndexByte(string(stat), ')')+1:])
		if len(fields) > 0 && fields[0] == "Z" {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("normal exit left worker child %d alive", pid)
}

func TestHelperDetachedSession(t *testing.T) {
	if os.Getenv("GO_WANT_HELPER_PROCESS") != "1" {
		return
	}
	role := os.Getenv("TILLR_TEST_ROLE")
	dir := os.Getenv("TILLR_TEST_DIR")
	self, err := os.Executable()
	if err != nil {
		os.Exit(2)
	}
	switch role {
	case "worker":
		cmd := exec.Command(self, "-test.run=^TestHelperDetachedSession$")
		cmd.Env = append(os.Environ(), "TILLR_TEST_ROLE=detached")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
		if err := cmd.Start(); err != nil {
			os.Exit(3)
		}
		readyFile := filepath.Join(dir, "ready")
		for deadline := time.Now().Add(hangGuard); time.Now().Before(deadline); {
			if _, err := os.Stat(readyFile); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		if os.Getenv("TILLR_TEST_MODE") == "exit" {
			os.Exit(0)
		}
		time.Sleep(300 * time.Second)
	case "detached":
		if err := os.WriteFile(filepath.Join(dir, "detached.pid"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			os.Exit(4)
		}
		cmd := exec.Command(self, "-test.run=^TestHelperDetachedSession$")
		cmd.Env = append(os.Environ(), "TILLR_TEST_ROLE=nested")
		cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
		if err := cmd.Start(); err != nil {
			os.Exit(5)
		}
		nestedFile := filepath.Join(dir, "nested.pid")
		for deadline := time.Now().Add(hangGuard); time.Now().Before(deadline); {
			if _, err := os.Stat(nestedFile); err == nil {
				break
			}
			time.Sleep(10 * time.Millisecond)
		}
		_ = os.WriteFile(filepath.Join(dir, "ready"), []byte("ok"), 0o644)
		time.Sleep(300 * time.Second)
	case "nested":
		if err := os.WriteFile(filepath.Join(dir, "nested.pid"), []byte(strconv.Itoa(os.Getpid())), 0o644); err != nil {
			os.Exit(6)
		}
		time.Sleep(300 * time.Second)
	}
	os.Exit(0)
}

func assertProcessGone(t *testing.T, pidFile string) {
	t.Helper()
	raw, err := os.ReadFile(pidFile)
	if err != nil {
		t.Fatal(err)
	}
	pid, err := strconv.Atoi(strings.TrimSpace(string(raw)))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	for deadline := time.Now().Add(hangGuard); time.Now().Before(deadline); {
		stat, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
		if os.IsNotExist(err) {
			return
		}
		if err != nil {
			t.Fatal(err)
		}
		fields := strings.Fields(string(stat)[strings.LastIndexByte(string(stat), ')')+1:])
		if len(fields) > 0 && (fields[0] == "Z" || fields[0] == "X") {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("process %d from %s remained alive", pid, pidFile)
}

func TestDetachedSessionAndNestedChildCleanedUpOnExitTimeoutCancelAndPause(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}

	t.Run("normal-exit", func(t *testing.T) {
		dir := scratch(t)
		t.Setenv("GO_WANT_HELPER_PROCESS", "1")
		t.Setenv("TILLR_TEST_ROLE", "worker")
		t.Setenv("TILLR_TEST_DIR", dir)
		t.Setenv("TILLR_TEST_MODE", "exit")

		st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		req := run.Request{
			RunID:      "detached-exit",
			Backend:    "command",
			Command:    []string{self, "-test.run=^TestHelperDetachedSession$"},
			WorkingDir: run.WorktreeDir,
		}
		env, err := run.Exec(context.Background(), st, req, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if rec := recordOf(t, env); rec.EndCause != run.EndNormalExit {
			t.Fatalf("end cause: %s, want %s", rec.EndCause, run.EndNormalExit)
		}
		assertProcessGone(t, filepath.Join(dir, "detached.pid"))
		assertProcessGone(t, filepath.Join(dir, "nested.pid"))
	})

	t.Run("timeout", func(t *testing.T) {
		dir := scratch(t)
		t.Setenv("GO_WANT_HELPER_PROCESS", "1")
		t.Setenv("TILLR_TEST_ROLE", "worker")
		t.Setenv("TILLR_TEST_DIR", dir)
		t.Setenv("TILLR_TEST_MODE", "sleep")

		st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		req := run.Request{
			RunID:      "detached-timeout",
			Backend:    "command",
			Command:    []string{self, "-test.run=^TestHelperDetachedSession$"},
			WorkingDir: run.WorktreeDir,
			TimeoutMS:  500,
		}
		env, err := run.Exec(context.Background(), st, req, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if rec := recordOf(t, env); rec.EndCause != run.EndTimeLimit {
			t.Fatalf("end cause: %s, want %s", rec.EndCause, run.EndTimeLimit)
		}
		assertProcessGone(t, filepath.Join(dir, "detached.pid"))
		assertProcessGone(t, filepath.Join(dir, "nested.pid"))
	})

	t.Run("cancel", func(t *testing.T) {
		dir := scratch(t)
		t.Setenv("GO_WANT_HELPER_PROCESS", "1")
		t.Setenv("TILLR_TEST_ROLE", "worker")
		t.Setenv("TILLR_TEST_DIR", dir)
		t.Setenv("TILLR_TEST_MODE", "sleep")

		st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		ctx, cancel := context.WithCancel(context.Background())
		defer cancel()
		go func() {
			for deadline := time.Now().Add(hangGuard); time.Now().Before(deadline); {
				if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
					cancel()
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
		req := run.Request{
			RunID:      "detached-cancel",
			Backend:    "command",
			Command:    []string{self, "-test.run=^TestHelperDetachedSession$"},
			WorkingDir: run.WorktreeDir,
		}
		env, err := run.Exec(ctx, st, req, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), Now: time.Now()})
		if err != nil {
			t.Fatal(err)
		}
		if rec := recordOf(t, env); rec.EndCause != run.EndSignal {
			t.Fatalf("end cause: %s, want %s", rec.EndCause, run.EndSignal)
		}
		assertProcessGone(t, filepath.Join(dir, "detached.pid"))
		assertProcessGone(t, filepath.Join(dir, "nested.pid"))
	})

	t.Run("pause", func(t *testing.T) {
		dir := scratch(t)
		t.Setenv("GO_WANT_HELPER_PROCESS", "1")
		t.Setenv("TILLR_TEST_ROLE", "worker")
		t.Setenv("TILLR_TEST_DIR", dir)
		t.Setenv("TILLR_TEST_MODE", "sleep")

		ctx := context.Background()
		st, err := store.InitFixture(ctx, filepath.Join(dir, "state.db"))
		if err != nil {
			t.Fatal(err)
		}
		defer st.Close()
		adapterDir := filepath.Join(dir, "adapter")
		if err := os.MkdirAll(adapterDir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(adapterDir, "pause-kind"), []byte(run.PauseCannotResume+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(adapterDir, "extract-report"), []byte("#!/bin/sh\nprev=; out=\nfor arg in \"$@\"; do [ \"$prev\" != --out ] || out=$arg; prev=$arg; done\nprintf 'This run report\\n' > \"$out\"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		adapter := filepath.Join(adapterDir, "run-stub")
		script := fmt.Sprintf("#!/bin/sh\nexec %q -test.run=^TestHelperDetachedSession$\n", self)
		if err := os.WriteFile(adapter, []byte(script), 0o755); err != nil {
			t.Fatal(err)
		}
		laneDir := filepath.Join(dir, "lanes", "lane")
		if err := os.MkdirAll(filepath.Join(laneDir, "worktree"), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(laneDir, "session-id"), []byte("12345678-1234-1234-1234-123456789abc\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		fire := make(chan time.Time)
		req := run.Request{
			RunID:      "detached-pause",
			ItemID:     "item",
			LaneID:     "lane",
			Backend:    "stub",
			Command:    []string{adapter},
			WorkingDir: "worktree",
		}
		opts := run.ExecOptions{
			LaneRoot:    filepath.Join(dir, "lanes"),
			BackendsDir: dir,
			Now:         time.Now(),
			PauseAfter:  func(time.Duration) <-chan time.Time { return fire },
		}
		go func() {
			for deadline := time.Now().Add(hangGuard); time.Now().Before(deadline); {
				if _, err := os.Stat(filepath.Join(dir, "ready")); err == nil {
					if err := run.RequestPause(ctx, st, req.RunID, 2*time.Second, time.Now()); err != nil {
						return
					}
					close(fire)
					return
				}
				time.Sleep(10 * time.Millisecond)
			}
		}()
		env, err := run.Exec(ctx, st, req, opts)
		if err != nil {
			t.Fatal(err)
		}
		if rec := recordOf(t, env); rec.State != run.StatePaused {
			t.Fatalf("state: %s, want %s", rec.State, run.StatePaused)
		}
		assertProcessGone(t, filepath.Join(dir, "detached.pid"))
		assertProcessGone(t, filepath.Join(dir, "nested.pid"))
	})
}

// A resumed run starts under a new execution id, so a process an earlier execution left
// behind (its runner was killed before cleaning up) carries an id nothing looks for. It
// still carries the run's id and the loop store, and the next execution of the run ends it.
func TestExecEndsEarlierExecutionsMarkedProcess(t *testing.T) {
	dir := scratch(t)
	t.Setenv("TILLR_LOOP_STORE", filepath.Join(dir, "state.db"))
	leftover := exec.Command("sleep", "300")
	leftover.Env = append(os.Environ(), "TILLR_RUN_ID=resumed", "TILLR_RUN_EXEC_ID=an-earlier-execution")
	leftover.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := leftover.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() { _ = leftover.Wait(); close(reaped) }()
	t.Cleanup(func() { _ = leftover.Process.Kill(); <-reaped })
	st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	req := run.Request{RunID: "resumed", Backend: "command", Command: []string{"true"}, WorkingDir: run.WorktreeDir}
	if _, err := run.Exec(context.Background(), st, req, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	select {
	case <-reaped:
	case <-time.After(hangGuard):
		t.Fatal("an earlier execution's marked process survived the run's next execution")
	}
}

// Without a loop store the run id is not unique, so a process that merely shares it is
// not touched.
func TestExecLeavesSameRunIDProcessWithoutLoopStore(t *testing.T) {
	dir := scratch(t)
	t.Setenv("TILLR_LOOP_STORE", "")
	other := exec.Command("sleep", "300")
	other.Env = append(os.Environ(), "TILLR_RUN_ID=shared", "TILLR_LOOP_STORE="+filepath.Join(dir, "elsewhere.db"))
	other.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	if err := other.Start(); err != nil {
		t.Fatal(err)
	}
	reaped := make(chan struct{})
	go func() { _ = other.Wait(); close(reaped) }()
	t.Cleanup(func() { _ = other.Process.Kill(); <-reaped })
	st, err := store.Open(context.Background(), filepath.Join(dir, "state.db"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer st.Close()
	req := run.Request{RunID: "shared", Backend: "command", Command: []string{"true"}, WorkingDir: run.WorktreeDir}
	if _, err := run.Exec(context.Background(), st, req, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes"), Now: time.Now()}); err != nil {
		t.Fatal(err)
	}
	if err := other.Process.Signal(syscall.Signal(0)); err != nil {
		t.Fatalf("a process of another store was ended: %v", err)
	}
}
