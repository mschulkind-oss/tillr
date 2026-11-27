package native

import (
	"context"
	"encoding/json"
	"errors"
	"flag"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/buildstamp"
)

const (
	ExitOK      = 0
	ExitFailed  = 1
	ExitRefused = 2

	defaultMaxTurns         = 100
	maxBurstRateLimitRetry  = 3
	maxBurstRetryAfter      = 30 * time.Second
	maxModelTimeoutRetry    = 2
	defaultRetryBackoffBase = 250 * time.Millisecond
)

// ToolExecutor runs tool calls requested by the model. Slice 1 ships no
// built-in file or shell tools (Slices 2-4 add them); tests inject mock
// executors to exercise tool-call turns, turn-gap pauses, and resume repair.
type ToolExecutor interface {
	Definitions() []ToolDef
	Execute(ctx context.Context, call ContentBlock) (string, bool)
}

// Config holds the inputs for one native-agent execution.
type Config struct {
	Backend           string
	Model             string
	Effort            string
	Account           string
	SessionID         string
	ArtifactDir       string
	BriefPath         string
	ResumePromptPath  string
	SessionMode       string
	PauseRequestPath  string
	HarnessRecordPath string
	HarnessVersion    string
	MaxTurns          int
	RetryBackoffBase  time.Duration
}

func writeHarnessRecord(path, version string) error {
	if path == "" {
		return nil
	}
	v := strings.TrimSpace(version)
	if v == "" {
		v = strings.TrimSpace(buildstamp.Read().String())
	}
	if v == "" {
		v = "dev"
	}
	return os.WriteFile(path, []byte("tillr-native/"+v+"\n"), 0o644)
}

func pauseRequested(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

type streamEmitter struct {
	w io.Writer
}

func (e *streamEmitter) emit(v any) error {
	raw, err := json.Marshal(v)
	if err != nil {
		return err
	}
	raw = append(raw, '\n')
	_, err = e.w.Write(raw)
	return err
}

func nowMillis() int64 {
	return time.Now().UTC().UnixMilli()
}

func sleepOrStop(ctx context.Context, d time.Duration, pausePath string) bool {
	if d <= 0 {
		return !pauseRequested(pausePath) && ctx.Err() == nil
	}
	deadline := time.NewTimer(d)
	defer deadline.Stop()
	ticker := time.NewTicker(10 * time.Millisecond)
	defer ticker.Stop()
	for {
		if pauseRequested(pausePath) {
			return false
		}
		select {
		case <-ctx.Done():
			return false
		case <-deadline.C:
			return !pauseRequested(pausePath)
		case <-ticker.C:
		}
	}
}

func usagePayload(u Usage) map[string]any {
	m := map[string]any{
		"input":       u.Input,
		"output":      u.Output,
		"cacheRead":   u.CacheRead,
		"cacheWrite":  u.CacheWrite,
		"reasoning":   u.Reasoning,
		"totalTokens": u.EffectiveTotal(),
	}
	if u.CostUSD != nil {
		m["cost"] = map[string]any{"total": *u.CostUSD}
	}
	return m
}

func assistantContentPayload(blocks []ContentBlock) []map[string]any {
	out := make([]map[string]any, 0, len(blocks))
	for _, b := range blocks {
		switch b.Type {
		case BlockText:
			out = append(out, map[string]any{"type": "text", "text": b.Text})
		case BlockThinking:
			m := map[string]any{"type": "thinking", "thinking": b.Thinking}
			if b.Signature != "" {
				m["signature"] = b.Signature
			}
			out = append(out, m)
		case BlockToolUse:
			out = append(out, map[string]any{
				"type":  "toolCall",
				"id":    b.ID,
				"name":  b.Name,
				"input": b.Input,
			})
		}
	}
	return out
}

// Run executes the single-item turn, session, and pause loop against provider,
// writing JSONL stream events to out and returning the process exit code.
func Run(ctx context.Context, cfg Config, provider Provider, executor ToolExecutor, out io.Writer) int {
	if provider == nil {
		return ExitRefused
	}
	if err := writeHarnessRecord(cfg.HarnessRecordPath, cfg.HarnessVersion); err != nil {
		return ExitFailed
	}

	mode := cfg.SessionMode
	if mode == "" {
		mode = "new"
	}

	var (
		sess *Session
		err  error
	)
	switch mode {
	case "new":
		sess, err = OpenNewSession(cfg.ArtifactDir, SessionHeader{
			SessionID: cfg.SessionID,
			Backend:   cfg.Backend,
			Model:     cfg.Model,
			Effort:    cfg.Effort,
		})
		if err != nil {
			return ExitRefused
		}
		promptPath := cfg.ResumePromptPath
		if promptPath == "" {
			promptPath = cfg.BriefPath
		}
		promptBytes, err := os.ReadFile(promptPath)
		if err != nil {
			return ExitRefused
		}
		if err := sess.AppendMessage(Message{
			Role:    RoleUser,
			Content: []ContentBlock{{Type: BlockText, Text: string(promptBytes)}},
		}); err != nil {
			return ExitFailed
		}
	case "resume":
		sess, err = LoadSession(cfg.ArtifactDir, cfg.SessionID)
		if err != nil {
			return ExitRefused
		}
		if _, err := sess.RepairInterruptedTools(); err != nil {
			return ExitFailed
		}
		if cfg.ResumePromptPath != "" {
			promptBytes, err := os.ReadFile(cfg.ResumePromptPath)
			if err != nil {
				return ExitRefused
			}
			if err := sess.AppendMessage(Message{
				Role:    RoleUser,
				Content: []ContentBlock{{Type: BlockText, Text: string(promptBytes)}},
			}); err != nil {
				return ExitFailed
			}
		}
	default:
		return ExitRefused
	}

	emit := &streamEmitter{w: out}
	_ = emit.emit(map[string]any{
		"type":      "session",
		"id":        cfg.SessionID,
		"model":     cfg.Model,
		"timestamp": nowMillis(),
	})
	_ = emit.emit(map[string]any{
		"type":      "agent_start",
		"timestamp": nowMillis(),
	})

	maxTurns := cfg.MaxTurns
	if maxTurns <= 0 {
		maxTurns = defaultMaxTurns
	}
	backoffBase := cfg.RetryBackoffBase
	if backoffBase <= 0 {
		backoffBase = defaultRetryBackoffBase
	}

	var toolDefs []ToolDef
	if executor != nil {
		toolDefs = executor.Definitions()
	}

	for turn := 1; turn <= maxTurns; turn++ {
		if pauseRequested(cfg.PauseRequestPath) {
			return ExitOK
		}
		if ctx.Err() != nil {
			return ExitFailed
		}

		resp, exitCode, paused := runProviderTurn(ctx, cfg, sess, toolDefs, provider, emit, turn, backoffBase)
		if paused {
			return ExitOK
		}
		if exitCode != ExitOK {
			return exitCode
		}

		if err := sess.AppendMessage(Message{
			Role:    RoleAssistant,
			Content: resp.Content,
		}); err != nil {
			return ExitFailed
		}

		modelName := resp.Model
		if modelName == "" {
			modelName = cfg.Model
		}
		msgID := resp.ID
		if msgID == "" {
			msgID = fmt.Sprintf("turn-%d", turn)
		}
		stopReason := resp.StopReason
		if stopReason == "" {
			stopReason = StopEndTurn
		}
		endEvent := map[string]any{
			"type":      "message_end",
			"timestamp": nowMillis(),
			"message": map[string]any{
				"role":       RoleAssistant,
				"id":         msgID,
				"model":      modelName,
				"content":    assistantContentPayload(resp.Content),
				"stopReason": stopReason,
				"usage":      usagePayload(resp.Usage),
			},
		}
		if resp.TTFTMS > 0 {
			endEvent["ttftMs"] = resp.TTFTMS
		}
		if resp.DurationMS > 0 {
			endEvent["durationMs"] = resp.DurationMS
		}
		_ = emit.emit(endEvent)

		var toolCalls []ContentBlock
		for _, blk := range resp.Content {
			if blk.Type == BlockToolUse {
				toolCalls = append(toolCalls, blk)
			}
		}
		if len(toolCalls) == 0 {
			_ = emit.emit(map[string]any{
				"type":      "agent_settled",
				"aborted":   false,
				"timestamp": nowMillis(),
			})
			return ExitOK
		}

		results := make([]ContentBlock, 0, len(toolCalls))
		for _, call := range toolCalls {
			_ = emit.emit(map[string]any{
				"type":       "tool_execution_start",
				"toolCallId": call.ID,
				"toolName":   call.Name,
				"timestamp":  nowMillis(),
			})
			var (
				resText string
				isErr   bool
			)
			if executor == nil {
				resText = fmt.Sprintf("unknown tool %q", call.Name)
				isErr = true
			} else {
				resText, isErr = executor.Execute(ctx, call)
			}
			_ = emit.emit(map[string]any{
				"type":       "tool_execution_end",
				"toolCallId": call.ID,
				"toolName":   call.Name,
				"isError":    isErr,
				"timestamp":  nowMillis(),
			})
			results = append(results, ContentBlock{
				Type:      BlockToolResult,
				ToolUseID: call.ID,
				Content:   resText,
				IsError:   isErr,
			})
		}

		if err := sess.AppendMessage(Message{
			Role:    RoleUser,
			Content: results,
		}); err != nil {
			return ExitFailed
		}

		if pauseRequested(cfg.PauseRequestPath) {
			return ExitOK
		}
	}

	return ExitFailed
}

func runProviderTurn(
	ctx context.Context,
	cfg Config,
	sess *Session,
	toolDefs []ToolDef,
	provider Provider,
	emit *streamEmitter,
	turn int,
	backoffBase time.Duration,
) (TurnResponse, int, bool) {
	burstRetries := 0
	timeoutRetries := 0
	for {
		if pauseRequested(cfg.PauseRequestPath) {
			return TurnResponse{}, ExitOK, true
		}
		msgID := fmt.Sprintf("turn-%d", turn)
		_ = emit.emit(map[string]any{
			"type":      "message_start",
			"timestamp": nowMillis(),
			"message": map[string]any{
				"role": RoleAssistant,
				"id":   msgID,
			},
		})

		req := TurnRequest{
			SessionID: cfg.SessionID,
			Model:     cfg.Model,
			Effort:    cfg.Effort,
			Messages:  append([]Message(nil), sess.Messages...),
			Tools:     toolDefs,
		}
		resp, err := provider.StreamTurn(ctx, req, func(d StreamDelta) {
			if d.Delta == "" {
				return
			}
			_ = emit.emit(map[string]any{
				"type":      "message_update",
				"timestamp": nowMillis(),
				"assistantMessageEvent": map[string]any{
					"type":  d.Type,
					"delta": d.Delta,
				},
			})
		})
		if err == nil {
			return resp, ExitOK, false
		}

		var rle *RateLimitError
		if errors.As(err, &rle) {
			if !rle.Terminal && rle.RetryAfter > 0 && rle.RetryAfter <= maxBurstRetryAfter && burstRetries < maxBurstRateLimitRetry {
				burstRetries++
				if !sleepOrStop(ctx, rle.RetryAfter, cfg.PauseRequestPath) {
					if pauseRequested(cfg.PauseRequestPath) {
						return TurnResponse{}, ExitOK, true
					}
					return TurnResponse{}, ExitFailed, false
				}
				continue
			}
			info := rle.Window
			if info == nil {
				info = &RateLimitInfo{
					RateLimitType: "quota",
					Utilization:   1,
					Status:        "rejected",
				}
			}
			if info.Status == "" {
				info.Status = "rejected"
			}
			if info.Utilization == 0 && info.Status == "rejected" {
				info.Utilization = 1
			}
			_ = emit.emit(map[string]any{
				"type":            "rate_limit_event",
				"timestamp":       nowMillis(),
				"rate_limit_info": info,
			})
			return TurnResponse{}, ExitFailed, false
		}

		var te *TimeoutError
		if errors.As(err, &te) {
			_ = emit.emit(map[string]any{
				"type":      "message_end",
				"timestamp": nowMillis(),
				"message": map[string]any{
					"role":         RoleAssistant,
					"id":           msgID,
					"model":        cfg.Model,
					"stopReason":   StopError,
					"errorMessage": te.Error(),
				},
			})
			if timeoutRetries < maxModelTimeoutRetry {
				timeoutRetries++
				if !sleepOrStop(ctx, backoffBase*time.Duration(timeoutRetries), cfg.PauseRequestPath) {
					if pauseRequested(cfg.PauseRequestPath) {
						return TurnResponse{}, ExitOK, true
					}
					return TurnResponse{}, ExitFailed, false
				}
				continue
			}
			return TurnResponse{}, ExitFailed, false
		}

		_ = emit.emit(map[string]any{
			"type":      "message_end",
			"timestamp": nowMillis(),
			"message": map[string]any{
				"role":         RoleAssistant,
				"id":           msgID,
				"model":        cfg.Model,
				"stopReason":   StopError,
				"errorMessage": err.Error(),
			},
		})
		return TurnResponse{}, ExitFailed, false
	}
}

// RunCLI implements the `tillr-run native-agent` face invoked by backends/native/run-native.
func RunCLI(args []string, stdout, stderr io.Writer) int {
	const prog = "native-agent"
	fs := flag.NewFlagSet("tillr-run "+prog, flag.ContinueOnError)
	fs.SetOutput(stderr)
	backend := fs.String("backend", "", "the backend name from staging")
	model := fs.String("model", "", "the model id from staging")
	effort := fs.String("effort", "", "the reasoning effort tier")
	account := fs.String("account", "", "optional provider account slug")
	brief := fs.String("brief", "../brief.md", "path to the staged brief")
	target := fs.String("target", "", "target working directory")
	sessionID := fs.String("session-id", "", "stager-minted session UUID")
	timeoutSec := fs.String("timeout", "", "optional run wall in seconds (enforced by tillr-run exec)")

	if err := fs.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return ExitOK
		}
		return ExitRefused
	}
	if fs.NArg() > 0 {
		fmt.Fprintf(stderr, "%s: unexpected argument %q\n", prog, fs.Arg(0))
		return ExitRefused
	}
	if *backend == "" || *model == "" {
		fmt.Fprintf(stderr, "%s: --backend and --model are required\n", prog)
		return ExitRefused
	}
	if *timeoutSec != "" {
		n, err := strconv.Atoi(*timeoutSec)
		if err != nil || n < 0 {
			fmt.Fprintf(stderr, "%s: --timeout must be a non-negative integer, got %q\n", prog, *timeoutSec)
			return ExitRefused
		}
	}
	workDir := strings.TrimSpace(*target)
	if workDir == "" {
		if wd, err := os.Getwd(); err == nil {
			workDir = wd
		}
	}
	if workDir != "" {
		if abs, err := filepath.Abs(workDir); err == nil {
			workDir = abs
		}
	}

	briefAbs, err := filepath.Abs(*brief)
	if err != nil {
		fmt.Fprintf(stderr, "%s: resolving brief path: %v\n", prog, err)
		return ExitRefused
	}
	artifactDir := filepath.Dir(briefAbs)

	sid := strings.TrimSpace(*sessionID)
	if sid == "" {
		if raw, err := os.ReadFile(filepath.Join(artifactDir, "session-id")); err == nil {
			sid = strings.TrimSpace(string(raw))
		}
	}
	if !ValidateUUID(sid) {
		fmt.Fprintf(stderr, "%s: refused: missing or invalid session-id %q\n", prog, sid)
		return ExitRefused
	}

	var provider Provider
	mockURL := strings.TrimSpace(os.Getenv("TILLR_NATIVE_MOCK_URL"))
	if mockURL != "" {
		provider, err = NewLoopbackProvider(mockURL, nil)
		if err != nil {
			fmt.Fprintf(stderr, "%s: refused: %v\n", prog, err)
			return ExitRefused
		}
	} else if modelID, ok := ParseBedrockModelID(*model); ok {
		auth, err := ResolveBedrockAuth(*model, *account, os.Getenv)
		if err != nil {
			fmt.Fprintf(stderr, "run-native: refused: %v\n", err)
			return ExitRefused
		}
		region := ResolveBedrockRegion(modelID, *account, os.Getenv)
		provider, err = NewBedrockProvider(BedrockConfig{
			Region: region,
			Auth:   auth,
		})
		if err != nil {
			fmt.Fprintf(stderr, "run-native: refused: %v\n", err)
			return ExitRefused
		}
	} else {
		fmt.Fprintf(stderr, "%s: refused: no live provider wire adapter configured for model %q\n", prog, *model)
		return ExitRefused
	}

	seat := strings.TrimSpace(os.Getenv("TILLR_SEAT"))
	access := strings.TrimSpace(os.Getenv("TILLR_ACCESS"))
	readOnly := access == "read_only" || strings.TrimSpace(os.Getenv("TILLR_READ_ONLY_GROUP")) != "" || seat == "triage"
	toolset := NewToolset(FencePolicy{
		WorkDir:      workDir,
		ArtifactDir:  artifactDir,
		ArtifactsDir: strings.TrimSpace(os.Getenv("TILLR_OUTPUTS_DIR")),
		DurableDir:   strings.TrimSpace(os.Getenv("YOLO_DURABLE_DIR")),
		ProgressFile: strings.TrimSpace(os.Getenv("TILLR_PROGRESS_FILE")),
		ReadOnly:     readOnly,
		PlanQuickWay: readOnly && seat == "plan",
	})

	cfg := Config{
		Backend:           *backend,
		Model:             *model,
		Effort:            *effort,
		Account:           *account,
		SessionID:         sid,
		ArtifactDir:       artifactDir,
		BriefPath:         briefAbs,
		ResumePromptPath:  os.Getenv("TILLR_RESUME_PROMPT_FILE"),
		SessionMode:       os.Getenv("TILLR_SESSION_MODE"),
		PauseRequestPath:  os.Getenv("TILLR_PAUSE_REQUEST"),
		HarnessRecordPath: os.Getenv("TILLR_HARNESS_RECORD_FILE"),
	}
	return Run(context.Background(), cfg, provider, toolset, stdout)
}
