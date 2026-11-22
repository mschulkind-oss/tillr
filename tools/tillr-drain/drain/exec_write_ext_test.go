package drain_test

import (
	"os"
	"syscall"
)

// writeForkSafe is os.WriteFile for a file the test may run, or copy a program from, next.
//
// A program written and then run can fail to start with ETXTBSY ("text file busy"): another
// goroutine of this test binary forks while the file is open for writing, the child holds
// a copy of the write descriptor until it execs, and an exec of the file in that window is
// refused. The suite runs its tests in parallel and forks constantly, so under load the
// window is hit: a run measured 288 refusals in 1,200 write-and-run pairs while 16 other
// goroutines forked, and none with the lock below. Holding syscall.ForkLock for reading
// across the write keeps every fork out of the window, because a fork holds the lock for
// writing from before the fork until the child has exec'd. Neither a retry nor a wait can
// replace it: the refusal is a deterministic outcome of the interleaving, not slowness.
func writeForkSafe(path string, data []byte, perm os.FileMode) error {
	syscall.ForkLock.RLock()
	defer syscall.ForkLock.RUnlock()
	return os.WriteFile(path, data, perm)
}
