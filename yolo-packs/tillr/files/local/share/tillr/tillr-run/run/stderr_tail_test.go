package run_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

const minimalPiBrief = `# test

## Header
## Task
## Scope and boundaries
## Seats
## Settled decisions
## Facts carried
## Method
## Verification commands
## Report shape
## Stop conditions
`

func TestNonZeroPiWorkerRecordsStderrTail(t *testing.T) {
	s, root := leaseFixture(t)
	laneRoot := filepath.Join(root, "lanes")
	laneDir := filepath.Join(laneRoot, "lane-1")
	workDir := filepath.Join(laneDir, "worktree")
	binDir := filepath.Join(root, "bin")
	for _, d := range []string{workDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(laneDir, "brief.md"), []byte(minimalPiBrief), 0o644); err != nil {
		t.Fatal(err)
	}
	fakePi := `#!/bin/sh
printf '%s\n' '{"type":"turn_start"}' '{"type":"request_start","timestamp":1795365026487}'
printf '%s\n' 'Warning: startup noise' 'Error: provider stream closed unexpectedly' >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(binDir, "pi"), []byte(fakePi), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TILLR_PI_BUILDS_DIR", "")
	t.Setenv("TILLR_PI_AGENT_DIRS_DIR", "")

	backendsDir, err := filepath.Abs("../backends")
	if err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(backendsDir, "pi", "run-pi")

	env, err := run.Exec(context.Background(), s, run.Request{
		RunID:      "run-pi-stderr-1",
		ItemID:     "item-1",
		LaneID:     "lane-1",
		Backend:    "pi",
		WorkingDir: "worktree",
		Command:    []string{adapter, "--backend", "pi", "--model", "fixture/model", "--effort", "low"},
	}, run.ExecOptions{
		LaneRoot:    laneRoot,
		BackendsDir: backendsDir,
		Now:         time.Date(2026, 11, 22, 16, 30, 26, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	var rec run.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.ExitCode == nil || *rec.ExitCode != 1 {
		t.Fatalf("exit_code = %v, want 1", rec.ExitCode)
	}
	const wantLine = "Error: provider stream closed unexpectedly"
	if !strings.Contains(rec.StderrTail, wantLine) {
		t.Fatalf("returned record stderr_tail = %q, want %q", rec.StderrTail, wantLine)
	}

	// Verify run.StatusRecord reads stderr_tail back from the stored run.finish event.
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		statusEnv, found, err := run.StatusRecord(r, rec.RunID)
		if err != nil || !found {
			t.Fatalf("StatusRecord: found=%v err=%v", found, err)
		}
		var status struct {
			Run *run.Record `json:"run"`
		}
		if err := json.Unmarshal(statusEnv.Payload, &status); err != nil {
			t.Fatal(err)
		}
		if status.Run == nil || !strings.Contains(status.Run.StderrTail, wantLine) {
			t.Fatalf("status record stderr_tail = %+v, want %q", status.Run, wantLine)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func TestZeroExitAndStaleStreamErrDoNotLeakStderrTail(t *testing.T) {
	s, root := leaseFixture(t)
	laneRoot := filepath.Join(root, "lanes")
	laneDir := filepath.Join(laneRoot, "lane-1")
	workDir := filepath.Join(laneDir, "worktree")
	binDir := filepath.Join(root, "bin")
	for _, d := range []string{workDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(laneDir, "brief.md"), []byte(minimalPiBrief), 0o644); err != nil {
		t.Fatal(err)
	}
	// Seed a stale stream.err from an earlier run on the same lane.
	if err := os.WriteFile(filepath.Join(laneDir, "stream.err"), []byte("stale error from previous run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fakePi := `#!/bin/sh
printf '%s\n' '{"type":"message_end","message":{"role":"assistant","content":[{"type":"text","text":"RESULT: done\n"}]}}'
printf '%s\n' 'Warning: normal startup warning' >&2
exit 0
`
	if err := os.WriteFile(filepath.Join(binDir, "pi"), []byte(fakePi), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TILLR_PI_BUILDS_DIR", "")
	t.Setenv("TILLR_PI_AGENT_DIRS_DIR", "")

	backendsDir, err := filepath.Abs("../backends")
	if err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(backendsDir, "pi", "run-pi")

	env, err := run.Exec(context.Background(), s, run.Request{
		RunID:      "run-pi-ok",
		ItemID:     "item-1",
		LaneID:     "lane-1",
		Backend:    "pi",
		WorkingDir: "worktree",
		Command:    []string{adapter, "--backend", "pi", "--model", "fixture/model", "--effort", "low"},
	}, run.ExecOptions{
		LaneRoot:    laneRoot,
		BackendsDir: backendsDir,
		Now:         time.Date(2026, 11, 22, 16, 31, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	var rec run.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.StderrTail != "" {
		t.Fatalf("exit 0 record carried stderr_tail %q, want empty", rec.StderrTail)
	}

	// Next run fails before launching pi (preflight refusal): stale stream.err must be cleared.
	if err := os.WriteFile(filepath.Join(laneDir, "stream.err"), []byte("stale error from previous run\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	env2, err := run.Exec(context.Background(), s, run.Request{
		RunID:      "run-pi-refused",
		ItemID:     "item-1",
		LaneID:     "lane-1",
		Backend:    "pi",
		WorkingDir: "worktree",
		Command:    []string{adapter, "--backend", "pi", "--model", "fixture/model"}, // missing --effort -> exit 2 before exec
	}, run.ExecOptions{
		LaneRoot:    laneRoot,
		BackendsDir: backendsDir,
		Now:         time.Date(2026, 11, 22, 16, 32, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Exec refusal: %v", err)
	}
	var rec2 run.Record
	if err := json.Unmarshal(env2.Payload, &rec2); err != nil {
		t.Fatal(err)
	}
	if rec2.StderrTail != "" {
		t.Fatalf("refusal leaked stale stream.err: %q", rec2.StderrTail)
	}
}

func TestStderrTailTruncatesToLastLinesWithinLimit(t *testing.T) {
	s, root := leaseFixture(t)
	laneRoot := filepath.Join(root, "lanes")
	laneDir := filepath.Join(laneRoot, "lane-1")
	workDir := filepath.Join(laneDir, "worktree")
	binDir := filepath.Join(root, "bin")
	for _, d := range []string{workDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	if err := os.WriteFile(filepath.Join(laneDir, "brief.md"), []byte(minimalPiBrief), 0o644); err != nil {
		t.Fatal(err)
	}
	// Write > 4096 bytes to stderr, ending with a recognizable error line.
	fakePi := `#!/bin/sh
i=0
while [ "$i" -lt 200 ]; do
    printf 'padding line %03d xxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxxx\n' "$i" >&2
    i=$((i + 1))
done
printf '%s\n' 'FATAL: connection reset by peer' >&2
exit 1
`
	if err := os.WriteFile(filepath.Join(binDir, "pi"), []byte(fakePi), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TILLR_PI_BUILDS_DIR", "")
	t.Setenv("TILLR_PI_AGENT_DIRS_DIR", "")

	backendsDir, err := filepath.Abs("../backends")
	if err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(backendsDir, "pi", "run-pi")

	env, err := run.Exec(context.Background(), s, run.Request{
		RunID:      "run-pi-long-stderr",
		ItemID:     "item-1",
		LaneID:     "lane-1",
		Backend:    "pi",
		WorkingDir: "worktree",
		Command:    []string{adapter, "--backend", "pi", "--model", "fixture/model", "--effort", "low"},
	}, run.ExecOptions{
		LaneRoot:    laneRoot,
		BackendsDir: backendsDir,
		Now:         time.Date(2026, 11, 22, 16, 33, 0, 0, time.UTC),
	})
	if err != nil {
		t.Fatalf("Exec: %v", err)
	}
	var rec run.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	if len(rec.StderrTail) > run.StderrTailBytes {
		t.Fatalf("stderr_tail len = %d, want <= %d", len(rec.StderrTail), run.StderrTailBytes)
	}
	if !strings.HasSuffix(rec.StderrTail, "FATAL: connection reset by peer") {
		t.Fatalf("stderr_tail did not preserve final line: %q", rec.StderrTail)
	}
	if strings.Contains(rec.StderrTail, "padding line 000") {
		t.Fatalf("stderr_tail was not truncated from the front")
	}
}
