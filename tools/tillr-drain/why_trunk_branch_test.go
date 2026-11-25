package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestRevParseTrunkReadsConfiguredBranchWithoutMain(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Why Test",
			"GIT_AUTHOR_EMAIL=why-test@example.invalid",
			"GIT_COMMITTER_NAME=Why Test",
			"GIT_COMMITTER_EMAIL=why-test@example.invalid",
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
	want := runGit("rev-parse", "HEAD")

	// Switch checkout away from dev; revParseTrunk must still resolve dev, and must not resolve main.
	runGit("checkout", "-b", "work")

	got, ok := revParseTrunk(context.Background(), dir, "dev")
	if !ok || got != want {
		t.Fatalf("revParseTrunk(dev) = (%q, %v), want (%q, true)", got, ok, want)
	}
	if _, ok := revParseTrunk(context.Background(), dir, "main"); ok {
		t.Fatal("revParseTrunk(main) unexpectedly succeeded in repo without main")
	}
}
