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

func TestInfrastructureAndReadHoldsScopedToOpenItems(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "alarms-scoped.db")
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

	for _, ddl := range []string{
		`CREATE TABLE backend_outcomes (terminal_seq INTEGER PRIMARY KEY, class TEXT NOT NULL, closes INTEGER NOT NULL)`,
		`CREATE TABLE items (item_id TEXT PRIMARY KEY, state TEXT NOT NULL)`,
		`CREATE TABLE dispatches (item_id TEXT NOT NULL, lane_id TEXT, run_id TEXT, seat TEXT, action TEXT NOT NULL, reason TEXT NOT NULL, deferred_by TEXT, dispatched_at INTEGER NOT NULL)`,
	} {
		if _, err := db.Exec(ddl); err != nil {
			t.Fatal(err)
		}
	}

	// Backend "alpha": closed with no superseding event -> alarm active.
	if _, err := db.Exec(`INSERT INTO events (seq, ts, kind, object_type, object_id, payload) VALUES
		(1, 1000, 'run.finish', 'run', 'run-alpha', '{"backend":"alpha","run_id":"run-alpha","state":"failed","exit_code":1,"stderr_tail":"tls failed"}'),
		(2, 1001, 'drain.backend-outcome', 'backend', 'alpha', '{"terminal_seq":1,"cause":"alpha outage"}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO backend_outcomes (terminal_seq, class, closes) VALUES (1, 'infrastructure', 1)`); err != nil {
		t.Fatal(err)
	}

	// Backend "beta": closed, then superseded by a successful probe -> no alarm.
	if _, err := db.Exec(`INSERT INTO events (seq, ts, kind, object_type, object_id, payload) VALUES
		(3, 1002, 'run.finish', 'run', 'run-beta', '{"backend":"beta","run_id":"run-beta","state":"failed","exit_code":1}'),
		(4, 1003, 'drain.backend-probe', 'backend', 'beta', '{"success":1}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO backend_outcomes (terminal_seq, class, closes) VALUES (3, 'infrastructure', 1)`); err != nil {
		t.Fatal(err)
	}

	// Backend "gamma": closed, then superseded by a later non-interrupted run.finish -> no alarm.
	if _, err := db.Exec(`INSERT INTO events (seq, ts, kind, object_type, object_id, payload) VALUES
		(5, 1004, 'run.finish', 'run', 'run-gamma-1', '{"backend":"gamma","run_id":"run-gamma-1","state":"failed","exit_code":1}'),
		(6, 1005, 'run.finish', 'run', 'run-gamma-2', '{"backend":"gamma","run_id":"run-gamma-2","state":"finished","exit_code":0}')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO backend_outcomes (terminal_seq, class, closes) VALUES (5, 'infrastructure', 1), (6, 'ok', 0)`); err != nil {
		t.Fatal(err)
	}

	// Items and dispatches: only open items should appear in readHolds.
	if _, err := db.Exec(`INSERT INTO items (item_id, state) VALUES ('item-open', 'in-queue'), ('item-done', 'done')`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO dispatches (item_id, lane_id, run_id, seat, action, reason, deferred_by, dispatched_at) VALUES
		('item-open', 'lane-1', 'run-open', 'build', 'hold', 'backend-closed', 'alpha', 2000),
		('item-done', 'lane-2', 'run-done', 'build', 'hold', 'backend-closed', 'alpha', 2000)`); err != nil {
		t.Fatal(err)
	}

	ro, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer ro.Close()

	if err := ro.ReadTx(ctx, func(r *store.Reader) error {
		stored, err := ReadStored(r)
		if err != nil {
			return err
		}
		if len(stored.infra) != 1 || stored.infra[0].Backend != "alpha" || stored.infra[0].Cause != "alpha outage" {
			t.Fatalf("infra alarms = %+v, want only alpha", stored.infra)
		}
		if len(stored.holds) != 1 || stored.holds[0].Item != "item-open" {
			t.Fatalf("stored holds = %+v, want only item-open", stored.holds)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
