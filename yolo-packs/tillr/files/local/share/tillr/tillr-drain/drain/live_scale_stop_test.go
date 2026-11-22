package drain_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

func TestLoopScalesAndStopsWithLiveRuns(t *testing.T) {
	markers := scratch(t, "live-scale")
	t.Setenv("LIVE_SCALE_TEST", markers)
	adapter := stub(t, "live-scale-worker", counting+`touch "$LIVE_SCALE_TEST/$TILLR_RUN_ID.started"
while [ ! -f "$LIVE_SCALE_TEST/$TILLR_RUN_ID.release" ] && [ ! -f "$LIVE_SCALE_TEST/cleanup" ]; do sleep 0.02; done
`+committingAdapterBody+`printf 'RESULT: done\n'`)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, itemRecordWithFence("item_a", "in-queue", "candidate.txt"), itemRecordWithFence("item_b", "in-queue", "candidate.txt"))
	t.Cleanup(func() { writeFile(t, filepath.Join(markers, "cleanup"), "finish") })
	// Record the actual exec face PID, not an adapter descendant.
	runBin := stub(t, "record-run-pid", `if [ "$1" = exec ]; then echo "$$" > "$LIVE_SCALE_TEST/$TILLR_RUN_ID.pid"; fi
exec "`+sibling(t, "run")+`" "$@"`)
	cmd, out := startStopFixtureWithRun(t, f, 1, runBin)
	const first, second = "run-item_a-a1", "run-item_b-a1"
	waitForFile(t, filepath.Join(markers, first+".started"), hangGuard)
	scale := func(count string) {
		t.Helper()
		if code, output := invoke(t, "scale", "--root", f.root, "--store", f.store, "--lanes", count); code != 0 {
			t.Fatalf("scale: %d: %s", code, output)
		}
	}
	waitFor := func(what string, check func() bool) {
		t.Helper()
		deadline := time.Now().Add(15 * time.Second)
		for !check() {
			if time.Now().After(deadline) {
				t.Fatalf("timed out waiting for %s", what)
			}
			time.Sleep(20 * time.Millisecond)
		}
	}
	verdict := func(id string) bool {
		for _, e := range stopEventsOf(t, f.store, "gate.verdict") {
			if e.ObjectID != nil && *e.ObjectID == id {
				return true
			}
		}
		return false
	}
	scale("2")
	waitForFile(t, filepath.Join(markers, second+".started"), 15*time.Second)
	if n := len(terminalRunRows(t, f.store)); n != 0 {
		t.Fatalf("scaling ended a run: %d terminal rows", n)
	}
	pid, err := os.ReadFile(filepath.Join(markers, first+".pid"))
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(markers, first+".release"), "finish")
	waitFor("reaping finished run face "+strings.TrimSpace(string(pid)), func() bool {
		_, err := os.Stat(filepath.Join("/proc", strings.TrimSpace(string(pid))))
		return os.IsNotExist(err)
	})
	waitFor("the first run's verdict after scaling up", func() bool { return verdict(first) })

	offersNoLanes := func() bool {
		ctx := context.Background()
		s, err := store.OpenRead(ctx, f.store)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		var empty bool
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			lanes, live, err := drain.LiveLoopLanes(r, f.store, time.Now())
			empty = live && len(lanes) == 0
			return err
		}); err != nil {
			t.Fatal(err)
		}
		return empty
	}
	scale("0")
	waitFor("the live loop to offer zero lanes", offersNoLanes)

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if raw, err := exec.CommandContext(ctx, drainBin(t), "stop", "--root", f.root, "--store", f.store).CombinedOutput(); err != nil {
		t.Fatalf("stop: %v: %s", err, raw)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("loop: %v: %s", err, out.String())
		}
	case <-ctx.Done():
		t.Fatal("stopped loop did not exit while the second run was still executing")
	}
	if n := len(terminalRunRows(t, f.store)); n != 1 {
		t.Fatalf("stop did not leave the second run in flight: %d terminal rows", n)
	}
	next, nextOut := startStopFixture(t, f, 1)
	waitFor("the successor to honor the saved zero target", offersNoLanes)
	writeFile(t, filepath.Join(markers, second+".release"), "finish")
	waitFor("the successor to examine the surviving run", func() bool { return verdict(second) })
	stopFixture(t, f, next, nextOut, false)
}
