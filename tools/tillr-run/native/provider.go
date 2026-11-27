// Package native implements tillr's first-party Go agent loop behind the
// tillr-run adapter contract (docs/design/tillr-native-agent-backend.md).
package native

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const (
	RoleUser      = "user"
	RoleAssistant = "assistant"

	BlockText       = "text"
	BlockThinking   = "thinking"
	BlockToolUse    = "tool_use"
	BlockToolResult = "tool_result"

	DeltaText     = "text_delta"
	DeltaThinking = "thinking_delta"

	StopEndTurn   = "end_turn"
	StopToolUse   = "tool_use"
	StopMaxTokens = "max_tokens"
	StopError     = "error"
)

// Provider is the single streaming turn interface that isolates the agent loop,
// session transcript, and tools from provider HTTP wire protocols (D-NAB2).
type Provider interface {
	StreamTurn(ctx context.Context, req TurnRequest, onDelta func(StreamDelta)) (TurnResponse, error)
}

// ContentBlock is a provider-neutral message content block.
type ContentBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text,omitempty"`
	Thinking  string          `json:"thinking,omitempty"`
	Signature string          `json:"signature,omitempty"`
	Redacted  []byte          `json:"redacted,omitempty"`
	ID        string          `json:"id,omitempty"`
	Name      string          `json:"name,omitempty"`
	Input     json.RawMessage `json:"input,omitempty"`
	ToolUseID string          `json:"tool_use_id,omitempty"`
	Content   string          `json:"content,omitempty"`
	IsError   bool            `json:"is_error,omitempty"`
}

// Message is one provider-neutral conversation turn.
type Message struct {
	Role      string         `json:"role"`
	Content   []ContentBlock `json:"content"`
	Timestamp int64          `json:"timestamp,omitempty"`
}

// ToolDef describes one tool offered to the model.
type ToolDef struct {
	Name        string          `json:"name"`
	Description string          `json:"description,omitempty"`
	InputSchema json.RawMessage `json:"input_schema,omitempty"`
}

// TurnRequest carries everything a provider wire adapter needs for one turn.
type TurnRequest struct {
	SessionID  string    `json:"session_id"`
	Model      string    `json:"model"`
	Effort     string    `json:"effort,omitempty"`
	System     string    `json:"system,omitempty"`
	Messages   []Message `json:"messages"`
	Tools      []ToolDef `json:"tools,omitempty"`
	CacheHints []int     `json:"cache_hints,omitempty"`
}

// StreamDelta is one incremental assistant text or thinking chunk.
type StreamDelta struct {
	Type  string `json:"type"`
	Delta string `json:"delta"`
}

// Usage holds normalized token and cost receipts for one assistant turn.
type Usage struct {
	Input       int64    `json:"input,omitempty"`
	Output      int64    `json:"output,omitempty"`
	CacheRead   int64    `json:"cacheRead,omitempty"`
	CacheWrite  int64    `json:"cacheWrite,omitempty"`
	Reasoning   int64    `json:"reasoning,omitempty"`
	TotalTokens int64    `json:"totalTokens,omitempty"`
	CostUSD     *float64 `json:"costUsd,omitempty"`
}

// EffectiveTotal returns TotalTokens if set, otherwise the sum of token counters.
func (u Usage) EffectiveTotal() int64 {
	if u.TotalTokens > 0 {
		return u.TotalTokens
	}
	return u.Input + u.Output + u.CacheRead + u.CacheWrite + u.Reasoning
}

// RateLimitInfo describes a provider quota or rate-limit window.
type RateLimitInfo struct {
	RateLimitType string  `json:"rateLimitType"`
	Utilization   float64 `json:"utilization"`
	Status        string  `json:"status"`
	ResetsAt      int64   `json:"resetsAt"`
}

// TurnResponse is the completed assistant turn returned by Provider.StreamTurn.
type TurnResponse struct {
	ID         string         `json:"id,omitempty"`
	Model      string         `json:"model,omitempty"`
	Content    []ContentBlock `json:"content"`
	StopReason string         `json:"stop_reason,omitempty"`
	Usage      Usage          `json:"usage"`
	RateLimit  *RateLimitInfo `json:"rate_limit,omitempty"`
	TTFTMS     float64        `json:"ttft_ms,omitempty"`
	DurationMS float64        `json:"duration_ms,omitempty"`
}

// RateLimitError reports an HTTP 429 burst throttle or terminal quota refusal.
type RateLimitError struct {
	Message    string
	RetryAfter time.Duration
	Window     *RateLimitInfo
	Terminal   bool
}

func (e *RateLimitError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "rate limit exceeded"
}

// TimeoutError reports an HTTP header or SSE body-idle timeout.
type TimeoutError struct {
	Message string
}

func (e *TimeoutError) Error() string {
	if e.Message != "" {
		return e.Message
	}
	return "request body timeout"
}

// LoopbackProvider speaks a simple JSON/SSE protocol to an httptest.Server
// bound strictly to a loopback address. It refuses any non-loopback host so
// offline tests never reach an external provider over the network.
type LoopbackProvider struct {
	endpoint string
	client   *http.Client
}

// NewLoopbackProvider validates that rawURL targets an http loopback endpoint
// and returns a Provider backed by standard net/http.
func NewLoopbackProvider(rawURL string, client *http.Client) (*LoopbackProvider, error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil {
		return nil, fmt.Errorf("invalid loopback provider url: %w", err)
	}
	if u.Scheme != "http" {
		return nil, fmt.Errorf("loopback provider requires http scheme, got %q", u.Scheme)
	}
	host := u.Hostname()
	if !isLoopbackHost(host) {
		return nil, fmt.Errorf("loopback provider refuses non-loopback host %q", host)
	}
	if client == nil {
		client = &http.Client{}
	}
	return &LoopbackProvider{endpoint: u.String(), client: client}, nil
}

func isLoopbackHost(host string) bool {
	if strings.EqualFold(host, "localhost") {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

type loopbackEvent struct {
	Type     string          `json:"type"`
	Delta    *StreamDelta    `json:"delta,omitempty"`
	Response *TurnResponse   `json:"response,omitempty"`
	Error    string          `json:"error,omitempty"`
	Timeout  bool            `json:"timeout,omitempty"`
	Window   *RateLimitInfo  `json:"rate_limit,omitempty"`
	Terminal bool            `json:"terminal,omitempty"`
	Raw      json.RawMessage `json:"-"`
}

// StreamTurn POSTs TurnRequest as JSON to the loopback endpoint and reads SSE
// ("data: ...") or JSONL events until a "response" or "error" event arrives.
func (p *LoopbackProvider) StreamTurn(ctx context.Context, req TurnRequest, onDelta func(StreamDelta)) (TurnResponse, error) {
	body, err := json.Marshal(req)
	if err != nil {
		return TurnResponse{}, fmt.Errorf("encoding turn request: %w", err)
	}
	httpReq, err := http.NewRequestWithContext(ctx, http.MethodPost, p.endpoint, bytes.NewReader(body))
	if err != nil {
		return TurnResponse{}, err
	}
	httpReq.Header.Set("Content-Type", "application/json")
	httpReq.Header.Set("Accept", "text/event-stream, application/x-ndjson, application/json")

	started := time.Now()
	resp, err := p.client.Do(httpReq)
	if err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return TurnResponse{}, &TimeoutError{Message: "request body timeout"}
		}
		var netErr net.Error
		if errors.As(err, &netErr) && netErr.Timeout() {
			return TurnResponse{}, &TimeoutError{Message: "request body timeout"}
		}
		return TurnResponse{}, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusTooManyRequests {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 64*1024))
		rle := &RateLimitError{Message: strings.TrimSpace(string(raw))}
		if ra := strings.TrimSpace(resp.Header.Get("Retry-After")); ra != "" {
			if secs, err := strconv.ParseFloat(ra, 64); err == nil && secs > 0 {
				rle.RetryAfter = time.Duration(secs * float64(time.Second))
			}
		}
		var payload struct {
			Error    string         `json:"error"`
			Terminal bool           `json:"terminal"`
			Window   *RateLimitInfo `json:"rate_limit"`
		}
		if json.Unmarshal(raw, &payload) == nil {
			if payload.Error != "" {
				rle.Message = payload.Error
			}
			rle.Terminal = payload.Terminal
			rle.Window = payload.Window
		}
		if rle.Window != nil && rle.Window.Status == "rejected" {
			rle.Terminal = true
		}
		return TurnResponse{}, rle
	}
	if resp.StatusCode == http.StatusRequestTimeout || resp.StatusCode == http.StatusGatewayTimeout {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		msg := strings.TrimSpace(string(raw))
		if msg == "" {
			msg = "request body timeout"
		}
		return TurnResponse{}, &TimeoutError{Message: msg}
	}
	if resp.StatusCode != http.StatusOK {
		raw, _ := io.ReadAll(io.LimitReader(resp.Body, 16*1024))
		return TurnResponse{}, fmt.Errorf("provider returned HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(raw)))
	}

	var (
		gotResp    bool
		result     TurnResponse
		firstDelta time.Time
	)
	scanner := bufio.NewScanner(resp.Body)
	scanner.Buffer(make([]byte, 0, 64*1024), 4*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, ":") || strings.HasPrefix(line, "event:") {
			continue
		}
		if strings.HasPrefix(line, "data:") {
			line = strings.TrimSpace(strings.TrimPrefix(line, "data:"))
			if line == "" || line == "[DONE]" {
				continue
			}
		}
		var ev loopbackEvent
		if err := json.Unmarshal([]byte(line), &ev); err != nil {
			return TurnResponse{}, fmt.Errorf("decoding loopback event: %w", err)
		}
		switch ev.Type {
		case "delta":
			if ev.Delta != nil {
				if firstDelta.IsZero() {
					firstDelta = time.Now()
				}
				if onDelta != nil {
					onDelta(*ev.Delta)
				}
			}
		case "response":
			if ev.Response != nil {
				result = *ev.Response
				gotResp = true
			}
		case "error":
			if ev.Timeout {
				msg := ev.Error
				if msg == "" {
					msg = "request body timeout"
				}
				return TurnResponse{}, &TimeoutError{Message: msg}
			}
			if ev.Window != nil {
				return TurnResponse{}, &RateLimitError{
					Message:  ev.Error,
					Window:   ev.Window,
					Terminal: ev.Terminal || ev.Window.Status == "rejected",
				}
			}
			return TurnResponse{}, errors.New(ev.Error)
		}
	}
	if err := scanner.Err(); err != nil {
		if errors.Is(err, context.DeadlineExceeded) {
			return TurnResponse{}, &TimeoutError{Message: "request body timeout"}
		}
		return TurnResponse{}, err
	}
	if !gotResp {
		return TurnResponse{}, errors.New("loopback stream ended without a response event")
	}
	if result.DurationMS == 0 {
		result.DurationMS = float64(time.Since(started).Milliseconds())
	}
	if result.TTFTMS == 0 && !firstDelta.IsZero() {
		result.TTFTMS = float64(firstDelta.Sub(started).Milliseconds())
	}
	return result, nil
}
