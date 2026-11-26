package boundary

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/heavyrun"
	"github.com/mschulkind-oss/tillr-drain/internal/testinputs"
)

const hangGuard = 5 * time.Minute

func scratchUnderDurable(t *testing.T) string {
	t.Helper()
	return t.TempDir()
}

func writeForkFile(t *testing.T, path, body string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatal(err)
	}
}

func writeForkSafe(path string, data []byte, perm os.FileMode) error {
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	return os.WriteFile(path, data, perm)
}

func runGit(t *testing.T, dir string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
	cmd.Env = BoundaryGitEnv()
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func declareSiblingInputs(t *testing.T) {
	t.Helper()
	if err := testinputs.Track("../.."); err != nil {
		t.Fatal(err)
	}
}

func compareAll(before, after *Boundary) ([]Violation, error) {
	violations, observed, err := CompareRunObserved(before, after)
	return append(violations, observed...), err
}

func registryGuardGit(t *testing.T, root string) {
	t.Helper()
	real, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := scratchUnderDurable(t)
	script := fmt.Sprintf(`#!/usr/bin/env python3
import fcntl, os, sys
args=sys.argv[1:]
def assert_locked(name, action):
    try:
        with open(os.path.join(os.environ['TILLR_TEST_COMMON'],name),'r') as lock:
            fcntl.flock(lock,fcntl.LOCK_EX|fcntl.LOCK_NB)
    except BlockingIOError:
        return
    except FileNotFoundError:
        pass
    sys.exit('registry command ran without '+name+': '+action)

if 'worktree' in args:
    action=args[args.index('worktree')+1]
    if action in ('add','remove','prune','list'):
        assert_locked('worktree-registry.lock',action)
    if action in ('add','remove','prune'):
        assert_locked('land.lock',action)
os.execv(%q,[%q]+args)
`, real, real)
	if err := writeForkSafe(filepath.Join(bin, "git"), []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_TEST_COMMON", filepath.Join(root, ".git"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}

func heavyRepo(t *testing.T) (string, string) {
	t.Helper()
	root := scratchUnderDurable(t)
	runGit(t, root, "init", "-q", "-b", "main")
	writeForkFile(t, filepath.Join(root, "value"), "good")
	runGit(t, root, "add", ".")
	runGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "good")
	return root, runGit(t, root, "rev-parse", "HEAD")
}

func heavyFixture(t *testing.T, root string, judge func(context.Context, string, string) ([]heavyrun.HeavyCheck, error)) heavyrun.HeavyRunner {
	t.Helper()
	lk, busy, err := heavyrun.AcquireLoopLock(context.Background(), root)
	if err != nil || busy {
		t.Fatalf("loop lock for %s: busy=%v err=%v", root, busy, err)
	}
	t.Cleanup(func() { lk.Close() })
	return heavyrun.HeavyRunner{
		Root:                     root,
		Lock:                     lk,
		Work:                     filepath.Join(scratchUnderDurable(t), "worktree"),
		Trunk:                    "refs/heads/main",
		Judge:                    judge,
		WithWorktreeRegistryLock: WithWorktreeRegistryLock,
		UnlockRegisteredWorktree: UnlockRegisteredWorktree,
	}
}
