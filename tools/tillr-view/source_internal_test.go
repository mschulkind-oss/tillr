package main

import (
	"context"
	"database/sql"
	"errors"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// A read-only face reads a store older than its own schema, refuses one newer, and refuses
// a file that records no migration at all.
//
// Measured 2026-11-13: `just observatory` builds tillr-view from the working tree, so a
// change to the store's schema makes the built binary newer than the store the launched
// binaries own, and the strict `!=` check refused it — "store schema version 4 is not the
// 5 this binary supports" — which made the observatory unopenable against the live store
// for as long as a relaunch was pending. The two directions are not symmetric: an older
// store is a subset of the columns this binary knows, while a newer one may carry rows
// whose meaning it does not.
func TestAStoreOlderThanThisBinaryIsReadAndANewerOneIsRefused(t *testing.T) {
	for _, tc := range []struct {
		name    string
		version int
		refused bool
	}{
		{"older", store.SchemaVersion - 1, false},
		{"no migration row", 0, true},
		{"current", store.SchemaVersion, false},
		{"newer", store.SchemaVersion + 1, true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "state.db")
			s, err := store.Open(context.Background(), path, true)
			if err != nil {
				t.Fatalf("creating the store: %v", err)
			}
			if err := s.Close(); err != nil {
				t.Fatalf("closing the store: %v", err)
			}
			db, err := sql.Open("sqlite", "file:"+path)
			if err != nil {
				t.Fatalf("opening the store to set its version: %v", err)
			}
			// The recorded version is what the gate reads; the columns stay whatever this
			// binary created, which is the point -- the gate, not the shape, is under test.
			if _, err := db.Exec(`DELETE FROM schema_migrations`); err != nil {
				t.Fatalf("clearing schema_migrations: %v", err)
			}
			if _, err := db.Exec(`INSERT INTO schema_migrations (version, applied_at) VALUES (?, 0)`, tc.version); err != nil {
				t.Fatalf("recording version %d: %v", tc.version, err)
			}
			if err := db.Close(); err != nil {
				t.Fatalf("closing the version writer: %v", err)
			}

			v, err := openStore(context.Background(), path, 0)
			if tc.refused {
				var mismatch schemaMismatch
				if !errors.As(err, &mismatch) {
					t.Fatalf("a store at version %d was not refused: %v", tc.version, err)
				}
				if mismatch.version != tc.version {
					t.Fatalf("the refusal names version %d, want %d", mismatch.version, tc.version)
				}
				return
			}
			if err != nil {
				t.Fatalf("a store at version %d was refused: %v", tc.version, err)
			}
			if v.snapshot.SchemaVersion != tc.version {
				t.Fatalf("the snapshot reports version %d, want %d", v.snapshot.SchemaVersion, tc.version)
			}
		})
	}
}

// The serve face asks the same predicate the read faces do. It had its own copy of the
// comparison, so fixing the status path left `serve` refusing the store the status path
// had just read -- the failure that made `just observatory-dev` die on a v4 store with a
// v5 binary after the first fix.
func TestTheServeFaceAcceptsEveryVersionTheReadFacesDo(t *testing.T) {
	for _, tc := range []struct {
		version int
		want    bool
	}{
		{0, false},
		{store.SchemaVersion - 1, true},
		{store.SchemaVersion, true},
		{store.SchemaVersion + 1, false},
	} {
		if got := schemaSupported(tc.version); got != tc.want {
			t.Fatalf("schemaSupported(%d) = %v, want %v", tc.version, got, tc.want)
		}
	}
}
