package native_test

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/native"
)

func mustEventFrame(t *testing.T, eventType string, payload any) []byte {
	t.Helper()
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal %s: %v", eventType, err)
	}
	frame, err := native.EncodeEventStreamMessage(map[string]string{
		":message-type": "event",
		":event-type":   eventType,
		":content-type": "application/json",
	}, raw)
	if err != nil {
		t.Fatalf("EncodeEventStreamMessage %s: %v", eventType, err)
	}
	return frame
}

func mustExceptionFrame(t *testing.T, excType string, msg string) []byte {
	t.Helper()
	raw, err := json.Marshal(map[string]string{"message": msg})
	if err != nil {
		t.Fatalf("marshal exception %s: %v", excType, err)
	}
	frame, err := native.EncodeEventStreamMessage(map[string]string{
		":message-type":   "exception",
		":exception-type": excType,
		":content-type":   "application/json",
	}, raw)
	if err != nil {
		t.Fatalf("EncodeEventStreamMessage exception %s: %v", excType, err)
	}
	return frame
}

func verifySigV4Authorization(t *testing.T, r *http.Request, body []byte, secret, region, service string) {
	t.Helper()
	auth := r.Header.Get("Authorization")
	const prefix = "AWS4-HMAC-SHA256 "
	if !strings.HasPrefix(auth, prefix) {
		t.Fatalf("Authorization missing AWS4-HMAC-SHA256 prefix: %q", auth)
	}
	parts := strings.Split(strings.TrimPrefix(auth, prefix), ", ")
	kv := make(map[string]string, len(parts))
	for _, p := range parts {
		k, v, ok := strings.Cut(p, "=")
		if ok {
			kv[k] = v
		}
	}
	credParts := strings.Split(kv["Credential"], "/")
	if len(credParts) != 5 {
		t.Fatalf("invalid Credential scope %q", kv["Credential"])
	}
	dateStamp := credParts[1]
	if credParts[2] != region || credParts[3] != service || credParts[4] != "aws4_request" {
		t.Fatalf("unexpected Credential scope %q", kv["Credential"])
	}

	signedHeaders := strings.Split(kv["SignedHeaders"], ";")
	var canonHeaders strings.Builder
	for _, h := range signedHeaders {
		var val string
		if h == "host" {
			val = r.Host
		} else {
			val = strings.Join(r.Header.Values(h), ",")
		}
		canonHeaders.WriteString(h)
		canonHeaders.WriteByte(':')
		canonHeaders.WriteString(strings.Join(strings.Fields(strings.TrimSpace(val)), " "))
		canonHeaders.WriteByte('\n')
	}

	sum := sha256.Sum256(body)
	payloadHash := hex.EncodeToString(sum[:])
	if got := r.Header.Get("X-Amz-Content-Sha256"); got != payloadHash {
		t.Fatalf("X-Amz-Content-Sha256 = %q, want %q", got, payloadHash)
	}

	// Reconstruct canonical URI (double-encoded per AWS SigV4 non-S3 rules).
	rawURI := r.URL.EscapedPath()
	var canonURI strings.Builder
	const hexDigits = "0123456789ABCDEF"
	for i := 0; i < len(rawURI); i++ {
		c := rawURI[i]
		if (c >= 'A' && c <= 'Z') || (c >= 'a' && c <= 'z') || (c >= '0' && c <= '9') ||
			c == '-' || c == '_' || c == '.' || c == '~' || c == '/' {
			canonURI.WriteByte(c)
		} else {
			canonURI.WriteByte('%')
			canonURI.WriteByte(hexDigits[c>>4])
			canonURI.WriteByte(hexDigits[c&0x0F])
		}
	}

	canonReq := strings.Join([]string{
		r.Method,
		canonURI.String(),
		"",
		canonHeaders.String(),
		kv["SignedHeaders"],
		payloadHash,
	}, "\n")
	canonHash := sha256.Sum256([]byte(canonReq))
	amzDate := r.Header.Get("X-Amz-Date")
	scope := strings.Join(credParts[1:], "/")
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + hex.EncodeToString(canonHash[:])

	hmacSum := func(key, data []byte) []byte {
		m := hmac.New(sha256.New, key)
		m.Write(data)
		return m.Sum(nil)
	}
	kDate := hmacSum([]byte("AWS4"+secret), []byte(dateStamp))
	kRegion := hmacSum(kDate, []byte(region))
	kService := hmacSum(kRegion, []byte(service))
	kSigning := hmacSum(kService, []byte("aws4_request"))
	wantSig := hex.EncodeToString(hmacSum(kSigning, []byte(sts)))
	if kv["Signature"] != wantSig {
		t.Fatalf("SigV4 signature mismatch: got %s, want %s\ncanonical request:\n%s", kv["Signature"], wantSig, canonReq)
	}
}

func TestBedrockProviderSigV4AndChunkedEventStream(t *testing.T) {
	const (
		accessKey = "AKIAEXAMPLEKEY123"
		secretKey = "secret-key-example-456"
		sessToken = "session-token-789"
		region    = "us-west-2"
		modelID   = "us.anthropic.claude-sonnet-4-20250514-v1:0"
	)

	var gotRequestBody []byte
	var gotRequestPath string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotRequestPath = r.URL.RawPath
		if gotRequestPath == "" {
			gotRequestPath = r.URL.Path
		}
		var err error
		gotRequestBody, err = io.ReadAll(r.Body)
		if err != nil {
			t.Fatalf("ReadAll: %v", err)
		}
		verifySigV4Authorization(t, r, gotRequestBody, secretKey, region, "bedrock")
		if got := r.Header.Get("X-Amz-Security-Token"); got != sessToken {
			t.Fatalf("X-Amz-Security-Token = %q, want %q", got, sessToken)
		}

		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)

		var stream bytes.Buffer
		stream.Write(mustEventFrame(t, "messageStart", map[string]any{"role": "assistant"}))
		stream.Write(mustEventFrame(t, "contentBlockDelta", map[string]any{
			"contentBlockIndex": 0,
			"delta":             map[string]any{"text": "Hello, "},
		}))
		stream.Write(mustEventFrame(t, "contentBlockDelta", map[string]any{
			"contentBlockIndex": 0,
			"delta":             map[string]any{"text": "Bedrock!"},
		}))
		stream.Write(mustEventFrame(t, "contentBlockStop", map[string]any{"contentBlockIndex": 0}))
		stream.Write(mustEventFrame(t, "messageStop", map[string]any{"stopReason": "end_turn"}))
		stream.Write(mustEventFrame(t, "metadata", map[string]any{
			"usage": map[string]any{
				"inputTokens":           12,
				"outputTokens":          5,
				"cacheReadInputTokens":  3,
				"cacheWriteInputTokens": 2,
				"totalTokens":           22,
			},
		}))

		// Write in small arbitrary 7-byte chunks to exercise frame split handling.
		raw := stream.Bytes()
		flusher, _ := w.(http.Flusher)
		for off := 0; off < len(raw); off += 7 {
			end := off + 7
			if end > len(raw) {
				end = len(raw)
			}
			_, _ = w.Write(raw[off:end])
			if flusher != nil {
				flusher.Flush()
			}
		}
	}))
	defer srv.Close()

	provider, err := native.NewBedrockProvider(native.BedrockConfig{
		Region:     region,
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Auth: native.BedrockAuth{
			AccessKeyID:     accessKey,
			SecretAccessKey: secretKey,
			SessionToken:    sessToken,
		},
	})
	if err != nil {
		t.Fatalf("NewBedrockProvider: %v", err)
	}

	var deltas []native.StreamDelta
	resp, err := provider.StreamTurn(context.Background(), native.TurnRequest{
		Model:  "amazon-bedrock/" + modelID,
		Effort: "medium",
		System: "You are helpful.",
		Messages: []native.Message{
			{Role: native.RoleUser, Content: []native.ContentBlock{{Type: native.BlockText, Text: "hi"}}},
		},
		CacheHints: []int{0},
	}, func(d native.StreamDelta) {
		deltas = append(deltas, d)
	})
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}

	wantPath := "/model/us.anthropic.claude-sonnet-4-20250514-v1%3A0/converse-stream"
	if gotRequestPath != wantPath {
		t.Fatalf("request path = %q, want %q", gotRequestPath, wantPath)
	}

	var decodedReq map[string]any
	if err := json.Unmarshal(gotRequestBody, &decodedReq); err != nil {
		t.Fatalf("decode request body: %v", err)
	}
	sysList, _ := decodedReq["system"].([]any)
	if len(sysList) != 2 {
		t.Fatalf("expected system + cachePoint, got %#v", decodedReq["system"])
	}
	if _, ok := sysList[1].(map[string]any)["cachePoint"]; !ok {
		t.Fatalf("expected cachePoint in system[1], got %#v", sysList[1])
	}
	if _, ok := decodedReq["additionalModelRequestFields"]; !ok {
		t.Fatalf("expected additionalModelRequestFields for claude medium effort")
	}

	if resp.StopReason != native.StopEndTurn {
		t.Fatalf("StopReason = %q, want %q", resp.StopReason, native.StopEndTurn)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "Hello, Bedrock!" {
		t.Fatalf("unexpected content: %+v", resp.Content)
	}
	if len(deltas) != 2 {
		t.Fatalf("expected 2 deltas, got %d", len(deltas))
	}
	if resp.Usage.Input != 12 || resp.Usage.Output != 5 || resp.Usage.CacheRead != 3 || resp.Usage.CacheWrite != 2 || resp.Usage.TotalTokens != 22 {
		t.Fatalf("unexpected usage: %+v", resp.Usage)
	}
}

func TestBedrockProviderBearerAuthAndARNRegion(t *testing.T) {
	const (
		bearerTok = "bedrock-bearer-secret"
		arnModel  = "amazon-bedrock/arn:aws:bedrock:eu-central-1:123456789012:inference-profile/eu.anthropic.claude-sonnet-4-20250514-v1:0"
	)

	var gotAuth string
	var gotRawPath string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth = r.Header.Get("Authorization")
		gotRawPath = r.URL.RawPath
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(mustEventFrame(t, "messageStart", map[string]any{"role": "assistant"}))
		_, _ = w.Write(mustEventFrame(t, "contentBlockDelta", map[string]any{
			"contentBlockIndex": 0,
			"delta":             map[string]any{"text": "ok"},
		}))
		_, _ = w.Write(mustEventFrame(t, "messageStop", map[string]any{"stopReason": "end_turn"}))
	}))
	defer srv.Close()

	env := map[string]string{
		"AWS_BEARER_TOKEN_BEDROCK_EU_PROD": bearerTok,
		"AWS_REGION":                       "us-east-1",
	}
	auth, err := native.ResolveBedrockAuth(arnModel, "eu-prod", func(k string) string { return env[k] })
	if err != nil {
		t.Fatalf("ResolveBedrockAuth: %v", err)
	}
	region := native.ResolveBedrockRegion(arnModel, "eu-prod", func(k string) string { return env[k] })
	if region != "eu-central-1" {
		t.Fatalf("ResolveBedrockRegion = %q, want eu-central-1", region)
	}

	provider, err := native.NewBedrockProvider(native.BedrockConfig{
		Region:     "us-east-1", // overridden by ARN region in StreamTurn
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Auth:       auth,
	})
	if err != nil {
		t.Fatalf("NewBedrockProvider: %v", err)
	}

	resp, err := provider.StreamTurn(context.Background(), native.TurnRequest{
		Model:    arnModel,
		Messages: []native.Message{{Role: native.RoleUser, Content: []native.ContentBlock{{Type: native.BlockText, Text: "ping"}}}},
	}, nil)
	if err != nil {
		t.Fatalf("StreamTurn: %v", err)
	}
	if gotAuth != "Bearer "+bearerTok {
		t.Fatalf("Authorization = %q, want Bearer token", gotAuth)
	}
	if !strings.Contains(gotRawPath, "%3A") || !strings.Contains(gotRawPath, "%2F") {
		t.Fatalf("expected ARN colons and slashes escaped in RawPath, got %q", gotRawPath)
	}
	if len(resp.Content) != 1 || resp.Content[0].Text != "ok" {
		t.Fatalf("unexpected content: %+v", resp.Content)
	}
}

func TestBedrockProviderThinkingRedactedAndToolRoundTrip(t *testing.T) {
	redactedBytes := []byte{0x01, 0x02, 0x03, 0x04}
	var turnCount atomic.Int32
	var secondReqBody []byte

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		turn := turnCount.Add(1)
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)

		if turn == 1 {
			_, _ = w.Write(mustEventFrame(t, "messageStart", map[string]any{"role": "assistant"}))
			_, _ = w.Write(mustEventFrame(t, "contentBlockDelta", map[string]any{
				"contentBlockIndex": 0,
				"delta": map[string]any{
					"reasoningContent": map[string]any{"text": "let me check"},
				},
			}))
			_, _ = w.Write(mustEventFrame(t, "contentBlockDelta", map[string]any{
				"contentBlockIndex": 0,
				"delta": map[string]any{
					"reasoningContent": map[string]any{"signature": "sig-abc"},
				},
			}))
			_, _ = w.Write(mustEventFrame(t, "contentBlockStop", map[string]any{"contentBlockIndex": 0}))
			_, _ = w.Write(mustEventFrame(t, "contentBlockDelta", map[string]any{
				"contentBlockIndex": 1,
				"delta": map[string]any{
					"reasoningContent": map[string]any{"redactedContent": base64.StdEncoding.EncodeToString(redactedBytes)},
				},
			}))
			_, _ = w.Write(mustEventFrame(t, "contentBlockStop", map[string]any{"contentBlockIndex": 1}))
			_, _ = w.Write(mustEventFrame(t, "contentBlockStart", map[string]any{
				"contentBlockIndex": 2,
				"start": map[string]any{
					"toolUse": map[string]any{"toolUseId": "tool-1", "name": "read_file"},
				},
			}))
			_, _ = w.Write(mustEventFrame(t, "contentBlockDelta", map[string]any{
				"contentBlockIndex": 2,
				"delta":             map[string]any{"toolUse": map[string]any{"input": `{"path":`}},
			}))
			_, _ = w.Write(mustEventFrame(t, "contentBlockDelta", map[string]any{
				"contentBlockIndex": 2,
				"delta":             map[string]any{"toolUse": map[string]any{"input": `"README.md"}`}},
			}))
			_, _ = w.Write(mustEventFrame(t, "contentBlockStop", map[string]any{"contentBlockIndex": 2}))
			_, _ = w.Write(mustEventFrame(t, "messageStop", map[string]any{"stopReason": "tool_use"}))
			return
		}

		secondReqBody = body
		_, _ = w.Write(mustEventFrame(t, "messageStart", map[string]any{"role": "assistant"}))
		_, _ = w.Write(mustEventFrame(t, "contentBlockDelta", map[string]any{
			"contentBlockIndex": 0,
			"delta":             map[string]any{"text": "Done reading."},
		}))
		_, _ = w.Write(mustEventFrame(t, "messageStop", map[string]any{"stopReason": "end_turn"}))
	}))
	defer srv.Close()

	provider, err := native.NewBedrockProvider(native.BedrockConfig{
		Region:     "us-east-1",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Auth:       native.BedrockAuth{BearerToken: "tok"},
	})
	if err != nil {
		t.Fatalf("NewBedrockProvider: %v", err)
	}

	dir := t.TempDir()
	briefPath := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(briefPath, []byte("read README"), 0o644); err != nil {
		t.Fatal(err)
	}

	exec := &bedrockTestToolExec{
		defs: []native.ToolDef{{
			Name:        "read_file",
			Description: "read a file",
			InputSchema: json.RawMessage(`{"type":"object","properties":{"path":{"type":"string"}}}`),
		}},
		fn: func(call native.ContentBlock) (string, bool) {
			return "file contents", false
		},
	}

	var out bytes.Buffer
	code := native.Run(context.Background(), native.Config{
		Backend:     "native",
		Model:       "amazon-bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0",
		SessionID:   "11111111-1111-4111-8111-111111111111",
		ArtifactDir: dir,
		BriefPath:   briefPath,
	}, provider, exec, &out)
	if code != native.ExitOK {
		t.Fatalf("Run exited %d, out:\n%s", code, out.String())
	}

	var secondReq map[string]any
	if err := json.Unmarshal(secondReqBody, &secondReq); err != nil {
		t.Fatalf("unmarshal second request: %v", err)
	}
	msgs, _ := secondReq["messages"].([]any)
	if len(msgs) != 3 {
		t.Fatalf("expected 3 messages in turn 2, got %d", len(msgs))
	}
	assistantContent := msgs[1].(map[string]any)["content"].([]any)
	if len(assistantContent) != 3 {
		t.Fatalf("expected 3 assistant content blocks, got %d", len(assistantContent))
	}
	rc0 := assistantContent[0].(map[string]any)["reasoningContent"].(map[string]any)["reasoningText"].(map[string]any)
	if rc0["text"] != "let me check" || rc0["signature"] != "sig-abc" {
		t.Fatalf("thinking signature did not round-trip: %#v", rc0)
	}
	rc1 := assistantContent[1].(map[string]any)["reasoningContent"].(map[string]any)
	if rc1["redactedContent"] != base64.StdEncoding.EncodeToString(redactedBytes) {
		t.Fatalf("redacted thinking did not round-trip: %#v", rc1)
	}
	toolResultMsg := msgs[2].(map[string]any)["content"].([]any)[0].(map[string]any)["toolResult"].(map[string]any)
	if toolResultMsg["status"] != "success" || toolResultMsg["toolUseId"] != "tool-1" {
		t.Fatalf("unexpected toolResult: %#v", toolResultMsg)
	}
}

type bedrockTestToolExec struct {
	defs []native.ToolDef
	fn   func(native.ContentBlock) (string, bool)
}

func (e *bedrockTestToolExec) Definitions() []native.ToolDef { return e.defs }
func (e *bedrockTestToolExec) Execute(_ context.Context, call native.ContentBlock) (string, bool) {
	return e.fn(call)
}

func TestBedrockProviderCRC32Validation(t *testing.T) {
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		frame := mustEventFrame(t, "messageStart", map[string]any{"role": "assistant"})
		// Corrupt the message CRC32 trailer.
		bad := binary.BigEndian.Uint32(frame[len(frame)-4:]) ^ 0xFFFFFFFF
		binary.BigEndian.PutUint32(frame[len(frame)-4:], bad)
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(frame)
	}))
	defer srv.Close()

	provider, err := native.NewBedrockProvider(native.BedrockConfig{
		Region:     "us-east-1",
		BaseURL:    srv.URL,
		HTTPClient: srv.Client(),
		Auth:       native.BedrockAuth{BearerToken: "tok"},
	})
	if err != nil {
		t.Fatal(err)
	}
	_, err = provider.StreamTurn(context.Background(), native.TurnRequest{
		Model:    "amazon-bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []native.Message{{Role: native.RoleUser, Content: []native.ContentBlock{{Type: native.BlockText, Text: "hi"}}}},
	}, nil)
	if err == nil || !strings.Contains(err.Error(), "CRC mismatch") {
		t.Fatalf("expected CRC mismatch error, got %v", err)
	}
}

func TestBedrockProviderRateLimitBurstAndTerminal(t *testing.T) {
	t.Run("HTTP 429 ThrottlingException burst retry succeeds", func(t *testing.T) {
		var attempts atomic.Int32
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if attempts.Add(1) == 1 {
				w.Header().Set("Retry-After", "0.02")
				w.Header().Set("x-amzn-errortype", "ThrottlingException")
				w.WriteHeader(http.StatusTooManyRequests)
				_, _ = w.Write([]byte(`{"message":"Rate exceeded"}`))
				return
			}
			w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(mustEventFrame(t, "messageStart", map[string]any{"role": "assistant"}))
			_, _ = w.Write(mustEventFrame(t, "contentBlockDelta", map[string]any{
				"contentBlockIndex": 0,
				"delta":             map[string]any{"text": "recovered"},
			}))
			_, _ = w.Write(mustEventFrame(t, "messageStop", map[string]any{"stopReason": "end_turn"}))
		}))
		defer srv.Close()

		provider, err := native.NewBedrockProvider(native.BedrockConfig{
			Region:     "us-east-1",
			BaseURL:    srv.URL,
			HTTPClient: srv.Client(),
			Auth:       native.BedrockAuth{BearerToken: "tok"},
		})
		if err != nil {
			t.Fatal(err)
		}

		dir := t.TempDir()
		briefPath := filepath.Join(dir, "brief.md")
		_ = os.WriteFile(briefPath, []byte("hi"), 0o644)
		var out bytes.Buffer
		code := native.Run(context.Background(), native.Config{
			Backend:     "native",
			Model:       "amazon-bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0",
			SessionID:   "22222222-2222-4222-8222-222222222222",
			ArtifactDir: dir,
			BriefPath:   briefPath,
		}, provider, nil, &out)
		if code != native.ExitOK {
			t.Fatalf("Run exited %d, out:\n%s", code, out.String())
		}
		if attempts.Load() != 2 {
			t.Fatalf("expected 2 attempts, got %d", attempts.Load())
		}
	})

	t.Run("ServiceQuotaExceededException emits terminal rate_limit_event", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Retry-After", "120")
			w.Header().Set("x-amzn-errortype", "ServiceQuotaExceededException")
			w.WriteHeader(http.StatusTooManyRequests)
			_, _ = w.Write([]byte(`{"message":"Daily token quota exceeded"}`))
		}))
		defer srv.Close()

		provider, err := native.NewBedrockProvider(native.BedrockConfig{
			Region:     "us-east-1",
			BaseURL:    srv.URL,
			HTTPClient: srv.Client(),
			Auth:       native.BedrockAuth{BearerToken: "tok"},
		})
		if err != nil {
			t.Fatal(err)
		}

		dir := t.TempDir()
		briefPath := filepath.Join(dir, "brief.md")
		_ = os.WriteFile(briefPath, []byte("hi"), 0o644)
		var out bytes.Buffer
		code := native.Run(context.Background(), native.Config{
			Backend:     "native",
			Model:       "amazon-bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0",
			SessionID:   "33333333-3333-4333-8333-333333333333",
			ArtifactDir: dir,
			BriefPath:   briefPath,
		}, provider, nil, &out)
		if code != native.ExitFailed {
			t.Fatalf("Run exited %d, want ExitFailed (1)", code)
		}
		if !strings.Contains(out.String(), `"type":"rate_limit_event"`) || !strings.Contains(out.String(), `"status":"rejected"`) {
			t.Fatalf("expected rate_limit_event in output, got:\n%s", out.String())
		}
	})

	t.Run("eventstream throttlingException returns terminal RateLimitError", func(t *testing.T) {
		srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
			w.WriteHeader(http.StatusOK)
			_, _ = w.Write(mustExceptionFrame(t, "throttlingException", "Stream throttled"))
		}))
		defer srv.Close()

		provider, err := native.NewBedrockProvider(native.BedrockConfig{
			Region:     "us-east-1",
			BaseURL:    srv.URL,
			HTTPClient: srv.Client(),
			Auth:       native.BedrockAuth{BearerToken: "tok"},
		})
		if err != nil {
			t.Fatal(err)
		}
		_, err = provider.StreamTurn(context.Background(), native.TurnRequest{
			Model:    "amazon-bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0",
			Messages: []native.Message{{Role: native.RoleUser, Content: []native.ContentBlock{{Type: native.BlockText, Text: "hi"}}}},
		}, nil)
		var rle *native.RateLimitError
		if !errors.As(err, &rle) || !rle.Terminal || rle.Window == nil || rle.Window.Status != "rejected" {
			t.Fatalf("expected terminal RateLimitError, got %v", err)
		}
	})
}

func TestBedrockProviderStreamIdleTimeout(t *testing.T) {
	done := make(chan struct{})
	defer close(done)

	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/vnd.amazon.eventstream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write(mustEventFrame(t, "messageStart", map[string]any{"role": "assistant"}))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		select {
		case <-done:
		case <-r.Context().Done():
		}
	}))
	defer srv.Close()

	provider, err := native.NewBedrockProvider(native.BedrockConfig{
		Region:            "us-east-1",
		BaseURL:           srv.URL,
		HTTPClient:        srv.Client(),
		StreamIdleTimeout: 50 * time.Millisecond,
		Auth:              native.BedrockAuth{BearerToken: "tok"},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = provider.StreamTurn(context.Background(), native.TurnRequest{
		Model:    "amazon-bedrock/anthropic.claude-3-5-sonnet-20241022-v2:0",
		Messages: []native.Message{{Role: native.RoleUser, Content: []native.ContentBlock{{Type: native.BlockText, Text: "hi"}}}},
	}, nil)
	var te *native.TimeoutError
	if !errors.As(err, &te) {
		t.Fatalf("expected *TimeoutError, got %v", err)
	}
	if te.Message != "request body timeout" {
		t.Fatalf("TimeoutError.Message = %q, want %q", te.Message, "request body timeout")
	}
}

func TestRunCLIMissingBedrockCredentialsExitsRefused(t *testing.T) {
	dir := t.TempDir()
	briefPath := filepath.Join(dir, "brief.md")
	if err := os.WriteFile(briefPath, []byte("hello"), 0o644); err != nil {
		t.Fatal(err)
	}
	sid := "44444444-4444-4444-8444-444444444444"
	if err := os.WriteFile(filepath.Join(dir, "session-id"), []byte(sid+"\n"), 0o600); err != nil {
		t.Fatal(err)
	}

	// Ensure all Bedrock credential env vars and pi auth paths are empty.
	for _, key := range []string{
		"TILLR_NATIVE_MOCK_URL",
		"AWS_ACCESS_KEY_ID",
		"AWS_SECRET_ACCESS_KEY",
		"AWS_SESSION_TOKEN",
		"AWS_BEARER_TOKEN_BEDROCK",
		"AWS_ACCESS_KEY_ID_DEV",
		"AWS_SECRET_ACCESS_KEY_DEV",
		"AWS_BEARER_TOKEN_BEDROCK_DEV",
		"TILLR_DEPLOYED_PI_AGENT_DIR",
		"PI_CODING_AGENT_DIR",
	} {
		t.Setenv(key, "")
	}
	t.Setenv("HOME", dir)

	var stdout, stderr bytes.Buffer
	code := native.RunCLI([]string{
		"--backend", "native",
		"--model", "amazon-bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0",
		"--account", "dev",
		"--brief", briefPath,
	}, &stdout, &stderr)
	if code != native.ExitRefused {
		t.Fatalf("RunCLI exited %d, want ExitRefused (%d); stderr:\n%s", code, native.ExitRefused, stderr.String())
	}
	if !strings.Contains(stderr.String(), "run-native: refused:") {
		t.Fatalf("expected 'run-native: refused:' in stderr, got:\n%s", stderr.String())
	}
	_ = fmt.Sprintf("verified")
}
