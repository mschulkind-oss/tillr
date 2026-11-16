package store_test

import (
	"context"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// A reader answers a utility's own rows inside the read transaction, so a utility
// reads its tables without opening the database itself. The error half is what makes
// the test a test: a query surface that answers nil rows for a query it could not run
// would report an empty table for a broken one.
func TestAReaderQueriesDomainRows(t *testing.T) {
	const ddl = `CREATE TABLE IF NOT EXISTS query_test_rows (id TEXT PRIMARY KEY, label TEXT NOT NULL);`
	if err := store.RegisterDDL("query-test", ddl); err != nil {
		t.Fatalf("registering a DDL failed: %v", err)
	}
	s, err := store.InitFixture(context.Background(), filepath.Join(t.TempDir(), "board.sqlite3"))
	if err != nil {
		t.Fatalf("creating a fixture store failed: %v", err)
	}
	defer s.Close()
	err = s.WriteTx(context.Background(), func(tx *store.Tx) error {
		for _, row := range [][2]string{{"a", "first"}, {"b", "second"}} {
			if _, err := tx.Exec(`INSERT INTO query_test_rows (id, label) VALUES (?, ?)`, row[0], row[1]); err != nil {
				return err
			}
		}
		return nil
	})
	if err != nil {
		t.Fatalf("writing the rows failed: %v", err)
	}

	err = s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT id, label FROM query_test_rows ORDER BY id`)
		if err != nil {
			return err
		}
		defer rows.Close()
		var got []string
		for rows.Next() {
			var id, label string
			if err := rows.Scan(&id, &label); err != nil {
				return err
			}
			got = append(got, id+"="+label)
		}
		if err := rows.Err(); err != nil {
			return err
		}
		if len(got) != 2 || got[0] != "a=first" || got[1] != "b=second" {
			t.Fatalf("the reader answered %v, want [a=first b=second]", got)
		}
		if _, err := r.Query(`SELECT id FROM query_test_absent`); err == nil {
			t.Fatal("a query against a table that does not exist answered no error")
		}
		return nil
	})
	if err != nil {
		t.Fatalf("the read transaction failed: %v", err)
	}
}
