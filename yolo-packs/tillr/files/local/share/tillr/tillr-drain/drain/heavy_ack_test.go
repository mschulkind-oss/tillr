package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"
)

func TestHeavyOuterSupervisorWaitsForCleanupAcknowledgement(t *testing.T) {
	work := scratchUnderDurable(t)
	ready, ack := filepath.Join(work, "ready"), filepath.Join(work, "ack")
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	cmd := exec.CommandContext(ctx, "sh", "-c", "trap 'sleep 6; touch "+ack+"; exit 0' TERM; touch "+ready+"; while :; do sleep 0.01; done")
	done := make(chan error, 1)
	go func() { done <- runHeavySupervisorGroup(cmd) }()
	deadline := time.After(20 * time.Second)
	for {
		if _, err := os.Stat(ready); err == nil {
			break
		}
		select {
		case err := <-done:
			t.Fatalf("ended before readiness: %v", err)
		case <-deadline:
			t.Fatal("supervisor did not start")
		case <-time.After(10 * time.Millisecond):
		}
	}
	cancel()
	<-done
	if _, err := os.Stat(ack); err != nil {
		t.Fatalf("cleanup supervisor was killed before acknowledging its >5s cleanup: %v", err)
	}
}
