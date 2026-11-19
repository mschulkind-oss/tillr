package run_test

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// The fixtures are cut from the two pi runs of 2026-11-19 that repeated one read hundreds
// of times: nine consecutive calls each, from just before the loop began, followed by the
// `loop_detected` event pi now emits when it ends such a session (synthetic: those runs
// predate it) and the run's end.
func repeatingFixture(t *testing.T, name string) string {
	t.Helper()
	path, err := filepath.Abs(filepath.Join("..", "backends", "pi", "testdata", name))
	if err != nil {
		t.Fatal(err)
	}
	return path
}

func statusRun(t *testing.T, s *store.Store, runID string) run.Record {
	t.Helper()
	var rec run.Record
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		status, found, err := run.StatusRecord(r, runID)
		if err != nil || !found {
			return fmt.Errorf("status of %s: found=%v err=%v", runID, found, err)
		}
		var body struct {
			Run run.Record `json:"run"`
		}
		if err := json.Unmarshal(status.Payload, &body); err != nil {
			return err
		}
		rec = body.Run
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	return rec
}

func TestALoopDetectedStreamEndsAsRepeatingCall(t *testing.T) {
	for _, tc := range []struct{ fixture, args string }{
		{"repeating-call-request-b13084fe3e.jsonl", `{"limit":80,"offset":300,"path":"tools/tillr-drain/drain/pass.go"}`},
		{"repeating-call-landing-call-delivery-tests.jsonl", `{"limit":80,"offset":300,"path":"tools/tillr-drain/drain/landing_call_test.go"}`},
	} {
		t.Run(tc.fixture, func(t *testing.T) {
			s, root := leaseFixture(t)
			env, err := run.Exec(context.Background(), s, run.Request{RunID: "run-loop", LaneID: "lane-loop", Backend: "fixture", Command: []string{"/bin/cat", repeatingFixture(t, tc.fixture)}, WorkingDir: "."}, run.ExecOptions{LaneRoot: filepath.Join(root, "lanes"), Now: time.Now().UTC()})
			if err != nil {
				t.Fatalf("Exec: %v", err)
			}
			var rec run.Record
			if err := json.Unmarshal(env.Payload, &rec); err != nil {
				t.Fatal(err)
			}
			want := run.RepeatingCall{Tool: "read", Args: tc.args, ArgsDigest: fmt.Sprintf("%x", sha256.Sum256([]byte(tc.args))), Streak: 6}
			for name, got := range map[string]run.Record{"record": rec, "status": statusRun(t, s, "run-loop")} {
				if got.EndCause != run.EndRepeatingCall || got.State != run.StateExited || got.ExitCode == nil || *got.ExitCode != 0 {
					t.Errorf("%s: cause %q state %q exit %v, want repeating-call, exited 0", name, got.EndCause, got.State, got.ExitCode)
				}
				if got.RepeatingCall == nil || *got.RepeatingCall != want {
					t.Errorf("%s: repeating call %+v, want %+v", name, got.RepeatingCall, want)
				}
			}
		})
	}
}

func TestAStreamWithoutLoopDetectedIsAnOrdinaryExit(t *testing.T) {
	s, root := leaseFixture(t)
	dir := t.TempDir()
	stream := filepath.Join(dir, "healthy.jsonl")
	// The same real calls, without the event: repeated reads alone are not this cause.
	body, err := os.ReadFile(repeatingFixture(t, "repeating-call-request-b13084fe3e.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var kept []byte
	for _, line := range splitLines(body) {
		var e struct{ Type string }
		if json.Unmarshal(line, &e) == nil && e.Type == run.LoopDetectedEvent {
			continue
		}
		kept = append(append(kept, line...), '\n')
	}
	if err := os.WriteFile(stream, kept, 0o600); err != nil {
		t.Fatal(err)
	}
	if _, err := run.Exec(context.Background(), s, run.Request{RunID: "run-plain", LaneID: "lane-plain", Backend: "fixture", Command: []string{"/bin/cat", stream}, WorkingDir: "."}, run.ExecOptions{LaneRoot: filepath.Join(root, "lanes"), Now: time.Now().UTC()}); err != nil {
		t.Fatalf("Exec: %v", err)
	}
	got := statusRun(t, s, "run-plain")
	if got.EndCause != run.EndNormalExit || got.RepeatingCall != nil {
		t.Errorf("cause %q repeating %+v, want normal-exit and none", got.EndCause, got.RepeatingCall)
	}
}

func splitLines(b []byte) [][]byte {
	var out [][]byte
	start := 0
	for i, c := range b {
		if c == '\n' {
			out = append(out, b[start:i])
			start = i + 1
		}
	}
	if start < len(b) {
		out = append(out, b[start:])
	}
	return out
}

func TestARepeatingCallRecordMustNameItsCall(t *testing.T) {
	zero := 0
	base := run.Record{RunID: "run-x", LaneID: "lane-x", Backend: "fixture", State: run.StateExited, ExitCode: &zero,
		StartedAt: "2026-11-19T00:00:00Z", FinishedAt: "2026-11-19T00:01:00Z"}
	for name, tc := range map[string]struct {
		mutate func(*run.Record)
		ok     bool
	}{
		"named": {func(r *run.Record) {
			r.EndCause = run.EndRepeatingCall
			r.RepeatingCall = &run.RepeatingCall{Tool: "read", Streak: 6}
		}, true},
		"unnamed": {func(r *run.Record) { r.EndCause = run.EndRepeatingCall }, false},
		"call-wrong-cause": {func(r *run.Record) {
			r.EndCause = run.EndNormalExit
			r.RepeatingCall = &run.RepeatingCall{Tool: "read"}
		}, false},
	} {
		t.Run(name, func(t *testing.T) {
			s, _ := leaseFixture(t)
			rec := base
			tc.mutate(&rec)
			body, err := json.Marshal(rec)
			if err != nil {
				t.Fatal(err)
			}
			env := record.Envelope{RecordVersion: record.SupportedVersion, Kind: run.RecordKind, ID: "rec-x", Producer: record.Producer{System: record.System, Utility: run.Utility, Version: "test"}, CreatedAt: time.Now().UTC(), Payload: body}
			err = s.WriteTx(context.Background(), func(tx *store.Tx) error { _, err := run.Apply(context.Background(), tx, env); return err })
			if (err == nil) != tc.ok {
				t.Errorf("apply error %v, want ok=%v", err, tc.ok)
			}
		})
	}
}
