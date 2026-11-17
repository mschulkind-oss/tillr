package drain

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

func TestFailedDesignerResumeRequiresNoModelResponse(t *testing.T) {
	for _, tc := range []struct {
		name, stream, reason, state string
		exit                        int
		retry                       bool
	}{
		{"empty", "", "resumed-warm", run.StateExited, 1, true},
		{"error only", `{"type":"message_end","message":{"role":"assistant","stopReason":"error","content":[]}}`, "resumed-warm", run.StateExited, 1, true},
		{"start only", `{"type":"message_update","assistantMessageEvent":{"type":"start"}}`, "resumed-warm", run.StateExited, 1, true},
		{"partial", `{"type":"message_update","assistantMessageEvent":{"type":"text_delta","delta":"answer"}}`, "resumed-expired", run.StateExited, 1, false},
		{"tool", `{"type":"message_end","message":{"role":"assistant","content":[{"type":"toolCall"}]}}`, "resumed-compacted", run.StateExited, 1, false},
		{"error with partial text", `{"type":"message_end","message":{"role":"assistant","stopReason":"error","content":[{"type":"text","text":"partial answer"}]}}`, "resumed-warm", run.StateExited, 1, false},
		{"Claude answer", `{"type":"assistant","message":{"role":"assistant","content":[{"type":"text","text":"answer"}]}}`, "resumed-warm", run.StateExited, 1, false},
		{"new fails", "", "resume-failed", run.StateExited, 1, false},
		{"successful", "", "resumed-warm", run.StateExited, 0, false},
		{"wall", "", "resumed-warm", run.StateTimedOut, 1, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			file := filepath.Join(t.TempDir(), "stream")
			if err := os.WriteFile(file, []byte(tc.stream+"\n"), 0644); err != nil {
				t.Fatal(err)
			}
			rec := run.Record{State: tc.state, ExitCode: &tc.exit, ArtifactRefs: []run.ArtifactRef{{Kind: "log", Ref: file}}}
			got, err := failedDesignerResume(Decision{DesignerReason: tc.reason}, rec)
			if err != nil || got != tc.retry {
				t.Fatalf("%v %v", got, err)
			}
		})
	}
}
