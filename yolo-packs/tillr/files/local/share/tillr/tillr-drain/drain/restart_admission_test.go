package drain

import (
	"context"
	"errors"
	"os/exec"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

func TestRunHandoffRechecksARequestFiledAfterWrapperAdmission(t *testing.T) {
	h := newRestartHarness(t, nil, nil)
	ctx := context.WithValue(context.Background(), runStartAdmissionKey{}, h.l)
	if pending, err := h.l.buildRestartPending(ctx); err != nil || pending {
		t.Fatalf("initial admission=%v/%v", pending, err)
	}
	h.request(t, 1)
	steps := &ProcessSteps{Store: h.f.storePath}
	for _, resume := range []bool{false, true} {
		cmd := exec.Command("true")
		err := steps.startRunFaceContext(ctx, cmd, run.Request{RunID: "not-started", Resume: resume})
		if !errors.Is(err, errRestartPending) || cmd.Process != nil {
			t.Fatalf("pending request started a face: resume=%v err=%v process=%v", resume, err, cmd.Process)
		}
	}
}

func TestDeferredRestartDoesNotBlockRunHandoff(t *testing.T) {
	h := newRestartHarness(t, nil, nil)
	ctx := context.WithValue(context.Background(), runStartAdmissionKey{}, h.l)
	release, done := make(chan struct{}), make(chan struct{})
	h.l.rollAction.submit("tip", func(string) { defer close(done); <-release })
	defer func() { close(release); <-done }()
	h.request(t, 1)
	if pending, err := h.l.buildRestartPending(ctx); err != nil || pending {
		t.Fatalf("the roll is still in progress: admission=%v/%v", pending, err)
	}
	steps := &ProcessSteps{Store: h.f.storePath}
	cmd := exec.Command("true")
	if err := steps.startRunFaceContext(ctx, cmd, run.Request{RunID: "while-rolling"}); err != nil {
		t.Fatalf("deferred restart prevented handoff: %v", err)
	}
	if err := cmd.Wait(); err != nil {
		t.Fatal(err)
	}
}

func TestRunHandoffDoesNotStartAfterItsContextWasCanceled(t *testing.T) {
	h := newRestartHarness(t, nil, nil)
	ctx, cancel := context.WithCancel(context.WithValue(context.Background(), runStartAdmissionKey{}, h.l))
	cancel()
	steps := &ProcessSteps{Store: h.f.storePath}
	cmd := exec.Command("true")
	if err := steps.startRunFaceContext(ctx, cmd, run.Request{RunID: "not-started"}); !errors.Is(err, context.Canceled) || cmd.Process != nil {
		t.Fatalf("canceled handoff=%v/%v", err, cmd.Process)
	}
}
