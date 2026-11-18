package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

// Run the runner regressions as part of the module suite, not only as a manual
// Python command. The installed pack mirrors this module but not the repo runner.
func TestShardRunnerLiveness(t *testing.T) {
	if _, err := os.Stat("../go-test-shards.py"); os.IsNotExist(err) {
		t.Skip("repository runner is not present in the installed pack")
	} else if err != nil {
		t.Fatal(err)
	}
	dir, err := filepath.Abs(scratch(t, "shard-runner-test"))
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("python3", "shards_test.py")
	cmd.Env = append(os.Environ(), "TMPDIR="+dir)
	if output, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("runner liveness: %v\n%s", err, output)
	}
}
