package drain

import (
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
)

func TestRefusedWindowRequiresPinnedHeaderAndRefusal(t *testing.T) {
	for _, tc := range []struct {
		name, body string
		state      string
		exit       int
		want       bool
	}{
		{"spent", "rate limit kind: five_hour\nfraction: 1\nstatus: rejected\nreset: 1794794400\n---\n", run.StateExited, 1, true},
		{"undelimited body", "rate limit kind: five_hour\nfraction: 1\nstatus: rejected\nreset: 1794794400\n", run.StateExited, 1, false},
		{"body is not facts", "---\nrate limit kind: five_hour\nfraction: 1\nstatus: rejected\nreset: 1794794400\n", run.StateExited, 1, false},
		{"unknown fraction", "rate limit kind: five_hour\nfraction: unknown\nstatus: rejected\nreset: 1794794400\n---\n", run.StateExited, 1, false},
		{"malformed reset", "rate limit kind: five_hour\nfraction: 1\nstatus: rejected\nreset: nope\n---\n", run.StateExited, 1, false},
		{"not a refusal", "rate limit kind: five_hour\nfraction: 1\nstatus: rejected\nreset: 1794794400\n---\n", run.StateExited, 0, false},
		{"wall", "rate limit kind: five_hour\nfraction: 1\nstatus: rejected\nreset: 1794794400\n---\n", run.StateTimedOut, 124, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "report.md")
			if err := os.WriteFile(path, []byte("# run report\n"+tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			rec := run.Record{State: tc.state, ExitCode: &tc.exit, ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: path}}}
			kind, fraction, reset, ok := refusedWindow(rec)
			if ok != tc.want {
				t.Fatalf("got %v want %v", ok, tc.want)
			}
			if ok && (kind != "five_hour" || fraction != 1 || !reset.Equal(time.Unix(1794794400, 0))) {
				t.Fatalf("%s %v %v", kind, fraction, reset)
			}
		})
	}
}

func TestRefusedWindowAcceptsClaudeRateLimitedFixture(t *testing.T) {
	extractor := filepath.Join("..", "..", "tillr-run", "backends", "claude", "extract-report")
	fixture := filepath.Join("..", "..", "tillr-run", "backends", "claude", "testdata", "rate-limited.jsonl")
	reportPath := filepath.Join(t.TempDir(), "report.md")
	cmd := exec.Command(extractor, "--backend", "claude", "--model", "test-model", "--effort", "medium", "--exit-code", "1", "--out", reportPath, fixture)
	if out, err := cmd.CombinedOutput(); err == nil {
		t.Fatalf("expected non-zero exit for incomplete rate-limited stream, got 0: %s", out)
	}
	exit := 1
	rec := run.Record{State: run.StateExited, ExitCode: &exit, ArtifactRefs: []run.ArtifactRef{{Kind: "report", Ref: reportPath}}}
	kind, fraction, reset, ok := refusedWindow(rec)
	if !ok || kind != "five_hour" || fraction != 1.02 || !reset.Equal(time.Unix(1795128600, 0).UTC()) {
		t.Fatalf("refusedWindow = (%q, %v, %v, %v), want (five_hour, 1.02, %v, true)", kind, fraction, reset, ok, time.Unix(1795128600, 0).UTC())
	}
}

func TestAvailableBackendSkipsEveryShutHopWithoutEscapingSeat(t *testing.T) {
	p := Policy{SeatLists: map[string][]string{"design": {"a", "b", "c"}}, Preference: []string{"floor"}}
	for _, tc := range []struct {
		shut map[string]bool
		want string
		ok   bool
	}{
		{map[string]bool{"a": true}, "b", true},
		{map[string]bool{"a": true, "b": true}, "c", true},
		{map[string]bool{"a": true, "b": true, "c": true}, "", false},
		{map[string]bool{}, "a", true},
	} {
		name, left, ok := p.availableBackend("a", "design", tc.shut)
		if name != tc.want || ok != tc.ok || left != "" {
			t.Fatalf("%v: %s %s %v", tc.shut, name, left, ok)
		}
	}
}
