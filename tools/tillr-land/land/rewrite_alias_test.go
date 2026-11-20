package land

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func TestRewriteAliasesAndIdentityCarryForward(t *testing.T) {
	ctx := context.Background()
	s, err := store.Open(ctx, filepath.Join(t.TempDir(), "board.sqlite3"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	oldCommit := "1111111111111111111111111111111111111111"
	newCommit := "2222222222222222222222222222222222222222"

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS trunk_rewrites(old_sha TEXT PRIMARY KEY, new_sha TEXT NOT NULL, recorded_at TEXT NOT NULL, evidence TEXT NOT NULL)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO trunk_rewrites(old_sha, new_sha, recorded_at, evidence) VALUES(?, ?, '2026-11-20T00:00:00Z', 'test')`, oldCommit, newCommit); err != nil {
			return err
		}
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS trunk_identity_acceptances(sha TEXT PRIMARY KEY, accepted_by TEXT NOT NULL, reason TEXT NOT NULL)`); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO trunk_identity_acceptances(sha, accepted_by, reason) VALUES(?, 'owner', 'ok')`, oldCommit); err != nil {
			return err
		}
		if _, err := tx.Exec(`INSERT INTO landings(landing_id, event_seq, item_id, run_id, verdict_id, result, reason, base_sha, head_sha, trunk_before, trunk_after, gated_tree, landed_tree, conflict_paths, gate_skipped_reason)
			VALUES('l1', 1, 'item-1', 'run-1', 'v1', 'landed', '', 'base', 'head', 'before', ?, 'gtree', 'ltree', '[]', '')`, oldCommit); err != nil {
			return err
		}
		return CarryIdentityAcceptances(tx, map[string]string{oldCommit: newCommit})
	}); err != nil {
		t.Fatal(err)
	}

	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		if got := forwardRewrite(r, oldCommit); got != newCommit {
			t.Fatalf("forwardRewrite = %s, want %s", got, newCommit)
		}
		recorded, err := landingRecorded(r, newCommit)
		if err != nil || !recorded {
			t.Fatalf("landingRecorded(%s) = (%v, %v), want true", newCommit, recorded, err)
		}
		got, err := storedLanding(r, LandingRequest{ItemID: "item-1", RunID: "run-1", HeadSHA: "head"}, newCommit)
		if err != nil || got == nil || got.TrunkAfter != oldCommit {
			t.Fatalf("storedLanding(%s) = (%+v, %v)", newCommit, got, err)
		}
		var count int
		if err := r.QueryRow(`SELECT COUNT(*) FROM trunk_identity_acceptances WHERE sha=?`, newCommit).Scan(&count); err != nil || count != 1 {
			t.Fatalf("carried acceptance count = %d, err = %v", count, err)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestRecoveredLandingAcrossTrunkRewrite(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	repo := filepath.Join(dir, "repo")
	if err := os.MkdirAll(repo, 0o755); err != nil {
		t.Fatal(err)
	}
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", repo, "-c", "user.name=Test", "-c", "user.email=test@example.com", "-c", "commit.gpgsign=false"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	write := func(name, body string) {
		t.Helper()
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}

	git("init", "-q", "-b", "main")
	write("base.txt", "base\n")
	git("add", "base.txt")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")

	// Candidate head on lane branch.
	git("checkout", "-qb", "lane", base)
	write("feat.txt", "feat\n")
	git("add", "feat.txt")
	git("commit", "-qm", "feat")
	head := git("rev-parse", "HEAD")

	// Old landed commit on main, plus a remote rebase that replaces oldLanded with newLanded atop remoteCommit.
	git("checkout", "-q", "main")
	write("feat.txt", "feat\n")
	git("add", "feat.txt")
	git("commit", "-qm", "landed feat")
	oldLanded := git("rev-parse", "HEAD")

	git("reset", "--hard", "-q", base)
	write("remote.txt", "remote\n")
	git("add", "remote.txt")
	git("commit", "-qm", "remote")
	remoteCommit := git("rev-parse", "HEAD")
	write("feat.txt", "feat\n")
	git("add", "feat.txt")
	git("commit", "-qm", "rebased feat")
	newLanded := git("rev-parse", "HEAD")

	trunk, err := OpenTrunk(repo)
	if err != nil {
		t.Fatal(err)
	}
	s, err := store.Open(ctx, filepath.Join(dir, "board.sqlite3"), true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(`CREATE TABLE IF NOT EXISTS trunk_rewrites(old_sha TEXT PRIMARY KEY, new_sha TEXT NOT NULL, recorded_at TEXT NOT NULL, evidence TEXT NOT NULL)`); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO trunk_rewrites(old_sha, new_sha, recorded_at, evidence) VALUES(?, ?, '2026-11-20T00:00:00Z', 'test')`, oldLanded, newLanded)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var resolvedMarker string
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		resolvedMarker = forwardRewrite(r, oldLanded)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if resolvedMarker != newLanded {
		t.Fatalf("forwardRewrite = %s, want %s", resolvedMarker, newLanded)
	}
	recovered, ok, err := trunk.RecoveredLanding(base, head, resolvedMarker, newLanded)
	if err != nil || !ok || recovered != remoteCommit {
		t.Fatalf("RecoveredLanding = (%s, %v, %v), want (%s, true, nil)", recovered, ok, err, remoteCommit)
	}
}
