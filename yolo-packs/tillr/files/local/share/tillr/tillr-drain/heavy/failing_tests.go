package heavy

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/settings"
	"github.com/mschulkind-oss/tillr/store"
)

// FailingTestsDDL stores the named failing tests from a trunk suite check's last red
// result so subsequent proofs run only those tests until all of them pass, surviving
// loop restarts and cancelled proofs.
const FailingTestsDDL = `
CREATE TABLE IF NOT EXISTS heavy_failing_tests (
  trunk      TEXT NOT NULL,
  check_id   TEXT NOT NULL,
  tests_json TEXT NOT NULL,
  tip_sha    TEXT NOT NULL,
  updated_at INTEGER NOT NULL,
  PRIMARY KEY (trunk, check_id)
);
`

func init() {
	if err := store.RegisterDDL("heavy-failing-tests", FailingTestsDDL); err != nil {
		panic(err)
	}
}

func normalizeTrunk(trunk string) string {
	trunk = strings.TrimPrefix(strings.TrimSpace(trunk), "refs/heads/")
	if trunk == "" {
		return settings.DefaultTrunkBranch
	}
	return trunk
}

func normalizeFailingTests(tests []string) []string {
	seen := make(map[string]struct{}, len(tests))
	out := make([]string, 0, len(tests))
	for _, t := range tests {
		t = strings.TrimSpace(t)
		if t == "" {
			continue
		}
		if _, ok := seen[t]; ok {
			continue
		}
		seen[t] = struct{}{}
		out = append(out, t)
	}
	sort.Strings(out)
	return out
}

// LoadFailingTests reads the recorded failing test selectors for one trunk and check.
// An empty storePath or a store without the table returns nil, nil.
func LoadFailingTests(ctx context.Context, storePath, trunk, checkID string) ([]string, error) {
	if storePath == "" || checkID == "" {
		return nil, nil
	}
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		return nil, err
	}
	defer s.Close()
	var raw string
	err = s.ReadTx(ctx, func(r *store.Reader) error {
		qerr := r.QueryRow(
			`SELECT tests_json FROM heavy_failing_tests WHERE trunk = ? AND check_id = ?`,
			normalizeTrunk(trunk), checkID,
		).Scan(&raw)
		if qerr == sql.ErrNoRows || noTable(qerr) {
			return nil
		}
		return qerr
	})
	if err != nil || raw == "" {
		return nil, err
	}
	var tests []string
	if err := json.Unmarshal([]byte(raw), &tests); err != nil {
		return nil, err
	}
	return normalizeFailingTests(tests), nil
}

// SaveFailingTests records the named failing test selectors for one trunk and check.
// Passing an empty slice clears the record.
func SaveFailingTests(ctx context.Context, storePath, trunk, checkID, tipSHA string, tests []string, now time.Time) error {
	if storePath == "" || checkID == "" {
		return nil
	}
	norm := normalizeFailingTests(tests)
	if len(norm) == 0 {
		return ClearFailingTests(ctx, storePath, trunk, checkID)
	}
	raw, err := json.Marshal(norm)
	if err != nil {
		return err
	}
	if now.IsZero() {
		now = time.Now().UTC()
	}
	s, err := store.Open(ctx, storePath, false)
	if err != nil {
		return err
	}
	writeErr := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(FailingTestsDDL); err != nil {
			return err
		}
		_, err := tx.Exec(
			`INSERT INTO heavy_failing_tests (trunk, check_id, tests_json, tip_sha, updated_at)
			 VALUES (?, ?, ?, ?, ?)
			 ON CONFLICT(trunk, check_id) DO UPDATE SET
			   tests_json = excluded.tests_json,
			   tip_sha = excluded.tip_sha,
			   updated_at = excluded.updated_at`,
			normalizeTrunk(trunk), checkID, string(raw), tipSHA, now.UTC().UnixMilli(),
		)
		return err
	})
	return errors.Join(writeErr, s.Close())
}

// ClearFailingTests removes any recorded failing tests for one trunk and check.
func ClearFailingTests(ctx context.Context, storePath, trunk, checkID string) error {
	if storePath == "" || checkID == "" {
		return nil
	}
	s, err := store.Open(ctx, storePath, false)
	if err != nil {
		return err
	}
	writeErr := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(
			`DELETE FROM heavy_failing_tests WHERE trunk = ? AND check_id = ?`,
			normalizeTrunk(trunk), checkID,
		)
		if noTable(err) {
			return nil
		}
		return err
	})
	return errors.Join(writeErr, s.Close())
}
