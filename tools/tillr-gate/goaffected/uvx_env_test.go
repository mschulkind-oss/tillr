package goaffected_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestGoAffectedFailsWhenTestRunsUvxEvenWithUvxOnHostPath(t *testing.T) {
	tillrGateBin := buildTillrGateBinary(t, filepath.Join(t.TempDir(), "tillr-gate"))

	fakeBin := t.TempDir()
	if err := os.WriteFile(filepath.Join(fakeBin, "uvx"), []byte("#!/bin/sh\necho fake-uvx\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", fakeBin+string(os.PathListSeparator)+os.Getenv("PATH"))

	tmpPath := filepath.Join(scratchRoot(t), "ga-uvx-"+freshNonce(t))
	repo := filepath.Join(tmpPath, "repo")
	for _, d := range []string{
		filepath.Join(repo, "tools", "tillr-uvxprobe"),
		filepath.Join(repo, "tools", "gate"),
	} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Cleanup(func() { _ = os.RemoveAll(tmpPath) })

	if err := os.WriteFile(filepath.Join(repo, "tools", "gate", "go-test-edges.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-uvxprobe", "go.mod"), []byte("module github.com/mschulkind-oss/tillr-uvxprobe\n\ngo 1.22\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	testSrc := `package uvxprobe

import (
	"os/exec"
	"testing"
)

func TestSubprocessCallsUvx(t *testing.T) {
	// Run uvx through a child shell, mimicking a compiled helper binary
	// (where testing.Testing() is false).
	out, err := exec.Command("sh", "-c", "uvx --version").CombinedOutput()
	if err != nil {
		t.Fatalf("uvx failed: %v: %s", err, out)
	}
}
`
	if err := os.WriteFile(filepath.Join(repo, "tools", "tillr-uvxprobe", "probe_test.go"), []byte(testSrc), 0o644); err != nil {
		t.Fatal(err)
	}

	cmd := exec.Command(tillrGateBin, "go-affected", "--root", repo, "--changed", "tools/tillr-uvxprobe/probe_test.go")
	out, err := cmd.CombinedOutput()
	if err == nil {
		t.Fatalf("expected go-affected to fail when a Go test runs uvx, but it passed:\n%s", out)
	}
	if !strings.Contains(string(out), "TestSubprocessCallsUvx") {
		t.Fatalf("expected failure output to name TestSubprocessCallsUvx, got:\n%s", out)
	}
}
