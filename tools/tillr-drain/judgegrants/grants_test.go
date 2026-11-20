package judgegrants

import (
	"context"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestCurrentAndExpiredForItem(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "state.db")
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	issued := time.Date(2026, 11, 18, 23, 8, 0, 0, time.UTC)
	expires := issued.Add(24 * time.Hour) // 2026-11-19T23:08:00Z

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		for _, q := range []string{
			`CREATE TABLE item_grants (grant_id TEXT PRIMARY KEY, claim_classes TEXT NOT NULL, grantor TEXT NOT NULL, issued_at INTEGER NOT NULL, expires_at INTEGER NOT NULL)`,
			`CREATE TABLE item_criteria (item_id TEXT NOT NULL, criterion_id TEXT NOT NULL, text TEXT NOT NULL, state TEXT NOT NULL, claim_class TEXT NOT NULL)`,
			`INSERT INTO item_criteria VALUES ('item-verif', 'c1', 'check output', '', 'verification-only')`,
			`INSERT INTO item_criteria VALUES ('item-dir', 'c1', 'pick direction', '', 'direction')`,
		} {
			if _, err := tx.Exec(q); err != nil {
				return err
			}
		}
		_, err := tx.Exec(
			`INSERT INTO item_grants VALUES ('grant-older', 'verification-only', 'owner', ?, ?), ('grant-24h', 'verification-only', 'owner', ?, ?)`,
			issued.Add(-48*time.Hour).UnixMilli(), issued.Add(-24*time.Hour).UnixMilli(),
			issued.UnixMilli(), expires.UnixMilli(),
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	// 1. Healthy (> 1h before expiry): active, no warning, older expired grant superseded.
	twoHoursBefore := expires.Add(-2 * time.Hour)
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		views, err := CurrentViews(r, twoHoursBefore, path)
		if err != nil {
			return err
		}
		if len(views) != 1 || views[0].GrantID != "grant-24h" || views[0].State != "active" || views[0].Warning {
			t.Fatalf("2h before expiry: %+v", views)
		}
		if !strings.Contains(views[0].Summary, "2026-11-19T23:08:00Z") || strings.HasPrefix(views[0].Summary, "warning:") {
			t.Fatalf("unexpected warning 2h before expiry: %s", views[0].Summary)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 2. Within 1h of expiry (from 1h down to 1m before lapsing): warning!
	for _, offset := range []time.Duration{time.Hour, 45 * time.Minute, time.Minute} {
		at := expires.Add(-offset)
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			views, err := CurrentViews(r, at, path)
			if err != nil {
				return err
			}
			if len(views) != 1 || views[0].GrantID != "grant-24h" || views[0].State != "expiring" || !views[0].Warning {
				t.Fatalf("at -%s: %+v", offset, views)
			}
			for _, want := range []string{"warning:", "grant-24h", "2026-11-19T23:08:00Z", "tillr item grant issue --store " + path} {
				if !strings.Contains(views[0].Summary, want) {
					t.Errorf("summary %q missing %q", views[0].Summary, want)
				}
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}

	// 3. After expiry: most recent expired grant reported; item-verif matches grant-24h, item-dir does not.
	after := expires.Add(5 * time.Minute)
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		views, err := CurrentViews(r, after, path)
		if err != nil {
			return err
		}
		if len(views) != 1 || views[0].GrantID != "grant-24h" || views[0].State != "expired" || !views[0].Warning {
			t.Fatalf("after expiry: %+v", views)
		}
		exp, err := ExpiredForItem(r, "item-verif", after)
		if err != nil || exp == nil || exp.GrantID != "grant-24h" {
			t.Fatalf("expired covering grant = %+v, err=%v", exp, err)
		}
		detail, mover := FormatHold(exp, path)
		for _, text := range []string{detail, mover} {
			for _, want := range []string{"grant-24h", "expired at 2026-11-19T23:08:00Z", "tillr item grant issue --store " + path} {
				if !strings.Contains(text, want) {
					t.Errorf("hold text %q missing %q", text, want)
				}
			}
		}
		none, err := ExpiredForItem(r, "item-dir", after)
		if err != nil || none != nil {
			t.Fatalf("uncovered item matched %+v, err=%v", none, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	// 4. Replacement grant issued: clears expired state and warning.
	renewed := after.Add(24 * time.Hour)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO item_grants VALUES ('grant-renewed', 'verification-only', 'owner', ?, ?)`, after.UnixMilli(), renewed.UnixMilli())
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		views, err := CurrentViews(r, after.Add(time.Minute), path)
		if err != nil {
			return err
		}
		if len(views) != 1 || views[0].GrantID != "grant-renewed" || views[0].State != "active" || views[0].Warning {
			t.Fatalf("after renewal: %+v", views)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
