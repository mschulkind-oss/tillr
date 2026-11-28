package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-gate/shards"
)

func TestCheckEnvStripsUvxUnlessDeclaredInTools(t *testing.T) {
	binDir := t.TempDir()
	for _, name := range []string{"uvx", "sibling-tool"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("GOCACHE", t.TempDir())

	t.Run("stripped when uvx is not declared", func(t *testing.T) {
		env := shards.EnvSliceToMap(checkEnv(PlanCheck{
			CheckID: "go-test-example",
			Tools:   []string{"sh", "go", "git"},
		}))
		t.Setenv("PATH", env["PATH"])
		if found, err := exec.LookPath("uvx"); err == nil {
			t.Fatalf("expected uvx stripped from check PATH, found %q", found)
		}
		if _, err := exec.LookPath("sibling-tool"); err != nil {
			t.Fatalf("expected sibling-tool kept on check PATH: %v", err)
		}
	})

	t.Run("preserved when uvx is declared", func(t *testing.T) {
		env := shards.EnvSliceToMap(checkEnv(PlanCheck{
			CheckID: "documents",
			Tools:   []string{"uvx"},
		}))
		t.Setenv("PATH", env["PATH"])
		if _, err := exec.LookPath("uvx"); err != nil {
			t.Fatalf("expected uvx kept when declared in Tools: %v", err)
		}
	})
}
