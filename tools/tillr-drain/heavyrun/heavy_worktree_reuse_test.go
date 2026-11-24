package heavyrun

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"testing"
)

func logGitWorktreeActions(t *testing.T) string {
	t.Helper()
	realGit, err := exec.LookPath("git")
	if err != nil {
		t.Fatal(err)
	}
	bin := scratchUnderDurable(t)
	logPath := filepath.Join(bin, "worktree-actions.log")
	script := fmt.Sprintf(`#!/usr/bin/env python3
import os, sys
args = sys.argv[1:]
if "worktree" in args:
    idx = args.index("worktree")
    action = args[idx + 1] if idx + 1 < len(args) else ""
    with open(%q, "a") as fh:
        fh.write(action + "\n")
os.execv(%q, [%q] + args)
`, logPath, realGit, realGit)
	if err := os.WriteFile(filepath.Join(bin, "git"), []byte(script), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	return logPath
}

func readWorktreeActions(t *testing.T, logPath string) []string {
	t.Helper()
	raw, err := os.ReadFile(logPath)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatal(err)
	}
	var actions []string
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			actions = append(actions, line)
		}
	}
	return actions
}

func dirInode(t *testing.T, path string) uint64 {
	t.Helper()
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		t.Fatalf("unexpected Stat_t type %T", info.Sys())
	}
	return st.Ino
}

func TestHeavyReusesProofWorktreeInPlaceAndCleansLeftovers(t *testing.T) {
	root, _ := heavyRepo(t)
	if err := os.WriteFile(filepath.Join(root, ".gitignore"), []byte("*.ignored\nignored-dir/\n"), 0644); err != nil {
		t.Fatal(err)
	}
	heavyGit(t, root, "add", ".gitignore")
	heavyGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "add gitignore")
	tip1 := heavyGit(t, root, "rev-parse", "HEAD")

	logPath := logGitWorktreeActions(t)

	var seenHead, seenBranch, seenStatus, seenValue string
	runner := heavyFixture(t, root, func(_ context.Context, dir, _ string) ([]HeavyCheck, error) {
		seenHead = heavyGit(t, dir, "rev-parse", "HEAD")
		seenBranch = heavyGit(t, dir, "branch", "--show-current")
		seenStatus = heavyGit(t, dir, "status", "--porcelain", "--untracked-files=all", "--ignored")
		raw, err := os.ReadFile(filepath.Join(dir, "value"))
		if err != nil {
			return nil, err
		}
		seenValue = string(raw)

		// Leave tracked, staged, untracked, and ignored leftovers behind.
		if err := os.WriteFile(filepath.Join(dir, "value"), []byte("dirty-tracked"), 0644); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, "staged.txt"), []byte("staged"), 0644); err != nil {
			return nil, err
		}
		heavyGit(t, dir, "add", "staged.txt")
		if err := os.WriteFile(filepath.Join(dir, "untracked.txt"), []byte("untracked"), 0644); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Join(dir, "untracked-dir"), 0755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, "untracked-dir", "file.txt"), []byte("nested"), 0644); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, "leftover.ignored"), []byte("ignored"), 0644); err != nil {
			return nil, err
		}
		if err := os.MkdirAll(filepath.Join(dir, "ignored-dir"), 0755); err != nil {
			return nil, err
		}
		if err := os.WriteFile(filepath.Join(dir, "ignored-dir", "artifact.txt"), []byte("ignored"), 0644); err != nil {
			return nil, err
		}
		return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
	})

	// First proof creates the worktree and keeps it after finishing.
	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatalf("first proof: %v", err)
	}
	if seenHead != tip1 || seenBranch != "" || seenStatus != "" {
		t.Fatalf("first proof state: head=%s (want %s) branch=%q status=%q", seenHead, tip1, seenBranch, seenStatus)
	}
	if _, err := os.Stat(runner.Work); err != nil {
		t.Fatalf("proof worktree should remain after first proof: %v", err)
	}
	inodeAfterFirst := dirInode(t, runner.Work)

	// Clear the action log before the second proof at a new tip.
	if err := os.Remove(logPath); err != nil && !os.IsNotExist(err) {
		t.Fatal(err)
	}
	tip2 := heavyCommit(t, root, "second-tip")

	if _, err := runner.Run(context.Background()); err != nil {
		t.Fatalf("second proof: %v", err)
	}
	if seenHead != tip2 {
		t.Fatalf("second proof HEAD=%s, want %s", seenHead, tip2)
	}
	if seenBranch != "" {
		t.Fatalf("second proof is not detached: branch=%q", seenBranch)
	}
	if seenStatus != "" {
		t.Fatalf("second proof saw leftovers in git status:\n%s", seenStatus)
	}
	if seenValue != "second-tip" {
		t.Fatalf("second proof value=%q, want %q", seenValue, "second-tip")
	}
	inodeAfterSecond := dirInode(t, runner.Work)
	if inodeAfterSecond != inodeAfterFirst {
		t.Fatalf("proof worktree directory was recreated: inode %d -> %d", inodeAfterFirst, inodeAfterSecond)
	}
	for _, action := range readWorktreeActions(t, logPath) {
		if action == "remove" || action == "add" {
			t.Fatalf("second proof ran git worktree %s (all actions: %v)", action, readWorktreeActions(t, logPath))
		}
	}
	if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip2 {
		t.Fatalf("green=%s, want %s", green, tip2)
	}
}

func TestHeavyMissingOrPrunableProofWorktreeFallsBackToRemoveAndAdd(t *testing.T) {
	for _, tc := range []struct {
		name     string
		prunable bool
	}{
		{name: "missing"},
		{name: "prunable", prunable: true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root, base := heavyRepo(t)
			tip := heavyCommit(t, root, "tip-value")
			runner := heavyFixture(t, root, func(_ context.Context, dir, _ string) ([]HeavyCheck, error) {
				if got := heavyGit(t, dir, "rev-parse", "HEAD"); got != tip {
					t.Errorf("proof HEAD=%s, want %s", got, tip)
				}
				if status := heavyGit(t, dir, "status", "--porcelain", "--untracked-files=all", "--ignored"); status != "" {
					t.Errorf("proof worktree not clean:\n%s", status)
				}
				return []HeavyCheck{{CheckID: "unit", Result: "pass"}}, nil
			})
			if tc.prunable {
				if err := os.MkdirAll(filepath.Dir(runner.Work), 0755); err != nil {
					t.Fatal(err)
				}
				heavyGit(t, root, "worktree", "add", "--detach", runner.Work, base)
				if err := os.RemoveAll(runner.Work); err != nil {
					t.Fatal(err)
				}
				if listing := heavyGit(t, root, "worktree", "list", "--porcelain"); !strings.Contains(listing, "prunable") {
					t.Fatalf("expected prunable registration before proof:\n%s", listing)
				}
			}

			logPath := logGitWorktreeActions(t)
			if _, err := runner.Run(context.Background()); err != nil {
				t.Fatalf("proof failed: %v", err)
			}
			actions := readWorktreeActions(t, logPath)
			sawRemove, sawAdd := false, false
			for _, a := range actions {
				if a == "remove" {
					sawRemove = true
				}
				if a == "add" {
					sawAdd = true
				}
			}
			if !sawRemove || !sawAdd {
				t.Fatalf("expected remove-and-add path for %s worktree, got actions %v", tc.name, actions)
			}
			if green := heavyGit(t, root, "rev-parse", GreenRef); green != tip {
				t.Fatalf("green=%s, want %s", green, tip)
			}
			if _, err := os.Stat(runner.Work); err != nil {
				t.Fatalf("proof worktree should remain after creation: %v", err)
			}
		})
	}
}
