package main_test

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runner "github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestPiRunUsesAndRemovesPerRunTmpDir(t *testing.T) {
	dir := scratch(t, "pi-tmpdir")
	root := filepath.Join(dir, "lanes")
	lane := filepath.Join(root, "lane")
	worktree := filepath.Join(lane, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
		t.Fatal(err)
	}
	streamPath, err := filepath.Abs(filepath.Join("backends", "pi", "testdata", "complete.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	wantStream, err := os.ReadFile(streamPath)
	if err != nil {
		t.Fatal(err)
	}
	seenTmpDir := filepath.Join(dir, "seen-tmpdir")
	binDir := filepath.Join(dir, "bin")
	writeExecutable(t, filepath.Join(binDir, "pi"), `#!/bin/sh
set -eu
printf '%s' "${TMPDIR:-}" > "$STUB_SEEN_TMPDIR"
[ -n "${TMPDIR:-}" ] && [ -d "$TMPDIR" ]
printf 'tool output\n' > "$TMPDIR/pi-bash-deadbeef.log"
mkdir -p "$TMPDIR/pi-codemode-123"
printf 'nested output\n' > "$TMPDIR/pi-codemode-123/out.txt"
printf 'keep me\n' > "$YOLO_DURABLE_DIR/other-scratch.txt"
cat "$STUB_STREAM"
`)

	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("STUB_SEEN_TMPDIR", seenTmpDir)
	t.Setenv("STUB_STREAM", streamPath)
	t.Setenv("TILLR_RESUME_PROMPT_FILE", "")
	t.Setenv("TILLR_SESSION_MODE", "new")
	t.Setenv("TILLR_TRACE_DIR", filepath.Join(dir, "trace"))
	t.Setenv("TILLR_PI_AGENT_DIRS_DIR", "")

	s, err := store.InitFixture(context.Background(), filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	adapter, err := filepath.Abs(filepath.Join("backends", "pi", "run-pi"))
	if err != nil {
		t.Fatal(err)
	}
	backends, err := filepath.Abs("backends")
	if err != nil {
		t.Fatal(err)
	}

	const runID = "run-tmpdir-1"
	req := runner.Request{
		RunID:      runID,
		ItemID:     "item-tmpdir",
		LaneID:     "lane",
		Backend:    "pi",
		WorkingDir: "worktree",
		Command:    []string{adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium"},
	}
	opts := runner.ExecOptions{
		LaneRoot:    root,
		BackendsDir: backends,
		Now:         time.Now(),
	}
	env, err := runner.Exec(context.Background(), s, req, opts)
	if err != nil {
		t.Fatalf("runner.Exec failed: %v", err)
	}
	var rec runner.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatalf("unmarshaling run record: %v", err)
	}
	if rec.State != runner.StateExited || rec.ExitCode == nil || *rec.ExitCode != 0 {
		t.Fatalf("run ended with state=%q exit=%v, want exited/0", rec.State, rec.ExitCode)
	}

	durableDir := filepath.Join(lane, "durable", runID)
	wantTmpDir := filepath.Join(durableDir, "tmp")

	gotTmpDirBytes, err := os.ReadFile(seenTmpDir)
	if err != nil {
		t.Fatalf("reading recorded TMPDIR: %v", err)
	}
	if got := string(gotTmpDirBytes); got != wantTmpDir {
		t.Fatalf("pi saw TMPDIR=%q, want %q", got, wantTmpDir)
	}

	if _, err := os.Stat(wantTmpDir); !os.IsNotExist(err) {
		t.Fatalf("run tmp directory %q still exists after exit (stat err=%v)", wantTmpDir, err)
	}

	otherScratch, err := os.ReadFile(filepath.Join(durableDir, "other-scratch.txt"))
	if err != nil || string(otherScratch) != "keep me\n" {
		t.Fatalf("other scratch file was disturbed: content=%q err=%v", string(otherScratch), err)
	}

	logPath := filepath.Join(lane, runID+".log")
	gotLog, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("reading run log %q: %v", logPath, err)
	}
	if !bytes.Equal(gotLog, wantStream) {
		t.Fatalf("run log changed: got %d bytes, want %d bytes", len(gotLog), len(wantStream))
	}

	runReport, err := os.ReadFile(filepath.Join(durableDir, "report.md"))
	if err != nil {
		t.Fatalf("reading run report.md: %v", err)
	}
	laneReport, err := os.ReadFile(filepath.Join(lane, "report.md"))
	if err != nil {
		t.Fatalf("reading lane report.md: %v", err)
	}
	if !bytes.Equal(runReport, laneReport) {
		t.Fatalf("run report.md and lane report.md differ")
	}
	for _, want := range []string{"backend: pi", "complete: yes", "result: done", "RESULT: done"} {
		if !strings.Contains(string(runReport), want) {
			t.Fatalf("report.md missing %q:\n%s", want, runReport)
		}
	}
}

func TestPiRunRemovesTmpDirOnNonZeroExit(t *testing.T) {
	adapter, err := filepath.Abs(filepath.Join("backends", "pi", "run-pi"))
	if err != nil {
		t.Fatal(err)
	}
	dir := scratch(t, "pi-tmpdir-nonzero")
	worktree := filepath.Join(dir, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
		t.Fatal(err)
	}
	runScratch := filepath.Join(dir, "run-scratch")
	if err := os.MkdirAll(runScratch, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(runScratch, "keep.txt"), []byte("intact\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seenTmpDir := filepath.Join(dir, "seen-tmpdir")
	binDir := filepath.Join(dir, "bin")
	writeExecutable(t, filepath.Join(binDir, "pi"), `#!/bin/sh
set -eu
printf '%s' "${TMPDIR:-}" > "$STUB_SEEN_TMPDIR"
printf 'temp\n' > "$TMPDIR/pi-bash-fail.log"
printf 'failure detail\n' >&2
exit 17
`)

	cmd := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium")
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"YOLO_DURABLE_DIR="+runScratch,
		"TMPDIR=/tmp",
		"STUB_SEEN_TMPDIR="+seenTmpDir,
		"TILLR_PAUSE_REQUEST=",
		"TILLR_RESUME_PROMPT_FILE=",
		"TILLR_SESSION_MODE=new",
		"TILLR_PI_AGENT_DIRS_DIR=",
	)
	err = cmd.Run()
	exitErr, ok := err.(*exec.ExitError)
	if !ok || exitErr.ExitCode() != 17 {
		t.Fatalf("run-pi exit = %v, want exit code 17", err)
	}

	wantTmpDir := filepath.Join(runScratch, "tmp")
	gotTmpDirBytes, err := os.ReadFile(seenTmpDir)
	if err != nil {
		t.Fatalf("reading recorded TMPDIR: %v", err)
	}
	if got := string(gotTmpDirBytes); got != wantTmpDir {
		t.Fatalf("pi saw TMPDIR=%q, want %q", got, wantTmpDir)
	}
	if _, err := os.Stat(wantTmpDir); !os.IsNotExist(err) {
		t.Fatalf("run tmp directory %q still exists after non-zero exit (stat err=%v)", wantTmpDir, err)
	}
	keep, err := os.ReadFile(filepath.Join(runScratch, "keep.txt"))
	if err != nil || string(keep) != "intact\n" {
		t.Fatalf("sibling scratch file disturbed: content=%q err=%v", string(keep), err)
	}
}

func TestPiRunPreservesOuterTmpDirWhenNested(t *testing.T) {
	adapter, err := filepath.Abs(filepath.Join("backends", "pi", "run-pi"))
	if err != nil {
		t.Fatal(err)
	}
	dir := scratch(t, "pi-tmpdir-nested")
	worktree := filepath.Join(dir, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
		t.Fatal(err)
	}
	runScratch := filepath.Join(dir, "run-scratch")
	outerTmp := filepath.Join(runScratch, "tmp")
	if err := os.MkdirAll(outerTmp, 0o755); err != nil {
		t.Fatal(err)
	}
	outerFile := filepath.Join(outerTmp, "outer.log")
	if err := os.WriteFile(outerFile, []byte("outer\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	seenTmpDir := filepath.Join(dir, "seen-tmpdir")
	binDir := filepath.Join(dir, "bin")
	writeExecutable(t, filepath.Join(binDir, "pi"), `#!/bin/sh
set -eu
printf '%s' "${TMPDIR:-}" > "$STUB_SEEN_TMPDIR"
printf 'inner\n' > "$TMPDIR/inner.log"
`)

	cmd := exec.Command(adapter, "--backend", "pi", "--model", "fixture-model", "--effort", "medium")
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(),
		"PATH="+binDir+string(os.PathListSeparator)+os.Getenv("PATH"),
		"YOLO_DURABLE_DIR="+runScratch,
		"TMPDIR="+outerTmp,
		"STUB_SEEN_TMPDIR="+seenTmpDir,
		"TILLR_PAUSE_REQUEST=",
		"TILLR_RESUME_PROMPT_FILE=",
		"TILLR_SESSION_MODE=new",
		"TILLR_PI_AGENT_DIRS_DIR=",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("run-pi failed: %v\n%s", err, out)
	}

	gotTmpDirBytes, err := os.ReadFile(seenTmpDir)
	if err != nil {
		t.Fatalf("reading recorded TMPDIR: %v", err)
	}
	gotTmpDir := string(gotTmpDirBytes)
	if !strings.HasPrefix(gotTmpDir, outerTmp+string(filepath.Separator)) {
		t.Fatalf("nested pi saw TMPDIR=%q, want subdirectory under %q", gotTmpDir, outerTmp)
	}
	if _, err := os.Stat(gotTmpDir); !os.IsNotExist(err) {
		t.Fatalf("nested tmp directory %q still exists after exit (stat err=%v)", gotTmpDir, err)
	}
	gotOuter, err := os.ReadFile(outerFile)
	if err != nil || string(gotOuter) != "outer\n" {
		t.Fatalf("outer tmp file was disturbed: content=%q err=%v", string(gotOuter), err)
	}
}
