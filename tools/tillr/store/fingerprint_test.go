package store_test

import (
	"bytes"
	"context"
	"database/sql"
	"os"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func fingerprint(t *testing.T, path string) string {
	t.Helper()
	f, err := store.Fingerprint(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	return f
}

// A "writes nothing" test compares fingerprints because the database file's bytes cannot
// see a write: a commit stays in the log, and no close copies it. The fingerprint sees that
// write, and does not move when a checkpoint only relocates content from the log into the
// database file.
func TestTheFingerprintSeesAWriteTheDatabaseFileDoesNot(t *testing.T) {
	path := checkpointStore(t)
	before := fingerprint(t, path)
	beforeBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	s := openWritable(t, path)
	writeOne(t, s, "stray")
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	afterBytes, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(beforeBytes, afterBytes) {
		t.Fatal("the write reached the database file, so this store cannot show the difference the fingerprint exists for")
	}
	written := fingerprint(t, path)
	if written == before {
		t.Fatal("the fingerprint did not change when a row was written")
	}
	// A plain SQLite checkpoint copies the log into the database file and writes no row.
	raw, err := sql.Open("sqlite", "file:"+path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := raw.Exec(`PRAGMA wal_checkpoint(TRUNCATE)`); err != nil {
		t.Fatal(err)
	}
	raw.Close()
	moved, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	if bytes.Equal(moved, afterBytes) {
		t.Fatal("the checkpoint changed nothing in the database file, so the layout did not move")
	}
	if fingerprint(t, path) != written {
		t.Fatal("moving content from the log into the database file changed the fingerprint")
	}
}
