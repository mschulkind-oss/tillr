package drain

import "sync"

var (
	testCleanupMu    sync.Mutex
	testCleanupFuncs []func()
)

// RegisterTestCleanup registers a process-level cleanup function to run via defer
// before os.Exit in TestMain.
func RegisterTestCleanup(fn func()) {
	testCleanupMu.Lock()
	defer testCleanupMu.Unlock()
	testCleanupFuncs = append(testCleanupFuncs, fn)
}

func runTestCleanups() {
	testCleanupMu.Lock()
	fns := append([]func(){}, testCleanupFuncs...)
	testCleanupFuncs = nil
	testCleanupMu.Unlock()
	for i := len(fns) - 1; i >= 0; i-- {
		fns[i]()
	}
}
