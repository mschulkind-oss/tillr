package drain_test

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

func TestASettledLaneTakesTheThirdItemBeforeItsSiblingEnds(t *testing.T) {
	signals := scratch(t, "pass-signals")
	release := filepath.Join(signals, "release")
	third := filepath.Join(signals, "third")
	adapter := stub(t, "adapter", `lane=$(basename "$(dirname "$(pwd)")")
if [ "$lane" = lane-2 ]; then
  while [ ! -f "$PASS_RELEASE" ]; do sleep 0.01; done
else
  if [ -f "$PASS_FIRST" ]; then touch "$PASS_THIRD"; else touch "$PASS_FIRST"; fi
fi
`+committingAdapter)
	f := newLoopFixture(t, adapter)
	t.Cleanup(func() { cleanupStoreRuns(t, f.store) })
	t.Setenv("PASS_RELEASE", release)
	t.Setenv("PASS_FIRST", filepath.Join(signals, "first"))
	t.Setenv("PASS_THIRD", third)
	backend := f.policy.Backends["fixture"]
	backend.TimeoutSeconds = 0
	f.policy.Backends["fixture"] = backend
	fixtureAt(t, f.store, itemRecord("item_001", "in-queue"), itemRecord("item_002", "in-queue"), itemRecord("item_003", "in-queue"))
	loop := f.newLoop(t, 2, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	var result drain.LoopResult
	var err error
	go func() { result, err = loop.Run(ctx); close(finished) }()
	deadline := time.Now().Add(hangGuard)
	for time.Now().Before(deadline) {
		if _, e := os.Stat(third); e == nil {
			break
		}
		select {
		case <-finished:
			t.Fatalf("loop stopped before third dispatch: %+v / %v", result, err)
		default:
		}
		time.Sleep(10 * time.Millisecond)
	}
	_, dispatched := os.Stat(third)
	writeFile(t, release, "release\n")
	select {
	case <-finished:
	case <-time.After(hangGuard):
		cancel()
		t.Fatal("loop did not finish after sibling was released")
	}
	if dispatched != nil {
		t.Fatal("third item was not dispatched on the first lane before releasing the second lane")
	}
	if err != nil || result.Stop != drain.StopQueueDrained || result.Dispatched != 3 {
		t.Fatalf("result %+v / %v", result, err)
	}
}
