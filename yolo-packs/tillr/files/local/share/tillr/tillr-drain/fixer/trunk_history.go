package fixer

import (
	"context"
	"database/sql"
	"errors"
	"os/exec"
	"strings"

	"github.com/mschulkind-oss/tillr/store"
)

const (
	// ReasonTrunkRewritten holds landings until the trunk contains its lost history again.
	ReasonTrunkRewritten = "trunk-rewritten"
	// ObservedTrunkRef pins the last observed trunk tip.
	ObservedTrunkRef = "refs/tillr/trunk-history/observed"
	// LostTrunkRef pins the lost trunk tip when a rewrite is detected.
	LostTrunkRef = "refs/tillr/trunk-history/lost"
	// TrunkHistoryDDL creates the trunk_history table.
	TrunkHistoryDDL = `
CREATE TABLE IF NOT EXISTS trunk_history (
 id INTEGER PRIMARY KEY CHECK (id = 1),
 observed_tip TEXT NOT NULL,
 lost_tip TEXT NOT NULL DEFAULT ''
);
`
)

// ErrTrunkRewritten is returned when landing waits on restored trunk history.
var ErrTrunkRewritten = errors.New("landing waits on restored trunk history")

// TrunkHistory records the observed trunk tip and any lost tip awaiting restoration.
type TrunkHistory struct{ Observed, Lost string }

// Reason formats the hold reason for a lost trunk tip.
func (h TrunkHistory) Reason() string {
	return ReasonTrunkRewritten + ": " + h.Lost + " -> " + h.Observed
}

// ReadTrunkHistory reads the trunk_history row, returning zero when the table or row is absent.
func ReadTrunkHistory(r *store.Reader) (TrunkHistory, error) {
	var h TrunkHistory
	var exists bool
	if err := r.QueryRow(`SELECT EXISTS(SELECT 1 FROM sqlite_master WHERE type='table' AND name='trunk_history')`).Scan(&exists); err != nil {
		return h, err
	}
	if !exists {
		return h, nil
	} // old read-only stores have not observed history yet
	err := r.QueryRow(`SELECT observed_tip,lost_tip FROM trunk_history WHERE id=1`).Scan(&h.Observed, &h.Lost)
	if err == sql.ErrNoRows {
		return h, nil
	}
	return h, err
}

// TrunkAncestor reports whether old is an ancestor of tip.
// A negative ancestry answer is not a Git failure. Missing objects and other Git
// errors refuse observation rather than fabricating a rewrite.
func TrunkAncestor(ctx context.Context, root, old, tip string) (bool, error) {
	cmd := exec.CommandContext(ctx, "git", "-C", root, "merge-base", "--is-ancestor", old, tip)
	cmd.Env = BoundaryGitEnv()
	out, err := cmd.CombinedOutput()
	if err == nil {
		return true, nil
	}
	var exit *exec.ExitError
	if errors.As(err, &exit) && exit.ExitCode() == 1 {
		return false, nil
	}
	return false, externalf("checking trunk ancestry %s -> %s: %v: %s", old, tip, err, strings.TrimSpace(string(out)))
}
