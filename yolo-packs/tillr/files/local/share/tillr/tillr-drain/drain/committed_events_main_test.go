package drain

import (
	"os"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// The loop process reads its store with the committed-event cache on (loopFace), so this
// package's tests do too: every read the suite makes goes through the cache the loop uses.
func TestMain(m *testing.M) {
	os.Exit(runDrainTests(m))
}

func runDrainTests(m *testing.M) int {
	store.CacheCommittedEvents()
	defer runTestCleanups()
	return m.Run()
}
