package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

// TestMeasureHandoffUnderLandingLock is an opt-in experiment, not a latency
// assertion. The control recreates a land holder's old exclusion of registry
// readers. The fixture holds the landing lock for two seconds in both modes.
func TestMeasureHandoffUnderLandingLock(t *testing.T) {
	script := os.Getenv("TILLR_HANDOFF_REPORT_SCRIPT")
	if script == "" {
		t.Skip("set TILLR_HANDOFF_REPORT_SCRIPT to the absolute path to handoff.py")
	}
	for _, mode := range []string{"before", "after"} {
		t.Run(mode, func(t *testing.T) {
			f, l, steps := workflowLoopFixture(t, "")
			l.cfg.Now = time.Now
			workerRelease := make(chan struct{})
			l.cfg.Steps = &handoffSteps{workflowSteps: steps, workerRelease: workerRelease}
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var result LoopResult
			if _, err := l.pass(ctx, &result); err != nil {
				t.Fatal(err)
			}
			unlock, err := l.lockTrunkHistory(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "before" {
				registryUnlock, err := acquireRegistryLock(ctx, filepath.Join(f.root, ".git", "worktree-registry.lock"))
				if err != nil {
					unlock()
					t.Fatal(err)
				}
				landUnlock := unlock
				unlock = func() { registryUnlock(); landUnlock() }
			}
			released := make(chan struct{})
			time.AfterFunc(2*time.Second, func() { unlock(); close(released) })
			defer func() { <-released }()
			close(workerRelease)
			drainOwned(t, l, &result)
			for i := 0; i < 4 && result.Landed == 0; i++ {
				if _, err := l.pass(ctx, &result); err != nil {
					t.Fatal(err)
				}
				drainOwned(t, l, &result)
			}
			if result.Landed != 1 {
				t.Fatalf("workflow did not land: %+v", result)
			}
			raw, err := exec.Command("python3", script, "--store", f.storePath, "--records", filepath.Join(f.root, ".tillr", "loop")).CombinedOutput()
			if err != nil {
				t.Fatalf("report: %v: %s", err, raw)
			}
			t.Logf("%s", raw)
		})
	}
}
