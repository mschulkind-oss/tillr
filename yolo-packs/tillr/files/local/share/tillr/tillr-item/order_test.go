package main_test

import (
	"os/exec"
	"strings"
	"testing"
)

func TestOrderExportFaceIsRemoved(t *testing.T) {
	path, _ := placeFixture(t, "a", "b", "c")
	out, err := exec.Command(bin, "order", "--store", path).CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 2 || !strings.Contains(string(out), `unknown face "order"`) {
		t.Fatalf("retired command: %v: %s", err, out)
	}
	if code, out := run(t, "--help"); code != 0 || strings.Contains(string(out), ", order,") {
		t.Fatalf("help: exit %d: %s", code, out)
	}
}
