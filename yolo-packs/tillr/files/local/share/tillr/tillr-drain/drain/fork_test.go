package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestForkFenceNarrowsOnlyTheRecordedTarget(t *testing.T) {
	target := checkoutForDelivery(t.TempDir(), "lane-3", deliveryDecision{Kind: "fork", Fork: "pi"})
	fence := itemDeliveryFence(target, []string{".forks/lanes/lane-1/pi-subagents/", ".forks/lanes/lane-1/pi/src/"})
	if len(fence) != 1 || fence[0] != ".forks/lanes/lane-3/pi/src/" {
		t.Fatalf("fence = %v", fence)
	}
}

// A fork checkout is a repository inside this one, so its diff names paths relative to the
// checkout while the fence is repo-relative. The prefix is what joins the two spellings.
//
// The prefix is a fact about REPOSITORIES, not about path shapes, so this test builds a
// real one: a repository with a fork checkout inside it. It used to assert against a
// `/repo` that does not exist, which is why it could not tell "a checkout of another
// repository" from "a path with .forks in it".
func TestFencePrefixNamesOnlyACheckoutInsideTheRepository(t *testing.T) {
	root := scratchUnderDurable(t)
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "test")
	writeForkFile(t, filepath.Join(root, "README.md"), "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "base")

	fork := filepath.Join(root, ".forks", "lanes", "lane-1", "pi")
	if err := os.MkdirAll(fork, 0o755); err != nil {
		t.Fatalf("creating the fork checkout failed: %v", err)
	}
	runGit(t, fork, "init", "-b", "main")

	loop := &Loop{cfg: LoopConfig{Root: root}}
	if got, want := loop.fencePrefix(context.Background(), fork), ".forks/lanes/lane-1/pi/"; got != want {
		t.Fatalf("the fork checkout's prefix is %q, want %q", got, want)
	}

	// A worktree of the main repository is inside `root` and is still not a checkout of
	// another repository, so it gains no prefix.
	worktree := filepath.Join(root, ".tillr", "lanes", "lane-1", "worktree")
	if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
		t.Fatalf("creating the lane directory failed: %v", err)
	}
	runGit(t, root, "worktree", "add", "-b", "lane-1", worktree, "main")
	if got := loop.fencePrefix(context.Background(), worktree); got != "" {
		t.Fatalf("the lane worktree's prefix is %q, want empty", got)
	}

	for _, outside := range []string{
		filepath.Join(root, "..", "elsewhere", "pi"),
		root,
	} {
		if got := loop.fencePrefix(context.Background(), outside); got != "" {
			t.Fatalf("the prefix for %s is %q, want empty", outside, got)
		}
	}
	if !insideFence(".forks/lanes/lane-1/pi/src/foo.ts", []string{".forks/lanes/lane-1/pi"}) {
		t.Fatalf("a file under the checkout is outside a fence that names the checkout")
	}
}

// A fork checkout is a repository of its own, so its diff names paths relative to that
// checkout while the fence is repo-relative. The fence check joins the two spellings: a
// file under the checkout is inside a fence that names the checkout, and a file the fence
// does not name is reported with the repo-relative path an operator can find.
func TestFenceViolationScopesAForkCheckoutToItsRepoRelativePath(t *testing.T) {
	root := scratchUnderDurable(t)
	fork := filepath.Join(root, ".forks", "lanes", "lane-1", "pi")
	if err := os.MkdirAll(filepath.Join(fork, "src"), 0o755); err != nil {
		t.Fatalf("creating the fork checkout failed: %v", err)
	}
	runGit(t, fork, "init", "-b", "main")
	writeForkFile(t, filepath.Join(fork, "src", "foo.ts"), "base\n")
	runGit(t, fork, "add", ".")
	runGit(t, fork, "commit", "-m", "base")
	base := runGit(t, fork, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(fork, "src", "foo.ts"), "changed\n")
	runGit(t, fork, "commit", "-qam", "change")
	head := runGit(t, fork, "rev-parse", "HEAD")

	laneRoot := filepath.Join(scratchUnderDurable(t), "lanes")
	laneDir := filepath.Join(laneRoot, "lane-1")
	if err := os.MkdirAll(laneDir, 0o755); err != nil {
		t.Fatalf("creating the lane directory failed: %v", err)
	}
	loop := &Loop{cfg: LoopConfig{Root: root, LaneRoot: laneRoot}}
	candidate := Candidate{ItemID: "item", LaneID: "lane-1", RunID: "run", Worktree: fork, BaseSHA: base, HeadSHA: head}

	if err := os.WriteFile(filepath.Join(laneDir, FenceFile), []byte(".forks/lanes/lane-1/pi\n"), 0o644); err != nil {
		t.Fatalf("writing the fence failed: %v", err)
	}
	escape, err := loop.fenceViolation(context.Background(), candidate)
	if err != nil {
		t.Fatalf("reading the fence failed: %v", err)
	}
	if escape != "" {
		t.Fatalf("a change inside the fork checkout was refused: %s", escape)
	}

	if err := os.WriteFile(filepath.Join(laneDir, FenceFile), []byte("docs/\n"), 0o644); err != nil {
		t.Fatalf("writing the fence failed: %v", err)
	}
	escape, err = loop.fenceViolation(context.Background(), candidate)
	if err != nil {
		t.Fatalf("reading the fence failed: %v", err)
	}
	if !strings.Contains(escape, ".forks/lanes/lane-1/pi/src/foo.ts") {
		t.Fatalf("the escape does not name the repo-relative path: %q", escape)
	}
	// A lane-3 dispatch's rebound fence does not authorize a candidate in lane-1.
	if err := os.WriteFile(filepath.Join(laneDir, FenceFile), []byte(".forks/lanes/lane-3/pi/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	escape, err = loop.fenceViolation(context.Background(), candidate)
	if err != nil || !strings.Contains(escape, ".forks/lanes/lane-1/pi/src/foo.ts") {
		t.Fatalf("a sibling checkout escaped the rebound fence check: escape=%q error=%v", escape, err)
	}
}

// A worktree of the main repository is not a repository of its own: its diff is already
// repo-relative, so the fence check must not prefix it. Measured 2026-11-14, take 7: every
// candidate came back `boundary-scope-escape` naming a path that was inside its fence,
// because the lane worktree at <root>/.tillr/lanes/<lane>/worktree was prefixed with its
// own repo-relative path. This is the case the fork test above could not fail on, since
// every checkout it builds is a separate repository.
func TestFenceViolationAcceptsAMainRepoWorktree(t *testing.T) {
	root := scratchUnderDurable(t)
	runGit(t, root, "init", "-b", "main")
	runGit(t, root, "config", "user.email", "test@example.com")
	runGit(t, root, "config", "user.name", "test")
	if err := os.MkdirAll(filepath.Join(root, "tools", "tillr-view"), 0o755); err != nil {
		t.Fatalf("creating the directory failed: %v", err)
	}
	writeForkFile(t, filepath.Join(root, "tools", "tillr-view", "README.md"), "base\n")
	runGit(t, root, "add", ".")
	runGit(t, root, "commit", "-m", "base")

	laneRoot := filepath.Join(root, ".tillr", "lanes")
	worktree := filepath.Join(laneRoot, "lane-1", "worktree")
	if err := os.MkdirAll(filepath.Join(laneRoot, "lane-1"), 0o755); err != nil {
		t.Fatalf("creating the lane directory failed: %v", err)
	}
	runGit(t, root, "worktree", "add", "-b", "lane-1", worktree, "main")
	base := runGit(t, worktree, "rev-parse", "HEAD")
	writeForkFile(t, filepath.Join(worktree, "tools", "tillr-view", "README.md"), "changed\n")
	runGit(t, worktree, "commit", "-qam", "change")
	head := runGit(t, worktree, "rev-parse", "HEAD")

	loop := &Loop{cfg: LoopConfig{Root: root, LaneRoot: laneRoot}}
	candidate := Candidate{ItemID: "item", LaneID: "lane-1", RunID: "run", Worktree: worktree, BaseSHA: base, HeadSHA: head}
	if err := os.WriteFile(filepath.Join(laneRoot, "lane-1", FenceFile), []byte("tools/tillr-view/\n"), 0o644); err != nil {
		t.Fatalf("writing the fence failed: %v", err)
	}
	escape, err := loop.fenceViolation(context.Background(), candidate)
	if err != nil {
		t.Fatalf("reading the fence failed: %v", err)
	}
	if escape != "" {
		t.Fatalf("a change inside the lane worktree was refused: %s", escape)
	}

	if err := os.WriteFile(filepath.Join(laneRoot, "lane-1", FenceFile), []byte("docs/\n"), 0o644); err != nil {
		t.Fatalf("writing the fence failed: %v", err)
	}
	escape, err = loop.fenceViolation(context.Background(), candidate)
	if err != nil {
		t.Fatalf("reading the fence failed: %v", err)
	}
	if !strings.Contains(escape, "tools/tillr-view/README.md") {
		t.Fatalf("the escape does not name the repo-relative path: %q", escape)
	}
	if strings.Contains(escape, ".tillr/lanes/lane-1/worktree/tools/tillr-view/README.md") {
		t.Fatalf("the escape names the worktree's own path, not the repo-relative one: %q", escape)
	}
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range os.Environ() {
		if strings.HasPrefix(entry, "GIT_DIR=") || strings.HasPrefix(entry, "GIT_WORK_TREE=") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env,
		"GIT_AUTHOR_NAME=tillr", "GIT_AUTHOR_EMAIL=tillr@example.invalid",
		"GIT_COMMITTER_NAME=tillr", "GIT_COMMITTER_EMAIL=tillr@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v in %s failed: %v\n%s", args, dir, err, out)
	}
	return strings.TrimSpace(string(out))
}

func writeForkFile(t *testing.T, path, body string) {
	t.Helper()
	if err := writeForkSafe(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s failed: %v", path, err)
	}
}
