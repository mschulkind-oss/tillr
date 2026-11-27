package setupcheck

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func writeExecFile(t *testing.T, path string, mode os.FileMode) string {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\nexit 0\n"), mode); err != nil {
		t.Fatal(err)
	}
	return path
}

func TestCheckAdapterBuiltin(t *testing.T) {
	// Source checkout fallback (TILLR_INSTALL_ROOT unset).
	t.Setenv("TILLR_INSTALL_ROOT", "")
	for _, name := range []string{"run-pi", "run-claude", "run-agy", "run-muse"} {
		got, err := CheckAdapter("", name)
		if err != nil {
			t.Fatalf("CheckAdapter(%q) from source checkout: %v", name, err)
		}
		if filepath.Base(got) != name {
			t.Fatalf("CheckAdapter(%q) = %q, want basename %q", name, got, name)
		}
	}

	// TILLR_INSTALL_ROOT override.
	installRoot := t.TempDir()
	want := writeExecFile(t, filepath.Join(installRoot, "tillr-run", "backends", "pi", "run-pi"), 0o755)
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)
	got, err := CheckAdapter("", "run-pi")
	if err != nil {
		t.Fatalf("CheckAdapter(run-pi) with TILLR_INSTALL_ROOT: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}

	// Relative TILLR_INSTALL_ROOT must fail.
	t.Setenv("TILLR_INSTALL_ROOT", "relative/path")
	if _, err := CheckAdapter("", "run-pi"); err == nil || !strings.Contains(err.Error(), "TILLR_INSTALL_ROOT must be absolute") {
		t.Fatalf("expected absolute TILLR_INSTALL_ROOT error, got %v", err)
	}
}

func TestCheckAdapterAbsolute(t *testing.T) {
	dir := t.TempDir()
	execPath := writeExecFile(t, filepath.Join(dir, "my-adapter"), 0o755)
	got, err := CheckAdapter(dir, execPath)
	if err != nil {
		t.Fatalf("CheckAdapter(abs) failed: %v", err)
	}
	if got != execPath {
		t.Fatalf("got %q, want %q", got, execPath)
	}
}

func TestCheckAdapterRelativeAndFallback(t *testing.T) {
	t.Setenv("TILLR_INSTALL_ROOT", "")
	root := t.TempDir()

	// Relative adapter present under root.
	local := writeExecFile(t, filepath.Join(root, "scripts", "adapter.sh"), 0o755)
	got, err := CheckAdapter(root, filepath.Join("scripts", "adapter.sh"))
	if err != nil {
		t.Fatalf("CheckAdapter(local rel) failed: %v", err)
	}
	if got != local {
		t.Fatalf("got %q, want %q", got, local)
	}

	// Relative tools/tillr-run/backends/... absent under root falls back to source checkout.
	got, err = CheckAdapter(root, filepath.Join("tools", "tillr-run", "backends", "pi", "run-pi"))
	if err != nil {
		t.Fatalf("CheckAdapter(fallback rel) failed: %v", err)
	}
	if !strings.HasSuffix(filepath.ToSlash(got), "tools/tillr-run/backends/pi/run-pi") {
		t.Fatalf("unexpected fallback path %q", got)
	}

	// Relative tools/tillr-run/backends/... missing everywhere fails and names both locations tried.
	missingRel := filepath.Join("tools", "tillr-run", "backends", "pi", "missing-bin")
	if _, err := CheckAdapter(root, missingRel); err == nil {
		t.Fatalf("expected missing error, got nil")
	} else {
		wantRoot := filepath.Join(root, missingRel)
		if !strings.Contains(err.Error(), "missing") || !strings.Contains(err.Error(), wantRoot) || !strings.Contains(err.Error(), "missing-bin") {
			t.Fatalf("expected refusal naming both locations tried (%s and installed path), got: %v", wantRoot, err)
		}
	}

	// Installed copy via TILLR_INSTALL_ROOT resolves relative shipped adapter paths
	// when the operated repository has no file at the relative path.
	installRoot := t.TempDir()
	claudeRel := filepath.Join("tools", "tillr-run", "backends", "claude", "run-claude")
	installedClaude := writeExecFile(t, filepath.Join(installRoot, "tillr-run", "backends", "claude", "run-claude"), 0o755)
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)

	got, fromInstall, err := CheckAdapterWithOrigin(root, claudeRel)
	if err != nil {
		t.Fatalf("CheckAdapterWithOrigin(install fallback) failed: %v", err)
	}
	if !fromInstall || got != installedClaude {
		t.Fatalf("got (%q, %v), want (%q, true)", got, fromInstall, installedClaude)
	}

	// Missing in both the operated repository and TILLR_INSTALL_ROOT names both locations tried.
	missingClaudeRel := filepath.Join("tools", "tillr-run", "backends", "claude", "missing-claude")
	wantRootMissing := filepath.Join(root, missingClaudeRel)
	wantInstallMissing := filepath.Join(installRoot, "tillr-run", "backends", "claude", "missing-claude")
	if _, _, err := CheckAdapterWithOrigin(root, missingClaudeRel); err == nil {
		t.Fatalf("expected missing error for %s", missingClaudeRel)
	} else if !strings.Contains(err.Error(), wantRootMissing) || !strings.Contains(err.Error(), wantInstallMissing) {
		t.Fatalf("refusal must name both %q and %q, got: %v", wantRootMissing, wantInstallMissing, err)
	}

	// Missing backends directory under TILLR_INSTALL_ROOT names both the repo path and the install path.
	emptyInstall := t.TempDir()
	t.Setenv("TILLR_INSTALL_ROOT", emptyInstall)
	wantRootClaude := filepath.Join(root, claudeRel)
	wantInstallBackends := filepath.Join(emptyInstall, "tillr-run", "backends")
	if _, _, err := CheckAdapterWithOrigin(root, claudeRel); err == nil {
		t.Fatalf("expected error when install backends dir is missing")
	} else if !strings.Contains(err.Error(), wantRootClaude) || !strings.Contains(err.Error(), wantInstallBackends) {
		t.Fatalf("refusal must name both %q and %q, got: %v", wantRootClaude, wantInstallBackends, err)
	}

	// Operated repository wins over TILLR_INSTALL_ROOT when both exist.
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)
	repoClaude := writeExecFile(t, filepath.Join(root, claudeRel), 0o755)
	got, fromInstall, err = CheckAdapterWithOrigin(root, claudeRel)
	if err != nil {
		t.Fatalf("CheckAdapterWithOrigin(repo precedence) failed: %v", err)
	}
	if fromInstall || got != repoClaude {
		t.Fatalf("got (%q, %v), want (%q, false)", got, fromInstall, repoClaude)
	}
}

func TestCheckAdapterBareCommand(t *testing.T) {
	dir := t.TempDir()
	execPath := writeExecFile(t, filepath.Join(dir, "custom-adapter"), 0o755)
	t.Setenv("PATH", dir+string(os.PathListSeparator)+os.Getenv("PATH"))

	got, err := CheckAdapter("", "custom-adapter")
	if err != nil {
		t.Fatalf("CheckAdapter(bare) failed: %v", err)
	}
	if got != execPath {
		t.Fatalf("got %q, want %q", got, execPath)
	}

	if _, err := CheckAdapter("", "definitely-not-a-real-command-xyz"); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing error for unknown command, got %v", err)
	}
}

func TestCheckAdapterErrors(t *testing.T) {
	dir := t.TempDir()

	// Missing file.
	missingPath := filepath.Join(dir, "missing-adapter")
	if _, err := CheckAdapter(dir, missingPath); err == nil || !strings.Contains(err.Error(), "missing") {
		t.Fatalf("expected missing error, got %v", err)
	}

	// Non-executable file (0644).
	nonExec := writeExecFile(t, filepath.Join(dir, "non-exec"), 0o644)
	if _, err := CheckAdapter(dir, nonExec); err == nil || !strings.Contains(err.Error(), "not executable") {
		t.Fatalf("expected not executable error, got %v", err)
	}

	// Directory.
	subDir := filepath.Join(dir, "dir-adapter")
	if err := os.MkdirAll(subDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if _, err := CheckAdapter(dir, subDir); err == nil || !strings.Contains(err.Error(), "directory") {
		t.Fatalf("expected directory error, got %v", err)
	}
}

func TestResolveBriefTemplate(t *testing.T) {
	t.Setenv("TILLR_INSTALL_ROOT", "")
	got, err := ResolveBriefTemplate()
	if err != nil {
		t.Fatalf("ResolveBriefTemplate from source checkout: %v", err)
	}
	if filepath.Base(got) != "brief-template.md" {
		t.Fatalf("got %q, want brief-template.md", got)
	}

	installRoot := t.TempDir()
	want := filepath.Join(installRoot, "tillr-run", "backends", "brief-template.md")
	if err := os.MkdirAll(filepath.Dir(want), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(want, []byte("# template\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)
	got, err = ResolveBriefTemplate()
	if err != nil {
		t.Fatalf("ResolveBriefTemplate with TILLR_INSTALL_ROOT: %v", err)
	}
	if got != want {
		t.Fatalf("got %q, want %q", got, want)
	}
}

func TestResolveWorkflowFile(t *testing.T) {
	t.Setenv("TILLR_INSTALL_ROOT", "")
	repoRoot := t.TempDir()

	// Falls back to source checkout config/tillr/workflows/planned-build@v1.json.
	got, fromRoot, err := ResolveWorkflowFile(repoRoot, "planned-build@v1")
	if err != nil {
		t.Fatalf("ResolveWorkflowFile(planned-build@v1) fallback: %v", err)
	}
	if fromRoot {
		t.Fatalf("expected fromRoot=false, got true (%q)", got)
	}
	if filepath.Base(got) != "planned-build@v1.json" {
		t.Fatalf("got %q, want planned-build@v1.json", got)
	}

	// Repo-local override takes precedence.
	localWF := filepath.Join(repoRoot, "config", "tillr", "workflows", "planned-build@v1.json")
	if err := os.MkdirAll(filepath.Dir(localWF), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(localWF, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	got, fromRoot, err = ResolveWorkflowFile(repoRoot, "planned-build@v1")
	if err != nil {
		t.Fatalf("ResolveWorkflowFile(planned-build@v1) local: %v", err)
	}
	if !fromRoot || got != localWF {
		t.Fatalf("got (%q, %v), want (%q, true)", got, fromRoot, localWF)
	}

	// TILLR_INSTALL_ROOT fallback.
	installRoot := t.TempDir()
	installedWF := filepath.Join(installRoot, "workflows", "custom@v2.json")
	if err := os.MkdirAll(filepath.Dir(installedWF), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedWF, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)
	got, fromRoot, err = ResolveWorkflowFile(repoRoot, "custom@v2")
	if err != nil {
		t.Fatalf("ResolveWorkflowFile(custom@v2) installed: %v", err)
	}
	if fromRoot || got != installedWF {
		t.Fatalf("got (%q, %v), want (%q, false)", got, fromRoot, installedWF)
	}

	// Invalid ref.
	if _, _, err := ResolveWorkflowFile(repoRoot, "bad-ref"); err == nil {
		t.Fatalf("expected error for invalid workflow ref")
	}
}

func TestResolveWorkflowsDirCheckoutToolsInstallRoot(t *testing.T) {
	checkout := t.TempDir()
	if err := os.MkdirAll(filepath.Join(checkout, "tools", "tillr-item"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checkout, "tools", "tillr-item", "go.mod"), []byte("module github.com/mschulkind-oss/tillr-item\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	wfDir := filepath.Join(checkout, "config", "tillr", "workflows")
	if err := os.MkdirAll(wfDir, 0o755); err != nil {
		t.Fatal(err)
	}
	wfFile := filepath.Join(wfDir, "planned-build@v1.json")
	if err := os.WriteFile(wfFile, []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TILLR_INSTALL_ROOT", filepath.Join(checkout, "tools"))
	got, fromRoot, err := ResolveWorkflowFile(t.TempDir(), "planned-build@v1")
	if err != nil {
		t.Fatalf("ResolveWorkflowFile with checkout tools install root: %v", err)
	}
	if fromRoot || got != wfFile {
		t.Fatalf("got (%q, %v), want (%q, false)", got, fromRoot, wfFile)
	}

	// Non-tillr <repo>/tools must NOT fall back to <repo>/config/tillr/workflows.
	consumer := t.TempDir()
	if err := os.MkdirAll(filepath.Join(consumer, "tools"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(consumer, "config", "tillr", "workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(consumer, "config", "tillr", "workflows", "planned-build@v1.json"), []byte("{}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_INSTALL_ROOT", filepath.Join(consumer, "tools"))
	if _, _, err := ResolveWorkflowFile(t.TempDir(), "planned-build@v1"); err == nil {
		t.Fatal("expected error for non-tillr consumer tools dir")
	}
}
