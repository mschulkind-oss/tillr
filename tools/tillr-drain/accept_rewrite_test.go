package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/store"
)

func acceptGit(t *testing.T, dir string, env []string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = append(os.Environ(), env...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

// The face accepts a re-authored trunk once, and the second time refuses: nothing is
// recorded as lost any more.
func TestAcceptRewriteFace(t *testing.T) {
	repo := scratch(t, "repo")
	ident := []string{"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
	acceptGit(t, repo, nil, "init", "-q", "-b", "main")
	if err := os.WriteFile(filepath.Join(repo, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	acceptGit(t, repo, nil, "add", ".")
	acceptGit(t, repo, ident, "commit", "-qm", "a")
	lost := acceptGit(t, repo, nil, "rev-parse", "HEAD")
	path := filepath.Join(scratch(t, "store"), "state.db")
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO trunk_history(id,observed_tip,lost_tip) VALUES(1,?,'')`, lost)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	s.Close()
	owner := []string{"GIT_AUTHOR_NAME=owner", "GIT_AUTHOR_EMAIL=owner@example.invalid", "GIT_COMMITTER_NAME=owner", "GIT_COMMITTER_EMAIL=owner@example.invalid"}
	current := acceptGit(t, repo, owner, "commit-tree", lost+"^{tree}", "-m", "a")
	acceptGit(t, repo, nil, "reset", "-q", "--hard", current)

	args := []string{"accept-rewrite", "--root", repo, "--store", path, "--lost", lost, "--current", current, "--by", "owner", "--why", "re-authored", "--now", "2026-11-17T20:00:00Z"}
	if code, out := invoke(t, args...); code != 0 || !strings.Contains(string(out), "accepted the rewrite "+lost+" -> "+current) {
		t.Fatalf("accept: %d %s", code, out)
	}
	if code, out := invoke(t, args...); code == 0 || !strings.Contains(string(out), "no rewrite is recorded") {
		t.Fatalf("a second acceptance: %d %s", code, out)
	}
	if code, out := invoke(t, "accept-rewrite", "--root", repo, "--lost", lost); code == 0 || !strings.Contains(string(out), "accept-rewrite needs") {
		t.Fatalf("missing flags: %d %s", code, out)
	}
}
