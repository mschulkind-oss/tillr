package main

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr/record"
)

func initChecksDirRepo(t *testing.T) (string, string, string) {
	t.Helper()
	root := t.TempDir()
	git := func(args ...string) string {
		t.Helper()
		cmd := exec.Command("git", append([]string{"-C", root, "-c", "core.hooksPath=/dev/null", "-c", "user.name=Test", "-c", "user.email=test@example.invalid"}, args...)...)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
		return string(out)
	}
	write := func(rel, body string) {
		t.Helper()
		p := filepath.Join(root, rel)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
	}
	writeDecl := func(relDir, id string) {
		raw, err := json.Marshal(map[string]any{
			"id":        id,
			"label":     id,
			"scope":     "tree",
			"tier":      "fast",
			"budget_ms": 1000,
			"command":   []string{"true"},
		})
		if err != nil {
			t.Fatal(err)
		}
		write(filepath.Join(relDir, id+".json"), string(raw))
	}

	git("init", "-q")
	writeDecl("tools/gate/checks", "default-check")
	write("README.md", "# Base\n")
	git("add", ".")
	git("commit", "-qm", "base")
	base := git("rev-parse", "HEAD")
	base = base[:len(base)-1]

	write(".tillr.toml", "[gate]\nchecks_dir = \"custom/checks\"\n")
	writeDecl("custom/checks", "custom-check")
	writeDecl("override/checks", "override-check")
	write("README.md", "# Head\n")
	git("add", ".")
	git("commit", "-qm", "head")
	head := git("rev-parse", "HEAD")
	head = head[:len(head)-1]
	return root, base, head
}

func TestChecksDirFaceAndOverrideAndRefusal(t *testing.T) {
	root, base, head := initChecksDirRepo(t)
	logs := filepath.Join(t.TempDir(), "logs")

	// Omitting --checks reads [gate] checks_dir from .tillr.toml
	if code := checksFace([]string{"--fast-tree", "--base", base, "--head", head, "--worktree", root, "--logs", logs}); code != 0 {
		t.Fatalf("checksFace custom checks_dir exited %d", code)
	}

	// Explicit --checks overrides .tillr.toml
	if code := checksFace([]string{"--fast-tree", "--base", base, "--head", head, "--worktree", root, "--checks", "override/checks", "--logs", logs + "-over"}); code != 0 {
		t.Fatalf("checksFace --checks override exited %d", code)
	}

	// Absent .tillr.toml defaults to tools/gate/checks (base commit has no .tillr.toml)
	cmd := exec.Command("git", "-C", root, "checkout", "-q", base)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git checkout base: %v\n%s", err, out)
	}
	if code := checksFace([]string{"--fast-tree", "--base", base, "--head", base, "--worktree", root, "--logs", logs + "-base"}); code != 0 {
		t.Fatalf("checksFace absent .tillr.toml exited %d", code)
	}

	// Malformed .tillr.toml exits ExitExternal (4)
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[gate]\nchecks_dir = \"../bad\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if code := checksFace([]string{"--fast-tree", "--base", base, "--head", base, "--worktree", root, "--logs", logs + "-bad"}); code != record.ExitExternal {
		t.Fatalf("checksFace invalid .tillr.toml exited %d, want %d", code, record.ExitExternal)
	}
}

func TestChecksDirClassifyRepoLoader(t *testing.T) {
	root, base, head := initChecksDirRepo(t)
	loader, err := newClassifyRepoLoader(root)
	if err != nil {
		t.Fatal(err)
	}

	baseSet, err := loader.commitSet(base)
	if err != nil {
		t.Fatal(err)
	}
	if len(baseSet.declarations) != 1 || baseSet.declarations[0].ID != "default-check" {
		t.Fatalf("base commitSet declarations = %+v, want default-check", baseSet.declarations)
	}

	headSet, err := loader.commitSet(head)
	if err != nil {
		t.Fatal(err)
	}
	if len(headSet.declarations) != 1 || headSet.declarations[0].ID != "custom-check" {
		t.Fatalf("head commitSet declarations = %+v, want custom-check", headSet.declarations)
	}

	wtSet, err := loader.worktreeSet(".", head)
	if err != nil {
		t.Fatal(err)
	}
	if len(wtSet.declarations) != 1 || wtSet.declarations[0].ID != "custom-check" {
		t.Fatalf("worktreeSet declarations = %+v, want custom-check", wtSet.declarations)
	}

	// Malformed .tillr.toml in worktree refuses
	if err := os.WriteFile(filepath.Join(root, ".tillr.toml"), []byte("[gate]\nchecks_dir = \".git\"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if _, err := loader.worktreeSet("bad-wt", head); err == nil {
		// note: "bad-wt" resolves to <repo>/bad-wt which has no .tillr.toml; test "." with fresh loader
	}
	freshLoader, err := newClassifyRepoLoader(root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := freshLoader.worktreeSet(".", head); err == nil {
		t.Fatalf("expected error for invalid .tillr.toml in worktreeSet")
	}
}
