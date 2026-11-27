package extract

import (
	"encoding/json"
	"math"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/run"
)

func mustJSONLines(t *testing.T, records []map[string]any) string {
	t.Helper()
	lines := make([]string, len(records))
	for i, r := range records {
		b, err := json.Marshal(r)
		if err != nil {
			t.Fatal(err)
		}
		lines[i] = string(b)
	}
	return strings.Join(lines, "\n")
}

func TestPiKindsAndClock(t *testing.T) {
	records := []map[string]any{
		{"type": "turn_start", "timestamp": 1000},
		{"type": "message_update", "timestamp": 1200, "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "hi"}},
		{"type": "message_end", "timestamp": 2200, "message": map[string]any{"role": "assistant"}},
		{"type": "turn_end", "timestamp": 2300, "message": map[string]any{"role": "assistant", "model": "m", "usage": map[string]any{"input": 10, "output": 20, "cacheRead": 0}}},
	}
	detail := PiDialect(mustJSONLines(t, records), false).UsageDetails
	if detail == nil || len(detail.Models) == 0 {
		t.Fatalf("expected usage details, got %+v", detail)
	}
	model := detail.Models[0]
	if model.Input == nil || *model.Input != 10 || model.Output == nil || *model.Output != 20 || model.CacheRead == nil || *model.CacheRead != 0 {
		t.Fatalf("unexpected model counters: %+v", model)
	}
	if model.CacheWrite != nil {
		t.Fatalf("expected nil cache_write, got %d", *model.CacheWrite)
	}
	if detail.FirstOutputMS == nil || *detail.FirstOutputMS != 200 {
		t.Fatalf("first_output_ms = %v, want 200", detail.FirstOutputMS)
	}
	if detail.OutputTPS == nil || *detail.OutputTPS != 20 {
		t.Fatalf("output_tokens_per_second = %v, want 20", detail.OutputTPS)
	}
	if len(detail.Turns) == 0 || detail.Turns[0].FirstOutputMS == nil || *detail.Turns[0].FirstOutputMS != 200 {
		t.Fatalf("turn first_output_ms = %+v, want 200", detail.Turns)
	}
}

func TestClaudeDeduplicatesAndResultKinds(t *testing.T) {
	records := []map[string]any{
		{"type": "system", "subtype": "init", "model": "m", "timestamp": "2026-11-17T00:00:00Z"},
		{"type": "assistant", "timestamp": "2026-11-17T00:00:01Z", "message": map[string]any{"id": "a", "model": "m", "usage": map[string]any{"input_tokens": 2, "output_tokens": 4}, "content": []any{map[string]any{"type": "text", "text": "hi"}}}},
		{"type": "assistant", "timestamp": "2026-11-17T00:00:02Z", "message": map[string]any{"id": "a", "model": "m", "usage": map[string]any{"input_tokens": 2, "output_tokens": 4}, "content": []any{}}},
		{"type": "result", "usage": map[string]any{"input_tokens": 2, "output_tokens": 4}},
	}
	detail := ClaudeDialect(mustJSONLines(t, records)).UsageDetails
	if detail == nil || len(detail.Models) == 0 {
		t.Fatalf("expected usage details, got %+v", detail)
	}
	if detail.Models[0].Input == nil || *detail.Models[0].Input != 2 {
		t.Fatalf("input = %v, want 2", detail.Models[0].Input)
	}
	if len(detail.Turns) != 1 {
		t.Fatalf("turns = %d, want 1", len(detail.Turns))
	}
	if detail.Models[0].CacheRead != nil {
		t.Fatalf("expected nil cache_read, got %d", *detail.Models[0].CacheRead)
	}
	if detail.FirstOutputMS != nil {
		t.Fatalf("expected nil first_output_ms, got %v", *detail.FirstOutputMS)
	}
	if detail.OutputTPS != nil {
		t.Fatalf("expected nil output_tokens_per_second, got %v", *detail.OutputTPS)
	}
}

func TestClaudeStreamClocks(t *testing.T) {
	records := []map[string]any{
		{"type": "system", "subtype": "init", "model": "m", "timestamp": "2026-11-17T00:00:00Z"},
		{"type": "stream_event", "timestamp": "2026-11-17T00:00:00.100Z", "event": map[string]any{"type": "message_start"}},
		{"type": "stream_event", "timestamp": "2026-11-17T00:00:00.300Z", "event": map[string]any{"type": "content_block_delta", "delta": map[string]any{"type": "text_delta", "text": "hi"}}},
		{"type": "assistant", "timestamp": "2026-11-17T00:00:01.300Z", "message": map[string]any{"id": "a", "model": "m", "usage": map[string]any{"input_tokens": 2, "output_tokens": 4}}},
		{"type": "stream_event", "timestamp": "2026-11-17T00:00:01.300Z", "event": map[string]any{"type": "message_stop"}},
	}
	detail := ClaudeDialect(mustJSONLines(t, records)).UsageDetails
	if detail == nil {
		t.Fatal("expected usage details")
	}
	if detail.FirstOutputMS == nil || *detail.FirstOutputMS != 300 {
		t.Fatalf("first_output_ms = %v, want 300", detail.FirstOutputMS)
	}
	if len(detail.Turns) == 0 || detail.Turns[0].FirstOutputMS == nil || *detail.Turns[0].FirstOutputMS != 200 {
		t.Fatalf("turn first_output_ms = %+v, want 200", detail.Turns)
	}
	if detail.OutputTPS == nil || *detail.OutputTPS != 4 {
		t.Fatalf("output_tokens_per_second = %v, want 4", detail.OutputTPS)
	}
}

func TestResultOnlyAndMultipleModels(t *testing.T) {
	res := ClaudeDialect(`{"type":"result","usage":{"output_tokens":0}}`).UsageDetails
	if res == nil || len(res.Models) == 0 || res.Models[0].Output == nil || *res.Models[0].Output != 0 {
		t.Fatalf("unexpected result-only details: %+v", res)
	}
	if len(res.Turns) != 0 {
		t.Fatalf("expected 0 turns, got %d", len(res.Turns))
	}
	records := []map[string]any{
		{"type": "turn_end", "message": map[string]any{"model": "a", "usage": map[string]any{"input": 2, "output": 3, "cacheRead": 0, "cacheWrite": 0, "reasoning": 5, "totalTokens": 10}}},
		{"type": "turn_end", "message": map[string]any{"model": "b", "usage": map[string]any{"input": 2, "output": 3, "cacheRead": 0, "cacheWrite": 0, "reasoning": 5, "totalTokens": 10}}},
	}
	detail := PiDialect(mustJSONLines(t, records), false).UsageDetails
	if detail == nil || len(detail.Models) != 2 {
		t.Fatalf("expected 2 models, got %+v", detail)
	}
	if *detail.Models[0].Model != "a" || *detail.Models[1].Model != "b" {
		t.Fatalf("models = %v, %v", detail.Models[0].Model, detail.Models[1].Model)
	}
	if *detail.Models[0].Tokens != 10 || *detail.Models[1].Tokens != 10 {
		t.Fatalf("tokens = %d, %d", *detail.Models[0].Tokens, *detail.Models[1].Tokens)
	}
	if detail.FirstOutputMS != nil {
		t.Fatalf("expected nil first_output_ms, got %v", *detail.FirstOutputMS)
	}
}

func TestMissingFirstDeltaClockStaysAbsent(t *testing.T) {
	records := []map[string]any{
		{"type": "turn_start", "timestamp": 1000},
		{"type": "message_update", "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "first"}},
		{"type": "message_update", "timestamp": 1200, "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "later"}},
		{"type": "message_end", "timestamp": 2200, "message": map[string]any{"role": "assistant"}},
		{"type": "turn_end", "timestamp": 2300, "message": map[string]any{"model": "m", "usage": map[string]any{"output": 20}}},
		{"type": "turn_start", "timestamp": 3000},
		{"type": "message_update", "timestamp": 3200, "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "second turn"}},
		{"type": "message_end", "timestamp": 4200, "message": map[string]any{"role": "assistant"}},
		{"type": "turn_end", "timestamp": 4300, "message": map[string]any{"model": "m", "usage": map[string]any{"output": 20}}},
	}
	detail := PiDialect(mustJSONLines(t, records), false).UsageDetails
	if detail == nil {
		t.Fatal("expected usage details")
	}
	if detail.FirstOutputMS != nil {
		t.Fatalf("expected nil first_output_ms, got %v", *detail.FirstOutputMS)
	}
	if detail.Turns[0].FirstOutputMS != nil {
		t.Fatalf("expected nil turn[0].first_output_ms, got %v", *detail.Turns[0].FirstOutputMS)
	}
	if detail.Turns[0].OutputTPS != nil {
		t.Fatalf("expected nil turn[0].output_tokens_per_second, got %v", *detail.Turns[0].OutputTPS)
	}
}

func TestPiCompletionExcludesTools(t *testing.T) {
	records := []map[string]any{
		{"type": "turn_start", "timestamp": 1000},
		{"type": "message_update", "timestamp": 1200, "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "hi"}},
		{"type": "message_end", "timestamp": 2200, "message": map[string]any{"role": "assistant", "id": "a"}},
		{"type": "turn_end", "timestamp": 62300, "message": map[string]any{"role": "assistant", "id": "a", "model": "m", "usage": map[string]any{"output": 20}}},
	}
	detail := PiDialect(mustJSONLines(t, records), false).UsageDetails
	if detail == nil {
		t.Fatal("expected usage details")
	}
	if detail.OutputTPS == nil || *detail.OutputTPS != 20 {
		t.Fatalf("output_tokens_per_second = %v, want 20", detail.OutputTPS)
	}
	if detail.Turns[0].FinishedMS == nil || *detail.Turns[0].FinishedMS != 2200 {
		t.Fatalf("finished_at_ms = %v, want 2200", detail.Turns[0].FinishedMS)
	}
}

func TestClaudeCompletionSettlesAfterAllRepeatedBlocks(t *testing.T) {
	records := []map[string]any{
		{"type": "stream_event", "timestamp": 1000, "event": map[string]any{"type": "message_start", "message": map[string]any{"id": "a"}}},
		{"type": "stream_event", "timestamp": 1200, "event": map[string]any{"type": "content_block_delta", "delta": map[string]any{"text": "hi"}}},
		{"type": "assistant", "timestamp": 1500, "message": map[string]any{"id": "a", "model": "m", "usage": map[string]any{"output_tokens": 20}}},
		{"type": "assistant", "timestamp": 2200, "message": map[string]any{"id": "a", "model": "m", "usage": map[string]any{"output_tokens": 20}}},
		{"type": "stream_event", "timestamp": 2400, "event": map[string]any{"type": "message_stop"}},
	}
	detail := ClaudeDialect(mustJSONLines(t, records)).UsageDetails
	if detail == nil || detail.OutputTPS == nil || math.Abs(*detail.OutputTPS-20.0/1.2) > 1e-9 {
		t.Fatalf("output_tokens_per_second = %+v, want %v", detail, 20.0/1.2)
	}
	if detail.Turns[0].FinishedMS == nil || *detail.Turns[0].FinishedMS != 2400 {
		t.Fatalf("finished_at_ms = %v, want 2400", detail.Turns[0].FinishedMS)
	}
	partial := ClaudeDialect(mustJSONLines(t, records[:len(records)-1])).UsageDetails
	if partial == nil {
		t.Fatal("expected partial usage details")
	}
	if partial.OutputTPS != nil {
		t.Fatalf("expected nil output_tokens_per_second, got %v", *partial.OutputTPS)
	}
	if partial.Turns[0].FinishedMS != nil {
		t.Fatalf("expected nil finished_at_ms, got %v", *partial.Turns[0].FinishedMS)
	}
}

func TestMissingUsageIsAbsent(t *testing.T) {
	if d := PiDialect("{}", false).UsageDetails; d != nil {
		t.Fatalf("pi: expected nil usage details, got %+v", d)
	}
	if d := ClaudeDialect("{}").UsageDetails; d != nil {
		t.Fatalf("claude: expected nil usage details, got %+v", d)
	}
}

func int64Ptr(v int64) *int64 { return &v }

func TestEstimateModelCostUSDBedrockFamilies(t *testing.T) {
	u := run.ModelUsage{
		Input:      int64Ptr(1_000_000),
		Output:     int64Ptr(200_000),
		CacheRead:  int64Ptr(500_000),
		CacheWrite: int64Ptr(100_000),
	}

	tests := []struct {
		model string
		want  float64
	}{
		// Anthropic Claude: 3.0 + 0.2*15.0 + 0.5*0.30 + 0.1*3.75 = 3.0 + 3.0 + 0.15 + 0.375 = 6.525
		{"anthropic.claude-sonnet-4-20250514-v1:0", 6.525},
		// Anthropic Claude 3.5 Sonnet: same rates
		{"anthropic.claude-3-5-sonnet-20241022-v2:0", 6.525},
		// Amazon Nova Pro: 0.80 + 0.2*3.20 + 0.5*0.20 + 0 = 1.54
		{"amazon.nova-pro-v1:0", 1.54},
		// DeepSeek V3: 0.58 + 0.2*1.68 = 0.916
		{"deepseek.v3-v1:0", 0.916},
		// Meta Llama 3.3 70B: 0.72 + 0.2*0.72 = 0.864
		{"meta.llama3-3-70b-instruct-v1:0", 0.864},
		// Mistral Pixtral Large: 2.0 + 0.2*6.0 = 3.2
		{"mistral.pixtral-large-2502-v1:0", 3.2},
	}

	for _, tc := range tests {
		got, ok := EstimateModelCostUSD(tc.model, u)
		if !ok {
			t.Errorf("EstimateModelCostUSD(%q) ok = false, want true", tc.model)
			continue
		}
		if math.Abs(got-tc.want) > 1e-9 {
			t.Errorf("EstimateModelCostUSD(%q) = %v, want %v", tc.model, got, tc.want)
		}
	}
}

func TestEstimateModelCostUSDNormalization(t *testing.T) {
	u := run.ModelUsage{
		Input:      int64Ptr(1_000_000),
		Output:     int64Ptr(100_000),
		CacheRead:  int64Ptr(500_000),
		CacheWrite: int64Ptr(200_000),
	}
	// 3.0 + 1.5 + 0.15 + 0.75 = 5.40
	const want = 5.40

	variants := []string{
		"anthropic.claude-sonnet-4-20250514-v1:0",
		"amazon-bedrock/anthropic.claude-sonnet-4-20250514-v1:0",
		"bedrock/anthropic.claude-sonnet-4-20250514-v1:0",
		"amazon-bedrock/us.anthropic.claude-sonnet-4-20250514-v1:0",
		"bedrock/eu.anthropic.claude-sonnet-4-20250514-v1:0",
		"ap.anthropic.claude-sonnet-4-20250514-v1:0",
		"global.anthropic.claude-sonnet-4-20250514-v1:0",
		"amazon-bedrock/arn:aws:bedrock:eu-central-1:123456789012:inference-profile/eu.anthropic.claude-sonnet-4-20250514-v1:0",
		"bedrock/arn:aws-us-gov:bedrock:us-gov-west-1:123456789012:foundation-model/anthropic.claude-sonnet-4-20250514-v1:0",
		"arn:aws:bedrock:us-east-1:123456789012:inference-profile/us.anthropic.claude-sonnet-4-20250514-v1:0",
	}

	for _, model := range variants {
		got, ok := EstimateModelCostUSD(model, u)
		if !ok {
			t.Errorf("EstimateModelCostUSD(%q) ok = false, want true", model)
			continue
		}
		if math.Abs(got-want) > 1e-9 {
			t.Errorf("EstimateModelCostUSD(%q) = %v, want %v", model, got, want)
		}
	}
}

func TestEstimateModelCostUSDUnrecognized(t *testing.T) {
	u := run.ModelUsage{Input: int64Ptr(100), Output: int64Ptr(50)}
	for _, model := range []string{
		"",
		"mock/unpriced",
		"amazon-bedrock/nonexistent-model-v1:0",
		"arn:aws:bedrock:us-east-1:123456789012:application-inference-profile/opaque-id",
	} {
		got, ok := EstimateModelCostUSD(model, u)
		if ok || got != 0 {
			t.Errorf("EstimateModelCostUSD(%q) = (%v, %v), want (0, false)", model, got, ok)
		}
	}
}
