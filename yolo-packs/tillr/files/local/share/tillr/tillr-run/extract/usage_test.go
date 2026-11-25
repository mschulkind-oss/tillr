package extract

import (
	"encoding/json"
	"math"
	"strings"
	"testing"
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
