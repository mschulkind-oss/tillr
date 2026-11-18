package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestSyncRoadmapScopeDoesNotReadSymlinkTargets(t *testing.T) {
	for _, linked := range []bool{false, true} {
		repo := syncRepo(t, map[string]string{"docs/roadmap.md": "# Roadmap\n[Alias](alias.md)\n"})
		if err := os.Symlink("draft.md", filepath.Join(repo, "docs/alias.md")); err != nil {
			t.Fatal(err)
		}
		cmd := exec.Command("git", "-C", repo, "add", "docs/alias.md")
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("%v: %s", err, out)
		}
		road := "docs/alias.md"
		if linked {
			road = "docs/roadmap.md"
		}
		db := filepath.Join(scratch(t, "symlink-scope"), "state.db")
		cmd = exec.Command(bin, "sync", "--store", db, "--root", repo, "--roadmap", road, "--json")
		cmd.Env = append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
		out, err := cmd.CombinedOutput()
		exit, ok := err.(*exec.ExitError)
		if !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), "docs/alias.md") {
			t.Fatalf("symlink linked=%v error %v: %s", linked, err, out)
		}
		if _, err := os.Stat(db); !os.IsNotExist(err) {
			t.Fatal("symlink refusal wrote store")
		}
	}
}
