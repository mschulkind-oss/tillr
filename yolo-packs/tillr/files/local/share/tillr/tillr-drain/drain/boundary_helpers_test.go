package drain

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func commitDenyListFixture(t *testing.T, root string) {
	t.Helper()
	policy, err := os.ReadFile("../../../config/tillr/deny-list.json")
	if err != nil {
		t.Fatal(err)
	}
	target := filepath.Join(root, denyListPath)
	if err := os.MkdirAll(filepath.Dir(target), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, target, string(policy))
	runGit(t, root, "add", denyListPath)
	runGit(t, root, "commit", "-qm", "common deny-list")
}

// The wrapper checks exclusion at the exact registry command, not whether a
// race happened to hit the brief empty-commondir window on this run.
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
	if err := writeForkSafe(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_TEST_COMMON", filepath.Join(root, ".git"))
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
}
