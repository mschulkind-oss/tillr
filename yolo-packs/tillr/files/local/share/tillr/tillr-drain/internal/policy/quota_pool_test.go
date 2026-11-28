package policy

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr/store"
)

func TestQuotaPoolKeyAndPeers(t *testing.T) {
	sharedCmd := []string{"/usr/bin/probe-sub"}
	otherCmd := []string{"/usr/bin/probe-sub", "--account", "b"}
	p0 := &Policy{
		Backends: map[string]Backend{
			"tier-1":   {Quota: sharedCmd},
			"tier-2":   {Quota: sharedCmd},
			"explicit": {QuotaGroup: "sub-x", Quota: otherCmd},
			"bare":     {},
		},
	}
	p1 := &Policy{
		Backends: map[string]Backend{
			"tier-3":      {Quota: sharedCmd},
			"explicit-b":  {QuotaGroup: "sub-x"},
			"independent": {Quota: otherCmd},
		},
	}
	if got := QuotaPoolKey("explicit", p0.Backends["explicit"]); got != "group:sub-x" {
		t.Fatalf("explicit quota_group key = %q, want group:sub-x", got)
	}
	if got := QuotaPoolKey("tier-1", p0.Backends["tier-1"]); got != `cmd:["/usr/bin/probe-sub"]` {
		t.Fatalf("implicit cmd key = %q", got)
	}
	if got := QuotaPoolKey("bare", p0.Backends["bare"]); got != "backend:bare" {
		t.Fatalf("bare key = %q, want backend:bare", got)
	}
	if got := QuotaPoolKey("tier-1", p0.Backends["tier-1"]); got == QuotaPoolKey("independent", p1.Backends["independent"]) {
		t.Fatalf("distinct quota commands collided on key %q", got)
	}

	if got, want := QuotaPoolPeers("tier-1", p0, p1), []string{"tier-1", "tier-2", "tier-3"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("QuotaPoolPeers(tier-1) = %v, want %v", got, want)
	}
	if got, want := QuotaPoolPeers("explicit", p0, p1), []string{"explicit", "explicit-b"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("QuotaPoolPeers(explicit) = %v, want %v", got, want)
	}
	if got, want := QuotaPoolPeers("unknown", p0, p1), []string{"unknown"}; !reflect.DeepEqual(got, want) {
		t.Fatalf("QuotaPoolPeers(unknown) = %v, want %v", got, want)
	}
}

func TestCheckQuotaGroupsRefusesConflictingCommands(t *testing.T) {
	base := func(group string, quota []string) Backend {
		return Backend{Adapter: "/bin/true", Model: "m", QuotaGroup: group, Quota: quota}
	}
	samePolicyOK := Policy{
		DefaultBackend: "a",
		Backends: map[string]Backend{
			"a": base("pool-a", []string{"/bin/probe", "--a"}),
			"b": base("pool-a", []string{"/bin/probe", "--a"}),
			"c": base("pool-a", nil),
		},
	}
	if err := samePolicyOK.Check(); err != nil {
		t.Fatalf("valid shared quota_group rejected: %v", err)
	}

	samePolicyConflict := Policy{
		DefaultBackend: "a",
		Backends: map[string]Backend{
			"a": base("pool-a", []string{"/bin/probe", "--a"}),
			"b": base("pool-a", []string{"/bin/probe", "--b"}),
		},
	}
	if err := samePolicyConflict.Check(); err == nil || !strings.Contains(err.Error(), "conflicting quota commands") {
		t.Fatalf("conflicting quota_group in one policy = %v, want conflicting quota commands error", err)
	}

	p0 := &Policy{Backends: map[string]Backend{"a": base("pool-a", []string{"/bin/probe", "--a"})}}
	p1 := &Policy{Backends: map[string]Backend{"b": base("pool-a", []string{"/bin/probe", "--b"})}}
	if err := CheckQuotaGroups(p0, p1); err == nil || !strings.Contains(err.Error(), "conflicting quota commands") {
		t.Fatalf("conflicting quota_group across profiles = %v, want conflicting quota commands error", err)
	}
}

func TestRefreshQuotasSharedPoolProbesOnceAndRetainsInactiveProfiles(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	storePath := filepath.Join(dir, "state.db")
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(BackendQuotaDDL); err != nil {
			return err
		}
		if _, err := tx.Exec(storelog.BackendWindowDDL); err != nil {
			return err
		}
		// Seed an obsolete row absent from all profiles to verify cleanup still removes orphans.
		_, err := tx.Exec(`INSERT INTO backend_quota(backend,command,observed_at,reserve,windows,error) VALUES('orphan','["old"]',1,0,'[]','')`)
		return err
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()

	at := time.Date(2026, 11, 28, 12, 0, 0, 0, time.UTC)
	now := func() time.Time { return at }
	reset := at.Add(2 * time.Hour).Format(time.RFC3339)

	makeProbe := func(name string, fraction float64) (cmdPath, callsPath, payloadPath string) {
		t.Helper()
		cmdPath = filepath.Join(dir, name+".sh")
		callsPath = filepath.Join(dir, name+".calls")
		payloadPath = filepath.Join(dir, name+".json")
		body := fmt.Sprintf(`{"windows":[{"kind":"five_hour","fraction":%g,"reset_at":%q}]}`, fraction, reset)
		if err := os.WriteFile(payloadPath, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		script := fmt.Sprintf("#!/bin/sh\nprintf x >> %q\ncat %q\n", callsPath, payloadPath)
		if err := os.WriteFile(cmdPath, []byte(script), 0700); err != nil {
			t.Fatal(err)
		}
		return cmdPath, callsPath, payloadPath
	}

	p0Cmd, p0Calls, p0Payload := makeProbe("p0", 0.85)
	p1Cmd, p1Calls, _ := makeProbe("p1", 0.10)

	p0 := &Policy{
		DefaultBackend: "sub-a",
		Backends: map[string]Backend{
			"sub-a": {Adapter: "/bin/true", Model: "m1", Quota: []string{p0Cmd}, Reserve: 0.2},
			"sub-b": {Adapter: "/bin/true", Model: "m2", Quota: []string{p0Cmd}, Reserve: 0.2},
			"sub-c": {Adapter: "/bin/true", Model: "m3", Quota: []string{p0Cmd}, Reserve: 0.15},
			"sub-d": {Adapter: "/bin/true", Model: "m4", Quota: []string{p0Cmd}, Reserve: 0.1},
		},
	}
	p1 := &Policy{
		DefaultBackend: "other-sub",
		Backends: map[string]Backend{
			"other-sub": {Adapter: "/bin/true", Model: "m5", Quota: []string{p1Cmd}, Reserve: 0.05},
			"no-quota":  {Adapter: "/bin/true", Model: "m6"},
		},
	}

	var mu sync.Mutex
	// Profile 1 is in force (activeIndex = 1); Profile 0 is not in force.
	if err := RefreshQuotas(ctx, dir, storePath, &mu, []*Policy{p0, p1}, 1, now, func(string, ...any) {}); err != nil {
		t.Fatalf("RefreshQuotas initial: %v", err)
	}
	if b, _ := os.ReadFile(p0Calls); string(b) != "x" {
		t.Fatalf("four backends sharing one quota command ran probe %d times (%q), want 1", len(b), string(b))
	}
	if b, _ := os.ReadFile(p1Calls); string(b) != "x" {
		t.Fatalf("p1 probe ran %d times (%q), want 1", len(b), string(b))
	}

	readMap := func() map[string]QuotaStatus {
		t.Helper()
		r, err := store.OpenRead(ctx, storePath)
		if err != nil {
			t.Fatal(err)
		}
		defer r.Close()
		var statuses []QuotaStatus
		if err := r.ReadTx(ctx, func(rd *store.Reader) error {
			var e error
			statuses, e = ReadQuotaStatuses(rd)
			return e
		}); err != nil {
			t.Fatal(err)
		}
		out := map[string]QuotaStatus{}
		for _, q := range statuses {
			out[q.Backend] = q
		}
		return out
	}

	got := readMap()
	if _, orphanExists := got["orphan"]; orphanExists {
		t.Fatal("orphan backend_quota row was not deleted")
	}
	wantReserves := map[string]float64{"sub-a": 0.2, "sub-b": 0.2, "sub-c": 0.15, "sub-d": 0.1, "other-sub": 0.05}
	for name, wantReserve := range wantReserves {
		q, ok := got[name]
		if !ok {
			t.Fatalf("missing backend_quota row for %s", name)
		}
		if q.Reserve != wantReserve {
			t.Fatalf("%s reserve = %v, want %v", name, q.Reserve, wantReserve)
		}
		if name != "other-sub" && (len(q.Windows) != 1 || q.Windows[0].Fraction != 0.85) {
			t.Fatalf("%s windows = %+v, want fraction 0.85", name, q.Windows)
		}
	}

	// Advance past probe interval and update p0's reading while p1 stays in force.
	at = at.Add(QuotaProbeInterval + time.Second)
	updatedBody := fmt.Sprintf(`{"windows":[{"kind":"five_hour","fraction":0.25,"reset_at":%q}]}`, reset)
	if err := os.WriteFile(p0Payload, []byte(updatedBody), 0600); err != nil {
		t.Fatal(err)
	}
	if err := RefreshQuotas(ctx, dir, storePath, &mu, []*Policy{p0, p1}, 1, now, func(string, ...any) {}); err != nil {
		t.Fatalf("RefreshQuotas after interval: %v", err)
	}
	if b, _ := os.ReadFile(p0Calls); string(b) != "xx" {
		t.Fatalf("inactive profile pool probe count = %d (%q), want 2", len(b), string(b))
	}
	got = readMap()
	for _, name := range []string{"sub-a", "sub-b", "sub-c", "sub-d"} {
		q, ok := got[name]
		if !ok || len(q.Windows) != 1 || q.Windows[0].Fraction != 0.25 {
			t.Fatalf("inactive profile backend %s after re-probe = %+v, want fraction 0.25", name, q)
		}
	}
}
