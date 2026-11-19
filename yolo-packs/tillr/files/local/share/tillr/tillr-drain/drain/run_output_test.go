package drain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestRunOutputTailMatchesMemoryTail(t *testing.T) {
	for _, body := range []string{"", "\n fixture failed\n"} {
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

func TestRunOutputTailCutsOnLineBoundaryBeforeFiltering(t *testing.T) {
	traceLine := `tillr-sqlite at=2026-11-19T04:46:53.856135402Z pid=3089251 tx=2 store="/workspace/.tillr/state.db" operation="open" phase=end elapsed=1ms error=""` + "\n"
	t.Run("mid-trace-line", func(t *testing.T) {
		// Make the suffix after the cut start inside traceLine (at "x=2 store=...").
		after := "fixture failed\n"
		suffix := "x=2 store=" + strings.Repeat("a", StderrTailBytes-len("x=2 store=")-len(after)-1) + "\n" + after
		if len(suffix) != StderrTailBytes {
			t.Fatalf("suffix len %d, want %d", len(suffix), StderrTailBytes)
		}
		body := strings.Repeat(traceLine, 10) + "tillr-sqlite t" + suffix
		dir := scratchUnderDurable(t)
		if err := os.WriteFile(filepath.Join(dir, RunStderrFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := runOutputTail(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got != "…fixture failed" {
			t.Fatalf("tail %q, want %q", got, "…fixture failed")
		}
	})

	t.Run("exact-line-boundary", func(t *testing.T) {
		firstKept := "first kept line\n"
		rest := strings.Repeat("b\n", (StderrTailBytes-len(firstKept))/2)
		tail := firstKept + rest
		if len(tail) != StderrTailBytes {
			t.Fatalf("tail len %d, want %d", len(tail), StderrTailBytes)
		}
		body := traceLine + tail
		dir := scratchUnderDurable(t)
		if err := os.WriteFile(filepath.Join(dir, RunStderrFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := runOutputTail(dir)
		if err != nil {
			t.Fatal(err)
		}
		if !strings.HasPrefix(got, "…first kept line\n") {
			t.Fatalf("tail %q lost the complete line starting at offset", got)
		}
	})

	t.Run("mid-non-trace-line", func(t *testing.T) {
		body := strings.Repeat("x", StderrTailBytes) + "\nfixture failed\n"
		dir := scratchUnderDurable(t)
		if err := os.WriteFile(filepath.Join(dir, RunStderrFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := runOutputTail(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got != "…fixture failed" {
			t.Fatalf("tail %q, want %q", got, "…fixture failed")
		}
	})

	t.Run("no-newline-in-tail", func(t *testing.T) {
		body := strings.Repeat("x", StderrTailBytes+10)
		dir := scratchUnderDurable(t)
		if err := os.WriteFile(filepath.Join(dir, RunStderrFile), []byte(body), 0o644); err != nil {
			t.Fatal(err)
		}
		got, err := runOutputTail(dir)
		if err != nil {
			t.Fatal(err)
		}
		if got != "…" {
			t.Fatalf("tail %q, want %q", got, "…")
		}
	})
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
