package store

import (
	"fmt"
	"os"
	"sync/atomic"
	"time"
)

var writerSequence atomic.Uint64

// traceWriter is opt-in diagnostics, never a new write or heartbeat. PID plus
// transaction ID correlate begin/acquired/end spans across independently opened stores.
func traceWriter(id uint64, path, operation, phase string, started time.Time, err error) {
	fmt.Fprintf(os.Stderr, "tillr-sqlite at=%s pid=%d tx=%d store=%q operation=%q phase=%s elapsed=%s error=%q\n",
		time.Now().UTC().Format(time.RFC3339Nano), os.Getpid(), id, path, operation, phase, time.Since(started), errorText(err))
}

func errorText(err error) string {
	if err == nil {
		return ""
	}
	return err.Error()
}
