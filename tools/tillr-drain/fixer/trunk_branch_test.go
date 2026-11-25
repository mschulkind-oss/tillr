package fixer

import (
	"context"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestAcceptRewriteReadsConfiguredTrunkBranchWithoutMain(t *testing.T) {
	dir := t.TempDir()
	runGitTest(t, dir, "init", "-b", "dev")
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[trunk]\nbranch = \"dev\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "README.md"), []byte("init\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", ".tillr.toml", "README.md")
	runGitTest(t, dir, "commit", "-m", "initial")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGitTest(t, dir, "add", "a.txt")
	runGitTest(t, dir, "commit", "-m", "first msg")
	lost := runGitTest(t, dir, "rev-parse", "HEAD")

	runGitTest(t, dir, "commit", "--amend", "-m", "amended msg")
	current := runGitTest(t, dir, "rev-parse", "HEAD")

	dbPath, s := openTestStore(t)
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
	runGitTest(t, dir, "update-ref", LostTrunkRef, lost)
	runGitTest(t, dir, "update-ref", ObservedTrunkRef, lost)

	got, err := AcceptRewrite(ctx, AcceptRewriteRequest{
		Root:    dir,
		Store:   dbPath,
		Lost:    lost,
		Current: current,
		By:      "owner",
		Why:     "amend commit message on dev",
		At:      time.Date(2026, 11, 25, 0, 1, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("AcceptRewrite: %v", err)
	}
	if got.Current != current || got.Lost != lost {
		t.Fatalf("AcceptRewrite = %+v", got)
	}
}
