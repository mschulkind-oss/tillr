package drain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunOutputTailMatchesMemoryTail(t *testing.T) {
	for _, body := range []string{"", "\n fixture failed\n", strings.Repeat("x", StderrTailBytes) + "\nfixture failed\n"} {
		dir := scratchUnderDurable(t)
		if err := os.WriteFile(filepath.Join(dir, RunStderrFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		var previous stderrTail
		_, _ = previous.Write([]byte(body))
		got, err := runOutputTail(dir)
		if err != nil || got != previous.String() {
			t.Fatalf("file tail %q / %v, want previous diagnostic %q", got, err, previous.String())
		}
	}
}

func TestRunOutputMissingIsNotAnError(t *testing.T) {
	got, err := runOutputTail(scratchUnderDurable(t))
	if err != nil || got != "no output recorded" {
		t.Fatalf("missing output: %q / %v", got, err)
	}
}

func TestRunOutputReadFailureIsNotMissing(t *testing.T) {
	dir := scratchUnderDurable(t)
	if err := os.Mkdir(filepath.Join(dir, RunStderrFile), 0o755); err != nil {
		t.Fatal(err)
	}
	if got, err := runOutputTail(dir); err == nil || got == "no output recorded" {
		t.Fatalf("unreadable output mistaken for legacy absence: %q / %v", got, err)
	}
}
