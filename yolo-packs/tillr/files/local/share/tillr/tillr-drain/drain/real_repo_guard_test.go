package drain

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"
)

// The suite's scratch lives under the real repository's working tree, so a fixture that
// forgets `git init` resolves to the real repository: its proof then judges the real
// trunk and moves the real refs/tillr/green (measured 2026-11-20). This guard fails any
// proof, loop lock or fixer path that resolves to the real common directory.
func init() {
	real := realCommonDir()
	if real == "" {
		return
	}
	heavyCommonGuard = func(common string) error {
		if sameDirKey(common) == real {
			return fmt.Errorf("test reached the real repository's common directory %s: give the fixture its own repository", common)
		}
		return nil
	}
}

// realCommonDir finds the common git directory of the tree the suite runs in by reading
// the filesystem, not by running git: some tests put a wrapper first on PATH that
// re-runs the test binary on every git call, and a git call from here would recurse.
func realCommonDir() string {
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	for ; ; dir = filepath.Dir(dir) {
		dotGit := filepath.Join(dir, ".git")
		info, err := os.Stat(dotGit)
		if err == nil && info.IsDir() {
			return sameDirKey(dotGit)
		}
		if err == nil {
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
		if filepath.Dir(dir) == dir {
			return ""
		}
	}
}

func sameDirKey(p string) string {
	if resolved, err := filepath.EvalSymlinks(p); err == nil {
		p = resolved
	}
	return filepath.Clean(p)
}

// loopHeld takes root's loop lock for the test, as the loop does around a proof.
func loopHeld(t testing.TB, root string) *LoopLock {
	t.Helper()
	key := t.Name() + "\x00" + root
	if held, ok := heldLoopLocks.Load(key); ok {
		return held.(*LoopLock)
	}
	lock, busy, err := acquireLoopLock(context.Background(), root)
	if err != nil || busy {
		t.Fatalf("loop lock for %s: busy=%v err=%v", root, busy, err)
	}
	heldLoopLocks.Store(key, lock)
	t.Cleanup(func() { heldLoopLocks.Delete(key); lock.Close() })
	return lock
}

var heldLoopLocks sync.Map
