package native

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"
)

const (
	defaultBedrockHeaderTimeout     = 60 * time.Second
	defaultBedrockStreamIdleTimeout = 90 * time.Second
	maxBedrockErrorBodyBytes        = 64 * 1024
)

// BedrockConfig configures a BedrockProvider.
type BedrockConfig struct {
	Region            string
	Auth              BedrockAuth
	BaseURL           string
	HTTPClient        *http.Client
	HeaderTimeout     time.Duration
	StreamIdleTimeout time.Duration
	Now               func() time.Time
}

// BedrockProvider implements Provider over AWS Bedrock Runtime ConverseStream
// using pure-Go SigV4 or bearer token auth and binary EventStream decoding.
type BedrockProvider struct {
	region            string
	auth              BedrockAuth
	baseURL           string
	client            *http.Client
	headerTimeout     time.Duration
	streamIdleTimeout time.Duration
	now               func() time.Time
}

// NewBedrockProvider validates cfg and returns a BedrockProvider.
func NewBedrockProvider(cfg BedrockConfig) (*BedrockProvider, error) {
	if !cfg.Auth.IsBearer() && (strings.TrimSpace(cfg.Auth.AccessKeyID) == "" || strings.TrimSpace(cfg.Auth.SecretAccessKey) == "") {
		return nil, errors.New("bedrock: either bearer token or AccessKeyID+SecretAccessKey is required")
	}
	region := strings.TrimSpace(cfg.Region)
	if region == "" {
		region = defaultBedrockRegion
	}
	client := cfg.HTTPClient
	if client == nil {
		client = &http.Client{}
	}
	headerTimeout := cfg.HeaderTimeout
	if headerTimeout <= 0 {
		headerTimeout = defaultBedrockHeaderTimeout
	}
	idleTimeout := cfg.StreamIdleTimeout
	if idleTimeout <= 0 {
		idleTimeout = defaultBedrockStreamIdleTimeout
	}
	nowFn := cfg.Now
	if nowFn == nil {
		nowFn = time.Now
	}
	return &BedrockProvider{
		region:            region,
		auth:              cfg.Auth,
		baseURL:           strings.TrimSpace(cfg.BaseURL),
		client:            client,
		headerTimeout:     headerTimeout,
		streamIdleTimeout: idleTimeout,
		now:               nowFn,
	}, nil
}

// StreamTurn sends req to Bedrock ConverseStream and decodes the binary
// EventStream response into stream deltas and a completed TurnResponse.
func (p *BedrockProvider) StreamTurn(ctx context.Context, req TurnRequest, onDelta func(StreamDelta)) (TurnResponse, error) {
	modelID := strings.TrimSpace(req.Model)
	if parsed, ok := ParseBedrockModelID(modelID); ok {
		modelID = parsed
	}
	if modelID == "" {
		return TurnResponse{}, errors.New("bedrock: model ID is required")
	}

	region := p.region
	if arnRegion, ok := extractBedrockARNRegion(modelID); ok {
		region = arnRegion
	}

	endpointURL, err := p.buildEndpointURL(region, modelID)
	if err != nil {
		return TurnResponse{}, err
	}

	payload, err := buildConverseStreamRequest(modelID, req)
	if err != nil {
		return TurnResponse{}, fmt.Errorf("bedrock: encoding request: %w", err)
	}

	reqCtx, cancelReq := context.WithCancel(ctx)
	defer cancelReq()

	httpReq, err := http.NewRequestWithContext(reqCtx, http.MethodPost, endpointURL.String(), bytes.NewReader(payload))
	if err != nil {
		return TurnResponse{}, err
	}
	httpReq.URL = endpointURL
	httpReq.Host = endpointURL.Host
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "application/vnd.amazon.eventstream")

	if p.auth.IsBearer() {
		httpReq.Header.Set("Authorization", "Bearer "+p.auth.BearerToken)
	} else {
		if err := SignSigV4(httpReq, payload, p.auth.SigV4(region), "bedrock", p.now()); err != nil {
			return TurnResponse{}, err
		}
	}

	started := p.now()
	var headerTimedOut bool
	var timeoutMu sync.Mutex
	headerTimer := time.AfterFunc(p.headerTimeout, func() {
		timeoutMu.Lock()
		headerTimedOut = true
		timeoutMu.Unlock()
		cancelReq()
	})

	resp, err := p.client.Do(httpReq)
	headerTimer.Stop()
	if err != nil {
		timeoutMu.Lock()
		timedOut := headerTimedOut
		timeoutMu.Unlock()
		if timedOut || isTimeoutErr(err) {
			return TurnResponse{}, &TimeoutError{Message: "request body timeout"}
		}
		return TurnResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return TurnResponse{}, p.handleHTTPError(resp)
	}

	return p.readEventStream(reqCtx, cancelReq, resp, modelID, started, onDelta)
}

func (p *BedrockProvider) buildEndpointURL(region, modelID string) (*url.URL, error) {
	path, rawPath := EncodeBedrockModelPath(modelID)
	if p.baseURL == "" {
		return &url.URL{
			Scheme:  "https",
			Host:    fmt.Sprintf("bedrock-runtime.%s.amazonaws.com", region),
			Path:    path,
			RawPath: rawPath,
		}, nil
	}
	base, err := url.Parse(p.baseURL)
	if err != nil {
		return nil, fmt.Errorf("bedrock: invalid base URL %q: %w", p.baseURL, err)
	}
	basePath := strings.TrimRight(base.Path, "/")
	baseRawPath := strings.TrimRight(base.EscapedPath(), "/")
	u := *base
	u.Path = basePath + path
	u.RawPath = baseRawPath + rawPath
	return &u, nil
}

func (p *BedrockProvider) handleHTTPError(resp *http.Response) error {
	raw, _ := io.ReadAll(io.LimitReader(resp.Body, maxBedrockErrorBodyBytes))
	errType := strings.TrimSpace(resp.Header.Get("x-amzn-errortype"))
	var bodyErr struct {
		Message string `json:"message"`
		Type    string `json:"__type"`
		Code    string `json:"code"`
	}
	_ = json.Unmarshal(raw, &bodyErr)
	msg := strings.TrimSpace(bodyErr.Message)
	if msg == "" {
		msg = strings.TrimSpace(string(raw))
	}
	if errType == "" {
		errType = bodyErr.Type
	}
	if errType == "" {
		errType = bodyErr.Code
	}

	retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), p.now())

	if resp.StatusCode == http.StatusTooManyRequests || isThrottlingType(errType) || isQuotaType(errType) {
		return classifyBedrockRateLimit(errType, msg, retryAfter, p.now())
	}
	if msg != "" {
		return fmt.Errorf("bedrock HTTP %d (%s): %s", resp.StatusCode, errType, msg)
	}
	return fmt.Errorf("bedrock HTTP %d", resp.StatusCode)
}

func classifyBedrockRateLimit(errType, msg string, retryAfter time.Duration, now time.Time) *RateLimitError {
	if msg == "" {
		msg = errType
	}
	if msg == "" {
		msg = "rate limit exceeded"
	}

	if isQuotaType(errType) {
		var resetsAt int64
		if retryAfter > 0 {
			resetsAt = now.Add(retryAfter).Unix()
		}
		return &RateLimitError{
			Message:    msg,
			RetryAfter: retryAfter,
			Terminal:   true,
			Window: &RateLimitInfo{
				RateLimitType: "service_quota_exceeded",
				Utilization:   1,
				Status:        "rejected",
				ResetsAt:      resetsAt,
			},
		}
	}

	// ThrottlingException with Retry-After <= 30s is treated as a burst throttle
	// eligible for in-turn retry by loop.go; > 60s or absent is terminal.
	if retryAfter > 0 && retryAfter <= maxBurstRetryAfter {
		return &RateLimitError{
			Message:    msg,
			RetryAfter: retryAfter,
			Terminal:   false,
		}
	}

	var resetsAt int64
	if retryAfter > 0 {
		resetsAt = now.Add(retryAfter).Unix()
	}
	return &RateLimitError{
		Message:    msg,
		RetryAfter: retryAfter,
		Terminal:   true,
		Window: &RateLimitInfo{
			RateLimitType: "throttling",
			Utilization:   1,
			Status:        "rejected",
			ResetsAt:      resetsAt,
		},
	}
}

func isThrottlingType(errType string) bool {
	lower := strings.ToLower(errType)
	return strings.Contains(lower, "throttling")
}

func isQuotaType(errType string) bool {
	lower := strings.ToLower(errType)
	return strings.Contains(lower, "servicequotaexceeded") || strings.Contains(lower, "quotaexceeded")
}

func parseRetryAfter(val string, now time.Time) time.Duration {
	trimmed := strings.TrimSpace(val)
	if trimmed == "" {
		return 0
	}
	if secs, err := strconv.ParseFloat(trimmed, 64); err == nil && secs > 0 {
		return time.Duration(secs * float64(time.Second))
	}
	if t, err := http.ParseTime(trimmed); err == nil {
		if d := t.Sub(now); d > 0 {
			return d
		}
	}
	return 0
}

type idleWatchReader struct {
	r        io.Reader
	timer    *time.Timer
	timeout  time.Duration
	timedOut bool
	mu       sync.Mutex
}

func newIdleWatchReader(r io.Reader, timeout time.Duration, onTimeout func()) *idleWatchReader {
	ir := &idleWatchReader{
		r:       r,
		timeout: timeout,
	}
	ir.timer = time.AfterFunc(timeout, func() {
		ir.mu.Lock()
		ir.timedOut = true
		ir.mu.Unlock()
		onTimeout()
	})
	return ir
}

func (ir *idleWatchReader) Read(p []byte) (int, error) {
	n, err := ir.r.Read(p)
	if n > 0 {
		ir.timer.Reset(ir.timeout)
	}
	return n, err
}

func (ir *idleWatchReader) Stop() {
	ir.timer.Stop()
}

func (ir *idleWatchReader) TimedOut() bool {
	ir.mu.Lock()
	defer ir.mu.Unlock()
	return ir.timedOut
}

type blockAccumulator struct {
	kind      string
	text      strings.Builder
	thinking  strings.Builder
	signature strings.Builder
	redacted  []byte
	toolID    string
	toolName  string
	toolInput strings.Builder
}

func (b *blockAccumulator) toContentBlock() ContentBlock {
	switch b.kind {
	case BlockThinking:
		if len(b.redacted) > 0 {
			return ContentBlock{
				Type:     BlockThinking,
				Redacted: b.redacted,
			}
		}
		return ContentBlock{
			Type:      BlockThinking,
			Thinking:  b.thinking.String(),
			Signature: b.signature.String(),
		}
	case BlockToolUse:
		raw := strings.TrimSpace(b.toolInput.String())
		if raw == "" {
			raw = "{}"
		}
		return ContentBlock{
			Type:  BlockToolUse,
			ID:    b.toolID,
			Name:  b.toolName,
			Input: json.RawMessage(raw),
		}
	default:
		return ContentBlock{
			Type: BlockText,
			Text: b.text.String(),
		}
	}
}

func (p *BedrockProvider) readEventStream(
	ctx context.Context,
	cancelReq context.CancelFunc,
	resp *http.Response,
	modelID string,
	started time.Time,
	onDelta func(StreamDelta),
) (TurnResponse, error) {
	idleReader := newIdleWatchReader(resp.Body, p.streamIdleTimeout, func() {
		cancelReq()
		_ = resp.Body.Close()
	})
	defer idleReader.Stop()

	dec := NewEventStreamDecoder(idleReader)
	blocks := make(map[int]*blockAccumulator)
	var stopReason string
	var usage Usage
	var ttftRecorded bool
	var ttftMS float64

	recordTTFT := func() {
		if !ttftRecorded {
			ttftRecorded = true
			ttftMS = float64(p.now().Sub(started).Microseconds()) / 1000.0
		}
	}

	getBlock := func(idx int, defaultKind string) *blockAccumulator {
		b, ok := blocks[idx]
		if !ok {
			b = &blockAccumulator{kind: defaultKind}
			blocks[idx] = b
		}
		return b
	}

	for {
		msg, err := dec.Next()
		if errors.Is(err, io.EOF) {
			break
		}
		if err != nil {
			if idleReader.TimedOut() || isTimeoutErr(err) || (ctx.Err() != nil && errors.Is(ctx.Err(), context.DeadlineExceeded)) {
				return TurnResponse{}, &TimeoutError{Message: "request body timeout"}
			}
			return TurnResponse{}, fmt.Errorf("bedrock: reading eventstream: %w", err)
		}

		msgType := msg.Headers[":message-type"]
		if msgType == "exception" || msgType == "error" {
			excType := msg.Headers[":exception-type"]
			if excType == "" {
				excType = msg.Headers[":error-code"]
			}
			var payload struct {
				Message string `json:"message"`
			}
			_ = json.Unmarshal(msg.Payload, &payload)
			errMsg := strings.TrimSpace(payload.Message)
			if errMsg == "" {
				errMsg = strings.TrimSpace(string(msg.Payload))
			}
			if isThrottlingType(excType) || isQuotaType(excType) {
				retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"), p.now())
				return TurnResponse{}, classifyBedrockRateLimit(excType, errMsg, retryAfter, p.now())
			}
			return TurnResponse{}, fmt.Errorf("bedrock stream exception (%s): %s", excType, errMsg)
		}

		if msgType != "event" {
			continue
		}

		switch msg.Headers[":event-type"] {
		case "messageStart":
			// No-op beyond acknowledging assistant message start.
		case "contentBlockStart":
			var ev struct {
				ContentBlockIndex int `json:"contentBlockIndex"`
				Start             struct {
					ToolUse *struct {
						ToolUseID string `json:"toolUseId"`
						Name      string `json:"name"`
					} `json:"toolUse"`
				} `json:"start"`
			}
			if err := json.Unmarshal(msg.Payload, &ev); err != nil {
				return TurnResponse{}, fmt.Errorf("bedrock: decoding contentBlockStart: %w", err)
			}
			if ev.Start.ToolUse != nil {
				b := getBlock(ev.ContentBlockIndex, BlockToolUse)
				b.kind = BlockToolUse
				b.toolID = ev.Start.ToolUse.ToolUseID
				b.toolName = ev.Start.ToolUse.Name
			}
		case "contentBlockDelta":
			var ev struct {
				ContentBlockIndex int `json:"contentBlockIndex"`
				Delta             struct {
					Text             *string `json:"text"`
					ReasoningContent *struct {
						Text            *string `json:"text"`
						Signature       *string `json:"signature"`
						RedactedContent []byte  `json:"redactedContent"`
					} `json:"reasoningContent"`
					ToolUse *struct {
						Input string `json:"input"`
					} `json:"toolUse"`
				} `json:"delta"`
			}
			if err := json.Unmarshal(msg.Payload, &ev); err != nil {
				return TurnResponse{}, fmt.Errorf("bedrock: decoding contentBlockDelta: %w", err)
			}
			if ev.Delta.Text != nil {
				b := getBlock(ev.ContentBlockIndex, BlockText)
				b.text.WriteString(*ev.Delta.Text)
				if *ev.Delta.Text != "" {
					recordTTFT()
					if onDelta != nil {
						onDelta(StreamDelta{Type: DeltaText, Delta: *ev.Delta.Text})
					}
				}
			}
			if rc := ev.Delta.ReasoningContent; rc != nil {
				b := getBlock(ev.ContentBlockIndex, BlockThinking)
				b.kind = BlockThinking
				if rc.Text != nil {
					b.thinking.WriteString(*rc.Text)
					if *rc.Text != "" {
						recordTTFT()
						if onDelta != nil {
							onDelta(StreamDelta{Type: DeltaThinking, Delta: *rc.Text})
						}
					}
				}
				if rc.Signature != nil {
					b.signature.WriteString(*rc.Signature)
				}
				if len(rc.RedactedContent) > 0 {
					b.redacted = append([]byte(nil), rc.RedactedContent...)
				}
			}
			if ev.Delta.ToolUse != nil {
				b := getBlock(ev.ContentBlockIndex, BlockToolUse)
				b.kind = BlockToolUse
				b.toolInput.WriteString(ev.Delta.ToolUse.Input)
			}
		case "contentBlockStop":
			// Finalized when assembling ordered blocks below.
		case "messageStop":
			var ev struct {
				StopReason string `json:"stopReason"`
			}
			if err := json.Unmarshal(msg.Payload, &ev); err != nil {
				return TurnResponse{}, fmt.Errorf("bedrock: decoding messageStop: %w", err)
			}
			stopReason = mapBedrockStopReason(ev.StopReason)
		case "metadata":
			var ev struct {
				Usage struct {
					InputTokens           int64 `json:"inputTokens"`
					OutputTokens          int64 `json:"outputTokens"`
					CacheReadInputTokens  int64 `json:"cacheReadInputTokens"`
					CacheWriteInputTokens int64 `json:"cacheWriteInputTokens"`
					TotalTokens           int64 `json:"totalTokens"`
				} `json:"usage"`
			}
			if err := json.Unmarshal(msg.Payload, &ev); err != nil {
				return TurnResponse{}, fmt.Errorf("bedrock: decoding metadata: %w", err)
			}
			usage.Input = ev.Usage.InputTokens
			usage.Output = ev.Usage.OutputTokens
			usage.CacheRead = ev.Usage.CacheReadInputTokens
			usage.CacheWrite = ev.Usage.CacheWriteInputTokens
			usage.TotalTokens = ev.Usage.TotalTokens
		}
	}

	indices := make([]int, 0, len(blocks))
	for idx := range blocks {
		indices = append(indices, idx)
	}
	sort.Ints(indices)

	content := make([]ContentBlock, 0, len(indices))
	for _, idx := range indices {
		content = append(content, blocks[idx].toContentBlock())
	}

	durationMS := float64(p.now().Sub(started).Microseconds()) / 1000.0
	if stopReason == "" {
		stopReason = StopEndTurn
	}

	return TurnResponse{
		Model:      modelID,
		Content:    content,
		StopReason: stopReason,
		Usage:      usage,
		TTFTMS:     ttftMS,
		DurationMS: durationMS,
	}, nil
}

func mapBedrockStopReason(reason string) string {
	switch reason {
	case "end_turn", "stop_sequence":
		return StopEndTurn
	case "tool_use":
		return StopToolUse
	case "max_tokens":
		return StopMaxTokens
	case "":
		return StopEndTurn
	default:
		return reason
	}
}

func isTimeoutErr(err error) bool {
	if errors.Is(err, context.DeadlineExceeded) {
		return true
	}
	var netErr net.Error
	return errors.As(err, &netErr) && netErr.Timeout()
}

func buildConverseStreamRequest(modelID string, req TurnRequest) ([]byte, error) {
	hintSet := make(map[int]struct{}, len(req.CacheHints))
	for _, idx := range req.CacheHints {
		hintSet[idx] = struct{}{}
	}

	body := make(map[string]any)

	if req.System != "" {
		sysBlocks := []any{
			map[string]any{"text": req.System},
		}
		if len(req.CacheHints) > 0 {
			sysBlocks = append(sysBlocks, map[string]any{
				"cachePoint": map[string]any{"type": "default"},
			})
		}
		body["system"] = sysBlocks
	}

	msgs := make([]map[string]any, 0, len(req.Messages))
	for i, m := range req.Messages {
		content := make([]any, 0, len(m.Content)+1)
		for _, b := range m.Content {
			switch b.Type {
			case BlockText:
				content = append(content, map[string]any{
					"text": b.Text,
				})
			case BlockThinking:
				if len(b.Redacted) > 0 {
					content = append(content, map[string]any{
						"reasoningContent": map[string]any{
							"redactedContent": b.Redacted,
						},
					})
				} else {
					rt := map[string]any{
						"text": b.Thinking,
					}
					if b.Signature != "" {
						rt["signature"] = b.Signature
					}
					content = append(content, map[string]any{
						"reasoningContent": map[string]any{
							"reasoningText": rt,
						},
					})
				}
			case BlockToolUse:
				input := b.Input
				if len(bytes.TrimSpace(input)) == 0 {
					input = json.RawMessage(`{}`)
				}
				content = append(content, map[string]any{
					"toolUse": map[string]any{
						"toolUseId": b.ID,
						"name":      b.Name,
						"input":     input,
					},
				})
			case BlockToolResult:
				status := "success"
				if b.IsError {
					status = "error"
				}
				content = append(content, map[string]any{
					"toolResult": map[string]any{
						"toolUseId": b.ToolUseID,
						"content": []any{
							map[string]any{"text": b.Content},
						},
						"status": status,
					},
				})
			}
		}
		if _, hinted := hintSet[i]; hinted && len(content) > 0 {
			content = append(content, map[string]any{
				"cachePoint": map[string]any{"type": "default"},
			})
		}
		msgs = append(msgs, map[string]any{
			"role":    m.Role,
			"content": content,
		})
	}
	body["messages"] = msgs

	if len(req.Tools) > 0 {
		tools := make([]any, 0, len(req.Tools))
		for _, t := range req.Tools {
			schema := t.InputSchema
			if len(bytes.TrimSpace(schema)) == 0 {
				schema = json.RawMessage(`{}`)
			}
			spec := map[string]any{
				"name": t.Name,
				"inputSchema": map[string]any{
					"json": schema,
				},
			}
			if t.Description != "" {
				spec["description"] = t.Description
			}
			tools = append(tools, map[string]any{
				"toolSpec": spec,
			})
		}
		body["toolConfig"] = map[string]any{
			"tools": tools,
		}
	}

	if budget, maxTokens, ok := claudeThinkingBudget(modelID, req.Effort); ok {
		body["inferenceConfig"] = map[string]any{
			"maxTokens": maxTokens,
		}
		body["additionalModelRequestFields"] = map[string]any{
			"thinking": map[string]any{
				"type":          "enabled",
				"budget_tokens": budget,
			},
		}
	}

	return json.Marshal(body)
}

func claudeThinkingBudget(modelID, effort string) (budget int, maxTokens int, ok bool) {
	if !strings.Contains(strings.ToLower(modelID), "claude") {
		return 0, 0, false
	}
	switch strings.ToLower(strings.TrimSpace(effort)) {
	case "minimal":
		return 1024, 9216, true
	case "low":
		return 2048, 10240, true
	case "medium":
		return 8192, 16384, true
	case "high":
		return 16384, 24576, true
	case "xhigh", "max":
		return 32768, 40960, true
	default:
		return 0, 0, false
	}
}
