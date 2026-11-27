package extract_test

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-run/extract"
	"github.com/mschulkind-oss/tillr-run/run"
)

func TestNativeDialectReportAndUsageDetails(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"session","id":"11111111-1111-4111-8111-111111111111","model":"mock/test-1","timestamp":1764201600000}`,
		`{"type":"agent_start","timestamp":1764201600100}`,
		`{"type":"message_start","timestamp":1764201600200,"message":{"role":"assistant","id":"turn-1"}}`,
		`{"type":"message_update","timestamp":1764201600400,"assistantMessageEvent":{"type":"text_delta","delta":"Working...\n"}}`,
		`{"type":"message_end","timestamp":1764201601400,"message":{"role":"assistant","id":"turn-1","model":"mock/test-1","content":[{"type":"text","text":"Working...\n"},{"type":"toolCall","id":"call_1","name":"noop","input":{}}],"stopReason":"tool_use","usage":{"input":100,"output":25,"cacheRead":10,"cacheWrite":5,"reasoning":0,"totalTokens":140,"cost":{"total":0.0012}}}}`,
		`{"type":"message_start","timestamp":1764201601500,"message":{"role":"assistant","id":"turn-2"}}`,
		`{"type":"message_update","timestamp":1764201601700,"assistantMessageEvent":{"type":"text_delta","delta":"RESULT: done\n"}}`,
		`{"type":"message_end","timestamp":1764201602700,"message":{"role":"assistant","id":"turn-2","model":"mock/test-1","content":[{"type":"text","text":"RESULT: done\n"}],"stopReason":"end_turn","usage":{"input":120,"output":30,"cacheRead":20,"cacheWrite":0,"reasoning":0,"totalTokens":170,"cost":{"total":0.0018}}}}`,
		`{"type":"agent_settled","aborted":false,"timestamp":1764201602800}`,
	}, "\n") + "\n"

	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.md")
	var stdout, stderr bytes.Buffer
	code := extract.RunCLI(
		[]string{
			"--dialect", "native",
			"--backend", "native",
			"--model", "mock/test-1",
			"--route", "mock/test-1",
			"--effort", "medium",
			"--exit-code", "0",
			"--out", reportPath,
		},
		strings.NewReader(stream),
		&stdout,
		&stderr,
	)
	if code != extract.ExitDone {
		t.Fatalf("RunCLI exit = %d, want 0; stderr=%s", code, stderr.String())
	}

	rawReport, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	report := string(rawReport)
	headerText, _, ok := strings.Cut(report, "\n---\n")
	if !ok {
		t.Fatalf("report missing header delimiter:\n%s", report)
	}
	wantKeys := []string{
		"backend: native",
		"model: mock/test-1",
		"route: mock/test-1",
		"effort: medium",
		"session: 11111111-1111-4111-8111-111111111111",
		"exit code: 0",
		"started: ",
		"finished: ",
		"stream lines: 9",
		"events: 9",
		"unparseable lines: 0",
		"tokens: 310",
		"cost usd: 0.003000",
		"rate limit kind: unknown",
		"fraction: unknown",
		"status: unknown",
		"reset: unknown",
		"complete: yes",
		"result: done",
	}
	for _, want := range wantKeys {
		if !strings.Contains(headerText, want) {
			t.Errorf("report header missing %q:\n%s", want, headerText)
		}
	}

	rawUsage, err := os.ReadFile(reportPath + ".usage.json")
	if err != nil {
		t.Fatalf("reading .usage.json: %v", err)
	}
	var details run.UsageDetails
	if err := json.Unmarshal(rawUsage, &details); err != nil {
		t.Fatalf("parsing .usage.json: %v\n%s", err, rawUsage)
	}
	if len(details.Models) != 1 {
		t.Fatalf("models count = %d, want 1", len(details.Models))
	}
	if details.Models[0].Tokens == nil || *details.Models[0].Tokens != 310 {
		t.Fatalf("model tokens = %v, want 310", details.Models[0].Tokens)
	}
	if len(details.Turns) != 2 {
		t.Fatalf("turns count = %d, want 2", len(details.Turns))
	}
}

func TestNativeDialectUnknownCostAndRateLimitHeaders(t *testing.T) {
	stream := strings.Join([]string{
		`{"type":"session","id":"22222222-2222-4222-8222-222222222222","model":"mock/unpriced","timestamp":1764201600000}`,
		`{"type":"message_end","timestamp":1764201600500,"message":{"role":"assistant","id":"turn-1","model":"mock/unpriced","content":[{"type":"text","text":"partial"}],"stopReason":"error","usage":{"input":50,"output":10,"totalTokens":60}}}`,
		`{"type":"rate_limit_event","timestamp":1764201600600,"rate_limit_info":{"rateLimitType":"five_hour","utilization":1,"status":"rejected","resetsAt":1764219600}}`,
	}, "\n") + "\n"

	dir := t.TempDir()
	reportPath := filepath.Join(dir, "report.md")
	var stdout, stderr bytes.Buffer
	code := extract.RunCLI(
		[]string{
			"--dialect", "native",
			"--backend", "native",
			"--model", "mock/unpriced",
			"--exit-code", "1",
			"--out", reportPath,
		},
		strings.NewReader(stream),
		&stdout,
		&stderr,
	)
	if code != extract.ExitIncomplete {
		t.Fatalf("RunCLI exit = %d, want %d; stderr=%s", code, extract.ExitIncomplete, stderr.String())
	}

	rawReport, err := os.ReadFile(reportPath)
	if err != nil {
		t.Fatal(err)
	}
	report := string(rawReport)
	for _, want := range []string{
		"cost usd: unknown",
		"rate limit kind: five_hour",
		"fraction: 1",
		"status: rejected",
		"reset: 1764219600",
	} {
		if !strings.Contains(report, want) {
			t.Errorf("report missing %q:\n%s", want, report)
		}
	}
}
