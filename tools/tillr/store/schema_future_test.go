package store_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestWriterRefusesNewerSchemaBeforeMigration(t *testing.T) {
	ctx := context.Background()
	path := filepath.Join(t.TempDir(), "future.db")
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`INSERT INTO schema_migrations(version,applied_at) VALUES(?,0)`, store.SchemaVersion+1); err != nil {
			return err
		}
		_, err := tx.Exec(`DROP TABLE meta`)
		return err
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	writer, err := store.Open(ctx, path, true)
	if err == nil {
		writer.Close()
		t.Fatal("writer accepted a newer schema")
	}
	if !strings.Contains(err.Error(), "newer") {
		t.Fatal(err)
	}
	reader, err := store.OpenRead(ctx, path)
	if err != nil {
		t.Fatal(err)
	}
	defer reader.Close()
	if err := reader.ReadTx(ctx, func(r *store.Reader) error {
		var n int
		if err := r.QueryRow(`SELECT count(*) FROM sqlite_master WHERE name='meta'`).Scan(&n); err != nil {
			return err
		}
		if n != 0 {
			t.Fatal("migration recreated meta before refusing")
		}
		version, err := r.SchemaVersion()
		if version != store.SchemaVersion+1 {
			t.Fatal(version)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
}
