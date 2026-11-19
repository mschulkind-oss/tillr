package main

import (
	"os"
	"path/filepath"
	"strings"
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

func TestGoModuleIdentityCachesByGoModAndGoSumContentAndRefreshesOnChange(t *testing.T) {
	root, _ := memoryRepo(t)
	memoryWrite(t, filepath.Join(root, "input/go.mod"), "module fixture\n\ngo 1.24\n")
	memoryWrite(t, filepath.Join(root, "input/go.sum"), "")
	memoryGit(t, root, "add", ".")
	memoryGit(t, root, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "module")
	tree := memoryGit(t, root, "rev-parse", "HEAD^{tree}")

	// Build a fake `go` wrapper that counts `go env` and `go mod edit` invocations.
	binDir := t.TempDir()
	logFile := filepath.Join(t.TempDir(), "go-calls.log")
	realGo, err := os.Readlink("/proc/self/exe")
	_ = realGo
	wrapper := filepath.Join(binDir, "go")
	script := `#!/bin/sh
echo "$*" >> "` + logFile + `"
if [ "$1" = "env" ] && [ "$2" = "-json" ]; then
  echo '{"GOWORK":"","GOFLAGS":""}'
  exit 0
fi
if [ "$1" = "mod" ] && [ "$2" = "edit" ] && [ "$3" = "-json" ]; then
  if grep -q "replace " go.mod; then
    echo '{"Replace":[{"New":{"Path":"../outside"}}]}'
  else
    echo '{}'
  fi
  exit 0
fi
if [ "$1" = "version" ]; then
  echo "go version go1.24.0 linux/amd64"
  exit 0
fi
exit 1
`
	if err := os.WriteFile(wrapper, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+":"+os.Getenv("PATH"))

	// Clear in-memory caches so this test observes exact invocations.
	goEnvMu.Lock()
	goEnvCache = map[goEnvCacheKey]map[string]string{}
	goEnvMu.Unlock()
	goModMu.Lock()
	goModCache = map[string]cachedModuleIdentity{}
	goModMu.Unlock()

	check := memoryCheck(filepath.Join(t.TempDir(), "started"))
	check.CheckID = "go-build"
	check.Tools = []string{"go"}
	inputs, err := readTreeInputs(root, tree)
	if err != nil {
		t.Fatal(err)
	}

	// First call: invokes `go env -json` once and `go mod edit -json` once.
	_, reason1, err := goBuildIdentity(check, root, inputs)
	if err != nil || reason1 != "" {
		t.Fatalf("first call: reason=%q err=%v", reason1, err)
	}
	// Second call with unchanged go.mod and go.sum: neither `go env` nor `go mod edit` runs again.
	_, reason2, err := goBuildIdentity(check, root, inputs)
	if err != nil || reason2 != "" {
		t.Fatalf("second call: reason=%q err=%v", reason2, err)
	}
	// Simulate a fresh process (clear in-memory module cache, keep disk cache):
	// `go mod edit` still does not run because the disk cache hits on (go.mod, go.sum) content.
	goModMu.Lock()
	goModCache = map[string]cachedModuleIdentity{}
	goModMu.Unlock()
	_, reason3, err := goBuildIdentity(check, root, inputs)
	if err != nil || reason3 != "" {
		t.Fatalf("third call: reason=%q err=%v", reason3, err)
	}

	callsBeforeEdit, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	envCount := strings.Count(string(callsBeforeEdit), "env -json")
	editCount := strings.Count(string(callsBeforeEdit), "mod edit -json")
	if envCount != 1 {
		t.Fatalf("expected 1 `go env -json` call, got %d:\n%s", envCount, callsBeforeEdit)
	}
	if editCount != 1 {
		t.Fatalf("expected 1 `go mod edit -json` call across identical go.mod/go.sum, got %d:\n%s", editCount, callsBeforeEdit)
	}

	// Modify go.mod to add an outside local replacement: cache must miss and recompute.
	memoryWrite(t, filepath.Join(root, "input/go.mod"), "module fixture\n\ngo 1.24\nreplace fixture/b => ../outside\n")
	_, reason4, err := goBuildIdentity(check, root, inputs)
	if err != nil || reason4 != "local Go replacement outside declared inputs" {
		t.Fatalf("modified go.mod: reason=%q err=%v", reason4, err)
	}
	callsAfterEdit, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatal(err)
	}
	if got := strings.Count(string(callsAfterEdit), "mod edit -json"); got != 2 {
		t.Fatalf("expected 2 `go mod edit -json` calls after go.mod change, got %d:\n%s", got, callsAfterEdit)
	}
}
