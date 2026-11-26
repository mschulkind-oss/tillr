package drain

import (
	"path/filepath"
	"testing"
)

func TestDrainFixtureGitCommandsIgnoreInheritedGitEnv(t *testing.T) {
	foreign := scratchUnderDurable(t)
	runGit(t, foreign, "init", "-q", "-b", "main")
	runGit(t, foreign, "commit", "--allow-empty", "-qm", "foreign initial")
	foreignHeadBefore := runGit(t, foreign, "rev-parse", "HEAD")

	t.Setenv("GIT_DIR", filepath.Join(foreign, ".git"))
	t.Setenv("GIT_WORK_TREE", foreign)
	t.Setenv("GIT_INDEX_FILE", filepath.Join(foreign, ".git", "index"))
	t.Setenv("GIT_COMMON_DIR", filepath.Join(foreign, ".git"))

	f, _ := passFixture(t)
	runGit(t, f.root, "commit", "--allow-empty", "-qm", "fixture commit")

	if got := runGit(t, f.root, "log", "-1", "--format=%s"); got != "fixture commit" {
		t.Fatalf("fixture HEAD subject = %q, want %q", got, "fixture commit")
	}
	if got := runGit(t, foreign, "rev-parse", "HEAD"); got != foreignHeadBefore {
		t.Fatalf("foreign repository HEAD moved from %s to %s", foreignHeadBefore, got)
	}
	if got := runGit(t, foreign, "log", "-1", "--format=%s"); got != "foreign initial" {
		t.Fatalf("foreign repository HEAD subject = %q, want %q", got, "foreign initial")
	}
}
