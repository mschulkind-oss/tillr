package joinmerge

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/internal/boundary"
)

func gitTest(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestFoldSiblingCommitsCleanAndConflict(t *testing.T) {
	repo := t.TempDir()
	gitTest(t, repo, "init", "-q", "-b", "main")
	gitTest(t, repo, "config", "user.name", "tillr")
	gitTest(t, repo, "config", "user.email", "tillr@example.invalid")

	writeTestFile(t, filepath.Join(repo, "a.txt"), "base a\n")
	writeTestFile(t, filepath.Join(repo, "b.txt"), "base b\n")
	writeTestFile(t, filepath.Join(repo, "tools/tillr/buildstamp/support-revision"), "rev0\n")
	gitTest(t, repo, "add", ".")
	gitTest(t, repo, "commit", "-qm", "base")
	base := gitTest(t, repo, "rev-parse", "HEAD")

	// Branch 1: edits a.txt and support-revision
	gitTest(t, repo, "checkout", "-q", "-b", "w1", base)
	writeTestFile(t, filepath.Join(repo, "a.txt"), "from w1\n")
	writeTestFile(t, filepath.Join(repo, "tools/tillr/buildstamp/support-revision"), "rev1\n")
	gitTest(t, repo, "add", ".")
	gitTest(t, repo, "commit", "-qm", "feat: piece 1")
	h1 := gitTest(t, repo, "rev-parse", "HEAD")
	t1 := gitTest(t, repo, "rev-parse", "HEAD^{tree}")

	// Branch 2: edits b.txt and support-revision (mirror-only conflict!)
	gitTest(t, repo, "checkout", "-q", "-b", "w2", base)
	writeTestFile(t, filepath.Join(repo, "b.txt"), "from w2\n")
	writeTestFile(t, filepath.Join(repo, "tools/tillr/buildstamp/support-revision"), "rev2\n")
	gitTest(t, repo, "add", ".")
	gitTest(t, repo, "commit", "-qm", "feat: piece 2")
	h2 := gitTest(t, repo, "rev-parse", "HEAD")
	t2 := gitTest(t, repo, "rev-parse", "HEAD^{tree}")

	// Branch 3: edits a.txt (real non-mirror conflict with w1!)
	gitTest(t, repo, "checkout", "-q", "-b", "w3", base)
	writeTestFile(t, filepath.Join(repo, "a.txt"), "from w3\n")
	gitTest(t, repo, "add", ".")
	gitTest(t, repo, "commit", "-qm", "feat: piece 3")
	h3 := gitTest(t, repo, "rev-parse", "HEAD")
	t3 := gitTest(t, repo, "rev-parse", "HEAD^{tree}")

	ctx := context.Background()

	// 1. Mirror-only conflict resolves cleanly with MirrorOnly=true.
	res, err := FoldSiblingCommits(ctx, repo, base, []SiblingCommit{
		{NodeID: "w1", HeadSHA: h1, TreeSHA: t1, Subject: "feat: piece 1", Message: "feat: piece 1"},
		{NodeID: "w2", HeadSHA: h2, TreeSHA: t2, Subject: "feat: piece 2", Message: "feat: piece 2"},
	})
	if err != nil {
		t.Fatalf("FoldSiblingCommits mirror-only failed: %v", err)
	}
	if !res.Clean || !res.MirrorOnly || res.TreeSHA == "" {
		t.Fatalf("expected clean mirror-only merge, got %+v", res)
	}
	if got := gitTest(t, repo, "show", res.TreeSHA+":a.txt"); got != "from w1" {
		t.Fatalf("a.txt = %q, want from w1", got)
	}
	if got := gitTest(t, repo, "show", res.TreeSHA+":b.txt"); got != "from w2" {
		t.Fatalf("b.txt = %q, want from w2", got)
	}
	if got := gitTest(t, repo, "show", res.TreeSHA+":tools/tillr/buildstamp/support-revision"); got != "rev0" {
		t.Fatalf("support-revision = %q, want rev0 (restored to base)", got)
	}
	_ = boundary.TillrMirrorRoot

	// 2. Non-mirror conflict returns Clean=false with ConflictingPaths and ConflictKey.
	conflictRes, err := FoldSiblingCommits(ctx, repo, base, []SiblingCommit{
		{NodeID: "w1", HeadSHA: h1, TreeSHA: t1},
		{NodeID: "w3", HeadSHA: h3, TreeSHA: t3},
	})
	if err != nil {
		t.Fatalf("FoldSiblingCommits conflict failed: %v", err)
	}
	if conflictRes.Clean || len(conflictRes.ConflictingPaths) != 1 || conflictRes.ConflictingPaths[0] != "a.txt" {
		t.Fatalf("expected conflict on a.txt, got %+v", conflictRes)
	}
	if conflictRes.ConflictKey == "" {
		t.Fatal("expected non-empty ConflictKey")
	}
}
