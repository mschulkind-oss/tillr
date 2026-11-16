package drain

import (
	"bytes"
	"errors"
	"strings"
	"testing"
)

type failingTraceOutput struct{}

func (failingTraceOutput) Write([]byte) (int, error) {
	return 0, errors.New("diagnostic output unavailable")
}

func TestSQLiteTraceForwarderHandlesSplitFinalAndOversizedLines(t *testing.T) {
	var output bytes.Buffer
	f := &sqliteTraceForwarder{output: &output}
	for _, part := range []string{"private secret\ntillr-", "sqlite pid=1 ", "tx=2\ntillr-sqlite-stack frame=blocked", "\n", "tillr-sqlite " + strings.Repeat("x", sqliteTraceLineLimit), "discard remainder\n", "tillr-sqlite final"} {
		if n, err := f.Write([]byte(part)); err != nil || n != len(part) {
			t.Fatalf("write changed behavior: %d/%v", n, err)
		}
	}
	f.Flush()
	want := "tillr-sqlite pid=1 tx=2\ntillr-sqlite-stack frame=blocked\ntillr-sqlite final\n"
	if output.String() != want {
		t.Fatalf("filtered output %q, want %q", output.String(), want)
	}
	broken := &sqliteTraceForwarder{output: failingTraceOutput{}}
	if _, err := broken.Write([]byte("tillr-sqlite diagnostic\n")); err != nil {
		t.Fatal("diagnostic changed child outcome")
	}
}

func blockedOwnedTransitionStage(entered, release chan struct{}) { close(entered); <-release }

func TestOwnedTransitionTimeoutDiagnosticCapturesActualStageOnlyWhenEnabled(t *testing.T) {
	entered, release := make(chan struct{}), make(chan struct{})
	go blockedOwnedTransitionStage(entered, release)
	<-entered
	defer close(release)
	t.Setenv("TILLR_SQLITE_TRACE", "")
	if got := ownedTransitionTimeoutDiagnostic(); got != "" {
		t.Fatal("trace-off changed timeout assertion")
	}
	t.Setenv("TILLR_SQLITE_TRACE", "1")
	t.Setenv("TILLR_TRACE_SECRET_CONTROL", "environment-secret-must-not-appear")
	got := ownedTransitionTimeoutDiagnostic()
	if !strings.Contains(got, "blockedOwnedTransitionStage") || !strings.Contains(got, "blocked-stage goroutine snapshot") {
		t.Fatalf("missing actual blocked stage: %s", got)
	}
	if strings.Contains(got, "environment-secret-must-not-appear") {
		t.Fatal("timeout stack leaked environment")
	}
}
