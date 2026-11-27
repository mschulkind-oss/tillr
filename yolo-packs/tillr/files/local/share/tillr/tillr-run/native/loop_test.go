package native_test

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/native"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

var (
	builtRunBinOnce sync.Once
	builtRunBinPath string
	builtRunBinErr  error
)

func tillrRunBinary(t *testing.T) string {
	t.Helper()
	builtRunBinOnce.Do(func() {
		dir, err := os.MkdirTemp("", "tillr-run-native-test-*")
		if err != nil {
			builtRunBinErr = err
			return
		}
		bin := filepath.Join(dir, "tillr-run")
		cmd := exec.Command("go", "build", "-o", bin, "..")
		cmd.Env = append(os.Environ(), "CGO_ENABLED=0")
		if out, err := cmd.CombinedOutput(); err != nil {
			builtRunBinErr = fmt.Errorf("building tillr-run: %v\n%s", err, out)
			return
		}
		builtRunBinPath = bin
	})
	if builtRunBinErr != nil {
		t.Fatal(builtRunBinErr)
	}
	return builtRunBinPath
}

func stageNativeLane(t *testing.T, laneRoot, laneID, sessionID string) (laneDir, workDir, briefPath string) {
	t.Helper()
	laneDir = filepath.Join(laneRoot, laneID)
	workDir = filepath.Join(laneDir, run.WorktreeDir)
	if err := os.MkdirAll(workDir, 0o755); err != nil {
		t.Fatal(err)
	}
	rawTemplate, err := os.ReadFile(filepath.Join("..", "backends", "brief-template.md"))
	if err != nil {
		t.Fatal(err)
	}
	replacements := map[string]string{
		"ITEM_ID":            "item-native-1",
		"TITLE":              "offline native test",
		"RUN_ID":             "run-native-1",
		"BACKEND":            "native",
		"BASE_SHA":           strings.Repeat("a", 40),
		"TARGET":             workDir,
		"TARGET_KIND":        "lane worktree",
		"DATE":               "2026-11-27",
		"TIMEOUT_SECONDS":    "0",
		"DRIVER_REASON":      "unused",
		"MAIN_STATUS_BEFORE": "unused",
		"TASK":               "Verify slice 1 offline.",
		"CRITERION_1":        "emits RESULT: done",
		"OUT_OF_FENCE_PATHS": "- everything else",
		"SEAT_LINES":         "- `build` — build",
		"SETTLED":            "- none",
		"FACTS":              "- none",
		"METHOD":             "run mock turn",
		"VERIFICATION":       "```\ntrue\n```",
	}
	brief := string(rawTemplate)
	for k, v := range replacements {
		brief = strings.ReplaceAll(brief, "{{"+k+"}}", v)
	}
	briefPath = filepath.Join(laneDir, "brief.md")
	if err := os.WriteFile(briefPath, []byte(brief), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(laneDir, run.SessionIDFile), []byte(sessionID+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	return laneDir, workDir, briefPath
}

func writeSSEEvent(w http.ResponseWriter, v any) {
	raw, _ := json.Marshal(v)
	fmt.Fprintf(w, "data: %s\n\n", raw)
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
}

func decodeRunRecord(t *testing.T, payload json.RawMessage) run.Record {
	t.Helper()
	var rec run.Record
	if err := json.Unmarshal(payload, &rec); err != nil {
		t.Fatalf("decoding run.Record: %v", err)
	}
	return rec
}

func TestLoopbackProviderRefusesNonLoopback(t *testing.T) {
	for _, bad := range []string{
		"https://api.anthropic.com/v1/messages",
		"http://example.com/turn",
		"https://127.0.0.1:8080/turn",
	} {
		if _, err := native.NewLoopbackProvider(bad, nil); err == nil {
			t.Errorf("NewLoopbackProvider(%q) succeeded, want refusal", bad)
		}
	}
}

func TestExecColdStartWritesHarnessSessionReportAndUsage(t *testing.T) {
	cost := 0.0025
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req native.TurnRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSEEvent(w, map[string]any{
			"type":  "delta",
			"delta": native.StreamDelta{Type: native.DeltaText, Delta: "All checks passed.\nRESULT: done\n"},
		})
		writeSSEEvent(w, map[string]any{
			"type": "response",
			"response": native.TurnResponse{
				ID:         "msg-cold-1",
				Model:      req.Model,
				StopReason: native.StopEndTurn,
				Content:    []native.ContentBlock{{Type: native.BlockText, Text: "All checks passed.\nRESULT: done\n"}},
				Usage: native.Usage{
					Input:       120,
					Output:      35,
					CacheRead:   10,
					TotalTokens: 165,
					CostUSD:     &cost,
				},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("TILLR_RUN_BIN", tillrRunBinary(t))
	t.Setenv("TILLR_NATIVE_MOCK_URL", srv.URL)

	tmp := t.TempDir()
	storePath := filepath.Join(tmp, "state.db")
	laneRoot := filepath.Join(tmp, "lanes")
	sessionID := "11111111-1111-4111-8111-111111111111"
	laneDir, workDir, _ := stageNativeLane(t, laneRoot, "lane-1", sessionID)

	ctx := context.Background()
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	adapter, err := filepath.Abs(filepath.Join("..", "backends", "native", "run-native"))
	if err != nil {
		t.Fatal(err)
	}
	backendsDir, err := filepath.Abs(filepath.Join("..", "backends"))
	if err != nil {
		t.Fatal(err)
	}
	req := run.Request{
		RunID:      "run-native-cold-1",
		ItemID:     "item-native-1",
		LaneID:     "lane-1",
		Backend:    "native",
		Command:    []string{adapter, "--backend", "native", "--model", "mock/model-1", "--effort", "medium"},
		WorkingDir: run.WorktreeDir,
	}

	env, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: laneRoot, BackendsDir: backendsDir, Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("run.Exec failed: %v", err)
	}
	rec := decodeRunRecord(t, env.Payload)
	if rec.State != run.StateExited || rec.ExitCode == nil || *rec.ExitCode != 0 {
		t.Fatalf("record state=%q exit=%v, want exited 0", rec.State, rec.ExitCode)
	}
	if !strings.HasPrefix(rec.Harness, "tillr-native/") {
		t.Fatalf("Record.Harness = %q, want prefix tillr-native/", rec.Harness)
	}
	if rec.ExtensionSet != "" {
		t.Fatalf("Record.ExtensionSet = %q, want empty", rec.ExtensionSet)
	}

	// Verify session transcript exists outside workDir.
	transcriptPath := native.TranscriptPath(laneDir, sessionID)
	if strings.HasPrefix(transcriptPath, workDir) {
		t.Fatalf("transcript %s must live outside workDir %s", transcriptPath, workDir)
	}
	loaded, err := native.LoadSession(laneDir, sessionID)
	if err != nil {
		t.Fatalf("LoadSession: %v", err)
	}
	if len(loaded.Messages) != 2 || loaded.Messages[0].Role != native.RoleUser || loaded.Messages[1].Role != native.RoleAssistant {
		t.Fatalf("unexpected transcript messages: %+v", loaded.Messages)
	}

	// Verify 19-key report.md (both pinned artifact and lane copy) and .usage.json.
	var pinnedReport, scratchDir string
	for _, ref := range rec.ArtifactRefs {
		switch ref.Kind {
		case "report":
			pinnedReport = ref.Ref
		case "scratch":
			scratchDir = ref.Ref
		}
	}
	if pinnedReport == "" {
		t.Fatalf("Record.ArtifactRefs missing report ref: %+v", rec.ArtifactRefs)
	}
	if scratchDir == "" {
		t.Fatalf("Record.ArtifactRefs missing scratch ref: %+v", rec.ArtifactRefs)
	}
	reportPath := filepath.Join(laneDir, "report.md")
	rawReport, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatalf("reading report.md: %v", err)
	}
	header, _, ok := strings.Cut(string(rawReport), "\n---\n")
	if !ok {
		t.Fatalf("report.md missing delimiter:\n%s", rawReport)
	}
	headerLines := 0
	for _, line := range strings.Split(header, "\n") {
		if strings.Contains(line, ": ") {
			headerLines++
		}
	}
	if headerLines != 19 {
		t.Fatalf("report.md header keys = %d, want 19:\n%s", headerLines, header)
	}
	if !strings.Contains(header, "tokens: 165") || !strings.Contains(header, "cost usd: 0.002500") || !strings.Contains(header, "result: done") {
		t.Fatalf("unexpected report.md header:\n%s", header)
	}

	usageFiles, err := filepath.Glob(filepath.Join(scratchDir, "spend-*", "report.md.usage.json"))
	if err != nil || len(usageFiles) == 0 {
		t.Fatalf("expected report.md.usage.json under %s/spend-*, got %v (err=%v)", scratchDir, usageFiles, err)
	}
	rawUsage, err := os.ReadFile(usageFiles[0])
	if err != nil {
		t.Fatalf("reading %s: %v", usageFiles[0], err)
	}
	var details run.UsageDetails
	if err := json.Unmarshal(rawUsage, &details); err != nil {
		t.Fatalf("parsing .usage.json: %v", err)
	}
	if len(details.Models) != 1 || len(details.Turns) != 1 {
		t.Fatalf("unexpected usage details: %+v", details)
	}
	if rec.Usage == nil || rec.Usage.Details == nil || len(rec.Usage.Details.Turns) != 1 {
		t.Fatalf("Record.Usage.Details = %+v, want 1 turn", rec.Usage)
	}
}

func TestExecPauseAtTurnGapAndResumeContinuesSameSession(t *testing.T) {
	var (
		mu          sync.Mutex
		calls       int
		turn1Active = make(chan struct{})
		releaseTurn = make(chan struct{})
		secondReq   native.TurnRequest
	)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var req native.TurnRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		mu.Lock()
		calls++
		callNum := calls
		if callNum == 2 {
			secondReq = req
		}
		mu.Unlock()

		w.Header().Set("Content-Type", "text/event-stream")
		if callNum == 1 {
			close(turn1Active)
			<-releaseTurn
			// Return a tool_use block so native-agent flushes the tool result
			// and hits the turn-gap pause check cleanly.
			writeSSEEvent(w, map[string]any{
				"type": "response",
				"response": native.TurnResponse{
					ID:         "msg-pause-1",
					Model:      req.Model,
					StopReason: native.StopToolUse,
					Content: []native.ContentBlock{
						{Type: native.BlockText, Text: "Checking state."},
						{Type: native.BlockToolUse, ID: "call_pause_1", Name: "noop", Input: json.RawMessage(`{}`)},
					},
					Usage: native.Usage{Input: 50, Output: 15, TotalTokens: 65},
				},
			})
			return
		}
		writeSSEEvent(w, map[string]any{
			"type": "response",
			"response": native.TurnResponse{
				ID:         "msg-pause-2",
				Model:      req.Model,
				StopReason: native.StopEndTurn,
				Content:    []native.ContentBlock{{Type: native.BlockText, Text: "Resumed and finished.\nRESULT: done\n"}},
				Usage:      native.Usage{Input: 90, Output: 20, TotalTokens: 110},
			},
		})
	}))
	defer srv.Close()

	t.Setenv("TILLR_RUN_BIN", tillrRunBinary(t))
	t.Setenv("TILLR_NATIVE_MOCK_URL", srv.URL)

	tmp := t.TempDir()
	storePath := filepath.Join(tmp, "state.db")
	laneRoot := filepath.Join(tmp, "lanes")
	sessionID := "22222222-2222-4222-8222-222222222222"
	stageNativeLane(t, laneRoot, "lane-1", sessionID)

	ctx := context.Background()
	s, err := store.Open(ctx, storePath, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()

	adapter, _ := filepath.Abs(filepath.Join("..", "backends", "native", "run-native"))
	backendsDir, _ := filepath.Abs(filepath.Join("..", "backends"))
	req := run.Request{
		RunID:      "run-native-pause-1",
		ItemID:     "item-native-1",
		LaneID:     "lane-1",
		Backend:    "native",
		Command:    []string{adapter, "--backend", "native", "--model", "mock/model-1"},
		WorkingDir: run.WorktreeDir,
	}

	// Request a pause as soon as turn 1 reaches the provider, then release turn 1.
	go func() {
		<-turn1Active
		_ = run.RequestPause(ctx, s, req.RunID, 30*time.Second, time.Now().UTC())
		// Wait briefly so exec observes run.pause-request and writes TILLR_PAUSE_REQUEST.
		time.Sleep(150 * time.Millisecond)
		close(releaseTurn)
	}()

	env, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: laneRoot, BackendsDir: backendsDir, Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("first run.Exec failed: %v", err)
	}
	rec := decodeRunRecord(t, env.Payload)
	if rec.State != run.StatePaused {
		t.Fatalf("first run state = %q, want %q", rec.State, run.StatePaused)
	}

	// Now resume the same run and verify it continues the same session.
	req.Resume = true
	req.ResumePrompt = "Continue from where you paused."
	env2, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: laneRoot, BackendsDir: backendsDir, Now: time.Now().UTC()})
	if err != nil {
		t.Fatalf("resumed run.Exec failed: %v", err)
	}
	rec2 := decodeRunRecord(t, env2.Payload)
	if rec2.State != run.StateExited || rec2.ExitCode == nil || *rec2.ExitCode != 0 {
		t.Fatalf("resumed run state=%q exit=%v, want exited 0", rec2.State, rec2.ExitCode)
	}

	mu.Lock()
	defer mu.Unlock()
	if calls != 2 {
		t.Fatalf("provider calls = %d, want 2", calls)
	}
	if secondReq.SessionID != sessionID {
		t.Fatalf("resumed SessionID = %q, want %q", secondReq.SessionID, sessionID)
	}
	// History on turn 2 must carry: initial user brief, turn-1 assistant tool_use,
	// turn-1 user tool_result, and the resume user prompt.
	if len(secondReq.Messages) != 4 {
		t.Fatalf("resumed message count = %d, want 4: %+v", len(secondReq.Messages), secondReq.Messages)
	}
}

func TestInterruptedTrailingToolUseRepairsCleanlyOnResume(t *testing.T) {
	dir := t.TempDir()
	sessionID := "33333333-3333-4333-8333-333333333333"
	sess, err := native.OpenNewSession(dir, native.SessionHeader{
		SessionID: sessionID,
		Backend:   "native",
		Model:     "mock/model-1",
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := sess.AppendMessage(native.Message{
		Role:    native.RoleUser,
		Content: []native.ContentBlock{{Type: native.BlockText, Text: "initial brief"}},
	}); err != nil {
		t.Fatal(err)
	}
	// Simulate crash/SIGKILL right after assistant emitted a tool_use with no tool_result.
	if err := sess.AppendMessage(native.Message{
		Role: native.RoleAssistant,
		Content: []native.ContentBlock{
			{Type: native.BlockToolUse, ID: "call_interrupted_99", Name: "bash", Input: json.RawMessage(`{"command":"sleep 99"}`)},
		},
	}); err != nil {
		t.Fatal(err)
	}

	resumePrompt := filepath.Join(dir, "resume.md")
	if err := os.WriteFile(resumePrompt, []byte("Continue after interruption."), 0o644); err != nil {
		t.Fatal(err)
	}

	var gotReq native.TurnRequest
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&gotReq); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		w.Header().Set("Content-Type", "text/event-stream")
		writeSSEEvent(w, map[string]any{
			"type": "response",
			"response": native.TurnResponse{
				ID:         "msg-repaired-1",
				Model:      gotReq.Model,
				StopReason: native.StopEndTurn,
				Content:    []native.ContentBlock{{Type: native.BlockText, Text: "Recovered.\nRESULT: done\n"}},
				Usage:      native.Usage{Input: 40, Output: 10, TotalTokens: 50},
			},
		})
	}))
	defer srv.Close()

	provider, err := native.NewLoopbackProvider(srv.URL, srv.Client())
	if err != nil {
		t.Fatal(err)
	}
	var stream bytes.Buffer
	code := native.Run(context.Background(), native.Config{
		Backend:          "native",
		Model:            "mock/model-1",
		SessionID:        sessionID,
		ArtifactDir:      dir,
		ResumePromptPath: resumePrompt,
		SessionMode:      "resume",
	}, provider, nil, &stream)
	if code != native.ExitOK {
		t.Fatalf("native.Run exit = %d, want 0", code)
	}

	// Expect: user(brief), assistant(tool_use), user(synthetic error tool_result), user(resume prompt).
	if len(gotReq.Messages) != 4 {
		t.Fatalf("provider received %d messages, want 4: %+v", len(gotReq.Messages), gotReq.Messages)
	}
	repaired := gotReq.Messages[2]
	if repaired.Role != native.RoleUser || len(repaired.Content) != 1 {
		t.Fatalf("repaired message = %+v, want user with 1 tool_result", repaired)
	}
	blk := repaired.Content[0]
	if blk.Type != native.BlockToolResult || blk.ToolUseID != "call_interrupted_99" || !blk.IsError {
		t.Fatalf("repaired block = %+v, want is_error tool_result for call_interrupted_99", blk)
	}
}

func TestExecRateLimitRefusedWindowAndModelTimeoutEvent(t *testing.T) {
	t.Run("rate_limit_event satisfies RefusedWindow headers", func(t *testing.T) {
		resetAt := time.Now().UTC().Add(2 * time.Hour).Unix()
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/json")
			w.WriteHeader(http.StatusTooManyRequests)
			_ = json.NewEncoder(w).Encode(map[string]any{
				"error":    "quota exhausted",
				"terminal": true,
				"rate_limit": native.RateLimitInfo{
					RateLimitType: "five_hour",
					Utilization:   1,
					Status:        "rejected",
					ResetsAt:      resetAt,
				},
			})
		}))
		defer srv.Close()

		t.Setenv("TILLR_RUN_BIN", tillrRunBinary(t))
		t.Setenv("TILLR_NATIVE_MOCK_URL", srv.URL)

		tmp := t.TempDir()
		storePath := filepath.Join(tmp, "state.db")
		laneRoot := filepath.Join(tmp, "lanes")
		sessionID := "44444444-4444-4444-8444-444444444444"
		laneDir, _, _ := stageNativeLane(t, laneRoot, "lane-1", sessionID)

		ctx := context.Background()
		s, err := store.Open(ctx, storePath, true)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()

		adapter, _ := filepath.Abs(filepath.Join("..", "backends", "native", "run-native"))
		backendsDir, _ := filepath.Abs(filepath.Join("..", "backends"))
		req := run.Request{
			RunID:      "run-native-rl-1",
			ItemID:     "item-native-1",
			LaneID:     "lane-1",
			Backend:    "native",
			Command:    []string{adapter, "--backend", "native", "--model", "mock/model-1"},
			WorkingDir: run.WorktreeDir,
		}

		env, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: laneRoot, BackendsDir: backendsDir, Now: time.Now().UTC()})
		if err != nil {
			t.Fatalf("run.Exec failed: %v", err)
		}
		rec := decodeRunRecord(t, env.Payload)
		if rec.State != run.StateExited || rec.ExitCode == nil || *rec.ExitCode == 0 {
			t.Fatalf("record state=%q exit=%v, want non-zero exit", rec.State, rec.ExitCode)
		}

		// Verify report.md header satisfies drain/internal/policy.RefusedWindow's exact contract.
		rawReport, err := os.ReadFile(filepath.Join(laneDir, "report.md"))
		if err != nil {
			t.Fatal(err)
		}
		headerText, _, ok := strings.Cut(string(rawReport), "\n---\n")
		if !ok || !strings.HasPrefix(headerText, "# run report\n") {
			t.Fatalf("invalid report format:\n%s", rawReport)
		}
		header := map[string]string{}
		for _, line := range strings.Split(headerText, "\n") {
			if k, v, found := strings.Cut(line, ": "); found {
				header[k] = strings.TrimSpace(v)
			}
		}
		if header["rate limit kind"] != "five_hour" || header["status"] != "rejected" || header["fraction"] != "1" || header["reset"] != strconv.FormatInt(resetAt, 10) {
			t.Fatalf("RefusedWindow headers mismatch: %+v", header)
		}
	})

	t.Run("timeout errors record run.model-timeout", func(t *testing.T) {
		srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			http.Error(w, "request body timeout", http.StatusGatewayTimeout)
		}))
		defer srv.Close()

		t.Setenv("TILLR_RUN_BIN", tillrRunBinary(t))
		t.Setenv("TILLR_NATIVE_MOCK_URL", srv.URL)

		tmp := t.TempDir()
		storePath := filepath.Join(tmp, "state.db")
		laneRoot := filepath.Join(tmp, "lanes")
		sessionID := "55555555-5555-4555-8555-555555555555"
		stageNativeLane(t, laneRoot, "lane-1", sessionID)

		ctx := context.Background()
		s, err := store.Open(ctx, storePath, true)
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()

		adapter, _ := filepath.Abs(filepath.Join("..", "backends", "native", "run-native"))
		backendsDir, _ := filepath.Abs(filepath.Join("..", "backends"))
		req := run.Request{
			RunID:      "run-native-to-1",
			ItemID:     "item-native-1",
			LaneID:     "lane-1",
			Backend:    "native",
			Command:    []string{adapter, "--backend", "native", "--model", "mock/model-1"},
			WorkingDir: run.WorktreeDir,
		}

		env, err := run.Exec(ctx, s, req, run.ExecOptions{LaneRoot: laneRoot, BackendsDir: backendsDir, Now: time.Now().UTC()})
		if err != nil {
			t.Fatalf("run.Exec failed: %v", err)
		}
		rec := decodeRunRecord(t, env.Payload)
		if rec.State != run.StateExited || rec.ExitCode == nil || *rec.ExitCode == 0 {
			t.Fatalf("record state=%q exit=%v, want non-zero exit", rec.State, rec.ExitCode)
		}

		var timeoutEvents int
		if err := s.ReadTx(ctx, func(r *store.Reader) error {
			return r.QueryRow(`SELECT count(*) FROM events WHERE object_id = ? AND kind = ?`, req.RunID, run.ModelTimeoutKind).Scan(&timeoutEvents)
		}); err != nil {
			t.Fatal(err)
		}
		if timeoutEvents == 0 {
			t.Fatalf("expected at least one %s event in store, got 0", run.ModelTimeoutKind)
		}
	})
}
