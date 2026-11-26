package main

import (
	"bytes"
	"os"
	"os/exec"
	"path/filepath"
	"slices"
	"strings"
	"testing"
)

func TestWatchLintHelp(t *testing.T) {
	for _, args := range [][]string{{"--help"}, {"-h"}, {"lint", "--help"}, {"lint", "-h"}} {
		t.Run(strings.Join(args, "_"), func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(t.Context(), args, &stdout, &stderr)
			if code != 0 || stderr.Len() != 0 || !strings.Contains(stdout.String(), "tillr watch lint [<repo-or-dir>]") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestWatchLintArgParsingAndErrors(t *testing.T) {
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"unknown flag", []string{"lint", "--unknown"}},
		{"missing charter arg", []string{"lint", "--charter"}},
		{"missing policy arg", []string{"lint", "--policy"}},
		{"empty policy arg", []string{"lint", "--policy="}},
		{"empty charter arg", []string{"lint", "--charter="}},
		{"extra positional arg", []string{"lint", "dir1", "dir2"}},
		{"extra positional around flags", []string{"lint", "dir1", "--rules", "r", "dir2"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			var stdout, stderr bytes.Buffer
			code := run(t.Context(), tc.args, &stdout, &stderr)
			if code != 1 || stdout.Len() != 0 || !strings.HasPrefix(stderr.String(), "tillr watch: ") {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestWatchLintPositionalOrderAndCwdDefault(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "config", "tillr", "watch")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(rulesDir, "refusals.toml"), []byte(testRule), 0600); err != nil {
		t.Fatal(err)
	}
	policyPath := filepath.Join(dir, "custom-policy.json")
	const policy = `{
  "default_backend": "main",
  "backends": {
    "main": {"adapter": "true", "model": "test-model"}
  },
  "seats": {
    "build": "main"
  }
}`
	if err := os.WriteFile(policyPath, []byte(policy), 0600); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		args []string
		cwd  string
	}{
		{"positional before flags", []string{"lint", dir, "--policy", policyPath, "--rules=" + rulesDir}, ""},
		{"positional after flags", []string{"lint", "--policy=" + policyPath, "--rules", rulesDir, dir}, ""},
		{"defaulted to cwd", []string{"lint", "--policy", policyPath}, dir},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if tc.cwd != "" {
				t.Chdir(tc.cwd)
			}
			var stdout, stderr bytes.Buffer
			code := run(t.Context(), tc.args, &stdout, &stderr)
			if code != 0 || !strings.Contains(stdout.String(), "source=shipped:rules/") || stderr.Len() != 0 {
				t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
			}
		})
	}
}

func TestWatchLintRealRepo(t *testing.T) {
	root := filepath.Clean(filepath.Join("..", ".."))
	policies, err := filepath.Glob(filepath.Join(root, "config", "tillr", "backend-policy*.json"))
	if err != nil || len(policies) == 0 {
		t.Fatalf("glob policies: %v (len=%d)", err, len(policies))
	}
	slices.Sort(policies)
	args := []string{"lint", root}
	for _, p := range policies {
		args = append(args, "--policy", p)
	}
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), args, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "rule drain-loop-fault source=shipped:rules/drain-loop-fault.toml") || stderr.Len() != 0 {
		t.Fatalf("real repo lint failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
}

func TestWatchLintRecordedVerdictExample(t *testing.T) {
	src := filepath.Clean(filepath.Join("..", "tillr", "examples", "recorded-verdict"))
	dst := filepath.Join(t.TempDir(), "recorded-verdict")
	if err := os.CopyFS(dst, os.DirFS(src)); err != nil {
		t.Fatalf("copy recorded-verdict: %v", err)
	}
	prep := exec.CommandContext(t.Context(), "sh", "prepare.sh")
	prep.Dir = dst
	if out, err := prep.CombinedOutput(); err != nil {
		t.Fatalf("prepare.sh: %v\n%s", err, out)
	}
	if err := os.Chmod(filepath.Join(dst, "stub-adapter.sh"), 0o755); err != nil {
		t.Fatalf("chmod stub-adapter.sh: %v", err)
	}
	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"lint", dst}, &stdout, &stderr)
	if code != 0 || !strings.Contains(stdout.String(), "rule drain-loop-fault source=shipped:rules/drain-loop-fault.toml") || stderr.Len() != 0 {
		t.Fatalf("recorded-verdict lint failed: code=%d\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
}

func TestWatchLintDefectiveTreeSortedAndReadOnly(t *testing.T) {
	dir := t.TempDir()
	rulesDir := filepath.Join(dir, "config", "tillr", "watch")
	if err := os.MkdirAll(rulesDir, 0755); err != nil {
		t.Fatal(err)
	}
	badRule := `[[rule]]
id = "bad_rule"
scope = "core"
severity = "warning"
[rule.reads]
events = ["not.a.real.event"]
[rule.when]
group_by = "item_id"
pattern = "count_in_window"
threshold = 2
window = "1h"
[rule.guidance]
summary = ""
check = []
do = []
[rule.delivery]
receiver = "drain:build"
`
	rulePath := filepath.Join(rulesDir, "bad.toml")
	if err := os.WriteFile(rulePath, []byte(badRule), 0600); err != nil {
		t.Fatal(err)
	}

	var stdout, stderr bytes.Buffer
	code := run(t.Context(), []string{"lint", dir}, &stdout, &stderr)
	if code != 1 || stderr.Len() != 0 {
		t.Fatalf("code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
	}
	lines := strings.Split(strings.TrimSuffix(stdout.String(), "\n"), "\n")
	if len(lines) < 3 {
		t.Fatalf("expected multiple findings, got %q", stdout.String())
	}
	for _, line := range lines {
		if !strings.HasPrefix(line, "config/tillr/watch/bad.toml: watch-lint-") {
			t.Errorf("unexpected line format: %q", line)
		}
	}
	if !slices.IsSorted(lines) {
		t.Errorf("findings are not sorted:\n%s", stdout.String())
	}
	if _, err := os.Stat(filepath.Join(dir, ".tillr")); !os.IsNotExist(err) {
		t.Errorf("lint created .tillr directory: %v", err)
	}
	after, err := os.ReadFile(rulePath)
	if err != nil {
		t.Fatal(err)
	}
	if string(after) != badRule {
		t.Errorf("lint mutated input rule file")
	}
}

func TestWatchLintPolicyAdapterRefused(t *testing.T) {
	t.Run("missing adapter", func(t *testing.T) {
		dir := t.TempDir()
		policyPath := filepath.Join(dir, "policy.json")
		const policy = `{
  "default_backend": "main",
  "backends": {
    "main": {"adapter": "./missing-adapter", "model": "test-model"}
  },
  "seats": {
    "build": "main"
  }
}`
		if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := run(t.Context(), []string{"lint", dir, "--policy", policyPath}, &stdout, &stderr)
		if code != 1 || stderr.Len() != 0 {
			t.Fatalf("expected code 1 with no stderr, got code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "watch-lint-seat-unmapped") || !strings.Contains(stdout.String(), "missing") {
			t.Fatalf("unexpected stdout: %q", stdout.String())
		}
	})

	t.Run("non-executable adapter", func(t *testing.T) {
		dir := t.TempDir()
		adapterPath := filepath.Join(dir, "non-exec-adapter")
		if err := os.WriteFile(adapterPath, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		policyPath := filepath.Join(dir, "policy.json")
		policy := `{
  "default_backend": "main",
  "backends": {
    "main": {"adapter": "` + filepath.ToSlash(adapterPath) + `", "model": "test-model"}
  },
  "seats": {
    "build": "main"
  }
}`
		if err := os.WriteFile(policyPath, []byte(policy), 0o600); err != nil {
			t.Fatal(err)
		}
		var stdout, stderr bytes.Buffer
		code := run(t.Context(), []string{"lint", dir, "--policy", policyPath}, &stdout, &stderr)
		if code != 1 || stderr.Len() != 0 {
			t.Fatalf("expected code 1 with no stderr, got code=%d stdout=%q stderr=%q", code, stdout.String(), stderr.String())
		}
		if !strings.Contains(stdout.String(), "watch-lint-seat-unmapped") || !strings.Contains(stdout.String(), "not executable") {
			t.Fatalf("unexpected stdout: %q", stdout.String())
		}
	})
}
