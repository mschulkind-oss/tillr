package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os/exec"
	"time"

	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr/store"
)

// QuotaProbeInterval is the maximum age of a reading used for new dispatches.
// A window reset or changed command also invalidates a cached reading.
const QuotaProbeInterval = quota.ProbeInterval
const quotaCommandTimeout = 15 * time.Second
const quotaBodyLimit = 64 * 1024

const backendQuotaDDL = quota.DDL

type QuotaWindow = quota.QuotaWindow
type QuotaStatus = quota.QuotaStatus

func decodeQuota(raw []byte, at time.Time) ([]QuotaWindow, error) {
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

func readQuotaStatuses(r *store.Reader) ([]QuotaStatus, error) { return quota.ReadStatuses(r) }

func quotaFresh(q QuotaStatus, command string, at time.Time) bool {
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
const quotaUpdatedKind = "drain.quota-updated"

// refreshQuotas performs I/O outside transactions, then records every success or
// error. Failed readings close only that backend, never silently admit work.
// The mutex serializes pass and examination/failover probes in this process.
func (l *Loop) refreshQuotas(ctx context.Context) error {
	l.quotaMu.Lock()
	defer l.quotaMu.Unlock()
	policy := l.currentPolicy()
	if policy == nil {
		return nil
	}
	reader, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return err
	}
	var previous []QuotaStatus
	err = reader.ReadTx(ctx, func(r *store.Reader) error { var e error; previous, e = readQuotaStatuses(r); return e })
	reader.Close()
	if err != nil {
		return err
	}
	byName := map[string]QuotaStatus{}
	remove := []string{}
	for _, q := range previous {
		byName[q.Backend] = q
		if b, ok := policy.Backends[q.Backend]; !ok || len(b.Quota) == 0 {
			remove = append(remove, q.Backend)
		}
	}
	pending := []string{}
	reserves := map[string]float64{}
	for _, name := range backendNames(policy.Backends) {
		b := policy.Backends[name]
		if len(b.Quota) == 0 {
			continue
		}
		encoded, _ := json.Marshal(b.Quota)
		if old, ok := byName[name]; ok && quotaFresh(old, string(encoded), l.cfg.Now().UTC()) {
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
	s, err := store.Open(ctx, l.cfg.Store, true)
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
				if err := appendEffectOn(tx, quotaUpdatedKind, "backend", name, struct{}{}, l.cfg.Now().UTC()); err != nil {
					return err
				}
			}
			for name, reserve := range reserves {
				if _, err := tx.Exec(`UPDATE backend_quota SET reserve=? WHERE backend=?`, reserve, name); err != nil {
					return err
				}
				if err := appendEffectOn(tx, quotaUpdatedKind, "backend", name, struct{}{}, l.cfg.Now().UTC()); err != nil {
					return err
				}
			}
			return nil
		}); err != nil {
			return err
		}
	}
	for _, name := range pending {
		b := policy.Backends[name]
		encoded, _ := json.Marshal(b.Quota)
		command := string(encoded)
		probeCtx, cancel := context.WithTimeout(ctx, quotaCommandTimeout)
		cmd := exec.CommandContext(probeCtx, b.Quota[0], b.Quota[1:]...)
		cmd.Dir = l.cfg.Root
		var output quotaBuffer
		cmd.Stdout = &output
		// Discard stderr: an adapter must not make credentials into recorded state.
		cmd.Stderr = io.Discard
		err := runCheckGroup(cmd)
		cancel()
		if ctx.Err() != nil {
			return ctx.Err()
		}
		q := QuotaStatus{Backend: name, Command: command, ObservedAt: l.cfg.Now().UTC(), Reserve: b.Reserve, Windows: []QuotaWindow{}}
		if err == nil {
			q.Windows, err = decodeQuota(output.Bytes(), q.ObservedAt)
		}
		if err != nil {
			q.Error = fmt.Sprintf("quota command failed: %v", err)
			q.Windows = []QuotaWindow{}
			l.reportf("loop: backend %s: %s\n", name, q.Error)
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
			return appendEffectOn(tx, quotaUpdatedKind, "backend", name, struct{}{}, q.ObservedAt)
		}); err != nil {
			return err
		}
	}
	return nil
}

func (l *Loop) quotaReplacementBackend(ctx context.Context, start, seat string) (string, bool, error) {
	if err := l.refreshQuotas(ctx); err != nil {
		return "", false, err
	}
	s, err := store.OpenRead(ctx, l.cfg.Store)
	if err != nil {
		return "", false, err
	}
	defer s.Close()
	name, open := "", false
	policy := l.currentPolicy()
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		shut, err := readQuotaShutBackends(r, l.cfg.Now(), policy)
		if err != nil {
			return err
		}
		name, open = policy.availableBackend(start, seat, shut)
		return nil
	})
	return name, open, err
}

// readQuotaShutBackends is every backend a dispatch must skip: shut by quota or
// closed by its latest infrastructure ending.
func readQuotaShutBackends(r *store.Reader, at time.Time, policy *Policy) (map[string]bool, error) {
	return readQuotaShutBackendsFrom(r, at, policy, nil)
}

func readQuotaShutBackendsFrom(r *store.Reader, at time.Time, policy *Policy, byRun map[string]*pendingWork) (map[string]bool, error) {
	shut, err := readQuotaOnlyShut(r, at, policy)
	if err != nil {
		return nil, err
	}
	closed, err := readInfrastructureClosedFrom(r, byRun)
	if err != nil {
		return nil, err
	}
	for name := range closed {
		shut[name] = true
	}
	return shut, nil
}

// readDispatchShut is the shut set a claim re-reads. A retry the owner authorized
// is the one dispatch to a closed backend, so it is refused by quota only.
func readDispatchShut(r *store.Reader, at time.Time, policy *Policy, d Decision) (map[string]bool, error) {
	return readDispatchShutFrom(r, at, policy, d, nil)
}

func readDispatchShutFrom(r *store.Reader, at time.Time, policy *Policy, d Decision, byRun map[string]*pendingWork) (map[string]bool, error) {
	if d.Action == ActionRetry {
		return readQuotaOnlyShut(r, at, policy)
	}
	return readQuotaShutBackendsFrom(r, at, policy, byRun)
}

// No quota command means no reading-based hold. Refusal windows hold every backend.
func readQuotaOnlyShut(r *store.Reader, at time.Time, policy *Policy) (map[string]bool, error) {
	legacy, err := readShutBackends(r, at)
	if err != nil {
		return nil, err
	}
	if policy == nil {
		return legacy, nil
	}
	statuses, err := readQuotaStatuses(r)
	if err != nil {
		return nil, err
	}
	byName := map[string]QuotaStatus{}
	for _, q := range statuses {
		byName[q.Backend] = q
	}
	shut := legacy
	for name, b := range policy.Backends {
		if len(b.Quota) == 0 {
			continue
		}
		q, ok := byName[name]
		command, _ := json.Marshal(b.Quota)
		if legacy[name] || !ok || !quotaFresh(q, string(command), at) || q.Error != "" {
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
