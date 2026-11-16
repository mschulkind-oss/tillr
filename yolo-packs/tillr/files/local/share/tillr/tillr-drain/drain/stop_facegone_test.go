package drain

import (
	"context"
	"os"
	"os/exec"
	"testing"
)

// A run face killed outright (an OOM kill) can leave its adapter alive in its
// own process group, still carrying the run and store markers. `stop --end-runs`
// must not report the runs ended while that worker lives: it records the
// interruption, then ends the worker.
func TestEndRunsEndsWorkerWhoseRunFaceIsGone(t *testing.T) {
	f, l := passFixture(t, "item-a")
	id := claimRun(t, f, l)
	identity, err := processStoreIdentity(f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	cmd := exec.Command("sleep", "300")
	cmd.Env = append(os.Environ(), "TILLR_RUN_ID="+id, "TILLR_LOOP_STORE="+identity)
	if err := cmd.Start(); err != nil {
		t.Fatal(err)
	}
	exited := make(chan error, 1)
	go func() { exited <- cmd.Wait() }()
	t.Cleanup(func() { cmd.Process.Kill() })

	report, err := Stop(context.Background(), StopConfig{Store: f.storePath, EndRuns: true})
	if err != nil {
		t.Fatalf("Stop: %v (skipped %q)", err, report.Skipped)
	}
	if !orphanedRuns(t, f)[id] {
		t.Fatalf("run %s was not recorded as interrupted", id)
	}
	for _, line := range report.Skipped {
		if line == "run "+id+": a worker survives but its run face is gone" {
			t.Fatalf("worker was skipped, not ended: %q", report.Skipped)
		}
	}
	<-exited // the worker was signaled; the test's own deadline bounds this
}
