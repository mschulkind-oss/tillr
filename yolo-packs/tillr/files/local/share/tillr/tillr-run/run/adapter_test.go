package run_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

func TestResolveBackendsDirAndBriefTemplate(t *testing.T) {
	dir := t.TempDir()
	installRoot := filepath.Join(dir, "install")
	installedBackends := filepath.Join(installRoot, "tillr-run", "backends")
	installedTemplate := filepath.Join(installedBackends, "brief-template.md")
	if err := os.MkdirAll(installedBackends, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(installedTemplate, []byte("# brief\n"), 0644); err != nil {
		t.Fatal(err)
	}

	consumer := filepath.Join(dir, "consumer")
	if err := os.MkdirAll(consumer, 0755); err != nil {
		t.Fatal(err)
	}

	origWD, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(consumer); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chdir(origWD) })

	// (a) Consumer repo with no tools/tillr-run/backends succeeds via TILLR_INSTALL_ROOT.
	t.Setenv("TILLR_INSTALL_ROOT", installRoot)
	gotBackends, err := run.ResolveBackendsDir()
	if err != nil {
		t.Fatalf("ResolveBackendsDir: %v", err)
	}
	if gotBackends != installedBackends {
		t.Fatalf("ResolveBackendsDir = %q, want %q", gotBackends, installedBackends)
	}
	gotTemplate, err := run.ResolveBriefTemplate()
	if err != nil {
		t.Fatalf("ResolveBriefTemplate: %v", err)
	}
	if gotTemplate != installedTemplate {
		t.Fatalf("ResolveBriefTemplate = %q, want %q", gotTemplate, installedTemplate)
	}

	// (b) Decoy <consumer>/tools/tillr-run/backends is ignored when TILLR_INSTALL_ROOT is set.
	decoyBackends := filepath.Join(consumer, "tools", "tillr-run", "backends")
	if err := os.MkdirAll(decoyBackends, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(decoyBackends, "brief-template.md"), []byte("# decoy\n"), 0644); err != nil {
		t.Fatal(err)
	}
	gotBackends, err = run.ResolveBackendsDir()
	if err != nil {
		t.Fatalf("ResolveBackendsDir with decoy: %v", err)
	}
	if gotBackends != installedBackends {
		t.Fatalf("ResolveBackendsDir with decoy = %q, want %q", gotBackends, installedBackends)
	}
	gotTemplate, err = run.ResolveBriefTemplate()
	if err != nil {
		t.Fatalf("ResolveBriefTemplate with decoy: %v", err)
	}
	if gotTemplate != installedTemplate {
		t.Fatalf("ResolveBriefTemplate with decoy = %q, want %q", gotTemplate, installedTemplate)
	}

	// (c) Missing installed template or directory is refused naming the installed path (no fallback to decoy).
	if err := os.Remove(installedTemplate); err != nil {
		t.Fatal(err)
	}
	if _, err := run.ResolveBriefTemplate(); err == nil || !strings.Contains(err.Error(), installedTemplate) {
		t.Fatalf("missing template err = %v, want path %q", err, installedTemplate)
	}
	if err := os.RemoveAll(installedBackends); err != nil {
		t.Fatal(err)
	}
	if _, err := run.ResolveBackendsDir(); err == nil || !strings.Contains(err.Error(), installedBackends) {
		t.Fatalf("missing backends dir err = %v, want path %q", err, installedBackends)
	}

	// Relative TILLR_INSTALL_ROOT is refused.
	t.Setenv("TILLR_INSTALL_ROOT", "relative/install")
	if _, err := run.ResolveBackendsDir(); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative TILLR_INSTALL_ROOT backends err = %v, want absolute refusal", err)
	}
	if _, err := run.ResolveBriefTemplate(); err == nil || !strings.Contains(err.Error(), "absolute") {
		t.Fatalf("relative TILLR_INSTALL_ROOT template err = %v, want absolute refusal", err)
	}

	// Unset TILLR_INSTALL_ROOT in consumer repo with decoy is refused.
	t.Setenv("TILLR_INSTALL_ROOT", "")
	if _, err := run.ResolveBackendsDir(); err == nil {
		t.Fatal("expected refusal in consumer repo without TILLR_INSTALL_ROOT")
	}
	if _, err := run.ResolveBriefTemplate(); err == nil {
		t.Fatal("expected refusal in consumer repo without TILLR_INSTALL_ROOT")
	}

	// Unset TILLR_INSTALL_ROOT from a source checkout's tools/<mod>/<subpkg> cwd resolves the checkout.
	sourceCheckout := filepath.Join(dir, "source")
	sourceRunMod := filepath.Join(sourceCheckout, "tools", "tillr-run")
	sourceBackends := filepath.Join(sourceRunMod, "backends")
	sourceTemplate := filepath.Join(sourceBackends, "brief-template.md")
	drainSubpkg := filepath.Join(sourceCheckout, "tools", "tillr-drain", "drain")
	if err := os.MkdirAll(sourceBackends, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(drainSubpkg, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sourceRunMod, "go.mod"), []byte("module github.com/mschulkind-oss/tillr-run\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(sourceTemplate, []byte("# source\n"), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.Chdir(drainSubpkg); err != nil {
		t.Fatal(err)
	}
	gotBackends, err = run.ResolveBackendsDir()
	if err != nil {
		t.Fatalf("ResolveBackendsDir from tools/tillr-drain/drain: %v", err)
	}
	if gotBackends != sourceBackends {
		t.Fatalf("ResolveBackendsDir from tools/tillr-drain/drain = %q, want %q", gotBackends, sourceBackends)
	}
	gotTemplate, err = run.ResolveBriefTemplate()
	if err != nil {
		t.Fatalf("ResolveBriefTemplate from tools/tillr-drain/drain: %v", err)
	}
	if gotTemplate != sourceTemplate {
		t.Fatalf("ResolveBriefTemplate from tools/tillr-drain/drain = %q, want %q", gotTemplate, sourceTemplate)
	}
}
