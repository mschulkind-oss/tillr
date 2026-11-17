package main_test

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// The helper is a separate loop-side process, so killing it closes every handle
// the supervisor owned without signaling the run.
func TestRunOutputProcessHelper(t *testing.T) {
	if os.Getenv("RUN_OUTPUT_HELPER") != "1" {
		return
	}
	var req run.Request
	if err := json.Unmarshal([]byte(os.Getenv("RUN_OUTPUT_REQUEST")), &req); err != nil {
		t.Fatal(err)
	}
	steps := &drain.ProcessSteps{RunBin: os.Getenv("RUN_OUTPUT_BIN"), Store: os.Getenv("RUN_OUTPUT_STORE"), LaneRoot: os.Getenv("RUN_OUTPUT_LANES"), Root: os.Getenv("RUN_OUTPUT_ROOT"), Now: time.Now}
	if _, _, err := steps.Run(context.Background(), req); err != nil {
		t.Fatal(err)
	}
}

func TestRunOutputSurvivesLoopProcessExit(t *testing.T) {
	f := newResumableFixture(t)
	signals := scratch(t, "run-output-signals")
	ready, release, finished := filepath.Join(signals, "ready"), filepath.Join(signals, "release"), filepath.Join(signals, "finished")
	// Assert both descriptors are regular files before exec; stdout must not be
	// left as a pipe either, even though it only prints a record at the end.
	wrapper := stub(t, "run-wrapper", `if [ ! -f /proc/self/fd/1 ] || [ ! -f /proc/self/fd/2 ]; then
 echo pipes > "`+ready+`.writing"
 mv "`+ready+`.writing" "`+ready+`"
 exit 4
fi
echo files > "`+ready+`.writing"
mv "`+ready+`.writing" "`+ready+`"
while [ ! -f "`+release+`" ]; do sleep 0.01; done
"`+sibling(t, "run")+`" "$@"
status=$?
# Delay the payload to exercise a reader observing the publication boundary.
{ sleep 0.1; echo "$status"; } > "`+finished+`.writing"
mv "`+finished+`.writing" "`+finished+`"
exit "$status"`)
	req := f.loop.policy.Backends["pi"].Request(f.item, "lane-1", f.runID, "pi")
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatal(err)
	}
	helper := exec.Command(os.Args[0], "-test.run=^TestRunOutputProcessHelper$")
	helper.Env = append(os.Environ(), "RUN_OUTPUT_HELPER=1", "RUN_OUTPUT_REQUEST="+string(raw), "RUN_OUTPUT_BIN="+wrapper, "RUN_OUTPUT_STORE="+f.loop.store, "RUN_OUTPUT_LANES="+f.loop.laneRoot, "RUN_OUTPUT_ROOT="+f.loop.root)
	helper.Stdout, helper.Stderr = os.Stdout, os.Stderr
	if err := helper.Start(); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = helper.Process.Kill(); writeFile(t, release, "release"); _ = helper.Wait() })
	waitForFile(t, ready, 30*time.Second)
	if got := strings.TrimSpace(readText(t, ready)); got != "files" {
		t.Fatalf("run inherited %s, want regular files and no pipes", got)
	}
	if err := helper.Process.Kill(); err != nil {
		t.Fatal(err)
	}
	_ = helper.Wait()
	writeFile(t, release, "release")
	waitForFile(t, finished, 30*time.Second)
	if got := strings.TrimSpace(readText(t, finished)); got != "1" {
		t.Fatalf("run face exit %q, want fixture exit 1 (not a broken pipe)", got)
	}
	s, err := store.OpenRead(context.Background(), f.loop.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var state string
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		return r.QueryRow(`SELECT state FROM runs WHERE run_id = ?`, f.runID).Scan(&state)
	}); err != nil {
		t.Fatal(err)
	}
	if state != run.StateExited {
		t.Fatalf("run state %q, want exited after loop died", state)
	}
	if _, err := os.Stat(filepath.Join(drain.RunDir(f.loop.root, f.runID), "run.record.json")); err != nil {
		t.Fatal(err)
	}
}

func TestFailedRunReportsFileOutput(t *testing.T) {
	dir := scratch(t, "missing-adapter")
	writeFile(t, filepath.Join(dir, "pause-kind"), "stops-where-it-stands\n")
	f := newLoopFixture(t, filepath.Join(dir, "absent"))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	report := &strings.Builder{}
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	loop := f.newLoopWith(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, report)
	if _, err := loop.Run(context.Background()); err != nil {
		t.Fatal(err)
	}
	output, err := os.ReadFile(filepath.Join(drain.RunDir(f.root, "run-item_041-a1"), "run.stderr"))
	if err != nil {
		t.Fatal(err)
	}
	diagnostic := strings.TrimSpace(string(output))
	for _, marker := range []string{"tillr-run: starting", f.policy.Backends["fixture"].Adapter, "no such file or directory"} {
		if !strings.Contains(diagnostic, marker) || !strings.Contains(report.String(), marker) {
			t.Fatalf("meaningful diagnostic %q missing from file/report:\n%s\n%s", marker, diagnostic, report)
		}
	}
	expected := diagnostic
	if len(output) > drain.StderrTailBytes {
		expected = "…" + strings.TrimSpace(string(output[len(output)-drain.StderrTailBytes:]))
	}
	if diagnostic == "" || !strings.Contains(report.String(), expected) {
		t.Fatalf("bounded file diagnostic %q missing from loop report:\n%s", expected, report)
	}
}

func TestLegacyRunWithoutOutputSettles(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	// Simulate an older launcher: the real face records the outcome but there
	// is no output file by the time the loop settles it.
	path := filepath.Join(drain.RunDir(f.root, "run-item_041-a1"), "run.stderr")
	steps.RunBin = stub(t, "legacy-run", `"`+steps.RunBin+`" "$@"
status=$?
rm -f "`+path+`"
exit "$status"`)
	report := &strings.Builder{}
	loop := f.newLoopWith(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, report)
	result, err := loop.Run(context.Background())
	if err != nil || result.Landed != 1 {
		t.Fatalf("legacy run failed: %+v / %v\n%s", result, err, report)
	}
	if !strings.Contains(report.String(), "no output recorded") {
		t.Fatalf("missing legacy diagnostic:\n%s", report)
	}
}
