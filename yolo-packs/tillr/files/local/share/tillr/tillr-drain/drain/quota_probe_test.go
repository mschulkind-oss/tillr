package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func fixtureQuotaCommand() []string {
	return []string{"/bin/sh", "-c", `printf '%s' '{"windows":[{"kind":"five_hour","fraction":0,"reset_at":null}]}'`}
}

func enableFixtureQuota(l *Loop, name string) {
	b := l.cfg.Policy.Backends[name]
	b.Quota = fixtureQuotaCommand()
	l.cfg.Policy.Backends[name] = b
}

// Holding another writer proves the cached path needs only a reader, not a
// migration, cleanup transaction, or unchanged reserve update.
func TestQuotaCachedRefreshNeedsNoWriter(t *testing.T) {
	for _, configured := range []bool{false, true} {
		t.Run(fmt.Sprint(configured), func(t *testing.T) {
			f, l := passFixture(t, "item-a")
			if configured {
				enableFixtureQuota(l, "fixture")
			}
			if err := l.refreshQuotas(context.Background()); err != nil {
				t.Fatal(err)
			}
			s, err := store.Open(context.Background(), f.storePath, true)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
				ctx, cancel := context.WithTimeout(context.Background(), hangGuard)
				defer cancel()
				return l.refreshQuotas(ctx)
			}); err != nil {
				t.Fatalf("unchanged quota refresh requires writer: %v", err)
			}
		})
	}
}

func TestQuotaProbeReserveAndFreshness(t *testing.T) {
	f, l := passFixture(t, "item-a")
	at := l.cfg.Now()
	l.cfg.Now = func() time.Time { return at }
	command := filepath.Join(f.root, "quota")
	calls := filepath.Join(f.root, "quota-calls")
	payload := filepath.Join(f.root, "quota.json")
	reset := at.Add(time.Hour).UTC().Format(time.RFC3339)
	write := func(fraction float64) {
		t.Helper()
		if err := os.WriteFile(payload, []byte(fmt.Sprintf(`{"windows":[{"kind":"five_hour","fraction":%g,"reset_at":%q},{"kind":"seven_day","fraction":0.3,"reset_at":%q}]}`, fraction, reset, reset)), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(.8)
	if err := writeForkSafe(command, []byte(fmt.Sprintf("#!/bin/sh\nprintf x >> %q\ncat %q\n", calls, payload)), 0700); err != nil {
		t.Fatal(err)
	}
	backend := l.cfg.Policy.Backends["fixture"]
	backend.Quota, backend.Reserve = []string{command}, .2
	l.cfg.Policy.Backends["fixture"] = backend
	if got := mustPlan(t, l)[0].decision; got.Action != ActionHold || got.Reason != ReasonBackendWindow {
		t.Fatalf("reserve admitted: %+v", got)
	}
	mustPlan(t, l)
	restarted := f.loop(t, &recordingSteps{}, &itemContentStager{})
	restarted.cfg.Policy = l.cfg.Policy
	restarted.cfg.Now = l.cfg.Now
	if got := mustPlan(t, restarted)[0].decision; got.Action != ActionHold {
		t.Fatalf("restart lost quota: %+v", got)
	}
	if b, _ := os.ReadFile(calls); string(b) != "x" {
		t.Fatalf("fresh reading probed again: %q", b)
	}
	at = at.Add(QuotaProbeInterval + time.Second)
	write(.79)
	if got := mustPlan(t, l)[0].decision; got.Action != ActionDispatch {
		t.Fatalf("below reserve held: %+v", got)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		var n int
		if err := tx.QueryRow(`SELECT count(*) FROM backend_quota_readings`).Scan(&n); err != nil {
			return err
		}
		if n != 2 {
			t.Errorf("readings=%d", n)
		}
		return nil
	})
	// Changing the reserve takes effect without a new HTTP reading.
	backend.Reserve = .3
	l.cfg.Policy.Backends["fixture"] = backend
	if got := mustPlan(t, l)[0].decision; got.Action != ActionHold {
		t.Fatalf("new reserve ignored: %+v", got)
	}
	// Removing the command ignores readings and reserve, but not a refusal.
	backend.Quota = nil
	l.cfg.Policy.Backends["fixture"] = backend
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO backend_windows VALUES ('fixture','weekly',1,'rejected',?,'refused')`, at.Add(time.Hour).UnixMilli())
		return err
	})
	if got := mustPlan(t, l)[0].decision; got.Reason != ReasonBackendWindow {
		t.Fatalf("unprobed backend ignored refusal: %+v", got)
	}
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`DELETE FROM backend_windows`)
		return err
	})
	if got := mustPlan(t, l)[0].decision; got.Action != ActionDispatch {
		t.Fatalf("unprobed backend held by reserve/reading: %+v", got)
	}
	if b, _ := os.ReadFile(calls); string(b) != "xx" {
		t.Fatalf("absent command probed: %q", b)
	}
}

func TestQuotaProbeFailureClosesOnlyItsBackend(t *testing.T) {
	f, l := passFixture(t, "item-a")
	b := l.cfg.Policy.Backends["fixture"]
	b.Quota = []string{"/bin/sh", "-c", "printf bad"}
	l.cfg.Policy.Backends["fixture"] = b
	if got := mustPlan(t, l)[0].decision; got.Action != ActionHold || got.Reason != ReasonBackendWindow {
		t.Fatalf("failed probe admitted: %+v", got)
	}
	s, err := store.OpenRead(context.Background(), f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var message string
		err := r.QueryRow(`SELECT error FROM backend_quota`).Scan(&message)
		if !strings.Contains(message, "invalid quota") {
			t.Errorf("error=%q", message)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}

func TestQuotaProbeHoldsDefaultOnEitherWindowAndError(t *testing.T) {
	for _, body := range []string{`{"windows":[{"kind":"five_hour","fraction":0.1,"reset_at":"2026-11-16T00:00:00Z"},{"kind":"seven_day","fraction":0.8,"reset_at":"2026-11-22T00:00:00Z"}]}`, `bad`} {
		t.Run(body, func(t *testing.T) {
			_, l := passFixture(t, "item-a")
			first := l.cfg.Policy.Backends["fixture"]
			first.Quota = []string{"/bin/sh", "-c", "printf '%s' '" + body + "'"}
			first.Reserve = .2
			l.cfg.Policy.Backends["fixture"] = first
			l.cfg.Policy.Backends["other"] = Backend{Adapter: "fixture"}
			if got := mustPlan(t, l)[0].decision; got.Action != ActionHold || got.Reason != ReasonBackendWindow || got.DeferredBy != "fixture" {
				t.Fatalf("did not hold on the default: %+v", got)
			}
		})
	}
}

func TestQuotaFreshInvalidatesResetAndChangedCommand(t *testing.T) {
	at := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	reset := at.Add(10 * time.Second)
	q := QuotaStatus{ObservedAt: at, Command: "a", Windows: []QuotaWindow{{Kind: "five_hour", Fraction: .1, ResetAt: &reset}}}
	if !quotaFresh(q, "a", at.Add(9*time.Second)) || quotaFresh(q, "a", reset) || quotaFresh(q, "b", at) {
		t.Fatal("freshness ignored reset/command")
	}
	q.Windows = nil
	if !quotaFresh(q, "a", at.Add(QuotaProbeInterval)) || quotaFresh(q, "a", at.Add(QuotaProbeInterval+time.Millisecond)) {
		t.Fatal("freshness interval boundary")
	}
	// The standalone planner's second-rounded clock can read a millisecond observation.
	q.ObservedAt = at.Add(900 * time.Millisecond)
	if !quotaFresh(q, "a", at) {
		t.Fatal("same-second reading became future/stale")
	}
}

func TestQuotaDocumentValidation(t *testing.T) {
	at := time.Date(2026, 11, 17, 0, 0, 0, 0, time.UTC)
	for _, raw := range []string{`{}`, `{"windows":[]}`, `{"windows":[{"kind":"five_hour","fraction":-1,"reset_at":"2026-11-18T00:00:00Z"}]}`, `{"windows":[{"kind":"five_hour","fraction":0.8}]}`, `{"windows":[{"kind":"five_hour","reset_at":"2026-11-18T00:00:00Z"}]}`} {
		if _, err := decodeQuota([]byte(raw), at); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}
