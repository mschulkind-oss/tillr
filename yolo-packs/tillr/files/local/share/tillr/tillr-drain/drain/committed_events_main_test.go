package drain

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/tillr-loop/loop"
	"github.com/mschulkind-oss/tillr/store"
)

// The loop process reads its store with the narrow selected-event cache on (loopFace), so
// this package's tests do too: every read the suite makes goes through the cache the loop
// uses without holding unselected event kinds in memory.
func TestMain(m *testing.M) {
	if os.Getenv("TILLR_DRAIN_EXEC_LOOP_ADAPTER") == "1" {
		_ = os.Unsetenv("TILLR_DRAIN_EXEC_LOOP_ADAPTER")
		os.Exit(loop.Main(os.Args[1:], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(runDrainTests(m))
}

func runDrainTests(m *testing.M) int {
	// The stand-in `pi` programs these cases write do not answer `pi --features`, so the
	// features they stand for are stated to the adapter instead of probed.
	os.Setenv("TILLR_PI_FEATURES", "parked-tool-calls")
	store.CacheSelectedEvents()
	defer runTestCleanups()
	return m.Run()
}
