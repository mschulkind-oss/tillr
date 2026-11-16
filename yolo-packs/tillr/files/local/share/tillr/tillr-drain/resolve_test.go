package main

import (
	"os"
	"path/filepath"
	"testing"
)

// TestResolveTraceBinPrefersTheRepositoryCopy pins the default the loop runs with when the
// caller names no --trace-bin: the indexer is a script beside the Go tree, not a binary on
// PATH, so a checkout's `tools/tillr-trace` is the copy that matches the code being tested.
func TestResolveTraceBinPrefersTheRepositoryCopy(t *testing.T) {
	root := scratchDir(t)
	path := filepath.Join(root, "tools", "tillr-trace", "tillr-trace")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating the fixture trace directory failed: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("writing the fixture trace program failed: %v", err)
	}

	if got := resolveTraceBin(root, "", false); got != path {
		t.Fatalf("resolveTraceBin(%q, \"\", false) = %q, want the repository's %q", root, got, path)
	}
	named := filepath.Join(root, "named-tillr-trace")
	if got := resolveTraceBin(root, named, true); got != named {
		t.Fatalf("an explicit --trace-bin was answered with %q, want %q", got, named)
	}
}

// TestResolveTraceBinAnswersAnExplicitEmptyValueWithNothing is the other half of the same
// question: a named program and a DISABLED step are different requests, and one string
// cannot carry both. Before this, `--trace-bin ""` fell through to the search and silently
// re-enabled the step the operator had just turned off.
func TestResolveTraceBinAnswersAnExplicitEmptyValueWithNothing(t *testing.T) {
	root := scratchDir(t)
	path := filepath.Join(root, "tools", "tillr-trace", "tillr-trace")
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("creating the fixture trace directory failed: %v", err)
	}
	if err := os.WriteFile(path, []byte("#!/bin/sh\n"), 0o755); err != nil {
		t.Fatalf("writing the fixture trace program failed: %v", err)
	}

	if got := resolveTraceBin(root, "", true); got != "" {
		t.Fatalf("an explicit empty --trace-bin was answered with %q, want the disabled step", got)
	}
	if got := resolveTraceBin(root, "named", true); got != "named" {
		t.Fatalf("an explicit --trace-bin was answered with %q, want it unchanged", got)
	}
}

// scratchDir is a fixture directory under the durable scratch root, never /tmp: the suite's
// evidence goes where it survives the box's cleanup.
func scratchDir(t *testing.T) string {
	t.Helper()
	base := os.Getenv("YOLO_DURABLE_DIR")
	if base == "" {
		base = filepath.Join("..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(base, 0o755); err != nil {
		t.Fatalf("creating the scratch root failed: %v", err)
	}
	dir, err := os.MkdirTemp(base, "resolve-trace-bin-")
	if err != nil {
		t.Fatalf("creating the scratch directory failed: %v", err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	return dir
}
