package main_test

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

// fixtureRepo is a real checkout with two commits, so the harness's `git diff` names a
// real change rather than a fixture's path list.
func fixtureRepo(t *testing.T) (root, base string) {
	t.Helper()
	root = scratch(t, "fixture-repo")
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Gate Harness", "GIT_AUTHOR_EMAIL=harness@example.invalid",
			"GIT_COMMITTER_NAME=Gate Harness", "GIT_COMMITTER_EMAIL=harness@example.invalid")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %s failed: %v\n%s", strings.Join(args, " "), err, out)
		}
		return strings.TrimSpace(string(out))
	}
	git("init", "-q", "-b", "main")
	writeFile(t, root, "README.md", "first\n")
	git("add", "-A")
	git("commit", "-q", "-m", "first")
	base = git("rev-parse", "HEAD")
	writeFile(t, root, "docs/a.md", "a document\n")
	git("add", "-A")
	git("commit", "-q", "-m", "second")
	return root, base
}

func writeFile(t *testing.T, root, path, body string) {
	t.Helper()
	full := filepath.Join(root, path)
	if err := os.MkdirAll(filepath.Dir(full), 0o755); err != nil {
		t.Fatalf("creating %s failed: %v", path, err)
	}
	if err := os.WriteFile(full, []byte(body), 0o644); err != nil {
		t.Fatalf("writing %s failed: %v", path, err)
	}
}

// fixtureRegistry writes a registry whose digest is the one its own bytes hash to, which
// is what the loader checks. A case names the declarations and the in-process checks it is
// about; the digest is computed, never typed.
func builtRegistry(t *testing.T, declarations, inProcess []map[string]any) string {
	t.Helper()
	decl, err := json.Marshal(declarations)
	if err != nil {
		t.Fatalf("encoding the declarations failed: %v", err)
	}
	proc, err := json.Marshal(inProcess)
	if err != nil {
		t.Fatalf("encoding the in-process checks failed: %v", err)
	}
	sum := sha256.Sum256(append(append([]byte{}, decl...), proc...))
	body := `{"digest":"` + hex.EncodeToString(sum[:]) + `","declarations":` + string(decl) +
		`,"in_process":` + string(proc) + `}`
	path := filepath.Join(scratch(t, "fixture-registry"), "registry.json")
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		t.Fatalf("writing the registry failed: %v", err)
	}
	return path
}

// documentsDeclaration is the one check the harness cases plan: a Markdown change selects
// it, and its command is cheap so the Go run face finishes in a test.
func documentsDeclaration() map[string]any {
	return map[string]any{
		"id": "documents", "kind": "documents", "label": "{count} changed Markdown file(s)",
		"selects": []map[string]any{{"regex": `.*\.md$`, "must_exist": true}},
		"command": []string{"sh", "-c", "exit 0"},
		"watches": []map[string]any{{"paths": true}},
		"tier":    "landing", "timeout_ms": 30000,
	}
}
