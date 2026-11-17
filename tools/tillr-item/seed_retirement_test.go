package main_test

import (
	"bytes"
	"os"
	"os/exec"
	"strings"
	"testing"
)

func TestSeedRefusalNamesSync(t *testing.T) {
	cmd := exec.Command(bin, "seed")
	cmd.Env = os.Environ()
	var stdout, stderr bytes.Buffer
	cmd.Stdout, cmd.Stderr = &stdout, &stderr
	err := cmd.Run()
	exit, ok := err.(*exec.ExitError)
	if !ok || exit.ExitCode() != 2 || stdout.Len() != 0 || !strings.Contains(stderr.String(), "tillr item sync") {
		t.Fatalf("seed: %v stdout=%q stderr=%q; want usage refusal naming sync", err, stdout.String(), stderr.String())
	}
}
