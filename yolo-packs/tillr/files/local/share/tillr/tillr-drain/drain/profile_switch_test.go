package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// Every test here uses fake quota readings written straight into the store and a
// fixed clock: no provider is called and nothing sleeps.

type switchFixture struct {
	f      refusalLoopFixture
	l      *Loop
	report *bytes.Buffer
	at     time.Time
	quota  []string
}

func newSwitchFixture(t *testing.T) *switchFixture {
	t.Helper()
	f, l := passFixture(t, "item-a")
	sf := &switchFixture{f: f, l: l, report: &bytes.Buffer{}, at: l.cfg.Now(), quota: []string{"quota-pref"}}
	l.cfg.Report = sf.report
	l.cfg.Root = f.root
	dir := t.TempDir()
	pref := &Policy{Root: f.root, DefaultBackend: "pref", Seats: map[string]string{"build": "pref"}, Backends: map[string]Backend{
		"pref": {Adapter: "fixture", Model: "m-pref", Quota: sf.quota, Reserve: 0.2}}}
	fall := &Policy{Root: f.root, DefaultBackend: "fall", Seats: map[string]string{"build": "fall"}, Backends: map[string]Backend{
		"fall": {Adapter: "fixture", Model: "m-fall"}}}
	refs := []string{filepath.Join(dir, "backend-policy.pref.json"), filepath.Join(dir, "backend-policy.fall.json")}
	for i, p := range []*Policy{pref, fall} {
		raw, err := json.Marshal(p)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(refs[i], raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	loaded := make([]*Policy, 2)
	var first []byte
	var hash string
	for i, ref := range refs {
		p, raw, h, err := readPolicyContent(ref)
		if err != nil {
			t.Fatal(err)
		}
		p.Root = f.root
		loaded[i] = p
		if i == 0 {
			first, hash = raw, h
		}
	}
	l.cfg.PolicyRef, l.cfg.PolicyProfileRefs, l.cfg.PolicyProfiles = refs[0], refs, []string{"pref", "fall"}
	l.cfg.Policy, l.profilePolicies = loaded[0], loaded
	l.policyState = PolicyState{Ref: refs[0], Profile: "pref", Profiles: []string{"pref", "fall"}, Hash: hash, DiskHash: hash, Content: string(first)}
	return sf
}

// reading stores a fake quota reading for the preferred profile's backend.
func (sf *switchFixture) reading(t *testing.T, observed time.Time, errMsg string, fractions ...float64) {
	t.Helper()
	kinds := []string{"five_hour", "seven_day"}
	reset := sf.at.Add(3 * time.Hour)
	var windows []QuotaWindow
	for i, f := range fractions {
		windows = append(windows, QuotaWindow{Kind: kinds[i], Fraction: f, ResetAt: &reset})
	}
	raw, _ := json.Marshal(windows)
	if windows == nil {
		raw = []byte("[]")
	}
	command, _ := json.Marshal(sf.quota)
	fixtureWrite(t, sf.f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO backend_quota(backend,command,observed_at,reserve,windows,error) VALUES('pref',?,?,0.2,?,?)
			ON CONFLICT(backend) DO UPDATE SET command=excluded.command,observed_at=excluded.observed_at,reserve=excluded.reserve,windows=excluded.windows,error=excluded.error`,
			string(command), observed.UnixMilli(), string(raw), errMsg)
		return err
	})
}

func (sf *switchFixture) switches(t *testing.T) []map[string]any {
	t.Helper()
	var out []map[string]any
	fixtureRead(t, sf.f, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind='drain.profile-switched' AND object_type='drain' AND object_id='policy' ORDER BY seq`)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var m map[string]any
			if err := json.Unmarshal(raw, &m); err != nil {
				return err
			}
			out = append(out, m)
		}
		return rows.Err()
	})
	return out
}

func (sf *switchFixture) profile() string {
	sf.l.policyMu.RLock()
	defer sf.l.policyMu.RUnlock()
	return sf.l.policyState.Profile
}

func TestProfileSwitchesAwayAndBackWithEventStateAndLines(t *testing.T) {
	ctx := context.Background()
	sf := newSwitchFixture(t)
	l := sf.l

	sf.reading(t, sf.at, "", 0.1)
	if err := l.selectProfile(ctx); err != nil {
		t.Fatal(err)
	}
	if got := sf.profile(); got != "pref" || len(sf.switches(t)) != 0 {
		t.Fatalf("open quota: profile %q with %d switch events, want pref and none", got, len(sf.switches(t)))
	}

	sf.reading(t, sf.at, "", 0.85)
	if err := l.selectProfile(ctx); err != nil {
		t.Fatal(err)
	}
	if sf.profile() != "fall" || l.cfg.Policy.DefaultBackend != "fall" {
		t.Fatalf("after closure: profile %q default %q, want fall", sf.profile(), l.cfg.Policy.DefaultBackend)
	}
	events := sf.switches(t)
	if len(events) != 1 {
		t.Fatalf("switch events = %d, want 1: %v", len(events), events)
	}
	ev := events[0]
	if ev["from_profile"] != "pref" || ev["to_profile"] != "fall" || ev["cause"] != "quota-closed" || ev["trigger_backend"] != "pref" || ev["from_index"] != float64(0) || ev["to_index"] != float64(1) {
		t.Fatalf("switch-away event = %v", ev)
	}
	reading, _ := ev["reading"].(map[string]any)
	if reading["source"] != "probe" || reading["reserve"] != 0.2 {
		t.Fatalf("event reading = %v", reading)
	}
	if !strings.Contains(sf.report.String(), "loop: profile switched pref -> fall (2/2): pref five_hour used 0.85 >= 0.80 (reserve 0.20; resets 2026-11-15T03:00:00Z)") {
		t.Fatalf("switch line missing:\n%s", sf.report.String())
	}

	state, err := ReadPolicyState(sf.f.root)
	if err != nil {
		t.Fatal(err)
	}
	if state.Profile != "fall" || state.ProfileIndex != 1 || state.SwitchedFrom != "pref" || state.SwitchedAt == nil || !strings.Contains(state.Why, "switched from pref") || state.Ref != l.cfg.PolicyProfileRefs[1] {
		t.Fatalf("recorded state = %+v", state)
	}
	if len(state.SkippedProfiles) != 1 || state.SkippedProfiles[0].Profile != "pref" || state.SkippedProfiles[0].ReopenThreshold != 0 {
		t.Fatalf("skipped profiles = %+v", state.SkippedProfiles)
	}

	// A pass-boundary reload must keep the profile in force, not reread the first profile.
	if err := l.refreshPolicy(); err != nil {
		t.Fatal(err)
	}
	if sf.profile() != "fall" || l.cfg.Policy.DefaultBackend != "fall" {
		t.Fatalf("reload put %q (%s) back in force", sf.profile(), l.cfg.Policy.DefaultBackend)
	}

	// A closed secondary window keeps the fallback in force.
	sf.reading(t, sf.at, "", 0.0, 0.82)
	if err := l.selectProfile(ctx); err != nil {
		t.Fatal(err)
	}
	if sf.profile() != "fall" || len(sf.switches(t)) != 1 {
		t.Fatalf("seven_day at 0.82 switched back: profile %q", sf.profile())
	}

	// Every window below its line switches back on that probe, with no timer.
	sf.reading(t, sf.at, "", 0.0, 0.34)
	if err := l.selectProfile(ctx); err != nil {
		t.Fatal(err)
	}
	if sf.profile() != "pref" || l.cfg.Policy.DefaultBackend != "pref" {
		t.Fatalf("after reopen: profile %q", sf.profile())
	}
	events = sf.switches(t)
	if len(events) != 2 || events[1]["cause"] != "quota-reopened" || events[1]["from_profile"] != "fall" || events[1]["to_profile"] != "pref" {
		t.Fatalf("switch events = %v", events)
	}
	if !strings.Contains(sf.report.String(), "loop: profile switched fall -> pref (1/2): pref reopened (five_hour used 0.00 < 0.80, seven_day used 0.34 < 0.80; reserve 0.20)") {
		t.Fatalf("switch-back line missing:\n%s", sf.report.String())
	}
	if state, err = ReadPolicyState(sf.f.root); err != nil || state.Profile != "pref" || state.ProfileIndex != 0 || len(state.SkippedProfiles) != 0 {
		t.Fatalf("state after switch-back = %+v, %v", state, err)
	}
}

func TestProfileDoesNotSwitchOnABrokenOrStaleQuotaOrAFailedRun(t *testing.T) {
	ctx := context.Background()
	sf := newSwitchFixture(t)
	l := sf.l

	sf.reading(t, sf.at, "quota command failed: exit 1")
	if err := l.selectProfile(ctx); err != nil {
		t.Fatal(err)
	}
	sf.reading(t, sf.at.Add(-time.Hour), "", 0.99)
	if err := l.selectProfile(ctx); err != nil {
		t.Fatal(err)
	}

	// A run that crashed with no rate-limit report records no refusal window.
	crash := run.Record{RunID: "run-crash", ItemID: "item-a", LaneID: "lane-1", Backend: "pref", State: run.StateExited, ExitCode: code(1),
		StartedAt: sf.at.Format(time.RFC3339), FinishedAt: sf.at.Format(time.RFC3339)}
	if err := l.recordBackendWindow(ctx, crash); err != nil {
		t.Fatal(err)
	}
	if sf.profile() != "pref" || len(sf.switches(t)) != 0 {
		t.Fatalf("profile %q with %d switch events, want pref and none", sf.profile(), len(sf.switches(t)))
	}
}

func TestRecordedRefusalSwitchesAtOnceAndExpiryDoesNotSwitchBackBeforeAFreshReading(t *testing.T) {
	ctx := context.Background()
	sf := newSwitchFixture(t)
	l := sf.l
	sf.reading(t, sf.at, "", 0.1)

	reset := sf.at.Add(2 * time.Hour).Truncate(time.Second)
	report := filepath.Join(sf.f.root, "refused.report")
	writeForkFile(t, report, fmt.Sprintf("# run report\nrate limit kind: five_hour\nfraction: 1\nstatus: rejected\nreset: %d\n---\n", reset.Unix()))
	refused := run.Record{RunID: "run-refused", ItemID: "item-a", LaneID: "lane-1", Backend: "pref", State: run.StateExited, ExitCode: code(1),
		StartedAt: sf.at.Format(time.RFC3339), FinishedAt: sf.at.Format(time.RFC3339), ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: report}}}
	fixtureWrite(t, sf.f, func(tx *store.Tx) error { return applyTerminal(tx, refused) })
	if err := l.recordBackendWindow(ctx, refused); err != nil {
		t.Fatal(err)
	}
	if sf.profile() != "fall" {
		t.Fatalf("a recorded refusal left profile %q in force", sf.profile())
	}
	events := sf.switches(t)
	if len(events) != 1 || events[0]["cause"] != "quota-closed" {
		t.Fatalf("events = %v", events)
	}
	if r, _ := events[0]["reading"].(map[string]any); r["source"] != "refusal" {
		t.Fatalf("refusal event reading = %v", r)
	}

	// The wall clock passing the reset is not a reading: the stored reading is
	// from before the reset, so it is stale and the fallback stays in force.
	l.cfg.Now = func() time.Time { return reset.Add(time.Minute) }
	if err := l.selectProfile(ctx); err != nil {
		t.Fatal(err)
	}
	if sf.profile() != "fall" {
		t.Fatalf("clock alone switched back to %q", sf.profile())
	}
	// A fresh error-free reading below the line switches back immediately.
	sf.at = l.cfg.Now()
	sf.reading(t, sf.at, "", 0.0)
	if err := l.selectProfile(ctx); err != nil {
		t.Fatal(err)
	}
	if sf.profile() != "pref" {
		t.Fatalf("a fresh reading below the line left %q in force", sf.profile())
	}
}

func TestProfileSelectionNeverSwitchesWithOneProfile(t *testing.T) {
	f, l := passFixture(t, "item-a")
	_ = f
	if err := l.selectProfile(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestSwitchedProfileGivesAWorkerSessionSeatChangedOnItsRetainedLane(t *testing.T) {
	sf := newSwitchFixture(t)
	dir := scratchUnderDurable(t)
	id := "22222222-2222-4222-8222-222222222222"
	writeForkFile(t, filepath.Join(dir, SessionIDFile), id+"\n")
	adapter := filepath.Join(dir, "adapter")
	writeForkFile(t, adapter, "#!/bin/sh\n")
	writeForkFile(t, filepath.Join(dir, "pause-kind"), run.PauseAtGap+"\n")
	preferred := sf.l.cfg.Policy.Request("item-a", "lane-1", "run-1", "pref")
	preferred.Command = []string{adapter}
	worker := &WorkerSession{SessionID: id, LaneID: "lane-1", LaneDir: dir, Backend: preferred.Backend, Route: preferred.Route()}
	if got := workerSessionReason(worker, preferred); got != "resumed-worker" {
		t.Fatalf("on the preferred profile: %s", got)
	}
	sf.l.policyMu.Lock()
	sf.l.cfg.Policy = sf.l.profilePolicies[1]
	sf.l.policyMu.Unlock()
	fallback := sf.l.cfg.Policy.Request("item-a", "lane-1", "run-2", "fall")
	fallback.Command = []string{adapter}
	if got := workerSessionReason(worker, fallback); got != "seat-changed" {
		t.Fatalf("on the fallback profile: %s, want seat-changed (no cross-backend resume)", got)
	}
}

// The pass-top quota refresh is what drives selection: a fake quota command
// that reports a closed window switches, and one that reports an open window
// switches back, with the fixed clock moved only to age the cached reading.
func TestPassQuotaRefreshDrivesTheProfileSwitch(t *testing.T) {
	ctx := context.Background()
	sf := newSwitchFixture(t)
	l := sf.l
	script := filepath.Join(sf.f.root, "fake-quota.sh")
	body := filepath.Join(sf.f.root, "fake-quota.json")
	writeQuota := func(fraction float64) {
		reset := l.cfg.Now().Add(3 * time.Hour).UTC().Format(time.RFC3339)
		if err := os.WriteFile(body, []byte(fmt.Sprintf(`{"windows":[{"kind":"five_hour","fraction":%v,"reset_at":%q}]}`, fraction, reset)), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	if err := writeForkSafe(script, []byte("#!/bin/sh\ncat "+body+"\n"), 0o700); err != nil {
		t.Fatal(err)
	}
	for _, p := range l.profilePolicies {
		if b, ok := p.Backends["pref"]; ok {
			b.Quota = []string{script}
			p.Backends["pref"] = b
		}
	}
	l.cfg.Policy = l.profilePolicies[0]

	writeQuota(0.9)
	if err := l.refreshQuotas(ctx); err != nil {
		t.Fatal(err)
	}
	if sf.profile() != "fall" {
		t.Fatalf("closed fake reading left %q in force:\n%s", sf.profile(), sf.report.String())
	}
	now := l.cfg.Now().Add(2 * QuotaProbeInterval)
	l.cfg.Now = func() time.Time { return now }
	writeQuota(0.1)
	if err := l.refreshQuotas(ctx); err != nil {
		t.Fatal(err)
	}
	if sf.profile() != "pref" {
		t.Fatalf("open fake reading left %q in force:\n%s", sf.profile(), sf.report.String())
	}
	if n := len(sf.switches(t)); n != 2 {
		t.Fatalf("switch events = %d, want 2", n)
	}
}

// An infrastructure closure on the fallback's backend (backend_outcomes) keeps
// the closed preferred profile in force instead of moving dispatch onto it, and
// an infrastructure closure on the profile in force never switches it.
func TestProfileNeverSwitchesOntoAnInfrastructureClosedProfile(t *testing.T) {
	ctx := context.Background()
	sf := newSwitchFixture(t)
	l := sf.l
	backendRun(t, sf.f, "fall-broken", "fall", 2)
	sf.reading(t, sf.at, "", 0.9)
	if err := l.selectProfile(ctx); err != nil {
		t.Fatal(err)
	}
	if sf.profile() != "pref" || len(sf.switches(t)) != 0 {
		t.Fatalf("switched onto an infrastructure-closed fallback: profile %q, %d events", sf.profile(), len(sf.switches(t)))
	}
	backendRun(t, sf.f, "pref-broken", "pref", 2)
	sf.reading(t, sf.at, "", 0.1)
	if err := l.selectProfile(ctx); err != nil {
		t.Fatal(err)
	}
	if sf.profile() != "pref" || len(sf.switches(t)) != 0 {
		t.Fatalf("an infrastructure failure switched the profile in force to %q", sf.profile())
	}
}

// NewLoop (loop startup and --check) evaluates the stored readings: a quota-
// closed preferred profile starts on the fallback as startup state, with the
// startup line naming it and no drain.profile-switched event recorded.
func TestNewLoopChoosesTheStartingProfileFromStoredReadingsWithoutASwitchEvent(t *testing.T) {
	sf := newSwitchFixture(t)
	sf.reading(t, sf.at, "", 0.9)
	cfg := sf.l.cfg
	cfg.Policy = nil
	started, err := NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	line := started.ProfileStartLine()
	if !strings.Contains(line, "loop: profile fall (2/2 of [pref, fall]") || !strings.Contains(line, "fallback profile (2/2) at startup: pref five_hour used 0.90 >= 0.80") {
		t.Fatalf("startup line = %q", line)
	}
	if started.cfg.Policy.DefaultBackend != "fall" || started.policyState.ProfileIndex != 1 || started.policyState.Ref != cfg.PolicyProfileRefs[1] || started.policyState.SwitchedFrom != "" {
		t.Fatalf("startup state = %+v (default %q)", started.policyState, started.cfg.Policy.DefaultBackend)
	}
	if n := len(sf.switches(t)); n != 0 {
		t.Fatalf("startup recorded %d drain.profile-switched events", n)
	}

	sf.reading(t, sf.at, "", 0.1)
	started, err = NewLoop(cfg)
	if err != nil {
		t.Fatal(err)
	}
	if line := started.ProfileStartLine(); !strings.Contains(line, "loop: profile pref (1/2 of [pref, fall]") || !strings.Contains(line, "preferred profile (1/2); all quota windows open") {
		t.Fatalf("open preferred startup line = %q", line)
	}
}
