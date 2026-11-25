package gitidentity

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatalf("getwd: %v", err)
	}
	// wd is tools/tillr/gitidentity
	return filepath.Clean(filepath.Join(wd, "..", "..", ".."))
}

type hookFixture struct {
	t      *testing.T
	root   string
	hooks  string
	binDir string
	env    []string
}

func newHookFixture(t *testing.T) *hookFixture {
	t.Helper()
	tmp := t.TempDir()
	binDir := filepath.Join(tmp, "bin")
	if err := os.MkdirAll(binDir, 0o755); err != nil {
		t.Fatalf("mkdir bin: %v", err)
	}

	tillrDir := filepath.Join(repoRoot(t), "tools", "tillr")
	binPath := filepath.Join(binDir, "tillr-pre-commit")
	build := exec.Command("go", "build", "-o", binPath, "./cmd/pre-commit-hook")
	build.Dir = tillrDir
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build pre-commit-hook: %v\n%s", err, out)
	}

	repoDir := filepath.Join(tmp, "repo")
	if err := os.MkdirAll(repoDir, 0o755); err != nil {
		t.Fatalf("mkdir repo: %v", err)
	}

	cfgPath := filepath.Join(tmp, "gitconfig")
	if err := os.WriteFile(cfgPath, []byte("[user]\n name = Owner\n email = owner@example.invalid\n"), 0o644); err != nil {
		t.Fatalf("write gitconfig: %v", err)
	}

	var env []string
	for _, entry := range os.Environ() {
		k, _, _ := strings.Cut(entry, "=")
		if strings.HasPrefix(k, "GIT_") {
			continue
		}
		if k == "PATH" {
			continue
		}
		env = append(env, entry)
	}
	env = append(env,
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"GIT_CONFIG_GLOBAL="+cfgPath,
		"GIT_CONFIG_NOSYSTEM=1",
	)

	f := &hookFixture{
		t:      t,
		root:   repoDir,
		hooks:  filepath.Join(repoDir, ".git", "hooks"),
		binDir: binDir,
		env:    env,
	}

	f.gitMust("init", "-q", "-b", "main")

	installer := filepath.Join(repoRoot(t), "bin", "hooks-install")
	inst := exec.Command(installer, "--root", repoRoot(t), "--hooks-dir", f.hooks)
	inst.Env = env
	if out, err := inst.CombinedOutput(); err != nil {
		t.Fatalf("hooks-install failed: %v\n%s", err, out)
	}

	f.gitMust("commit", "--allow-empty", "-qm", "base")
	return f
}

func (f *hookFixture) git(extra map[string]string, args ...string) (string, string, int) {
	f.t.Helper()
	fullArgs := append([]string{"-C", f.root}, args...)
	cmd := exec.Command("git", fullArgs...)
	env := append([]string(nil), f.env...)
	for k, v := range extra {
		env = append(env, k+"="+v)
	}
	cmd.Env = env
	var outBuf, errBuf bytes.Buffer
	cmd.Stdout = &outBuf
	cmd.Stderr = &errBuf
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			f.t.Fatalf("git %v: %v", args, err)
		}
	}
	return outBuf.String(), errBuf.String(), code
}

func (f *hookFixture) gitMust(args ...string) {
	f.t.Helper()
	_, stderr, code := f.git(nil, args...)
	if code != 0 {
		f.t.Fatalf("git %v failed (exit %d): %s", args, code, stderr)
	}
}

func TestPreCommitAuthorAndCommitterOverridesAreRefused(t *testing.T) {
	f := newHookFixture(t)

	for _, extra := range []map[string]string{
		{"GIT_AUTHOR_NAME": "Other"},
		{"GIT_COMMITTER_EMAIL": "other@example.invalid"},
	} {
		_, stderr, code := f.git(extra, "commit", "--allow-empty", "-qm", "foreign")
		if code == 0 {
			t.Fatalf("expected non-zero exit for override %v", extra)
		}
		if !strings.Contains(stderr, "pre-commit: foreign identity on main") {
			t.Fatalf("expected foreign identity message, got: %s", stderr)
		}
		if !strings.Contains(stderr, "Owner <owner@example.invalid>") {
			t.Fatalf("expected Owner <owner@example.invalid> in stderr, got: %s", stderr)
		}
	}

	_, stderr, code := f.git(nil, "commit", "--allow-empty", "--author=Other <other@example.invalid>", "-qm", "foreign")
	if code == 0 {
		t.Fatalf("expected non-zero exit for --author override, stderr: %s", stderr)
	}
}

func TestPreCommitLaneAndDetachedCommitsAreNotGuarded(t *testing.T) {
	f := newHookFixture(t)

	f.gitMust("checkout", "-qb", "lane")
	_, stderr, code := f.git(map[string]string{"GIT_AUTHOR_NAME": "Other"}, "commit", "--allow-empty", "-qm", "candidate")
	if code != 0 {
		t.Fatalf("lane commit failed: %s", stderr)
	}

	f.gitMust("checkout", "--detach", "-q")
	_, stderr, code = f.git(map[string]string{"GIT_AUTHOR_NAME": "Other"}, "commit", "--allow-empty", "-qm", "detached")
	if code != 0 {
		t.Fatalf("detached commit failed: %s", stderr)
	}
}

func TestPreCommitLocalIdentityCannotReplaceTheOwner(t *testing.T) {
	f := newHookFixture(t)

	f.gitMust("config", "user.name", "Other")
	_, stderr, code := f.git(nil, "commit", "--allow-empty", "-qm", "local")
	if code == 0 {
		t.Fatalf("expected non-zero exit for local config override")
	}
	if !strings.Contains(stderr, "local config") {
		t.Fatalf("expected 'local config' in stderr, got: %s", stderr)
	}
}

func TestPreCommitLocalHookIsChained(t *testing.T) {
	f := newHookFixture(t)

	localHook := filepath.Join(f.hooks, "pre-commit.local")
	if err := os.WriteFile(localHook, []byte("#!/bin/sh\necho local-hook >&2\nexit 7\n"), 0o755); err != nil {
		t.Fatalf("write pre-commit.local: %v", err)
	}

	_, stderr, code := f.git(nil, "commit", "--allow-empty", "-qm", "local guard")
	if code == 0 {
		t.Fatalf("expected non-zero exit from chained pre-commit.local")
	}
	if !strings.Contains(stderr, "local-hook") {
		t.Fatalf("expected 'local-hook' in stderr, got: %s", stderr)
	}
}
