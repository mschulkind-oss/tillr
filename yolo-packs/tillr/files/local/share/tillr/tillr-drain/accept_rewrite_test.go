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

	tree := acceptGit(t, repo, nil, "rev-parse", current+"^{tree}")
	args := []string{"accept-rewrite", "--root", repo, "--store", path, "--lost", lost, "--current", current, "--by", "owner", "--why", "re-authored", "--now", "2026-11-17T20:00:00Z"}
	want := "accepted the rewrite " + lost + " -> " + current + ": same tree " + tree + ", 1 commits replaced one for one; lost tip pinned at refs/tillr/trunk-history/accepted/" + lost + "\n"
	if code, out := driveRecord(t, bin, args...); code != 0 || string(out) != want {
		t.Fatalf("accept: %d %q, want %q", code, out, want)
	}
	if code, out := invoke(t, args...); code == 0 || !strings.Contains(string(out), "no rewrite is recorded") {
		t.Fatalf("a second acceptance: %d %s", code, out)
	}
	if code, out := invoke(t, "accept-rewrite", "--root", repo, "--lost", lost); code == 0 || !strings.Contains(string(out), "accept-rewrite needs") {
		t.Fatalf("missing flags: %d %s", code, out)
	}
	if code, out := invoke(t, "accept-rewrite", "--help"); code != 0 || !strings.Contains(string(out), "the commit on the trunk that replaces the lost tip") || strings.Contains(string(out), "with the same tree") {
		t.Fatalf("help: %d %s", code, out)
	}
}

// When the accepted rewrite is a rebase onto a moved base, the printed line describes
// the rebase, the matched lost commit count, and the current commit's tree without
// claiming the trees matched or that commits were replaced one for one.
func TestAcceptRewriteFaceRebase(t *testing.T) {
	repo := scratch(t, "repo")
	ident := []string{"GIT_AUTHOR_NAME=fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid"}
	commitFile := func(name, body string) string {
		if err := os.WriteFile(filepath.Join(repo, name), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		acceptGit(t, repo, nil, "add", name)
		acceptGit(t, repo, ident, "commit", "-qm", name)
		return acceptGit(t, repo, nil, "rev-parse", "HEAD")
	}
	acceptGit(t, repo, nil, "init", "-q", "-b", "main")
	base := commitFile("base.txt", "base\n")
	one := commitFile("one.txt", "one\n")
	lost := commitFile("two.txt", "two\n")

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

	acceptGit(t, repo, nil, "reset", "-q", "--hard", base)
	commitFile("upstream.txt", "upstream\n")
	acceptGit(t, repo, ident, "cherry-pick", one, lost)
	current := acceptGit(t, repo, nil, "rev-parse", "HEAD")
	lostTree := acceptGit(t, repo, nil, "rev-parse", lost+"^{tree}")
	currentTree := acceptGit(t, repo, nil, "rev-parse", current+"^{tree}")
	if lostTree == currentTree {
		t.Fatal("the fixture's rebase kept the tree, so it does not exercise a moved base")
	}

	args := []string{"accept-rewrite", "--root", repo, "--store", path, "--lost", lost, "--current", current, "--by", "owner", "--why", "rebased onto origin/main", "--now", "2026-11-20T01:36:20Z"}
	code, out := driveRecord(t, bin, args...)
	got := string(out)
	want := "accepted the rewrite " + lost + " -> " + current + ": rebase onto a moved base, 2 lost commits matched by patch, current tree " + currentTree + "; lost tip pinned at refs/tillr/trunk-history/accepted/" + lost + "\n"
	if code != 0 || got != want {
		t.Fatalf("accept rebase: %d %q, want %q", code, got, want)
	}
	if strings.Contains(got, "same tree") || strings.Contains(got, "one for one") {
		t.Fatalf("rebase output still claims same tree or one-for-one replacement: %q", got)
	}
}
