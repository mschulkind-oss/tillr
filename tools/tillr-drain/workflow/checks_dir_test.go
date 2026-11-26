package workflow

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestCheckNodeTasksResolvesBareCheckIDInCustomChecksDir(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[gate]\nchecks_dir = \"custom/checks\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "custom", "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "src"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "src", "main.go"), []byte("package main\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "tests", "other.py"), []byte("# src/main.go\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, "custom", "checks", "my-check.json"), []byte(`{"id":"my-check","reads":["src/main.go"],"command":["python3","tests/other.py"]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	err := ValidateNodeTask(root, "gate-check-ref", "write", []string{"src/"}, "Delete src/main.go and pass my-check.")
	if err == nil || !strings.Contains(err.Error(), "custom/checks/my-check.json") {
		t.Fatalf("expected error naming custom/checks/my-check.json, got %v", err)
	}
}
