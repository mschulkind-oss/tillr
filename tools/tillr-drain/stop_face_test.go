package main_test

import (
	"encoding/json"
	"github.com/mschulkind-oss/tillr/event"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
	"testing"
	"time"
)

func fmtInt(n int) string   { return strconv.Itoa(n) }
func fmtBool(b bool) string { return strconv.FormatBool(b) }
func stopEventsOf(t *testing.T, path, kind string) []event.Event {
	var out []event.Event
	for _, e := range eventsOf(t, path) {
		if e.Kind == kind {
			out = append(out, e)
		}
	}
	return out
}

func startStopFixture(t *testing.T, f loopFixture, lanes int, gateOverride ...string) (*exec.Cmd, *strings.Builder) {
	t.Helper()
	return startStopFixtureWithRun(t, f, lanes, sibling(t, "run"), gateOverride...)
}

func startStopFixtureWithRun(t *testing.T, f loopFixture, lanes int, runBin string, gateOverride ...string) (*exec.Cmd, *strings.Builder) {
	t.Helper()
	policy := filepath.Join(scratch(t, "stop-policy"), "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, policy, string(raw))
	gate := always(t, "gate", 0)
	if len(gateOverride) > 0 {
		gate = gateOverride[0]
	}
	land := always(t, "land", 0)
	if len(gateOverride) > 1 {
		land = gateOverride[1]
	}
	cmd := exec.Command(bin, "loop", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot, "--lanes", fmtInt(lanes), "--policy", policy, "--run-bin", runBin, "--item-bin", sibling(t, "item"), "--brief-template", filepath.Join("..", "tillr-run", "backends", "brief-template.md"), "--gate", gate, "--land", land, "--verdict", always(t, "verdict", 0))
	out := new(strings.Builder)
	cmd.Stdout = out
	cmd.Stderr = out
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { cmd.Process.Kill() })
	return cmd, out
}

func stopFixture(t *testing.T, f loopFixture, cmd *exec.Cmd, out *strings.Builder, end bool) {
	t.Helper()
	args := []string{"stop", "--root", f.root, "--store", f.store}
	if end {
		args = append(args, "--end-runs")
	}
	if raw, err := exec.Command(bin, args...).CombinedOutput(); err != nil {
		t.Fatalf("stop: %v: %s", err, raw)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop stop: %v: %s", err, out.String())
		}
	case <-time.After(hangGuard):
		t.Fatal("stop did not stop loop")
	}
}

func TestStopFaceOnlyEndsRunsOnExplicitFlag(t *testing.T) {
	for _, end := range []bool{false, true} {
		t.Run(fmtBool(end), func(t *testing.T) {
			markers := scratch(t, "stop-markers")
			t.Setenv("STOP_MARKERS", markers)
			adapter := stub(t, "stop-worker", counting+`echo started > "$STOP_MARKERS/$TILLR_RUN_ID.started"
while [ ! -f "$STOP_MARKERS/release" ]; do sleep 0.02; done
printf 'RESULT: done\n'`)
			f := newLoopFixture(t, adapter)
			fixtureAt(t, f.store, itemRecordWithFence("item_a", "in-queue", "candidate.txt"), itemRecordWithFence("item_b", "in-queue", "candidate.txt"))
			t.Cleanup(func() { writeFile(t, filepath.Join(markers, "release"), "finish") })
			cmd, out := startStopFixture(t, f, 2)
			waitForFile(t, filepath.Join(markers, "run-item_a-a1.started"), hangGuard)
			waitForFile(t, filepath.Join(markers, "run-item_b-a1.started"), hangGuard)
			stopFixture(t, f, cmd, out, end)
			if !end {
				if len(matchingPIDs(t, f.store)) != 2 {
					t.Fatal("stop without end-runs signaled a worker")
				}
				if len(terminalRunRows(t, f.store)) != 0 {
					t.Fatal("ordinary stop produced terminal records")
				}
				writeFile(t, filepath.Join(markers, "release"), "finish")
			}
			deadline := time.Now().Add(hangGuard)
			for len(terminalRunRows(t, f.store)) < 2 && time.Now().Before(deadline) {
				time.Sleep(10 * time.Millisecond)
			}
			if n := len(terminalRunRows(t, f.store)); n != 2 {
				t.Fatalf("terminal runs=%d, want 2", n)
			}
			if end {
				events := stopEventsOf(t, f.store, "drain.orphaned")
				if len(events) != 2 {
					t.Fatalf("explicit end function marked %d runs, want 2", len(events))
				}
				for _, e := range events {
					if e.ObjectID == nil {
						t.Fatal("end event has no run")
					}
				}
				if n := len(stopEventsOf(t, f.store, "gate.verdict")); n != 0 {
					t.Fatalf("ended runs judged %d times", n)
				}
				// Neither ended run spent a retry: the next loop dispatches both
				// items again with no authorization.
				cmd, out := startStopFixture(t, f, 2)
				waitForFile(t, filepath.Join(markers, "run-item_a-a2.started"), hangGuard)
				waitForFile(t, filepath.Join(markers, "run-item_b-a2.started"), hangGuard)
				writeFile(t, filepath.Join(markers, "release"), "finish")
				stopFixture(t, f, cmd, out, false)
				for _, kind := range []string{"drain.retry", "drain.retry.ack"} {
					if n := len(stopEventsOf(t, f.store, kind)); n != 0 {
						t.Fatalf("ended runs spent %d %s event(s)", n, kind)
					}
				}
			}
		})
	}
}

func TestThreeExplicitStopsDispatchAFourthTimeWithoutAuthorization(t *testing.T) {
	markers := scratch(t, "repeat-stops")
	t.Setenv("STOP_MARKERS", markers)
	adapter := stub(t, "repeated-worker", counting+`echo started > "$STOP_MARKERS/$TILLR_RUN_ID.started"
while [ ! -f "$STOP_MARKERS/release" ]; do sleep 0.02; done
`+committingAdapterBody+`printf 'RESULT: done\n'`)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, itemRecordWithFence("item_a", "in-queue", "candidate.txt"))
	t.Cleanup(func() { writeFile(t, filepath.Join(markers, "release"), "finish") })
	for attempt := 1; attempt <= 3; attempt++ {
		cmd, out := startStopFixture(t, f, 1)
		waitForFile(t, filepath.Join(markers, "run-item_a-a"+fmtInt(attempt)+".started"), hangGuard)
		stopFixture(t, f, cmd, out, true)
		deadline := time.Now().Add(hangGuard)
		for len(runsForItem(t, f.store, "item_a")) < attempt && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	cmd, out := startStopFixture(t, f, 1)
	waitForFile(t, filepath.Join(markers, "run-item_a-a4.started"), hangGuard)
	writeFile(t, filepath.Join(markers, "release"), "finish")
	deadline := time.Now().Add(hangGuard)
	for len(callsNamed(lines(t, f.count), "land")) < 1 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	stopFixture(t, f, cmd, out, false)
	if n := len(runsForItem(t, f.store, "item_a")); n != 4 {
		t.Fatalf("runs=%d, want 4", n)
	}
	if n := len(stopEventsOf(t, f.store, "gate.verdict")); n != 1 {
		t.Fatalf("gate verdicts=%d, want only the fourth run", n)
	}
	if n := len(callsNamed(lines(t, f.count), "land")); n != 1 {
		t.Fatalf("land calls=%d, want 1", n)
	}
	for _, line := range lines(t, f.count) {
		if strings.Contains(line, "--resume") {
			t.Fatal("stop consumed a wall continuation")
		}
	}
}

func TestStopFaceDuringInFlightGateEndsPromptlyAndNextLoopLandsCandidate(t *testing.T) {
	markers := scratch(t, "stop-gate-face")
	ready := filepath.Join(markers, "gate.ready")
	pidsFile := filepath.Join(markers, "gate.pids")
	allowPass := filepath.Join(markers, "gate.pass")

	gateProg := stub(t, "gate-session-tree", `[ "$1" = --help ] && exit 0
`+counting+`python3 - <<'PY'
import os, pathlib, subprocess, sys

allow = pathlib.Path("`+allowPass+`")
if allow.exists():
    sys.exit(0)

group = subprocess.Popen(
    ['python3', '-c', '''
import os, pathlib, subprocess
leaf = subprocess.Popen(["sleep", "300"], start_new_session=True)
pathlib.Path("`+pidsFile+`.part").write_text(f"{os.getpid()} {leaf.pid}\\n")
os.replace("`+pidsFile+`.part", "`+pidsFile+`")
pathlib.Path("`+ready+`").touch()
leaf.wait()
'''],
    start_new_session=True,
)
sys.exit(group.wait())
PY`)

	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecordWithFence("item_a", "in-queue", "candidate.txt"))

	cmd, out := startStopFixture(t, f, 1, gateProg)
	waitForFile(t, ready, hangGuard)
	rawPids, err := os.ReadFile(pidsFile)
	if err != nil {
		t.Fatal(err)
	}
	var pids []int
	for _, field := range strings.Fields(string(rawPids)) {
		pid, err := strconv.Atoi(field)
		if err != nil {
			t.Fatal(err)
		}
		pids = append(pids, pid)
		t.Cleanup(func() { _ = syscall.Kill(pid, syscall.SIGKILL) })
	}

	stopFixture(t, f, cmd, out, false)

	for _, pid := range pids {
		if err := syscall.Kill(pid, 0); err == nil {
			t.Fatalf("separate-session gate descendant %d outlived tillr drain stop", pid)
		}
	}
	if n := len(stopEventsOf(t, f.store, "gate.verdict")); n != 0 {
		t.Fatalf("stopped gate recorded %d gate.verdict event(s), want 0", n)
	}

	landDone := filepath.Join(markers, "land.done")
	landProg := stub(t, "land-marker", `[ "$1" = --help ] && exit 0
`+counting+`touch "`+landDone+`"
exit 0`)

	writeFile(t, allowPass, "ok")
	nextCmd, nextOut := startStopFixture(t, f, 1, gateProg, landProg)
	waitForFile(t, landDone, hangGuard)
	stopFixture(t, f, nextCmd, nextOut, false)
	if n := len(callsNamed(lines(t, f.count), "land")); n != 1 {
		t.Fatalf("land calls=%d, want 1", n)
	}
	if n := len(runsForItem(t, f.store, "item_a")); n != 1 {
		t.Fatalf("runs=%d, want 1 (resumed existing candidate without re-running worker)", n)
	}
}
