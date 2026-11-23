package fixer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestPatchIDsAndUpdateLandedMarkers(t *testing.T) {
	repo := initTestRepo(t)
	base := runGitTest(t, repo, "rev-parse", "HEAD")
	if err := os.WriteFile(filepath.Join(repo, "p.txt"), []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "p.txt")
	runGitTest(t, repo, "commit", "-m", "p")
	c1 := runGitTest(t, repo, "rev-parse", "HEAD")

	ctx := context.Background()
	commits, ids, err := PatchIDs(ctx, repo, base+".."+c1)
	if err != nil {
		t.Fatal(err)
	}
	if len(commits) != 1 || commits[0] != c1 || ids[c1] == "" {
		t.Fatalf("PatchIDs = (%v, %v)", commits, ids)
	}
	single, err := SingleCommitPatchID(ctx, repo, c1)
	if err != nil || single != ids[c1] {
		t.Fatalf("SingleCommitPatchID = %q, want %q (err=%v)", single, ids[c1], err)
	}
	nmCommits, nmIDs, err := NonMergePatchIDs(ctx, repo, base+".."+c1)
	if err != nil || len(nmCommits) != 1 || nmIDs[c1] != single {
		t.Fatalf("NonMergePatchIDs = (%v, %v, %v)", nmCommits, nmIDs, err)
	}

	// UpdateLandedMarkers moves refs/tillr/landed/<id> from old to new.
	runGitTest(t, repo, "update-ref", "refs/tillr/landed/run-1", base)
	if err := UpdateLandedMarkers(ctx, repo, map[string]string{base: c1}); err != nil {
		t.Fatal(err)
	}
	got := runGitTest(t, repo, "rev-parse", "refs/tillr/landed/run-1")
	if got != c1 {
		t.Fatalf("landed marker = %s, want %s", got, c1)
	}
}

func TestAcceptRewriteUnit(t *testing.T) {
	repo := initTestRepo(t)
	base := runGitTest(t, repo, "rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(repo, "x.txt"), []byte("x\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, repo, "add", "x.txt")
	runGitTest(t, repo, "commit", "-m", "original commit")
	lost := runGitTest(t, repo, "rev-parse", "HEAD")

	// Amend commit message to create a same-tree rewrite on main.
	runGitTest(t, repo, "commit", "--amend", "-m", "rewritten commit")
	current := runGitTest(t, repo, "rev-parse", "HEAD")

	storePath, s := openTestStore(t)
	ctx := context.Background()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		if _, err := tx.Exec(TrunkHistoryDDL); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO trunk_history(id, observed_tip, lost_tip) VALUES(1, ?, ?)`, current, lost)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	_ = base

	got, err := AcceptRewrite(ctx, AcceptRewriteRequest{
		Root:    repo,
		Store:   storePath,
		Lost:    lost,
		Current: current,
		By:      "owner",
		Why:     "re-authored",
		At:      time.Date(2026, 11, 22, 13, 0, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("AcceptRewrite: %v", err)
	}
	if got.Lost != lost || got.Current != current || got.Commits != 1 || got.Rebased {
		t.Fatalf("AcceptRewrite = %+v", got)
	}
	if pin := runGitTest(t, repo, "rev-parse", got.Pin); pin != lost {
		t.Fatalf("pin = %s, want %s", pin, lost)
	}
}
