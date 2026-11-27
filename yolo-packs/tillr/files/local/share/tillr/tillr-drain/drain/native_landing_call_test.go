package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/mschulkind-oss/tillr-run/native"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestNativeEndingCapabilityWithoutFeaturesProbe(t *testing.T) {
	f, l := passFixture(t, "item-native-cap")
	backendsDir, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends"))
	if err != nil {
		t.Fatal(err)
	}
	runNative := filepath.Join(backendsDir, "native", "run-native")

	// Install trap pi and claude binaries on PATH; recording native's capability
	// must read backends/native/ending-capability directly with zero subprocess probe.
	trapDir := t.TempDir()
	probed := filepath.Join(trapDir, "probed")
	trapScript := "#!/bin/sh\necho probed > '" + probed + "'\nexit 99\n"
	for _, bin := range []string{"pi", "claude"} {
		if err := writeForkSafe(filepath.Join(trapDir, bin), []byte(trapScript), 0755); err != nil {
			t.Fatal(err)
		}
	}
	t.Setenv("PATH", trapDir)

	l.cfg.Policy.DefaultBackend = "native"
	l.cfg.Policy.Backends = map[string]Backend{
		"native": {Adapter: runNative, Model: "mock/test", Effort: "medium"},
	}

	if err := l.recordEndingCapabilities(context.Background()); err != nil {
		t.Fatalf("recordEndingCapabilities: %v", err)
	}
	if _, err := os.Stat(probed); !os.IsNotExist(err) {
		t.Fatalf("expected zero --features subprocess probe for native backend, but trap ran")
	}

	var raw []byte
	fixtureRead(t, f, func(r *store.Reader) error {
		return r.QueryRow(
			`SELECT payload FROM events WHERE kind=? ORDER BY seq DESC LIMIT 1`,
			endingCapabilitiesKind,
		).Scan(&raw)
	})
	var caps map[string]string
	if err := json.Unmarshal(raw, &caps); err != nil {
		t.Fatalf("decode ending capabilities: %v", err)
	}
	if caps[runNative] != run.EndingCallSuspends {
		t.Fatalf("recorded native ending-capability = %q, want %q (all=%v)", caps[runNative], run.EndingCallSuspends, caps)
	}
}

type drainScriptedProvider struct {
	turn  int
	turns []native.TurnResponse
}

func (p *drainScriptedProvider) StreamTurn(_ context.Context, _ native.TurnRequest, _ func(native.StreamDelta)) (native.TurnResponse, error) {
	p.turn++
	idx := p.turn - 1
	if idx >= len(p.turns) {
		return native.TurnResponse{
			ID:         "done",
			StopReason: native.StopEndTurn,
			Content:    []native.ContentBlock{{Type: native.BlockText, Text: "RESULT: done\n"}},
		}, nil
	}
	return p.turns[idx], nil
}

func TestNativeLandingResultPersistedAndReconciled(t *testing.T) {
	const sessionID = "bbbbbbbb-bbbb-4bbb-8bbb-bbbbbbbbbbbb"
	answer := landingAnswer{
		CallID: "call-native-1",
		Kind:   "refused",
		Text:   "Gate refused commit: TestCheck failed.",
	}

	t.Run("nativeLandingResultPersisted validates session, call_id, is_error and text", func(t *testing.T) {
		good := `{"type":"session","session_id":"` + sessionID + `","started_at":"2026-11-27T00:00:00Z","backend":"native","model":"mock/test"}` + "\n" +
			`{"type":"message","timestamp":"2026-11-27T00:00:01Z","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-native-1","content":"Gate refused commit: TestCheck failed.\n","is_error":true}]}}` + "\n"
		if !landingResultPersisted([]byte(good), sessionID, answer) {
			t.Fatalf("expected valid native transcript to match")
		}
		if landingResultPersisted([]byte(good), "other-session", answer) {
			t.Fatalf("mismatched session_id should not match")
		}
		nonErr := `{"type":"session","session_id":"` + sessionID + `"}` + "\n" +
			`{"type":"message","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"call-native-1","content":"Gate refused commit: TestCheck failed.","is_error":false}]}}` + "\n"
		if landingResultPersisted([]byte(nonErr), sessionID, answer) {
			t.Fatalf("is_error=false tool_result should not match")
		}
	})

	t.Run("reconcileLandingDelivery records call.delivered after native SessionTurn replay", func(t *testing.T) {
		f, l := passFixture(t, "item-native-reconcile")
		laneID := "lane-1"
		laneDir := l.laneDirPath(laneID)
		workDir := l.laneTarget(laneID)
		if err := os.MkdirAll(workDir, 0o755); err != nil {
			t.Fatal(err)
		}
		runGit(t, workDir, "init", "-q")
		if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("base\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, workDir, "add", "a.txt")
		runGit(t, workDir, "commit", "-qm", "base")
		baseSHA := runGit(t, workDir, "rev-parse", "HEAD")

		if err := os.WriteFile(filepath.Join(workDir, "a.txt"), []byte("change\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		runGit(t, workDir, "add", "a.txt")
		runGit(t, workDir, "commit", "-qm", "feat: candidate")
		headSHA := runGit(t, workDir, "rev-parse", "HEAD")

		briefPath := filepath.Join(laneDir, "brief.md")
		if err := os.WriteFile(briefPath, []byte("brief\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		run1Durable := run.DurableDir(laneDir, "run-1")
		run2Durable := run.DurableDir(laneDir, "run-2")
		for _, d := range []string{run1Durable, run2Durable} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}

		// Run 1 parks land call into native session transcript.
		var out1 bytes.Buffer
		code := native.Run(context.Background(), native.Config{
			Backend:          "native",
			Model:            "mock/test",
			SessionID:        sessionID,
			ArtifactDir:      laneDir,
			WorkDir:          workDir,
			DurableDir:       run1Durable,
			BaseSHA:          baseSHA,
			EndingCapability: run.EndingCallSuspends,
			BriefPath:        briefPath,
			SessionMode:      "new",
		}, &drainScriptedProvider{
			turns: []native.TurnResponse{{
				ID:         "t1",
				StopReason: native.StopToolUse,
				Content: []native.ContentBlock{{
					Type:  native.BlockToolUse,
					ID:    answer.CallID,
					Name:  "land",
					Input: json.RawMessage(`{}`),
				}},
			}},
		}, nil, &out1)
		if code != native.ExitOK {
			t.Fatalf("run 1 exit=%d", code)
		}
		transcriptPath := native.TranscriptPath(laneDir, sessionID)

		// Record call.offered and call.answered in the store.
		fixtureWrite(t, f, func(tx *store.Tx) error {
			call := landingCall{
				landingOffer: landingOffer{
					CallID:     answer.CallID,
					Commit:     headSHA,
					Base:       baseSHA,
					Transcript: transcriptPath,
				},
				ItemID:     "item-native-reconcile",
				RunID:      "run-1",
				SessionID:  sessionID,
				Worktree:   workDir,
				Scratch:    run1Durable,
				Capability: run.EndingCallSuspends,
				Tool:       "land",
			}
			if err := appendLandingCallEvent(tx, callOffered, answer.CallID, call, l.cfg.Now()); err != nil {
				return err
			}
			return appendLandingCallEvent(tx, callAnswered, answer.CallID, answer, l.cfg.Now())
		})

		// Stage landing-answer.json and landing-transcript for run-2 SessionTurn.
		d := Decision{
			ItemID: "item-native-reconcile",
			LaneID: laneID,
			Returned: &ReturnedEnding{
				RunID: "run-1",
				WorkerSession: &WorkerSession{
					SessionID: sessionID,
				},
			},
			RunRequest: &run.Request{
				RunID:       "run-2",
				ItemID:      "item-native-reconcile",
				LaneID:      laneID,
				SessionTurn: true,
			},
		}
		if err := l.stageLandingAnswer(context.Background(), d, &StageSpec{SessionID: sessionID}); err != nil {
			t.Fatalf("stageLandingAnswer: %v", err)
		}

		// Run 2 resumes and replays landing-answer.json into the native transcript.
		var out2 bytes.Buffer
		code = native.Run(context.Background(), native.Config{
			Backend:          "native",
			Model:            "mock/test",
			SessionID:        sessionID,
			ArtifactDir:      laneDir,
			WorkDir:          workDir,
			DurableDir:       run2Durable,
			BaseSHA:          baseSHA,
			EndingCapability: run.EndingCallSuspends,
			BriefPath:        briefPath,
			SessionMode:      "resume",
		}, &drainScriptedProvider{
			turns: []native.TurnResponse{{
				ID:         "t2",
				StopReason: native.StopToolUse,
				Content: []native.ContentBlock{{
					Type:  native.BlockToolUse,
					ID:    "obs-after-refusal",
					Name:  "obstacle",
					Input: json.RawMessage(`{"kind":"owner","question":"Confirm expected behavior?"}`),
				}},
			}},
		}, nil, &out2)
		if code != native.ExitOK {
			t.Fatalf("run 2 exit=%d", code)
		}

		// Reconcile delivery in tillr-drain and assert call.delivered was recorded.
		if err := l.reconcileLandingDelivery(context.Background(), d); err != nil {
			t.Fatalf("reconcileLandingDelivery: %v", err)
		}
		var delivered int
		fixtureRead(t, f, func(r *store.Reader) error {
			return r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id=?`, callDelivered, answer.CallID).Scan(&delivered)
		})
		if delivered != 1 {
			t.Fatalf("call.delivered count=%d, want 1", delivered)
		}
	})
}
