package main_test

// The restart protocol against real processes: a built loop, the real run face and pi
// adapter, and a stub harness in place of the model (docs/reference/tillr-restart.md,
// "Replacing the loop and observatory in place").

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
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
	f      loopFixture
	exe    string
	out    string
	cmd    *exec.Cmd
	pids   string
	signal string
	lanes  int
}

func startRoll(t *testing.T, lanes int, pauseKind string, harnessBody ...string) *rollScenario {
	t.Helper()
	adapterDir, err := filepath.Abs(filepath.Join("..", "tillr-run", "backends"))
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
	s.f.policy.DefaultBackend, s.f.policy.Preference = "pi", []string{"pi"}
	delete(s.f.policy.Backends, "fixture")
	s.f.policy.Backends["pi"] = drain.Backend{Adapter: adapter, Model: "fixture-model", Effort: "low", TimeoutSeconds: 0}
	records := []string{}
	for i := 1; i <= lanes && i <= 3; i++ {
		records = append(records, fencedItemRecord("item_04"+strconv.Itoa(i), "in-queue", "candidate.txt"))
	}
	fixtureAt(t, s.f.store, records...)

	// The loop runs from a copy, so the file on disk can be replaced under it.
	s.exe = filepath.Join(dir, "tillr-drain")
	copyFile(t, bin, s.exe, nil)
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
	t.Cleanup(func() { s.cmd.Process.Kill() })
	return s
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
	copyFile(t, bin, next, []byte(tail))
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
	deadline := time.Now().Add(60 * time.Second)
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
	raw, err := exec.Command(s.exe, "restart", "--root", s.f.root, "--store", s.f.store, "--wait").CombinedOutput()
	if err != nil {
		t.Fatalf("restart --wait: %v: %s\n%s", err, raw, s.output(t))
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
	s := startRoll(t, 1, "")
	s.inFlight(t, 1)
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
}

func TestRestartRequestFiledOfflineIsAnsweredByTheNextLoop(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store)
	waiter := exec.Command(bin, "restart", "--root", f.root, "--store", f.store, "--wait")
	var out strings.Builder
	waiter.Stdout = &out
	if err := waiter.Start(); err != nil {
		t.Fatal(err)
	}
	time.Sleep(300 * time.Millisecond)
	policy := filepath.Join(scratch(t, "offline-policy"), "policy.json")
	raw, _ := json.Marshal(f.policy)
	writeFile(t, policy, string(raw))
	loop := exec.Command(bin, "loop", "--exit-when-drained", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot, "--lanes", "1", "--policy", policy, "--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"), "--brief-template", filepath.Join("..", "tillr-run", "backends", "brief-template.md"), "--gate", always(t, "gate", 0), "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0))
	if raw, err := loop.CombinedOutput(); err != nil {
		t.Fatalf("loop: %v: %s", err, raw)
	}
	if err := waiter.Wait(); err != nil {
		t.Fatalf("restart --wait: %v\n%s", err, out.String())
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
	time.Sleep(120 * drain.DefaultStorePollInterval)
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
	copyFile(t, bin, next, []byte("\n# the launcher's build\n"))
	writeFile(t, current, next+"\n")
	if answer := s.restart(t); !strings.Contains(answer, "restarted") {
		t.Fatalf("answer = %s\n%s", answer, s.output(t))
	}
	if lines := buildLines(s.output(t)); len(lines) != 2 || lines[0] == lines[1] {
		t.Fatalf("build lines = %v", lines)
	}
}
