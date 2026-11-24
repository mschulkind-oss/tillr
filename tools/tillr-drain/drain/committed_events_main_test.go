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
	store.CacheSelectedEvents()
	defer runTestCleanups()
	return m.Run()
}
