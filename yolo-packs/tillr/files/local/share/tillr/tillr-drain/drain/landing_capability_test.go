package drain

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/store"
)

func TestPiEndingCapabilityProbe(t *testing.T) {
	for _, tc := range []struct{ name, script, want string }{
		{"supported", "printf '[\"parked-tool-calls\"]\\n'", "call-suspends"},
		{"unsupported", "printf '[\"other\"]\\n'", "footer"},
		{"rejects", "exit 2", "footer"},
		{"malformed", "echo parked-tool-calls", "footer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			pi := filepath.Join(dir, "pi")
			if err := writeForkSafe(pi, []byte("#!/bin/sh\n"+tc.script+"\n"), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			got, path, _ := probePiEndingCapability(context.Background())
			if got != tc.want || path != pi {
				t.Fatalf("got %q (%q), want %q (%q)", got, path, tc.want, pi)
			}
		})
	}
}

func TestPiEndingCapabilityProbeBoundsSleeperAndChildHoldingOutput(t *testing.T) {
	oldTimeout, oldDelay := piFeaturesProbeTimeout, piFeaturesWaitDelay
	piFeaturesProbeTimeout = 100 * time.Millisecond
	piFeaturesWaitDelay = 100 * time.Millisecond
	t.Cleanup(func() {
		piFeaturesProbeTimeout, piFeaturesWaitDelay = oldTimeout, oldDelay
	})

	dir := t.TempDir()
	pi := filepath.Join(dir, "pi")
	if err := writeForkSafe(pi, []byte("#!/bin/sh\nsleep 30 &\nsleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", dir)

	done := make(chan struct{})
	var (
		got      string
		path     string
		features []string
	)
	go func() {
		defer close(done)
		got, path, features = probePiEndingCapability(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("probe held past bound; child holding stdout was not bounded")
	}
	if got != "footer" || path != pi || features != nil {
		t.Fatalf("got %q (%q) features=%v, want footer (%q) nil", got, path, features, pi)
	}
}

func TestClaudeEndingCapabilityProbe(t *testing.T) {
	sysPath := os.Getenv("PATH")
	for _, tc := range []struct{ name, script, env, want string }{
		{"knows deferred resume", "echo 'No deferred tool marker found in the resumed session' >&2; exit 1", "", "call-suspends"},
		{"predates it", "echo 'No conversation found to continue' >&2; exit 1", "", "footer"},
		{"switched off", "echo 'No deferred tool marker found in the resumed session' >&2; exit 1", "1", "footer"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			dir := t.TempDir()
			if err := writeForkSafe(filepath.Join(dir, "claude"), []byte("#!/bin/sh\n"+tc.script+"\n"), 0755); err != nil {
				t.Fatal(err)
			}
			t.Setenv("PATH", dir)
			t.Setenv("CLAUDE_CODE_DISABLE_DEFERRED_TOOL_RESUME", tc.env)
			if got, _ := probeClaudeEndingCapability(context.Background()); got != tc.want {
				t.Fatalf("got %q, want %q", got, tc.want)
			}
		})
	}
	t.Setenv("PATH", t.TempDir())
	if got, path := probeClaudeEndingCapability(context.Background()); got != "footer" || path != "" {
		t.Fatalf("absent binary: got %q (%q), want footer", got, path)
	}

	// A hung binary or child holding stderr is bounded by the probe timeout.
	oldTimeout, oldDelay := piFeaturesProbeTimeout, piFeaturesWaitDelay
	piFeaturesProbeTimeout = 100 * time.Millisecond
	piFeaturesWaitDelay = 100 * time.Millisecond
	t.Cleanup(func() {
		piFeaturesProbeTimeout, piFeaturesWaitDelay = oldTimeout, oldDelay
	})
	hungDir := t.TempDir()
	claudeBin := filepath.Join(hungDir, "claude")
	if err := writeForkSafe(claudeBin, []byte("#!/bin/sh\necho 'No deferred tool marker found in the resumed session' >&2\nsleep 30 &\nsleep 30\n"), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", hungDir+string(os.PathListSeparator)+sysPath)
	t.Setenv("CLAUDE_CODE_DISABLE_DEFERRED_TOOL_RESUME", "")
	done := make(chan struct{})
	var got, path string
	go func() {
		defer close(done)
		got, path = probeClaudeEndingCapability(context.Background())
	}()
	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("claude probe held past bound")
	}
	if got != "footer" || path != claudeBin {
		t.Fatalf("hung probe: got %q (%q), want footer (%q)", got, path, claudeBin)
	}
}

func TestClaudeLandingResultPersisted(t *testing.T) {
	answer := landingAnswer{CallID: "toolu_1", Kind: "refused", Text: "ANSWER: refused\n"}
	row := func(session, id string, isError bool, content string) string {
		return `{"type":"user","sessionId":"` + session + `","message":{"role":"user","content":[{"type":"tool_result","tool_use_id":"` + id + `","is_error":` + map[bool]string{true: "true", false: "false"}[isError] + `,"content":` + content + `}]}}` + "\n"
	}
	deferred := `{"type":"attachment","sessionId":"s","attachment":{"type":"hook_deferred_tool","toolUseID":"toolu_1"}}` + "\n"
	for _, tc := range []struct {
		name, transcript string
		want             bool
	}{
		{"string content, newline trimmed", deferred + row("s", "toolu_1", true, `"ANSWER: refused"`), true},
		{"text blocks", deferred + row("s", "toolu_1", true, `[{"type":"text","text":"ANSWER: refused"}]`), true},
		{"still only deferred", deferred, false},
		{"other call", row("s", "toolu_2", true, `"ANSWER: refused"`), false},
		{"other session", row("other", "toolu_1", true, `"ANSWER: refused"`), false},
		{"not an error result", row("s", "toolu_1", false, `"ANSWER: refused"`), false},
		{"different text", row("s", "toolu_1", true, `"something else"`), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			if got := landingResultPersisted([]byte(tc.transcript), "s", answer); got != tc.want {
				t.Fatalf("persisted = %t; want %t", got, tc.want)
			}
		})
	}
}

// End-to-end Claude delivery call: a worker delivers through mcp__tillr__land, receives
// the gate's refusal (with commit fc5cd652a's failing-case answer text) as that call's
// own tool_result on promptless --resume, fixes and delivers again, and on gate acceptance
// lands and closes at proven without reopening the session.
type claudeLandingCallSteps struct{ *designRepairSteps }

func (s *claudeLandingCallSteps) Land(ctx context.Context, c Candidate) (LandOutcome, error) {
	before := runGit(s.t, s.root, "rev-parse", TrunkRef)
	out, err := s.designRepairSteps.Land(ctx, c)
	if err != nil {
		return out, err
	}
	after := runGit(s.t, s.root, "rev-parse", TrunkRef)
	db, err := store.Open(ctx, s.store, true)
	if err != nil {
		return out, err
	}
	defer db.Close()
	err = db.WriteTx(ctx, func(tx *store.Tx) error {
		var seq int64
		if err := tx.QueryRow(`SELECT COALESCE(MAX(seq),0) FROM events`).Scan(&seq); err != nil {
			return err
		}
		_, err := tx.Exec(`INSERT INTO landings(landing_id,item_id,run_id,verdict_id,result,base_sha,head_sha,trunk_before,trunk_after,event_seq) VALUES(?,?,?,'fixture','landed',?,?,?,?,?)`,
			"fixture-"+c.RunID, c.ItemID, c.RunID, c.BaseSHA, c.HeadSHA, before, after, seq)
		return err
	})
	return out, err
}

func TestClaudeLandingCallDeliversGateRefusalAndRecordsAcceptance(t *testing.T) {
	ctx := context.Background()
	adapter, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "claude", "run-claude"))
	if err != nil {
		t.Fatal(err)
	}
	bin := t.TempDir()
	transcript := filepath.Join(bin, "claude-session.jsonl")
	// Stand-in `claude` exercising the real `run-claude` flags, `claude-settings.json`
	// PreToolUse hook, and `claude-mcp.json` stdio MCP server.
	if err := writeForkSafe(filepath.Join(bin, "claude"), []byte(`#!/usr/bin/env python3
import json, os, pathlib, shlex, subprocess, sys
argv = sys.argv[1:]
if "--continue" in argv:
    print("No deferred tool marker found in the resumed session", file=sys.stderr)
    sys.exit(1)
stdin_bytes = sys.stdin.buffer.read()
durable = pathlib.Path(os.environ["YOLO_DURABLE_DIR"])
(durable / "received-stdin").write_bytes(stdin_bytes)
mcp_cfg = json.loads(pathlib.Path(argv[argv.index("--mcp-config") + 1]).read_text())["mcpServers"]["tillr"]
settings = json.loads(pathlib.Path(argv[argv.index("--settings") + 1]).read_text())
pre_cmd = settings["hooks"]["PreToolUse"][-1]["hooks"][0]["command"]
transcript = pathlib.Path(os.environ["TEST_CLAUDE_TRANSCRIPT"])
cwd = os.getcwd()

def run_pre(call_id):
    ev = {"session_id": session, "transcript_path": str(transcript), "cwd": cwd,
          "tool_name": "mcp__tillr__land", "tool_use_id": call_id, "tool_input": {}}
    out = subprocess.run(shlex.split(pre_cmd), input=json.dumps(ev), text=True, capture_output=True, check=True)
    return json.loads(out.stdout)["hookSpecificOutput"]

if "--session-id" in argv:
    session = argv[argv.index("--session-id") + 1]
    assert b"## Finishing" in stdin_bytes, stdin_bytes
    target = pathlib.Path(cwd, "tools/item-a/candidate.txt")
    target.parent.mkdir(parents=True, exist_ok=True)
    target.write_text("attempt-1\n")
    subprocess.run(["git", "add", "tools/item-a/candidate.txt"], cwd=cwd, check=True)
    subprocess.run(["git", "commit", "-qm", "attempt 1"], cwd=cwd, check=True)
    decision = run_pre("toolu_1")
    assert decision["permissionDecision"] == "defer", decision
    with transcript.open("a", encoding="utf-8") as out:
        out.write(json.dumps({"type": "attachment", "sessionId": session, "attachment": {
            "type": "hook_deferred_tool", "toolUseID": "toolu_1", "toolName": "mcp__tillr__land",
            "toolInput": {}}}) + "\n")
    call_id = "toolu_1"
else:
    session = argv[argv.index("--resume") + 1]
    assert stdin_bytes == b"", f"promptless resume received stdin: {stdin_bytes!r}"
    decision = run_pre("toolu_1")
    assert decision["permissionDecision"] == "allow", decision
    reqs = [
        {"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": {"protocolVersion": "2025-03-26"}},
        {"jsonrpc": "2.0", "id": 2, "method": "tools/call", "params": {
            "name": "land", "arguments": {}, "_meta": {"claudecode/toolUseId": "toolu_1"}}},
    ]
    mcp = subprocess.run([mcp_cfg["command"], *mcp_cfg["args"]],
                         input="".join(json.dumps(r) + "\n" for r in reqs), text=True, capture_output=True, check=True)
    replies = [json.loads(line) for line in mcp.stdout.splitlines()]
    tool_res = replies[1]["result"]
    assert tool_res["isError"] is True, tool_res
    # Claude Code 2.1.329 trims a trailing newline when persisting an MCP error tool_result.
    saved_text = tool_res["content"][0]["text"].rstrip("\n")
    (durable / "delivered-tool-result").write_text(saved_text)
    with transcript.open("a", encoding="utf-8") as out:
        out.write(json.dumps({"type": "user", "sessionId": session, "message": {"role": "user", "content": [{
            "type": "tool_result", "tool_use_id": "toolu_1", "is_error": True, "content": saved_text}]}}) + "\n")
    target = pathlib.Path(cwd, "tools/item-a/candidate.txt")
    target.write_text("attempt-2\n")
    subprocess.run(["git", "add", "tools/item-a/candidate.txt"], cwd=cwd, check=True)
    subprocess.run(["git", "commit", "-qm", "attempt 2"], cwd=cwd, check=True)
    decision2 = run_pre("toolu_2")
    assert decision2["permissionDecision"] == "defer", decision2
    with transcript.open("a", encoding="utf-8") as out:
        out.write(json.dumps({"type": "attachment", "sessionId": session, "attachment": {
            "type": "hook_deferred_tool", "toolUseID": "toolu_2", "toolName": "mcp__tillr__land",
            "toolInput": {}}}) + "\n")
    call_id = "toolu_2"

print(json.dumps({"type": "system", "subtype": "init", "session_id": session, "model": "fixture-model"}))
print(json.dumps({"type": "result", "subtype": "success", "is_error": False, "stop_reason": "tool_deferred",
                  "result": "", "session_id": session, "total_cost_usd": 0.01,
                  "deferred_tool_use": {"id": call_id, "name": "mcp__tillr__land"},
                  "usage": {"input_tokens": 50, "output_tokens": 10}}))
`), 0755); err != nil {
		t.Fatal(err)
	}
	t.Setenv("PATH", bin+string(os.PathListSeparator)+os.Getenv("PATH"))
	t.Setenv("TEST_CLAUDE_TRANSCRIPT", transcript)
	t.Setenv("CLAUDE_CODE_DISABLE_DEFERRED_TOOL_RESUME", "")

	f, l := passFixture(t, "item-a")
	fixtureWrite(t, f, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET body_ref='' WHERE item_id='item-a'`)
		return err
	})
	l.cfg.Lanes = []string{"lane-1"}
	l.cfg.Policy.DefaultBackend = "claude"
	l.cfg.Policy.Backends = map[string]Backend{"claude": {Adapter: adapter, Model: "fixture-model", Effort: "medium"}}
	steps := &claudeLandingCallSteps{&designRepairSteps{
		returnedLandingSteps: &returnedLandingSteps{
			continuationSteps: &continuationSteps{t: t, store: f.storePath, laneRoot: f.laneRoot, at: l.cfg.Now()},
			root:              f.root,
		},
		failures: 1,
		output:   triageFixtureBlock,
	}}
	l.cfg.Steps = steps
	l.cfg.Stage = &LaneStager{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"), Now: l.cfg.Now}

	if err := l.recordEndingCapabilities(ctx); err != nil {
		t.Fatal(err)
	}
	// Turn 1 is refused at the gate. The resumed turn delivers the refusal and
	// lands toolu_2. Stop at landing, which can happen on worker completion.
	for i := 0; i < 4 && steps.lands == 0; i++ {
		onePassNoStop(t, l)
	}
	if len(steps.requests) != 2 || steps.gates != 2 || steps.lands != 1 {
		t.Fatalf("unexpected lifecycle: runs=%d gates=%d lands=%d", len(steps.requests), steps.gates, steps.lands)
	}
	if !steps.requests[1].SessionTurn || steps.sessions[0] != steps.sessions[1] {
		t.Fatalf("turn 2 did not resume the same Claude session: req=%+v sessions=%v", steps.requests[1], steps.sessions)
	}
	turn2Durable := run.DurableDir(l.laneDirPath(steps.requests[1].LaneID), steps.requests[1].RunID)
	deliveredBytes, err := os.ReadFile(filepath.Join(turn2Durable, "delivered-tool-result"))
	if err != nil {
		t.Fatal(err)
	}
	delivered := string(deliveredBytes)
	for _, want := range []string{
		"ANSWER: refused\nStage: gate\n",
		failureHeading,
		"check go-drain",
		"case: TestGatheredWriteFairness",
		"at: tools/tillr-drain/drain/gathered_fairness_test.go:113",
		"assertion: a check asserts elapsed time",
		"reproduce only this case: " + triageFixtureOneCase,
		"not the whole suite",
		"Turn: " + steps.requests[1].RunID,
	} {
		if !strings.Contains(delivered, want) {
			t.Errorf("Claude tool_result lacks %q:\n%s", want, delivered)
		}
	}

	// Advance green pointer to prove the landing: toolu_2 closes silently as proven with no third run.
	tip := runGit(t, f.root, "rev-parse", "HEAD")
	runGit(t, f.root, "update-ref", GreenRef, tip)
	if err := l.reconcileLandingCalls(ctx); err != nil {
		t.Fatal(err)
	}
	if len(steps.requests) != 2 {
		t.Fatalf("acceptance reopened session: runs=%d", len(steps.requests))
	}
	fixtureRead(t, f, func(r *store.Reader) error {
		var form, closedReason string
		if err := r.QueryRow(`SELECT json_extract(payload,'$.form') FROM events WHERE kind=? AND object_id='toolu_1'`, callDelivered).Scan(&form); err != nil || form != "tool-result" {
			t.Fatalf("toolu_1 delivery form=%q err=%v", form, err)
		}
		var landed int
		if err := r.QueryRow(`SELECT count(*) FROM events WHERE kind=? AND object_id='toolu_2'`, callLanded).Scan(&landed); err != nil || landed != 1 {
			t.Fatalf("toolu_2 landed=%d err=%v", landed, err)
		}
		if err := r.QueryRow(`SELECT json_extract(payload,'$.reason') FROM events WHERE kind=? AND object_id='toolu_2'`, callClosed).Scan(&closedReason); err != nil || closedReason != "proven" {
			t.Fatalf("toolu_2 closed=%q err=%v", closedReason, err)
		}
		return nil
	})
}
