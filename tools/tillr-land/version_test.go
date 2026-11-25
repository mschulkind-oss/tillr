package main_test

import (
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func TestVersion(t *testing.T) {
	exe := filepath.Join(t.TempDir(), "tillr-land")
	if out, err := exec.Command("go", "build", "-o", exe, ".").CombinedOutput(); err != nil {
		t.Fatalf("go build: %v\n%s", err, out)
	}
	out, err := exec.Command(exe, "--version").Output()
	if err != nil {
		t.Fatalf("--version: %v", err)
	}
	line := strings.TrimSuffix(string(out), "\n")
	if strings.Contains(line, "\n") {
		t.Fatalf("--version is multi-line: %q", string(out))
	}
	for _, want := range []string{"green ", "digest ", "exe " + exe} {
		if !strings.Contains(line, want) {
			t.Fatalf("--version = %q, missing %q", line, want)
		}
	}
}
