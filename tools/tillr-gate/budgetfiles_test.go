package main_test

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestBudgetDeclarationsPlanAndRunTheirOwnDirectory(t *testing.T) {
	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	source := filepath.Join(root, "tools", "gate", "checks")
	// Runs must use a fixture Git repository and its own store, never the owner's
	// common Git directory. Copy the programs beside their declarations.
	root = gitRepo(t)
	dir := filepath.Join(root, "tools", "gate", "checks")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	entries, err := os.ReadDir(source)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasPrefix(entry.Name(), "gate-budget") {
			continue
		}
		raw, err := os.ReadFile(filepath.Join(source, entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, entry.Name()), raw, 0o644); err != nil {
			t.Fatal(err)
		}
	}
	gateDir := filepath.Join(root, "tools", "gate")
	gateEntries, err := os.ReadDir(filepath.Join("..", "gate"))
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range gateEntries {
		if entry.IsDir() {
			continue
		}
		raw, err := os.ReadFile(filepath.Join("..", "gate", entry.Name()))
		if err != nil {
			t.Fatal(err)
		}
		mode := os.FileMode(0o644)
		if entry.Name() == "repo-gate" {
			mode = 0o755
		}
		if err := os.WriteFile(filepath.Join(gateDir, entry.Name()), raw, mode); err != nil {
			t.Fatal(err)
		}
	}
	absTillr, err := filepath.Abs(filepath.Join("..", "tillr"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(absTillr, filepath.Join(root, "tools", "tillr")); err != nil {
		t.Fatal(err)
	}
	git(t, root, "add", ".")
	git(t, root, "-c", "user.name=fixture", "-c", "user.email=fixture@example.invalid", "-c", "commit.gpgsign=false", "commit", "-qm", "checks")
	code, out := filePlan(t, dir, []string{"tools/gate/gate_budget.go"}, git(t, root, "rev-parse", "HEAD^{tree}"))
	if code != 0 {
		t.Fatalf("plan exit %d: %s", code, out)
	}
	checks := decodedFilePlan(t, out)
	if len(checks) != 2 {
		t.Fatalf("budget and its test not planned: %s", out)
	}
	for _, check := range checks {
		if check["check_id"] == "gate-budget" {
			if !reflect.DeepEqual(check["reads"], []any{"tools/gate/checks/"}) {
				t.Fatalf("gate-budget reads beyond its directory: %v", check)
			}
		}
	}
	planPath := filepath.Join(scratch(t, "budget-plan"), "plan.json")
	if err := os.WriteFile(planPath, out, 0o644); err != nil {
		t.Fatal(err)
	}
	code, out = run(t, "run", "--input", planPath, "--worktree", root, "--logs", filepath.Join(scratch(t, "budget-run"), "logs"))
	if code != 0 {
		t.Fatalf("run exit %d: %s", code, out)
	}
	code, out = filePlan(t, dir, []string{"unrelated/file"})
	if code != 0 {
		t.Fatalf("plan exit %d: %s", code, out)
	}
	checks = decodedFilePlan(t, out)
	if len(checks) != 1 || checks[0]["check_id"] != "gate-budget" {
		t.Fatalf("unrelated change selected budget test: %s", out)
	}
}
