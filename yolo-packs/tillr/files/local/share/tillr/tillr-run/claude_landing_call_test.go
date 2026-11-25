package main_test

import (
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func withTillrRunPath(stubBin string) string {
	return stubBin + string(os.PathListSeparator) + filepath.Dir(bin) + string(os.PathListSeparator) + os.Getenv("PATH")
}

// stageClaudeLane lays out what staging writes beside a brief for a call-suspends write node.
func stageClaudeLane(t *testing.T) (adapter, dir, worktree, durable, bin string) {
	t.Helper()
	adapter, err := filepath.Abs(filepath.Join("backends", "claude", "run-claude"))
	if err != nil {
		t.Fatal(err)
	}
	dir = scratch(t, "claude-landing")
	bin = filepath.Join(dir, "bin")
	worktree = filepath.Join(dir, "worktree")
	durable = filepath.Join(dir, "durable")
	for _, d := range []string{worktree, durable} {
		if err := os.MkdirAll(d, 0755); err != nil {
			t.Fatal(err)
		}
	}
	// A call-suspends brief has Finishing in place of Report shape and Stop conditions.
	var kept []string
	skip := false
	for _, line := range strings.Split(filledBrief(t, worktree), "\n") {
		if strings.HasPrefix(line, "## ") {
			skip = line == "## Report shape" || line == "## Stop conditions"
		}
		if strings.HasPrefix(line, "- **Target:**") {
			line = "- **Target:** " + worktree + " (lane worktree)"
		}
		if !skip {
			kept = append(kept, line)
		}
	}
	lines := append(kept, "## Finishing", "", "Call `land`.", "")
	for name, body := range map[string]string{
		"brief.md": strings.Join(lines, "\n"), "session-id": "12345678-1234-1234-1234-123456789abc\n",
		"base_sha": "basebase\n", "ending-capability": "call-suspends\n",
	} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	return
}

func runClaudeAdapter(t *testing.T, adapter, worktree, durable, stubBin, mode string, extra ...string) {
	t.Helper()
	cmd := exec.Command(adapter, "--backend", "claude", "--model", "fixture-model", "--effort", "medium")
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(), "PATH="+withTillrRunPath(stubBin), "YOLO_DURABLE_DIR="+durable,
		"TILLR_SESSION_MODE="+mode, "TILLR_RESUME_PROMPT_FILE=", "TILLR_READ_ONLY_GROUP=", "TILLR_PAUSE_REQUEST=")
	cmd.Env = append(cmd.Env, extra...)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("adapter: %v %s", err, out)
	}
}

func readFile(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	return string(raw)
}

func TestClaudeCallSuspendsRunGetsTheDeliveryServerAndHooks(t *testing.T) {
	adapter, dir, worktree, durable, bin := stageClaudeLane(t)
	writeExecutable(t, filepath.Join(bin, "claude"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$STUB_ARGV\"\ncat >/dev/null\n")
	argv := filepath.Join(dir, "argv")
	runClaudeAdapter(t, adapter, worktree, durable, bin, "new", "STUB_ARGV="+argv)
	args := readFile(t, argv)
	for _, want := range []string{"--mcp-config\n" + durable + "/claude-mcp.json\n", "--settings\n" + durable + "/claude-settings.json\n", "--strict-mcp-config\n"} {
		if !strings.Contains(args, want) {
			t.Fatalf("argv lacks %q:\n%s", want, args)
		}
	}
	settings := readFile(t, filepath.Join(durable, "claude-settings.json"))
	if !strings.Contains(settings, "PreToolUse") || !strings.Contains(settings, `"Stop"`) {
		t.Fatalf("settings carry no hooks: %s", settings)
	}
}

func TestClaudeFooterRunGetsNoDeliveryServer(t *testing.T) {
	adapter, dir, worktree, durable, bin := stageClaudeLane(t)
	full := strings.Replace(filledBrief(t, worktree), "- **Target:** "+worktree+" (", "- **Target:** "+worktree+" (lane worktree) (", 1)
	for name, body := range map[string]string{"ending-capability": "footer\n", "brief.md": full} {
		if err := os.WriteFile(filepath.Join(dir, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	writeExecutable(t, filepath.Join(bin, "claude"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$STUB_ARGV\"\ncat >/dev/null\n")
	argv := filepath.Join(dir, "argv")
	runClaudeAdapter(t, adapter, worktree, durable, bin, "new", "STUB_ARGV="+argv)
	if args := readFile(t, argv); strings.Contains(args, "--mcp-config") || strings.Contains(args, "--settings") {
		t.Fatalf("a footer run got delivery flags:\n%s", args)
	}
}

func TestClaudeAnswerIsDeliveredAsTheDeferredCallsResultWhenHeld(t *testing.T) {
	adapter, dir, worktree, durable, bin := stageClaudeLane(t)
	writeExecutable(t, filepath.Join(bin, "claude"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$STUB_ARGV\"\ncat > \"$STUB_INPUT\"\n")
	transcript := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"attachment","attachment":{"type":"hook_deferred_tool","toolUseID":"toolu_1"}}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"landing-answer.json": `{"call_id":"toolu_1","text":"ANSWER: refused"}`, "landing-transcript": transcript + "\n"} {
		if err := os.WriteFile(filepath.Join(durable, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	argv, input := filepath.Join(dir, "argv"), filepath.Join(dir, "input")
	runClaudeAdapter(t, adapter, worktree, durable, bin, "resume", "STUB_ARGV="+argv, "STUB_INPUT="+input)
	if got := readFile(t, input); got != "" {
		t.Fatalf("a replayed call takes no prompt, got %q", got)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(durable, "landing-delivery"))); got != "tool-result" {
		t.Fatalf("delivery = %q", got)
	}
	if !strings.Contains(readFile(t, argv), "--resume\n12345678-1234-1234-1234-123456789abc\n") {
		t.Fatalf("not a resume:\n%s", readFile(t, argv))
	}
}

func TestClaudeAnswerTravelsAsAMessageWhenNoDeferredCallIsHeld(t *testing.T) {
	adapter, dir, worktree, durable, bin := stageClaudeLane(t)
	writeExecutable(t, filepath.Join(bin, "claude"), "#!/bin/sh\ncat > \"$STUB_INPUT\"\n")
	transcript := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"user"}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"landing-answer.json": `{"call_id":"toolu_1","text":"ANSWER: refused"}`, "landing-transcript": transcript + "\n"} {
		if err := os.WriteFile(filepath.Join(durable, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	input := filepath.Join(dir, "input")
	runClaudeAdapter(t, adapter, worktree, durable, bin, "resume", "STUB_INPUT="+input)
	if got := readFile(t, input); got != "ANSWER: refused" {
		t.Fatalf("prompt = %q", got)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(durable, "landing-delivery"))); got != "message" {
		t.Fatalf("delivery = %q", got)
	}
}

func TestClaudeRefusedReplayFallsBackToTheAnswerAsAMessage(t *testing.T) {
	adapter, dir, worktree, durable, bin := stageClaudeLane(t)
	writeExecutable(t, filepath.Join(bin, "claude"), "#!/bin/sh\ncat > \"$STUB_CALL\"\nif [ ! -e \"$STUB_REFUSED\" ]; then : > \"$STUB_REFUSED\"; echo 'No deferred tool marker found in the resumed session' >&2; exit 1; fi\ncp \"$STUB_CALL\" \"$STUB_INPUT\"\n")
	transcript := filepath.Join(dir, "session.jsonl")
	if err := os.WriteFile(transcript, []byte(`{"type":"attachment","attachment":{"type":"hook_deferred_tool","toolUseID":"toolu_1"}}`+"\n"), 0644); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"landing-answer.json": `{"call_id":"toolu_1","text":"ANSWER: refused"}`, "landing-transcript": transcript + "\n"} {
		if err := os.WriteFile(filepath.Join(durable, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	input := filepath.Join(dir, "input")
	runClaudeAdapter(t, adapter, worktree, durable, bin, "resume", "STUB_INPUT="+input, "STUB_CALL="+filepath.Join(dir, "call"), "STUB_REFUSED="+filepath.Join(dir, "refused"))
	if got := readFile(t, input); got != "ANSWER: refused" {
		t.Fatalf("retry prompt = %q", got)
	}
	if got := strings.TrimSpace(readFile(t, filepath.Join(durable, "landing-delivery"))); got != "message" {
		t.Fatalf("delivery = %q", got)
	}
}

func TestClaudeCallSuspendsMergesSettingsUnsetsDisableAndHandlesResolvedAndMissingTranscripts(t *testing.T) {
	adapter, dir, worktree, durable, bin := stageClaudeLane(t)
	writeExecutable(t, filepath.Join(bin, "claude"), "#!/bin/sh\nprintf '%s\\n' \"$@\" > \"$STUB_ARGV\"\nenv > \"$STUB_ENV\"\ncat > \"$STUB_INPUT\"\n")
	policySettings := filepath.Join(dir, "policy.json")
	if err := os.WriteFile(policySettings, []byte(`{"env":{"CUSTOM_FLAG":"yes"}}`), 0644); err != nil {
		t.Fatal(err)
	}
	argv, envOut, input := filepath.Join(dir, "argv"), filepath.Join(dir, "env"), filepath.Join(dir, "input")
	cmd := exec.Command(adapter, "--backend", "claude", "--model", "fixture-model", "--effort", "medium", "--account", "subscription", "--", "--tools", "Read,Edit,Bash", "--settings", policySettings)
	cmd.Dir = worktree
	cmd.Env = append(os.Environ(), "PATH="+withTillrRunPath(bin), "YOLO_DURABLE_DIR="+durable,
		"TILLR_SESSION_MODE=new", "TILLR_RESUME_PROMPT_FILE=", "TILLR_READ_ONLY_GROUP=", "TILLR_PAUSE_REQUEST=",
		"CLAUDE_CODE_DISABLE_DEFERRED_TOOL_RESUME=1", "STUB_ARGV="+argv, "STUB_ENV="+envOut, "STUB_INPUT="+input)
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("adapter: %v %s", err, out)
	}
	if got := strings.Count(readFile(t, argv), "--settings\n"); got != 1 {
		t.Fatalf("expected exactly one --settings in argv, got %d:\n%s", got, readFile(t, argv))
	}
	settings := readFile(t, filepath.Join(durable, "claude-settings.json"))
	for _, want := range []string{`"CLAUDE_CODE_USE_BEDROCK":""`, `"CUSTOM_FLAG":"yes"`, `"PreToolUse"`, `"Stop"`} {
		if !strings.Contains(settings, want) {
			t.Fatalf("merged settings lack %q: %s", want, settings)
		}
	}
	if strings.Contains(readFile(t, envOut), "CLAUDE_CODE_DISABLE_DEFERRED_TOOL_RESUME=") {
		t.Fatalf("CLAUDE_CODE_DISABLE_DEFERRED_TOOL_RESUME leaked into call-suspends env")
	}

	// Already-resolved call (held == 3) keeps tool-result and sends the continuation prompt.
	transcript := filepath.Join(dir, "session.jsonl")
	resolved := `{"type":"attachment","attachment":{"type":"hook_deferred_tool","toolUseID":"toolu_1"}}` + "\n" +
		`{"type":"user","message":{"content":[{"type":"tool_result","tool_use_id":"toolu_1","content":"ANSWER: refused"}]}}` + "\n"
	if err := os.WriteFile(transcript, []byte(resolved), 0644); err != nil {
		t.Fatal(err)
	}
	for name, body := range map[string]string{"landing-answer.json": `{"call_id":"toolu_1","text":"ANSWER: refused"}`, "landing-transcript": transcript + "\n"} {
		if err := os.WriteFile(filepath.Join(durable, name), []byte(body), 0644); err != nil {
			t.Fatal(err)
		}
	}
	runClaudeAdapter(t, adapter, worktree, durable, bin, "resume", "STUB_ARGV="+argv, "STUB_ENV="+envOut, "STUB_INPUT="+input)
	if got := strings.TrimSpace(readFile(t, filepath.Join(durable, "landing-delivery"))); got != "tool-result" {
		t.Fatalf("resolved delivery = %q, want tool-result", got)
	}
	if got := strings.TrimSpace(readFile(t, input)); got != "Continue from where the session stopped." {
		t.Fatalf("resolved prompt = %q", got)
	}

	// Missing transcript exits non-zero so the drain falls back to fresh-session.
	if err := os.Remove(transcript); err != nil {
		t.Fatal(err)
	}
	missing := exec.Command(adapter, "--backend", "claude", "--model", "fixture-model", "--effort", "medium")
	missing.Dir = worktree
	missing.Env = append(os.Environ(), "PATH="+withTillrRunPath(bin), "YOLO_DURABLE_DIR="+durable,
		"TILLR_SESSION_MODE=resume", "TILLR_RESUME_PROMPT_FILE=", "TILLR_READ_ONLY_GROUP=", "TILLR_PAUSE_REQUEST=")
	if out, err := missing.CombinedOutput(); err == nil {
		t.Fatalf("expected non-zero exit on missing transcript, got 0: %s", out)
	}

	// extract-report renders a deferred result stream cleanly with tokens and cost preserved.
	extractor, err := filepath.Abs(filepath.Join("backends", "claude", "extract-report"))
	if err != nil {
		t.Fatal(err)
	}
	deferredStream := filepath.Join(dir, "deferred.jsonl")
	streamBody := `{"type":"system","subtype":"init","session_id":"12345678-1234-1234-1234-123456789abc","model":"fixture-model"}` + "\n" +
		`{"type":"result","subtype":"success","is_error":false,"stop_reason":"tool_deferred","result":"","session_id":"12345678-1234-1234-1234-123456789abc","total_cost_usd":0.0125,"usage":{"input_tokens":100,"output_tokens":25}}` + "\n"
	if err := os.WriteFile(deferredStream, []byte(streamBody), 0644); err != nil {
		t.Fatal(err)
	}
	reportOut, err := exec.Command(extractor, "--backend", "claude", "--model", "fixture-model", "--effort", "medium", "--exit-code", "0", deferredStream).CombinedOutput()
	if exit, ok := err.(*exec.ExitError); !ok || exit.ExitCode() != 4 {
		t.Fatalf("deferred stream should exit 4 (incomplete footer, settled by offer), got %v:\n%s", err, reportOut)
	}
	for _, want := range []string{"tokens: 125", "cost usd: 0.012500", "[delivery call deferred; waiting for the gate's answer]"} {
		if !strings.Contains(string(reportOut), want) {
			t.Fatalf("deferred report lacks %q:\n%s", want, reportOut)
		}
	}
}
