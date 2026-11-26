package cleanup_test

import (
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"testing"
)

func scratchEntries(root string) map[string]bool {
	out := make(map[string]bool)
	for _, pattern := range []string{"tillr-*-bin-*", "tillr-drain-trace-*"} {
		matches, _ := filepath.Glob(filepath.Join(root, pattern))
		for _, m := range matches {
			if info, err := os.Stat(m); err == nil && info.IsDir() {
				out[filepath.Base(m)] = true
			}
		}
	}
	return out
}

func TestTillrScratchCleanup(t *testing.T) {
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	repoRoot := wd
	if filepath.Base(repoRoot) == "tests" {
		repoRoot = filepath.Dir(repoRoot)
	}
	shared := os.Getenv("YOLO_DURABLE_DIR")
	if shared == "" {
		shared = filepath.Join(repoRoot, ".yolo", "durable")
	}
	if err := os.MkdirAll(shared, 0o755); err != nil {
		t.Fatal(err)
	}
	scratch, err := os.MkdirTemp(shared, "tillr-cleanup-check-")
	if err != nil {
		t.Fatal(err)
	}
	defer os.RemoveAll(scratch)

	durableRoot := filepath.Join(scratch, "durable")
	tmpDir := filepath.Join(scratch, "tmp")
	if err := os.MkdirAll(durableRoot, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(tmpDir, 0o755); err != nil {
		t.Fatal(err)
	}

	before := scratchEntries(durableRoot)
	cmd := exec.Command("go", "test", "-count=1", "tests/tillr-testmain_test.go")
	cmd.Dir = repoRoot
	cmd.Env = append(os.Environ(), "YOLO_DURABLE_DIR="+durableRoot, "TMPDIR="+tmpDir)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("tests/tillr-testmain_test.go failed: %v\n%s", err, out)
	}
	after := scratchEntries(durableRoot)
	var added []string
	for name := range after {
		if !before[name] {
			added = append(added, name)
		}
	}
	sort.Strings(added)
	if len(added) > 0 {
		t.Fatalf("suite left scratch entries: %s", fmt.Sprint(added))
	}
}
