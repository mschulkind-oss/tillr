package procguard

import (
	"bytes"
	"io"
	"os"
	"runtime"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
)

const SQLiteTraceLineLimit = 64 << 10

// SQLiteTraceForwarder preserves only the trace lines (drainmodel.TraceLinePrefixes), including
// prefixed stack frames. Child stderr can contain secrets unrelated to SQLite.
// Oversized lines are dropped, and diagnostic output errors never change Sync.
type SQLiteTraceForwarder struct {
	Output   io.Writer
	pending  []byte
	dropping bool
}

func (f *SQLiteTraceForwarder) Write(p []byte) (int, error) {
	size := len(p)
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		complete := end >= 0
		if !complete {
			end = len(p)
		}
		part := p[:end]
		if !f.dropping {
			if len(f.pending)+len(part) > SQLiteTraceLineLimit {
				f.pending = nil
				f.dropping = true
			} else {
				f.pending = append(f.pending, part...)
			}
		}
		if !complete {
			break
		}
		f.Flush()
		p = p[end+1:]
	}
	return size, nil
}

func (f *SQLiteTraceForwarder) Flush() {
	if !f.dropping && drainmodel.IsTraceLine(f.pending) {
		_, _ = f.Output.Write(append(f.pending, '\n'))
	}
	f.pending = nil
	f.dropping = false
}

// OwnedTransitionTimeoutDiagnostic captures a goroutine snapshot when TILLR_SQLITE_TRACE=1.
// Called only at an existing timeout before its assertion as diagnostic evidence of the
// actual blocked stage, not a longer completion deadline.
func OwnedTransitionTimeoutDiagnostic() string {
	if os.Getenv("TILLR_SQLITE_TRACE") != "1" {
		return ""
	}
	buf := make([]byte, 1<<20)
	n := runtime.Stack(buf, true)
	diagnostic := "\nblocked-stage goroutine snapshot:\n" + string(buf[:n])
	if n == len(buf) {
		diagnostic += "\nstack snapshot truncated"
	}
	return diagnostic
}
