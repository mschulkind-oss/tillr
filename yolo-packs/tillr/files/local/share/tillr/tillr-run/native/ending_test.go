package native_test

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/native"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

type scriptedProvider struct {
	mu       sync.Mutex
	turns    []native.TurnResponse
	requests []native.TurnRequest
}

func (p *scriptedProvider) StreamTurn(_ context.Context, req native.TurnRequest, _ func(native.StreamDelta)) (native.TurnResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	idx := len(p.requests) - 1
	if idx >= len(p.turns) {
		return native.TurnResponse{
			ID:         "overflow",
			StopReason: native.StopEndTurn,
			Content:    []native.ContentBlock{{Type: native.BlockText, Text: "RESULT: done\n"}},
		}, nil
	}
	return p.turns[idx], nil
}

func initGitWorktree(t *testing.T, dir string) string {
	t.Helper()
	runCmd := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test User",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test User",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runCmd("init", "-q")
	if err := os.WriteFile(filepath.Join(dir, "file.txt"), []byte("base\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd("add", "file.txt")
	runCmd("commit", "-qm", "base")
	return runCmd("rev-parse", "HEAD")
}

func commitChange(t *testing.T, dir, name, content, msg string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, name), []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
	runCmd := func(args ...string) string {
		cmd := exec.Command("git", args...)
		cmd.Dir = dir
		cmd.Env = append(os.Environ(),
			"GIT_AUTHOR_NAME=Test User",
			"GIT_AUTHOR_EMAIL=test@example.com",
			"GIT_COMMITTER_NAME=Test User",
			"GIT_COMMITTER_EMAIL=test@example.com",
		)
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("git %v failed: %v\n%s", args, err, out)
		}
		return strings.TrimSpace(string(out))
	}
	runCmd("add", name)
	runCmd("commit", "-qm", msg)
	return runCmd("rev-parse", "HEAD")
}

func parseStreamEvents(t *testing.T, raw string) []map[string]any {
	t.Helper()
	var events []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(raw), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid stream jsonl %q: %v", line, err)
		}
		events = append(events, m)
	}
	return events
}

type noopExec struct{}

func (noopExec) Definitions() []native.ToolDef {
	return []native.ToolDef{{Name: "read_file", InputSchema: json.RawMessage(`{"type":"object"}`)}}
}

func (noopExec) Execute(_ context.Context, call native.ContentBlock) (string, bool) {
	return "ok:" + call.Name, false
}

func TestEndingToolsValidationAndParking(t *testing.T) {
	t.Run("land refuses HEAD==base and dirty tree then parks after commit", func(t *testing.T) {
		root := t.TempDir()
		artifactDir := filepath.Join(root, "lane")
		workDir := filepath.Join(artifactDir, "worktree")
		durableDir := filepath.Join(root, "scratch")
		for _, d := range []string{artifactDir, workDir, durableDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		baseSHA := initGitWorktree(t, workDir)
		briefPath := filepath.Join(artifactDir, "brief.md")
		if err := os.WriteFile(briefPath, []byte("implement change\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(artifactDir, "ending-capability"), []byte("call-suspends\n"), 0o644); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(artifactDir, "base_sha"), []byte(baseSHA+"\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		var committedSHA string
		turn := 0
		prov := &funcProvider{fn: func(_ context.Context, req native.TurnRequest) (native.TurnResponse, error) {
			turn++
			switch turn {
			case 1:
				// Verify land and obstacle are exposed in call-suspends mode.
				names := map[string]bool{}
				for _, d := range req.Tools {
					names[d.Name] = true
				}
				if !names["land"] || !names["obstacle"] || names["no_change"] {
					t.Fatalf("unexpected tools in call-suspends write run: %v", names)
				}
				// Attempt 1: HEAD == base -> in-turn error.
				return native.TurnResponse{
					ID:         "t1",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "call-land-1",
						Name:  "land",
						Input: json.RawMessage(`{}`),
					}},
				}, nil
			case 2:
				// Confirm in-turn error returned for HEAD == base.
				last := req.Messages[len(req.Messages)-1]
				if len(last.Content) != 1 || !last.Content[0].IsError || !strings.Contains(last.Content[0].Content, "HEAD is still the staged base") {
					t.Fatalf("turn 2 expected HEAD==base error, got %+v", last.Content)
				}
				// Commit a change AND leave a dirty file -> in-turn dirty error.
				commitChange(t, workDir, "a.txt", "v1\n", "feat: add a")
				if err := os.WriteFile(filepath.Join(workDir, "stray.txt"), []byte("dirty\n"), 0o644); err != nil {
					t.Fatal(err)
				}
				return native.TurnResponse{
					ID:         "t2",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "call-land-2",
						Name:  "land",
						Input: json.RawMessage(`{}`),
					}},
				}, nil
			case 3:
				last := req.Messages[len(req.Messages)-1]
				if len(last.Content) != 1 || !last.Content[0].IsError || !strings.Contains(last.Content[0].Content, "stray.txt") {
					t.Fatalf("turn 3 expected dirty path error, got %+v", last.Content)
				}
				// Clean stray file, now call land cleanly -> parks!
				if err := os.Remove(filepath.Join(workDir, "stray.txt")); err != nil {
					t.Fatal(err)
				}
				committedSHA = commitChange(t, workDir, "b.txt", "v2\n", "feat: add b")
				return native.TurnResponse{
					ID:         "t3",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "call-land-3",
						Name:  "land",
						Input: json.RawMessage(`{}`),
					}},
				}, nil
			default:
				t.Fatalf("unexpected turn %d after parked land", turn)
				return native.TurnResponse{}, nil
			}
		}}

		sessionID := "11111111-1111-4111-8111-111111111111"
		var out bytes.Buffer
		code := native.Run(context.Background(), native.Config{
			Backend:     "native",
			Model:       "mock/test",
			SessionID:   sessionID,
			ArtifactDir: artifactDir,
			WorkDir:     workDir,
			DurableDir:  durableDir,
			BriefPath:   briefPath,
			SessionMode: "new",
		}, prov, noopExec{}, &out)
		if code != native.ExitOK {
			t.Fatalf("Run exit=%d, want 0", code)
		}

		// Verify landing-offer.json and landing-state.json exist with expected contents.
		rawOffer, err := os.ReadFile(filepath.Join(durableDir, "landing-offer.json"))
		if err != nil {
			t.Fatalf("read landing-offer.json: %v", err)
		}
		var offer map[string]any
		if err := json.Unmarshal(rawOffer, &offer); err != nil {
			t.Fatalf("decode landing-offer.json: %v", err)
		}
		if offer["call_id"] != "call-land-3" || offer["commit"] != committedSHA || offer["base"] != baseSHA {
			t.Fatalf("unexpected offer: %+v", offer)
		}
		if offer["transcript"] != native.TranscriptPath(artifactDir, sessionID) {
			t.Fatalf("offer transcript=%v, want %s", offer["transcript"], native.TranscriptPath(artifactDir, sessionID))
		}
		if _, err := os.Stat(filepath.Join(durableDir, "landing-state.json")); err != nil {
			t.Fatalf("expected landing-state.json: %v", err)
		}

		// Verify stream emitted parked and did NOT emit agent_settled.
		events := parseStreamEvents(t, out.String())
		sawParked := false
		for _, ev := range events {
			if ev["type"] == "parked" && ev["toolCallId"] == "call-land-3" {
				sawParked = true
			}
			if ev["type"] == "agent_settled" {
				t.Fatalf("parked run must not emit agent_settled")
			}
		}
		if !sawParked {
			t.Fatalf("missing parked event in stream:\n%s", out.String())
		}

		// Verify session transcript ends with unanswered tool_use call-land-3.
		sess, err := native.LoadSession(artifactDir, sessionID)
		if err != nil {
			t.Fatalf("LoadSession: %v", err)
		}
		lastMsg := sess.Messages[len(sess.Messages)-1]
		if lastMsg.Role != native.RoleAssistant || len(lastMsg.Content) != 1 || lastMsg.Content[0].ID != "call-land-3" {
			t.Fatalf("expected unanswered trailing tool_use call-land-3, got %+v", lastMsg)
		}
	})

	t.Run("obstacle validates fields and seat attempt then parks", func(t *testing.T) {
		root := t.TempDir()
		artifactDir := filepath.Join(root, "lane")
		workDir := filepath.Join(artifactDir, "worktree")
		durableDir := filepath.Join(root, "scratch")
		for _, d := range []string{artifactDir, workDir, durableDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		baseSHA := initGitWorktree(t, workDir)
		briefPath := filepath.Join(artifactDir, "brief.md")
		if err := os.WriteFile(briefPath, []byte("brief\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		prov := &scriptedProvider{
			turns: []native.TurnResponse{
				// Missing required fields for definition obstacle.
				{
					ID:         "t1",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "obs-1",
						Name:  "obstacle",
						Input: json.RawMessage(`{"kind":"definition","criteria":"c1"}`),
					}},
				},
				// Seat obstacle with no attempt on clean base -> refused in-turn.
				{
					ID:         "t2",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "obs-2",
						Name:  "obstacle",
						Input: json.RawMessage(`{"kind":"seat","tried":"read","missing":"all"}`),
					}},
				},
				// Valid owner obstacle -> parks!
				{
					ID:         "t3",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "obs-3",
						Name:  "obstacle",
						Input: json.RawMessage(`{"kind":"owner","question":"Which protocol?"}`),
					}},
				},
			},
		}

		sessionID := "22222222-2222-4222-8222-222222222222"
		var out bytes.Buffer
		code := native.Run(context.Background(), native.Config{
			Backend:          "native",
			Model:            "mock/test",
			SessionID:        sessionID,
			ArtifactDir:      artifactDir,
			WorkDir:          workDir,
			DurableDir:       durableDir,
			BaseSHA:          baseSHA,
			EndingCapability: run.EndingCallSuspends,
			BriefPath:        briefPath,
			SessionMode:      "new",
		}, prov, noopExec{}, &out)
		if code != native.ExitOK {
			t.Fatalf("Run exit=%d, want 0", code)
		}
		if len(prov.requests) != 3 {
			t.Fatalf("turns=%d, want 3", len(prov.requests))
		}
		// Check turn 2 saw missing-field error, turn 3 saw seat-needs-attempt error.
		r2 := prov.requests[1].Messages[len(prov.requests[1].Messages)-1].Content[0]
		if !r2.IsError || !strings.Contains(r2.Content, "evidence") {
			t.Fatalf("turn 2 expected missing evidence error, got %+v", r2)
		}
		r3 := prov.requests[2].Messages[len(prov.requests[2].Messages)-1].Content[0]
		if !r3.IsError || !strings.Contains(r3.Content, "seat obstacle needs an attempt") {
			t.Fatalf("turn 3 expected seat-needs-attempt error, got %+v", r3)
		}
		rawOffer, err := os.ReadFile(filepath.Join(durableDir, "landing-offer.json"))
		if err != nil {
			t.Fatalf("read landing-offer.json: %v", err)
		}
		var offer map[string]any
		if err := json.Unmarshal(rawOffer, &offer); err != nil {
			t.Fatal(err)
		}
		if offer["tool"] != "obstacle" || offer["call_id"] != "obs-3" {
			t.Fatalf("unexpected obstacle offer: %+v", offer)
		}
	})

	t.Run("no_change validates dirty tree then parks when clean at base", func(t *testing.T) {
		root := t.TempDir()
		artifactDir := filepath.Join(root, "lane")
		workDir := filepath.Join(artifactDir, "worktree")
		durableDir := filepath.Join(root, "scratch")
		for _, d := range []string{artifactDir, workDir, durableDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		baseSHA := initGitWorktree(t, workDir)
		briefPath := filepath.Join(artifactDir, "brief.md")
		brief := "## Finishing\n\nA reproduced no-change finding may finish with no_change.\n"
		if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
			t.Fatal(err)
		}
		// Dirty the tree first so first no_change fails in-turn.
		if err := os.WriteFile(filepath.Join(workDir, "dirty.txt"), []byte("x\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		turn := 0
		prov := &funcProvider{fn: func(_ context.Context, req native.TurnRequest) (native.TurnResponse, error) {
			turn++
			if turn == 1 {
				return native.TurnResponse{
					ID:         "t1",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "nc-1",
						Name:  "no_change",
						Input: json.RawMessage(`{"cause":"already fixed","evidence":"test passes","why_no_change":"no code needed"}`),
					}},
				}, nil
			}
			last := req.Messages[len(req.Messages)-1].Content[0]
			if !last.IsError || !strings.Contains(last.Content, "dirty.txt") {
				t.Fatalf("turn 2 expected dirty error, got %+v", last)
			}
			_ = os.Remove(filepath.Join(workDir, "dirty.txt"))
			return native.TurnResponse{
				ID:         "t2",
				StopReason: native.StopToolUse,
				Content: []native.ContentBlock{{
					Type:  native.BlockToolUse,
					ID:    "nc-2",
					Name:  "no_change",
					Input: json.RawMessage(`{"cause":"already fixed","evidence":"test passes","why_no_change":"no code needed"}`),
				}},
			}, nil
		}}

		sessionID := "33333333-3333-4333-8333-333333333333"
		var out bytes.Buffer
		code := native.Run(context.Background(), native.Config{
			Backend:          "native",
			Model:            "mock/test",
			SessionID:        sessionID,
			ArtifactDir:      artifactDir,
			WorkDir:          workDir,
			DurableDir:       durableDir,
			BaseSHA:          baseSHA,
			EndingCapability: run.EndingCallSuspends,
			BriefPath:        briefPath,
			SessionMode:      "new",
		}, prov, noopExec{}, &out)
		if code != native.ExitOK {
			t.Fatalf("Run exit=%d, want 0", code)
		}
		rawOffer, err := os.ReadFile(filepath.Join(durableDir, "landing-offer.json"))
		if err != nil {
			t.Fatalf("read landing-offer.json: %v", err)
		}
		var offer map[string]any
		if err := json.Unmarshal(rawOffer, &offer); err != nil {
			t.Fatal(err)
		}
		if offer["tool"] != "no_change" || offer["call_id"] != "nc-2" || offer["base"] != baseSHA {
			t.Fatalf("unexpected no_change offer: %+v", offer)
		}
	})

	t.Run("batched land call returns in-turn error and footer mode omits land", func(t *testing.T) {
		root := t.TempDir()
		artifactDir := filepath.Join(root, "lane")
		workDir := filepath.Join(artifactDir, "worktree")
		durableDir := filepath.Join(root, "scratch")
		for _, d := range []string{artifactDir, workDir, durableDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		baseSHA := initGitWorktree(t, workDir)
		commitChange(t, workDir, "x.txt", "ok\n", "feat: x")
		briefPath := filepath.Join(artifactDir, "brief.md")
		if err := os.WriteFile(briefPath, []byte("brief\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		prov := &scriptedProvider{
			turns: []native.TurnResponse{
				{
					ID:         "t1",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{
						{Type: native.BlockToolUse, ID: "r1", Name: "read_file", Input: json.RawMessage(`{}`)},
						{Type: native.BlockToolUse, ID: "l1", Name: "land", Input: json.RawMessage(`{}`)},
					},
				},
				{
					ID:         "t2",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{
						{Type: native.BlockToolUse, ID: "l2", Name: "land", Input: json.RawMessage(`{}`)},
					},
				},
			},
		}
		var out bytes.Buffer
		code := native.Run(context.Background(), native.Config{
			Backend:          "native",
			Model:            "mock/test",
			SessionID:        "44444444-4444-4444-8444-444444444444",
			ArtifactDir:      artifactDir,
			WorkDir:          workDir,
			DurableDir:       durableDir,
			BaseSHA:          baseSHA,
			EndingCapability: run.EndingCallSuspends,
			BriefPath:        briefPath,
			SessionMode:      "new",
		}, prov, noopExec{}, &out)
		if code != native.ExitOK {
			t.Fatalf("Run exit=%d, want 0", code)
		}
		t2Msgs := prov.requests[1].Messages
		lastUser := t2Msgs[len(t2Msgs)-1]
		if len(lastUser.Content) != 2 || !lastUser.Content[1].IsError || !strings.Contains(lastUser.Content[1].Content, "by itself") {
			t.Fatalf("expected batched land refusal, got %+v", lastUser.Content)
		}

		// Footer mode omits land from tool definitions.
		footerProv := &scriptedProvider{
			turns: []native.TurnResponse{{
				ID:         "f1",
				StopReason: native.StopEndTurn,
				Content:    []native.ContentBlock{{Type: native.BlockText, Text: "RESULT: done\n"}},
			}},
		}
		out.Reset()
		code = native.Run(context.Background(), native.Config{
			Backend:          "native",
			Model:            "mock/test",
			SessionID:        "55555555-5555-4555-8555-555555555555",
			ArtifactDir:      artifactDir,
			WorkDir:          workDir,
			DurableDir:       durableDir,
			BaseSHA:          baseSHA,
			EndingCapability: run.EndingFooter,
			BriefPath:        briefPath,
			SessionMode:      "new",
		}, footerProv, noopExec{}, &out)
		if code != native.ExitOK {
			t.Fatalf("footer Run exit=%d, want 0", code)
		}
		for _, d := range footerProv.requests[0].Tools {
			if d.Name == "land" {
				t.Fatalf("footer mode must not expose land tool")
			}
		}
	})
}

func TestLandingAnswerReplayOnResume(t *testing.T) {
	root := t.TempDir()
	artifactDir := filepath.Join(root, "lane")
	workDir := filepath.Join(artifactDir, "worktree")
	durable1 := filepath.Join(root, "scratch-1")
	durable2 := filepath.Join(root, "scratch-2")
	for _, d := range []string{artifactDir, workDir, durable1, durable2} {
		if err := os.MkdirAll(d, 0o755); err != nil {
			t.Fatal(err)
		}
	}
	baseSHA := initGitWorktree(t, workDir)
	firstSHA := commitChange(t, workDir, "a.txt", "v1\n", "feat: first")
	briefPath := filepath.Join(artifactDir, "brief.md")
	if err := os.WriteFile(briefPath, []byte("brief\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	sessionID := "66666666-6666-4666-8666-666666666666"

	// Run 1: park land call.
	prov1 := &scriptedProvider{
		turns: []native.TurnResponse{{
			ID:         "t1",
			StopReason: native.StopToolUse,
			Content: []native.ContentBlock{{
				Type:  native.BlockToolUse,
				ID:    "call-park-1",
				Name:  "land",
				Input: json.RawMessage(`{}`),
			}},
		}},
	}
	var out1 bytes.Buffer
	code := native.Run(context.Background(), native.Config{
		Backend:          "native",
		Model:            "mock/test",
		SessionID:        sessionID,
		ArtifactDir:      artifactDir,
		WorkDir:          workDir,
		DurableDir:       durable1,
		BaseSHA:          baseSHA,
		EndingCapability: run.EndingCallSuspends,
		BriefPath:        briefPath,
		SessionMode:      "new",
	}, prov1, noopExec{}, &out1)
	if code != native.ExitOK {
		t.Fatalf("run 1 exit=%d", code)
	}

	// Stage landing-answer.json, landing-state.json (with firstSHA refused), and resume prompt for SessionTurn.
	answerText := "Gate refused commit " + firstSHA + ": test TestFoo failed."
	answerPath := filepath.Join(durable2, "landing-answer.json")
	if err := os.WriteFile(answerPath, []byte(`{"call_id":"call-park-1","kind":"refused","text":"`+answerText+`"}`+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	stateJSON, _ := json.Marshal(map[string]any{
		"open":    []any{},
		"refused": []any{map[string]any{"commit": firstSHA, "answer": answerText}},
	})
	if err := os.WriteFile(filepath.Join(durable2, "landing-state.json"), stateJSON, 0o600); err != nil {
		t.Fatal(err)
	}
	resumePromptPath := filepath.Join(durable2, "resume-prompt.md")
	if err := os.WriteFile(resumePromptPath, []byte("Fix TestFoo and offer a new commit.\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Run 2 (SessionTurn resume): first tries offering the same refused commit (rejected in-turn), then commits fix and parks.
	var secondSHA string
	turn := 0
	prov2 := &funcProvider{fn: func(_ context.Context, req native.TurnRequest) (native.TurnResponse, error) {
		turn++
		if turn == 1 {
			// Verify the replayed landing-answer.json arrived as an is_error: true tool_result.
			last := req.Messages[len(req.Messages)-1]
			if last.Role != native.RoleUser || len(last.Content) < 1 {
				t.Fatalf("expected user message with replayed tool_result, got %+v", last)
			}
			res := last.Content[0]
			if res.Type != native.BlockToolResult || res.ToolUseID != "call-park-1" || !res.IsError || res.Content != answerText {
				t.Fatalf("unexpected replayed tool_result: %+v", res)
			}
			if len(last.Content) != 2 || last.Content[1].Type != native.BlockText || !strings.Contains(last.Content[1].Text, "Fix TestFoo") {
				t.Fatalf("expected resume prompt text alongside tool_result, got %+v", last.Content)
			}
			// Re-offering already-refused firstSHA must fail in-turn.
			return native.TurnResponse{
				ID:         "r1",
				StopReason: native.StopToolUse,
				Content: []native.ContentBlock{{
					Type:  native.BlockToolUse,
					ID:    "call-park-2a",
					Name:  "land",
					Input: json.RawMessage(`{}`),
				}},
			}, nil
		}
		last := req.Messages[len(req.Messages)-1].Content[0]
		if !last.IsError || !strings.Contains(last.Content, "already refused") {
			t.Fatalf("expected already-refused error, got %+v", last)
		}
		secondSHA = commitChange(t, workDir, "a.txt", "v2\n", "fix: fix TestFoo")
		return native.TurnResponse{
			ID:         "r2",
			StopReason: native.StopToolUse,
			Content: []native.ContentBlock{{
				Type:  native.BlockToolUse,
				ID:    "call-park-2b",
				Name:  "land",
				Input: json.RawMessage(`{}`),
			}},
		}, nil
	}}

	var out2 bytes.Buffer
	code = native.Run(context.Background(), native.Config{
		Backend:          "native",
		Model:            "mock/test",
		SessionID:        sessionID,
		ArtifactDir:      artifactDir,
		WorkDir:          workDir,
		DurableDir:       durable2,
		BaseSHA:          baseSHA,
		EndingCapability: run.EndingCallSuspends,
		BriefPath:        briefPath,
		ResumePromptPath: resumePromptPath,
		SessionMode:      "resume",
	}, prov2, noopExec{}, &out2)
	if code != native.ExitOK {
		t.Fatalf("run 2 exit=%d", code)
	}
	if _, err := os.Stat(answerPath); !os.IsNotExist(err) {
		t.Fatalf("landing-answer.json should be deleted after replay, err=%v", err)
	}
	rawOffer, err := os.ReadFile(filepath.Join(durable2, "landing-offer.json"))
	if err != nil {
		t.Fatalf("read second landing-offer.json: %v", err)
	}
	var offer map[string]any
	if err := json.Unmarshal(rawOffer, &offer); err != nil {
		t.Fatal(err)
	}
	if offer["call_id"] != "call-park-2b" || offer["commit"] != secondSHA {
		t.Fatalf("unexpected second offer: %+v", offer)
	}
}

func TestKeepGoingNudges(t *testing.T) {
	t.Run("nudges uncommitted writer in call-suspends mode until commit and land", func(t *testing.T) {
		root := t.TempDir()
		artifactDir := filepath.Join(root, "lane")
		workDir := filepath.Join(artifactDir, "worktree")
		durableDir := filepath.Join(root, "scratch")
		for _, d := range []string{artifactDir, workDir, durableDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		baseSHA := initGitWorktree(t, workDir)
		briefPath := filepath.Join(artifactDir, "brief.md")
		if err := os.WriteFile(briefPath, []byte("brief\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		turn := 0
		prov := &funcProvider{fn: func(_ context.Context, req native.TurnRequest) (native.TurnResponse, error) {
			turn++
			if turn == 1 {
				// Leave dirty file and try to end with plain text.
				_ = os.WriteFile(filepath.Join(workDir, "wip.txt"), []byte("wip\n"), 0o644)
				return native.TurnResponse{
					ID:         "t1",
					StopReason: native.StopEndTurn,
					Content:    []native.ContentBlock{{Type: native.BlockText, Text: "I edited wip.txt."}},
				}, nil
			}
			last := req.Messages[len(req.Messages)-1]
			if last.Role != native.RoleUser || !strings.Contains(last.Content[0].Text, "wip.txt") || !strings.Contains(last.Content[0].Text, "land") {
				t.Fatalf("expected keep-going nudge naming wip.txt and land, got %+v", last)
			}
			commitChange(t, workDir, "wip.txt", "wip\n", "feat: commit wip")
			return native.TurnResponse{
				ID:         "t2",
				StopReason: native.StopToolUse,
				Content: []native.ContentBlock{{
					Type:  native.BlockToolUse,
					ID:    "land-after-nudge",
					Name:  "land",
					Input: json.RawMessage(`{}`),
				}},
			}, nil
		}}

		var out bytes.Buffer
		code := native.Run(context.Background(), native.Config{
			Backend:          "native",
			Model:            "mock/test",
			SessionID:        "77777777-7777-4777-8777-777777777777",
			ArtifactDir:      artifactDir,
			WorkDir:          workDir,
			DurableDir:       durableDir,
			BaseSHA:          baseSHA,
			EndingCapability: run.EndingCallSuspends,
			BriefPath:        briefPath,
			SessionMode:      "new",
		}, prov, noopExec{}, &out)
		if code != native.ExitOK || turn != 2 {
			t.Fatalf("code=%d turn=%d, want 0 and 2", code, turn)
		}
		if _, err := os.Stat(filepath.Join(durableDir, "landing-offer.json")); err != nil {
			t.Fatalf("expected landing-offer.json after nudge: %v", err)
		}
	})

	t.Run("caps keep-going nudges at 3 in footer mode", func(t *testing.T) {
		root := t.TempDir()
		artifactDir := filepath.Join(root, "lane")
		workDir := filepath.Join(artifactDir, "worktree")
		for _, d := range []string{artifactDir, workDir} {
			if err := os.MkdirAll(d, 0o755); err != nil {
				t.Fatal(err)
			}
		}
		baseSHA := initGitWorktree(t, workDir)
		briefPath := filepath.Join(artifactDir, "brief.md")
		if err := os.WriteFile(briefPath, []byte("brief\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		prov := &scriptedProvider{
			turns: []native.TurnResponse{
				{ID: "t1", StopReason: native.StopEndTurn, Content: []native.ContentBlock{{Type: native.BlockText, Text: "still working"}}},
				{ID: "t2", StopReason: native.StopEndTurn, Content: []native.ContentBlock{{Type: native.BlockText, Text: "still working"}}},
				{ID: "t3", StopReason: native.StopEndTurn, Content: []native.ContentBlock{{Type: native.BlockText, Text: "still working"}}},
				{ID: "t4", StopReason: native.StopEndTurn, Content: []native.ContentBlock{{Type: native.BlockText, Text: "giving up"}}},
			},
		}
		var out bytes.Buffer
		code := native.Run(context.Background(), native.Config{
			Backend:          "native",
			Model:            "mock/test",
			SessionID:        "88888888-8888-4888-8888-888888888888",
			ArtifactDir:      artifactDir,
			WorkDir:          workDir,
			BaseSHA:          baseSHA,
			EndingCapability: run.EndingFooter,
			BriefPath:        briefPath,
			SessionMode:      "new",
		}, prov, noopExec{}, &out)
		if code != native.ExitOK {
			t.Fatalf("code=%d, want 0", code)
		}
		// 1 initial turn + 3 bounded nudges = 4 total provider turns.
		if len(prov.requests) != 4 {
			t.Fatalf("turns=%d, want 4 (1 initial + 3 nudges)", len(prov.requests))
		}
	})
}

func TestRepeatingCallRecoveryAndEndRepeatingCallViaRunExec(t *testing.T) {
	t.Run("recovers at streak 4 when model changes call", func(t *testing.T) {
		root := t.TempDir()
		artifactDir := filepath.Join(root, "lane")
		if err := os.MkdirAll(artifactDir, 0o755); err != nil {
			t.Fatal(err)
		}
		briefPath := filepath.Join(artifactDir, "brief.md")
		if err := os.WriteFile(briefPath, []byte("brief\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		// 4 identical calls (with different JSON key ordering on call 3), then a done turn.
		prov := &scriptedProvider{
			turns: []native.TurnResponse{
				{ID: "t1", StopReason: native.StopToolUse, Content: []native.ContentBlock{{Type: native.BlockToolUse, ID: "c1", Name: "read_file", Input: json.RawMessage(`{"path":"a.go","offset":1}`)}}},
				{ID: "t2", StopReason: native.StopToolUse, Content: []native.ContentBlock{{Type: native.BlockToolUse, ID: "c2", Name: "read_file", Input: json.RawMessage(`{"path":"a.go","offset":1}`)}}},
				{ID: "t3", StopReason: native.StopToolUse, Content: []native.ContentBlock{{Type: native.BlockToolUse, ID: "c3", Name: "read_file", Input: json.RawMessage(`{"offset":1,"path":"a.go"}`)}}},
				{ID: "t4", StopReason: native.StopToolUse, Content: []native.ContentBlock{{Type: native.BlockToolUse, ID: "c4", Name: "read_file", Input: json.RawMessage(`{"path":"a.go","offset":1}`)}}},
				{ID: "t5", StopReason: native.StopEndTurn, Content: []native.ContentBlock{{Type: native.BlockText, Text: "Recovered.\nRESULT: done\n"}}},
			},
		}

		var out bytes.Buffer
		code := native.Run(context.Background(), native.Config{
			Backend:     "native",
			Model:       "mock/test",
			SessionID:   "99999999-9999-4999-8999-999999999999",
			ArtifactDir: artifactDir,
			BriefPath:   briefPath,
			SessionMode: "new",
		}, prov, noopExec{}, &out)
		if code != native.ExitOK {
			t.Fatalf("Run exit=%d, want 0", code)
		}

		// Turn 5 must have received the recovery warning alongside tool_result c4.
		t5Msgs := prov.requests[4].Messages
		lastUser := t5Msgs[len(t5Msgs)-1]
		if len(lastUser.Content) != 2 || !strings.Contains(lastUser.Content[1].Text, "identical arguments 4 times") {
			t.Fatalf("expected recovery warning in turn 5, got %+v", lastUser.Content)
		}

		events := parseStreamEvents(t, out.String())
		sawRecovered := false
		sawSettled := false
		for _, ev := range events {
			if ev["type"] == "loop_detected" && ev["action"] == "recovered" {
				sawRecovered = true
			}
			if ev["type"] == "agent_settled" {
				sawSettled = true
			}
		}
		if !sawRecovered || !sawSettled {
			t.Fatalf("sawRecovered=%v sawSettled=%v, want both true", sawRecovered, sawSettled)
		}
	})

	t.Run("records EndRepeatingCall on streak 6 through tillr-run Exec", func(t *testing.T) {
		bin := tillrRunBinary(t)
		backendsDir, err := filepath.Abs(filepath.Join("..", "backends"))
		if err != nil {
			t.Fatal(err)
		}
		runNative := filepath.Join(backendsDir, "native", "run-native")

		turn := 0
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			turn++
			w.Header().Set("Content-Type", "text/event-stream")
			writeSSEEvent(w, map[string]any{
				"type": "response",
				"response": native.TurnResponse{
					ID:         "rep",
					Model:      "mock/test-1",
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{{
						Type:  native.BlockToolUse,
						ID:    "rep-call",
						Name:  "read_file",
						Input: json.RawMessage(`{"path":"file.txt"}`),
					}},
					Usage: native.Usage{Input: 10, Output: 5, TotalTokens: 15},
				},
			})
		}))
		defer srv.Close()

		root := t.TempDir()
		s, err := store.Open(context.Background(), filepath.Join(root, "state.db"), true)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()

		laneRoot := filepath.Join(root, "lanes")
		sessionID := "aaaaaaaa-aaaa-4aaa-8aaa-aaaaaaaaaaaa"
		_, workDir, briefPath := stageNativeLane(t, laneRoot, "lane-rep", sessionID)
		if err := os.WriteFile(filepath.Join(workDir, "file.txt"), []byte("hello\n"), 0o644); err != nil {
			t.Fatal(err)
		}

		t.Setenv("TILLR_RUN_BIN", bin)
		t.Setenv("TILLR_NATIVE_MOCK_URL", srv.URL)

		env, err := run.Exec(context.Background(), s, run.Request{
			RunID:      "run-native-rep",
			ItemID:     "item-native-rep",
			LaneID:     "lane-rep",
			Backend:    "native",
			WorkingDir: run.WorktreeDir,
			Command:    []string{runNative, "--backend", "native", "--model", "mock/test-1", "--brief", briefPath, "--target", workDir},
		}, run.ExecOptions{
			LaneRoot:    laneRoot,
			BackendsDir: backendsDir,
			Now:         time.Now().UTC(),
		})
		if err != nil {
			t.Fatalf("run.Exec: %v", err)
		}
		rec := decodeRunRecord(t, env.Payload)
		exitCode := -1
		if rec.ExitCode != nil {
			exitCode = *rec.ExitCode
		}
		if rec.EndCause != run.EndRepeatingCall || rec.State != run.StateExited || exitCode != 0 {
			errBytes, _ := os.ReadFile(filepath.Join(laneRoot, "lane-rep", "stream.err"))
			logBytes, _ := os.ReadFile(filepath.Join(laneRoot, "lane-rep", "run-native-rep.log"))
			t.Fatalf("record cause=%q state=%q exit=%d, want repeating-call exited 0\nstderr:\n%s\nlog:\n%s", rec.EndCause, rec.State, exitCode, errBytes, logBytes)
		}
		if rec.RepeatingCall == nil || rec.RepeatingCall.Tool != "read_file" || rec.RepeatingCall.Streak != 6 || rec.RepeatingCall.Action != "ended" {
			t.Fatalf("unexpected RepeatingCall: %+v", rec.RepeatingCall)
		}
		if rec.LoopRecovery == nil || rec.LoopRecovery.Count != 1 {
			t.Fatalf("expected 1 LoopRecovery at streak 4 before ending at 6, got %+v", rec.LoopRecovery)
		}
	})
}

type funcProvider struct {
	fn func(ctx context.Context, req native.TurnRequest) (native.TurnResponse, error)
}

func (f *funcProvider) StreamTurn(ctx context.Context, req native.TurnRequest, _ func(native.StreamDelta)) (native.TurnResponse, error) {
	return f.fn(ctx, req)
}
