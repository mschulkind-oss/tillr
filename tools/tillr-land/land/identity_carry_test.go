package land

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

// This slice tests acceptance carry; landing alias readers are a separate change.
func TestRewriteAliasesAndIdentityCarryForward(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "board.sqlite3"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	old, replacement, preaccepted := strings.Repeat("1", 40), strings.Repeat("2", 40), strings.Repeat("3", 40)
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS trunk_identity_acceptances(sha TEXT PRIMARY KEY, accepted_by TEXT NOT NULL, reason TEXT NOT NULL)`); err != nil {
			return err
		}
		if err := AcceptIdentity(tx, old, "owner", "accepted original"); err != nil {
			return err
		}
		if err := AcceptIdentity(tx, preaccepted, "maintainer", "accepted directly"); err != nil {
			return err
		}
		if err := CarryIdentityAcceptances(tx, map[string]string{old: replacement}); err != nil {
			return err
		}
		return CarryIdentityAcceptances(tx, map[string]string{old: preaccepted})
	}); err != nil {
		t.Fatal(err)
	}
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		for _, want := range []struct{ sha, by, reason string }{
			{old, "owner", "accepted original"},
			{replacement, "owner", "accepted original (carried from " + old + ")"},
			{preaccepted, "maintainer", "accepted directly"},
		} {
			var by, reason string
			if err := r.QueryRow(`SELECT accepted_by, reason FROM trunk_identity_acceptances WHERE sha=?`, want.sha).Scan(&by, &reason); err != nil || by != want.by || reason != want.reason {
				t.Errorf("acceptance for %s = (%q, %q, %v), want (%q, %q)", want.sha, by, reason, err, want.by, want.reason)
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}
