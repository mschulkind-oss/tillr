package main_test

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/buildstamp"
	"github.com/mschulkind-oss/tillr/record"
)

func TestLoopCheckAdapter(t *testing.T) {
	setup := func(t *testing.T, adapter string) (root, storePath, policyPath, installRoot string) {
		t.Helper()
		root, storePath, policyPath = setupConsumerRepo(t)
		policyJSON := fmt.Sprintf(`{"default_backend":"pi","backends":{"pi":{"adapter":%q,"model":"fixture","timeout_seconds":60}}}`, adapter)
		if err := os.WriteFile(policyPath, []byte(policyJSON), 0o644); err != nil {
			t.Fatal(err)
		}
		installRoot = scratch(t, "install")
		installedTemplate := filepath.Join(installRoot, "tillr-run", "backends", "brief-template.md")
		if err := os.MkdirAll(filepath.Dir(installedTemplate), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(installedTemplate, []byte("installed template\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		revFile := filepath.Join(installRoot, "tillr", "buildstamp", "support-revision")
		if err := os.MkdirAll(filepath.Dir(revFile), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(revFile, []byte(buildstamp.Read().SupportRevision+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		return root, storePath, policyPath, installRoot
	}

	t.Run("missing-adapter-refused", func(t *testing.T) {
		missingAdapter := filepath.Join(scratch(t, "adapters"), "missing-adapter")
		root, storePath, policyPath, installRoot := setup(t, missingAdapter)

		code, out := invokeInDir(t, root, installRoot,
			"loop", "--check", "--root", root, "--store", storePath, "--policy", policyPath,
			"--gate", "true", "--land", "true", "--verdict", "true")
		if code != record.ExitUsage {
			t.Fatalf("code = %d, want %d; out:\n%s", code, record.ExitUsage, out)
		}
		if !strings.Contains(out, `"pi"`) {
			t.Fatalf("output did not name backend %q:\n%s", "pi", out)
		}
		if !strings.Contains(out, missingAdapter) {
			t.Fatalf("output did not name adapter %q:\n%s", missingAdapter, out)
		}
	})

	t.Run("non-executable-adapter-refused", func(t *testing.T) {
		nonExecAdapter := filepath.Join(scratch(t, "adapters"), "non-exec-adapter")
		if err := os.WriteFile(nonExecAdapter, []byte("#!/bin/sh\nexit 0\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		root, storePath, policyPath, installRoot := setup(t, nonExecAdapter)

		code, out := invokeInDir(t, root, installRoot,
			"loop", "--check", "--root", root, "--store", storePath, "--policy", policyPath,
			"--gate", "true", "--land", "true", "--verdict", "true")
		if code != record.ExitUsage {
			t.Fatalf("code = %d, want %d; out:\n%s", code, record.ExitUsage, out)
		}
		if !strings.Contains(out, `"pi"`) {
			t.Fatalf("output did not name backend %q:\n%s", "pi", out)
		}
		if !strings.Contains(out, nonExecAdapter) {
			t.Fatalf("output did not name adapter %q:\n%s", nonExecAdapter, out)
		}
	})

	t.Run("executable-adapter-accepted", func(t *testing.T) {
		execAdapter := filepath.Join(scratch(t, "adapters"), "exec-adapter")
		if err := os.WriteFile(execAdapter, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
		root, storePath, policyPath, installRoot := setup(t, execAdapter)

		code, out := invokeInDir(t, root, installRoot,
			"loop", "--check", "--root", root, "--store", storePath, "--policy", policyPath,
			"--gate", "true", "--land", "true", "--verdict", "true")
		if code != 0 {
			t.Fatalf("code = %d, want 0; out:\n%s", code, out)
		}
	})

	t.Run("shipped-relative-adapter-resolves-from-install-root", func(t *testing.T) {
		const relAdapter = "tools/tillr-run/backends/claude/run-claude"
		root, storePath, policyPath, installRoot := setup(t, relAdapter)
		installedAdapter := filepath.Join(installRoot, "tillr-run", "backends", "claude", "run-claude")
		if err := os.MkdirAll(filepath.Dir(installedAdapter), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(installedAdapter, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}

		code, out := invokeInDir(t, root, installRoot,
			"loop", "--check", "--root", root, "--store", storePath, "--policy", policyPath,
			"--gate", "true", "--land", "true", "--verdict", "true")
		if code != 0 {
			t.Fatalf("code = %d, want 0; out:\n%s", code, out)
		}
	})

	t.Run("shipped-relative-adapter-missing-names-both-locations", func(t *testing.T) {
		const relAdapter = "tools/tillr-run/backends/claude/run-claude"
		root, storePath, policyPath, installRoot := setup(t, relAdapter)
		wantRepoPath := filepath.Join(root, relAdapter)
		wantInstallPath := filepath.Join(installRoot, "tillr-run", "backends", "claude", "run-claude")

		code, out := invokeInDir(t, root, installRoot,
			"loop", "--check", "--root", root, "--store", storePath, "--policy", policyPath,
			"--gate", "true", "--land", "true", "--verdict", "true")
		if code != record.ExitUsage {
			t.Fatalf("code = %d, want %d; out:\n%s", code, record.ExitUsage, out)
		}
		if !strings.Contains(out, wantRepoPath) {
			t.Fatalf("refusal did not name operated repository path %q:\n%s", wantRepoPath, out)
		}
		if !strings.Contains(out, wantInstallPath) {
			t.Fatalf("refusal did not name installed adapter path %q:\n%s", wantInstallPath, out)
		}
	})
}
