package drain

import (
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

func TestReturnedSessionInterruptionIsNotMissingSession(t *testing.T) {
	dir := scratchUnderDurable(t)
	log := filepath.Join(dir, "log")
	writeForkFile(t, log, "session was interrupted\n")
	d := Decision{WorkerSessionReason: "resumed-worker", RunRequest: &run.Request{SessionTurn: true}}
	for _, cause := range []string{run.EndSignal, run.EndTimeLimit, run.EndRestart} {
		rec := run.Record{State: run.StateExited, ExitCode: countPtr(1), EndCause: cause, ArtifactRefs: []run.ArtifactRef{{Kind: "log", Ref: log}}}
		failed, err := failedDesignerResume(d, rec)
		if err != nil || failed {
			t.Fatalf("%s replaced a continuing attempt: %v %v", cause, failed, err)
		}
	}
}
