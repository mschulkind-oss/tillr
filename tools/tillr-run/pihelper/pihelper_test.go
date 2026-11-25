package pihelper

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestLandingCallHeld(t *testing.T) {
	dir := t.TempDir()
	write := func(name, content string) string {
		p := filepath.Join(dir, name)
		if err := os.WriteFile(p, []byte(content), 0o644); err != nil {
			t.Fatal(err)
		}
		return p
	}

	heldPath := write("held.jsonl", strings.Join([]string{
		`{"type":"session","id":"s1"}`,
		`{"id":"m1","parentId":null,"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"land"}]}}`,
	}, "\n")+"\n")
	if !LandingCallHeld(heldPath, "call_1") {
		t.Fatal("expected call_1 to be held")
	}
	if RunLandingCallHeld([]string{heldPath, "call_1"}, io.Discard) != 0 {
		t.Fatal("expected exit 0 for held call")
	}
	if RunLandingCallHeld([]string{heldPath}, io.Discard) != 1 {
		t.Fatal("expected exit 1 for wrong arg count")
	}

	lastResultPath := write("last_result.jsonl", strings.Join([]string{
		`{"type":"session","id":"s1"}`,
		`{"id":"m1","parentId":null,"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"land"}]}}`,
		`{"id":"m2","parentId":"m1","type":"message","message":{"role":"toolResult","toolCallId":"call_1"}}`,
	}, "\n")+"\n")
	if !LandingCallHeld(lastResultPath, "call_1") {
		t.Fatal("expected call_1 with trailing toolResult to be held")
	}

	afterResultPath := write("after_result.jsonl", strings.Join([]string{
		`{"type":"session","id":"s1"}`,
		`{"id":"m1","parentId":null,"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"land"}]}}`,
		`{"id":"m2","parentId":"m1","type":"message","message":{"role":"toolResult","toolCallId":"call_1"}}`,
		`{"id":"m3","parentId":"m2","type":"message","message":{"role":"user","content":"next"}}`,
	}, "\n")+"\n")
	if LandingCallHeld(afterResultPath, "call_1") {
		t.Fatal("expected call_1 followed by user message not to be held")
	}

	compactionPath := write("compaction.jsonl", strings.Join([]string{
		`{"type":"session","id":"s1"}`,
		`{"id":"m1","parentId":null,"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_1","name":"land"}]}}`,
		`{"id":"c1","parentId":"m1","type":"compaction"}`,
	}, "\n")+"\n")
	if LandingCallHeld(compactionPath, "call_1") {
		t.Fatal("expected compaction to fail closed")
	}

	cyclePath := write("cycle.jsonl", strings.Join([]string{
		`{"type":"session","id":"s1"}`,
		`{"id":"m1","parentId":"m2","type":"message","message":{"role":"user"}}`,
		`{"id":"m2","parentId":"m1","type":"message","message":{"role":"user"}}`,
	}, "\n")+"\n")
	if LandingCallHeld(cyclePath, "call_1") {
		t.Fatal("expected cycle to fail closed")
	}

	dupPath := write("dup.jsonl", strings.Join([]string{
		`{"type":"session","id":"s1"}`,
		`{"id":"m1","parentId":null,"type":"message","message":{"role":"assistant","content":[{"type":"toolCall","id":"call_1"}]}}`,
		`{"id":"m1","parentId":"m1","type":"message","message":{"role":"user"}}`,
	}, "\n")+"\n")
	if LandingCallHeld(dupPath, "call_1") {
		t.Fatal("expected duplicate id to fail closed")
	}
}

func TestRunCompactHelpAndValidation(t *testing.T) {
	var out, errBuf bytes.Buffer
	if code := RunCompact([]string{"--help"}, &out, &errBuf); code != 0 {
		t.Fatalf("expected exit 0 for --help, got %d", code)
	}
	if !strings.Contains(out.String(), "--session-id") {
		t.Fatalf("expected --session-id in help output, got %q", out.String())
	}

	out.Reset()
	errBuf.Reset()
	if code := RunCompact([]string{"--backend", "claude", "--model", "m", "--session-id", "s"}, &out, &errBuf); code != 2 {
		t.Fatalf("expected exit 2 for non-pi backend, got %d", code)
	}

	out.Reset()
	errBuf.Reset()
	if code := RunCompact([]string{"--backend", "pi", "--model", "m", "--session-id", "s", "--timeout", "-1"}, &out, &errBuf); code != 2 {
		t.Fatalf("expected exit 2 for negative timeout, got %d", code)
	}
}

func TestRunCompactSuccessAndJournal(t *testing.T) {
	dir := t.TempDir()
	transcript := filepath.Join(dir, "transcript.jsonl")
	if err := os.WriteFile(transcript, []byte("hello\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	journalPath := filepath.Join(dir, "journal.json")
	if err := os.WriteFile(journalPath, []byte(`{"existing":true}`), 0o644); err != nil {
		t.Fatal(err)
	}
	harness := filepath.Join(dir, "pi-harness.sh")
	script := `#!/bin/sh
while IFS= read -r line; do
  case "$line" in
    *get_state*)
      printf '{"type":"response","id":"get_state","success":true,"data":{"sessionId":"sess-1","sessionFile":"%s"}}\n' "` + transcript + `"
      ;;
    *compact*)
      printf '{"type":"response","id":"compact","success":true,"data":{"tokensBefore":1000,"estimatedTokensAfter":250,"usage":{"totalTokens":1250,"cost":{"total":0.05}}}}\n'
      exit 0
      ;;
  esac
done
`
	if err := os.WriteFile(harness, []byte(script), 0o755); err != nil {
		t.Fatal(err)
	}

	t.Setenv("TILLR_COMPACTION_JOURNAL", journalPath)
	var out, errBuf bytes.Buffer
	code := RunCompact([]string{
		"--backend", "pi",
		"--model", "claude-sonnet",
		"--session-id", "sess-1",
		"--pi-dir", dir,
	}, &out, &errBuf)
	if code != 0 {
		t.Fatalf("RunCompact failed with %d: %s", code, errBuf.String())
	}
	var receipt map[string]any
	if err := json.Unmarshal(out.Bytes(), &receipt); err != nil {
		t.Fatalf("invalid receipt JSON %q: %v", out.String(), err)
	}
	if receipt["tokens_before"] != float64(1000) || receipt["tokens_after"] != float64(250) {
		t.Fatalf("unexpected receipt: %+v", receipt)
	}

	jBytes, err := os.ReadFile(journalPath)
	if err != nil {
		t.Fatal(err)
	}
	var journal map[string]any
	if err := json.Unmarshal(jBytes, &journal); err != nil {
		t.Fatal(err)
	}
	if journal["transcript"] != transcript || journal["offset"] != float64(6) {
		t.Fatalf("unexpected journal: %+v", journal)
	}
}

func TestJournalPathAndOwedWakes(t *testing.T) {
	if got := JournalPath(map[string]string{"PI_BACKGROUND_TASK_JOURNAL": "off"}); got != "" {
		t.Fatalf("expected empty for disabled journal, got %q", got)
	}
	if got := JournalPath(map[string]string{"XDG_STATE_HOME": "/tmp/xdg"}); got != "/tmp/xdg/pi-background-tasks/journal.sqlite" {
		t.Fatalf("unexpected JournalPath: %q", got)
	}

	dir := t.TempDir()
	dbPath := filepath.Join(dir, "journal.sqlite")
	db, err := sql.Open("sqlite", dbPath)
	if err != nil {
		t.Fatal(err)
	}
	schema := `
CREATE TABLE instances (instance_id TEXT, pi_session_id TEXT, process_id INTEGER, started_at INTEGER, disabled_at TEXT);
CREATE TABLE tasks (task_key TEXT, started_by_instance TEXT, completion_policy TEXT, ended_at INTEGER);
CREATE TABLE deliveries (delivery_key TEXT, task_key TEXT, wake_requested INTEGER);
CREATE TABLE delivery_events (delivery_key TEXT, event TEXT);
INSERT INTO instances VALUES ('i1', 's1', 4242, 1000, NULL);
INSERT INTO tasks VALUES ('t1', 'i1', 'wake', NULL);
INSERT INTO tasks VALUES ('t2', 'i1', 'wake', 2000);
INSERT INTO deliveries VALUES ('d2', 't2', 1);
INSERT INTO delivery_events VALUES ('d2', 'observed');
`
	if _, err := db.Exec(schema); err != nil {
		db.Close()
		t.Fatal(err)
	}
	db.Close()

	wakes, ok := OwedWakes(dbPath, 4242, 500)
	if !ok || len(wakes) != 1 || wakes[0] != "t1" {
		t.Fatalf("unexpected OwedWakes: wakes=%v ok=%v", wakes, ok)
	}
	ow := OwedWork(dbPath, 4242, 500)
	if ow == nil || !*ow {
		t.Fatal("expected OwedWork=true")
	}

	var out, errBuf bytes.Buffer
	code := RunStopAtGap([]string{"--owed-wakes", "4242", "500"}, []string{"PI_BACKGROUND_TASK_JOURNAL=" + dbPath}, &out, &errBuf)
	if code != 0 || strings.TrimSpace(out.String()) != `["t1"]` {
		t.Fatalf("unexpected --owed-wakes output: code=%d out=%q err=%q", code, out.String(), errBuf.String())
	}
}

func TestBuildAgentDirHelpers(t *testing.T) {
	in := "{\n  // line comment\n  \"url\": \"https://a//b/*c*/\",\n  /* block\n  comment */\n  \"k\": 1\n}"
	stripped := stripJSONC(in)
	var parsed map[string]any
	if err := json.Unmarshal([]byte(stripped), &parsed); err != nil {
		t.Fatalf("stripJSONC produced invalid JSON: %v\n%s", err, stripped)
	}
	if parsed["url"] != "https://a//b/*c*/" {
		t.Fatalf("stripJSONC corrupted string literal: %v", parsed["url"])
	}

	raw, err := canonicalJSON(map[string]any{"b": "<x>", "a": 1})
	if err != nil {
		t.Fatal(err)
	}
	if string(raw) != `{"a":1,"b":"<x>"}` {
		t.Fatalf("unexpected canonicalJSON: %s", raw)
	}

	cmd := "XDG_DATA_HOME=/tmp/pnpm-xdg/data && find packages -name '*.test.ts' -type f -delete"
	rewritten := rewriteBuildCmd(cmd, "/var/cache")
	if strings.Contains(rewritten, "/tmp/pnpm-xdg") || strings.Contains(rewritten, "find packages") || !strings.Contains(rewritten, "node -e") {
		t.Fatalf("unexpected rewriteBuildCmd: %s", rewritten)
	}
}
