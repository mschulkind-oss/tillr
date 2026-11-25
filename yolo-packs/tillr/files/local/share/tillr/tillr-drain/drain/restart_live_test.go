package drain_test

// The restart protocol against real processes: a built loop, the real run face and pi
// adapter, and a stub harness in place of the model (docs/reference/tillr-restart.md,
// "Replacing the loop and observatory in place").

import (
	"bufio"
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
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

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/presence"
	"github.com/mschulkind-oss/tillr-run/run"
)

// rollHarness keeps a fixture worker in flight and records any TERM it receives, so
// the tests can detect a replacement that incorrectly signals the worker.
const rollHarness = harnessCount + `item=$(basename "$(dirname "$PWD")")
seg_file="$STUB_SEGMENT.$item"
n=0; [ ! -f "$seg_file" ] || n=$(cat "$seg_file")
n=$((n+1)); echo "$n" > "$seg_file"
if [ "$n" -lt 2 ] && [ "$item" != lane-4 ]; then
    echo "$$ $item" >> "$STUB_PIDS"
    trap 'date +%s%3N >> "$STUB_SIGNALS"; if [ "$item" = lane-3 ]; then sleep 1.5; fi; exit 143' TERM
    sleep 600 &
    wait $!
else
    cat
    while [ -f "$STUB_HOLD" ]; do sleep 0.05; done
    echo "$item" > candidate.txt
    git add candidate.txt
    git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
    printf 'RESULT: done\n'
fi
`

type rollScenario struct {
	f            loopFixture
	exe          string
	out          string
	cmd          *exec.Cmd
	loopOwner    presence.Owner
	pids         string
	signal       string
	lanes        int
	wantRestarts int
}

func startRoll(t *testing.T, lanes int, pauseKind string, harnessBody ...string) *rollScenario {
	t.Helper()
	return startConfiguredRoll(t, lanes, pauseKind, nil, harnessBody...)
}

func startConfiguredRoll(t *testing.T, lanes int, pauseKind string, configure func(loopFixture), harnessBody ...string) *rollScenario {
	t.Helper()
	adapterDir, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends"))
	if err != nil {
		t.Fatal(err)
	}
	if pauseKind != "" {
		// A copy of the adapter tree whose declaration is the one under test.
		copyTo := filepath.Join(scratch(t, "roll-adapter"), "backends")
		if raw, err := exec.Command("cp", "-r", adapterDir, copyTo).CombinedOutput(); err != nil {
			t.Fatalf("copying the adapter: %v: %s", err, raw)
		}
		adapterDir = copyTo
		writeFile(t, filepath.Join(adapterDir, "pi", "pause-kind"), pauseKind+"\n")
	}
	body := `[ "${1:-}" != --features ] || exit 2
` + rollHarness
	if len(harnessBody) > 0 {
		body = harnessBody[0]
	}
	harness := stub(t, "pi", body)
	t.Setenv("PATH", filepath.Dir(harness)+string(os.PathListSeparator)+os.Getenv("PATH"))
	dir := scratch(t, "roll")
	s := &rollScenario{pids: filepath.Join(dir, "pids"), signal: filepath.Join(dir, "signals"), lanes: lanes}
	t.Setenv("STUB_PIDS", s.pids)
	t.Setenv("STUB_SIGNALS", s.signal)
	t.Setenv("STUB_SEGMENT", filepath.Join(dir, "segment"))
	t.Setenv("STUB_ARGV", filepath.Join(dir, "argv"))
	adapter := filepath.Join(adapterDir, "pi", "run-pi")
	s.f = newLoopFixture(t, adapter)
	s.f.policy.DefaultBackend = "pi"
	delete(s.f.policy.Backends, "fixture")
	s.f.policy.Backends["pi"] = drain.Backend{Adapter: adapter, Model: "fixture-model", Effort: "low", TimeoutSeconds: 0}
	records := []string{}
	for i := 1; i <= lanes && i <= 3; i++ {
		records = append(records, fencedItemRecord("item_04"+strconv.Itoa(i), "in-queue", "candidate.txt"))
	}
	fixtureAt(t, s.f.store, records...)
	if configure != nil {
		configure(s.f)
	}

	// The loop runs from a copy, so the file on disk can be replaced under it.
	s.exe = filepath.Join(dir, "tillr-drain")
	copyFile(t, drainBin(t), s.exe, nil)
	policy := filepath.Join(dir, "policy.json")
	raw, err := json.Marshal(s.f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	s.out = filepath.Join(dir, "loop.out")
	outFile, err := os.Create(s.out)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { outFile.Close() })
	backends := filepath.Join(adapterDir, "brief-template.md")
	s.cmd = exec.Command(s.exe, "loop", "--root", s.f.root, "--store", s.f.store, "--lane-root", s.f.laneRoot, "--lanes", strconv.Itoa(lanes), "--policy", policy, "--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"), "--brief-template", backends, "--gate", always(t, "gate", 0), "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0))
	s.cmd.Stdout, s.cmd.Stderr = outFile, outFile
	if err := s.cmd.Start(); err != nil {
		t.Fatal(err)
	}
	s.recordLoopOwner()
	t.Cleanup(func() { s.stop(t) })
	return s
}

func (s *rollScenario) recordLoopOwner() {
	if s.cmd == nil || s.cmd.Process == nil {
		return
	}
	if here, err := presence.CurrentOwner(); err == nil {
		if start, err := presence.ProcessStart(s.cmd.Process.Pid); err == nil && start != "" {
			s.loopOwner = presence.Owner{PID: s.cmd.Process.Pid, Start: start, Domain: here.Domain, UID: here.UID}
		}
	}
}

func storeRunOwners(storePath string, here *presence.Owner) ([]presence.Owner, error) {
	resolved, err := presence.StoreIdentity(storePath)
	if err != nil {
		return nil, err
	}
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return nil, err
	}
	wantStore := "TILLR_LOOP_STORE=" + resolved
	var owners []presence.Owner
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		env, err := presence.RunProcessEnvironment(filepath.Join("/proc", entry.Name()), here.UID)
		if presence.ProcessGone(err) {
			continue
		}
		if err != nil {
			return nil, err
		}
		hasRun, hasStore := false, false
		for _, kv := range bytes.Split(env, []byte{0}) {
			s := string(kv)
			hasRun = hasRun || (strings.HasPrefix(s, "TILLR_RUN_ID=") && len(s) > len("TILLR_RUN_ID="))
			hasStore = hasStore || s == wantStore
		}
		if !hasRun || !hasStore {
			continue
		}
		start, err := presence.ProcessStart(pid)
		if presence.ProcessGone(err) || (err == nil && start == "") {
			continue
		}
		if err != nil {
			return nil, err
		}
		owners = append(owners, presence.Owner{PID: pid, Start: start, Domain: here.Domain, UID: here.UID})
	}
	return owners, nil
}

func forceEndStoreRuns(storePath string, here *presence.Owner, seen []presence.Owner) {
	if live, err := storeRunOwners(storePath, here); err == nil {
		seen = append(seen, live...)
	}
	signalLive := func(sig syscall.Signal) {
		for _, o := range seen {
			if dead, err := presence.OwnerDead(&o, here); err == nil && !dead {
				_ = syscall.Kill(o.PID, sig)
			}
		}
	}
	allDead := func() bool {
		if live, err := storeRunOwners(storePath, here); err == nil && len(live) > 0 {
			seen = append(seen, live...)
			return false
		}
		for _, o := range seen {
			if dead, err := presence.OwnerDead(&o, here); err == nil && !dead {
				return false
			}
		}
		return true
	}
	signalLive(syscall.SIGTERM)
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		if allDead() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	signalLive(syscall.SIGKILL)
	killDeadline := time.Now().Add(hangGuard)
	for time.Now().Before(killDeadline) {
		if allDead() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func waitForStoreRunsExit(t *testing.T, ctx context.Context, storePath string, here *presence.Owner, seen []presence.Owner) {
	t.Helper()
	for {
		live, err := storeRunOwners(storePath, here)
		if err != nil {
			forceEndStoreRuns(storePath, here, seen)
			t.Errorf("checking live run processes: %v", err)
			return
		}
		seen = append(seen, live...)
		stillAlive := len(live) > 0
		for i := 0; !stillAlive && i < len(seen); i++ {
			dead, err := presence.OwnerDead(&seen[i], here)
			if err != nil {
				forceEndStoreRuns(storePath, here, seen)
				t.Errorf("checking run process %d: %v", seen[i].PID, err)
				return
			}
			if !dead {
				stillAlive = true
			}
		}
		if !stillAlive {
			return
		}
		if err := ctx.Err(); err != nil {
			forceEndStoreRuns(storePath, here, seen)
			t.Errorf("timed out waiting for run processes to exit: %v", err)
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func cleanupStoreRuns(t *testing.T, storePath string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	here, err := presence.CurrentOwner()
	if err != nil {
		t.Errorf("reading current owner to clean store runs: %v", err)
		return
	}
	seen, err := storeRunOwners(storePath, here)
	if err != nil {
		t.Errorf("listing live run processes for cleanup: %v", err)
		return
	}
	if len(seen) > 0 {
		if _, err := drain.EndRuns(ctx, storePath); err != nil {
			forceEndStoreRuns(storePath, here, seen)
		} else {
			waitForStoreRunsExit(t, ctx, storePath, here, seen)
		}
	}
	if err := stopStoreNotifyRelays(ctx, storePath, here); err != nil {
		t.Errorf("stopping notify relays: %v", err)
	}
}

func waitAndCleanupStoreRuns(t *testing.T, storePath string) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	here, err := presence.CurrentOwner()
	if err != nil {
		t.Errorf("reading current owner to wait for store runs: %v", err)
		return
	}
	seen, err := storeRunOwners(storePath, here)
	if err != nil {
		t.Errorf("listing live run processes for cleanup: %v", err)
		return
	}
	if len(seen) > 0 {
		waitForStoreRunsExit(t, ctx, storePath, here, seen)
	}
	if err := stopStoreNotifyRelays(ctx, storePath, here); err != nil {
		t.Errorf("stopping notify relays: %v", err)
	}
}

func (s *rollScenario) stop(t *testing.T) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
	defer cancel()
	here, err := presence.CurrentOwner()
	if err != nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
		t.Errorf("reading current owner to stop roll fixture: %v", err)
		return
	}
	seen, _ := storeRunOwners(s.f.store, here)
	if _, err := drain.Stop(ctx, drain.StopConfig{Store: s.f.store, EndRuns: true}); err != nil {
		_ = s.cmd.Process.Kill()
		_ = s.cmd.Wait()
		forceEndStoreRuns(s.f.store, here, seen)
		_ = stopStoreNotifyRelays(ctx, s.f.store, here)
		t.Errorf("stopping roll fixture: %v", err)
		return
	}
	if s.loopOwner.Start != "" {
		if dead, err := presence.OwnerDead(&s.loopOwner, here); err == nil && !dead {
			// A caller failed before the loop opened its session row in the store.
			_ = s.cmd.Process.Signal(syscall.SIGTERM)
			_ = s.cmd.Wait()
			if _, err := drain.Stop(ctx, drain.StopConfig{Store: s.f.store, EndRuns: true}); err != nil {
				forceEndStoreRuns(s.f.store, here, seen)
				_ = stopStoreNotifyRelays(ctx, s.f.store, here)
				t.Errorf("ending roll runs after early loop stop: %v", err)
				return
			}
		} else {
			_ = s.cmd.Wait()
		}
	} else {
		_ = s.cmd.Wait()
	}
	waitForStoreRunsExit(t, ctx, s.f.store, here, seen)
	if err := stopStoreNotifyRelays(ctx, s.f.store, here); err != nil {
		t.Errorf("stopping roll notify relays: %v", err)
	}
}

func stopStoreNotifyRelays(ctx context.Context, storePath string, here *presence.Owner) error {
	entries, err := os.ReadDir("/proc")
	if err != nil {
		return err
	}
	var relays []presence.Owner
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("/proc", entry.Name(), "cmdline"))
		if err != nil {
			continue
		}
		args := strings.Split(strings.TrimRight(string(raw), "\x00"), "\x00")
		hasNotify, hasServe, hasStore := false, false, false
		for i, arg := range args {
			if arg == "notify" {
				hasNotify = true
			}
			if arg == "serve" {
				hasServe = true
			}
			if arg == "--store" && i+1 < len(args) && args[i+1] == storePath {
				hasStore = true
			}
		}
		if !hasNotify || !hasServe || !hasStore {
			continue
		}
		start, err := presence.ProcessStart(pid)
		if presence.ProcessGone(err) || (err == nil && start == "") {
			continue
		}
		if err != nil {
			return err
		}
		relays = append(relays, presence.Owner{PID: pid, Start: start, Domain: here.Domain, UID: here.UID})
		_ = syscall.Kill(pid, syscall.SIGTERM)
	}
	for _, o := range relays {
		for {
			dead, err := presence.OwnerDead(&o, here)
			if err != nil {
				return err
			}
			if dead {
				break
			}
			if err := ctx.Err(); err != nil {
				return fmt.Errorf("timed out waiting for notify relay %d to exit: %w", o.PID, err)
			}
			time.Sleep(10 * time.Millisecond)
		}
	}
	return nil
}

func copyFile(t *testing.T, from, to string, extra []byte) {
	t.Helper()
	raw, err := os.ReadFile(from)
	if err != nil {
		t.Fatal(err)
	}
	if err := writeForkSafe(to, append(raw, extra...), 0o755); err != nil {
		t.Fatal(err)
	}
}

// replaceBinary renames a changed copy over the live path: the running loop's image is now
// an unlinked file, and the path holds a different build.
func (s *rollScenario) replaceBinary(t *testing.T) { s.replaceBinaryWith(t, "\n# a different build\n") }

func (s *rollScenario) replaceBinaryWith(t *testing.T, tail string) {
	t.Helper()
	next := s.exe + ".next"
	copyFile(t, drainBin(t), next, []byte(tail))
	if err := os.Rename(next, s.exe); err != nil {
		t.Fatal(err)
	}
}

func (s *rollScenario) output(t *testing.T) string {
	raw, err := os.ReadFile(s.out)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func (s *rollScenario) waitFor(t *testing.T, what string, ok func() bool) {
	t.Helper()
	deadline := time.Now().Add(hangGuard)
	for !ok() {
		if time.Now().After(deadline) {
			t.Fatalf("timed out waiting for %s\n%s", what, s.output(t))
		}
		time.Sleep(20 * time.Millisecond)
	}
}

func (s *rollScenario) inFlight(t *testing.T, n int) {
	s.waitFor(t, "every harness to be in its first segment", func() bool { return len(lines(t, s.pids)) >= n })
}

func (s *rollScenario) restart(t *testing.T) string {
	t.Helper()
	before := strings.Count(s.output(t), restartedInPlaceMarker)
	raw, err := exec.Command(s.exe, "restart", "--root", s.f.root, "--store", s.f.store, "--wait").CombinedOutput()
	if err != nil {
		t.Fatalf("restart --wait: %v: %s\n%s", err, raw, s.output(t))
	}
	if strings.Contains(string(raw), "restarted") {
		s.wantRestarts = before + 1
	}
	return string(raw)
}

func buildLines(out string) []string {
	var got []string
	for _, line := range strings.Split(out, "\n") {
		if strings.HasPrefix(line, "loop: build ") {
			got = append(got, line)
		}
	}
	return got
}

func alive(pid int) bool { return syscall.Kill(pid, 0) == nil }

// The replacement keeps its PID, prints a new build line, and leaves the run alone.
func TestRestartReplacesTheLoopInPlaceWithoutSignalingARun(t *testing.T) {
	// waitForSuccessorPass counts SQLite trace lines. Enable tracing before
	// launch so the loop carries it through the exec regardless of the runner.
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	s := startRoll(t, 1, "")
	s.inFlight(t, 1)
	const id = "run-item_041-a1"
	s.assertLiveWorkerWhy(t, id, false, false)
	before := buildLines(s.output(t))
	if len(before) != 1 {
		t.Fatalf("want one build line before the restart: %v", before)
	}
	pid := s.cmd.Process.Pid
	s.replaceBinary(t)
	answer := s.restart(t)
	if !strings.Contains(answer, "restarted") {
		t.Fatalf("answer = %s", answer)
	}
	if !alive(pid) {
		t.Fatal("the loop's process id did not survive the restart")
	}
	after := buildLines(s.output(t))
	if len(after) != 2 || after[0] == after[1] {
		t.Fatalf("build lines = %v: want two, and the second new", after)
	}
	workerPID, _ := strconv.Atoi(strings.Fields(lines(t, s.pids)[0])[0])
	if !alive(workerPID) || len(lines(t, s.signal)) != 0 {
		t.Fatal("replacement signaled or ended the in-flight worker")
	}
	s.waitForSuccessorPass(t)
	s.assertLiveWorkerWhy(t, id, false, true)
}

func TestRestartRequestFiledOfflineIsAnsweredByTheNextLoop(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store)
	waiter := exec.Command(drainBin(t), "restart", "--root", f.root, "--store", f.store, "--wait")
	stdout, err := waiter.StdoutPipe()
	if err != nil {
		t.Fatal(err)
	}
	var stderr bytes.Buffer
	waiter.Stderr = &stderr
	if err := waiter.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if waiter.Process != nil && waiter.ProcessState == nil {
			_ = waiter.Process.Kill()
			_ = waiter.Wait()
		}
	})
	scanner := bufio.NewScanner(stdout)
	if !scanner.Scan() {
		waitErr := waiter.Wait()
		t.Fatalf("restart --wait exited before recording a request: %v (scan: %v)\n%s", waitErr, scanner.Err(), stderr.String())
	}
	var out strings.Builder
	out.WriteString(scanner.Text())
	out.WriteByte('\n')
	if !strings.Contains(out.String(), "restart-drain-") {
		t.Fatalf("restart --wait first line = %q, want a restart-drain id", out.String())
	}
	// Drain the remaining stdout (the answer line) while the loop runs.
	restDone := make(chan error, 1)
	go func() {
		for scanner.Scan() {
			out.WriteString(scanner.Text())
			out.WriteByte('\n')
		}
		restDone <- scanner.Err()
	}()
	policy := filepath.Join(scratch(t, "offline-policy"), "policy.json")
	raw, _ := json.Marshal(f.policy)
	writeFile(t, policy, string(raw))
	loop := exec.Command(drainBin(t), "loop", "--exit-when-drained", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot, "--lanes", "1", "--policy", policy, "--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"), "--brief-template", filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), "--gate", always(t, "gate", 0), "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0))
	if raw, err := loop.CombinedOutput(); err != nil {
		t.Fatalf("loop: %v: %s", err, raw)
	}
	if err := <-restDone; err != nil {
		t.Fatalf("reading restart --wait output: %v", err)
	}
	if err := waiter.Wait(); err != nil {
		t.Fatalf("restart --wait: %v\n%s\n%s", err, out.String(), stderr.String())
	}
	if !strings.Contains(out.String(), "restart-drain-") || !strings.Contains(out.String(), "already-current") {
		t.Fatalf("restart --wait printed %q", out.String())
	}
}

// The binary on disk is replaced and no request is made: a hundred polls later the real
// running process still prints one build line and is still the original image.
func TestNoRequestMeansTheRunningBinaryNeverRestarts(t *testing.T) {
	s := startRoll(t, 1, "")
	s.inFlight(t, 1)
	before := buildLines(s.output(t))
	image, _ := os.Readlink("/proc/" + strconv.Itoa(s.cmd.Process.Pid) + "/exe")
	s.replaceBinary(t)
	time.Sleep(5 * time.Second)
	if after := buildLines(s.output(t)); len(after) != 1 || after[0] != before[0] {
		t.Fatalf("build lines changed with no request: %v -> %v", before, after)
	}
	// The original image, now unlinked by the rename: the process was never replaced.
	if now, _ := os.Readlink("/proc/" + strconv.Itoa(s.cmd.Process.Pid) + "/exe"); now != image+" (deleted)" {
		t.Fatalf("the running image changed from %s to %s", image, now)
	}
	if len(lines(t, s.signal)) != 0 {
		t.Fatal("a run was signalled with no request")
	}
}

// A launcher can name a new build while a worker remains in flight; replacement does
// not wait for that worker.
func TestARestartThroughALauncherReachesTheLaunchersBuild(t *testing.T) {
	dir := scratch(t, "roll-launcher")
	current := filepath.Join(dir, "current")
	launcher := filepath.Join(dir, "launcher")
	writeFile(t, launcher, "#!/bin/sh\n[ -n \"$TILLR_LAUNCHER_RESOLVE\" ] && cat "+current+" && exit 0\nexit 9\n")
	os.Chmod(launcher, 0o755)
	t.Setenv("TILLR_LAUNCHER", launcher)
	s := startRoll(t, 1, "")
	s.inFlight(t, 1)
	next := filepath.Join(dir, "tillr-drain-next")
	copyFile(t, drainBin(t), next, []byte("\n# the launcher's build\n"))
	writeFile(t, current, next+"\n")
	if answer := s.restart(t); !strings.Contains(answer, "restarted") {
		t.Fatalf("answer = %s\n%s", answer, s.output(t))
	}
	if lines := buildLines(s.output(t)); len(lines) != 2 || lines[0] == lines[1] {
		t.Fatalf("build lines = %v", lines)
	}
}

func randomRollTestMarker(t *testing.T) string {
	t.Helper()
	var buf [16]byte
	if _, err := rand.Read(buf[:]); err != nil {
		t.Fatalf("generating roll test marker: %v", err)
	}
	return hex.EncodeToString(buf[:])
}

func procCmdline(pid int) string {
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/cmdline", pid))
	if err != nil {
		return "<gone>"
	}
	return strings.TrimSpace(strings.ReplaceAll(string(raw), "\x00", " "))
}

func procParentPID(t *testing.T, pid int) int {
	t.Helper()
	raw, err := os.ReadFile(fmt.Sprintf("/proc/%d/stat", pid))
	if err != nil {
		t.Fatalf("reading /proc/%d/stat: %v", pid, err)
	}
	end := bytes.LastIndexByte(raw, ')')
	if end < 0 {
		t.Fatalf("/proc/%d/stat malformed: %q", pid, raw)
	}
	fields := strings.Fields(string(raw[end+1:]))
	if len(fields) < 2 {
		t.Fatalf("/proc/%d/stat missing ppid: %q", pid, raw)
	}
	ppid, err := strconv.Atoi(fields[1])
	if err != nil {
		t.Fatalf("parsing ppid from /proc/%d/stat: %v", pid, err)
	}
	return ppid
}

func ownerAt(t *testing.T, here *presence.Owner, pid int) presence.Owner {
	t.Helper()
	start, err := presence.ProcessStart(pid)
	if err != nil || start == "" {
		t.Fatalf("process %d is not live (start=%q, err=%v)", pid, start, err)
	}
	return presence.Owner{PID: pid, Start: start, Domain: here.Domain, UID: here.UID}
}

func markedRollProcesses(t *testing.T, here *presence.Owner, marker string) ([]presence.Owner, []string) {
	t.Helper()
	entries, err := os.ReadDir("/proc")
	if err != nil {
		t.Fatalf("reading /proc: %v", err)
	}
	want := "TILLR_ROLL_TEST_MARKER=" + marker
	var owners []presence.Owner
	var desc []string
	for _, entry := range entries {
		pid, err := strconv.Atoi(entry.Name())
		if err != nil || pid == os.Getpid() {
			continue
		}
		env, err := presence.RunProcessEnvironment(filepath.Join("/proc", entry.Name()), here.UID)
		if presence.ProcessGone(err) {
			continue
		}
		if err != nil {
			t.Fatalf("reading /proc/%d/environ: %v", pid, err)
		}
		matched := false
		for _, kv := range bytes.Split(env, []byte{0}) {
			if string(kv) == want {
				matched = true
				break
			}
		}
		if !matched {
			continue
		}
		start, err := presence.ProcessStart(pid)
		if presence.ProcessGone(err) || (err == nil && start == "") {
			continue
		}
		if err != nil {
			t.Fatalf("reading /proc/%d/stat: %v", pid, err)
		}
		owners = append(owners, presence.Owner{PID: pid, Start: start, Domain: here.Domain, UID: here.UID})
		desc = append(desc, fmt.Sprintf("%d (%s)", pid, procCmdline(pid)))
	}
	return owners, desc
}

func cleanupLeakedMarkedProcesses(here *presence.Owner, owners []presence.Owner) {
	for _, o := range owners {
		if dead, err := presence.OwnerDead(&o, here); err == nil && !dead {
			_ = syscall.Kill(o.PID, syscall.SIGKILL)
		}
	}
}

func assertNoMarkedProcessAliveAfter(t *testing.T, name string, fn func(t *testing.T)) {
	t.Helper()
	here, err := presence.CurrentOwner()
	if err != nil {
		t.Fatalf("current owner: %v", err)
	}
	marker := randomRollTestMarker(t)
	ok := t.Run(name, func(t *testing.T) {
		t.Setenv("TILLR_ROLL_TEST_MARKER", marker)
		fn(t)
	})
	owners, desc := markedRollProcesses(t, here, marker)
	if len(owners) > 0 {
		cleanupLeakedMarkedProcesses(here, owners)
		t.Fatalf("%s returned with %d started process(es) still alive:\n%s", name, len(desc), strings.Join(desc, "\n"))
	}
	if !ok {
		t.Fatalf("%s failed", name)
	}
}

func TestRollTestsEndEveryProcessTheyStartBeforeReturning(t *testing.T) {
	here, err := presence.CurrentOwner()
	if err != nil {
		t.Fatalf("current owner: %v", err)
	}

	var recorded []presence.Owner
	assertNoMarkedProcessAliveAfter(t, "roll_process_tree", func(t *testing.T) {
		s := startRoll(t, 1, "")
		s.inFlight(t, 1)
		workerPID, err := strconv.Atoi(strings.Fields(lines(t, s.pids)[0])[0])
		if err != nil {
			t.Fatalf("parsing worker pid: %v", err)
		}
		stopAtGapPID := procParentPID(t, workerPID)
		runFacePID := procParentPID(t, stopAtGapPID)
		if cmd := procCmdline(stopAtGapPID); !strings.Contains(cmd, "stop-at-gap") {
			t.Fatalf("worker parent %d cmdline = %q, want stop-at-gap", stopAtGapPID, cmd)
		}
		if cmd := procCmdline(workerPID); !strings.Contains(cmd, "/pi ") {
			t.Fatalf("worker %d cmdline = %q, want stub pi", workerPID, cmd)
		}
		recorded = []presence.Owner{
			ownerAt(t, here, s.cmd.Process.Pid),
			ownerAt(t, here, runFacePID),
			ownerAt(t, here, stopAtGapPID),
			ownerAt(t, here, workerPID),
		}
	})
	for _, o := range recorded {
		dead, err := presence.OwnerDead(&o, here)
		if err != nil {
			t.Fatalf("checking recorded process %d: %v", o.PID, err)
		}
		if !dead {
			_ = syscall.Kill(o.PID, syscall.SIGKILL)
			t.Fatalf("recorded process %d (%s) still alive after test returned", o.PID, procCmdline(o.PID))
		}
	}

	assertNoMarkedProcessAliveAfter(t, "roll_adapter_stop_at_gap", func(t *testing.T) {
		s := startRoll(t, 1, run.PauseWhereItStands)
		s.inFlight(t, 1)
		workerPID, err := strconv.Atoi(strings.Fields(lines(t, s.pids)[0])[0])
		if err != nil {
			t.Fatalf("parsing worker pid: %v", err)
		}
		stopAtGapPID := procParentPID(t, workerPID)
		if cmd := procCmdline(stopAtGapPID); !strings.Contains(cmd, "roll-adapter-") || !strings.Contains(cmd, "pi-stop-at-gap") {
			t.Fatalf("worker parent %d cmdline = %q, want roll-adapter pi-stop-at-gap", stopAtGapPID, cmd)
		}
		if cmd := procCmdline(workerPID); !strings.Contains(cmd, "/pi ") {
			t.Fatalf("worker %d cmdline = %q, want stub pi", workerPID, cmd)
		}
	})

	assertNoMarkedProcessAliveAfter(t, "restart_in_place", TestRestartReplacesTheLoopInPlaceWithoutSignalingARun)
	assertNoMarkedProcessAliveAfter(t, "no_request", TestNoRequestMeansTheRunningBinaryNeverRestarts)
	assertNoMarkedProcessAliveAfter(t, "launcher_restart", TestARestartThroughALauncherReachesTheLaunchersBuild)
}
