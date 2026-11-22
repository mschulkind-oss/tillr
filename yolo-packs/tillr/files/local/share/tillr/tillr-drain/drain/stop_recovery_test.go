package drain_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
)

func callsNamed(lines []string, name string) []string {
	var found []string
	for _, line := range lines {
		if strings.Contains(strings.Fields(line)[0], "/"+name) {
			found = append(found, line)
		}
	}
	return found
}

// The third run reports a supervisor fault after both siblings are in flight.
type faultWithSiblings struct {
	*drain.ProcessSteps
	markers string
}

func (s faultWithSiblings) Run(ctx context.Context, req run.Request) (run.Record, string, error) {
	if req.ItemID != "item_fault" {
		return s.ProcessSteps.Run(ctx, req)
	}
	for {
		_, a := os.Stat(filepath.Join(s.markers, "item_a.started"))
		_, b := os.Stat(filepath.Join(s.markers, "item_b.started"))
		if a == nil && b == nil {
			return run.Record{}, "", fmt.Errorf("fixture supervisor fault")
		}
		select {
		case <-ctx.Done():
			return run.Record{}, "", ctx.Err()
		case <-time.After(10 * time.Millisecond):
		}
	}
}

func TestFaultLeavesTwoRunsToFinishAndReplacementJudgesOnce(t *testing.T) {
	markers := scratch(t, "survivors")
	t.Setenv("SURVIVOR_MARKERS", markers)
	t.Cleanup(func() {
		writeFile(t, filepath.Join(markers, "item_a.finish"), "finish")
		writeFile(t, filepath.Join(markers, "item_b.finish"), "finish")
	})
	adapter := stub(t, "survivor", counting+`echo started > "$SURVIVOR_MARKERS/$TILLR_ITEM_ID.started"
while [ ! -f "$SURVIVOR_MARKERS/$TILLR_ITEM_ID.finish" ]; do sleep 0.02; done
`+committingAdapterBody+`echo finished > "$SURVIVOR_MARKERS/$TILLR_ITEM_ID.finished"
printf 'RESULT: done\n'`)
	// The run face exports the run identity; derive the fixture item from it.
	adapter = stub(t, "identified-survivor", `TILLR_ITEM_ID=$(printf '%s' "$TILLR_RUN_ID" | cut -d- -f2)
export TILLR_ITEM_ID
exec "`+adapter+`" "$@"`)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, itemRecordWithFence("item_a", "in-queue", "candidate.txt"), itemRecordWithFence("item_b", "in-queue", "candidate.txt"), itemRecordWithFence("item_fault", "in-queue", "candidate.txt"))
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	l, err := drain.NewLoop(drain.LoopConfig{Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{"lane-1", "lane-2", "lane-3"}, Policy: f.policy, Steps: faultWithSiblings{steps, markers}, Stage: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, Now: steps.Now, ExitWhenDrained: true})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := l.Run(context.Background()); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("fixture fault was not reported")
		}
	case <-time.After(hangGuard):
		t.Fatal("fault stop waited for or canceled its runs")
	}
	writeFile(t, filepath.Join(markers, "item_a.finish"), "finish")
	waitForFile(t, filepath.Join(markers, "item_a.finished"), hangGuard)
	// One run finishes offline, the other while the replacement supervises it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replacement := f.newLoop(t, 3, steps)
	go func() { _, err := replacement.Run(ctx); done <- err }()
	writeFile(t, filepath.Join(markers, "item_b.finish"), "finish")
	deadline := time.Now().Add(hangGuard)
	for len(callsNamed(lines(t, f.count), "land")) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(hangGuard):
		t.Fatal("replacement did not stop")
	}
	if n := len(callsNamed(lines(t, f.count), "land")); n != 2 {
		t.Fatalf("land calls=%d, want 2", n)
	}
	for _, id := range []string{"item_a", "item_b"} {
		if n := len(runsForItem(t, f.store, id)); n != 1 {
			t.Fatalf("%s attempts=%d, want 1", id, n)
		}
		for _, face := range []string{"gate", "land"} {
			n := 0
			for _, call := range callsNamed(lines(t, f.count), face) {
				if strings.Contains(call, "--run run-"+id+"-a1 ") {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("%s %s calls=%d, want 1", id, face, n)
			}
		}
	}
}
