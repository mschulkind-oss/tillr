package policy

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"math"
	"os/exec"
	"sort"
	"strings"
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
// error. Failed readings close only that pool's backends, never silently admit
// work. It retains and probes quota rows across every profile in policies,
// running each unique quota pool's command once and fanning the reading out to
// every backend in that pool with each backend's own reserve.
// The mutex serializes pass and examination/failover probes in this process.
func RefreshQuotas(ctx context.Context, root, storePath string, mu *sync.Mutex, policies []*Policy, activeIndex int, now func() time.Time, reportf func(string, ...any)) error {
	mu.Lock()
	defer mu.Unlock()
	ordered := make([]*Policy, 0, len(policies))
	if activeIndex >= 0 && activeIndex < len(policies) && policies[activeIndex] != nil {
		ordered = append(ordered, policies[activeIndex])
	}
	for i, p := range policies {
		if i == activeIndex || p == nil {
			continue
		}
		ordered = append(ordered, p)
	}
	if len(ordered) == 0 {
		return nil
	}
	backends := map[string]Backend{}
	for _, p := range ordered {
		for _, name := range BackendNames(p.Backends) {
			if _, exists := backends[name]; !exists {
				backends[name] = p.Backends[name]
			}
		}
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
		if b, ok := backends[q.Backend]; !ok || len(b.Quota) == 0 {
			remove = append(remove, q.Backend)
		}
	}
	type quotaPool struct {
		key     string
		command string
		quota   []string
		members []string
	}
	poolsByKey := map[string]*quotaPool{}
	poolKeys := []string{}
	for _, name := range BackendNames(backends) {
		b := backends[name]
		if len(b.Quota) == 0 {
			continue
		}
		key := QuotaPoolKey(name, b)
		pool, ok := poolsByKey[key]
		if !ok {
			pool = &quotaPool{
				key:     key,
				command: QuotaCommandKey(b.Quota),
				quota:   b.Quota,
			}
			poolsByKey[key] = pool
			poolKeys = append(poolKeys, key)
		}
		pool.members = append(pool.members, name)
	}
	sort.Strings(poolKeys)
	pending := []*quotaPool{}
	reserves := map[string]float64{}
	at := now().UTC()
	for _, key := range poolKeys {
		pool := poolsByKey[key]
		allFresh := true
		for _, name := range pool.members {
			if old, ok := byName[name]; !ok || !QuotaFresh(old, pool.command, at) {
				allFresh = false
				break
			}
		}
		if allFresh {
			for _, name := range pool.members {
				if old := byName[name]; old.Reserve != backends[name].Reserve {
					reserves[name] = backends[name].Reserve
				}
			}
			continue
		}
		pending = append(pending, pool)
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
	for _, pool := range pending {
		probeCtx, cancel := context.WithTimeout(ctx, quotaCommandTimeout)
		cmd := exec.CommandContext(probeCtx, pool.quota[0], pool.quota[1:]...)
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
		observedAt := now().UTC()
		windows := []QuotaWindow{}
		errMsg := ""
		if err == nil {
			windows, err = DecodeQuota(output.Bytes(), observedAt)
		}
		if err != nil {
			errMsg = fmt.Sprintf("quota command failed: %v", err)
			windows = []QuotaWindow{}
			reportf("loop: backend %s: %s\n", strings.Join(pool.members, ","), errMsg)
		}
		raw, _ := json.Marshal(windows)
		if err := s.WriteTx(ctx, func(tx *store.Tx) error {
			for _, name := range pool.members {
				reserve := backends[name].Reserve
				args := []any{name, pool.command, observedAt.UnixMilli(), reserve, string(raw), errMsg}
				if _, err := tx.Exec(`INSERT INTO backend_quota_readings(backend,command,observed_at,reserve,windows,error) VALUES(?,?,?,?,?,?)`, args...); err != nil {
					return err
				}
				if _, err := tx.Exec(`INSERT INTO backend_quota(backend,command,observed_at,reserve,windows,error) VALUES(?,?,?,?,?,?) ON CONFLICT(backend) DO UPDATE SET command=excluded.command,observed_at=excluded.observed_at,reserve=excluded.reserve,windows=excluded.windows,error=excluded.error WHERE excluded.observed_at>=backend_quota.observed_at`, args...); err != nil {
					return err
				}
				if err := runhistory.AppendEffectOn(tx, QuotaUpdatedKind, "backend", name, struct{}{}, observedAt); err != nil {
					return err
				}
			}
			return nil
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
