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

func heavyGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
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
