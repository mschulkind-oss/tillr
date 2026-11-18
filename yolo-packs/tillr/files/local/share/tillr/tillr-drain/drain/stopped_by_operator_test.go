package drain

import (
	"context"
	"errors"
	"fmt"
	"os/exec"
	"testing"
	"time"
)

// A signal aimed at the loop's process group reaches a child the loop is running before the
// loop's own handler has canceled its context. That child's death is the operator's stop, not
// a fault, so the decision waits for the cancellation that is certain to follow.
func TestAChildKilledByTheGroupSignalIsTheOperatorsStopOnceTheContextIsCanceled(t *testing.T) {
	childErr := exec.Command("sh", "-c", "kill -TERM $$").Run()
	if childErr == nil {
		t.Fatal("the child was not killed by SIGTERM")
	}
	wrapped := fmt.Errorf("listing the tracked documents: %v", childErr)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go func() {
		// The handler's cancellation arrives after the child's death was observed.
		time.Sleep(20 * time.Millisecond)
		cancel()
	}()
	if !stoppedByOperator(ctx, wrapped) {
		t.Fatal("a child killed by SIGTERM was reported as a fault although the loop was then stopped")
	}
}

// Without a following cancellation the signal was aimed at the child alone, and a
// failure, a plain error and a nil error are never the operator's stop.
func TestAChildKilledAloneAndOrdinaryErrorsAreNotTheOperatorsStop(t *testing.T) {
	old := signalGrace
	signalGrace = 10 * time.Millisecond
	defer func() { signalGrace = old }()
	childErr := exec.Command("sh", "-c", "kill -TERM $$").Run()
	if stoppedByOperator(context.Background(), childErr) {
		t.Fatal("a child killed alone was taken for the operator's stop")
	}
	if stoppedByOperator(context.Background(), errors.New("store would not open")) {
		t.Fatal("an ordinary error was taken for the operator's stop")
	}
	if stoppedByOperator(context.Background(), nil) {
		t.Fatal("no error was taken for the operator's stop")
	}
}
