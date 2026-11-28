package pathenv

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestStripExecutableFromPathRemovesTargetAndPreservesSiblings(t *testing.T) {
	binDir := t.TempDir()
	cacheRoot := t.TempDir()
	for _, name := range []string{"uvx", "keep-tool"} {
		path := filepath.Join(binDir, name)
		if err := os.WriteFile(path, []byte("#!/bin/sh\necho "+name+"\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}
	otherDir := t.TempDir()
	if err := os.WriteFile(filepath.Join(otherDir, "other-tool"), []byte("#!/bin/sh\necho other\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	orig := binDir + string(os.PathListSeparator) + otherDir
	got := StripExecutableFromPath(orig, "uvx", cacheRoot)
	if got == orig {
		t.Fatalf("expected PATH to change when uvx is present: %q", got)
	}

	t.Setenv("PATH", got)
	if found, err := exec.LookPath("uvx"); err == nil {
		t.Fatalf("expected uvx to be absent from stripped PATH, found %q", found)
	}
	if _, err := exec.LookPath("keep-tool"); err != nil {
		t.Fatalf("expected sibling keep-tool to remain resolvable: %v", err)
	}
	if _, err := exec.LookPath("other-tool"); err != nil {
		t.Fatalf("expected untouched dir tool to remain resolvable: %v", err)
	}
	parts := filepath.SplitList(got)
	if len(parts) != 2 || parts[1] != otherDir {
		t.Fatalf("expected untouched directory %q preserved in place, got %v", otherDir, parts)
	}
}

func TestStripExecutableFromPathLeavesUnmatchedPathUnchanged(t *testing.T) {
	binDir := t.TempDir()
	cacheRoot := t.TempDir()
	if err := os.WriteFile(filepath.Join(binDir, "go"), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}

	got := StripExecutableFromPath(binDir, "uvx", cacheRoot)
	if got != binDir {
		t.Fatalf("PATH changed when uvx was absent: got %q, want %q", got, binDir)
	}
	entries, err := os.ReadDir(cacheRoot)
	if err != nil {
		t.Fatal(err)
	}
	if len(entries) != 0 {
		t.Fatalf("expected no shadow dirs created when uvx is absent, found %d", len(entries))
	}
}

func TestStripExecutableFromPathReusesStableShadowDir(t *testing.T) {
	binDir := t.TempDir()
	cacheRoot := t.TempDir()
	for _, name := range []string{"uvx", "go"} {
		if err := os.WriteFile(filepath.Join(binDir, name), []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
			t.Fatal(err)
		}
	}

	first := StripExecutableFromPath(binDir, "uvx", cacheRoot)
	second := StripExecutableFromPath(binDir, "uvx", cacheRoot)
	if first != second {
		t.Fatalf("shadow PATH not stable across calls: %q != %q", first, second)
	}
	if !strings.HasPrefix(first, filepath.Join(cacheRoot, "go-test-no-uvx-bin")) {
		t.Fatalf("expected shadow dir under cacheRoot, got %q", first)
	}
}
