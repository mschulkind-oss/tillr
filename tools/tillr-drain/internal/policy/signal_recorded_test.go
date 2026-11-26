package policy

import (
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

// TestClassifyAttemptRecordedSignal pins what the drain does with a run record that names
// its signal: a crash signal and an external SIGKILL both count against the backend
// (infrastructure, the README's rule that an unrequested SIGKILL keeps normal-exit), and
// only an external SIGTERM or SIGINT is an interruption.
func TestClassifyAttemptRecordedSignal(t *testing.T) {
	for _, tc := range []struct {
		name string
		rec  run.Record
		want AttemptClass
	}{
		{"direct-crash-sigsegv", run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(-1), EndCause: run.EndNormalExit, Signal: "SIGSEGV"}, AttemptInfrastructure},
		{"forwarded-crash-sigabrt", run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(134), EndCause: run.EndNormalExit, Signal: "SIGABRT"}, AttemptInfrastructure},
		{"direct-kill-sigkill", run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(-1), EndCause: run.EndNormalExit, Signal: "SIGKILL"}, AttemptInfrastructure},
		{"forwarded-kill-sigkill", run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(137), EndCause: run.EndNormalExit, Signal: "SIGKILL"}, AttemptInfrastructure},
		{"external-sigterm", run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(-1), EndCause: run.EndSignal, Signal: "SIGTERM"}, AttemptInterrupted},
		{"forwarded-sigint", run.Record{Backend: "fixture", State: run.StateExited, ExitCode: code(130), EndCause: run.EndSignal, Signal: "SIGINT"}, AttemptInterrupted},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := ClassifyAttempt(tc.rec, ""); got != tc.want {
				t.Errorf("ClassifyAttempt = %v, want %v", got, tc.want)
			}
		})
	}
}
