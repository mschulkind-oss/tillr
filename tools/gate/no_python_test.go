package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestNoPythonCleanRepoPasses(t *testing.T) {
	root, _ := loadTestContext(t)
	findings, err := noPythonFindings(root)
	if err != nil {
		t.Fatal(err)
	}
	if len(findings) != 0 {
		t.Fatalf("expected clean repo, got findings: %v", findings)
	}
}

func TestNoPythonRefusesTrackedPyAndExtensionlessShebang(t *testing.T) {
	bin := buildRepoGateForTest(t)
	_, id := loadTestContext(t)
	personal := id[0]

	initRepo := func(t *testing.T) string {
		t.Helper()
		repo := t.TempDir()
		env := append(os.Environ(),
			"GIT_CONFIG_COUNT=2",
			"GIT_CONFIG_KEY_0=user.name",
			"GIT_CONFIG_VALUE_0="+personal[0],
			"GIT_CONFIG_KEY_1=user.email",
			"GIT_CONFIG_VALUE_1="+personal[1],
		)
		cmd := exec.Command("git", "init", "-q")
		cmd.Dir = repo
		cmd.Env = env
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git init: %v: %s", err, out)
		}
		return repo
	}

	addAndCheck := func(t *testing.T, repo, rel string, content []byte, wantCode int, wantSub string) {
		t.Helper()
		dst := filepath.Join(repo, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(dst, content, 0o644); err != nil {
			t.Fatal(err)
		}
		add := exec.Command("git", "add", "--", rel)
		add.Dir = repo
		if out, err := add.CombinedOutput(); err != nil {
			t.Fatalf("git add %s: %v: %s", rel, err, out)
		}
		cmd := exec.Command(bin, "no-python")
		cmd.Dir = repo
		out, err := cmd.CombinedOutput()
		code := 0
		if err != nil {
			if ee, ok := err.(*exec.ExitError); ok {
				code = ee.ExitCode()
			} else {
				t.Fatalf("no-python: %v", err)
			}
		}
		if code != wantCode {
			t.Fatalf("%s: exit=%d want=%d out=%s", rel, code, wantCode, out)
		}
		if wantSub != "" && !strings.Contains(string(out), wantSub) {
			t.Fatalf("%s: missing %q in %s", rel, wantSub, out)
		}
	}

	// 1. Tracked .py file fails
	repo1 := initRepo(t)
	addAndCheck(t, repo1, "example.py", []byte("print(1)\n"), 1, "tracked Python file: example.py")

	// 2. Extensionless file with Python shebang fails
	repo2 := initRepo(t)
	addAndCheck(t, repo2, "bin/helper", []byte("#!/usr/bin/env python3\nprint(1)\n"), 1, "extensionless Python shebang: bin/helper")

	// 3. Unexempted .py file under tools/tillr-gate/ fails
	repo3 := initRepo(t)
	addAndCheck(t, repo3, "tools/tillr-gate/extra.py", []byte("print(1)\n"), 1, "tracked Python file: tools/tillr-gate/extra.py")
}
