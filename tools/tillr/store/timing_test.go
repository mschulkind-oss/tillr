package store

import (
	"context"
	"database/sql"
	"path/filepath"
	"testing"
	"time"
)

func TestStoreBusyTimeoutDefaultsAndOverrides(t *testing.T) {
	path := filepath.Join(t.TempDir(), "timing.db")
	for _, tc := range []struct {
		name    string
		options []OpenOption
		want    int
	}{
		{"default", nil, 5000},
		{"short", []OpenOption{WithBusyTimeout(time.Millisecond)}, 1},
		{"no wait", []OpenOption{WithBusyTimeout(0)}, 0},
	} {
		t.Run(tc.name, func(t *testing.T) {
			s, err := Open(context.Background(), path, true, tc.options...)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			for name, db := range map[string]*sql.DB{"writer": s.db, "sidecar": s.sidecar} {
				var got int
				if err := db.QueryRow("PRAGMA busy_timeout").Scan(&got); err != nil {
					t.Fatal(err)
				}
				if got != tc.want {
					t.Errorf("%s busy timeout = %d, want %d", name, got, tc.want)
				}
			}
			r, err := OpenRead(context.Background(), path, tc.options...)
			if err != nil {
				t.Fatal(err)
			}
			defer r.Close()
			var got int
			if err := r.db.QueryRow("PRAGMA busy_timeout").Scan(&got); err != nil {
				t.Fatal(err)
			}
			if got != tc.want {
				t.Errorf("reader busy timeout = %d, want %d", got, tc.want)
			}
			for _, create := range []bool{false, true} {
				var owner *CheckpointOwner
				if create {
					owner, err = CreateAndOwnCheckpoints(context.Background(), path, tc.options...)
				} else {
					owner, err = OwnCheckpoints(context.Background(), path, tc.options...)
				}
				if err != nil {
					t.Fatal(err)
				}
				for name, db := range map[string]*sql.DB{"owner writer": owner.writer.db, "owner keeper": owner.keeper.db} {
					if err := db.QueryRow("PRAGMA busy_timeout").Scan(&got); err != nil {
						t.Fatal(err)
					}
					if got != tc.want {
						t.Errorf("%s (create=%v) busy timeout = %d, want %d", name, create, got, tc.want)
					}
				}
				if _, err := owner.Close(context.Background()); err != nil {
					t.Fatal(err)
				}
			}
		})
	}
	for _, d := range []time.Duration{-time.Millisecond, time.Microsecond} {
		if s, err := Open(context.Background(), path, false, WithBusyTimeout(d)); err == nil {
			s.Close()
			t.Fatalf("invalid busy timeout %s accepted", d)
		}
	}
}

func TestSQLiteTraceThresholdDefaultAndOverride(t *testing.T) {
	if got := time.Duration(sqliteTraceSlowSpan.Load()); got != 2*time.Second {
		t.Fatalf("trace threshold = %s, want 2s", got)
	}
	restore, err := SetSQLiteTraceSlowSpan(time.Millisecond)
	if err != nil {
		t.Fatal(err)
	}
	if got := time.Duration(sqliteTraceSlowSpan.Load()); got != time.Millisecond {
		t.Errorf("trace threshold = %s, want 1ms", got)
	}
	restore()
	if got := time.Duration(sqliteTraceSlowSpan.Load()); got != 2*time.Second {
		t.Errorf("restored threshold = %s, want 2s", got)
	}
	if _, err := SetSQLiteTraceSlowSpan(0); err == nil {
		t.Fatal("zero trace threshold accepted")
	}
}
