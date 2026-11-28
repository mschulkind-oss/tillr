package main

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// recipeBlock returns the per-module partition block of the `test` recipe in .justfile.
func recipeBlock(t *testing.T) string {
	t.Helper()
	raw, err := os.ReadFile(filepath.Join("..", "..", ".justfile"))
	if err != nil {
		t.Skipf("no .justfile above this module (a pack mirror copy): %v", err)
	}
	s := string(raw)
	start := strings.Index(s, "# Go's -skip has no package qualifier")
	end := strings.Index(s[start:], "        fi\n    done\n")
	if start < 0 || end < 0 {
		t.Fatal("the test recipe's disabled-args block is not where the test expects it")
	}
	return s[start : start+end]
}

// runRecipe runs the recipe block over module tools/m of a fixture repository and returns
// its combined output.
func runRecipe(t *testing.T, root string) (string, error) {
	t.Helper()
	bin := filepath.Join(t.TempDir(), "tillr-gate")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v\n%s", err, out)
	}
	block := strings.ReplaceAll(recipeBlock(t), "go run -C tools/tillr-gate .", bin)
	script := "set -euo pipefail\nset -- -v\nfor dir in tools/m; do\nif true; then\n" + block + "fi\ndone\n"
	cmd := exec.Command("bash", "-c", script)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GOFLAGS=-mod=mod", "GOWORK=off")
	out, err := cmd.CombinedOutput()
	return string(out), err
}

func TestJustTestRecipeSkipsListedTestsOnlyInTheirPackage(t *testing.T) {
	root := writeDisabledArgsRepo(t, disabledArgsList)
	writeFiles := map[string]string{"tools/m/d/d.go": "package d\n"}
	for path, body := range writeFiles {
		full := filepath.Join(root, filepath.FromSlash(path))
		if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runRecipe(t, root)
	if err != nil {
		t.Fatalf("recipe failed: %v\n%s", err, out)
	}
	for _, want := range []string{"--- PASS: TestKept", "--- PASS: TestParent/good", "ok  \texample.com/m/c", "example.com/m/d\t[no test files]"} {
		if !strings.Contains(out, want) {
			t.Fatalf("missing %q in:\n%s", want, out)
		}
	}
	// c's same-named TestShared runs; a's listed one and b's listed TestOther do not.
	if strings.Count(out, "--- PASS: TestShared") != 2 || strings.Contains(out, "TestOther") || strings.Contains(out, "bad.one") {
		t.Fatalf("listed tests must be skipped only in their own package:\n%s", out)
	}
}

func TestJustTestRecipeWithAnEmptyListRunsOneGoTestOverTheModule(t *testing.T) {
	root := writeDisabledArgsRepo(t, `{"version":1,"disabled":[]}`)
	pass := "package %s\n\nimport \"testing\"\n\nfunc TestShared(t *testing.T) {}\n"
	for _, pkg := range []string{"a", "b"} {
		body := strings.Replace(pass, "%s", pkg, 1)
		if err := os.WriteFile(filepath.Join(root, "tools", "m", pkg, pkg+"_test.go"), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	out, err := runRecipe(t, root)
	if err != nil {
		t.Fatalf("recipe failed: %v\n%s", err, out)
	}
	if strings.Count(out, "--- PASS: TestShared") != 3 {
		t.Fatalf("every package's TestShared runs when nothing is listed:\n%s", out)
	}
}
