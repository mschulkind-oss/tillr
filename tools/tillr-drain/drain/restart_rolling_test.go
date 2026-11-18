package drain

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
)

func TestRollingExecClosesOwnershipBeforeReplacementReusesTheCheckout(t *testing.T) {
	root, _ := heavyRepo(t)
	work := scratchUnderDurable(t)
	child := filepath.Join(work, "rolling-child.pid")
	program := executable(t, "sleep 1000 & echo $! > "+child+".part; mv "+child+".part "+child+"; wait")
	self, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command(self, "-test.run=^TestHeavyExecOwnerHelper$")
	cmd.Env = append(os.Environ(), "TILLR_TEST_HEAVY_EXEC=1", "TILLR_TEST_HEAVY_ROOT="+root, "TILLR_TEST_HEAVY_WORK="+work, "TILLR_TEST_HEAVY_CHILD="+child, "TILLR_TEST_ROLLING_PROGRAM="+program)
	log, err := os.Create(filepath.Join(work, "rolling-exec.log"))
	if err != nil {
		t.Fatal(err)
	}
	defer log.Close()
	cmd.Stdout, cmd.Stderr = log, log
	if err := cmd.Run(); err != nil {
		raw, _ := os.ReadFile(log.Name())
		t.Fatalf("rolling replacement: %v\n%s", err, raw)
	}
	raw, err := os.ReadFile(filepath.Join(work, "replacement-verdict"))
	if err != nil || string(raw) != "one rolling verdict; unchanged tip\n" {
		t.Fatalf("replacement proof=%q/%v", raw, err)
	}
	heavyChildDead(t, child)
}
