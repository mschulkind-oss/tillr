package keep

import (
	"bufio"
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"os/signal"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"
	"unsafe"

	"golang.org/x/sys/unix"
	_ "modernc.org/sqlite"
)

const hangGuard = 5 * time.Minute

func TestMain(m *testing.M) {
	switch os.Getenv("TILLR_KEEP_TEST_HELPER") {
	case "drain-stub":
		runDrainStubHelper()
		os.Exit(0)
	case "exit4":
		os.Exit(4)
	case "impostor":
		time.Sleep(30 * time.Second)
		os.Exit(0)
	}
	code := m.Run()
	if builtDrainDir != "" {
		_ = os.RemoveAll(builtDrainDir)
	}
	os.Exit(code)
}

func runDrainStubHelper() {
	name := os.Getenv("TILLR_KEEP_TEST_PROC_NAME")
	if name == "" {
		name = "tillr-drain"
	}
	nameBytes := append([]byte(name), 0)
	_, _, _ = unix.Syscall(unix.SYS_PRCTL, unix.PR_SET_NAME, uintptr(unsafe.Pointer(&nameBytes[0])), 0)

	if pidFile := os.Getenv("TILLR_KEEP_TEST_PID_FILE"); pidFile != "" {
		_ = os.MkdirAll(filepath.Dir(pidFile), 0o755)
		_ = os.WriteFile(pidFile, []byte(strconv.Itoa(os.Getpid())), 0o644)
	}

	sigCh := make(chan os.Signal, 1)
	signal.Notify(sigCh, syscall.SIGTERM, syscall.SIGINT)
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		if _, err := os.Stat("exit"); err == nil {
			return
		}
		select {
		case <-sigCh:
			return
		case <-time.After(10 * time.Millisecond):
		}
	}
}

var (
	builtDrainOnce sync.Once
	builtDrainDir  string
	builtDrainPath string
	builtDrainErr  error
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	// wd is tools/tillr-drain/keep
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

func buildDrainBinary(t *testing.T) string {
	t.Helper()
	builtDrainOnce.Do(func() {
		dir, err := os.MkdirTemp("", "tillr-keep-test-bin-")
		if err != nil {
			builtDrainErr = err
			return
		}
		builtDrainDir = dir
		bin := filepath.Join(dir, "tillr-drain")
		modDir := filepath.Clean(filepath.Join(repoRoot(t), "tools", "tillr-drain"))
		cmd := exec.Command("go", "build", "-o", bin, ".")
		cmd.Dir = modDir
		if out, err := cmd.CombinedOutput(); err != nil {
			builtDrainErr = fmt.Errorf("go build tillr-drain: %v\n%s", err, out)
			return
		}
		builtDrainPath = bin
	})
	if builtDrainErr != nil {
		t.Fatal(builtDrainErr)
	}
	return builtDrainPath
}

func startDrainStub(t *testing.T, root string, procName string, argv ...string) *exec.Cmd {
	t.Helper()
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := &exec.Cmd{
		Path: self,
		Args: append([]string{filepath.Join(root, ".stub", "tillr-drain")}, argv...),
		Dir:  root,
		Env: append(os.Environ(),
			"TILLR_KEEP_TEST_HELPER=drain-stub",
			"TILLR_KEEP_TEST_PROC_NAME="+procName,
		),
	}
	if err := cmd.Start(); err != nil {
		t.Fatalf("start drain stub: %v", err)
	}
	wantComm := procName
	if len(wantComm) > 15 {
		wantComm = wantComm[:15]
	}
	deadline := time.Now().Add(hangGuard)
	for {
		if info, ok := Process(cmd.Process.Pid); ok && info.Comm == wantComm {
			break
		}
		if time.Now().After(deadline) {
			_ = cmd.Process.Kill()
			_ = cmd.Wait()
			t.Fatalf("drain stub never renamed itself to %q", wantComm)
		}
		time.Sleep(10 * time.Millisecond)
	}
	return cmd
}

func stopAndWait(cmd *exec.Cmd) {
	if cmd == nil || cmd.Process == nil {
		return
	}
	_ = cmd.Process.Signal(syscall.SIGTERM)
	done := make(chan struct{})
	go func() {
		_ = cmd.Wait()
		close(done)
	}()
	select {
	case <-done:
	case <-time.After(hangGuard):
		_ = cmd.Process.Kill()
		<-done
	}
}

func TestThreeStubExitsWait60_120_240(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	stop := make(chan struct{})
	var waits []int
	var launches []*LoopChild
	var out bytes.Buffer

	launch := func() (*LoopChild, error) {
		cmd := exec.Command(self)
		cmd.Env = append(os.Environ(), "TILLR_KEEP_TEST_HELPER=exit4")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		child := NewOwnedChild(cmd)
		launches = append(launches, child)
		return child, nil
	}
	observe := func(child *LoopChild) bool {
		exited := ObserveLoop(stop, child, &out)
		if child.ExitCode() != 4 {
			t.Errorf("got exit code %d, want 4", child.ExitCode())
		}
		return exited
	}
	wait := func(delay int) bool {
		waits = append(waits, delay)
		if len(waits) == 3 {
			close(stop)
			return true
		}
		return false
	}

	err = Keep(stop, func() (*LoopChild, error) { return nil, nil }, launch, observe, wait, func() float64 { return 0 }, &out)
	if err != nil {
		t.Fatal(err)
	}
	if len(launches) != 3 {
		t.Fatalf("got %d launches, want 3", len(launches))
	}
	if !reflect.DeepEqual(waits, []int{60, 120, 240}) {
		t.Fatalf("got waits %v, want [60 120 240]", waits)
	}
}

func TestAdoptedExitRestartsWithProductionDiscoveryAndObservation(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	marker := filepath.Join(root, "exit")
	adopted := startDrainStub(t, root, "tillr-drain", "loop")
	defer stopAndWait(adopted)

	var launched []*LoopChild
	var waits []int
	stop := make(chan struct{})
	var out bytes.Buffer
	adoptedDone := make(chan struct{})
	go func() {
		_ = adopted.Wait()
		close(adoptedDone)
	}()

	find := func() (*LoopChild, error) {
		pid, err := FindLoop(root)
		if err != nil || pid == 0 {
			return nil, err
		}
		return NewAdoptedChild(pid), nil
	}
	launch := func() (*LoopChild, error) {
		cmd := exec.Command(self)
		cmd.Dir = root
		cmd.Env = append(os.Environ(), "TILLR_KEEP_TEST_HELPER=exit4")
		if err := cmd.Start(); err != nil {
			return nil, err
		}
		child := NewOwnedChild(cmd)
		launched = append(launched, child)
		return child, nil
	}
	observe := func(child *LoopChild) bool {
		_ = os.WriteFile(marker, nil, 0o644)
		return observeLoopWithPoll(stop, child, &out, 20*time.Millisecond)
	}
	wait := func(seconds int) bool {
		<-adoptedDone
		waits = append(waits, seconds)
		if len(waits) == 3 {
			close(stop)
			return true
		}
		return false
	}

	if err := Keep(stop, find, launch, observe, wait, nil, &out); err != nil {
		t.Fatal(err)
	}
	if len(launched) != 2 {
		t.Fatalf("got %d launches, want 2", len(launched))
	}
	if !reflect.DeepEqual(waits, []int{60, 120, 240}) {
		t.Fatalf("got waits %v, want [60 120 240]", waits)
	}
	for _, c := range launched {
		if c.ExitCode() != 4 {
			t.Errorf("launched child exit code = %d, want 4", c.ExitCode())
		}
	}
}

func TestLoopRootOverridesAreRefused(t *testing.T) {
	dir := t.TempDir()
	for _, option := range []string{"--root", "--root=elsewhere", "-root", "-root=elsewhere"} {
		var stdout, stderr bytes.Buffer
		code := Run([]string{"--root", dir, "--", option, "elsewhere"}, &stdout, &stderr)
		if code != 2 {
			t.Errorf("option %q: got exit %d, want 2 (stderr: %s)", option, code, stderr.String())
		}
		if !strings.Contains(stderr.String(), "not a loop override") {
			t.Errorf("option %q: stderr %q missing 'not a loop override'", option, stderr.String())
		}
	}
	if _, err := os.Stat(filepath.Join(dir, ".tillr")); !os.IsNotExist(err) {
		t.Fatalf(".tillr should not have been created")
	}
}

func TestOnlyLoopFaceForThisRootIsTheLoop(t *testing.T) {
	root := t.TempDir()
	var others []*exec.Cmd
	for _, face := range []string{"why", "plan", "once", "stop", "policy", "keep"} {
		others = append(others, startDrainStub(t, root, "tillr-drain", face))
	}
	others = append(others, startDrainStub(t, root, "tillr-drain", "loop", "--root", filepath.Join(root, "elsewhere")))
	defer func() {
		for _, c := range others {
			stopAndWait(c)
		}
	}()

	pid, err := FindLoop(root)
	if err != nil {
		t.Fatal(err)
	}
	if pid != 0 {
		t.Fatalf("expected no loop, found pid %d", pid)
	}

	loop := startDrainStub(t, root, "tillr-drain", "loop", "--root", root, "--lanes", "5")
	defer stopAndWait(loop)

	pid, err = FindLoop(root)
	if err != nil {
		t.Fatal(err)
	}
	if pid != loop.Process.Pid {
		t.Fatalf("got pid %d, want %d", pid, loop.Process.Pid)
	}
}

func TestJustRecipeKeepsEmptyLoopArgument(t *testing.T) {
	dir := t.TempDir()
	// Stand-in tillr-drain on PATH prints its argv as a JSON array.
	stub := filepath.Join(dir, "tillr-drain")
	script := `#!/bin/sh
printf '['
first=1
for arg in "$@"; do
    if [ "$first" -eq 0 ]; then printf ','; fi
    first=0
    printf '"%s"' "$arg"
done
printf ']\n'
`
	if err := os.WriteFile(stub, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("just", "--justfile", filepath.Join(repoRoot(t), ".justfile"), "tillr-keep",
		"--root", dir, "--", "--rolling", "", "--lanes", "5", "a b")
	cmd.Env = append(os.Environ(), "PATH="+dir+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("just tillr-keep failed: %v\n%s", err, out)
	}
	var got []string
	if err := json.Unmarshal(bytes.TrimSpace(out), &got); err != nil {
		t.Fatalf("unmarshal %q: %v", out, err)
	}
	want := []string{"keep", "--root", dir, "--", "--rolling", "", "--lanes", "5", "a b"}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got argv %q, want %q", got, want)
	}
}

func TestCapAndHealthyReset(t *testing.T) {
	delay := 60
	var waits []int
	for i := 0; i < 8; i++ {
		var w int
		w, delay = RestartDelay(delay, 299)
		waits = append(waits, w)
	}
	want := []int{60, 120, 240, 480, 960, 1800, 1800, 1800}
	if !reflect.DeepEqual(waits, want) {
		t.Fatalf("got waits %v, want %v", waits, want)
	}
	w, next := RestartDelay(delay, 300)
	if w != 60 || next != 60 {
		t.Fatalf("got (%d, %d), want (60, 60)", w, next)
	}
}

func TestCommandOnlyComposesLoop(t *testing.T) {
	dir, err := os.MkdirTemp(t.TempDir(), "consumer repo ")
	if err != nil {
		t.Fatal(err)
	}
	command := LoopCommand(dir, []string{"--lanes", "3"})
	wantHead := []string{"tillr-drain", "loop", "--root", dir}
	if !reflect.DeepEqual(command[:4], wantHead) {
		t.Fatalf("head = %v, want %v", command[:4], wantHead)
	}
	for _, face := range []string{"gate", "land", "verdict"} {
		found := false
		for i := 0; i < len(command)-1; i++ {
			if command[i] == "--"+face && command[i+1] == "tillr-loop "+face {
				found = true
				break
			}
		}
		if !found {
			t.Errorf("command %v missing --%s tillr-loop %s", command, face, face)
		}
	}
	for _, forbidden := range []string{"retry", "--no-after-merge-check"} {
		for _, arg := range command {
			if arg == forbidden {
				t.Errorf("command %v unexpectedly contains %q", command, forbidden)
			}
		}
	}
	if !reflect.DeepEqual(command[len(command)-2:], []string{"--lanes", "3"}) {
		t.Fatalf("tail = %v, want [--lanes 3]", command[len(command)-2:])
	}
}

func TestProcessNameNotCommandLineAndRepoScope(t *testing.T) {
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	impostor := exec.Command(self, "tillr-drain")
	impostor.Dir = root
	impostor.Env = append(os.Environ(), "TILLR_KEEP_TEST_HELPER=impostor")
	if err := impostor.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopAndWait(impostor)

	real := startDrainStub(t, root, "tillr-drain", "loop")
	defer stopAndWait(real)

	pid, err := FindLoop(root)
	if err != nil {
		t.Fatal(err)
	}
	if pid != real.Process.Pid {
		t.Fatalf("got pid %d, want %d", pid, real.Process.Pid)
	}
	otherPID, err := FindLoop(filepath.Join(root, "other"))
	if err != nil {
		t.Fatal(err)
	}
	if otherPID != 0 {
		t.Fatalf("expected 0 for other root, got %d", otherPID)
	}
}

func TestStopLeavesLaunchedLoopRunning(t *testing.T) {
	drainBin := buildDrainBinary(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	root := t.TempDir()
	// Stub tillr-drain in root that execs our drain-stub helper with comm tillr-drain-abc123.
	stub := filepath.Join(root, "tillr-drain")
	stubScript := fmt.Sprintf("#!/bin/sh\nexport TILLR_KEEP_TEST_HELPER=drain-stub\nexport TILLR_KEEP_TEST_PROC_NAME=tillr-drain-abc123\nexec -a %q %q \"$@\"\n", stub, self)
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatal(err)
	}
	// Note: POSIX sh `exec -a` is bash; use bash shebang.
	stubScript = fmt.Sprintf("#!/usr/bin/env bash\nexport TILLR_KEEP_TEST_HELPER=drain-stub\nexport TILLR_KEEP_TEST_PROC_NAME=tillr-drain-abc123\nexec -a %q %q \"$@\"\n", stub, self)
	if err := os.WriteFile(stub, []byte(stubScript), 0o755); err != nil {
		t.Fatal(err)
	}

	env := append(os.Environ(), "PATH="+root+string(os.PathListSeparator)+os.Getenv("PATH"))
	child := exec.Command(drainBin, "keep", "--root", root)
	child.Env = env
	stdoutPipe, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = child.Stdout
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	var loopPID int
	defer func() {
		stopAndWait(child)
		if loopPID > 0 {
			_ = syscall.Kill(loopPID, syscall.SIGTERM)
		}
	}()

	reader := bufio.NewReader(stdoutPipe)
	line1, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read line1: %v", err)
	}
	if !strings.Contains(line1, "started loop") {
		t.Fatalf("line1 = %q, want 'started loop'", line1)
	}
	fields := strings.Fields(line1)
	loopPID, err = strconv.Atoi(fields[len(fields)-1])
	if err != nil {
		t.Fatalf("parse loop pid from %q: %v", line1, err)
	}
	line2, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read line2: %v", err)
	}
	if !strings.Contains(line2, "watching") {
		t.Fatalf("line2 = %q, want 'watching'", line2)
	}

	deadline := time.Now().Add(hangGuard)
	for {
		info, ok := Process(loopPID)
		if ok && info.Comm == "tillr-drain-abc" {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("loop %d never renamed to tillr-drain-abc (got %+v)", loopPID, info)
		}
		time.Sleep(10 * time.Millisecond)
	}

	found, err := FindLoop(root)
	if err != nil {
		t.Fatal(err)
	}
	if found != loopPID {
		t.Fatalf("FindLoop = %d, want %d", found, loopPID)
	}
	loopPGID, err := syscall.Getpgid(loopPID)
	if err != nil {
		t.Fatal(err)
	}
	childPGID, err := syscall.Getpgid(child.Process.Pid)
	if err != nil {
		t.Fatal(err)
	}
	if loopPGID == childPGID {
		t.Fatalf("loop and keeper share pgid %d", loopPGID)
	}

	// Run the actual just recipe for stop, scoped to this scratch checkout, with our built tillr-drain on PATH.
	stopCmd := exec.Command("just", "--justfile", filepath.Join(repoRoot(t), ".justfile"),
		"tillr-keep", "--root", root, "--stop")
	stopCmd.Env = append(os.Environ(), "PATH="+filepath.Dir(drainBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	if out, err := stopCmd.CombinedOutput(); err != nil {
		t.Fatalf("just tillr-keep --stop failed: %v\n%s", err, out)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("keeper exited with error: %v", err)
	}
	info, ok := Process(loopPID)
	if !ok || info.State == "Z" {
		t.Fatalf("loop %d should still be running, got ok=%v info=%+v", loopPID, ok, info)
	}
	found, err = FindLoop(root)
	if err != nil {
		t.Fatal(err)
	}
	if found != loopPID {
		t.Fatalf("FindLoop after stop = %d, want %d", found, loopPID)
	}
}

func TestStopLeavesAdoptedLoopAndPreventsSecondKeeper(t *testing.T) {
	drainBin := buildDrainBinary(t)
	root := t.TempDir()
	loop := startDrainStub(t, root, "tillr-drain", "loop")
	defer stopAndWait(loop)

	child := exec.Command(drainBin, "keep", "--root", root)
	stdoutPipe, err := child.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	child.Stderr = child.Stdout
	if err := child.Start(); err != nil {
		t.Fatal(err)
	}
	defer stopAndWait(child)

	reader := bufio.NewReader(stdoutPipe)
	line, err := reader.ReadString('\n')
	if err != nil {
		t.Fatalf("read line: %v", err)
	}
	if !strings.Contains(line, "watching") {
		t.Fatalf("line = %q, want 'watching'", line)
	}

	second := exec.Command(drainBin, "keep", "--root", root)
	secondOut, err := second.CombinedOutput()
	if err == nil {
		t.Fatalf("expected second keeper to fail, got exit 0")
	}
	if second.ProcessState.ExitCode() != 2 {
		t.Fatalf("second keeper exit = %d, want 2 (out: %s)", second.ProcessState.ExitCode(), secondOut)
	}
	if !strings.Contains(string(secondOut), "already running") {
		t.Fatalf("second keeper out = %q, want 'already running'", secondOut)
	}

	stopped := exec.Command(drainBin, "keep", "--root", root, "--stop")
	if out, err := stopped.CombinedOutput(); err != nil {
		t.Fatalf("--stop failed: %v\n%s", err, out)
	}
	if err := child.Wait(); err != nil {
		t.Fatalf("keeper wait: %v", err)
	}
	if info, ok := Process(loop.Process.Pid); !ok || info.State == "Z" {
		t.Fatalf("adopted loop should still be alive, got ok=%v info=%+v", ok, info)
	}
}

func TestAmbiguousProcessNamesRefuse(t *testing.T) {
	root := t.TempDir()
	c1 := startDrainStub(t, root, "tillr-drain", "loop")
	c2 := startDrainStub(t, root, "tillr-drain", "loop")
	defer stopAndWait(c1)
	defer stopAndWait(c2)

	_, err := FindLoop(root)
	if err == nil || !strings.Contains(err.Error(), "multiple tillr-drain") {
		t.Fatalf("got err %v, want 'multiple tillr-drain'", err)
	}
}

func TestStopDuringWaitPreventsRestart(t *testing.T) {
	stop := make(chan struct{})
	var launched []int
	var out bytes.Buffer

	wait := func(delay int) bool {
		close(stop)
		return true
	}
	err := Keep(
		stop,
		func() (*LoopChild, error) { return nil, nil },
		func() (*LoopChild, error) {
			launched = append(launched, 1)
			return NewAdoptedChild(1), nil
		},
		func(*LoopChild) bool { return true },
		wait,
		func() float64 { return 0 },
		&out,
	)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(launched, []int{1}) {
		t.Fatalf("launched = %v, want [1]", launched)
	}
}

func TestSavedScaleTargetSurvivesCrashRelaunchAndUnflaggedRestart(t *testing.T) {
	stripped := StripLanes([]string{"--policy", "p.json", "--lanes", "1", "--rolling", "", "--lanes=2", "-lanes", "3"})
	wantStripped := []string{"--policy", "p.json", "--rolling", ""}
	if !reflect.DeepEqual(stripped, wantStripped) {
		t.Fatalf("StripLanes = %q, want %q", stripped, wantStripped)
	}

	root := t.TempDir()
	dbPath := filepath.Join(root, ".tillr", "state.db")
	if err := os.MkdirAll(filepath.Dir(dbPath), 0o755); err != nil {
		t.Fatal(err)
	}
	sessionStart := time.Date(2026, 11, 23, 4, 0, 0, 0, time.UTC)
	before := sessionStart.Add(-5 * time.Minute).Format(time.RFC3339)
	after := sessionStart.Add(5 * time.Minute).Format(time.RFC3339)

	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`CREATE TABLE drain_runtime_config (
		key TEXT PRIMARY KEY, value_json TEXT NOT NULL, source TEXT NOT NULL,
		updated_at TEXT NOT NULL, updated_by TEXT NOT NULL)`)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`INSERT INTO drain_runtime_config VALUES ('ordinary_lanes', '6', 'operator', ?, 'matt')`, before)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	extra := []string{"--policy", "config/custom.json", "--lanes", "1"}
	// First launch and any retry before drain_runtime_config is seeded in this session keep --lanes 1.
	if got := LaunchExtraForAttempt(root, extra, sessionStart, false, 0); !reflect.DeepEqual(got, extra) {
		t.Fatalf("launch 0 = %q, want %q", got, extra)
	}
	if got := LaunchExtraForAttempt(root, extra, sessionStart, false, 1); !reflect.DeepEqual(got, extra) {
		t.Fatalf("launch 1 before seed = %q, want %q", got, extra)
	}

	// Once seeded and later live-scaled to 4 during this session, crash relaunch omits --lanes.
	db, err = sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	_, err = db.Exec(`UPDATE drain_runtime_config SET value_json='4', updated_at=? WHERE key='ordinary_lanes'`, after)
	if err != nil {
		t.Fatal(err)
	}
	db.Close()

	wantNoLanes := []string{"--policy", "config/custom.json"}
	if got := LaunchExtraForAttempt(root, extra, sessionStart, false, 1); !reflect.DeepEqual(got, wantNoLanes) {
		t.Fatalf("launch 1 after seed = %q, want %q", got, wantNoLanes)
	}
	if got := LaunchExtraForAttempt(root, extra, sessionStart, true, 0); !reflect.DeepEqual(got, wantNoLanes) {
		t.Fatalf("adopted launch = %q, want %q", got, wantNoLanes)
	}

	// Unflagged --restart preserves saved 4 instead of re-passing boot-time --lanes 1.
	if err := WriteRecordedLaunch(root, "config/custom.json", "1", true, true); err != nil {
		t.Fatal(err)
	}
	args, policy, lanes, _, _, err := ResolveRestartArgs(root, nil, 0)
	if err != nil {
		t.Fatal(err)
	}
	if policy != "config/custom.json" || lanes != "4" || !reflect.DeepEqual(args, wantNoLanes) {
		t.Fatalf("ResolveRestartArgs = (%q, %q, %q), want (%q, config/custom.json, 4)", args, policy, lanes, wantNoLanes)
	}

	// Explicit --lanes 2 on --restart still wins.
	args2, policy2, lanes2, _, _, err := ResolveRestartArgs(root, []string{"--lanes", "2"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if policy2 != "config/custom.json" || lanes2 != "2" {
		t.Fatalf("explicit --lanes: policy=%q lanes=%q", policy2, lanes2)
	}
	hasLanesFlag := false
	for _, a := range args2 {
		if a == "--lanes" {
			hasLanesFlag = true
		}
	}
	if !hasLanesFlag {
		t.Fatalf("args2 %q should contain --lanes", args2)
	}
}

func TestKeepHandlesMaxAgentsFlag(t *testing.T) {
	stripped := StripLanes([]string{"--policy", "p.json", "--max-agents", "1", "--rolling", "", "--max-agents=2", "-max-agents", "3", "-max-agents=4"})
	wantStripped := []string{"--policy", "p.json", "--rolling", ""}
	if !reflect.DeepEqual(stripped, wantStripped) {
		t.Fatalf("StripLanes = %q, want %q", stripped, wantStripped)
	}

	for _, tc := range []struct {
		name string
		args []string
		want string
	}{
		{"double-dash", []string{"--policy", "p.json", "--max-agents", "5"}, "5"},
		{"double-dash-eq", []string{"--policy=p.json", "--max-agents=6"}, "6"},
		{"single-dash", []string{"-max-agents", "7"}, "7"},
		{"single-dash-eq", []string{"-max-agents=8"}, "8"},
	} {
		_, got, _, has := ExtractPolicyAndLanes(tc.args)
		if !has || got != tc.want {
			t.Fatalf("%s: ExtractPolicyAndLanes = (%q, %v), want (%q, true)", tc.name, got, has, tc.want)
		}
	}

	root := t.TempDir()
	if err := WriteRecordedLaunch(root, "config/custom.json", "1", true, true); err != nil {
		t.Fatal(err)
	}
	args, policy, lanes, _, _, err := ResolveRestartArgs(root, []string{"--max-agents", "3"}, 0)
	if err != nil {
		t.Fatal(err)
	}
	if policy != "config/custom.json" || lanes != "3" {
		t.Fatalf("explicit --max-agents: policy=%q lanes=%q, want (config/custom.json, 3)", policy, lanes)
	}
	if !reflect.DeepEqual(args, []string{"--max-agents", "3", "--policy", "config/custom.json"}) {
		t.Fatalf("args = %q, want [--max-agents 3 --policy config/custom.json]", args)
	}
}

func TestStopAbsentIsNoopAndHelpDoesNotStart(t *testing.T) {
	dir := t.TempDir()
	var stdout, stderr bytes.Buffer
	code := Run([]string{"--root", dir, "--stop"}, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("--stop absent exit = %d, want 0 (stderr: %s)", code, stderr.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".tillr")); !os.IsNotExist(err) {
		t.Fatalf(".tillr should not be created by --stop")
	}

	drainBin := buildDrainBinary(t)
	cmd := exec.Command("just", "--justfile", filepath.Join(repoRoot(t), ".justfile"), "tillr-keep", "--help")
	cmd.Env = append(os.Environ(), "PATH="+filepath.Dir(drainBin)+string(os.PathListSeparator)+os.Getenv("PATH"))
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("just tillr-keep --help failed: %v\n%s", err, out)
	}
	if !strings.Contains(string(out), "--stop") {
		t.Fatalf("--help output %q missing --stop", out)
	}
}

func TestWaitForExitWaitsPast60sAndPrintsWhatItWaitsOn(t *testing.T) {
	states := []struct {
		info ProcInfo
		ok   bool
	}{
		{ProcInfo{Comm: "tillr-drain", Start: "12345", State: "S"}, true},
		{ProcInfo{Comm: "tillr-drain", Start: "12345", State: "S"}, true},
		{ProcInfo{Comm: "tillr-drain", Start: "12345", State: "S"}, true},
		{ProcInfo{Comm: "tillr-drain", Start: "12345", State: "S"}, true},
		{ProcInfo{Comm: "tillr-drain", Start: "12345", State: "S"}, true},
		{ProcInfo{}, false},
	}
	idx := 0
	now := time.Unix(0, 0)
	var out bytes.Buffer

	WaitForExit(4242, WaitOpts{
		ReportInterval: 30 * time.Second,
		PollInterval:   50 * time.Millisecond,
		Clock:          func() time.Time { return now },
		Sleep:          func(time.Duration) { now = now.Add(35 * time.Second) },
		ProcessFn: func(int) (ProcInfo, bool) {
			s := states[idx]
			idx++
			return s.info, s.ok
		},
	}, &out)

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if now.Sub(time.Unix(0, 0)) <= 60*time.Second {
		t.Fatalf("expected wait > 60s, got %v", now.Sub(time.Unix(0, 0)))
	}
	if len(lines) == 0 || lines[0] != "restart: waiting for loop 4242 to exit" {
		t.Fatalf("first line = %q", lines)
	}
	stillCount := 0
	for _, l := range lines[1:] {
		if l == "restart: still waiting for loop 4242 to exit" {
			stillCount++
		}
	}
	if stillCount != 3 {
		t.Fatalf("still waiting count = %d, want 3 (lines: %q)", stillCount, lines)
	}
}

func TestWaitForExitReturnsImmediatelyWhenAlreadyExited(t *testing.T) {
	var sleeps []time.Duration
	var out bytes.Buffer
	WaitForExit(4242, WaitOpts{
		Sleep:     func(d time.Duration) { sleeps = append(sleeps, d) },
		ProcessFn: func(int) (ProcInfo, bool) { return ProcInfo{}, false },
	}, &out)
	if len(sleeps) != 0 {
		t.Fatalf("expected no sleeps, got %v", sleeps)
	}
	if out.Len() != 0 {
		t.Fatalf("expected no output, got %q", out.String())
	}
}

func TestRestartDrainWaitsForSlowLoopExitThenBuildsAndRelaunches(t *testing.T) {
	work := t.TempDir()
	lockPath := filepath.Join(work, ".tillr", "keeper.lock")
	states := []struct {
		info ProcInfo
		ok   bool
	}{
		{ProcInfo{Comm: "tillr-drain", Start: "999", State: "S"}, true},
		{ProcInfo{Comm: "tillr-drain", Start: "999", State: "S"}, true},
		{ProcInfo{Comm: "tillr-drain", Start: "999", State: "S"}, true},
		{ProcInfo{Comm: "tillr-drain", Start: "999", State: "S"}, true},
		{ProcInfo{}, false},
	}
	idx := 0
	now := time.Unix(0, 0)
	var events []string
	var relaunchArgv []string
	var out bytes.Buffer

	t.Setenv("YOLO_DURABLE_DIR", work)
	err := RestartDrain(work, nil, lockPath, RestartOpts{
		BuildCmd:         []string{"fixture-build"},
		FindLoopFn:       func(string) (int, error) { return 4242, nil },
		LoopArgvFn:       func(int) ([]string, bool) { return []string{"loop", "--policy", "p.json", "--lanes", "2"}, true },
		StopKeeperFn:     func(string, io.Writer) error { return nil },
		WaitKeeperExitFn: func(string, time.Duration) error { return nil },
		WaitOpts: WaitOpts{
			ReportInterval: 30 * time.Second,
			PollInterval:   50 * time.Millisecond,
			Clock:          func() time.Time { return now },
			Sleep:          func(time.Duration) { now = now.Add(35 * time.Second) },
			ProcessFn: func(int) (ProcInfo, bool) {
				s := states[idx]
				idx++
				return s.info, s.ok
			},
		},
		RunCmdFn: func(argv []string, dir string, env []string) (int, string, string, error) {
			if len(argv) >= 2 && argv[0] == "tillr-drain" && argv[1] == "stop" {
				events = append(events, "stop")
				return 0, "", "", nil
			}
			events = append(events, "build")
			return 0, "", "", nil
		},
		GitHeadFn: func(string) (string, error) { return "deadbeef12345678", nil },
		StartKeeperFn: func(argv []string, dir string, logPath string) (int, error) {
			events = append(events, "relaunch")
			relaunchArgv = argv
			return 8888, nil
		},
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	text := out.String()
	if now.Sub(time.Unix(0, 0)) <= 60*time.Second {
		t.Fatalf("expected wait > 60s, got %v", now.Sub(time.Unix(0, 0)))
	}
	if !reflect.DeepEqual(events, []string{"build", "stop", "relaunch"}) {
		t.Fatalf("events = %v, want [build stop relaunch]", events)
	}
	joined := strings.Join(relaunchArgv, " ")
	if !strings.Contains(joined, "--policy") || !strings.Contains(joined, "--lanes") {
		t.Fatalf("relaunchArgv %q missing --policy or --lanes", relaunchArgv)
	}
	for _, want := range []string{
		"restart: stopping loop 4242\n",
		"restart: waiting for loop 4242 to exit\n",
		"restart: still waiting for loop 4242 to exit\n",
		"restart: loop 4242 exited\n",
		"restart: building from HEAD deadbeef1234\n",
		"restart: started keeper 8888 (policy=p.json, lanes=2)",
	} {
		if !strings.Contains(text, want) {
			t.Errorf("output missing %q\nfull output:\n%s", want, text)
		}
	}
}

func TestConsumerReportsProgramsUsingKeeperWorkingDirectory(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "consumer")
	binDir := filepath.Join(root, "relative-bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	drain := filepath.Join(binDir, "tillr-drain")
	if err := os.WriteFile(drain, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", "relative-bin")
	t.Setenv("YOLO_DURABLE_DIR", work)

	runCalled := false
	gitCalled := false
	var out bytes.Buffer
	err := RestartDrain(root, nil, filepath.Join(root, ".tillr", "keeper.lock"), RestartOpts{
		FindLoopFn:       func(string) (int, error) { return 0, nil },
		StopKeeperFn:     func(string, io.Writer) error { return nil },
		WaitKeeperExitFn: func(string, time.Duration) error { return nil },
		RunCmdFn: func([]string, string, []string) (int, string, string, error) {
			runCalled = true
			return 0, "", "", nil
		},
		GitHeadFn: func(string) (string, error) {
			gitCalled = true
			return "", nil
		},
		StartKeeperFn: func([]string, string, string) (int, error) { return 42, nil },
	}, &out)
	if err != nil {
		t.Fatal(err)
	}
	if runCalled || gitCalled {
		t.Fatalf("unexpected runCalled=%v gitCalled=%v", runCalled, gitCalled)
	}
	if !strings.Contains(out.String(), "tillr-drain="+drain) {
		t.Errorf("output missing tillr-drain=%s:\n%s", drain, out.String())
	}
	if !strings.Contains(out.String(), "tillr-gate=not found") {
		t.Errorf("output missing tillr-gate=not found:\n%s", out.String())
	}
}

func TestRestartInConsumerUsesPathProgramsWithoutTillrBuild(t *testing.T) {
	drainBin := buildDrainBinary(t)
	work := t.TempDir()
	root := filepath.Join(work, "consumer")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if out, err := exec.Command("git", "init", "-q", root).CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	binDir := filepath.Join(work, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatal(err)
	}
	logFile := filepath.Join(work, "drain.log")
	drainStub := filepath.Join(binDir, "tillr-drain")
	if err := os.WriteFile(drainStub, []byte(fmt.Sprintf("#!/bin/sh\necho \"$*\" > %q\n", logFile)), 0o755); err != nil {
		t.Fatal(err)
	}
	gateStub := filepath.Join(binDir, "tillr-gate")
	if err := os.WriteFile(gateStub, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	// Consumer's justfile invokes the installed keeper binary (here drainBin).
	justfile := fmt.Sprintf("[positional-arguments]\ntillr-keep *args:\n    @%s keep \"$@\"\n", drainBin)
	if err := os.WriteFile(filepath.Join(root, ".justfile"), []byte(justfile), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".tillr", "keeper.launch.json"),
		[]byte(`{"policy":"config/consumer.json","lanes":"3"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	env := append(os.Environ(),
		"YOLO_DURABLE_DIR="+work,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	defer func() {
		stopCmd := exec.Command(drainBin, "keep", "--root", root, "--stop")
		stopCmd.Dir = root
		stopCmd.Env = env
		_ = stopCmd.Run()
	}()

	cmd := exec.Command("just", "tillr-keep", "--restart")
	cmd.Dir = root
	cmd.Env = env
	outBytes, err := cmd.CombinedOutput()
	outStr := string(outBytes)
	if err != nil {
		t.Fatalf("just tillr-keep --restart failed: %v\n%s", err, outStr)
	}
	for _, want := range []string{
		"tillr-drain=" + drainStub,
		"tillr-gate=" + gateStub,
		"PATH programs",
		"policy=config/consumer.json, lanes=3",
	} {
		if !strings.Contains(outStr, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, outStr)
		}
	}
	if strings.Contains(outStr, "building from HEAD") {
		t.Errorf("consumer should not build from HEAD:\n%s", outStr)
	}

	deadline := time.Now().Add(hangGuard)
	for {
		if data, err := os.ReadFile(logFile); err == nil {
			s := string(data)
			if !strings.Contains(s, "loop --root") || !strings.Contains(s, "--policy config/consumer.json --lanes 3") {
				t.Fatalf("drain.log = %q", s)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", logFile)
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func requireFixtureCommitRepo(t testing.TB, dir string, args []string) {
	t.Helper()
	commit := false
	for _, arg := range args {
		if arg == "commit" || arg == "commit-tree" {
			commit = true
			break
		}
	}
	if !commit {
		return
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolving fixture git dir %q: %v", dir, err)
	}
	cmd := exec.Command("git", "-C", absDir, "rev-parse", "--git-dir")
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") || strings.HasPrefix(entry, "GIT_CEILING_DIRECTORIES=") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(absDir))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("refusing git %v in %s: directory is not its own repository (%v: %s)", args, dir, err, strings.TrimSpace(string(out)))
	}
}

func gitRun(t *testing.T, dir string, args ...string) string {
	t.Helper()
	requireFixtureCommitRepo(t, dir, args)
	cmdArgs := append([]string{"-C", dir, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid"}, args...)
	out, err := exec.Command("git", cmdArgs...).CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v\n%s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func setupBuildFixtureRepo(t *testing.T, root string) string {
	t.Helper()
	gitRun(t, root, "init", "-q")
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("/.tillr/\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	srcJust, err := os.ReadFile(filepath.Join(repoRoot(t), ".justfile"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".justfile"), srcJust, 0o644); err != nil {
		t.Fatal(err)
	}
	if mise, err := os.ReadFile(filepath.Join(repoRoot(t), "mise.toml")); err == nil {
		_ = os.WriteFile(filepath.Join(root, "mise.toml"), mise, 0o644)
	}
	modules := []string{"cli", "drain", "gate", "item", "land", "run", "store", "view"}
	for _, mod := range modules {
		dir := filepath.Join(root, "tools", "tillr-"+mod)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "go.mod"), []byte(fmt.Sprintf("module example.invalid/%s\n\ngo 1.23\n", mod)), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "main.go"), []byte("package main\nfunc main() {}\n"), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gitRun(t, root, "add", ".")
	gitRun(t, root, "commit", "-qm", "fixture")
	return gitRun(t, root, "rev-parse", "HEAD")
}

func TestRestartRebuildsFromHeadAndPreservesPolicyAndLanes(t *testing.T) {
	drainBin := buildDrainBinary(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	root := filepath.Join(work, "repo")
	live := filepath.Join(work, "bin")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(live, 0o755); err != nil {
		t.Fatal(err)
	}
	head := setupBuildFixtureRepo(t, root)

	// Stub tillr-drain in live: on `loop` acts as drain-stub writing stub.pid; on `stop` sends SIGTERM to stub.pid;
	// on `keep` forwards to our real drainBin.
	pidFile := filepath.Join(root, ".tillr", "stub.pid")
	drainStub := filepath.Join(live, "tillr-drain")
	stubScript := fmt.Sprintf(`#!/usr/bin/env bash
case "$1" in
  keep)
    exec %q "$@"
    ;;
  loop)
    export TILLR_KEEP_TEST_HELPER=drain-stub
    export TILLR_KEEP_TEST_PROC_NAME=tillr-drain
    export TILLR_KEEP_TEST_PID_FILE=%q
    exec -a %q %q "$@"
    ;;
  stop)
    if [ -f %q ]; then
      kill -TERM "$(cat %q)"
      exit 0
    fi
    exit 2
    ;;
esac
`, drainBin, pidFile, drainStub, self, pidFile, pidFile)
	if err := os.WriteFile(drainStub, []byte(stubScript), 0o755); err != nil {
		t.Fatal(err)
	}

	env := append(os.Environ(),
		"YOLO_DURABLE_DIR="+work,
		"PATH="+live+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	keeper := exec.Command(drainBin, "keep", "--root", root, "--", "--policy", "config/custom-policy.json", "--lanes", "4")
	keeper.Dir = root
	keeper.Env = env
	if err := keeper.Start(); err != nil {
		t.Fatal(err)
	}
	defer func() {
		stopAndWait(keeper)
		stopCmd := exec.Command(drainBin, "keep", "--root", root, "--stop")
		stopCmd.Dir = root
		stopCmd.Env = env
		_ = stopCmd.Run()
	}()

	deadline := time.Now().Add(hangGuard)
	var oldLoopPID int
	for {
		if data, err := os.ReadFile(pidFile); err == nil && len(bytes.TrimSpace(data)) > 0 {
			oldLoopPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", pidFile)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Run `just tillr-restart --root <root>`.
	restartCmd := exec.Command("just", "--justfile", filepath.Join(root, ".justfile"), "tillr-restart", "--root", root)
	restartCmd.Dir = root
	restartCmd.Env = env
	outBytes, err := restartCmd.CombinedOutput()
	outStr := string(outBytes)
	if err != nil {
		t.Fatalf("just tillr-restart failed: %v\n%s", err, outStr)
	}
	for _, want := range []string{
		"keeper: stop requested",
		fmt.Sprintf("restart: stopping loop %d", oldLoopPID),
		fmt.Sprintf("restart: loop %d exited", oldLoopPID),
		fmt.Sprintf("restart: building from HEAD %s", head[:12]),
		"policy=config/custom-policy.json",
		"lanes=4",
	} {
		if !strings.Contains(outStr, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, outStr)
		}
	}
	if err := keeper.Wait(); err != nil {
		t.Fatalf("old keeper wait: %v", err)
	}

	recBytes, err := os.ReadFile(filepath.Join(root, ".tillr", "keeper.launch.json"))
	if err != nil {
		t.Fatal(err)
	}
	var recorded map[string]string
	if err := json.Unmarshal(recBytes, &recorded); err != nil {
		t.Fatal(err)
	}
	wantRec := map[string]string{"lanes": "4", "policy": "config/custom-policy.json"}
	if !reflect.DeepEqual(recorded, wantRec) {
		t.Fatalf("recorded launch = %v, want %v", recorded, wantRec)
	}
	deadline = time.Now().Add(hangGuard)
	for {
		if _, err := os.Stat(filepath.Join(work, "keeper.log")); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for keeper.log")
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func stopAndWaitPID(pid int) {
	if pid <= 0 {
		return
	}
	pidfd, err := unix.PidfdOpen(pid, 0)
	if err != nil {
		return
	}
	defer unix.Close(pidfd)
	_ = unix.PidfdSendSignal(pidfd, unix.SIGTERM, nil, 0)
	fds := []unix.PollFd{{Fd: int32(pidfd), Events: unix.POLLIN}}
	for {
		_, err := unix.Poll(fds, -1)
		if err == unix.EINTR {
			continue
		}
		return
	}
}

func parseStartedKeeperPID(out string) int {
	const prefix = "restart: started keeper "
	idx := strings.Index(out, prefix)
	if idx < 0 {
		return 0
	}
	rest := out[idx+len(prefix):]
	end := 0
	for end < len(rest) && rest[end] >= '0' && rest[end] <= '9' {
		end++
	}
	if end == 0 {
		return 0
	}
	pid, err := strconv.Atoi(rest[:end])
	if err != nil {
		return 0
	}
	return pid
}

func TestRestartWhenNoLoopIsRunningUsesRecordedLaunchAndSucceeds(t *testing.T) {
	drainBin := buildDrainBinary(t)
	work := t.TempDir()
	root := filepath.Join(work, "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	head := setupBuildFixtureRepo(t, root)

	if err := os.MkdirAll(filepath.Join(root, ".tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".tillr", "keeper.launch.json"),
		[]byte(`{"policy":"config/recorded.json","lanes":"6"}`+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Put the build output directory first on PATH, with our prebuilt tillr-drain
	// behind it so `just tillr-restart` can bootstrap before work/bin/tillr-drain exists.
	env := append(os.Environ(),
		"YOLO_DURABLE_DIR="+work,
		"PATH="+filepath.Join(work, "bin")+string(os.PathListSeparator)+filepath.Dir(drainBin)+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	var keeperPID int
	defer func() {
		if keeperPID > 0 {
			stopAndWaitPID(keeperPID)
		} else {
			stopCmd := exec.Command(drainBin, "keep", "--root", root, "--stop")
			stopCmd.Dir = root
			stopCmd.Env = env
			_ = stopCmd.Run()
		}
		if loopPID, err := FindLoop(root); err == nil && loopPID > 0 {
			stopAndWaitPID(loopPID)
		}
	}()

	cmd := exec.Command("just", "--justfile", filepath.Join(root, ".justfile"), "tillr-restart", "--root", root)
	cmd.Dir = root
	cmd.Env = env
	outBytes, err := cmd.CombinedOutput()
	outStr := string(outBytes)
	if err != nil {
		t.Fatalf("just tillr-restart failed: %v\n%s", err, outStr)
	}
	keeperPID = parseStartedKeeperPID(outStr)
	if keeperPID <= 0 {
		t.Fatalf("output missing started keeper pid\nfull:\n%s", outStr)
	}
	for _, want := range []string{
		"keeper: not running",
		"restart: no loop running",
		fmt.Sprintf("restart: building from HEAD %s", head[:12]),
		"policy=config/recorded.json",
		"lanes=6",
	} {
		if !strings.Contains(outStr, want) {
			t.Errorf("output missing %q\nfull:\n%s", want, outStr)
		}
	}
	if _, err := os.Stat(filepath.Join(work, "bin", "tillr-drain")); err != nil {
		t.Fatalf("expected built tillr-drain in %s: %v", filepath.Join(work, "bin"), err)
	}
}

func TestRestartRefusesBeforeStoppingWhenPathDoesNotResolveBuiltBinaries(t *testing.T) {
	drainBin := buildDrainBinary(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	root := filepath.Join(work, "repo")
	live := filepath.Join(work, "bin")
	shadow := filepath.Join(work, "pack-launchers")
	for _, dir := range []string{root, live, shadow} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	setupBuildFixtureRepo(t, root)

	// Shadow directory (simulating pack launchers) comes BEFORE live on PATH.
	pidFile := filepath.Join(root, ".tillr", "stub.pid")
	shadowDrain := filepath.Join(shadow, "tillr-drain")
	stubScript := fmt.Sprintf(`#!/usr/bin/env bash
case "$1" in
  keep)
    exec %q "$@"
    ;;
  loop)
    export TILLR_KEEP_TEST_HELPER=drain-stub
    export TILLR_KEEP_TEST_PROC_NAME=tillr-drain
    export TILLR_KEEP_TEST_PID_FILE=%q
    exec -a %q %q "$@"
    ;;
  stop)
    if [ -f %q ]; then
      kill -TERM "$(cat %q)"
      exit 0
    fi
    exit 2
    ;;
esac
`, drainBin, pidFile, shadowDrain, self, pidFile, pidFile)
	if err := os.WriteFile(shadowDrain, []byte(stubScript), 0o755); err != nil {
		t.Fatal(err)
	}
	shadowTillr := filepath.Join(shadow, "tillr")
	shadowRun := filepath.Join(shadow, "tillr-run")
	for _, p := range []string{shadowTillr, shadowRun} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	env := append(os.Environ(),
		"YOLO_DURABLE_DIR="+work,
		"PATH="+shadow+string(os.PathListSeparator)+live+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	keeper := exec.Command(drainBin, "keep", "--root", root, "--", "--policy", "config/p.json", "--lanes", "2")
	keeper.Dir = root
	keeper.Env = env
	if err := keeper.Start(); err != nil {
		t.Fatal(err)
	}
	var loopPID int
	defer func() {
		stopAndWait(keeper)
		if loopPID > 0 {
			stopAndWaitPID(loopPID)
		}
	}()

	deadline := time.Now().Add(hangGuard)
	for {
		if data, err := os.ReadFile(pidFile); err == nil && len(bytes.TrimSpace(data)) > 0 {
			loopPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", pidFile)
		}
		time.Sleep(20 * time.Millisecond)
	}

	restartCmd := exec.Command("just", "--justfile", filepath.Join(root, ".justfile"), "tillr-restart", "--root", root)
	restartCmd.Dir = root
	restartCmd.Env = env
	outBytes, err := restartCmd.CombinedOutput()
	outStr := string(outBytes)
	if err == nil {
		t.Fatalf("expected just tillr-restart to refuse when PATH shadows built binaries, but it succeeded:\n%s", outStr)
	}

	// Must name each mismatched program, the path PATH resolved, and the path it built.
	for _, tc := range []struct {
		name     string
		resolved string
		built    string
	}{
		{"tillr", shadowTillr, filepath.Join(live, "tillr")},
		{"tillr-drain", shadowDrain, filepath.Join(live, "tillr-drain")},
		{"tillr-run", shadowRun, filepath.Join(live, "tillr-run")},
	} {
		if !strings.Contains(outStr, tc.name) || !strings.Contains(outStr, tc.resolved) || !strings.Contains(outStr, tc.built) {
			t.Errorf("refusal output missing mismatch details for %s (resolved=%s built=%s):\n%s", tc.name, tc.resolved, tc.built, outStr)
		}
	}

	// Must have refused BEFORE stopping anything: keeper and loop are still running.
	if info, ok := Process(keeper.Process.Pid); !ok || info.State == "Z" {
		t.Fatalf("keeper %d was stopped despite PATH mismatch refusal:\n%s", keeper.Process.Pid, outStr)
	}
	if info, ok := Process(loopPID); !ok || info.State == "Z" {
		t.Fatalf("loop %d was stopped despite PATH mismatch refusal:\n%s", loopPID, outStr)
	}
	if strings.Contains(outStr, "keeper: stop requested") || strings.Contains(outStr, "restart: stopping loop") {
		t.Errorf("refusal output shows stop actions were attempted:\n%s", outStr)
	}
}

func TestRestartStopsSecondLoopFromAnotherBinaryPathAndPackCachedBinary(t *testing.T) {
	drainBin := buildDrainBinary(t)
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	work := t.TempDir()
	root := filepath.Join(work, "repo")
	live := filepath.Join(work, "bin")
	cacheDir := filepath.Join(work, "cache")
	altDir := filepath.Join(work, "alt-bin")
	for _, dir := range []string{root, live, cacheDir, altDir} {
		if err := os.MkdirAll(dir, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	setupBuildFixtureRepo(t, root)

	// Primary loop stub in live/tillr-drain.
	pidFile := filepath.Join(root, ".tillr", "stub.pid")
	drainStub := filepath.Join(live, "tillr-drain")
	stubScript := fmt.Sprintf(`#!/usr/bin/env bash
case "$1" in
  keep)
    exec %q "$@"
    ;;
  loop)
    export TILLR_KEEP_TEST_HELPER=drain-stub
    export TILLR_KEEP_TEST_PROC_NAME=tillr-drain
    export TILLR_KEEP_TEST_PID_FILE=%q
    exec -a %q %q "$@"
    ;;
  stop)
    if [ -f %q ]; then
      kill -TERM "$(cat %q)"
      exit 0
    fi
    exit 2
    ;;
esac
`, drainBin, pidFile, drainStub, self, pidFile, pidFile)
	if err := os.WriteFile(drainStub, []byte(stubScript), 0o755); err != nil {
		t.Fatal(err)
	}

	env := append(os.Environ(),
		"YOLO_DURABLE_DIR="+work,
		"PATH="+live+string(os.PathListSeparator)+os.Getenv("PATH"),
	)
	keeper := exec.Command(drainBin, "keep", "--root", root, "--", "--policy", "config/custom.json", "--lanes", "2")
	keeper.Dir = root
	keeper.Env = env
	if err := keeper.Start(); err != nil {
		t.Fatal(err)
	}
	var firstLoopPID int
	var packLoop, altLoop *exec.Cmd
	var newKeeperPID int
	defer func() {
		stopAndWait(keeper)
		if newKeeperPID > 0 {
			stopAndWaitPID(newKeeperPID)
		}
		if firstLoopPID > 0 {
			stopAndWaitPID(firstLoopPID)
		}
		if packLoop != nil {
			stopAndWait(packLoop)
		}
		if altLoop != nil {
			stopAndWait(altLoop)
		}
	}()

	deadline := time.Now().Add(hangGuard)
	for {
		if data, err := os.ReadFile(pidFile); err == nil && len(bytes.TrimSpace(data)) > 0 {
			firstLoopPID, err = strconv.Atoi(strings.TrimSpace(string(data)))
			if err == nil {
				break
			}
		}
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s", pidFile)
		}
		time.Sleep(20 * time.Millisecond)
	}

	// Start a second loop simulating the pack launcher's fast-path cached binary
	// (`current-tillr-drain-<16-hex>`, whose Linux 15-byte comm is `current-tillr-d`).
	packBinPath := filepath.Join(cacheDir, "current-tillr-drain-0123456789abcdef")
	packLoop = exec.Command(self, "loop", "--root", root)
	packLoop.Args[0] = packBinPath
	packLoop.Dir = root
	packLoop.Env = append(os.Environ(),
		"TILLR_KEEP_TEST_HELPER=drain-stub",
		"TILLR_KEEP_TEST_PROC_NAME=current-tillr-drain-0123456789abcdef",
	)
	if err := packLoop.Start(); err != nil {
		t.Fatal(err)
	}
	waitUntilProcComm(t, packLoop.Process.Pid, "current-tillr-d")

	// Start a third loop from another binary path with a digest suffix, from a different cwd.
	altBinPath := filepath.Join(altDir, "tillr-drain-deadbeef01234567")
	altLoop = exec.Command(self, "loop", "--root", root)
	altLoop.Args[0] = altBinPath
	altLoop.Dir = work
	altLoop.Env = append(os.Environ(),
		"TILLR_KEEP_TEST_HELPER=drain-stub",
		"TILLR_KEEP_TEST_PROC_NAME=tillr-drain-deadbeef01234567",
	)
	if err := altLoop.Start(); err != nil {
		t.Fatal(err)
	}
	waitUntilProcComm(t, altLoop.Process.Pid, "tillr-drain-dea")

	restartCmd := exec.Command("just", "--justfile", filepath.Join(root, ".justfile"), "tillr-restart", "--root", root)
	restartCmd.Dir = root
	restartCmd.Env = env
	outBytes, err := restartCmd.CombinedOutput()
	outStr := string(outBytes)
	if err != nil {
		t.Fatalf("just tillr-restart failed with extra loops running: %v\n%s", err, outStr)
	}
	newKeeperPID = parseStartedKeeperPID(outStr)
	if newKeeperPID <= 0 {
		t.Fatalf("output missing started keeper pid:\n%s", outStr)
	}

	// Wait for our direct child processes to be reaped so they are not zombies.
	if err := keeper.Wait(); err != nil {
		t.Fatalf("old keeper wait: %v", err)
	}
	if err := packLoop.Wait(); err != nil {
		t.Fatalf("pack cached loop wait: %v", err)
	}
	if err := altLoop.Wait(); err != nil {
		t.Fatalf("alt binary loop wait: %v", err)
	}

	// Confirm none of the old loops are still running, and at most one loop is running now.
	for _, oldPID := range []int{firstLoopPID, packLoop.Process.Pid, altLoop.Process.Pid} {
		if info, ok := Process(oldPID); ok && info.State != "Z" {
			t.Errorf("old loop %d is still running after restart", oldPID)
		}
	}
	remainingLoops, err := FindLoops(root)
	if err != nil {
		t.Fatalf("FindLoops after restart: %v", err)
	}
	if len(remainingLoops) > 1 {
		t.Fatalf("restart left multiple loops running: %v", remainingLoops)
	}
}

func TestRestartRefusesWhenBuiltProgramMissingFromPath(t *testing.T) {
	work := t.TempDir()
	builtBin := filepath.Join(work, "bin")
	if err := os.MkdirAll(builtBin, 0o755); err != nil {
		t.Fatal(err)
	}
	builtDrain := filepath.Join(builtBin, "tillr-drain")
	builtRun := filepath.Join(builtBin, "tillr-run")
	for _, p := range []string{builtDrain, builtRun} {
		if err := os.WriteFile(p, []byte("#!/bin/sh\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	// Empty PATH so neither built program is resolved.
	t.Setenv("PATH", "")
	t.Setenv("YOLO_DURABLE_DIR", work)

	stoppedKeeper := false
	stoppedLoop := false
	startedKeeper := false
	var out bytes.Buffer
	err := RestartDrain(work, nil, filepath.Join(work, ".tillr", "keeper.lock"), RestartOpts{
		BuildCmd:         []string{"fixture-build"},
		FindLoopFn:       func(string) (int, error) { return 4242, nil },
		LoopArgvFn:       func(int) ([]string, bool) { return []string{"loop"}, true },
		StopKeeperFn:     func(string, io.Writer) error { stoppedKeeper = true; return nil },
		WaitKeeperExitFn: func(string, time.Duration) error { return nil },
		RunCmdFn: func(argv []string, dir string, env []string) (int, string, string, error) {
			if len(argv) > 0 && argv[0] == "fixture-build" {
				return 0, fmt.Sprintf("tillr-build: %s\ntillr-build: %s\n", builtDrain, builtRun), "", nil
			}
			stoppedLoop = true
			return 0, "", "", nil
		},
		GitHeadFn: func(string) (string, error) { return "deadbeef12345678", nil },
		StartKeeperFn: func([]string, string, string) (int, error) {
			startedKeeper = true
			return 9999, nil
		},
	}, &out)
	if err == nil {
		t.Fatalf("expected refusal when built programs are missing from PATH")
	}
	if stoppedKeeper || stoppedLoop || startedKeeper {
		t.Fatalf("expected refusal before stopping anything (stoppedKeeper=%v stoppedLoop=%v startedKeeper=%v)", stoppedKeeper, stoppedLoop, startedKeeper)
	}
	msg := err.Error()
	for _, want := range []string{"tillr-drain", "tillr-run", "not found", builtDrain, builtRun} {
		if !strings.Contains(msg, want) {
			t.Errorf("error %q missing %q", msg, want)
		}
	}
}

func TestRestartRefusesNamingPIDsWhenLoopOrKeeperStaysRunning(t *testing.T) {
	work := t.TempDir()
	now := time.Unix(0, 0)
	startedKeeper := false
	var out bytes.Buffer

	err := RestartDrain(work, nil, filepath.Join(work, ".tillr", "keeper.lock"), RestartOpts{
		FindLoopsFn:      func(string) ([]int, error) { return []int{5150, 5151}, nil },
		FindKeepersFn:    func(string) ([]int, error) { return []int{6160}, nil },
		LoopArgvFn:       func(int) ([]string, bool) { return []string{"loop"}, true },
		StopKeeperFn:     func(string, io.Writer) error { return nil },
		WaitKeeperExitFn: func(string, time.Duration) error { return nil },
		SignalProcFn:     func(int, string) error { return nil },
		WaitOpts: WaitOpts{
			Timeout:       2 * time.Second,
			DisableReport: true,
			PollInterval:  10 * time.Millisecond,
			Clock:         func() time.Time { return now },
			Sleep:         func(time.Duration) { now = now.Add(time.Second) },
			ProcessFn:     func(pid int) (ProcInfo, bool) { return ProcInfo{Comm: "tillr-drain", Start: "100", State: "S"}, true },
		},
		RunCmdFn: func(argv []string, dir string, env []string) (int, string, string, error) {
			return 0, "", "", nil
		},
		StartKeeperFn: func([]string, string, string) (int, error) {
			startedKeeper = true
			return 9999, nil
		},
	}, &out)
	if err == nil {
		t.Fatalf("expected refusal when loops and keeper remain running")
	}
	if startedKeeper {
		t.Fatalf("must not start a new keeper while old loops/keepers are still running")
	}
	msg := err.Error()
	for _, want := range []string{"5150", "5151", "6160"} {
		if !strings.Contains(msg, want) {
			t.Errorf("refusal error %q missing surviving PID %s", msg, want)
		}
	}
}

func waitUntilProcComm(t *testing.T, pid int, wantComm string) {
	t.Helper()
	deadline := time.Now().Add(hangGuard)
	for {
		if info, ok := Process(pid); ok && info.Comm == wantComm {
			return
		}
		if time.Now().After(deadline) {
			info, ok := Process(pid)
			t.Fatalf("timed out waiting for pid %d comm=%q (got ok=%v comm=%q)", pid, wantComm, ok, info.Comm)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
