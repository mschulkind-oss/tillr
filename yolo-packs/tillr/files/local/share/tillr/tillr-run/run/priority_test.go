package run_test

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestExecInheritsCallerCPUPriority(t *testing.T) {
	own, err := exec.Command("nice").Output()
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := strconv.Atoi(strings.TrimSpace(string(own)))
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	dir := scratch(t)
	s, err := store.InitFixture(ctx, filepath.Join(dir, "fixture.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out := filepath.Join(dir, "niceness")
	req := run.Request{RunID: "run-priority", ItemID: "item-priority", LaneID: "lane-priority", Backend: "command", WorkingDir: "worktree", Command: []string{
		"/bin/sh", "-c", `sh -c nice > "$1"`,
		"priority-test", out,
	}}
	if _, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes")}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != strconv.Itoa(baseline) {
		t.Fatalf("worker child niceness = %q, want inherited %d", got, baseline)
	}
}
