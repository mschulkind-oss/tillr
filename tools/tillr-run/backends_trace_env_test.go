package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// TestNoRunAdapterExportsOrInheritsGitTrace2 pins the owner ruling of 2026-11-17: git trace2
// recording is removed. A run's environment is inherited by every test suite the run starts,
// so a GIT_TRACE2_* variable there recorded 918k git commands for one run. The shell-command
// seam (BASH_ENV) stays, and the case proves the recording branch ran, so the absence is
// not just the branch being skipped.
func TestNoRunAdapterExportsOrInheritsGitTrace2(t *testing.T) {
	cases := []struct {
		name, adapter, harness string
		args                   []string
		stub                   string
	}{
		{"pi", filepath.Join("backends", "pi", "run-pi"), "pi",
			[]string{"--backend", "pi", "--model", "m", "--effort", "medium"}, "cat >/dev/null\nenv > \"$STUB_ENV\"\n"},
		{"claude", filepath.Join("backends", "claude", "run-claude"), "claude",
			[]string{"--backend", "claude", "--model", "m", "--effort", "medium"}, "env > \"$STUB_ENV\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter, err := filepath.Abs(tc.adapter)
			if err != nil {
				t.Fatal(err)
			}
			base := scratch(t, "trace2-env-"+tc.name)
			bin := filepath.Join(base, "bin")
			envLog := filepath.Join(base, "env.txt")
			writeExecutable(t, filepath.Join(bin, tc.harness), "#!/bin/sh\n"+tc.stub)
			worktree := filepath.Join(base, "lane", "worktree")
			if err := os.MkdirAll(worktree, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(base, "lane", "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
				t.Fatal(err)
			}
			traceDir := filepath.Join(base, "trace")
			cmd := exec.Command(adapter, tc.args...)
			cmd.Dir = worktree
			cmd.Env = append(os.Environ(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"STUB_ENV="+envLog,
				"TILLR_RUN_ID=run-trace2-env",
				"TILLR_TRACE_DIR="+traceDir,
				// What the loop's own environment might still carry from before the ruling.
				"GIT_TRACE2_EVENT="+filepath.Join(base, "inherited.jsonl"),
				"GIT_TRACE2_PARENT_SID=inherited",
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("the adapter failed: %v: %s", err, out)
			}
			raw, err := os.ReadFile(envLog)
			if err != nil {
				t.Fatalf("the harness stub recorded no environment: %v", err)
			}
			env := string(raw)
			if !strings.Contains(env, "BASH_ENV=") {
				t.Fatalf("the shell-command seam was not exported, so this case proves nothing:\n%s", env)
			}
			for _, line := range strings.Split(env, "\n") {
				if strings.HasPrefix(line, "GIT_TRACE2") {
					t.Fatalf("the run's environment carries %q", line)
				}
			}
		})
	}
}

// TestNonLandingAdaptersNeverRunGitRevParseOrWriteEnclosingRepo verifies that when
// TILLR_NON_LANDING=1 is set, run-pi and run-claude (plus pi-agent-dir.sh and
// pi-harness.sh) never invoke `git rev-parse` to discover an enclosing repository
// and never write into an enclosing repository's .tillr/loop.
func TestNonLandingAdaptersNeverRunGitRevParseOrWriteEnclosingRepo(t *testing.T) {
	cases := []struct {
		name, adapter, harness string
		args                   []string
		stub                   string
	}{
		{"pi", filepath.Join("backends", "pi", "run-pi"), "pi",
			[]string{"--backend", "pi", "--model", "m", "--effort", "medium"}, "cat >/dev/null\nenv > \"$STUB_ENV\"\n"},
		{"claude", filepath.Join("backends", "claude", "run-claude"), "claude",
			[]string{"--backend", "claude", "--model", "m", "--effort", "medium"}, "env > \"$STUB_ENV\"\n"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			adapter, err := filepath.Abs(tc.adapter)
			if err != nil {
				t.Fatal(err)
			}
			outer := scratch(t, "nonlanding-outer-"+tc.name)
			if out, err := exec.Command("git", "init", "-q", outer).CombinedOutput(); err != nil {
				t.Fatalf("git init: %v: %s", err, out)
			}
			outerTillr := filepath.Join(outer, ".tillr")
			if err := os.MkdirAll(outerTillr, 0o755); err != nil {
				t.Fatal(err)
			}
			plainRoot := filepath.Join(outer, "sub", "plain")
			workDir := filepath.Join(plainRoot, ".tillr", "lanes", "lane-1", "work")
			if err := os.MkdirAll(workDir, 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.WriteFile(filepath.Join(plainRoot, ".tillr", "lanes", "lane-1", "brief.md"), []byte(filledBrief(t, workDir)), 0o644); err != nil {
				t.Fatal(err)
			}

			bin := filepath.Join(scratch(t, "nonlanding-bin-"+tc.name), "bin")
			envLog := filepath.Join(plainRoot, "env.txt")
			gitLog := filepath.Join(plainRoot, "git-calls.log")
			writeExecutable(t, filepath.Join(bin, tc.harness), "#!/bin/sh\n"+tc.stub)
			writeExecutable(t, filepath.Join(bin, "git"), "#!/bin/sh\nprintf '%s\\n' \"$*\" >> \""+gitLog+"\"\nexit 99\n")

			traceDir := filepath.Join(plainRoot, ".tillr", "loop", "run-nonlanding-1")
			cmd := exec.Command(adapter, tc.args...)
			cmd.Dir = workDir
			cmd.Env = append(os.Environ(),
				"PATH="+bin+string(os.PathListSeparator)+os.Getenv("PATH"),
				"STUB_ENV="+envLog,
				"TILLR_RUN_ID=run-nonlanding-1",
				"TILLR_NON_LANDING=1",
				"TILLR_TRACE_DIR="+traceDir,
				"TILLR_REPO_ROOT="+plainRoot,
			)
			if out, err := cmd.CombinedOutput(); err != nil {
				t.Fatalf("adapter failed: %v: %s", err, out)
			}
			if calls, err := os.ReadFile(gitLog); err == nil && strings.TrimSpace(string(calls)) != "" {
				t.Fatalf("git was invoked on non-landing path:\n%s", calls)
			}
			if _, err := os.Stat(filepath.Join(outerTillr, "loop")); !os.IsNotExist(err) {
				t.Fatalf("enclosing repo .tillr/loop was created or modified: err=%v", err)
			}
			raw, err := os.ReadFile(envLog)
			if err != nil {
				t.Fatalf("harness stub wrote no env: %v", err)
			}
			if !strings.Contains(string(raw), "TILLR_TRACE_DIR="+traceDir) {
				t.Fatalf("expected TILLR_TRACE_DIR=%s in env:\n%s", traceDir, raw)
			}
		})
	}
}
