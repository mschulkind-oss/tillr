package main_test

import (
	"encoding/json"
	"github.com/mschulkind-oss/tillr/event"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
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
	cmd := exec.Command(bin, "loop", "--root", f.root, "--store", f.store, "--lane-root", f.laneRoot, "--lanes", fmtInt(lanes), "--policy", policy, "--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"), "--brief-template", filepath.Join("..", "tillr-run", "backends", "brief-template.md"), "--gate", gate, "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0))
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
	case <-time.After(10 * time.Second):
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
			waitForFile(t, filepath.Join(markers, "run-item_a-a1.started"), 10*time.Second)
			waitForFile(t, filepath.Join(markers, "run-item_b-a1.started"), 10*time.Second)
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
			deadline := time.Now().Add(10 * time.Second)
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
				waitForFile(t, filepath.Join(markers, "run-item_a-a2.started"), 10*time.Second)
				waitForFile(t, filepath.Join(markers, "run-item_b-a2.started"), 10*time.Second)
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
		waitForFile(t, filepath.Join(markers, "run-item_a-a"+fmtInt(attempt)+".started"), 10*time.Second)
		stopFixture(t, f, cmd, out, true)
		deadline := time.Now().Add(10 * time.Second)
		for len(runsForItem(t, f.store, "item_a")) < attempt && time.Now().Before(deadline) {
			time.Sleep(10 * time.Millisecond)
		}
	}
	cmd, out := startStopFixture(t, f, 1)
	waitForFile(t, filepath.Join(markers, "run-item_a-a4.started"), 10*time.Second)
	writeFile(t, filepath.Join(markers, "release"), "finish")
	deadline := time.Now().Add(15 * time.Second)
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
