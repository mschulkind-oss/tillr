package store

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"sort"
	"strings"
)

// Fingerprint answers a digest of the store's logical content: its schema (every table,
// index, trigger and view as SQLite records it), its user_version, and every row of every
// table, sqlite_sequence included. It reads through a read-only connection in one read
// transaction, so it describes one moment whatever else is writing.
//
// It does not depend on where that content lies on disk. A commit stays in the write-ahead
// log until a checkpoint copies it into the database file, and a store handle's close
// copies nothing (checkpoint.go), so the database file's bytes before and after a command
// say nothing about whether the command wrote. Equal fingerprints say it changed no row
// and no schema. A test that a face writes nothing compares these, never file bytes.
func Fingerprint(ctx context.Context, path string) (string, error) {
	r, err := OpenRead(ctx, path)
	if err != nil {
		return "", err
	}
	defer r.Close()
	var digest string
	err = r.ReadTx(ctx, func(rd *Reader) error {
		h := sha256.New()
		var version int64
		if err := rd.q.QueryRowContext(ctx, `PRAGMA user_version`).Scan(&version); err != nil {
			return err
		}
		fmt.Fprintf(h, "user_version %d\n", version)
		schema, err := fingerprintRows(ctx, rd.q, `SELECT type, name, tbl_name, sql FROM sqlite_master`)
		if err != nil {
			return err
		}
		fmt.Fprintf(h, "schema\n%s", schema)
		names, err := fingerprintTables(ctx, rd.q)
		if err != nil {
			return err
		}
		for _, name := range names {
			rows, err := fingerprintRows(ctx, rd.q, `SELECT * FROM "`+strings.ReplaceAll(name, `"`, `""`)+`"`)
			if err != nil {
				return err
			}
			fmt.Fprintf(h, "table %q\n%s", name, rows)
		}
		digest = hex.EncodeToString(h.Sum(nil))
		return nil
	})
	if err != nil {
		return "", fmt.Errorf("store: fingerprinting %s: %w", path, err)
	}
	return digest, nil
}

// fingerprintTables answers every table's name, in name order.
func fingerprintTables(ctx context.Context, q queryer) ([]string, error) {
	rows, err := q.QueryContext(ctx, `SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var names []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			return nil, err
		}
		names = append(names, name)
	}
	return names, rows.Err()
}

// fingerprintRows answers the rows query returns, one line each holding every value with
// its type, sorted so that the order SQLite happens to return them in does not matter.
func fingerprintRows(ctx context.Context, q queryer, query string) (string, error) {
	rows, err := q.QueryContext(ctx, query)
	if err != nil {
		return "", err
	}
	defer rows.Close()
	columns, err := rows.Columns()
	if err != nil {
		return "", err
	}
	values := make([]any, len(columns))
	pointers := make([]any, len(columns))
	for i := range values {
		pointers[i] = &values[i]
	}
	var lines []string
	for rows.Next() {
		if err := rows.Scan(pointers...); err != nil {
			return "", err
		}
		var line strings.Builder
		for _, v := range values {
			fmt.Fprintf(&line, "%T:%q\t", v, fmt.Sprint(v))
		}
		lines = append(lines, line.String())
	}
	if err := rows.Err(); err != nil {
		return "", err
	}
	sort.Strings(lines)
	return strings.Join(lines, "\n") + "\n", nil
}
