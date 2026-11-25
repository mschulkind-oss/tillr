package extract

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func runPiCLI(t *testing.T, args []string, stream string) (int, string, string) {
	t.Helper()
	var stdout, stderr bytes.Buffer
	code := RunCLI(append([]string{"--dialect", "pi"}, args...), strings.NewReader(stream), &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func runPiEvents(t *testing.T, events []map[string]any) (int, string, string) {
	t.Helper()
	var b strings.Builder
	for _, ev := range events {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	return runPiCLI(t, []string{"--backend", "pi", "--exit-code", "0"}, b.String())
}

func TestThresholdCompactionMemoryContextTurnKeepsFirstResult(t *testing.T) {
	firstText := "Nine gaps in the design.\n\nOBSTACLE: definition\nCRITERIA: c1\nEVIDENCE: missing specification\nRESULT: blocked\n"
	firstAssistant := map[string]any{
		"id":      "m1",
		"role":    "assistant",
		"content": []any{map[string]any{"type": "text", "text": firstText}},
	}
	memoryMsg := map[string]any{
		"id":         "m-mem",
		"role":       "custom",
		"customType": "memory-context",
		"content":    "<memory>persistent long-term memory context</memory>",
		"display":    false,
	}
	secondText := "waiting for the next instruction"
	secondAssistant := map[string]any{
		"id":      "m2",
		"role":    "assistant",
		"content": []any{map[string]any{"type": "text", "text": secondText}},
	}
	events := []map[string]any{
		{"type": "session", "version": 3, "id": "11111111-1111-7111-8111-111111111111"},
		{"type": "agent_start"},
		{"type": "turn_start"},
		{"type": "message_start", "message": map[string]any{"role": "user", "content": "plan the task"}},
		{"type": "message_end", "message": map[string]any{"role": "user", "content": "plan the task"}},
		{"type": "message_start", "message": map[string]any{"id": "m1", "role": "assistant", "content": []any{}}},
		{"type": "message_update", "message": firstAssistant, "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": firstText}},
		{"type": "message_end", "message": firstAssistant},
		{"type": "turn_end", "message": map[string]any{"id": "m1", "role": "assistant", "content": firstAssistant["content"], "usage": map[string]any{"totalTokens": 5051392, "cost": map[string]any{"total": 1.32}}}},
		{"type": "agent_end", "messages": []any{map[string]any{"role": "user", "content": "plan the task"}, firstAssistant}},
		{"type": "compaction_start", "reason": "threshold"},
		{"type": "compaction_end", "reason": "threshold", "aborted": false, "willRetry": false, "result": map[string]any{"summary": "No specific task has been requested yet. The session only loaded the user's persistent long-term memory context"}},
		{"type": "agent_start"},
		{"type": "turn_start"},
		{"type": "message_start", "message": memoryMsg},
		{"type": "message_end", "message": memoryMsg},
		{"type": "message_start", "message": map[string]any{"id": "m2", "role": "assistant", "content": []any{}}},
		{"type": "message_update", "message": secondAssistant, "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": secondText}},
		{"type": "message_end", "message": secondAssistant},
		{"type": "turn_end", "message": map[string]any{"id": "m2", "role": "assistant", "content": secondAssistant["content"], "usage": map[string]any{"totalTokens": 500, "cost": map[string]any{"total": 0.001}}}},
		{"type": "agent_end", "messages": []any{memoryMsg, secondAssistant}},
		{"type": "agent_settled"},
	}

	code, stdout, stderr := runPiEvents(t, events)
	if code != 3 {
		t.Fatalf("code = %d, want 3\n%s\n%s", code, stderr, stdout)
	}
	for _, want := range []string{"\ncomplete: yes\n", "\nresult: blocked\n", "\ntokens: 5051892\n", "\ncost usd: 1.321000\n"} {
		if !strings.Contains(stdout, want) {
			t.Fatalf("missing %q in:\n%s", want, stdout)
		}
	}
	_, body, ok := strings.Cut(stdout, "\n---\n")
	if !ok {
		t.Fatalf("missing --- separator:\n%s", stdout)
	}
	if !strings.Contains(body, "Nine gaps in the design.") {
		t.Fatalf("missing body text:\n%s", body)
	}
	lines := strings.Split(strings.TrimRight(body, "\n"), "\n")
	if lines[len(lines)-1] != "RESULT: blocked" {
		t.Fatalf("last line = %q, want RESULT: blocked", lines[len(lines)-1])
	}
	if strings.Contains(body, "waiting for the next instruction") {
		t.Fatalf("unexpected second turn text in:\n%s", body)
	}
}

func TestSettledOnlyStreamKeepsFirstResult(t *testing.T) {
	firstAssistant := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "RESULT: done\n"}}}
	memoryMsg := map[string]any{"role": "custom", "customType": "memory-context", "content": "<memory>context</memory>", "display": false}
	secondAssistant := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "waiting for the next instruction"}}}
	events := []map[string]any{
		{"type": "agent_end", "messages": []any{firstAssistant}},
		{"type": "compaction_start", "reason": "threshold"},
		{"type": "compaction_end", "reason": "threshold", "aborted": false, "willRetry": false},
		{"type": "agent_start"},
		{"type": "agent_end", "messages": []any{memoryMsg, secondAssistant}},
	}
	code, stdout, stderr := runPiEvents(t, events)
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s\n%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "\nresult: done\n") {
		t.Fatalf("missing result: done in:\n%s", stdout)
	}
	_, body, _ := strings.Cut(stdout, "\n---\n")
	if strings.TrimSpace(body) != "RESULT: done" {
		t.Fatalf("body = %q, want RESULT: done", body)
	}
}

func TestReasoningOnlyPostCompactionMemoryTurnKeepsFirstResult(t *testing.T) {
	firstAssistant := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "RESULT: done\n"}}}
	memoryMsg := map[string]any{"role": "custom", "customType": "memory-context", "content": "<memory>context</memory>", "display": false}
	secondAssistant := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": "idle"}}}
	events := []map[string]any{
		{"type": "agent_end", "messages": []any{firstAssistant}},
		{"type": "compaction_start", "reason": "threshold"},
		{"type": "compaction_end", "reason": "threshold", "aborted": false, "willRetry": false},
		{"type": "agent_start"},
		{"type": "message_end", "message": memoryMsg},
		{"type": "message_end", "message": secondAssistant},
		{"type": "agent_end", "messages": []any{memoryMsg, secondAssistant}},
	}
	code, stdout, stderr := runPiEvents(t, events)
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s\n%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "\nresult: done\n") {
		t.Fatalf("missing result: done in:\n%s", stdout)
	}
	if strings.Contains(stdout, "last assistant turn had no text or tool call") {
		t.Fatalf("unexpected reasoning-only message in:\n%s", stdout)
	}
}

func TestPostCompactionTurnWithPromptSupersedesFirstResult(t *testing.T) {
	prompts := []map[string]any{
		{"role": "user", "content": "actually check one more thing"},
		{"role": "custom", "customType": "tillr-goal", "content": "The run is not finished", "display": true},
	}
	for _, promptMsg := range prompts {
		firstAssistant := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "RESULT: done\n"}}}
		secondAssistant := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "RESULT: blocked\n"}}}
		events := []map[string]any{
			{"type": "agent_end", "messages": []any{firstAssistant}},
			{"type": "compaction_start", "reason": "threshold"},
			{"type": "compaction_end", "reason": "threshold", "aborted": false, "willRetry": false},
			{"type": "agent_start"},
			{"type": "message_end", "message": promptMsg},
			{"type": "message_end", "message": secondAssistant},
			{"type": "agent_end", "messages": []any{promptMsg, secondAssistant}},
		}
		code, stdout, stderr := runPiEvents(t, events)
		if code != 3 {
			t.Fatalf("code = %d, want 3\n%s\n%s", code, stderr, stdout)
		}
		if !strings.Contains(stdout, "\nresult: blocked\n") {
			t.Fatalf("missing result: blocked in:\n%s", stdout)
		}
	}
}

func TestOverflowCompactionWithRetryReportsRetriedTurn(t *testing.T) {
	firstAssistant := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "RESULT: done\n"}}}
	secondAssistant := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "RESULT: blocked\n"}}}
	events := []map[string]any{
		{"type": "agent_end", "messages": []any{firstAssistant}},
		{"type": "compaction_start", "reason": "overflow"},
		{"type": "compaction_end", "reason": "overflow", "aborted": false, "willRetry": true},
		{"type": "agent_start"},
		{"type": "agent_end", "messages": []any{secondAssistant}},
	}
	code, stdout, stderr := runPiEvents(t, events)
	if code != 3 {
		t.Fatalf("code = %d, want 3\n%s\n%s", code, stderr, stdout)
	}
	if !strings.Contains(stdout, "\nresult: blocked\n") {
		t.Fatalf("missing result: blocked in:\n%s", stdout)
	}
}

func TestCommitOfferSuppliesFooterWithoutSummary(t *testing.T) {
	for _, legacy := range []map[string]any{
		{},
		{"summary": "I ran every check (unverified claim)."},
	} {
		offerPath := filepath.Join(t.TempDir(), "landing-offer.json")
		payload := map[string]any{"call_id": "call-1", "base": "base", "commit": "head"}
		for k, v := range legacy {
			payload[k] = v
		}
		raw, err := json.Marshal(payload)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(offerPath, raw, 0o644); err != nil {
			t.Fatal(err)
		}
		code, stdout, stderr := runPiCLI(t, []string{"--exit-code", "0", "--landing-offer", offerPath}, "")
		if code != 0 {
			t.Fatalf("code = %d, want 0\n%s", code, stderr)
		}
		_, body, _ := strings.Cut(stdout, "\n---\n")
		if strings.TrimSpace(body) != "RESULT: done" {
			t.Fatalf("body = %q, want RESULT: done", body)
		}
	}
}

func TestReasoningOnlyLastTurnIsNotAVerdict(t *testing.T) {
	earlier := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "RESULT: done"}}}
	final := map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "thinking", "thinking": "private"}}}
	endings := [][]map[string]any{
		{{"type": "agent_end", "messages": []any{earlier, final}}},
		{{"type": "message_end", "message": final}},
		{{"type": "message", "message": final}},
	}
	for _, ending := range endings {
		events := append([]map[string]any{
			{"type": "message_update", "message": earlier, "assistantMessageEvent": map[string]any{"type": "text_delta", "delta": "RESULT: done"}},
		}, ending...)
		code, stdout, stderr := runPiEvents(t, events)
		if code != 4 {
			t.Fatalf("code = %d, want 4\n%s", code, stderr)
		}
		if !strings.Contains(stdout, "result: missing") {
			t.Fatalf("missing result: missing in:\n%s", stdout)
		}
		if !strings.Contains(stdout, "last assistant turn had no text or tool call") {
			t.Fatalf("missing reasoning-only note in:\n%s", stdout)
		}
		if strings.Contains(stdout, "RESULT: done") {
			t.Fatalf("unexpected RESULT: done in:\n%s", stdout)
		}
		if strings.Contains(stdout, "private") {
			t.Fatalf("unexpected private thinking in:\n%s", stdout)
		}
	}
}

func TestStaleFooterIsNotALiveVerdict(t *testing.T) {
	events := []map[string]any{
		{"type": "agent_settled"},
		{"type": "agent_end", "messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "RESULT: done"}}}}},
		{"type": "parked", "toolCallId": "call-1"},
		{"type": "agent_end", "messages": []any{}},
	}
	var b strings.Builder
	for _, ev := range events {
		raw, err := json.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		b.Write(raw)
		b.WriteByte('\n')
	}
	streamPath := filepath.Join(t.TempDir(), "stream")
	if err := os.WriteFile(streamPath, []byte(b.String()), 0o644); err != nil {
		t.Fatal(err)
	}
	liveCode, liveOut, liveErr := runPiCLI(t, []string{"--exit-code", "unknown", streamPath}, "")
	if liveCode != 4 {
		t.Fatalf("live code = %d, want 4\n%s", liveCode, liveErr)
	}
	liveLines := strings.Split(strings.TrimRight(liveOut, "\n"), "\n")
	if liveLines[len(liveLines)-1] == "RESULT: done" {
		t.Fatalf("live probe should not end with RESULT: done:\n%s", liveOut)
	}
	finalCode, finalOut, finalErr := runPiCLI(t, []string{"--exit-code", "0", streamPath}, "")
	if finalCode != 0 {
		t.Fatalf("final code = %d, want 0\n%s", finalCode, finalErr)
	}
	finalLines := strings.Split(strings.TrimRight(finalOut, "\n"), "\n")
	if finalLines[len(finalLines)-1] != "RESULT: done" {
		t.Fatalf("final last line = %q, want RESULT: done", finalLines[len(finalLines)-1])
	}
}

var repeatingStreams = map[string]string{
	"repeating-call-request-b13084fe3e.jsonl":          "tools/tillr-drain/drain/pass.go",
	"repeating-call-landing-call-delivery-tests.jsonl": "tools/tillr-drain/drain/landing_call_test.go",
}

func TestRepeatingCallReportNamesEndingAndExitsSix(t *testing.T) {
	for name, wantPath := range repeatingStreams {
		t.Run(name, func(t *testing.T) {
			streamPath := filepath.Join("..", "backends", "pi", "testdata", name)
			code, stdout, stderr := runPiCLI(t, []string{"--backend", "pi", "--exit-code", "0", streamPath}, "")
			if code != 6 {
				t.Fatalf("code = %d, want 6\n%s", code, stderr)
			}
			if !strings.Contains(stderr, "ENDED BY THE HARNESS") {
				t.Fatalf("missing ENDED BY THE HARNESS in stderr:\n%s", stderr)
			}
			if strings.Contains(stderr, "INCOMPLETE") {
				t.Fatalf("unexpected INCOMPLETE in stderr:\n%s", stderr)
			}
			if !strings.Contains(stdout, "\nresult: ended by the harness: repeating-call\n") {
				t.Fatalf("missing repeating-call result in:\n%s", stdout)
			}
			if !strings.Contains(stdout, "complete: no\n") {
				t.Fatalf("missing complete: no in:\n%s", stdout)
			}
			lines := strings.Split(strings.TrimRight(stdout, "\n"), "\n")
			last := lines[len(lines)-1]
			if !strings.Contains(last, "repeated one identical call 6 times in a row: read") {
				t.Fatalf("unexpected last line: %q", last)
			}
			if !strings.Contains(last, wantPath) {
				t.Fatalf("last line %q missing %q", last, wantPath)
			}
		})
	}
}

func TestWithoutLoopDetectedCallsAreIncomplete(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "backends", "pi", "testdata", "repeating-call-request-b13084fe3e.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	var kept []string
	for _, line := range strings.Split(strings.TrimRight(string(raw), "\n"), "\n") {
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatal(err)
		}
		if m["type"] != "loop_detected" {
			kept = append(kept, line)
		}
	}
	streamPath := filepath.Join(t.TempDir(), "stream")
	if err := os.WriteFile(streamPath, []byte(strings.Join(kept, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runPiCLI(t, []string{"--backend", "pi", "--exit-code", "0", streamPath}, "")
	if code != 4 {
		t.Fatalf("code = %d, want 4\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "\nresult: missing\n") {
		t.Fatalf("missing result: missing in:\n%s", stdout)
	}
}

func TestEndingIsHarnessEvenAfterEarlierFooter(t *testing.T) {
	raw, err := os.ReadFile(filepath.Join("..", "backends", "pi", "testdata", "repeating-call-request-b13084fe3e.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(string(raw), "\n"), "\n")
	inserted, err := json.Marshal(map[string]any{
		"type":     "agent_end",
		"messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "RESULT: done"}}}},
	})
	if err != nil {
		t.Fatal(err)
	}
	updated := make([]string, 0, len(lines)+1)
	updated = append(updated, lines[0], string(inserted))
	updated = append(updated, lines[1:]...)
	streamPath := filepath.Join(t.TempDir(), "stream")
	if err := os.WriteFile(streamPath, []byte(strings.Join(updated, "\n")+"\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	code, stdout, stderr := runPiCLI(t, []string{"--backend", "pi", "--exit-code", "0", streamPath}, "")
	if code != 6 {
		t.Fatalf("code = %d, want 6\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "\nresult: ended by the harness: repeating-call\n") {
		t.Fatalf("missing repeating-call result in:\n%s", stdout)
	}
}

func TestRecoveredActionIsNotAnEnding(t *testing.T) {
	events := []map[string]any{
		{"type": "loop_detected", "action": "recovered", "shape": "identical", "toolName": "read", "args": map[string]any{"path": "a.go"}, "streak": 6, "tokensBefore": 120000, "tokensAfter": 45000},
		{"type": "agent_end", "messages": []any{map[string]any{"role": "assistant", "content": []any{map[string]any{"type": "text", "text": "RESULT: done"}}}}},
	}
	code, stdout, stderr := runPiEvents(t, events)
	if code != 0 {
		t.Fatalf("code = %d, want 0\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "\nresult: done\n") {
		t.Fatalf("missing result: done in:\n%s", stdout)
	}
	if strings.Contains(stdout, "repeating-call") {
		t.Fatalf("unexpected repeating-call in:\n%s", stdout)
	}
}

func TestRecoveredThenEndedActionEndsAsRepeatingCall(t *testing.T) {
	events := []map[string]any{
		{"type": "loop_detected", "action": "recovered", "shape": "identical", "toolName": "read", "args": map[string]any{"path": "a.go"}, "streak": 6, "tokensBefore": 120000, "tokensAfter": 45000},
		{"type": "loop_detected", "action": "ended", "shape": "no-tool", "toolName": "", "args": nil, "streak": 5},
	}
	code, stdout, stderr := runPiEvents(t, events)
	if code != 6 {
		t.Fatalf("code = %d, want 6\n%s", code, stderr)
	}
	if !strings.Contains(stdout, "\nresult: ended by the harness: repeating-call\n") {
		t.Fatalf("missing repeating-call result in:\n%s", stdout)
	}
}
