package main

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func initGoVetFixtureRepo(t *testing.T, files map[string]string) string {
	t.Helper()
	dir := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init", "-q")
	runGit("config", "user.name", "Test")
	runGit("config", "user.email", "test@example.invalid")

	writeGoVetFixtureFiles(t, dir, files)
	runGit("add", ".")
	runGit("commit", "-q", "-m", "base")
	return dir
}

func writeGoVetFixtureFiles(t *testing.T, dir string, files map[string]string) {
	t.Helper()
	for rel, content := range files {
		p := filepath.Join(dir, filepath.FromSlash(rel))
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
	}
}

func commitGoVetFixture(t *testing.T, dir, msg string) (string, string) {
	t.Helper()
	runGit := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", dir}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	base := runGit("rev-parse", "HEAD")
	runGit("add", ".")
	runGit("commit", "-q", "-m", msg)
	head := runGit("rev-parse", "HEAD")
	return base, head
}

func TestGoVetCheckDeclaration(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("checks", "go-vet.json"))
	if err != nil {
		t.Fatalf("read go-vet.json: %v", err)
	}
	var decl struct {
		ID        string   `json:"id"`
		Scope     string   `json:"scope"`
		Tier      string   `json:"tier"`
		TimeoutMS int      `json:"timeout_ms"`
		Reads     []string `json:"reads"`
		Remember  *bool    `json:"remember"`
	}
	if err := json.Unmarshal(raw, &decl); err != nil {
		t.Fatalf("parse go-vet.json: %v", err)
	}
	if decl.ID != "go-vet" {
		t.Fatalf("id = %q, want go-vet", decl.ID)
	}
	if decl.Scope != "change" {
		t.Fatalf("scope = %q, want change", decl.Scope)
	}
	if decl.Tier != "heavy" {
		t.Fatalf("tier = %q, want heavy", decl.Tier)
	}
	if decl.TimeoutMS <= 0 {
		t.Fatalf("timeout_ms = %d, want > 0", decl.TimeoutMS)
	}
	if decl.Remember == nil || *decl.Remember {
		t.Fatalf("remember = %v, want false", decl.Remember)
	}
	hasToolsRead := false
	for _, r := range decl.Reads {
		if r == "tools/" {
			hasToolsRead = true
		}
	}
	if !hasToolsRead {
		t.Fatalf("reads = %v, want tools/", decl.Reads)
	}

	total, refusals, err := totalBudget("checks")
	if err != nil {
		t.Fatalf("totalBudget: %v", err)
	}
	if len(refusals) > 0 {
		t.Fatalf("gate-budget refusals: %v", refusals)
	}
	if total > LimitMS {
		t.Fatalf("fast budget %d > %d", total, LimitMS)
	}
}

func TestGoVetSelectionAndDependents(t *testing.T) {
	dir := initGoVetFixtureRepo(t, map[string]string{
		"tools/a/go.mod": "module example.invalid/a\n\ngo 1.21\n",
		"tools/a/a.go":   "package a\n\nfunc Value() int { return 1 }\n",
		"tools/b/go.mod": "module example.invalid/b\n\ngo 1.21\n\nrequire example.invalid/a v0.0.0\n\nreplace example.invalid/a => ../a\n",
		"tools/b/b.go":   "package b\n\nimport \"example.invalid/a\"\n\nfunc Double() int { return a.Value() * 2 }\n",
		"tools/c/go.mod": "module example.invalid/c\n\ngo 1.21\n",
		"tools/c/c.go":   "package c\n\nfunc Other() int { return 3 }\n",
	})

	writeGoVetFixtureFiles(t, dir, map[string]string{
		"tools/a/a.go": "package a\n\nfunc Value() int { return 2 }\n",
	})
	base, head := commitGoVetFixture(t, dir, "update a")

	var stdout, stderr bytes.Buffer
	code := runGoVet(dir, base, head, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("runGoVet failed (%d):\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	out := stdout.String()
	if !strings.Contains(out, "vetting tools/a") {
		t.Fatalf("expected tools/a to be vetted, got:\n%s", out)
	}
	if !strings.Contains(out, "vetting tools/b") {
		t.Fatalf("expected dependent tools/b to be vetted, got:\n%s", out)
	}
	if strings.Contains(out, "vetting tools/c") {
		t.Fatalf("did not expect unrelated tools/c to be vetted, got:\n%s", out)
	}

	writeGoVetFixtureFiles(t, dir, map[string]string{
		"docs/readme.md": "# docs\n",
	})
	base, head = commitGoVetFixture(t, dir, "docs only")
	stdout.Reset()
	stderr.Reset()
	code = runGoVet(dir, base, head, &stdout, &stderr)
	if code != 0 {
		t.Fatalf("docs-only runGoVet failed (%d): %s", code, stderr.String())
	}
	if !strings.Contains(stdout.String(), "no affected Go modules") {
		t.Fatalf("expected no affected Go modules, got:\n%s", stdout.String())
	}
}

func TestGoVetRefusesVetFailure(t *testing.T) {
	dir := initGoVetFixtureRepo(t, map[string]string{
		"tools/a/go.mod":  "module example.invalid/a\n\ngo 1.21\n",
		"tools/a/main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Printf(\"%s\", \"ok\") }\n",
	})

	writeGoVetFixtureFiles(t, dir, map[string]string{
		"tools/a/main.go": "package main\n\nimport \"fmt\"\n\nfunc main() { fmt.Printf(\"%d\", \"not-an-int\") }\n",
	})
	base, head := commitGoVetFixture(t, dir, "introduce vet failure")

	var stdout, stderr bytes.Buffer
	code := runGoVet(dir, base, head, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runGoVet = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "tools/a") || !strings.Contains(combined, "Printf") {
		t.Fatalf("expected vet diagnostic for tools/a Printf, got:\n%s", combined)
	}
}

func TestGoVetRefusesDependentVetFailure(t *testing.T) {
	dir := initGoVetFixtureRepo(t, map[string]string{
		"tools/a/go.mod": "module example.invalid/a\n\ngo 1.21\n",
		"tools/a/a.go":   "package a\n\ntype Pair struct { X, Y int }\n",
		"tools/b/go.mod": "module example.invalid/b\n\ngo 1.21\n\nrequire example.invalid/a v0.0.0\n\nreplace example.invalid/a => ../a\n",
		"tools/b/b.go":   "package b\n\nimport \"example.invalid/a\"\n\nfunc Make() a.Pair { return a.Pair{1, 2} }\n",
	})

	writeGoVetFixtureFiles(t, dir, map[string]string{
		"tools/a/a.go": "package a\n\n// updated\ntype Pair struct { X, Y int }\n",
	})
	base, head := commitGoVetFixture(t, dir, "touch a")

	var stdout, stderr bytes.Buffer
	code := runGoVet(dir, base, head, &stdout, &stderr)
	if code != 1 {
		t.Fatalf("runGoVet = %d, want 1\nstdout:\n%s\nstderr:\n%s", code, stdout.String(), stderr.String())
	}
	combined := stdout.String() + stderr.String()
	if !strings.Contains(combined, "tools/b") {
		t.Fatalf("expected failure in dependent tools/b, got:\n%s", combined)
	}
}
