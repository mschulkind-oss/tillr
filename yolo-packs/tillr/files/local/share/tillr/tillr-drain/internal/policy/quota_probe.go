package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sync"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/runhistory"
	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// QuotaProbeInterval is the maximum age of a reading used for new dispatches.
// A window reset or changed command also invalidates a cached reading.
const QuotaProbeInterval = quota.ProbeInterval
const quotaCommandTimeout = 15 * time.Second
const quotaBodyLimit = 64 * 1024

const BackendQuotaDDL = quota.DDL

type QuotaWindow = quota.QuotaWindow
type QuotaStatus = quota.QuotaStatus

func DecodeQuota(raw []byte, at time.Time) ([]QuotaWindow, error) {
	var doc struct {
		Windows []struct {
			Kind     string     `json:"kind"`
			Fraction *float64   `json:"fraction"`
			ResetAt  *time.Time `json:"reset_at"`
		} `json:"windows"`
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	if err := decoder.Decode(&doc); err != nil {
		return nil, fmt.Errorf("invalid quota JSON: %w", err)
	}
	var extra any
	if err := decoder.Decode(&extra); err != io.EOF {
		return nil, fmt.Errorf("invalid quota: expected one JSON document")
	}
	if len(doc.Windows) == 0 {
		return nil, fmt.Errorf("invalid quota: windows must not be empty")
	}
	seen := map[string]bool{}
	out := make([]QuotaWindow, 0, len(doc.Windows))
	for _, w := range doc.Windows {
		if w.Kind == "" || seen[w.Kind] || w.Fraction == nil || math.IsNaN(*w.Fraction) || math.IsInf(*w.Fraction, 0) || *w.Fraction < 0 || (w.ResetAt == nil && *w.Fraction != 0) || (w.ResetAt != nil && !w.ResetAt.After(at)) {
			return nil, fmt.Errorf("invalid quota: each window needs a unique kind, nonnegative fraction and future reset_at")
		}
		seen[w.Kind] = true
		if w.ResetAt != nil {
			utc := w.ResetAt.UTC()
			w.ResetAt = &utc
		}
		out = append(out, QuotaWindow{Kind: w.Kind, Fraction: *w.Fraction, ResetAt: w.ResetAt})
	}
	return out, nil
}

// A broken command cannot grow the loop's memory by streaming unlimited output.
type quotaBuffer struct{ bytes.Buffer }

func (b *quotaBuffer) Write(p []byte) (int, error) {
	if b.Len()+len(p) > quotaBodyLimit {
		return 0, fmt.Errorf("quota output exceeds %d bytes", quotaBodyLimit)
	}
	return b.Buffer.Write(p)
}

func ReadQuotaStatuses(r *store.Reader) ([]QuotaStatus, error) { return quota.ReadStatuses(r) }

func QuotaFresh(q QuotaStatus, command string, at time.Time) bool {
	if q.Command != command || at.Before(q.ObservedAt.Truncate(time.Second)) || at.Sub(q.ObservedAt) > QuotaProbeInterval {
		return false
	}
	for _, w := range q.Windows {
		if w.ResetAt != nil && !w.ResetAt.After(at) {
			return false
		}
	}
	return true
}

// Quota tables are not event folds, but their writes must advance the start
// view's cursor so an older publication cannot certify a drained queue.
const QuotaUpdatedKind = "drain.quota-updated"

func init() {
	if err := event.Register(QuotaUpdatedKind, drainmodel.Utility); err != nil {
		panic(err)
	}
}

// RefreshQuotas performs I/O outside transactions, then records every success or
// error. Failed readings close only that backend, never silently admit work.
// The mutex serializes pass and examination/failover probes in this process.
func RefreshQuotas(ctx context.Context, root, storePath string, mu *sync.Mutex, p *Policy, now func() time.Time, reportf func(string, ...any)) error {
	mu.Lock()
	defer mu.Unlock()
	if p == nil {
		return nil
	}
	reader, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return err
	}
	var previous []QuotaStatus
	err = reader.ReadTx(ctx, func(r *store.Reader) error { var e error; previous, e = ReadQuotaStatuses(r); return e })
	reader.Close()
	if err != nil {
		return err
	}
	byName := map[string]QuotaStatus{}
	remove := []string{}
	for _, q := range previous {
		byName[q.Backend] = q
		if b, ok := p.Backends[q.Backend]; !ok || len(b.Quota) == 0 {
			remove = append(remove, q.Backend)
		}
	}
	pending := []string{}
	reserves := map[string]float64{}
	for _, name := range BackendNames(p.Backends) {
		b := p.Backends[name]
		if len(b.Quota) == 0 {
			continue
		}
		encoded, _ := json.Marshal(b.Quota)
		if old, ok := byName[name]; ok && QuotaFresh(old, string(encoded), now().UTC()) {
			if old.Reserve != b.Reserve {
				reserves[name] = b.Reserve
			}
		} else {
			pending = append(pending, name)
		}
	}
	// Cached and unconfigured backends never acquire the store's writer lock.
	if len(remove)+len(reserves)+len(pending) == 0 {
		return nil
	}
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		return err
	}
	defer s.Close()
	if len(remove)+len(reserves) > 0 {
		// Removing a live observation preserves its append-only history.
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			for _, name := range remove {
				if _, err := tx.Exec(`DELETE FROM backend_quota WHERE backend=?`, name); err != nil {
					return err
				}
				if err := runhistory.AppendEffectOn(tx, QuotaUpdatedKind, "backend", name, struct{}{}, now().UTC()); err != nil {
					return err
				}
			}
			for name, reserve := range reserves {
				if _, err := tx.Exec(`UPDATE backend_quota SET reserve=? WHERE backend=?`, reserve, name); err != nil {
					return err
				}
				if err := runhistory.AppendEffectOn(tx, QuotaUpdatedKind, "backend", name, struct{}{}, now().UTC()); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, name := range pending {
		b := p.Backends[name]
		encoded, _ := json.Marshal(b.Quota)
		command := string(encoded)
		probeCtx, cancel := context.WithTimeout(ctx, quotaCommandTimeout)
		cmd := exec.CommandContext(probeCtx, b.Quota[0], b.Quota[1:]...)
		cmd.Dir = root
		var output quotaBuffer
		cmd.Stdout = &output
		// Discard stderr: an adapter must not make credentials into recorded state.
		cmd.Stderr = io.Discard
		err := heavyrun.RunCheckGroupStarted(cmd, nil)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		q := QuotaStatus{Backend: name, Command: command, ObservedAt: now().UTC(), Reserve: b.Reserve, Windows: []QuotaWindow{}}
		if err == nil {
			q.Windows, err = DecodeQuota(output.Bytes(), q.ObservedAt)
		}
		if err != nil {
			q.Error = fmt.Sprintf("quota command failed: %v", err)
			q.Windows = []QuotaWindow{}
			reportf("loop: backend %s: %s\n", name, q.Error)
		}
		raw, _ := json.Marshal(q.Windows)
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			args := []any{name, command, q.ObservedAt.UnixMilli(), b.Reserve, string(raw), q.Error}
			if _, err := tx.Exec(`INSERT INTO backend_quota_readings(backend,command,observed_at,reserve,windows,error) VALUES(?,?,?,?,?,?)`, args...); err != nil {
				return err
			}
			if _, err := tx.Exec(`INSERT INTO backend_quota(backend,command,observed_at,reserve,windows,error) VALUES(?,?,?,?,?,?) ON CONFLICT(backend) DO UPDATE SET command=excluded.command,observed_at=excluded.observed_at,reserve=excluded.reserve,windows=excluded.windows,error=excluded.error WHERE excluded.observed_at>=backend_quota.observed_at`, args...); err != nil {
				return err
			}
			return runhistory.AppendEffectOn(tx, QuotaUpdatedKind, "backend", name, struct{}{}, q.ObservedAt)
		}); err != nil {
			return err
		}
	}
	return nil
}

// No quota command means no reading-based hold. Refusal windows hold every backend.
func ReadQuotaOnlyShut(r *store.Reader, at time.Time, p *Policy) (map[string]bool, error) {
	legacy, err := ReadShutBackends(r, at)
	if err != nil {
		return nil, err
	}
	if p == nil {
		return legacy, nil
	}
	statuses, err := ReadQuotaStatuses(r)
	if err != nil {
		return nil, err
	}
	byName := map[string]QuotaStatus{}
	for _, q := range statuses {
		byName[q.Backend] = q
	}
	shut := legacy
	for name, b := range p.Backends {
		if len(b.Quota) == 0 {
			continue
		}
		q, ok := byName[name]
		command, _ := json.Marshal(b.Quota)
		if legacy[name] || !ok || !QuotaFresh(q, string(command), at) || q.Error != "" {
			shut[name] = true
			continue
		}
		for _, w := range q.Windows {
			if w.Fraction >= 1-b.Reserve {
				shut[name] = true
			}
		}
	}
	return shut, nil
}
