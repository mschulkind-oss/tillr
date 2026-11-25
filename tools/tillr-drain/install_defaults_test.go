package main_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

func setupConsumerRepo(t *testing.T) (root, storePath, policyPath string) {
	t.Helper()
	root = scratch(t, "consumer")
	if err := os.MkdirAll(filepath.Join(root, ".tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "config", "tillr"), 0o755); err != nil {
		t.Fatal(err)
	}
	storePath = filepath.Join(root, ".tillr", "state.db")
	s, err := store.InitFixture(context.Background(), storePath)
	if err != nil {
		t.Fatal(err)
	}
	s.Close()
	policyPath = filepath.Join(root, "config", "tillr", "backend-policy.json")
	if err := os.WriteFile(policyPath, []byte(`{"default_backend":"pi","backends":{"pi":{"adapter":"true","model":"fixture","timeout_seconds":60}}}`), 0o644); err != nil {
		t.Fatal(err)
	}
	return root, storePath, policyPath
}

func invokeInDir(t *testing.T, dir, installRoot string, args ...string) (int, string) {
	t.Helper()
	cmd := exec.Command(bin, args...)
	cmd.Dir = dir
	env := append(os.Environ(), "BOARD_DB=", "XDG_STATE_HOME="+scratch(t, "state"))
	if installRoot != "" {
		env = append(env, "TILLR_INSTALL_ROOT="+installRoot)
	} else {
		// Ensure TILLR_INSTALL_ROOT is unset if caller passed "".
		filtered := make([]string, 0, len(env))
		for _, e := range env {
			if !strings.HasPrefix(e, "TILLR_INSTALL_ROOT=") {
				filtered = append(filtered, e)
			}
		}
		env = filtered
	}
	cmd.Env = env
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatalf("running %v failed: %v", args, err)
	}
	return 0, string(out)
}

func TestBriefTemplateInstallDefaults(t *testing.T) {
	root, storePath, policyPath := setupConsumerRepo(t)
	// Place a decoy tools/tillr-run/backends/brief-template.md in the consumer repo.
	decoy := filepath.Join(root, "tools", "tillr-run", "backends", "brief-template.md")
	if err := os.MkdirAll(filepath.Dir(decoy), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(decoy, []byte("decoy template\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Missing installed file must be refused naming the installed path, ignoring decoy.
	missingRoot := scratch(t, "missing-install")
	wantInstalled := filepath.Join(missingRoot, "tillr-run", "backends", "brief-template.md")
	for _, tc := range []struct {
		name string
		args []string
	}{
		{"loop", []string{"loop", "--check", "--root", root, "--store", storePath, "--policy", policyPath, "--gate", "true", "--land", "true", "--verdict", "true"}},
		{"examine", []string{"examine", "--root", root, "--store", storePath, "--run", "run-1", "--policy", policyPath, "--gate", "true", "--land", "true", "--verdict", "true"}},
	} {
		t.Run(tc.name+"/missing-installed-refused", func(t *testing.T) {
			code, out := invokeInDir(t, root, missingRoot, tc.args...)
			if code != record.ExitUsage {
				t.Fatalf("code = %d, want %d; out:\n%s", code, record.ExitUsage, out)
			}
			if !strings.Contains(out, wantInstalled) {
				t.Fatalf("output did not name installed path %q:\n%s", wantInstalled, out)
			}
		})
	}

	// Explicit --brief-template wins even when installed file is missing.
	explicit := filepath.Join(scratch(t, "explicit"), "custom-template.md")
	if err := os.WriteFile(explicit, []byte("explicit template\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("loop/explicit-wins", func(t *testing.T) {
		code, out := invokeInDir(t, root, missingRoot,
			"loop", "--check", "--root", root, "--store", storePath, "--policy", policyPath,
			"--gate", "true", "--land", "true", "--verdict", "true",
			"--brief-template", explicit)
		if strings.Contains(out, wantInstalled) || strings.Contains(out, "--brief-template:") {
			t.Fatalf("explicit --brief-template was not honored (code %d):\n%s", code, out)
		}
	})
	t.Run("examine/explicit-wins", func(t *testing.T) {
		code, out := invokeInDir(t, root, missingRoot,
			"examine", "--root", root, "--store", storePath, "--run", "run-1", "--policy", policyPath,
			"--gate", "true", "--land", "true", "--verdict", "true",
			"--brief-template", explicit)
		if strings.Contains(out, wantInstalled) || strings.Contains(out, "--brief-template:") {
			t.Fatalf("explicit --brief-template was not honored (code %d):\n%s", code, out)
		}
	})

	// Installed brief-template.md succeeds (gets past template resolution).
	goodRoot := scratch(t, "good-install")
	installedTemplate := filepath.Join(goodRoot, "tillr-run", "backends", "brief-template.md")
	if err := os.MkdirAll(filepath.Dir(installedTemplate), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedTemplate, []byte("installed template\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Run("loop/installed-resolves", func(t *testing.T) {
		code, out := invokeInDir(t, root, goodRoot,
			"loop", "--check", "--root", root, "--store", storePath, "--policy", policyPath,
			"--gate", "true", "--land", "true", "--verdict", "true")
		if strings.Contains(out, "--brief-template:") {
			t.Fatalf("installed brief-template resolution failed (code %d):\n%s", code, out)
		}
	})
}
