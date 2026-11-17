package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestAdapterUsageKindsAndStreamingClocks(t *testing.T) {
	// The Go test cache cannot see files opened by Python. Read its sources here
	// so a changed dialect or regression test invalidates the cached result.
	for _, file := range []string{"usage.py", "usage_test.py", "report.py", "pi/extract-report", "claude/extract-report"} {
		if _, err := os.ReadFile(filepath.Join("backends", file)); err != nil {
			t.Fatal(err)
		}
	}
	cmd := exec.Command("python3", "-m", "unittest", "discover", "-s", "backends", "-p", "usage_test.py")
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("adapter usage measurements: %v\n%s", err, out)
	}
}
