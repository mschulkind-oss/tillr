package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/store"
)

func TestPushSummaryReadsConfiguredTrunkBranchWithoutMain(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=View Test",
			"GIT_AUTHOR_EMAIL=view-test@example.invalid",
			"GIT_COMMITTER_NAME=View Test",
			"GIT_COMMITTER_EMAIL=view-test@example.invalid",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}

	runGit("init", "-b", "dev")
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[trunk]\nbranch = \"dev\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", ".tillr.toml")
	runGit("commit", "-m", "init on dev")
	baseSHA := runGit("rev-parse", "HEAD")

	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("a\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit("add", "a.txt")
	runGit("commit", "-m", "second on dev")
	tipSHA := runGit("rev-parse", "HEAD")

	runGit("update-ref", "refs/tillr/published/origin/dev", baseSHA)
	runGit("update-ref", "refs/tillr/green", baseSHA)

	dbPath := filepath.Join(t.TempDir(), "state.db")
	ctx := context.Background()
	s, err := store.Open(ctx, dbPath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`INSERT INTO events(kind, ts, object_type, object_id, payload) VALUES(?, ?, ?, ?, ?)`,
			"drain.post-landing-action",
			time.Date(2026, 11, 25, 0, 0, 0, 0, time.UTC).UnixMilli(),
			"commit",
			tipSHA,
			`{"action":"push","what":"green","kind":"push-ok","remote":"origin","ref":"refs/heads/dev"}`,
		)
		return err
	}); err != nil {
		t.Fatal(err)
	}

	var got *pushSummary
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		got = readPushSummary(ctx, r, dir)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if got.UnpublishedMainCount == nil || *got.UnpublishedMainCount != 1 {
		t.Fatalf("UnpublishedMainCount = %v, want 1", got.UnpublishedMainCount)
	}
	if got.Ref != "dev" {
		t.Fatalf("Ref = %q, want dev", got.Ref)
	}
}
