package main

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func heavyMissTestGit(t *testing.T, root string, args ...string) string {
	t.Helper()
	cmd := exec.Command("git", args...)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_CONFIG_GLOBAL=/dev/null", "GIT_CONFIG_SYSTEM=/dev/null", "GIT_CONFIG_NOSYSTEM=1", "GIT_AUTHOR_NAME=Fixture", "GIT_AUTHOR_EMAIL=fixture@example.invalid", "GIT_COMMITTER_NAME=Fixture", "GIT_COMMITTER_EMAIL=fixture@example.invalid")
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("git %v: %v: %s", args, err, out)
	}
	return strings.TrimSpace(string(out))
}

func heavyMissTestWrite(t *testing.T, root, name, content string) {
	t.Helper()
	filename := filepath.Join(root, name)
	if err := os.MkdirAll(filepath.Dir(filename), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filename, []byte(content), 0644); err != nil {
		t.Fatal(err)
	}
}

func heavyMissTestCommit(t *testing.T, root string) string {
	t.Helper()
	heavyMissTestGit(t, root, "add", "-A")
	heavyMissTestGit(t, root, "commit", "--allow-empty", "-qm", "fixture")
	return heavyMissTestGit(t, root, "rev-parse", "HEAD")
}

func TestFullHeavySHA(t *testing.T) {
	for _, test := range []struct {
		sha  string
		want bool
	}{
		{strings.Repeat("a", 40), true},
		{strings.Repeat("0", 64), true},
		{"", false}, {"HEAD", false},
		{strings.Repeat("a", 39), false},
		{strings.Repeat("a", 41), false},
		{strings.Repeat("A", 40), false},
		{strings.Repeat("g", 40), false},
		{strings.Repeat("a", 40) + "\n", false},
	} {
		if got := fullHeavySHA(test.sha); got != test.want {
			t.Errorf("fullHeavySHA(%q) = %v, want %v", test.sha, got, test.want)
		}
	}
}

func TestHeavyMissPathsHistoricalModules(t *testing.T) {
	root := t.TempDir()
	heavyMissTestGit(t, root, "init", "-q")
	initial := map[string]string{
		"tools/alpha/go.mod":               "module alpha\n",
		"tools/alpha/nested/go.mod":        "module nested\n",
		"tools/alpha/nested/old name\n.go": "old\n",
		"tools/alpha/unchanged.go":         "old\n",
		"tools/deleted/go.mod":             "module deleted\n",
		"tools/deleted/file.go":            "old\n",
		"docs/outside.txt":                 "old\n",
		"yolo-packs/tillr/files/local/share/tillr/alpha/generated.go": "old\n",
	}
	for name, content := range initial {
		heavyMissTestWrite(t, root, name, content)
	}
	before := heavyMissTestCommit(t, root)
	for _, name := range []string{"tools/alpha/nested/go.mod", "tools/alpha/nested/old name\n.go", "tools/deleted/go.mod", "tools/deleted/file.go"} {
		if err := os.Remove(filepath.Join(root, name)); err != nil {
			t.Fatal(err)
		}
	}
	heavyMissTestWrite(t, root, "tools/alpha/nested/new name\n.go", "old\n")
	heavyMissTestWrite(t, root, "tools/new/go.mod", "module new\n")
	heavyMissTestWrite(t, root, "tools/new/file.go", "new\n")
	heavyMissTestWrite(t, root, "docs/outside.txt", "new\n")
	heavyMissTestWrite(t, root, "yolo-packs/tillr/files/local/share/tillr/alpha/generated.go", "new\n")
	after := heavyMissTestCommit(t, root)
	// Neither a dirty root module nor a configured relative diff may affect history.
	heavyMissTestWrite(t, root, "go.mod", "module misleading\n")
	heavyMissTestGit(t, root, "config", "diff.relative", "true")
	heavyMissTestGit(t, root, "config", "diff.external", "/must-not-run")
	paths, modules, err := heavyMissPaths(context.Background(), filepath.Join(root, "tools/alpha"), before, after)
	if err != nil {
		t.Fatal(err)
	}
	wantPaths := []string{
		"docs/outside.txt", "tools/alpha/nested/go.mod", "tools/alpha/nested/new name\n.go", "tools/alpha/nested/old name\n.go",
		"tools/deleted/file.go", "tools/deleted/go.mod", "tools/new/file.go", "tools/new/go.mod",
		"yolo-packs/tillr/files/local/share/tillr/alpha/generated.go",
	}
	if !reflect.DeepEqual(paths, wantPaths) {
		t.Errorf("paths = %q, want %q", paths, wantPaths)
	}
	wantModules := []string{"(non-module)", "tools/alpha", "tools/alpha/nested", "tools/deleted", "tools/new"}
	if !reflect.DeepEqual(modules, wantModules) {
		t.Errorf("modules = %q, want %q", modules, wantModules)
	}
}

func TestHeavyMissPathsRootEmptyAndInvalidEndpoints(t *testing.T) {
	for _, format := range []string{"sha1", "sha256"} {
		t.Run(format, func(t *testing.T) {
			root := t.TempDir()
			heavyMissTestGit(t, root, "init", "-q", "--object-format="+format)
			heavyMissTestWrite(t, root, "go.mod", "module root\n")
			before := heavyMissTestCommit(t, root)
			heavyMissTestWrite(t, root, "a space\nfile.go", "new\n")
			after := heavyMissTestCommit(t, root)
			paths, modules, err := heavyMissPaths(context.Background(), root, before, after)
			if err != nil || !reflect.DeepEqual(paths, []string{"a space\nfile.go"}) || !reflect.DeepEqual(modules, []string{"."}) {
				t.Fatalf("root result: %q %q %v", paths, modules, err)
			}
			paths, modules, err = heavyMissPaths(context.Background(), root, after, after)
			if err != nil || paths == nil || modules == nil || len(paths) != 0 || len(modules) != 0 {
				t.Fatalf("empty result: %#v %#v %v", paths, modules, err)
			}
			heavyMissTestGit(t, root, "tag", "-a", "fixture-tag", "-m", "tag")
			tag := heavyMissTestGit(t, root, "rev-parse", "fixture-tag")
			blob := heavyMissTestGit(t, root, "rev-parse", "HEAD:go.mod")
			for _, invalid := range []string{"HEAD", after[:12], strings.ToUpper(after), strings.Repeat("0", len(after)), tag, blob} {
				if _, _, err := heavyMissPaths(context.Background(), root, invalid, after); err == nil {
					t.Errorf("accepted invalid endpoint %q", invalid)
				}
			}
			if format == "sha256" {
				if _, _, err := heavyMissPaths(context.Background(), root, after[:40], after); err == nil {
					t.Error("accepted a 40-character abbreviation of a SHA-256 object")
				}
			}
			ctx, cancel := context.WithCancel(context.Background())
			cancel()
			if _, _, err := heavyMissPaths(ctx, root, before, after); err == nil {
				t.Error("accepted canceled context")
			}
		})
	}
}

func TestHeavyMissPathsDoesNotLazyFetchMissingTrees(t *testing.T) {
	root := t.TempDir()
	heavyMissTestGit(t, root, "init", "-q")
	heavyMissTestWrite(t, root, "file.txt", "before\n")
	before := heavyMissTestCommit(t, root)
	tree := heavyMissTestGit(t, root, "rev-parse", before+"^{tree}")
	heavyMissTestWrite(t, root, "file.txt", "after\n")
	after := heavyMissTestCommit(t, root)
	marker := filepath.Join(t.TempDir(), "fetch-attempted")
	uploadPack := filepath.Join(t.TempDir(), "upload-pack")
	if err := os.WriteFile(uploadPack, []byte("#!/bin/sh\ntouch '"+marker+"'\nexit 1\n"), 0755); err != nil {
		t.Fatal(err)
	}
	heavyMissTestGit(t, root, "config", "extensions.partialClone", "origin")
	heavyMissTestGit(t, root, "config", "remote.origin.promisor", "true")
	heavyMissTestGit(t, root, "config", "remote.origin.url", "file://"+root)
	heavyMissTestGit(t, root, "config", "remote.origin.uploadpack", uploadPack)
	heavyMissTestGit(t, root, "config", "protocol.file.allow", "always")
	if err := os.Remove(filepath.Join(root, ".git/objects", tree[:2], tree[2:])); err != nil {
		t.Fatal(err)
	}
	if _, _, err := heavyMissPaths(context.Background(), root, before, after); err == nil {
		t.Fatal("accepted a missing historical tree")
	}
	if _, err := os.Stat(marker); !os.IsNotExist(err) {
		t.Fatalf("lazy fetch was attempted: marker stat = %v", err)
	}
	// Positive control: this same missing object normally invokes the promisor.
	cmd := exec.Command("git", "cat-file", "-p", tree)
	cmd.Dir = root
	cmd.Env = append(os.Environ(), "GIT_NO_LAZY_FETCH=0")
	_, _ = cmd.CombinedOutput()
	if _, err := os.Stat(marker); err != nil {
		t.Fatalf("promisor fixture did not attempt fetch: %v", err)
	}
}

func TestHeavyMissPathsIgnoresReplacementObjects(t *testing.T) {
	root := t.TempDir()
	heavyMissTestGit(t, root, "init", "-q")
	heavyMissTestWrite(t, root, "original.txt", "first\n")
	before := heavyMissTestCommit(t, root)
	heavyMissTestWrite(t, root, "original.txt", "second\n")
	after := heavyMissTestCommit(t, root)
	heavyMissTestWrite(t, root, "replacement.txt", "replacement\n")
	replacement := heavyMissTestCommit(t, root)
	heavyMissTestGit(t, root, "replace", after, replacement)
	paths, modules, err := heavyMissPaths(context.Background(), root, before, after)
	if err != nil || !reflect.DeepEqual(paths, []string{"original.txt"}) || !reflect.DeepEqual(modules, []string{"(non-module)"}) {
		t.Fatalf("replacement changed report: %q %q %v", paths, modules, err)
	}
}
