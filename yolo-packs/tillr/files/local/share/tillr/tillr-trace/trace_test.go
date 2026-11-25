package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io/fs"
	"math"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"slices"
	"strconv"
	"strings"
	"testing"

	_ "modernc.org/sqlite"
)

const storeDDL = `
CREATE TABLE lanes (lane_id TEXT PRIMARY KEY, state TEXT NOT NULL, run_id TEXT,
  leased_until INTEGER);
CREATE TABLE runs (run_id TEXT PRIMARY KEY, item_id TEXT, lane_id TEXT NOT NULL,
  backend TEXT NOT NULL, state TEXT NOT NULL, exit_code INTEGER,
  started_at INTEGER NOT NULL, finished_at INTEGER NOT NULL,
  artifact_refs TEXT NOT NULL DEFAULT '[]', event_seq INTEGER NOT NULL DEFAULT 0);
CREATE TABLE items (item_id TEXT PRIMARY KEY, title TEXT NOT NULL, body_ref TEXT,
  state TEXT NOT NULL, design_doc TEXT, updated_at INTEGER NOT NULL,
  event_seq INTEGER NOT NULL DEFAULT 0, allowed_paths TEXT NOT NULL DEFAULT '');
CREATE TABLE candidates (candidate_id TEXT PRIMARY KEY, item_id TEXT NOT NULL,
  base_ref TEXT NOT NULL, base_sha TEXT NOT NULL, head_sha TEXT NOT NULL,
  tree_sha TEXT NOT NULL, state TEXT NOT NULL, enqueued_at INTEGER NOT NULL,
  event_seq INTEGER NOT NULL DEFAULT 0);
CREATE TABLE decisions (decision_id TEXT PRIMARY KEY, text TEXT NOT NULL,
  scope_kind TEXT NOT NULL, scope_ref TEXT NOT NULL DEFAULT '',
  decided_by TEXT NOT NULL, decided_at INTEGER NOT NULL,
  status TEXT NOT NULL DEFAULT 'active', supersedes TEXT,
  session_id TEXT NOT NULL DEFAULT '');
CREATE TABLE dispatches (decision_id TEXT PRIMARY KEY, item_id TEXT NOT NULL,
  lane_id TEXT, run_id TEXT, action TEXT NOT NULL, reason TEXT NOT NULL,
  dispatched_at INTEGER NOT NULL);
`

var (
	measuredTotals = struct {
		turns     int
		tokensIn  int64
		tokensOut int64
		cacheRead int64
		reasoning int64
		costUSD   float64
		toolCalls int
	}{
		turns:     7,
		tokensIn:  36196,
		tokensOut: 1917,
		cacheRead: 216192,
		reasoning: 438,
		costUSD:   0.004746410,
		toolCalls: 6,
	}

	testBinPath         string
	testDefaultTraceDir string
	testRepoRoot        string
)

func TestMain(m *testing.M) {
	os.Exit(runTests(m))
}

// runTests returns before TestMain exits, so cleanup also runs after setup or test failure.
func runTests(m *testing.M) int {
	wd, err := os.Getwd()
	if err != nil {
		return 1
	}
	testRepoRoot = filepath.Clean(filepath.Join(wd, "..", ".."))

	root := os.Getenv("YOLO_DURABLE_DIR")
	if root == "" {
		root = filepath.Join("..", "..", ".yolo", "durable")
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		return 1
	}
	tmpDir, err := os.MkdirTemp(root, "tillr-trace-bin-")
	if err != nil {
		return 1
	}
	defer os.RemoveAll(tmpDir)

	testBinPath = filepath.Join(tmpDir, "tillr-trace")
	build := exec.Command("go", "build", "-o", testBinPath, ".")
	build.Dir = wd
	build.Env = append(os.Environ(), "CGO_ENABLED=0")
	if out, err := build.CombinedOutput(); err != nil {
		os.Stderr.Write(out)
		return 1
	}

	testDefaultTraceDir = filepath.Join(tmpDir, "default-trace-root")
	_ = os.Setenv("TILLR_TRACE_ROOT", testDefaultTraceDir)

	return m.Run()
}

func turnEnd(index int, text string, tsMS int64) map[string]any {
	provider := "openrouter"
	model := "deepseek/deepseek-v4.1-flash-1017:floor"
	thinking := "high"
	return map[string]any{
		"type": "turn_end",
		"message": map[string]any{
			"role":     "assistant",
			"api":      "openai-completions",
			"provider": provider,
			"model":    model,
			"usage": map[string]any{
				"input":       100 + index,
				"output":      20,
				"cacheRead":   0,
				"cacheWrite":  0,
				"reasoning":   4,
				"totalTokens": 124,
				"cost": map[string]any{
					"input":             0.0001,
					"output":            0.00002,
					"cacheRead":         0,
					"cacheWrite":        0,
					"total":             0.00012 + float64(index)*0.000001,
					"totalFromProvider": true,
				},
			},
			"stopReason":       "toolUse",
			"timestamp":        tsMS,
			"responseModel":    strings.SplitN(model, ":", 2)[0],
			"durationMs":       1000 + index,
			"thinkingLevel":    thinking,
			"requestStartedAt": tsMS - 1000,
			"credential": map[string]any{
				"provider":    provider,
				"fingerprint": "967fba49",
				"algo":        "sha256:8",
				"source":      "OPENROUTER_API_KEY",
			},
			"content": []any{map[string]any{"type": "text", "text": text}},
		},
	}
}

func writeLog(t *testing.T, path, runID string, lines []map[string]any) {
	t.Helper()
	header := map[string]any{
		"type":      "session",
		"version":   3,
		"id":        "session-" + runID,
		"timestamp": "2026-11-12T19:00:00.000Z",
		"cwd":       "/w/lane-1/worktree",
	}
	var buf bytes.Buffer
	all := make([]map[string]any, 0, len(lines)+1)
	all = append(all, header)
	all = append(all, lines...)
	for _, line := range all {
		b, err := json.Marshal(line)
		if err != nil {
			t.Fatalf("marshal log line: %v", err)
		}
		buf.Write(b)
		buf.WriteByte('\n')
	}
	if err := os.WriteFile(path, buf.Bytes(), 0o644); err != nil {
		t.Fatalf("write log %s: %v", path, err)
	}
}

func procFixture(ts string, argv []string, module string, exitCode int, span string) map[string]any {
	return map[string]any{
		"kind":         "proc.exec",
		"ts":           ts,
		"argv":         argv,
		"cwd":          "/w/lane-1/worktree/" + module,
		"repo_rel_cwd": module,
		"exit_code":    exitCode,
		"span_id":      span,
	}
}

func writeTraceRun(t *testing.T, root, runID string, logLines []map[string]any, procRows []map[string]any) string {
	t.Helper()
	baseSHA := strings.Repeat("a", 40)
	runDir := filepath.Join(root, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", runDir, err)
	}
	if logLines != nil {
		writeLog(t, filepath.Join(runDir, runID+".log"), runID, logLines)
	}
	if procRows != nil {
		var buf bytes.Buffer
		for _, row := range procRows {
			b, err := json.Marshal(row)
			if err != nil {
				t.Fatalf("marshal proc row: %v", err)
			}
			buf.Write(b)
			buf.WriteByte('\n')
		}
		if err := os.WriteFile(filepath.Join(runDir, "proc-exec.jsonl"), buf.Bytes(), 0o644); err != nil {
			t.Fatalf("write proc-exec.jsonl: %v", err)
		}
	}
	if err := os.WriteFile(filepath.Join(runDir, "base_sha"), []byte(baseSHA+"\n"), 0o644); err != nil {
		t.Fatalf("write base_sha: %v", err)
	}
	manifest, _ := json.Marshal(map[string]any{
		"run_id":   runID,
		"base_sha": baseSHA,
		"head_sha": baseSHA,
		"tree_sha": baseSHA,
		"tick_id":  "tick-1",
	})
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	return runDir
}

type storeRunSeed struct {
	runID        string
	itemID       any
	laneID       string
	startedAt    int64
	finishedAt   int64
	artifactRefs []map[string]string
}

func makeStore(t *testing.T, path string, rows []storeRunSeed) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir store dir: %v", err)
	}
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatalf("open sqlite %s: %v", path, err)
	}
	defer db.Close()
	if _, err := db.Exec(storeDDL); err != nil {
		t.Fatalf("exec store DDL: %v", err)
	}
	for _, row := range rows {
		startedAt := row.startedAt
		if startedAt == 0 {
			startedAt = 1794526184000
		}
		finishedAt := row.finishedAt
		if finishedAt == 0 {
			finishedAt = 1794526200000
		}
		refsJSON := "[]"
		if len(row.artifactRefs) > 0 {
			b, _ := json.Marshal(row.artifactRefs)
			refsJSON = string(b)
		}
		if _, err := db.Exec(
			"INSERT INTO runs (run_id, item_id, lane_id, backend, state, exit_code,"+
				" started_at, finished_at, artifact_refs) VALUES (?,?,?,?,?,?,?,?,?)",
			row.runID, row.itemID, row.laneID, "pi", "exited", 0,
			startedAt, finishedAt, refsJSON,
		); err != nil {
			t.Fatalf("insert run %s: %v", row.runID, err)
		}
		if _, err := db.Exec(
			"INSERT OR IGNORE INTO lanes (lane_id, state) VALUES (?, 'free')",
			row.laneID,
		); err != nil {
			t.Fatalf("insert lane %s: %v", row.laneID, err)
		}
	}
}

type cliResult struct {
	code   int
	stdout string
	stderr string
}

func runCLIProc(t *testing.T, args ...string) cliResult {
	t.Helper()
	cmd := exec.Command(testBinPath, args...)
	cmd.Dir = testRepoRoot
	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr
	err := cmd.Run()
	code := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		} else {
			t.Fatalf("run %s %v: %v", testBinPath, args, err)
		}
	}
	return cliResult{code: code, stdout: stdout.String(), stderr: stderr.String()}
}

func runJSONDoc(t *testing.T, args ...string) map[string]any {
	t.Helper()
	res := runCLIProc(t, args...)
	if res.code != 0 {
		t.Fatalf("cli %v failed (%d): %s", args, res.code, res.stderr)
	}
	var doc map[string]any
	if err := json.Unmarshal([]byte(res.stdout), &doc); err != nil {
		t.Fatalf("unmarshal run json: %v\n%s", err, res.stdout)
	}
	return doc
}

func copyFile(t *testing.T, src, dst string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(dst), err)
	}
	data, err := os.ReadFile(src)
	if err != nil {
		t.Fatalf("read %s: %v", src, err)
	}
	if err := os.WriteFile(dst, data, 0o644); err != nil {
		t.Fatalf("write %s: %v", dst, err)
	}
}

func staleRun(t *testing.T, root, runID string) string {
	t.Helper()
	return writeTraceRun(t, root, runID, []map[string]any{
		{"type": "request_start", "timestamp": int64(1794510838000)},
		{"type": "tool_execution_start", "toolCallId": "e1", "toolName": "edit",
			"args": map[string]any{"path": "/w/lane-1/worktree/tools/tillr-store/read.go"}},
		{"type": "tool_execution_end", "toolCallId": "e1", "toolName": "edit",
			"result":  map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}},
			"isError": false, "durationMs": 3, "timestamp": int64(1794510840000)},
		turnEnd(1, "Edited.\nRESULT: done", 1794510845000),
	}, []map[string]any{
		procFixture("2026-11-12T19:10:02.000Z", []string{"go", "test", "./..."}, "tools/tillr-store", 0, "sh-00001"),
	})
}

func TestParserRecoversMeasuredTotals(t *testing.T) {
	laneID := "lane-1"
	parsed := parseRunLog(filepath.Join("testdata", "canary-run.log"), &TraceContext{
		RunID:  "run-canary-1",
		LaneID: &laneID,
	}, false)
	if parsed.State != "ok" {
		t.Fatalf("state = %q (%s), want ok", parsed.State, parsed.Detail)
	}
	s := parsed.Summary
	if s.Turns != measuredTotals.turns {
		t.Fatalf("turns = %d, want %d", s.Turns, measuredTotals.turns)
	}
	if s.TokensIn != measuredTotals.tokensIn {
		t.Fatalf("tokens_in = %d, want %d", s.TokensIn, measuredTotals.tokensIn)
	}
	if s.TokensOut != measuredTotals.tokensOut {
		t.Fatalf("tokens_out = %d, want %d", s.TokensOut, measuredTotals.tokensOut)
	}
	if s.CacheRead != measuredTotals.cacheRead {
		t.Fatalf("cache_read = %d, want %d", s.CacheRead, measuredTotals.cacheRead)
	}
	if s.Reasoning != measuredTotals.reasoning {
		t.Fatalf("reasoning = %d, want %d", s.Reasoning, measuredTotals.reasoning)
	}
	if s.ToolCalls != measuredTotals.toolCalls {
		t.Fatalf("tool_calls = %d, want %d", s.ToolCalls, measuredTotals.toolCalls)
	}
	if math.Abs(s.CostUSD-measuredTotals.costUSD) >= 1e-9 {
		t.Fatalf("cost_usd = %.12f, want %.12f", s.CostUSD, measuredTotals.costUSD)
	}

	var llmTurns, toolCalls int
	var firstTurn map[string]any
	var writeTargets []string
	for _, row := range parsed.Rows.Rows {
		switch row.Kind {
		case "llm.turn":
			llmTurns++
			if firstTurn == nil {
				_ = json.Unmarshal([]byte(row.Payload), &firstTurn)
			}
		case "tool.call":
			toolCalls++
			var p map[string]any
			_ = json.Unmarshal([]byte(row.Payload), &p)
			if p["tool_name"] == "write" {
				if tp, ok := p["target_path"].(string); ok {
					writeTargets = append(writeTargets, tp)
				}
			}
		}
	}
	if llmTurns != measuredTotals.turns {
		t.Fatalf("llm.turn count = %d, want %d", llmTurns, measuredTotals.turns)
	}
	if toolCalls != measuredTotals.toolCalls {
		t.Fatalf("tool.call count = %d, want %d", toolCalls, measuredTotals.toolCalls)
	}
	if firstTurn["provider"] != "openrouter" ||
		firstTurn["thinking_level"] != "high" ||
		firstTurn["credential_fp"] != "967fba49" ||
		firstTurn["stop_reason"] != "toolUse" {
		t.Fatalf("unexpected first turn payload: %v", firstTurn)
	}
	wantWrites := []string{"/workspace/.yolo/durable/canary/lanes/lane-1/worktree/CANARY.md"}
	if !slices.Equal(writeTargets, wantWrites) {
		t.Fatalf("write targets = %v, want %v", writeTargets, wantWrites)
	}
}

func TestCostAttributesTokensToLaneAndRoute(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "cost-root")
	logDst := filepath.Join(root, "runs", "run-canary-1", "run-canary-1.log")
	copyFile(t, filepath.Join("testdata", "canary-run.log"), logDst)
	store := filepath.Join(work, "cost-store.db")
	makeStore(t, store, []storeRunSeed{{runID: "run-canary-1", itemID: "574", laneID: "lane-1"}})

	res := runCLIProc(t, "--trace-root", root, "--store", store, "cost")
	if res.code != 0 {
		t.Fatalf("cost failed (%d): %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "lane-1") {
		t.Fatalf("missing lane-1 in cost output:\n%s", res.stdout)
	}
	if !strings.Contains(res.stdout, "openrouter:deepseek/deepseek-v4.1-flash-1017:floor:high") {
		t.Fatalf("missing openrouter route in cost output:\n%s", res.stdout)
	}
	var line string
	for _, l := range strings.Split(res.stdout, "\n") {
		if strings.Contains(l, "lane-1") {
			line = l
			break
		}
	}
	fields := strings.Fields(line)
	if len(fields) < 5 {
		t.Fatalf("unexpected cost line: %q", line)
	}
	gotIn, err := strconv.ParseInt(fields[len(fields)-5], 10, 64)
	if err != nil || gotIn != measuredTotals.tokensIn {
		t.Fatalf("tokens_in = %q, want %d (line: %s)", fields[len(fields)-5], measuredTotals.tokensIn, line)
	}
	gotCache, err := strconv.ParseInt(fields[len(fields)-4], 10, 64)
	if err != nil || gotCache != measuredTotals.cacheRead {
		t.Fatalf("cache_read = %q, want %d (line: %s)", fields[len(fields)-4], measuredTotals.cacheRead, line)
	}
	gotOut, err := strconv.ParseInt(fields[len(fields)-3], 10, 64)
	if err != nil || gotOut != measuredTotals.tokensOut {
		t.Fatalf("tokens_out = %q, want %d (line: %s)", fields[len(fields)-3], measuredTotals.tokensOut, line)
	}
	gotCost, err := strconv.ParseFloat(fields[len(fields)-1], 64)
	if err != nil || math.Abs(gotCost-measuredTotals.costUSD) >= 1e-6 {
		t.Fatalf("cost = %q, want %.9f (line: %s)", fields[len(fields)-1], measuredTotals.costUSD, line)
	}

	laneID := "lane-1"
	summary := parseRunLog(logDst, &TraceContext{RunID: "run-canary-1", LaneID: &laneID}, false).Summary
	if gotIn != summary.TokensIn {
		t.Fatalf("cost tokens_in %d != parser summary %d", gotIn, summary.TokensIn)
	}
}

func TestDiscrepanciesFlagsASuiteThatRanBeforeTheLastEdit(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "stale-root")
	runDir := filepath.Join(root, "runs", "run-stale-1")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", runDir, err)
	}
	writeLog(t, filepath.Join(runDir, "run-stale-1.log"), "run-stale-1", []map[string]any{
		{"type": "request_start", "timestamp": int64(1794510838000)},
		{"type": "tool_execution_start", "toolCallId": "call-edit-1", "toolName": "edit",
			"args": map[string]any{"path": "/w/lane-1/worktree/tools/tillr-store/read.go"}},
		{"type": "tool_execution_end", "toolCallId": "call-edit-1", "toolName": "edit",
			"result":  map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}},
			"isError": false, "durationMs": 3, "timestamp": int64(1794510840000)},
		turnEnd(1, "Edited the store.\nRESULT: done", 1794510845000),
	})
	var procBuf bytes.Buffer
	for _, r := range []map[string]any{
		procFixture("2026-11-12T19:10:02.000Z", []string{"go", "test", "./..."}, "tools/tillr-store", 0, "sh-00001"),
		procFixture("2026-11-12T19:12:02.000Z", []string{"go", "test", "./..."}, "tools/tillr-gate", 0, "sh-00002"),
	} {
		b, _ := json.Marshal(r)
		procBuf.Write(b)
		procBuf.WriteByte('\n')
	}
	if err := os.WriteFile(filepath.Join(runDir, "proc-exec.jsonl"), procBuf.Bytes(), 0o644); err != nil {
		t.Fatalf("write proc-exec.jsonl: %v", err)
	}
	manifest, _ := json.Marshal(map[string]any{
		"run_id":   "run-stale-1",
		"base_sha": strings.Repeat("a", 40),
		"head_sha": strings.Repeat("a", 40),
		"tree_sha": strings.Repeat("b", 40),
		"tick_id":  "tick-1",
	})
	if err := os.WriteFile(filepath.Join(runDir, "manifest.json"), manifest, 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}
	store := filepath.Join(work, "stale-store.db")
	makeStore(t, store, []storeRunSeed{{runID: "run-stale-1", itemID: "574", laneID: "lane-1"}})

	res := runCLIProc(t, "--trace-root", root, "--store", store, "discrepancies")
	if res.code != 0 {
		t.Fatalf("discrepancies failed (%d): %s", res.code, res.stderr)
	}
	for _, want := range []string{
		"disproved",
		"owed_suite",
		"suite passed only before the last edit",
		"uncommitted_worktree_with_result_done",
	} {
		if !strings.Contains(res.stdout, want) {
			t.Fatalf("missing %q in discrepancies output:\n%s", want, res.stdout)
		}
	}
}

func TestDiscrepanciesDoesNotFlagASuiteThatRanAfterTheLastEdit(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "fresh-root")
	runDir := writeTraceRun(t, root, "run-fresh-1", []map[string]any{
		{"type": "request_start", "timestamp": int64(1794510838000)},
		{"type": "tool_execution_start", "toolCallId": "call-edit-1", "toolName": "edit",
			"args": map[string]any{"path": "/w/lane-1/worktree/tools/tillr-store/read.go"}},
		{"type": "tool_execution_end", "toolCallId": "call-edit-1", "toolName": "edit",
			"result":  map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}},
			"isError": false, "durationMs": 3, "timestamp": int64(1794510840000)},
		turnEnd(1, "Edited and re-ran.\nRESULT: done", 1794510845000),
	}, []map[string]any{
		procFixture("2026-11-12T19:20:02.000Z", []string{"go", "test", "./..."}, "tools/tillr-store", 0, "sh-00009"),
	})

	manifestPath := filepath.Join(runDir, "manifest.json")
	raw, _ := os.ReadFile(manifestPath)
	var manifest map[string]any
	_ = json.Unmarshal(raw, &manifest)
	manifest["head_sha"] = strings.Repeat("c", 40)
	updated, _ := json.Marshal(manifest)
	if err := os.WriteFile(manifestPath, updated, 0o644); err != nil {
		t.Fatalf("write manifest.json: %v", err)
	}

	store := filepath.Join(work, "fresh-store.db")
	makeStore(t, store, []storeRunSeed{{runID: "run-fresh-1", itemID: "574", laneID: "lane-1"}})

	res := runCLIProc(t, "--trace-root", root, "--store", store, "discrepancies")
	if res.code != 0 {
		t.Fatalf("discrepancies failed (%d): %s", res.code, res.stderr)
	}
	wantOut := "discrepancies: none (no disproved claim in the runs read)"
	if strings.TrimSpace(res.stdout) != wantOut {
		t.Fatalf("discrepancies stdout = %q, want %q", res.stdout, wantOut)
	}

	audit := runCLIProc(t, "--trace-root", root, "--store", store, "audit", "run-fresh-1")
	if audit.code != 0 {
		t.Fatalf("audit failed (%d): %s", audit.code, audit.stderr)
	}
	if !strings.Contains(audit.stdout, "verified") || strings.Contains(audit.stdout, "disproved") {
		t.Fatalf("unexpected audit output:\n%s", audit.stdout)
	}
}

func TestMissingArtifactIsUnreadableNotACrash(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "empty-root")
	if err := os.MkdirAll(filepath.Join(root, "runs", "run-empty-1"), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	store := filepath.Join(work, "absent-store.db")

	audit := runCLIProc(t, "--trace-root", root, "--store", store, "audit", "run-empty-1")
	if audit.code != 0 {
		t.Fatalf("audit failed (%d): %s", audit.code, audit.stderr)
	}
	if !strings.Contains(audit.stdout, "unreadable") {
		t.Fatalf("expected unreadable in audit output:\n%s", audit.stdout)
	}
	if strings.Contains(audit.stdout, "log_state") {
		t.Fatalf("unexpected log_state in audit output:\n%s", audit.stdout)
	}

	cost := runCLIProc(t, "--trace-root", root, "--store", store, "cost")
	if cost.code != 0 {
		t.Fatalf("cost failed (%d): %s", cost.code, cost.stderr)
	}
	if !strings.Contains(cost.stdout, "cost: no llm.turn row") {
		t.Fatalf("expected 'cost: no llm.turn row' in cost output:\n%s", cost.stdout)
	}

	truncRoot := filepath.Join(work, "truncated-root")
	truncDir := filepath.Join(truncRoot, "runs", "run-trunc-1")
	if err := os.MkdirAll(truncDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	logPath := filepath.Join(truncDir, "run-trunc-1.log")
	writeLog(t, logPath, "run-trunc-1", []map[string]any{turnEnd(1, "hi", 1)})
	f, err := os.OpenFile(logPath, os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		t.Fatalf("open append: %v", err)
	}
	_, _ = f.WriteString(`{"type":"turn_end","message":{"role":"assist`)
	_ = f.Close()

	parsed := parseRunLog(logPath, &TraceContext{RunID: "run-trunc-1"}, false)
	if parsed.State != "truncated" {
		t.Fatalf("state = %q (%s), want truncated", parsed.State, parsed.Detail)
	}
	if parsed.Summary.Turns != 1 {
		t.Fatalf("turns = %d, want 1", parsed.Summary.Turns)
	}
}

func TestStoreIsOpenedReadOnly(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "readonly-root")
	copyFile(t, filepath.Join("testdata", "canary-run.log"), filepath.Join(root, "runs", "run-canary-1", "run-canary-1.log"))
	storeDir := filepath.Join(work, "readonly")
	store := filepath.Join(storeDir, "state.db")
	makeStore(t, store, []storeRunSeed{{runID: "run-canary-1", itemID: "574", laneID: "lane-1"}})
	if err := os.Chmod(store, 0o444); err != nil {
		t.Fatalf("chmod store: %v", err)
	}
	if err := os.Chmod(storeDir, 0o555); err != nil {
		t.Fatalf("chmod storeDir: %v", err)
	}
	t.Cleanup(func() {
		_ = os.Chmod(storeDir, 0o755)
		_ = os.Chmod(store, 0o644)
	})

	res := runCLIProc(t, "--trace-root", root, "--store", store, "cost")
	if res.code != 0 {
		t.Fatalf("cost failed (%d): %s", res.code, res.stderr)
	}
	if !strings.Contains(res.stdout, "lane-1") {
		t.Fatalf("missing lane-1 in cost output:\n%s", res.stdout)
	}

	opened := openStore(store)
	defer opened.Close()
	if opened.State != "ok" {
		t.Fatalf("openStore state = %q (%s), want ok", opened.State, opened.Detail)
	}
	if _, err := opened.DB.Exec("CREATE TABLE should_not_exist (a TEXT)"); err == nil {
		t.Fatalf("the store connection accepted a write; it is not read-only")
	}
}

func TestLoopDirectorySuppliesProcAndGateRecords(t *testing.T) {
	work := t.TempDir()
	traceRoot := filepath.Join(work, "loop-trace")
	store := filepath.Join(work, "loop-repo", ".tillr", "state.db")
	runID := "run-loop-1"
	loopDir := filepath.Join(filepath.Dir(store), "loop", runID)
	if err := os.MkdirAll(loopDir, 0o755); err != nil {
		t.Fatalf("mkdir loopDir: %v", err)
	}
	writeLog(t, filepath.Join(loopDir, runID+".log"), runID, []map[string]any{
		turnEnd(1, "Ran the suite.\nRESULT: done", 1794510845000),
	})
	procBytes, _ := json.Marshal(procFixture("2026-11-12T19:20:02.000Z", []string{"go", "test", "./..."}, "tools/tillr-store", 0, "sh-00001"))
	if err := os.WriteFile(filepath.Join(loopDir, "proc-exec.jsonl"), append(procBytes, '\n'), 0o644); err != nil {
		t.Fatalf("write proc-exec.jsonl: %v", err)
	}

	var gitBuf bytes.Buffer
	for _, row := range []map[string]any{
		{"event": "start", "sid": runID + "/1", "time": "2026-11-12T19:20:03.000Z", "argv": []string{"/bin/git", "commit", "-m", "the fix"}},
		{"event": "exit", "sid": runID + "/1", "time": "2026-11-12T19:20:03.100Z", "t_abs": 0.1, "code": 0},
	} {
		b, _ := json.Marshal(row)
		gitBuf.Write(b)
		gitBuf.WriteByte('\n')
	}
	_ = os.WriteFile(filepath.Join(loopDir, "git-trace2.jsonl"), gitBuf.Bytes(), 0o644)
	_ = os.WriteFile(filepath.Join(loopDir, "base_sha"), []byte(strings.Repeat("a", 40)+"\n"), 0o644)
	_ = os.WriteFile(filepath.Join(loopDir, "brief.md"), []byte("# brief\n"), 0o644)
	_ = os.WriteFile(filepath.Join(loopDir, "fence.txt"), []byte("tools/tillr-store\n"), 0o644)

	planBytes, _ := json.Marshal(map[string]any{
		"record_version": 1,
		"kind":           "gate.plan",
		"id":             "gate-plan-1",
		"created_at":     "2026-11-12T19:20:04Z",
		"payload": map[string]any{
			"plan_id":         "plan-abc",
			"base_sha":        strings.Repeat("a", 40),
			"tree_sha":        strings.Repeat("b", 40),
			"registry_digest": "digest-1",
			"checks": []map[string]any{
				{"check_id": "store-test", "command": []string{"go", "test", "./..."}},
				{"check_id": "trace-test", "command": []string{"go", "test", "./..."}},
			},
		},
	})
	_ = os.WriteFile(filepath.Join(loopDir, "gate.plan.json"), planBytes, 0o644)

	verdictBytes, _ := json.Marshal(map[string]any{
		"record_version": 1,
		"kind":           "gate.verdict",
		"id":             "gate-verdict-1",
		"created_at":     "2026-11-12T19:20:09Z",
		"payload": map[string]any{
			"verdict_id":      "verdict-1",
			"plan_id":         "plan-abc",
			"registry_digest": "digest-1",
			"base_sha":        strings.Repeat("a", 40),
			"tree_sha":        strings.Repeat("b", 40),
			"result":          "fail",
			"checks": []map[string]any{
				{"check_id": "store-test", "result": "pass", "duration_ms": 1200},
				{"check_id": "trace-test", "result": "fail", "duration_ms": 800},
			},
		},
	})
	_ = os.WriteFile(filepath.Join(loopDir, "gate.verdict.json"), verdictBytes, 0o644)

	makeStore(t, store, []storeRunSeed{{runID: runID, itemID: "574", laneID: "lane-1"}})

	audit := runCLIProc(t, "--trace-root", traceRoot, "--store", store, "audit", runID)
	if audit.code != 0 {
		t.Fatalf("audit failed (%d): %s", audit.code, audit.stderr)
	}
	if !strings.Contains(audit.stdout, "proc [ok]") {
		t.Fatalf("expected 'proc [ok]' in audit output:\n%s", audit.stdout)
	}
	if strings.Contains(audit.stdout, "git [") {
		t.Fatalf("unexpected 'git [' in audit output:\n%s", audit.stdout)
	}

	doc := runJSONDoc(t, "--trace-root", traceRoot, "--store", store, "run", runID)
	runObj := doc["run"].(map[string]any)
	if runObj["brief_path"] != filepath.Join(loopDir, "brief.md") {
		t.Fatalf("brief_path = %v, want %s", runObj["brief_path"], filepath.Join(loopDir, "brief.md"))
	}

	gateRows := map[string]map[string]any{}
	var procRows []map[string]any
	var gitRows []any
	for _, rawEv := range doc["events"].([]any) {
		ev := rawEv.(map[string]any)
		kind := ev["kind"].(string)
		payload := ev["payload"].(map[string]any)
		switch {
		case strings.HasPrefix(kind, "gate."):
			gateRows[kind] = payload
		case kind == "proc.exec":
			procRows = append(procRows, payload)
		case kind == "git.trace2":
			gitRows = append(gitRows, ev)
		}
	}
	if len(gateRows) != 2 || gateRows["gate.plan"] == nil || gateRows["gate.verdict"] == nil {
		t.Fatalf("unexpected gateRows: %v", gateRows)
	}
	if gateRows["gate.plan"]["checks_count"] != float64(2) {
		t.Fatalf("checks_count = %v, want 2", gateRows["gate.plan"]["checks_count"])
	}
	if gateRows["gate.verdict"]["verdict"] != "fail" {
		t.Fatalf("verdict = %v, want fail", gateRows["gate.verdict"]["verdict"])
	}
	timings := gateRows["gate.verdict"]["check_timings_ms"].(map[string]any)
	if timings["store-test"] != float64(1200) || timings["trace-test"] != float64(800) {
		t.Fatalf("check_timings_ms = %v", timings)
	}
	if len(procRows) != 1 || procRows[0]["exit_code"] != float64(0) {
		t.Fatalf("procRows = %v", procRows)
	}
	if len(gitRows) != 0 {
		t.Fatalf("expected stale git.trace2 stream ignored, got %v", gitRows)
	}
}

func TestEmptyProcExecIsAbsentEvidenceNotProof(t *testing.T) {
	work := t.TempDir()
	runID := "run-emptyproc-1"
	root := filepath.Join(work, "emptyproc-root")
	runDir := filepath.Join(root, "runs", runID)
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeLog(t, filepath.Join(runDir, runID+".log"), runID, []map[string]any{
		{"type": "request_start", "timestamp": int64(1794510838000)},
		{"type": "tool_execution_start", "toolCallId": "call-edit-1", "toolName": "edit",
			"args": map[string]any{"path": "/w/lane-1/worktree/tools/tillr-store/read.go"}},
		{"type": "tool_execution_end", "toolCallId": "call-edit-1", "toolName": "edit",
			"result":  map[string]any{"content": []any{map[string]any{"type": "text", "text": "ok"}}},
			"isError": false, "durationMs": 3, "timestamp": int64(1794510840000)},
		turnEnd(1, "Edited the store, then ran the suite.\nRESULT: done", 1794510845000),
	})
	procPath := filepath.Join(runDir, "proc-exec.jsonl")
	if err := os.WriteFile(procPath, nil, 0o644); err != nil {
		t.Fatalf("write empty proc-exec.jsonl: %v", err)
	}
	manifest, _ := json.Marshal(map[string]any{
		"run_id":   runID,
		"base_sha": strings.Repeat("a", 40),
		"head_sha": strings.Repeat("c", 40),
		"tree_sha": strings.Repeat("b", 40),
		"tick_id":  "tick-1",
	})
	_ = os.WriteFile(filepath.Join(runDir, "manifest.json"), manifest, 0o644)
	store := filepath.Join(work, "emptyproc-store.db")
	makeStore(t, store, []storeRunSeed{{runID: runID, itemID: "574", laneID: "lane-1"}})

	parsed := parseProcExec(procPath, &TraceContext{RunID: runID})
	if parsed.State != "missing" {
		t.Fatalf("state = %q (%s), want missing", parsed.State, parsed.Detail)
	}
	if len(parsed.Rows.Rows) != 0 {
		t.Fatalf("expected 0 rows, got %d", len(parsed.Rows.Rows))
	}

	audit := runCLIProc(t, "--trace-root", root, "--store", store, "audit", runID)
	if audit.code != 0 {
		t.Fatalf("audit failed (%d): %s", audit.code, audit.stderr)
	}
	for _, want := range []string{"owed_suite", "unverified", "no proc.exec record"} {
		if !strings.Contains(audit.stdout, want) {
			t.Fatalf("missing %q in audit output:\n%s", want, audit.stdout)
		}
	}
	if strings.Contains(audit.stdout, "disproved") {
		t.Fatalf("unexpected disproved in audit output:\n%s", audit.stdout)
	}
}

func TestALargeSourceIsStreamed(t *testing.T) {
	work := t.TempDir()
	logPath := filepath.Join(work, "stream-root", "runs", "run-stream", "run-stream.log")
	if err := os.MkdirAll(filepath.Dir(logPath), 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	f, err := os.Create(logPath)
	if err != nil {
		t.Fatalf("create log: %v", err)
	}
	header, _ := json.Marshal(map[string]any{
		"type":      "session",
		"version":   3,
		"id":        "session-stream",
		"timestamp": "2026-11-12T19:00:00.000Z",
	})
	_, _ = f.Write(append(header, '\n'))
	filler := strings.Repeat("x", 64*1024)
	update, _ := json.Marshal(map[string]any{"type": "message_update", "delta": filler})
	updateLine := append(update, '\n')
	for range 512 {
		_, _ = f.Write(updateLine)
	}
	endLine, _ := json.Marshal(turnEnd(1, "done", 1794510845000))
	_, _ = f.Write(append(endLine, '\n'))
	_ = f.Close()

	info, err := os.Stat(logPath)
	if err != nil {
		t.Fatalf("stat: %v", err)
	}
	size := info.Size()
	if size <= 32*1024*1024 {
		t.Fatalf("expected >32 MiB log, got %d", size)
	}

	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	parsed := parseRunLog(logPath, &TraceContext{RunID: "run-stream"}, false)
	runtime.GC()
	runtime.ReadMemStats(&after)

	if parsed.State != "ok" {
		t.Fatalf("state = %q (%s), want ok", parsed.State, parsed.Detail)
	}
	if parsed.Summary.Turns != 1 {
		t.Fatalf("turns = %d, want 1", parsed.Summary.Turns)
	}
	var liveDelta int64
	if after.HeapAlloc > before.HeapAlloc {
		liveDelta = int64(after.HeapAlloc - before.HeapAlloc)
	}
	if liveDelta >= size/4 {
		t.Fatalf("live heap delta %d >= size/4 (%d)", liveDelta, size/4)
	}
}

func TestTheLogTheRunRecordNamesIsRead(t *testing.T) {
	work := t.TempDir()
	store := filepath.Join(work, "named-log-repo", ".tillr", "state.db")
	runID := "run-named-log"
	laneDir := filepath.Join(work, "named-log-repo", ".tillr", "lanes", "lane-1")
	if err := os.MkdirAll(laneDir, 0o755); err != nil {
		t.Fatalf("mkdir laneDir: %v", err)
	}
	laneLog := filepath.Join(laneDir, runID+".log")
	writeLog(t, laneLog, runID, []map[string]any{turnEnd(1, "done", 1794510845000)})
	if err := os.MkdirAll(filepath.Join(filepath.Dir(store), "loop", runID), 0o755); err != nil {
		t.Fatalf("mkdir loopDir: %v", err)
	}
	makeStore(t, store, []storeRunSeed{{
		runID:  runID,
		itemID: "574",
		laneID: "lane-1",
		artifactRefs: []map[string]string{
			{"kind": "lane-root", "ref": laneDir},
			{"kind": "log", "ref": laneLog},
		},
	}})

	root := filepath.Join(work, "named-log-trace")
	doc := runJSONDoc(t, "--trace-root", root, "--store", store, "run", runID)
	runObj := doc["run"].(map[string]any)
	if runObj["log_path"] != laneLog || runObj["log_state"] != "ok" {
		t.Fatalf("log_path=%v log_state=%v, want (%s, ok)", runObj["log_path"], runObj["log_state"], laneLog)
	}
	var turns int
	for _, rawEv := range doc["events"].([]any) {
		if rawEv.(map[string]any)["kind"] == "llm.turn" {
			turns++
		}
	}
	if turns != 1 {
		t.Fatalf("turns = %d, want 1", turns)
	}
}

func TestAToolResultShapedAsTextIsStillARow(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "textshape-root")
	runDir := filepath.Join(root, "runs", "run-text")
	if err := os.MkdirAll(runDir, 0o755); err != nil {
		t.Fatalf("mkdir: %v", err)
	}
	writeLog(t, filepath.Join(runDir, "run-text.log"), "run-text", []map[string]any{
		{"type": "tool_execution_start", "toolCallId": "c1", "toolName": "read",
			"args": map[string]any{"path": "/w/a.md"}},
		{"type": "tool_execution_end", "toolCallId": "c1", "toolName": "read",
			"result": map[string]any{
				"content":           []any{map[string]any{"type": "text", "text": "x"}},
				"structuredContent": "---\nname: lane-work\n",
			},
			"isError": false, "durationMs": 3},
		{"type": "tool_execution_start", "toolCallId": "c2", "toolName": "bash", "args": "ls"},
		{"type": "tool_execution_end", "toolCallId": "c2", "toolName": "bash",
			"result": "plain text result", "isError": true, "durationMs": 4},
		{"type": "tool_execution_start", "toolCallId": "c3", "toolName": "bash",
			"args": map[string]any{"command": "ls"}},
		{"type": "tool_execution_end", "toolCallId": "c3", "toolName": "bash",
			"result":  map[string]any{"structuredContent": map[string]any{"exit_code": 7}},
			"isError": true, "durationMs": 5},
		turnEnd(1, "done", 1794510845000),
	})
	store := filepath.Join(work, "textshape-store", ".tillr", "state.db")
	makeStore(t, store, []storeRunSeed{{runID: "run-text", itemID: "1", laneID: "lane-1"}})

	doc := runJSONDoc(t, "--trace-root", root, "--store", store, "run", "run-text")
	calls := map[string]map[string]any{}
	for _, rawEv := range doc["events"].([]any) {
		ev := rawEv.(map[string]any)
		if ev["kind"] == "tool.call" {
			p := ev["payload"].(map[string]any)
			calls[p["tool_call_id"].(string)] = p
		}
	}
	if len(calls) != 3 || calls["c1"] == nil || calls["c2"] == nil || calls["c3"] == nil {
		t.Fatalf("unexpected calls: %v", calls)
	}
	if calls["c1"]["target_path"] != "/w/a.md" || calls["c1"]["exit_code"] != nil {
		t.Fatalf("unexpected c1: %v", calls["c1"])
	}
	if calls["c2"]["is_error"] != true || calls["c2"]["exit_code"] != nil {
		t.Fatalf("unexpected c2: %v", calls["c2"])
	}
	if calls["c3"]["exit_code"] != float64(7) {
		t.Fatalf("unexpected c3: %v", calls["c3"])
	}
}

func TestOneUnparseableRunDoesNotAbortTheRead(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "poison-root")
	for _, runID := range []string{"run-bad", "run-good"} {
		runDir := filepath.Join(root, "runs", runID)
		if err := os.MkdirAll(runDir, 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		writeLog(t, filepath.Join(runDir, runID+".log"), runID, []map[string]any{
			turnEnd(1, "done", 1794510845000),
		})
	}
	store := filepath.Join(work, "poison-store", ".tillr", "state.db")
	makeStore(t, store, []storeRunSeed{
		{runID: "run-bad", itemID: "1", laneID: "lane-1"},
		{runID: "run-good", itemID: "1", laneID: "lane-1"},
	})

	real := parseRunLogFn
	parseRunLogFn = func(path string, trace *TraceContext, turnsOnly bool) Parsed {
		if strings.Contains(path, "run-bad") {
			panic("AttributeError: 'str' object has no attribute 'get'")
		}
		return real(path, trace, turnsOnly)
	}
	defer func() { parseRunLogFn = real }()

	corpus, err := newCorpus(root, store, "")
	if err != nil {
		t.Fatalf("newCorpus: %v", err)
	}
	defer corpus.Close()
	corpus.load(corpus.runIDs(0), nil, true)

	var turns []string
	rows, err := corpus.DB.Query("SELECT run_id FROM obs_events WHERE kind = 'llm.turn'")
	if err != nil {
		t.Fatalf("query turns: %v", err)
	}
	for rows.Next() {
		var r string
		_ = rows.Scan(&r)
		turns = append(turns, r)
	}
	rows.Close()
	if !slices.Equal(turns, []string{"run-good"}) {
		t.Fatalf("turns = %v, want [run-good]", turns)
	}

	var unreadable []struct {
		runID   string
		payload string
	}
	urows, err := corpus.DB.Query("SELECT run_id, payload FROM obs_events WHERE kind = 'unreadable'")
	if err != nil {
		t.Fatalf("query unreadable: %v", err)
	}
	for urows.Next() {
		var runID, payload string
		_ = urows.Scan(&runID, &payload)
		unreadable = append(unreadable, struct {
			runID   string
			payload string
		}{runID, payload})
	}
	urows.Close()
	if len(unreadable) != 1 || unreadable[0].runID != "run-bad" {
		t.Fatalf("unreadable = %+v, want 1 row for run-bad", unreadable)
	}
	if !strings.Contains(unreadable[0].payload, "AttributeError") {
		t.Fatalf("unreadable payload = %s, want AttributeError", unreadable[0].payload)
	}
}

func treeFiles(t *testing.T, roots ...string) []string {
	t.Helper()
	var out []string
	for _, root := range roots {
		if _, err := os.Stat(root); os.IsNotExist(err) {
			continue
		}
		err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			out = append(out, path)
			return nil
		})
		if err != nil {
			t.Fatalf("walk %s: %v", root, err)
		}
	}
	slices.Sort(out)
	return out
}

func TestNoCommandWritesAnIndex(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "nowrite-root")
	staleRun(t, root, "run-nowrite-1")
	store := filepath.Join(work, "nowrite-repo", ".tillr", "state.db")
	makeStore(t, store, []storeRunSeed{{runID: "run-nowrite-1", itemID: "574", laneID: "lane-1"}})

	before := treeFiles(t, root, filepath.Dir(store), testDefaultTraceDir)
	common := []string{"--trace-root", root, "--store", store}
	queries := [][]string{
		{"tick", "--last"},
		{"tick", "tick-1"},
		{"audit", "run-nowrite-1"},
		{"run", "run-nowrite-1"},
		{"cost"},
		{"cost", "--runs", "0"},
		{"discrepancies"},
		{"discrepancies", "--all", "--runs", "0"},
	}
	for _, q := range queries {
		res := runCLIProc(t, append(common, q...)...)
		if res.code != 0 {
			t.Fatalf("%v failed (%d): %s", q, res.code, res.stderr)
		}
	}
	for _, q := range [][]string{{"tick", "--last"}, {"cost"}} {
		res := runCLIProc(t, append([]string{"--store", store}, q...)...)
		if res.code != 0 {
			t.Fatalf("%v failed (%d): %s", q, res.code, res.stderr)
		}
	}
	after := treeFiles(t, root, filepath.Dir(store), testDefaultTraceDir)
	if !slices.Equal(after, before) {
		t.Fatalf("files changed:\nbefore=%v\nafter=%v", before, after)
	}
	if _, err := os.Stat(testDefaultTraceDir); !os.IsNotExist(err) {
		t.Fatalf("default trace root %s should not exist", testDefaultTraceDir)
	}
	refused := runCLIProc(t, append(common, "ingest")...)
	if refused.code != 2 || !strings.Contains(refused.stderr, "invalid choice") {
		t.Fatalf("ingest got code=%d stderr=%q, want code=2 with invalid choice", refused.code, refused.stderr)
	}
}

func TestRunsBoundsTheRunsAnAggregateReads(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "bound-root")
	for _, runID := range []string{"run-old", "run-new"} {
		copyFile(t, filepath.Join("testdata", "canary-run.log"), filepath.Join(root, "runs", runID, runID+".log"))
	}
	store := filepath.Join(work, "bound-store.db")
	makeStore(t, store, []storeRunSeed{
		{runID: "run-old", laneID: "lane-old", startedAt: 1794500000000},
		{runID: "run-new", laneID: "lane-new", startedAt: 1794600000000},
	})
	common := []string{"--trace-root", root, "--store", store}

	one := runCLIProc(t, append(common, "cost", "--runs", "1")...)
	if one.code != 0 {
		t.Fatalf("cost --runs 1 failed (%d): %s", one.code, one.stderr)
	}
	if !strings.Contains(one.stdout, "lane-new") || strings.Contains(one.stdout, "lane-old") {
		t.Fatalf("unexpected --runs 1 output:\n%s", one.stdout)
	}

	every := runCLIProc(t, append(common, "cost", "--runs", "0")...)
	if every.code != 0 {
		t.Fatalf("cost --runs 0 failed (%d): %s", every.code, every.stderr)
	}
	if !strings.Contains(every.stdout, "lane-new") || !strings.Contains(every.stdout, "lane-old") {
		t.Fatalf("unexpected --runs 0 output:\n%s", every.stdout)
	}

	helptext := runCLIProc(t, "cost", "--help")
	collapsed := strings.Join(strings.Fields(helptext.stdout), " ")
	if !strings.Contains(collapsed, "default 100") {
		t.Fatalf("expected 'default 100' in cost --help:\n%s", helptext.stdout)
	}
}

func TestSinceComparesTheRunFinishTime(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "since-root")
	staleRun(t, root, "run-since-1")
	store := filepath.Join(work, "since-store.db")
	finished := int64(1794526200000)
	makeStore(t, store, []storeRunSeed{{
		runID:      "run-since-1",
		itemID:     "574",
		laneID:     "lane-1",
		finishedAt: finished,
	}})

	at := *isoFromIntMs(&finished)
	afterMs := finished + 1000
	after := *isoFromIntMs(&afterMs)

	common := []string{"--trace-root", root, "--store", store, "discrepancies"}
	kept := runCLIProc(t, append(common, "--since", at)...)
	if kept.code != 0 {
		t.Fatalf("discrepancies --since at failed (%d): %s", kept.code, kept.stderr)
	}
	if !strings.Contains(kept.stdout, "owed_suite") {
		t.Fatalf("expected owed_suite at %s:\n%s", at, kept.stdout)
	}

	dropped := runCLIProc(t, append(common, "--since", after)...)
	if dropped.code != 0 {
		t.Fatalf("discrepancies --since after failed (%d): %s", dropped.code, dropped.stderr)
	}
	if strings.Contains(dropped.stdout, "owed_suite") {
		t.Fatalf("did not expect owed_suite at %s:\n%s", after, dropped.stdout)
	}

	doc := runJSONDoc(t, "--trace-root", root, "--store", store, "run", "run-since-1")
	for _, rawCl := range doc["claims"].([]any) {
		cl := rawCl.(map[string]any)
		if cl["observed_at"] != at {
			t.Fatalf("claim observed_at = %v, want %s", cl["observed_at"], at)
		}
	}
}

func TestRunDocumentIsTheShapeTillrViewReads(t *testing.T) {
	work := t.TempDir()
	root := filepath.Join(work, "doc-root")
	staleRun(t, root, "run-doc-1")
	store := filepath.Join(work, "doc-store.db")
	makeStore(t, store, []storeRunSeed{{runID: "run-doc-1", itemID: "574", laneID: "lane-1"}})

	doc := runJSONDoc(t, "--trace-root", root, "--store", store, "run", "run-doc-1")
	if len(doc) != 3 || doc["run"] == nil || doc["events"] == nil || doc["claims"] == nil {
		t.Fatalf("unexpected doc keys: %v", doc)
	}
	runObj := doc["run"].(map[string]any)
	if runObj["run_id"] != "run-doc-1" || runObj["proc_state"] != "ok" || runObj["log_state"] != "ok" {
		t.Fatalf("unexpected runObj: %v", runObj)
	}

	kinds := map[string]bool{}
	for _, rawEv := range doc["events"].([]any) {
		ev := rawEv.(map[string]any)
		kinds[ev["kind"].(string)] = true
		if _, ok := ev["payload"].(map[string]any); !ok {
			t.Fatalf("event payload is not object: %v", ev)
		}
		if !strings.HasPrefix(ev["obs_id"].(string), "obs-run-doc-1-") {
			t.Fatalf("unexpected obs_id: %v", ev["obs_id"])
		}
	}
	for _, k := range []string{"llm.turn", "tool.call", "proc.exec"} {
		if !kinds[k] {
			t.Fatalf("missing kind %q in %v", k, kinds)
		}
	}
	hasOwed := false
	for _, rawCl := range doc["claims"].([]any) {
		if rawCl.(map[string]any)["claim_kind"] == "owed_suite" {
			hasOwed = true
		}
	}
	if !hasOwed {
		t.Fatalf("expected owed_suite claim in %v", doc["claims"])
	}

	absent := runJSONDoc(t, "--trace-root", root, "--store", store, "run", "no-such-run")
	if absent["run"] != nil || len(absent["events"].([]any)) != 0 {
		t.Fatalf("unexpected absent doc: %v", absent)
	}
}
