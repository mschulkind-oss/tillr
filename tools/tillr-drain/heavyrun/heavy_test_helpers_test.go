package heavyrun

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

func init() {
	installRealRepoGuard()
}

func installRealRepoGuard() {
	wd, _ := os.Getwd()
	guarded := realCommonDirs(wd, os.Getenv("YOLO_DURABLE_DIR"), os.Getenv("TMPDIR"))
	if len(guarded) == 0 {
		CommonGuard = nil
		return
	}
	CommonGuard = func(common string) error {
		if guarded[sameDirKey(common)] {
			return fmt.Errorf("test reached the real repository's common directory %s: give the fixture its own repository", common)
		}
		return nil
	}
}

func realCommonDirs(starts ...string) map[string]bool {
	found := map[string]bool{}
	for _, start := range starts {
		if start == "" {
			continue
		}
		abs, err := filepath.Abs(start)
		if err != nil {
			continue
		}
		candidates := []string{abs}
		if resolved, err := filepath.EvalSymlinks(abs); err == nil && resolved != abs {
			candidates = append(candidates, resolved)
		}
		for _, dir := range candidates {
			for ; ; dir = filepath.Dir(dir) {
				if common := gitCommonDirAt(dir); common != "" {
					found[common] = true
				}
				if filepath.Dir(dir) == dir {
					break
				}
			}
		}
	}
	return found
}

func gitCommonDirAt(dir string) string {
	dotGit := filepath.Join(dir, ".git")
	info, err := os.Stat(dotGit)
	if err != nil {
		return ""
	}
	if info.IsDir() {
		return sameDirKey(dotGit)
	}
	raw, err := os.ReadFile(dotGit)
	if err != nil || !strings.HasPrefix(string(raw), "gitdir:") {
		return ""
	}
	gitDir := strings.TrimSpace(strings.TrimPrefix(string(raw), "gitdir:"))
	if !filepath.IsAbs(gitDir) {
		gitDir = filepath.Join(dir, gitDir)
	}
	if rel, err := os.ReadFile(filepath.Join(gitDir, "commondir")); err == nil {
		common := strings.TrimSpace(string(rel))
		if !filepath.IsAbs(common) {
			common = filepath.Join(gitDir, common)
		}
		return sameDirKey(common)
	}
	return sameDirKey(gitDir)
}

func sameDirKey(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Clean(p)
}

func loopHeld(t testing.TB, root string) *LoopLock {
	t.Helper()
	key := t.Name() + "\x00" + root
	if held, ok := heldLoopLocks.Load(key); ok {
		return held.(*LoopLock)
	}
	lock, busy, err := AcquireLoopLock(context.Background(), root)
	if err != nil || busy {
		t.Fatalf("loop lock for %s: busy=%v err=%v", root, busy, err)
	}
	heldLoopLocks.Store(key, lock)
	t.Cleanup(func() { heldLoopLocks.Delete(key); lock.Close() })
	return lock
}

var heldLoopLocks sync.Map

func scratchUnderDurable(t *testing.T) string {
	t.Helper()
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("cannot create the scratch root: %v", err)
	}
	dir, err := os.MkdirTemp(root, "heavyrun-")
	if err != nil {
		t.Fatalf("cannot create the scratch directory: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}

func clearGitRepoEnv(env []string) []string {
	kept := make([]string, 0, len(env))
	for _, entry := range env {
		key, _, _ := strings.Cut(entry, "=")
		switch key {
		case "GIT_DIR", "GIT_WORK_TREE", "GIT_COMMON_DIR", "GIT_INDEX_FILE",
			"GIT_OBJECT_DIRECTORY", "GIT_ALTERNATE_OBJECT_DIRECTORIES",
			"GIT_IMPLICIT_WORK_TREE", "GIT_NAMESPACE", "GIT_GRAFT_FILE",
			"GIT_SHALLOW_FILE", "GIT_REPLACE_REF_BASE", "GIT_NO_REPLACE_OBJECTS",
			"GIT_PREFIX", "GIT_CONFIG", "GIT_CONFIG_PARAMETERS", "GIT_CONFIG_COUNT":
			continue
		}
		kept = append(kept, entry)
	}
	return kept
}

func requireFixtureCommitRepo(t testing.TB, dir string, args []string) {
	t.Helper()
	commit := false
	for _, arg := range args {
		if arg == "commit" || arg == "commit-tree" {
			commit = true
			break
		}
	}
	if !commit {
		return
	}
	absDir, err := filepath.Abs(dir)
	if err != nil {
		t.Fatalf("resolving fixture git dir %q: %v", dir, err)
	}
	cmd := exec.Command("git", "-C", absDir, "rev-parse", "--git-dir")
	env := make([]string, 0, len(os.Environ()))
	for _, entry := range clearGitRepoEnv(os.Environ()) {
		if strings.HasPrefix(entry, "GIT_CEILING_DIRECTORIES=") {
			continue
		}
		env = append(env, entry)
	}
	cmd.Env = append(env, "GIT_CEILING_DIRECTORIES="+filepath.Dir(absDir))
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("refusing git %v in %s: directory is not its own repository (%v: %s)", args, dir, err, strings.TrimSpace(string(out)))
	}
}

func heavyGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	requireFixtureCommitRepo(t, root, args)
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	cmd.Env = clearGitRepoEnv(os.Environ())
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func heavyRepo(t *testing.T) (string, string) {
	t.Helper()
	root := scratchUnderDurable(t)
	heavyGit(t, root, "init", "-q", "-b", "main")
	return root, heavyCommit(t, root, "good")
}

func heavyCommit(t *testing.T, root, value string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(root, "value"), []byte(value), 0644); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, root, "add", ".")
	heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", value)
	return heavyGit(t, root, "rev-parse", "HEAD")
}

func heavyFixture(t *testing.T, root string, judge func(context.Context, string, string) ([]HeavyCheck, error)) HeavyRunner {
	t.Helper()
	return HeavyRunner{Root: root, Lock: loopHeld(t, root), Work: filepath.Join(scratchUnderDurable(t), "worktree"), Trunk: "refs/heads/main", Judge: judge}
}

// A judge that runs checks one at a time reports each verdict as the check ends.
func TestHeavyJudgeReportsEachCheckAsItEnds(t *testing.T) {
	lines := []string{}
	ctx := context.WithValue(context.Background(), HeavyProgressReportKey{}, func(line string) { lines = append(lines, line) })
	HeavyProgressReport(ctx, "loop: heavy %s %s in %s\n", "unit", "fail", "3s")
	if len(lines) != 1 || lines[0] != "loop: heavy unit fail in 3s\n" {
		t.Fatalf("lines=%q", lines)
	}
	HeavyProgressReport(context.Background(), "nothing %s", "here")
}
