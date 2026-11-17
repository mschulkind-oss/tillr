package main

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGoBuildMemoryBindsInheritedBuildFlags(t *testing.T) {
	root, tree := memoryRepo(t)
	check := memoryCheck(t.TempDir() + "/started")
	check.CheckID = "go-build"
	check.Tools = []string{"go"}
	t.Setenv("GOFLAGS", "-tags=first")
	first, err := memoryRun(t, root, tree, check)
	if err != nil || first.Memory != "passed" {
		t.Fatalf("first build: %+v, %v", first, err)
	}
	t.Setenv("GOFLAGS", "-tags=second")
	second, err := memoryRun(t, root, tree, check)
	if err != nil || second.Memory != "passed" || second.Fingerprint == first.Fingerprint {
		t.Fatalf("changed build flags reused a pass: %+v, %v", second, err)
	}
	third, err := memoryRun(t, root, tree, check)
	if err != nil || third.Memory != "remembered" {
		t.Fatalf("unchanged flags were not remembered: %+v, %v", third, err)
	}
}

func TestGoBuildDoesNotRememberFileFlagsOrWorkspaces(t *testing.T) {
	for _, mode := range []string{"overlay", "modfile", "compiler-options", "workspace", "nested-workspace", "symlink"} {
		t.Run(mode, func(t *testing.T) {
			root, _ := memoryRepo(t)
			check := memoryCheck(filepath.Join(t.TempDir(), "started"))
			check.CheckID, check.Tools = "go-build", []string{"go"}
			switch mode {
			case "overlay":
				file := filepath.Join(root, "overlay.json")
				memoryWrite(t, file, `{"Replace":{}}`)
				t.Setenv("GOFLAGS", "-overlay="+file)
			case "compiler-options":
				t.Setenv("GOFLAGS", "-gcflags=all=-N")
			case "modfile":
				file := filepath.Join(root, "alternate.mod")
				memoryWrite(t, file, "module fixture\n\ngo 1.24\n")
				t.Setenv("GOFLAGS", "-modfile="+file)
			case "workspace":
				file := filepath.Join(root, "go.work")
				memoryWrite(t, file, "go 1.24\nuse ./input\n")
				memoryWrite(t, filepath.Join(root, "input/go.mod"), "module fixture\n\ngo 1.24\n")
				t.Setenv("GOWORK", file)
			case "nested-workspace":
				t.Setenv("GOWORK", "")
				memoryWrite(t, filepath.Join(root, "input/go.work"), "go 1.24\nuse .\n")
				memoryWrite(t, filepath.Join(root, "input/go.mod"), "module fixture\n\ngo 1.24\n")
			case "symlink":
				memoryWrite(t, filepath.Join(root, "input/go.mod"), "module fixture\n\ngo 1.24\nreplace fixture/b => ./dep\n")
				memoryWrite(t, filepath.Join(root, "lib/b/go.mod"), "module fixture/b\n\ngo 1.24\n")
				if err := os.Symlink(filepath.Join(root, "lib/b"), filepath.Join(root, "input/dep")); err != nil {
					t.Fatal(err)
				}
			}
			memoryGit(t, root, "add", ".")
			if memoryGit(t, root, "status", "--porcelain") != "" {
				memoryGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "options")
			}
			tree := memoryGit(t, root, "rev-parse", "HEAD^{tree}")
			for i := 0; i < 2; i++ {
				got, err := memoryRun(t, root, tree, check)
				if err != nil || got.Result != ResultPass || got.Memory != "not remembered" {
					t.Fatalf("run %d: %+v, %v", i, got, err)
				}
			}
			if files := memoryFiles(t, memoryStore(t, root)); len(files) != 0 {
				t.Fatalf("unsafe options published memory: %v", files)
			}
		})
	}
}
