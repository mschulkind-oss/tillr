package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestStopDuringGateReleasesExaminationLease(t *testing.T) {
	marker := filepath.Join(scratch(t, "gate-stop"), "started")
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecordWithFence("item_a", "in-queue", "candidate.txt"))
	gate := stub(t, "blocked-gate", `[ "$1" = --help ] && exit 0
echo started > "`+marker+`"
exec sleep 300`)
	cmd, out := startStopFixture(t, f, 1, gate)
	waitForFile(t, marker, hangGuard)
	stopFixture(t, f, cmd, out, false)
	for _, lane := range lanesIn(t, f.store) {
		if lane.RunID != nil || lane.State != "idle" {
			t.Fatalf("canceled gate retained its lease: %+v", lane)
		}
	}
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	result, err := f.newLoop(t, 1, steps).Run(context.Background())
	if err != nil || result.Landed != 1 || result.Dispatched != 0 {
		t.Fatalf("replacement gate: %+v %v", result, err)
	}
}

const shortWallRequest = `import json, sys
path = sys.argv[1]
with open(path) as f:
    request = json.load(f)
payload = request['payload']
if not payload.get('resume'):
    assert payload['timeout_ms'] == 1000, payload
    payload['timeout_ms'] = 100
else:
    payload['timeout_ms'] = 0
with open(path, 'w') as f:
    json.dump(request, f)
`

// Sharding must not turn a fixture's initial deadline into a deadline on its resume.
func TestWallContinuationFixtureClearsTheResumeWall(t *testing.T) {
	path := filepath.Join(t.TempDir(), "request.json")
	writeFile(t, path, `{"payload":{"resume":true,"timeout_ms":100}}`)
	if out, err := exec.Command("python3", "-c", shortWallRequest, path).CombinedOutput(); err != nil {
		t.Fatalf("mutating request: %v: %s", err, out)
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	var request struct {
		Payload struct {
			TimeoutMS int `json:"timeout_ms"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(raw, &request); err != nil {
		t.Fatal(err)
	}
	if request.Payload.TimeoutMS != 0 {
		t.Fatalf("resumed fixture still has a wall: %d ms", request.Payload.TimeoutMS)
	}
}

func TestStopDuringWallContinuation(t *testing.T) {
	for _, end := range []bool{false, true} {
		t.Run(fmtBool(end), func(t *testing.T) {
			markers := scratch(t, "wall-stop")
			t.Setenv("WALL_STOP", markers)
			adapter := stub(t, "wall-worker", counting+`if [ "$TILLR_RUN_RESUME" = true ]; then
 echo resumed > "$WALL_STOP/resumed"
 while [ ! -f "$WALL_STOP/release" ]; do sleep 0.01; done
 `+committingAdapterBody+`printf 'RESULT: done\n'
else
 exec sleep 300
fi`)
			f := newLoopFixture(t, adapter)
			backend := f.policy.Backends["fixture"]
			backend.TimeoutSeconds = 1
			f.policy.Backends["fixture"] = backend
			fixtureAt(t, f.store, itemRecordWithFence("item_a", "in-queue", "candidate.txt"))
			t.Cleanup(func() { writeFile(t, filepath.Join(markers, "release"), "finish") })
			// The production policy is in whole seconds; inject a millisecond wall
			// through the existing run-bin seam, into the request the real face reads.
			// Keep the resumed request unbounded, just as continuation normally does.
			shortRun := stub(t, "short-wall-run", `if [ "$1" = exec ]; then
 input=
 previous=
 for arg do
  [ "$previous" = --input ] && input=$arg
  previous=$arg
 done
 python3 - "$input" <<'PY'
`+shortWallRequest+`PY
fi
exec "`+sibling(t, "run")+`" "$@"`)
			cmd, out := startStopFixtureWithRun(t, f, 1, shortRun)
			waitForFile(t, filepath.Join(markers, "resumed"), hangGuard)
			if len(terminalRunRows(t, f.store)) != 1 {
				t.Fatal("continuation test needs the previous terminal row")
			}
			stopFixture(t, f, cmd, out, end)
			if end {
				if n := len(stopEventsOf(t, f.store, "drain.orphaned")); n != 1 {
					t.Fatalf("live continuation missed by explicit end: %d interruptions", n)
				}
			} else {
				if len(matchingPIDs(t, f.store)) == 0 {
					t.Fatal("ordinary stop ended the continuation")
				}
				writeFile(t, filepath.Join(markers, "release"), "finish")
				steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
				result, err := f.newLoop(t, 1, steps).Run(context.Background())
				if err != nil || result.Landed != 1 || result.Dispatched != 0 {
					t.Fatalf("offline continuation not settled: %+v %v", result, err)
				}
			}
		})
	}
}

func TestOfflineEndFindsFaceAfterSupervisorCrash(t *testing.T) {
	markers := scratch(t, "crash-stop")
	t.Setenv("CRASH_STOP", markers)
	adapter := stub(t, "crash-worker", `echo started > "$CRASH_STOP/started"
while [ ! -f "$CRASH_STOP/release" ]; do sleep 0.02; done`)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, itemRecordWithFence("item_a", "in-queue", "candidate.txt"))
	t.Cleanup(func() { writeFile(t, filepath.Join(markers, "release"), "finish") })
	cmd, _ := startStopFixture(t, f, 1)
	waitForFile(t, filepath.Join(markers, "started"), hangGuard)
	if err := cmd.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = cmd.Wait()
	if _, err := os.Stat(filepath.Join(f.root, ".tillr", "loop", "run-item_a-a1", "process.json")); !os.IsNotExist(err) {
		t.Fatal("test must find the face without a post-start identity file")
	}
	raw, err := exec.Command(bin, "stop", "--root", f.root, "--store", f.store, "--end-runs").CombinedOutput()
	if err != nil {
		t.Fatalf("offline end: %v: %s", err, raw)
	}
	deadline := time.Now().Add(hangGuard)
	for len(terminalRunRows(t, f.store)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if len(terminalRunRows(t, f.store)) != 1 || len(stopEventsOf(t, f.store, "drain.orphaned")) != 1 {
		t.Fatal("surviving face was not ended and recorded")
	}
}
