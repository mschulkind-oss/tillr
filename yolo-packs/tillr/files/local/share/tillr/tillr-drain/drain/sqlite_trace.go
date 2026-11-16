package drain

import (
	"bytes"
	"io"
)

const sqliteTraceLineLimit = 64 << 10

// sqliteTraceForwarder preserves only the store's diagnostic lines, including
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
	if !f.dropping && (bytes.HasPrefix(f.pending, []byte("tillr-sqlite ")) || bytes.HasPrefix(f.pending, []byte("tillr-sqlite-stack "))) {
		_, _ = f.output.Write(append(f.pending, '\n'))
	}
	f.pending = nil
	f.dropping = false
}
