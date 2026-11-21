package alarms

import (
	"context"
	"database/sql"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/quota"
	"github.com/mschulkind-oss/tillr/store"
)

func TestQuotaAlarmTracksReadingAtObservationTime(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(quota.DDL); err != nil {
		t.Fatal(err)
	}
	at := time.Date(2026, 7, 4, 12, 0, 0, 0, time.UTC)
	policy := &Policy{Since: at, Backends: map[string]Backend{"pi": {Quota: []string{"probe"}, Reserve: 0.1}}}
	read := func(clock time.Time, want string) {
		t.Helper()
		s, err := store.OpenRead(ctx, path)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			active, err := Read(r, clock, policy)
			if err != nil {
				return err
			}
			if want == "" {
				if len(active) != 0 {
					t.Fatalf("unexpected active alarms: %+v", active)
				}
			} else if len(active) != 1 || active[0].Kind != Window || !strings.Contains(active[0].Cause, want) || active[0].Since != "2026-07-04 08:00:00 EDT" {
				t.Fatalf("expected quota alarm %q: %+v", want, active)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	reading := func(windows, diagnostic string) {
		t.Helper()
		if _, err := db.Exec(`INSERT OR REPLACE INTO backend_quota VALUES ('pi','["probe"]',?,0.1,?,?)`, at.UnixMilli(), windows, diagnostic); err != nil {
			t.Fatal(err)
		}
	}
	read(at, "no quota reading recorded")
	reading(`[]`, "service returned 503")
	read(at, "service returned 503")
	reading(`[{"kind":"day","fraction":0.95,"reset_at":"2026-07-04T12:00:30Z"}]`, "")
	read(at, "day quota usage")
	read(at.Add(30*time.Second), "predates the provider reset")
	reading(`[{"kind":"day","fraction":0.1}]`, "")
	read(at, "")
	read(at.Add(2*time.Minute), "quota reading is stale")
}
