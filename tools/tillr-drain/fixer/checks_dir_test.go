package fixer_test

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/fixer"
)

func TestFixerHeavyDeclarationCustomChecksDir(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, ".tillr.toml"), []byte("[gate]\nchecks_dir = \"custom/checks\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(dir, "custom", "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "custom", "checks", "heavy-one.json"), []byte(`{"id":"heavy-one","scope":"tree","tier":"heavy"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fixer.FixerHeavyDeclaration(dir, "heavy-one") {
		t.Fatal("expected FixerHeavyDeclaration to find heavy-one in custom/checks")
	}

	// Absent .tillr.toml falls back to tools/gate/checks.
	defDir := t.TempDir()
	if err := os.MkdirAll(filepath.Join(defDir, "tools", "gate", "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(defDir, "tools", "gate", "checks", "heavy-def.json"), []byte(`{"id":"heavy-def","scope":"tree","tier":"heavy"}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if !fixer.FixerHeavyDeclaration(defDir, "heavy-def") {
		t.Fatal("expected FixerHeavyDeclaration to find heavy-def in default tools/gate/checks")
	}

	// Invalid .tillr.toml returns false rather than falling back.
	if err := os.WriteFile(filepath.Join(defDir, ".tillr.toml"), []byte("[gate]\nchecks_dir = \"../bad\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if fixer.FixerHeavyDeclaration(defDir, "heavy-def") {
		t.Fatal("expected FixerHeavyDeclaration to refuse invalid .tillr.toml")
	}
}
