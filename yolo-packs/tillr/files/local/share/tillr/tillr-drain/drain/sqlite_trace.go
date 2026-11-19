package drain

import (
	"bytes"
	"io"
)

const sqliteTraceLineLimit = 64 << 10

// traceLinePrefixes begin the lines of the opt-in trace (TILLR_SQLITE_TRACE=1) a tillr
// program writes on stderr: the store's transaction spans and stack dumps (tillr/store's
// trace.go and ioguard.go) and the lander's git spans (tillr-land's trunk.go). Each carries
// what it is about and never the program's own reason for failing.
var traceLinePrefixes = [][]byte{[]byte("tillr-sqlite "), []byte("tillr-sqlite-stack "), []byte("tillr-git ")}

// traceLinePrefixLen is the longest trace prefix: a line this long is known to be trace or not.
var traceLinePrefixLen = func() int {
	n := 0
	for _, prefix := range traceLinePrefixes {
		n = max(n, len(prefix))
	}
	return n
}()

func isTraceLine(line []byte) bool {
	for _, prefix := range traceLinePrefixes {
		if bytes.HasPrefix(line, prefix) {
			return true
		}
	}
	return false
}

// sqliteTraceForwarder preserves only the trace lines (traceLinePrefixes), including
// prefixed stack frames. Child stderr can contain secrets unrelated to SQLite.
// Oversized lines are dropped, and diagnostic output errors never change Sync.
type sqliteTraceForwarder struct {
	output   io.Writer
	pending  []byte
	dropping bool
}

func (f *sqliteTraceForwarder) Write(p []byte) (int, error) {
	size := len(p)
	for len(p) > 0 {
		end := bytes.IndexByte(p, '\n')
		complete := end >= 0
		if !complete {
			end = len(p)
		}
		part := p[:end]
		if !f.dropping {
			if len(f.pending)+len(part) > sqliteTraceLineLimit {
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

func (f *sqliteTraceForwarder) Flush() {
	if !f.dropping && isTraceLine(f.pending) {
		_, _ = f.output.Write(append(f.pending, '\n'))
	}
	f.pending = nil
	f.dropping = false
}
