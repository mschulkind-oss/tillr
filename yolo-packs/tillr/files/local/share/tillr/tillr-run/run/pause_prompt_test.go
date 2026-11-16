package run

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestContinuationUsesOnlyThisRunsReportAndConsumesRestartNotice(t *testing.T) {
	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0755); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(root, "pause-prompt-")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { os.RemoveAll(dir) })
	lane := filepath.Join(dir, "lane")
	worktree := filepath.Join(lane, "worktree")
	durable := filepath.Join(lane, "durable", "run")
	for _, path := range []string{worktree, durable} {
		if err := os.MkdirAll(path, 0755); err != nil {
			t.Fatal(err)
		}
	}
	write := func(path, body string) {
		t.Helper()
		if err := os.WriteFile(path, []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	write(filepath.Join(lane, "brief.md"), "Continue fixture work.\n")
	write(filepath.Join(lane, "report.md"), "Another run's report.\n")
	req := Request{RunID: "run", Resume: true}
	promptFor := func() string {
		t.Helper()
		mode, path, err := prepareContinuation(req, PauseCannotResume, lane, worktree, durable)
		if err != nil {
			t.Fatal(err)
		}
		if mode != "new" {
			t.Fatalf("mode=%s", mode)
		}
		body, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		return string(body)
	}
	t.Run("a wall or signal is not a restart and the lane report is not evidence", func(t *testing.T) {
		prompt := promptFor()
		if strings.Contains(prompt, "restart") {
			t.Fatalf("invented restart: %s", prompt)
		}
		if strings.Contains(prompt, "Report:") {
			t.Fatalf("named stale lane report: %s", prompt)
		}
	})
	t.Run("a paused run points to its own report and consumes its notice", func(t *testing.T) {
		report := filepath.Join(durable, "report.md")
		write(report, "This run's report.\n")
		notice := filepath.Join(durable, "pause-notice")
		write(notice, "The session was interrupted by a restart.\n")
		prompt := promptFor()
		if !strings.Contains(prompt, "stopped for a restart") || !strings.Contains(prompt, "Report: "+report) {
			t.Fatalf("missing paused evidence: %s", prompt)
		}
		if _, err := os.Stat(notice); !os.IsNotExist(err) {
			t.Fatalf("restart notice not consumed: %v", err)
		}
		if prompt := promptFor(); strings.Contains(prompt, "restart") {
			t.Fatalf("replayed old restart: %s", prompt)
		}
	})
}
