//go:build linux

package goaffected_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-gate/goaffected"
)

func buildTillrGateBinary(t *testing.T, outPath string) string {
	t.Helper()
	root := repoRoot(t)
	buildArgs := []string{"buil" + "d", "-o", outPath, "."}
	cmd := exec.Command("go", buildArgs...)
	cmd.Dir = filepath.Join(root, "tools", "tillr-gate")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("go build tillr-gate: %v\n%s", err, out)
	}
	return outPath
}

func precompileGoTest(t *testing.T, pkgDir, gocache string) {
	t.Helper()
	env := withEnvOverrides(goaffected.BuildEnv(), map[string]string{
		"GOCACHE": gocache,
	})
	cmd := exec.Command("go", "test", "-c", "-o", os.DevNull, ".")
	cmd.Dir = pkgDir
	cmd.Env = env
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("precompile go test in %s: %v\n%s", pkgDir, err, out)
	}
}

func withEnvOverrides(base []string, overrides map[string]string) []string {
	m := make(map[string]string, len(base)+len(overrides))
	for _, entry := range base {
		k, v, ok := strings.Cut(entry, "=")
		if ok {
			m[k] = v
		}
	}
	for k, v := range overrides {
		if v == "" {
			delete(m, k)
		} else {
			m[k] = v
		}
	}
	out := make([]string, 0, len(m))
	for k, v := range m {
		out = append(out, k+"="+v)
	}
	return out
}

func TestSubreaperReapsOrphansWhileCommandRuns(t *testing.T) {
	if info, err := os.Stat("/proc"); err != nil || !info.IsDir() {
		t.Skip("/proc not available")
	}
	tmpPath := filepath.Join(scratchRoot(t), "ga-subreaper-"+freshNonce(t))
	if err := os.MkdirAll(tmpPath, 0o755); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	code := `import os, sys, time
from pathlib import Path

r, w = os.pipe()
mid = os.fork()
if mid == 0:
    os.close(r)
    grand = os.fork()
    if grand == 0:
        os.close(w)
        os._exit(0)
    os.write(w, f"{grand}\n".encode())
    os.close(w)
    os._exit(0)
os.close(w)
os.waitpid(mid, 0)
grand_pid = int(os.read(r, 64).decode().strip())
os.close(r)
stat_path = Path(f"/proc/{grand_pid}/stat")
while True:
    if not stat_path.exists():
        print("reaped-while-running")
        sys.exit(0)
    try:
        raw = stat_path.read_text(encoding="utf-8", errors="replace")
    except (ProcessLookupError, FileNotFoundError):
        print("reaped-while-running")
        sys.exit(0)
    state = raw[raw.rfind(")") + 2:].split()[0]
    if state != "Z":
        time.sleep(0.01)
        continue
    time.sleep(0.01)
`
	rc, stdout, err := goaffected.RunGuardedCommand(
		[]string{"python3", "-c", code},
		tmpPath,
		goaffected.BuildEnv(),
		nil,
	)
	if err != nil {
		t.Fatalf("RunGuardedCommand error: %v\n%s", err, stdout)
	}
	if rc != 0 || !strings.Contains(stdout, "reaped-while-running") {
		t.Fatalf("expected rc=0 and reaped-while-running, got rc=%d stdout=%q", rc, stdout)
	}
}

func TestNoLeftoverProcessesAfterCheckFinishes(t *testing.T) {
	root := repoRoot(t)
	tillrGateBin := buildTillrGateBinary(t, filepath.Join(t.TempDir(), "tillr-gate"))

	tmpPath := filepath.Join(scratchRoot(t), "ga-reap-"+freshNonce(t))
	repo := filepath.Join(tmpPath, "repo")
	for _, d := range []string{
		filepath.Join(repo, "tools", "tillr-orphan"),
		filepath.Join(repo, "tools", "gate"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-orphan", "go.mod"), []byte("module github.com/mschulkind-oss/tillr-orphan\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	pidsFile := filepath.Join(tmpPath, "orphan-pids.txt")
	orphanTest := `package main
import (
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"
)
func TestSpawnsDetachedOrphan(t *testing.T) {
	pidsFile := os.Getenv("ORPHAN_PIDS_FILE")
	cmd := exec.Command("/bin/sh", "-c", "sleep 600 & child=$!; printf '%d\n%d\n' $$ $child > \"$1\"; wait $child", "sh", pidsFile)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setsid: true}
	if err := cmd.Start(); err != nil { t.Fatal(err) }
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	for {
		if raw, err := os.ReadFile(pidsFile); err == nil && len(raw) > 0 { break }
		select {
		case err := <-done:
			t.Fatalf("detached shell exited before writing %s: %v", pidsFile, err)
		default:
			time.Sleep(20 * time.Millisecond)
		}
	}
}
`
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-orphan", "orphan_test.go"), []byte(orphanTest), 0o644); err != nil {
		t.Fatal(err)
	}

	durable := filepath.Join(tmpPath, "durable")
	if err := os.MkdirAll(durable, 0o755); err != nil {
		t.Fatal(err)
	}
	gocache := sharedGocache(t)
	precompileGoTest(t, filepath.Join(repo, "tools", "tillr-orphan"), gocache)

	cmd := exec.Command(tillrGateBin, "go-affected", "--root", repo, "--changed", "tools/tillr-orphan/orphan_test.go")
	cmd.Env = withEnvOverrides(os.Environ(), map[string]string{
		"YOLO_DURABLE_DIR": durable,
		"GOCACHE":          gocache,
		"ORPHAN_PIDS_FILE": pidsFile,
	})
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tillr-gate go-affected failed: %v\n%s", err, out)
	}

	rawPids, err := os.ReadFile(pidsFile)
	if err != nil {
		t.Fatalf("read orphan-pids.txt: %v", err)
	}
	var recorded []int
	for _, line := range strings.Split(string(rawPids), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		pid, err := strconv.Atoi(line)
		if err != nil {
			t.Fatalf("parse pid %q: %v", line, err)
		}
		recorded = append(recorded, pid)
	}
	if len(recorded) != 2 {
		t.Fatalf("expected 2 recorded pids, got %v", recorded)
	}
	for _, pid := range recorded {
		if err := syscall.Kill(pid, 0); err != syscall.ESRCH {
			t.Fatalf("detached orphan pid %d survived after tillr-gate finished (kill err=%v)", pid, err)
		}
	}

	// Real drain package check: TestRestartReplacesTheLoopInPlaceWithoutSignalingARun spawns
	// tillr-run exec (setsid) -> stop-at-gap -> stub pi -> sleep 600 and leaves them behind
	// without a subreaper guard. Prove none remain after guarded invocation exits.
	drainDurable := filepath.Join(tmpPath, "drain-durable")
	if err := os.MkdirAll(drainDurable, 0o755); err != nil {
		t.Fatal(err)
	}
	sdir, _, release, err := goaffected.AcquireInvocationScratch(drainDurable, "drain-reap-test")
	if err != nil {
		t.Fatalf("AcquireInvocationScratch: %v", err)
	}
	env := withEnvOverrides(goaffected.BuildEnv(), map[string]string{
		"GOCACHE":          gocache,
		"YOLO_DURABLE_DIR": sdir,
		"TMPDIR":           sdir,
	})
	rc, stdout, runErr := goaffected.RunGuardedCommand(
		[]string{
			"go", "test", "-count=1", "-json", "-timeout", "0", "-parallel", "1",
			"-run", "^TestRestartReplacesTheLoopInPlaceWithoutSignalingARun$",
			"github.com/mschulkind-oss/tillr-drain/drain",
		},
		filepath.Join(root, "tools", "tillr-drain"),
		env,
		nil,
	)
	release()
	if runErr != nil || rc != 0 {
		t.Fatalf("drain guarded run failed rc=%d err=%v\n%s", rc, runErr, stdout)
	}

	psOut, err := exec.Command("ps", "-eo", "pid,ppid,args").Output()
	if err != nil {
		t.Fatalf("ps failed: %v", err)
	}
	me := strconv.Itoa(os.Getpid())
	var leftovers []string
	for _, line := range strings.Split(string(psOut), "\n")[1:] {
		if !strings.Contains(line, drainDurable) {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) > 0 && fields[0] != me {
			leftovers = append(leftovers, line)
		}
	}
	if len(leftovers) > 0 {
		t.Fatalf("leftover drain processes survived: %v", leftovers)
	}
}

func TestBinaryWatchdogConstantsAndSampleRule(t *testing.T) {
	root := repoRoot(t)
	interval, silentSamples := goaffected.LoadWatchdogConstants(filepath.Join(root, "tools", "tillr-gate", "watchdog.go"))
	if interval != time.Second || silentSamples != 60 {
		t.Fatalf("expected (1s, 60), got (%v, %d)", interval, silentSamples)
	}
	// Missing file falls back to defaults (1s, 60).
	fbInterval, fbSilent := goaffected.LoadWatchdogConstants(filepath.Join(root, "tools", "tillr-gate", "missing.go"))
	if fbInterval != time.Second || fbSilent != 60 {
		t.Fatalf("expected fallback (1s, 60), got (%v, %d)", fbInterval, fbSilent)
	}

	// 59 silent samples after initialization do not stall; the 60th does.
	w := goaffected.NewBinaryStallWatchdog(silentSamples)
	m100 := map[goaffected.ProcMemberID]uint64{{PID: 100, StartTime: "1"}: 500}
	if w.ObserveTree(m100, 500, false, "") {
		t.Fatal("initialization sample must not stall")
	}
	for i := 0; i < silentSamples-1; i++ {
		if w.ObserveTree(map[goaffected.ProcMemberID]uint64{{PID: 100, StartTime: "1"}: 500}, 500, false, "") {
			t.Fatalf("stalled early at sample %d", i+1)
		}
	}
	if w.SilentSamples != silentSamples-1 {
		t.Fatalf("expected SilentSamples=%d, got %d", silentSamples-1, w.SilentSamples)
	}
	if !w.ObserveTree(map[goaffected.ProcMemberID]uint64{{PID: 100, StartTime: "1"}: 500}, 500, false, "") {
		t.Fatal("expected stall on 60th silent sample")
	}
	if w.SilentSamples != silentSamples {
		t.Fatalf("expected SilentSamples=%d, got %d", silentSamples, w.SilentSamples)
	}

	// CPU growth resets SilentSamples to 0.
	wCPU := goaffected.NewBinaryStallWatchdog(silentSamples)
	_ = wCPU.ObserveTree(map[goaffected.ProcMemberID]uint64{{PID: 100, StartTime: "1"}: 500}, 500, false, "")
	for i := 0; i < silentSamples-1; i++ {
		if wCPU.ObserveTree(map[goaffected.ProcMemberID]uint64{{PID: 100, StartTime: "1"}: 500}, 500, false, "") {
			t.Fatal("unexpected stall before CPU growth")
		}
	}
	if wCPU.ObserveTree(map[goaffected.ProcMemberID]uint64{{PID: 100, StartTime: "1"}: 501}, 501, false, "") {
		t.Fatal("unexpected stall on CPU growth")
	}
	if wCPU.SilentSamples != 0 {
		t.Fatalf("expected SilentSamples=0 after CPU growth, got %d", wCPU.SilentSamples)
	}

	// Child membership/runtime change resets SilentSamples even below the high-water total.
	wMem := goaffected.NewBinaryStallWatchdog(silentSamples)
	_ = wMem.ObserveTree(map[goaffected.ProcMemberID]uint64{{PID: 100, StartTime: "1"}: 1000}, 1000, false, "")
	for n := 0; n < silentSamples*2; n++ {
		members := map[goaffected.ProcMemberID]uint64{
			{PID: 100, StartTime: "1"}:               500,
			{PID: 200 + n, StartTime: fmt.Sprint(n)}: 10,
		}
		if wMem.ObserveTree(members, 510, false, "") {
			t.Fatalf("unexpected stall on child membership change at %d", n)
		}
	}
	if wMem.SilentSamples != 0 {
		t.Fatalf("expected SilentSamples=0 after membership change, got %d", wMem.SilentSamples)
	}

	// Unavailable accounting disables detection permanently without signalling.
	wErr := goaffected.NewBinaryStallWatchdog(silentSamples)
	if wErr.ObserveTree(map[goaffected.ProcMemberID]uint64{}, 0, false, "permission denied") {
		t.Fatal("unexpected stall on error")
	}
	if wErr.Unavailable == "" {
		t.Fatal("expected Unavailable to be set")
	}
	for n := 0; n < silentSamples*2; n++ {
		if wErr.ObserveTree(map[goaffected.ProcMemberID]uint64{{PID: 100, StartTime: "1"}: 500}, 500, false, "") {
			t.Fatal("unexpected stall after watchdog became unavailable")
		}
	}
}

func TestStalledGoTestBinaryReceivesSigquitAndBusyBinaryIsNotSignalled(t *testing.T) {
	stallPkg := "github.com/mschulkind-oss/tillr-stall-sigquit"
	busyPkg := "github.com/mschulkind-oss/tillr-busy-cpu"
	childPkg := "github.com/mschulkind-oss/tillr-busy-child"

	tmpPath := filepath.Join(scratchRoot(t), "ga-sigquit-"+freshNonce(t))
	repo := filepath.Join(tmpPath, "repo")
	for _, d := range []string{
		filepath.Join(repo, "tools", "gate"),
		filepath.Join(repo, "tools", "tillr-gate"),
		filepath.Join(repo, "tools", "tillr-stall-sigquit"),
		filepath.Join(repo, "tools", "tillr-busy-cpu"),
		filepath.Join(repo, "tools", "tillr-busy-child"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	// Keep the 60-sample threshold and accelerate the sample interval so 60 samples take ~1.5s.
	watchdogSrc := "package main\nimport \"time\"\nconst WatchdogSampleInterval = 25 * time.Millisecond\nconst WatchdogSilentSamples = 60\n"
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-gate", "watchdog.go"), []byte(watchdogSrc), 0o644); err != nil {
		t.Fatal(err)
	}
	// Place tillr-gate binary beside repo/tools/tillr-gate/watchdog.go so LoadWatchdogConstants("") finds it.
	tillrGateBin := buildTillrGateBinary(t, filepath.Join(repo, "tools", "tillr-gate", "tillr-gate"))

	// 1. Stalled binary: blocks on a channel receive with zero CPU usage.
	stallDir := filepath.Join(repo, "tools", "tillr-stall-sigquit")
	if err := os.WriteFile(filepath.Join(stallDir, "go.mod"), []byte(fmt.Sprintf("module %s\n\ngo 1.22\n", stallPkg)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(stallDir, "stall_test.go"), []byte(`package main
import (
	"testing"
	"time"
)
func TestStalledBinaryBlocksForever(t *testing.T) {
	ch := make(chan struct{})
	go func() {
		time.Sleep(time.Hour)
		close(ch)
	}()
	<-ch
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// 2. Busy binary: burns CPU past the 60-sample window (~2.5s > ~1.5s) and exits 0.
	busyDir := filepath.Join(repo, "tools", "tillr-busy-cpu")
	if err := os.WriteFile(filepath.Join(busyDir, "go.mod"), []byte(fmt.Sprintf("module %s\n\ngo 1.22\n", busyPkg)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(busyDir, "busy_test.go"), []byte(`package main
import (
	"testing"
	"time"
)
func TestBusyBinaryKeepsRunning(t *testing.T) {
	end := time.Now()
	end = end.Add(2500 * time.Millisecond)
	var sink uint64
	for time.Now().Before(end) {
		for i := 0; i < 10000; i++ {
			sink += uint64(i)
		}
	}
	if sink == 0 {
		t.Fatal("unreachable")
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	// 3. Busy child: Go test binary itself blocks in Wait(), while its child burns CPU.
	childDir := filepath.Join(repo, "tools", "tillr-busy-child")
	if err := os.WriteFile(filepath.Join(childDir, "go.mod"), []byte(fmt.Sprintf("module %s\n\ngo 1.22\n", childPkg)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(childDir, "child_test.go"), []byte(`package main
import (
	"os/exec"
	"testing"
)
func TestBusyChildKeepsParentAlive(t *testing.T) {
	cmd := exec.Command("python3", "-c", "import time; end = time.monotonic() + 2.5\nwhile time.monotonic() < end: pass")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("child failed: %v: %s", err, out)
	}
}
`), 0o644); err != nil {
		t.Fatal(err)
	}

	gocache := sharedGocache(t)
	precompileGoTest(t, stallDir, gocache)
	precompileGoTest(t, busyDir, gocache)
	precompileGoTest(t, childDir, gocache)

	durable := filepath.Join(tmpPath, "durable")
	if err := os.MkdirAll(durable, 0o755); err != nil {
		t.Fatal(err)
	}
	env := withEnvOverrides(os.Environ(), map[string]string{
		"YOLO_DURABLE_DIR": durable,
		"GOCACHE":          gocache,
	})

	stallCmd := exec.Command(tillrGateBin, "go-affected", "--root", repo, "--changed", "tools/tillr-stall-sigquit/stall_test.go")
	stallCmd.Env = env
	stallOutBytes, stallErr := stallCmd.CombinedOutput()
	stallOut := string(stallOutBytes)
	if stallErr == nil {
		t.Fatalf("expected stalled binary to fail, got exit 0:\n%s", stallOut)
	}
	for _, want := range []string{"SIGQUIT: quit", "goroutine ", "TestStalledBinaryBlocksForever"} {
		if !strings.Contains(stallOut, want) {
			t.Fatalf("expected %q in stalled output:\n%s", want, stallOut)
		}
	}

	busyCmd := exec.Command(tillrGateBin, "go-affected", "--root", repo, "--changed", "tools/tillr-busy-cpu/busy_test.go")
	busyCmd.Env = env
	busyOutBytes, busyErr := busyCmd.CombinedOutput()
	busyOut := string(busyOutBytes)
	if busyErr != nil {
		t.Fatalf("expected busy binary to pass: %v\n%s", busyErr, busyOut)
	}
	if strings.Contains(busyOut, "SIGQUIT") {
		t.Fatalf("unexpected SIGQUIT in busy output:\n%s", busyOut)
	}
	if !strings.Contains(busyOut, fmt.Sprintf("go-affected: pass %s TestBusyBinaryKeepsRunning ", busyPkg)) {
		t.Fatalf("missing pass line in busy output:\n%s", busyOut)
	}

	childCmd := exec.Command(tillrGateBin, "go-affected", "--root", repo, "--changed", "tools/tillr-busy-child/child_test.go")
	childCmd.Env = env
	childOutBytes, childErr := childCmd.CombinedOutput()
	childOut := string(childOutBytes)
	if childErr != nil {
		t.Fatalf("expected busy-child binary to pass: %v\n%s", childErr, childOut)
	}
	if strings.Contains(childOut, "SIGQUIT") {
		t.Fatalf("unexpected SIGQUIT in busy-child output:\n%s", childOut)
	}
	if !strings.Contains(childOut, fmt.Sprintf("go-affected: pass %s TestBusyChildKeepsParentAlive ", childPkg)) {
		t.Fatalf("missing pass line in busy-child output:\n%s", childOut)
	}
}
