package runhistory

import (
	"database/sql"
	"errors"
	"sync/atomic"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// PendingEventsFolded counts how many events foldPendingHistory actually processed.
// Tests and benchmarks use it to verify incremental caching avoids re-folding
// unchanged history on every pass.
var PendingEventsFolded atomic.Int64

type pendingHistoryReadOnlyProbeKey struct{}

// IsReadOnlyReader reports whether r belongs to a read-only ReadTx (which caches
// ReadOnce values for the transaction) rather than a writable Tx.Reader() (which
// always invokes ReadOnce's loader).
func IsReadOnlyReader(r *store.Reader) bool {
	calls := 0
	load := func() (any, error) {
		calls++
		return struct{}{}, nil
	}
	_, _ = r.ReadOnce(pendingHistoryReadOnlyProbeKey{}, load)
	_, _ = r.ReadOnce(pendingHistoryReadOnlyProbeKey{}, load)
	return calls < 2
}

// ReaderStorePath returns the file path of the main SQLite database backing r,
// or empty for in-memory / temporary databases.
func ReaderStorePath(r *store.Reader) (string, error) {
	rows, err := r.Query(`PRAGMA database_list`)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	for rows.Next() {
		var seq int
		var name, file string
		if err := rows.Scan(&seq, &name, &file); err != nil {
			return "", err
		}
		if name == "main" {
			return file, nil
		}
	}
	return "", rows.Err()
}

func SamePendingAnchor(r *store.Reader, anchor event.Event) (bool, error) {
	var ts int64
	var kind, payload string
	err := r.QueryRow(`SELECT ts, kind, payload FROM events WHERE seq = ?`, anchor.Seq).Scan(&ts, &kind, &payload)
	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, err
	}
	return ts == anchor.At.UnixMilli() && kind == anchor.Kind && payload == string(anchor.Payload), nil
}
