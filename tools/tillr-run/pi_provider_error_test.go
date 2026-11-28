package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	runner "github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

// fakeStreamPi replays FAKE_STREAM and exits with FAKE_EXIT, the way pi does when its
// provider refuses a request and it has nothing else to say: it still exits 0.
const fakeStreamPi = `#!/usr/bin/env python3
import os, sys
sys.stdin.read()
with open(os.environ["FAKE_STREAM"], "rb") as f:
    os.write(1, f.read())
sys.exit(int(os.environ.get("FAKE_EXIT", "0")))
`

const providerRefusal = "github-copilot API error (400): {\"error\":{\"message\":\"bad\",\"code\":\"invalid_request_body\"}}"

func assistantTurn(kind, stop, errText, text string) string {
	msg := map[string]any{"role": "assistant", "stopReason": stop, "content": []any{}, "usage": map[string]any{"totalTokens": 0}}
	if errText != "" {
		msg["errorMessage"] = errText
	}
	if text != "" {
		msg["content"] = []any{map[string]any{"type": "text", "text": text}}
	}
	b, _ := json.Marshal(map[string]any{"type": kind, "message": msg})
	return string(b) + "\n"
}

// runFakePi runs the real pi adapter against a fake pi that replays stream and exits 0.
func runFakePi(t *testing.T, stream string) (runner.Record, int) {
	t.Helper()
	dir := scratch(t, "pi-provider-error")
	root := filepath.Join(dir, "lanes")
	lane := filepath.Join(root, "lane")
	worktree := filepath.Join(lane, "worktree")
	if err := os.MkdirAll(worktree, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, "brief.md"), []byte(filledBrief(t, worktree)), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(lane, "session-id"), []byte("12345678-1234-1234-1234-123456789abc\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	binDir := filepath.Join(dir, "bin")
	writeExecutable(t, filepath.Join(binDir, "pi"), fakeStreamPi)
	streamPath := filepath.Join(dir, "stream.jsonl")
	if err := os.WriteFile(streamPath, []byte(stream), 0o644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", binDir+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("FAKE_STREAM", streamPath)
	t.Setenv("TILLR_RESUME_PROMPT_FILE", "")
	t.Setenv("TILLR_SESSION_MODE", "new")
	t.Setenv("TILLR_TRACE_DIR", filepath.Join(dir, "trace"))
	s, err := store.InitFixture(context.Background(), filepath.Join(dir, "store.db"))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	adapter, _ := filepath.Abs("backends/pi/run-pi")
	backends, _ := filepath.Abs("backends")
	req := runner.Request{RunID: "run", LaneID: "lane", Backend: "pi", WorkingDir: "worktree", Command: []string{adapter, "--backend", "pi", "--model", "fixture", "--effort", "low"}}
	opts := runner.ExecOptions{LaneRoot: root, BackendsDir: backends, Now: time.Now()}
	env, err := runner.Exec(context.Background(), s, req, opts)
	if err != nil {
		t.Fatal(err)
	}
	var rec runner.Record
	if err := json.Unmarshal(env.Payload, &rec); err != nil {
		t.Fatal(err)
	}
	var status runner.Record
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		got, found, err := runner.StatusRecord(r, "run")
		if err != nil || !found {
			t.Fatalf("status: found=%v err=%v", found, err)
		}
		var body struct {
			Run runner.Record `json:"run"`
		}
		if err := json.Unmarshal(got.Payload, &body); err != nil {
			return err
		}
		status = body.Run
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if status.EndCause != rec.EndCause || status.ProviderError != rec.ProviderError {
		t.Fatalf("status %q/%q differs from the terminal record %q/%q", status.EndCause, status.ProviderError, rec.EndCause, rec.ProviderError)
	}
	code := -1
	if rec.ExitCode != nil {
		code = *rec.ExitCode
	}
	return rec, code
}

// A provider that refuses every request leaves a last assistant turn that ended in error,
// and pi still exits 0. That is a failed run naming the provider's message, never an
// exit-0 run with an unreadable report.
func TestPiLastTurnProviderErrorIsAFailedRun(t *testing.T) {
	stream := `{"type":"agent_start"}` + "\n" +
		assistantTurn("message_end", "error", providerRefusal, "") +
		assistantTurn("turn_end", "error", providerRefusal, "") +
		`{"type":"agent_end","messages":[` + strings.TrimSuffix(strings.TrimPrefix(assistantTurn("x", "error", providerRefusal, ""), `{"type":"x","message":`), "}\n") + `]}` + "\n"
	rec, code := runFakePi(t, stream)
	if rec.EndCause != runner.EndProviderError || rec.ProviderError != providerRefusal || code != 1 {
		t.Fatalf("record = %s/%q/exit %d, want provider-error/%q/exit 1", rec.EndCause, rec.ProviderError, code, providerRefusal)
	}
}

// An error that a later assistant turn in the same segment recovered from is no failure.
func TestPiRecoveredProviderErrorIsAnOrdinaryExit(t *testing.T) {
	stream := `{"type":"agent_start"}` + "\n" +
		assistantTurn("message_end", "error", providerRefusal, "") +
		assistantTurn("message_end", "stop", "", "All done.\n\nRESULT: done") +
		assistantTurn("turn_end", "stop", "", "All done.\n\nRESULT: done")
	rec, code := runFakePi(t, stream)
	if rec.EndCause != runner.EndNormalExit || rec.ProviderError != "" || code != 0 {
		t.Fatalf("record = %s/%q/exit %d, want normal-exit/none/exit 0", rec.EndCause, rec.ProviderError, code)
	}
}

// An auto_retry_end that gave up names the provider's final error when no turn carries one.
func TestPiAutoRetryGiveUpIsAProviderError(t *testing.T) {
	stream := `{"type":"agent_start"}` + "\n" +
		`{"type":"auto_retry_end","success":false,"attempt":3,"finalError":"503 overloaded"}` + "\n"
	rec, code := runFakePi(t, stream)
	if rec.EndCause != runner.EndProviderError || rec.ProviderError != "503 overloaded" || code != 1 {
		t.Fatalf("record = %s/%q/exit %d, want provider-error/503 overloaded/exit 1", rec.EndCause, rec.ProviderError, code)
	}
}
