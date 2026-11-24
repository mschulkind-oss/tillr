package drain

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// The loop process reads its store with the narrow selected-event cache on (loopFace), so
// this package's tests do too: every read the suite makes goes through the cache the loop
// uses without holding unselected event kinds in memory.
func TestMain(m *testing.M) {
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
