package drain

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/alarms"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestRecoveredBackendReusesOnlyItsOwnResumableSession(t *testing.T) {
	f, l := passFixture(t, "item-a")
	adapterDir := t.TempDir()
	writeForkFile(t, filepath.Join(adapterDir, "pause-kind"), run.PauseAtGap+"\n")
	transcript := filepath.Join(adapterDir, "session.jsonl")
	writeForkFile(t, transcript, "{}\n")
	request := run.Request{RunID: "next", ItemID: "item-a", LaneID: "lane-1", Backend: "fixture",
		Command: []string{filepath.Join(adapterDir, "run-fixture"), "--backend", "fixture", "--model", "test"}}
	prior := run.Record{RunID: "failed", ItemID: request.ItemID, LaneID: request.LaneID, Backend: request.Backend, Route: request.Route(),
		State: run.StateExited, ExitCode: code(4), StartedAt: "2026-11-18T00:00:00Z", FinishedAt: "2026-11-18T00:00:01Z",
		ArtifactRefs: []run.ArtifactRef{{Kind: "transcript", Ref: transcript}}}
	fixtureWrite(t, f, func(tx *store.Tx) error { return applyTerminal(tx, prior) })
	dir := RunDir(l.cfg.Root, prior.RunID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	const session = "503f6d2a-32b9-48b6-a09a-fa476150913e"
	writeForkFile(t, filepath.Join(dir, SessionIDFile), session+"\n")
	cases := []struct {
		name, lane, backend string
		resume              bool
	}{
		{"same lane and route", "lane-1", "fixture", true},
		{"different lane", "lane-2", "fixture", false},
		{"different backend", "lane-1", "alternate", false},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			req := request
			req.LaneID, req.Backend = tc.lane, tc.backend
			d := Decision{RunRequest: &req, InfrastructureFrom: prior.RunID}
			var spec StageSpec
			if err := l.stageInfrastructureSession(context.Background(), &d, &spec); err != nil {
				t.Fatal(err)
			}
			if req.SessionTurn != tc.resume || (spec.SessionID == session) != tc.resume {
				t.Fatalf("session turn=%v, session ID=%q; want resume=%v", req.SessionTurn, spec.SessionID, tc.resume)
			}
		})
	}
}

func TestNonZeroPiWorkerStderrAppearsInEndingAndAlarm(t *testing.T) {
	f, _ := passFixture(t, "item-a")
	ctx := context.Background()
	laneDir := filepath.Join(f.laneRoot, "lane-1")
	workDir := filepath.Join(laneDir, "worktree")
	binDir := filepath.Join(t.TempDir(), "bin")
	for _, d := range []string{workDir, binDir} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	const brief = "# test\n\n## Header\n## Task\n## Scope and boundaries\n## Seats\n## Settled decisions\n## Facts carried\n## Method\n## Verification commands\n## Report shape\n## Stop conditions\n"
	writeForkFile(t, filepath.Join(laneDir, "brief.md"), brief)

	const stderrLine = "Error: ECONNRESET while streaming github-copilot response"
	fakePi := "#!/bin/sh\n" +
		"printf '%s\\n' '{\"type\":\"turn_start\"}' '{\"type\":\"request_start\",\"timestamp\":1795365026487}'\n" +
		"printf '%s\\n' 'Warning: startup noise' '" + stderrLine + "' >&2\n" +
		"exit 1\n"
	if err := os.WriteFile(filepath.Join(binDir, "pi"), []byte(fakePi), 0o755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TILLR_PI_BUILDS_DIR", "")
	t.Setenv("TILLR_PI_AGENT_DIRS_DIR", "")

	backendsDir, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends"))
	if err != nil {
		t.Fatal(err)
	}
	adapter := filepath.Join(backendsDir, "pi", "run-pi")

	s, err := store.Open(ctx, f.storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	env, err := run.Exec(ctx, s, run.Request{
		RunID:      "run-pi-exit-1",
		ItemID:     "item-a",
		LaneID:     "lane-1",
		Backend:    "pi",
		WorkingDir: "worktree",
		Command:    []string{adapter, "--backend", "pi", "--model", "fixture/model", "--effort", "low"},
	}, run.ExecOptions{
		LaneRoot:    f.laneRoot,
		BackendsDir: backendsDir,
		Now:         time.Date(2026, 11, 22, 16, 30, 26, 0, time.UTC),
	})
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	if err != nil {
		t.Fatalf("run.Exec: %v", err)
	}
	var rec run.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	if rec.ExitCode == nil || *rec.ExitCode != 1 {
		t.Fatalf("exit_code = %v, want 1", rec.ExitCode)
	}
	if !strings.Contains(rec.StderrTail, stderrLine) {
		t.Fatalf("recorded ending stderr_tail = %q, want %q", rec.StderrTail, stderrLine)
	}
	// Confirm the extracted report itself has no result body (ends with '---'),
	// matching the real failure mode where the alarm previously showed ': ---'.
	if tail := strings.TrimSpace(logTail(rec.ArtifactRefs)); !strings.HasSuffix(tail, "---") {
		t.Fatalf("expected report tail to end with '---', got %q", tail)
	}

	if err := RecordBackendOutcomes(ctx, f.storePath); err != nil {
		t.Fatalf("RecordBackendOutcomes: %v", err)
	}
	var active []alarms.Alarm
	fixtureRead(t, f, func(r *store.Reader) error {
		var err error
		active, err = alarms.Read(r, time.Date(2026, 11, 22, 16, 31, 0, 0, time.UTC), nil)
		return err
	})
	if len(active) != 1 || active[0].Kind != alarms.Closed || active[0].Backend != "pi" {
		t.Fatalf("alarms = %+v, want one backend-closed alarm for pi", active)
	}
	text := active[0].String()
	if !strings.Contains(text, stderrLine) {
		t.Fatalf("alarm text = %q, want stderr line %q", text, stderrLine)
	}
	if strings.Contains(text, ": ---") {
		t.Fatalf("alarm text still contains ': ---': %q", text)
	}
}
