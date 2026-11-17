// Package quota reads the drain-owned quota observations without running probes.
package quota

import (
	"encoding/json"
	"github.com/mschulkind-oss/tillr/store"
	"strings"
	"time"
)

const ProbeInterval = time.Minute

const DDL = `CREATE TABLE IF NOT EXISTS backend_quota (
 backend TEXT PRIMARY KEY, command TEXT NOT NULL, observed_at INTEGER NOT NULL,
 reserve REAL NOT NULL, windows TEXT NOT NULL, error TEXT NOT NULL
);
CREATE TABLE IF NOT EXISTS backend_quota_readings (
 seq INTEGER PRIMARY KEY, backend TEXT NOT NULL, command TEXT NOT NULL,
 observed_at INTEGER NOT NULL, reserve REAL NOT NULL, windows TEXT NOT NULL, error TEXT NOT NULL
);`

// QuotaWindow is a fraction used and an absolute reset reported by the backend.
type QuotaWindow struct {
	Kind     string     `json:"kind"`
	Fraction float64    `json:"fraction"`
	ResetAt  *time.Time `json:"reset_at"`
}

type QuotaStatus struct {
	Backend    string        `json:"backend"`
	ObservedAt time.Time     `json:"observed_at"`
	Reserve    float64       `json:"reserve"`
	Windows    []QuotaWindow `json:"windows"`
	Error      string        `json:"error,omitempty"`
	Command    string        `json:"-"`
}

func ReadStatuses(r *store.Reader) ([]QuotaStatus, error) {
	rows, err := r.Query(`SELECT backend,command,observed_at,reserve,windows,error FROM backend_quota ORDER BY backend`)
	if err != nil && strings.Contains(err.Error(), "no such table: backend_quota") {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []QuotaStatus{}
	for rows.Next() {
		var q QuotaStatus
		var at int64
		var raw string
		if err := rows.Scan(&q.Backend, &q.Command, &at, &q.Reserve, &raw, &q.Error); err != nil {
			return nil, err
		}
		q.ObservedAt = time.UnixMilli(at).UTC()
		if err := json.Unmarshal([]byte(raw), &q.Windows); err != nil {
			return nil, err
		}
		out = append(out, q)
	}
	return out, rows.Err()
}
