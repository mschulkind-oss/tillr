package drain

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestSharedQuotaPoolFourBackendsProbeOnceCloseAndPauseTogether(t *testing.T) {
	ctx := context.Background()
	f, l := passFixture(t, "item-a")
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { return at }

	probeCmd := filepath.Join(f.root, "shared-quota.sh")
	probeCalls := filepath.Join(f.root, "shared-quota.calls")
	reset := at.Add(2 * time.Hour).UTC().Truncate(time.Second)
	body := fmt.Sprintf(`{"windows":[{"kind":"five_hour","fraction":0.25,"reset_at":%q}]}`, reset.Format(time.RFC3339))
	if err := writeForkSafe(probeCmd, []byte(fmt.Sprintf("#!/bin/sh\nprintf x >> %q\nprintf '%%s' '%s'\n", probeCalls, body)), 0700); err != nil {
		t.Fatal(err)
	}

	l.cfg.Policy = &Policy{
		Root:           f.root,
		DefaultBackend: "tier-1",
		Backends: map[string]Backend{
			"tier-1":      {Adapter: "/bin/true", Model: "m1", Quota: []string{probeCmd}, Reserve: 0.2},
			"tier-2":      {Adapter: "/bin/true", Model: "m2", Quota: []string{probeCmd}, Reserve: 0.2},
			"tier-3":      {Adapter: "/bin/true", Model: "m3", Quota: []string{probeCmd}, Reserve: 0.15},
			"tier-4":      {Adapter: "/bin/true", Model: "m4", Quota: []string{probeCmd}, Reserve: 0.1},
			"independent": {Adapter: "/bin/true", Model: "m5"},
		},
	}

	if err := l.refreshQuotas(ctx); err != nil {
		t.Fatalf("refreshQuotas: %v", err)
	}
	if b, _ := os.ReadFile(probeCalls); string(b) != "x" {
		t.Fatalf("four backends sharing one quota command ran probe %d times (%q), want 1", len(b), string(b))
	}

	// Stage pending live single-worker runs on tier-1 (refusing), tier-2 (same pool), and independent (other pool).
	stagePending := func(decisionID, itemID, laneID, runID, backend string) {
		t.Helper()
		d := Decision{
			DecisionID: decisionID,
			ItemID:     itemID,
			LaneID:     laneID,
			Action:     ActionDispatch,
			RunRequest: &run.Request{RunID: runID, ItemID: itemID, LaneID: laneID, Backend: backend},
		}
		fixtureWrite(t, f, func(tx *store.Tx) error {
			if _, err := tx.Exec(`INSERT OR REPLACE INTO lanes(lane_id,state,run_id) VALUES(?,?,?)`, laneID, run.LaneStateLeased, runID); err != nil {
				return err
			}
			return appendEffect(tx, dispatchStartedKind, runID, pendingWork{Decision: d}, at)
		})
	}
	stagePending("dec-1", "item-1", "lane-1", "run-refusing", "tier-1")
	stagePending("dec-2", "item-2", "lane-2", "run-peer", "tier-2")
	stagePending("dec-3", "item-3", "lane-3", "run-independent", "independent")

	report := filepath.Join(f.root, "refused.report")
	writeForkFile(t, report, fmt.Sprintf("# run report\nrate limit kind: five_hour\nfraction: 1\nstatus: rejected\nreset: %d\n---\n", reset.Unix()))
	refusingRec := run.Record{
		RunID:        "run-refusing",
		ItemID:       "item-1",
		LaneID:       "lane-1",
		Backend:      "tier-1",
		State:        run.StateExited,
		ExitCode:     code(1),
		StartedAt:    at.Format(time.RFC3339),
		FinishedAt:   at.Format(time.RFC3339),
		ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}},
	}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, refusingRec) })
	if err := l.recordBackendWindow(ctx, refusingRec); err != nil {
		t.Fatalf("recordBackendWindow: %v", err)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		windows, err := readBackendWindows(r, at)
		if err != nil {
			return err
		}
		for _, name := range []string{"tier-1", "tier-2", "tier-3", "tier-4"} {
			if _, ok := windows[name]; !ok {
				t.Errorf("backend_windows missing pool peer %s: %+v", name, windows)
			}
		}
		if _, ok := windows["independent"]; ok {
			t.Errorf("unrelated backend closed by pool refusal: %+v", windows)
		}

		shut, err := readQuotaOnlyShut(r, at, l.cfg.Policy)
		if err != nil {
			return err
		}
		for _, name := range []string{"tier-1", "tier-2", "tier-3", "tier-4"} {
			if !shut[name] {
				t.Errorf("ReadQuotaOnlyShut[%s] = false, want true", name)
			}
		}
		if shut["independent"] {
			t.Errorf("ReadQuotaOnlyShut[independent] = true, want false")
		}

		peerBackend, err := quotaPauseBackend(r, "run-peer")
		if err != nil {
			return err
		}
		if peerBackend != "tier-2" {
			t.Errorf("quota_pauses backend for run-peer = %q, want tier-2", peerBackend)
		}
		indBackend, err := quotaPauseBackend(r, "run-independent")
		if err != nil {
			return err
		}
		if indBackend != "" {
			t.Errorf("unrelated run-independent was paused with backend %q", indBackend)
		}
		return nil
	})
}

func TestInactiveProfileQuotaRowsKeptAndStillProbed(t *testing.T) {
	ctx := context.Background()
	f, _ := passFixture(t, "item-a")
	at := time.Date(2026, 11, 28, 13, 0, 0, 0, time.UTC)
	reset := at.Add(2 * time.Hour).Format(time.RFC3339)

	makeProbe := func(name string, fraction float64) (cmdPath, callsPath, payloadPath string) {
		t.Helper()
		cmdPath = filepath.Join(f.root, name+".sh")
		callsPath = filepath.Join(f.root, name+".calls")
		payloadPath = filepath.Join(f.root, name+".json")
		body := fmt.Sprintf(`{"windows":[{"kind":"five_hour","fraction":%g,"reset_at":%q}]}`, fraction, reset)
		if err := os.WriteFile(payloadPath, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
		if err := writeForkSafe(cmdPath, []byte(fmt.Sprintf("#!/bin/sh\nprintf x >> %q\ncat %q\n", callsPath, payloadPath)), 0700); err != nil {
			t.Fatal(err)
		}
		return cmdPath, callsPath, payloadPath
	}

	p0Cmd, p0Calls, p0Payload := makeProbe("primary", 0.90)
	p1Cmd, p1Calls, p1Payload := makeProbe("secondary", 0.10)

	writePolicyFile := func(name string, p Policy) string {
		t.Helper()
		path := filepath.Join(f.root, "backend-policy."+name+".json")
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}

	p0Ref := writePolicyFile("primary", Policy{
		DefaultBackend: "primary-a",
		Backends: map[string]Backend{
			"primary-a": {Adapter: "/bin/true", Model: "m1", QuotaGroup: "pool-primary", Quota: []string{p0Cmd}, Reserve: 0.2},
			"primary-b": {Adapter: "/bin/true", Model: "m2", QuotaGroup: "pool-primary", Quota: []string{p0Cmd}, Reserve: 0.1},
		},
	})
	p1Ref := writePolicyFile("secondary", Policy{
		DefaultBackend: "secondary-a",
		Backends: map[string]Backend{
			"secondary-a": {Adapter: "/bin/true", Model: "m3", Quota: []string{p1Cmd}, Reserve: 0.05},
		},
	})

	l, err := NewLoop(LoopConfig{
		Root:              f.root,
		Store:             f.storePath,
		LaneRoot:          filepath.Join(f.root, ".lanes"),
		Lanes:             []string{"lane-1"},
		PolicyRef:         p0Ref,
		PolicyProfile:     "primary",
		PolicyProfiles:    []string{"primary", "secondary"},
		PolicyProfileRefs: []string{p0Ref, p1Ref},
		Steps:             &recordingSteps{},
		Stage:             &itemContentStager{},
		Now:               func() time.Time { return at },
	})
	if err != nil {
		t.Fatalf("NewLoop: %v", err)
	}

	// Profile 0 is in force; Profile 1 (not in force) must still be probed and retained.
	if err := l.refreshQuotas(ctx); err != nil {
		t.Fatalf("refreshQuotas (index 0 active): %v", err)
	}
	if b, _ := os.ReadFile(p0Calls); string(b) != "x" {
		t.Fatalf("primary probe calls = %q, want x", b)
	}
	if b, _ := os.ReadFile(p1Calls); string(b) != "x" {
		t.Fatalf("secondary probe calls = %q, want x", b)
	}

	// Switch active profile in memory to Profile 1 (Profile 0 is now not in force).
	l.policyMu.Lock()
	l.policyState.ProfileIndex = 1
	l.cfg.Policy = l.profilePolicies[1]
	l.policyMu.Unlock()

	// Advance clock past QuotaProbeInterval, update both payloads, and refresh.
	at = at.Add(QuotaProbeInterval + time.Second)
	if err := os.WriteFile(p0Payload, []byte(fmt.Sprintf(`{"windows":[{"kind":"five_hour","fraction":0.30,"reset_at":%q}]}`, reset)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(p1Payload, []byte(fmt.Sprintf(`{"windows":[{"kind":"five_hour","fraction":0.40,"reset_at":%q}]}`, reset)), 0600); err != nil {
		t.Fatal(err)
	}
	if err := l.refreshQuotas(ctx); err != nil {
		t.Fatalf("refreshQuotas (index 1 active): %v", err)
	}
	if b, _ := os.ReadFile(p0Calls); string(b) != "xx" {
		t.Fatalf("bypassed primary probe calls = %q, want xx", b)
	}
	if b, _ := os.ReadFile(p1Calls); string(b) != "xx" {
		t.Fatalf("active secondary probe calls = %q, want xx", b)
	}

	fixtureRead(t, f, func(r *store.Reader) error {
		statuses, err := readQuotaStatuses(r)
		if err != nil {
			return err
		}
		byName := map[string]QuotaStatus{}
		for _, q := range statuses {
			byName[q.Backend] = q
		}
		for _, name := range []string{"primary-a", "primary-b"} {
			q, ok := byName[name]
			if !ok {
				t.Fatalf("bypassed profile backend %s deleted from backend_quota", name)
			}
			if len(q.Windows) != 1 || q.Windows[0].Fraction != 0.30 {
				t.Fatalf("bypassed profile backend %s windows = %+v, want fraction 0.30", name, q.Windows)
			}
		}
		if q, ok := byName["secondary-a"]; !ok || len(q.Windows) != 1 || q.Windows[0].Fraction != 0.40 {
			t.Fatalf("active profile backend secondary-a = %+v, want fraction 0.40", q)
		}
		return nil
	})
}

func TestNewLoopRejectsConflictingQuotaGroupsAcrossProfiles(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	writePolicyFile := func(name string, p Policy) string {
		t.Helper()
		path := filepath.Join(f.root, "backend-policy."+name+".json")
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(path, raw, 0600); err != nil {
			t.Fatal(err)
		}
		return path
	}
	p0Ref := writePolicyFile("p0", Policy{
		DefaultBackend: "a",
		Backends: map[string]Backend{
			"a": {Adapter: "/bin/true", Model: "m1", QuotaGroup: "shared-sub", Quota: []string{"/bin/probe-a"}},
		},
	})
	p1Ref := writePolicyFile("p1", Policy{
		DefaultBackend: "b",
		Backends: map[string]Backend{
			"b": {Adapter: "/bin/true", Model: "m2", QuotaGroup: "shared-sub", Quota: []string{"/bin/probe-b"}},
		},
	})
	_, err := NewLoop(LoopConfig{
		Root:              f.root,
		Store:             f.storePath,
		LaneRoot:          filepath.Join(f.root, ".lanes"),
		Lanes:             []string{"lane-1"},
		PolicyRef:         p0Ref,
		PolicyProfile:     "p0",
		PolicyProfiles:    []string{"p0", "p1"},
		PolicyProfileRefs: []string{p0Ref, p1Ref},
		Steps:             &recordingSteps{},
		Stage:             &itemContentStager{},
	})
	if err == nil {
		t.Fatal("NewLoop accepted conflicting quota_group commands across profiles")
	}
	if ExitCode(err) != 2 {
		t.Fatalf("NewLoop exit code = %d, want 2 (%v)", ExitCode(err), err)
	}
}

func TestProfileStatusSuffixIncludesActiveAndFallbackReset(t *testing.T) {
	resetAt := time.Date(2026, 11, 27, 20, 30, 0, 0, time.UTC)
	l := &Loop{
		policyState: PolicyState{
			Profile:      "claude-sub",
			Profiles:     []string{"claude-sub", "copilot-gemini"},
			ProfileIndex: 0,
		},
	}
	if got := l.profileStatusSuffix(); got != " profile=claude-sub" {
		t.Fatalf("preferred profileStatusSuffix = %q, want %q", got, " profile=claude-sub")
	}

	l.policyState = PolicyState{
		Profile:      "copilot-gemini",
		Profiles:     []string{"claude-sub", "copilot-gemini"},
		ProfileIndex: 1,
		SkippedProfiles: []SkippedProfileQuota{
			{Profile: "claude-sub", Backend: "claude-sonnet", ResetAt: &resetAt},
		},
	}
	want := " profile=copilot-gemini (fallback from claude-sub; resets 2026-11-27T20:30:00Z)"
	if got := l.profileStatusSuffix(); got != want {
		t.Fatalf("fallback profileStatusSuffix = %q, want %q", got, want)
	}
}
