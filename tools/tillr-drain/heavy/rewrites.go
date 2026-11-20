package heavy

import (
	"database/sql"
	"fmt"
	"strings"
)

// RewritesDDL is the store's record of absorbed trunk rewrites. A `git pull` that rebases
// main gives each unpushed commit a new sha; the store still names the old ones (landing
// rows, run pins, lane bases). Each row maps one old sha to the commit that replaced it,
// so a reader that looks a sha up in the store, or asks whether the trunk contains it,
// can follow the replacement. trunk_rewrite_unmatched holds the commits of a rewrite the
// drain could not map, so the hold that stops the loop can name them.
const RewritesDDL = `
CREATE TABLE IF NOT EXISTS trunk_rewrites (
 old_sha TEXT PRIMARY KEY,
 new_sha TEXT NOT NULL,
 recorded_at TEXT NOT NULL,
 evidence TEXT NOT NULL DEFAULT ''
);
CREATE INDEX IF NOT EXISTS trunk_rewrites_new ON trunk_rewrites(new_sha);
CREATE TABLE IF NOT EXISTS trunk_rewrite_unmatched (
 sha TEXT PRIMARY KEY,
 lost_tip TEXT NOT NULL
);
`

// Querier is what a store reader and a store transaction's reader both answer.
type Querier interface {
	Query(query string, args ...any) (*sql.Rows, error)
	QueryRow(query string, args ...any) *sql.Row
}

func noTable(err error) bool { return err != nil && strings.Contains(err.Error(), "no such table") }

// RewriteAliases answers sha followed by every earlier sha the trunk's rewrites replaced
// with it, directly or through several rewrites. A store without the table answers sha
// alone, so a landing lookup over the result is the plain lookup it was.
func RewriteAliases(q Querier, sha string) ([]string, error) {
	rows, err := q.Query(`WITH RECURSIVE a(sha) AS (SELECT ? UNION SELECT r.old_sha FROM trunk_rewrites r JOIN a ON r.new_sha = a.sha) SELECT sha FROM a`, sha)
	if noTable(err) {
		return []string{sha}, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	out := []string{sha}
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		if s != sha {
			out = append(out, s)
		}
	}
	return out, rows.Err()
}

// RewriteResolve follows sha through the recorded rewrites to the commit that replaced
// it, and answers sha itself when none did.
func RewriteResolve(q Querier, sha string) (string, error) {
	seen := map[string]bool{}
	for !seen[sha] {
		seen[sha] = true
		var next string
		err := q.QueryRow(`SELECT new_sha FROM trunk_rewrites WHERE old_sha = ?`, sha).Scan(&next)
		if err == sql.ErrNoRows || noTable(err) {
			return sha, nil
		}
		if err != nil {
			return "", err
		}
		sha = next
	}
	return sha, nil
}

// InList is the SQL placeholder list and arguments for a set of shas.
func InList(shas []string) (string, []any) {
	marks := make([]string, len(shas))
	args := make([]any, len(shas))
	for i, s := range shas {
		marks[i], args[i] = "?", s
	}
	return "(" + strings.Join(marks, ",") + ")", args
}

// RewriteUnmatched answers the commits of the lost history the drain could not map.
func RewriteUnmatched(q Querier, lost string) ([]string, error) {
	rows, err := q.Query(`SELECT sha FROM trunk_rewrite_unmatched WHERE lost_tip = ? ORDER BY rowid`, lost)
	if noTable(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	var out []string
	for rows.Next() {
		var s string
		if err := rows.Scan(&s); err != nil {
			return nil, err
		}
		out = append(out, s)
	}
	return out, rows.Err()
}

// RewriteAbsorbed is the latest rewrite the drain absorbed: when, and how many commits
// it mapped.
type RewriteAbsorbed struct {
	At      string `json:"at"`
	Commits int    `json:"commits"`
}

// Line is the one-line answer `why` and the status view give.
func (a RewriteAbsorbed) Line() string {
	return fmt.Sprintf("trunk: absorbed a rewrite at %s; %d commits mapped to their new shas", a.At, a.Commits)
}

// LatestRewrite reads the most recent absorbed rewrite, and false when none is recorded.
func LatestRewrite(q Querier) (RewriteAbsorbed, bool, error) {
	var a RewriteAbsorbed
	err := q.QueryRow(`SELECT recorded_at, COUNT(*) FROM trunk_rewrites WHERE recorded_at = (SELECT recorded_at FROM trunk_rewrites ORDER BY rowid DESC LIMIT 1) GROUP BY recorded_at`).Scan(&a.At, &a.Commits)
	if err == sql.ErrNoRows || noTable(err) {
		return a, false, nil
	}
	return a, err == nil, err
}
