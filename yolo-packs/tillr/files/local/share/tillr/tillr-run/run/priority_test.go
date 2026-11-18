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

func TestExecLowersTheWorkersCPUPriority(t *testing.T) {
	const want = 10
	own, err := exec.Command("nice").Output()
	if err != nil {
		t.Fatal(err)
	}
	baseline, err := strconv.Atoi(strings.TrimSpace(string(own)))
	if err != nil {
		t.Fatal(err)
	}
	if baseline >= want {
		t.Skipf("test already runs at niceness %d", baseline)
	}
	ctx := context.Background()
	dir := scratch(t)
	s, err := store.InitFixture(ctx, filepath.Join(dir, "fixture.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	out := filepath.Join(dir, "niceness")
	// The initial shell can run before Start returns. Sample its descendants
	// until the group priority changes, then check what a fresh child inherits.
	req := run.Request{RunID: "run-priority", ItemID: "item-priority", LaneID: "lane-priority", Backend: "command", WorkingDir: "worktree", Command: []string{
		"/bin/sh", "-c", `i=0; while [ "$(nice)" = "$1" ] && [ "$i" -lt 100 ]; do sleep 0.05; i=$((i+1)); done; sh -c nice > "$2"`,
		"priority-test", strconv.Itoa(baseline), out,
	}}
	if _, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: filepath.Join(dir, "lanes")}); err != nil {
		t.Fatal(err)
	}
	got, err := os.ReadFile(out)
	if err != nil {
		t.Fatal(err)
	}
	if strings.TrimSpace(string(got)) != strconv.Itoa(want) {
		t.Fatalf("worker child niceness = %q, want %d", got, want)
	}
}
