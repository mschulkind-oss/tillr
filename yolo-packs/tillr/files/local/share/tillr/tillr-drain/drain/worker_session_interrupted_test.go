package drain

import (
	"os"
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

func TestInfrastructureResumeRequiresMissingSessionDiagnostic(t *testing.T) {
	const session = "22222222-2222-4222-8222-222222222222"
	for _, tc := range []struct {
		name          string
		readOnlyGroup string
		writeSession  bool
		logLine       string
		stderrTail    string
		want          bool
	}{
		{
			name:         "infrastructure failure in log",
			writeSession: true,
			logLine:      "harness credential failed",
			want:         false,
		},
		{
			name:         "infrastructure failure in stderr",
			writeSession: true,
			stderrTail:   "harness credential failed",
			want:         false,
		},
		{
			name:         "missing session in log (pi)",
			writeSession: true,
			logLine:      "No session found matching '" + session + "'",
			want:         true,
		},
		{
			name:         "missing conversation in log (claude)",
			writeSession: true,
			logLine:      "No conversation found with session ID: " + session,
			want:         true,
		},
		{
			name:         "missing session in stderr",
			writeSession: true,
			stderrTail:   "No session found matching '" + session + "'",
			want:         true,
		},
		{
			name:          "read-only group reads node dir session id",
			readOnlyGroup: "verify-group",
			writeSession:  true,
			logLine:       "No session found matching '" + session + "'",
			want:          true,
		},
		{
			name:          "read-only group infrastructure failure",
			readOnlyGroup: "verify-group",
			writeSession:  true,
			logLine:       "harness credential failed",
			want:          false,
		},
		{
			name:         "no staged session id",
			writeSession: false,
			logLine:      "No session found matching '" + session + "'",
			want:         false,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := scratchUnderDurable(t)
			laneRoot := filepath.Join(dir, "lane")
			runID := "run-item-1-a2"
			if tc.writeSession {
				sessionDir := laneRoot
				if tc.readOnlyGroup != "" {
					sessionDir = run.NodeDir(laneRoot, runID)
				}
				if err := os.MkdirAll(sessionDir, 0o755); err != nil {
					t.Fatal(err)
				}
				writeForkFile(t, filepath.Join(sessionDir, SessionIDFile), session+"\n")
			}
			logPath := filepath.Join(dir, "run.log")
			writeForkFile(t, logPath, tc.logLine+"\n")
			d := Decision{
				InfrastructureFrom:  "run-item-1-a1",
				WorkerSessionReason: "resumed-worker",
				RunRequest:          &run.Request{RunID: runID, SessionTurn: true, ReadOnlyGroup: tc.readOnlyGroup},
			}
			rec := run.Record{
				RunID:      runID,
				State:      run.StateExited,
				ExitCode:   countPtr(4),
				EndCause:   run.EndNormalExit,
				StderrTail: tc.stderrTail,
				ArtifactRefs: []run.ArtifactRef{
					{Kind: "lane-root", Ref: laneRoot},
					{Kind: "log", Ref: logPath},
				},
			}
			got, err := failedDesignerResume(d, rec)
			if err != nil || got != tc.want {
				t.Fatalf("failedDesignerResume() = (%v, %v), want (%v, nil)", got, err, tc.want)
			}
		})
	}
}
