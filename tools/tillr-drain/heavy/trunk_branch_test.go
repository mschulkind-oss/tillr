package heavy

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStatusReadsConfiguredTrunkBranchWithoutMain(t *testing.T) {
	dir := t.TempDir()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Heavy Test",
			"GIT_AUTHOR_EMAIL=heavy-test@example.invalid",
			"GIT_COMMITTER_NAME=Heavy Test",
			"GIT_COMMITTER_EMAIL=heavy-test@example.invalid",
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
	tip := runGit("rev-parse", "HEAD")

	st, err := ReadStatus(context.Background(), dir, "")
	if err != nil {
		t.Fatalf("ReadStatus: %v", err)
	}
	if st.TipCommit != tip {
		t.Fatalf("TipCommit = %q, want %q", st.TipCommit, tip)
	}
}
