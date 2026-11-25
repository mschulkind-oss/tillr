package claude

import (
	"bytes"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

type landingFixture struct {
	t          *testing.T
	root       string
	repo       string
	durable    string
	transcript string
	base       string
}

func newLandingFixture(t *testing.T) *landingFixture {
	t.Helper()
	t.Setenv("BOARD_DRAIN_BRIEF", "")
	t.Setenv("TILLR_PAUSE_REQUEST", "")
	root := t.TempDir()
	repo := filepath.Join(root, "repo")
	durable := filepath.Join(root, "durable")
	if err := os.MkdirAll(repo, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(durable, 0755); err != nil {
		t.Fatal(err)
	}
	transcript := filepath.Join(root, "session.jsonl")
	if err := os.WriteFile(transcript, nil, 0644); err != nil {
		t.Fatal(err)
	}
	f := &landingFixture{
		t:          t,
		root:       root,
		repo:       repo,
		durable:    durable,
		transcript: transcript,
	}
	f.sh("git", "init", "-q")
	if err := os.WriteFile(filepath.Join(repo, "a"), []byte("1"), 0644); err != nil {
		t.Fatal(err)
	}
	f.sh("git", "add", "a")
	f.sh("git", "commit", "-qm", "base")
	f.base = f.head()
	return f
}

func (f *landingFixture) sh(args ...string) {
	f.t.Helper()
	cmd := exec.Command(args[0], args[1:]...)
	cmd.Dir = f.repo
	cmd.Env = append(os.Environ(),
		"GIT_AUTHOR_NAME=t",
		"GIT_AUTHOR_EMAIL=t@t",
		"GIT_COMMITTER_NAME=t",
		"GIT_COMMITTER_EMAIL=t@t",
	)
	if out, err := cmd.CombinedOutput(); err != nil {
		f.t.Fatalf("%v: %v\n%s", args, err, out)
	}
}

func (f *landingFixture) head() string {
	f.t.Helper()
	out, err := gitRun(f.repo, "rev-parse", "HEAD")
	if err != nil {
		f.t.Fatalf("rev-parse HEAD: %v", err)
	}
	return strings.TrimSpace(out)
}

func (f *landingFixture) commit(text string) string {
	f.t.Helper()
	if err := os.WriteFile(filepath.Join(f.repo, "a"), []byte(text), 0644); err != nil {
		f.t.Fatal(err)
	}
	f.sh("git", "commit", "-qam", text)
	return f.head()
}

func (f *landingFixture) call(command string, event any, extra ...string) (int, string, string) {
	f.t.Helper()
	var stdin bytes.Buffer
	if event != nil {
		raw, err := json.Marshal(event)
		if err != nil {
			f.t.Fatal(err)
		}
		stdin.Write(raw)
	}
	args := append([]string{command, "--durable", f.durable, "--base", f.base}, extra...)
	var stdout, stderr bytes.Buffer
	code := RunLanding(args, &stdin, &stdout, &stderr)
	return code, stdout.String(), stderr.String()
}

func (f *landingFixture) pre(tool, callID string, toolInput map[string]any, extra ...string) (string, string) {
	f.t.Helper()
	if tool == "" {
		tool = landTool
	}
	if callID == "" {
		callID = "toolu_1"
	}
	if toolInput == nil {
		toolInput = map[string]any{}
	}
	event := map[string]any{
		"tool_name":       tool,
		"tool_use_id":     callID,
		"tool_input":      toolInput,
		"cwd":             f.repo,
		"transcript_path": f.transcript,
	}
	code, stdout, stderr := f.call("pre-tool-use", event, extra...)
	if code != 0 {
		f.t.Fatalf("pre-tool-use exit %d: %s", code, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		return "", ""
	}
	var body struct {
		HookSpecificOutput struct {
			PermissionDecision       string `json:"permissionDecision"`
			PermissionDecisionReason string `json:"permissionDecisionReason"`
		} `json:"hookSpecificOutput"`
	}
	if err := json.Unmarshal([]byte(stdout), &body); err != nil {
		f.t.Fatalf("decode pre-tool-use stdout %q: %v", stdout, err)
	}
	return body.HookSpecificOutput.PermissionDecision, body.HookSpecificOutput.PermissionDecisionReason
}

func (f *landingFixture) offer() map[string]any {
	f.t.Helper()
	raw, err := os.ReadFile(filepath.Join(f.durable, "landing-offer.json"))
	if err != nil {
		f.t.Fatalf("read landing-offer.json: %v", err)
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		f.t.Fatalf("decode landing-offer.json: %v", err)
	}
	return out
}

func (f *landingFixture) serve(messages ...map[string]any) []map[string]any {
	f.t.Helper()
	var stdin bytes.Buffer
	for _, m := range messages {
		raw, err := json.Marshal(m)
		if err != nil {
			f.t.Fatal(err)
		}
		stdin.Write(raw)
		stdin.WriteByte('\n')
	}
	var stdout, stderr bytes.Buffer
	code := RunLanding([]string{"mcp", "--durable", f.durable}, &stdin, &stdout, &stderr)
	if code != 0 {
		f.t.Fatalf("mcp exit %d: %s", code, stderr.String())
	}
	var replies []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(stdout.String()), "\n") {
		if line == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			f.t.Fatalf("decode mcp reply %q: %v", line, err)
		}
		replies = append(replies, m)
	}
	return replies
}

func (f *landingFixture) stop() map[string]any {
	f.t.Helper()
	event := map[string]any{"cwd": f.repo, "transcript_path": f.transcript}
	code, stdout, stderr := f.call("stop", event)
	if code != 0 {
		f.t.Fatalf("stop exit %d: %s", code, stderr)
	}
	if strings.TrimSpace(stdout) == "" {
		return nil
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(stdout), &out); err != nil {
		f.t.Fatalf("decode stop stdout %q: %v", stdout, err)
	}
	return out
}

func TestACommittedCleanOfferIsDeferredAndRecordedAsPiRecordsIt(t *testing.T) {
	f := newLandingFixture(t)
	head := f.commit("2")
	decision, reason := f.pre("", "", nil)
	if decision != "defer" || reason != "" {
		t.Fatalf("got (%q, %q), want (defer, '')", decision, reason)
	}
	want := map[string]any{"call_id": "toolu_1", "commit": head, "base": f.base, "transcript": f.transcript}
	if got := f.offer(); !reflect.DeepEqual(got, want) {
		t.Fatalf("offer = %#v, want %#v", got, want)
	}
}

func TestExtractorUsesOnlyTheCommitOfferAsTheSuccessEnding(t *testing.T) {
	f := newLandingFixture(t)
	f.commit("2")
	decision, reason := f.pre("", "", nil)
	if decision != "defer" || reason != "" {
		t.Fatalf("got (%q, %q), want (defer, '')", decision, reason)
	}
	offerPath := filepath.Join(f.durable, "landing-offer.json")
	extractor, err := filepath.Abs(filepath.Join("..", "backends", "claude", "extract-report"))
	if err != nil {
		t.Fatal(err)
	}
	for _, legacy := range []bool{false, true} {
		if legacy {
			m := f.offer()
			m["summary"] = "claimed test results"
			raw, _ := json.Marshal(m)
			if err := os.WriteFile(offerPath, raw, 0644); err != nil {
				t.Fatal(err)
			}
		}
		cmd := exec.Command(extractor, "--backend", "claude", "--landing-offer", offerPath)
		cmd.Stdin = strings.NewReader("")
		out, err := cmd.CombinedOutput()
		if err != nil {
			t.Fatalf("extract-report (legacy=%v): %v\n%s", legacy, err, out)
		}
		text := string(out)
		if !strings.Contains(text, "complete: yes\nresult: done\n") {
			t.Fatalf("legacy=%v: missing complete/done in:\n%s", legacy, text)
		}
		if strings.Contains(text, "claimed test results") {
			t.Fatalf("legacy=%v: leaked summary in:\n%s", legacy, text)
		}
	}
}

func TestChecksAWorkerCanFixReturnAtOnceAndWriteNoOffer(t *testing.T) {
	f := newLandingFixture(t)
	decision, reason := f.pre("", "", nil)
	if decision != "deny" || !strings.Contains(reason, "HEAD is still the staged base") {
		t.Fatalf("base check: got (%q, %q)", decision, reason)
	}
	f.commit("2")
	if err := os.WriteFile(filepath.Join(f.repo, "dirty"), []byte("x"), 0644); err != nil {
		t.Fatal(err)
	}
	decision, reason = f.pre("", "", nil)
	if decision != "deny" || !strings.Contains(reason, "dirty") {
		t.Fatalf("dirty check: got (%q, %q)", decision, reason)
	}
	_ = os.Remove(filepath.Join(f.repo, "dirty"))
	f.sh("git", "mv", "a", "renamed_a")
	decision, reason = f.pre("", "", nil)
	if decision != "deny" || !strings.Contains(reason, "renamed_a") || !strings.Contains(reason, "a") {
		t.Fatalf("rename check: got (%q, %q)", decision, reason)
	}
	f.sh("git", "reset", "-q", "--hard", "HEAD")
	if _, err := os.Stat(filepath.Join(f.durable, "landing-offer.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no landing-offer.json, got err=%v", err)
	}
}

func TestUnformattedGoFilesAreDeniedWithFileNamesUntilFormatted(t *testing.T) {
	f := newLandingFixture(t)
	repoRoot, err := filepath.Abs(filepath.Join("..", "..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	justfileBytes, err := os.ReadFile(filepath.Join(repoRoot, ".justfile"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, ".justfile"), justfileBytes, 0644); err != nil {
		t.Fatal(err)
	}
	checksDir := filepath.Join(f.repo, "tools", "gate", "checks")
	if err := os.MkdirAll(checksDir, 0755); err != nil {
		t.Fatal(err)
	}
	formatJSON, err := os.ReadFile(filepath.Join(repoRoot, "tools", "gate", "checks", "format.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(checksDir, "format.json"), formatJSON, 0644); err != nil {
		t.Fatal(err)
	}
	exampleDir := filepath.Join(f.repo, "tools", "example")
	if err := os.MkdirAll(exampleDir, 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(exampleDir, "unformatted.go"), []byte("package example\nfunc bad(){println(1)}\n"), 0644); err != nil {
		t.Fatal(err)
	}
	f.sh("git", "add", ".justfile", "tools/gate/checks/format.json", "tools/example/unformatted.go")
	f.sh("git", "commit", "-qm", "unformatted")

	decision, reason := f.pre("", "", nil)
	if decision != "deny" || !strings.Contains(reason, "tools/example/unformatted.go") {
		t.Fatalf("unformatted check: got (%q, %q)", decision, reason)
	}
	if _, err := os.Stat(filepath.Join(f.durable, "landing-offer.json")); !os.IsNotExist(err) {
		t.Fatalf("expected no landing-offer.json, got err=%v", err)
	}
	f.sh("just", "format")
	f.sh("git", "add", "tools/example/unformatted.go")
	f.sh("git", "commit", "-q", "--amend", "--no-edit")
	decision, reason = f.pre("", "", nil)
	if decision != "defer" || reason != "" {
		t.Fatalf("after format: got (%q, %q), want (defer, '')", decision, reason)
	}
}

func TestACommitAlreadyRefusedIsDeniedWithTheStoredAnswer(t *testing.T) {
	f := newLandingFixture(t)
	head := f.commit("2")
	state, _ := json.Marshal(map[string]any{
		"refused": []map[string]any{{"commit": head, "answer": "ANSWER: gate said no"}},
	})
	if err := os.WriteFile(filepath.Join(f.durable, "landing-state.json"), state, 0644); err != nil {
		t.Fatal(err)
	}
	decision, reason := f.pre("", "", nil)
	if decision != "deny" || !strings.Contains(reason, "A new commit is required.\nANSWER: gate said no") {
		t.Fatalf("got (%q, %q)", decision, reason)
	}
}

func TestLandIgnoresOldReportOnlyRefusalsButHonorsContentRefusals(t *testing.T) {
	f := newLandingFixture(t)
	commit := f.commit("2")
	if d, _ := f.pre("", "", nil); d != "defer" {
		t.Fatalf("first pre = %q, want defer", d)
	}
	first := f.offer()
	_ = os.Remove(filepath.Join(f.durable, "landing-offer.json"))

	statePath := filepath.Join(f.durable, "landing-state.json")
	report := map[string]any{"commit": commit, "summary": "old summary", "answer": "Criterion c2 (report): missing results", "report_only": true}
	content := map[string]any{"commit": commit, "answer": "Criterion c1 (content): missing test"}
	raw, _ := json.Marshal(map[string]any{"refused": []any{report, content}})
	if err := os.WriteFile(statePath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	decision, reason := f.pre("", "next", nil)
	if decision != "deny" || !strings.Contains(reason, "Criterion c1 (content)") {
		t.Fatalf("got (%q, %q)", decision, reason)
	}

	raw, _ = json.Marshal(map[string]any{"refused": []any{report}})
	if err := os.WriteFile(statePath, raw, 0644); err != nil {
		t.Fatal(err)
	}
	decision, reason = f.pre("", "next", nil)
	if decision != "defer" || reason != "" {
		t.Fatalf("got (%q, %q), want (defer, '')", decision, reason)
	}
	first["call_id"] = "next"
	if got := f.offer(); !reflect.DeepEqual(got, first) {
		t.Fatalf("offer = %#v, want %#v", got, first)
	}
}

func TestASiblingCallIsDeniedWhileAnOfferIsOpenAndAllowedOnceItHasItsResult(t *testing.T) {
	f := newLandingFixture(t)
	f.commit("2")
	if d, _ := f.pre("", "", nil); d != "defer" {
		t.Fatalf("initial pre = %q", d)
	}
	if d, _ := f.pre("Bash", "toolu_2", nil); d != "deny" {
		t.Fatalf("sibling Bash = %q, want deny", d)
	}
	if d, _ := f.pre("", "toolu_3", nil); d != "deny" {
		t.Fatalf("second land = %q, want deny", d)
	}
	if d, _ := f.pre("", "toolu_1", nil); d != "defer" {
		t.Fatalf("replay parked call = %q, want defer", d)
	}
	row, _ := json.Marshal(map[string]any{
		"type":    "user",
		"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "x"}}},
	})
	if err := os.WriteFile(f.transcript, append(row, '\n'), 0644); err != nil {
		t.Fatal(err)
	}
	if d, r := f.pre("Bash", "toolu_2", nil); d != "" || r != "" {
		t.Fatalf("after result Bash = (%q, %q), want ('', '')", d, r)
	}
	if _, err := os.Stat(filepath.Join(f.durable, "landing-offer.json")); !os.IsNotExist(err) {
		t.Fatalf("expected landing-offer.json removed, err=%v", err)
	}
}

func TestTheStagedAnswerForThisCallIsAllowedThroughToTheServer(t *testing.T) {
	f := newLandingFixture(t)
	f.commit("2")
	raw, _ := json.Marshal(map[string]any{"call_id": "toolu_1", "text": "ANSWER"})
	if err := os.WriteFile(filepath.Join(f.durable, "landing-answer.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	if d, _ := f.pre("", "", nil); d != "allow" {
		t.Fatalf("got %q, want allow", d)
	}
}

func TestObstacleIsValidatedAndDeferredInThePiOfferShape(t *testing.T) {
	f := newLandingFixture(t)
	d, r := f.pre(obstacleTool, "toolu_1", map[string]any{"kind": "fence", "evidence": "e"})
	if d != "deny" || !strings.Contains(r, `Invalid obstacle kind "fence"`) {
		t.Fatalf("bad kind: (%q, %q)", d, r)
	}
	d, r = f.pre(obstacleTool, "toolu_1", map[string]any{"kind": "tooling", "command": "check", "paths": "---, ```", "evidence": "e"})
	if d != "deny" || !strings.Contains(r, "missing required field(s): paths") {
		t.Fatalf("blank paths: (%q, %q)", d, r)
	}
	d, r = f.pre(obstacleTool, "toolu_1", map[string]any{"kind": "seat", "tried": "t", "missing": "m"})
	if d != "deny" {
		t.Fatalf("seat without attempt: (%q, %q)", d, r)
	}
	d, _ = f.pre(obstacleTool, "toolu_1", map[string]any{"kind": "owner", "question": "Which?"})
	if d != "defer" {
		t.Fatalf("owner obstacle: %q, want defer", d)
	}
	offer := f.offer()
	wantObs := map[string]any{"kind": "owner", "fields": map[string]any{"QUESTION": "Which?"}}
	if !reflect.DeepEqual(offer["obstacle"], wantObs) || offer["tool"] != "obstacle" {
		t.Fatalf("offer = %#v", offer)
	}
}

func mcpCallMsg(callID string) map[string]any {
	return map[string]any{
		"jsonrpc": "2.0",
		"id":      2,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      "land",
			"arguments": map[string]any{},
			"_meta":     map[string]any{"claudecode/toolUseId": callID},
		},
	}
}

func TestMcpListsBothToolsAndCarriesTheNamingInstruction(t *testing.T) {
	f := newLandingFixture(t)
	replies := f.serve(
		map[string]any{"jsonrpc": "2.0", "id": 1, "method": "initialize", "params": map[string]any{"protocolVersion": "2025-03-26"}},
		map[string]any{"jsonrpc": "2.0", "method": "notifications/initialized"},
		map[string]any{"jsonrpc": "2.0", "id": 3, "method": "tools/list"},
	)
	if len(replies) != 2 {
		t.Fatalf("got %d replies, want 2", len(replies))
	}
	initRes := replies[0]["result"].(map[string]any)
	if !strings.Contains(strField(initRes, "instructions"), "mcp__tillr__land") {
		t.Fatalf("instructions = %q", initRes["instructions"])
	}
	toolsRes := replies[1]["result"].(map[string]any)["tools"].([]any)
	var names []string
	for _, item := range toolsRes {
		names = append(names, strField(item.(map[string]any), "name"))
	}
	if !reflect.DeepEqual(names, []string{"land", "obstacle"}) {
		t.Fatalf("tools = %v, want [land obstacle]", names)
	}
}

func TestMcpTheAnswerIsTheCallsOwnErrorResult(t *testing.T) {
	f := newLandingFixture(t)
	raw, _ := json.Marshal(map[string]any{"call_id": "toolu_1", "text": "ANSWER: refused\ncheck go-test"})
	if err := os.WriteFile(filepath.Join(f.durable, "landing-answer.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	replies := f.serve(mcpCallMsg("toolu_1"))
	if len(replies) != 1 {
		t.Fatalf("got %d replies, want 1", len(replies))
	}
	want := map[string]any{
		"content": []any{map[string]any{"type": "text", "text": "ANSWER: refused\ncheck go-test"}},
		"isError": true,
	}
	if !reflect.DeepEqual(replies[0]["result"], want) {
		t.Fatalf("result = %#v, want %#v", replies[0]["result"], want)
	}
}

func TestMcpACallThatWasNotParkedReportsSoAndClearsItsOffer(t *testing.T) {
	f := newLandingFixture(t)
	raw, _ := json.Marshal(map[string]any{"call_id": "toolu_1"})
	if err := os.WriteFile(filepath.Join(f.durable, "landing-offer.json"), raw, 0644); err != nil {
		t.Fatal(err)
	}
	replies := f.serve(mcpCallMsg("toolu_1"))
	res := replies[0]["result"].(map[string]any)
	if res["isError"] != true {
		t.Fatalf("expected isError=true, got %#v", res)
	}
	text := res["content"].([]any)[0].(map[string]any)["text"].(string)
	if !strings.Contains(text, "by itself") {
		t.Fatalf("text = %q", text)
	}
	if _, err := os.Stat(filepath.Join(f.durable, "landing-offer.json")); !os.IsNotExist(err) {
		t.Fatalf("expected landing-offer.json removed, err=%v", err)
	}
}

func TestTranscriptHeldStates(t *testing.T) {
	f := newLandingFixture(t)
	writeRows := func(rows []map[string]any) {
		var buf bytes.Buffer
		for _, r := range rows {
			raw, _ := json.Marshal(r)
			buf.Write(raw)
			buf.WriteByte('\n')
		}
		if err := os.WriteFile(f.transcript, buf.Bytes(), 0644); err != nil {
			t.Fatal(err)
		}
	}
	deferred := map[string]any{
		"type":       "attachment",
		"attachment": map[string]any{"type": "hook_deferred_tool", "toolUseID": "toolu_1", "toolName": "mcp__tillr__land"},
	}
	result := map[string]any{
		"type":    "user",
		"message": map[string]any{"content": []any{map[string]any{"type": "tool_result", "tool_use_id": "toolu_1", "content": "a"}}},
	}

	// Unresolved deferred call is held (0).
	writeRows([]map[string]any{deferred})
	if code := Held(f.transcript, "toolu_1"); code != 0 {
		t.Fatalf("unresolved deferred = %d, want 0", code)
	}
	// Call with its result saved is reported resolved (3).
	writeRows([]map[string]any{deferred, result})
	if code := Held(f.transcript, "toolu_1"); code != 3 {
		t.Fatalf("resolved = %d, want 3", code)
	}
	// No marker or another call is not held (1).
	writeRows([]map[string]any{{"type": "user"}})
	if code := Held(f.transcript, "toolu_1"); code != 1 {
		t.Fatalf("no marker = %d, want 1", code)
	}
	writeRows([]map[string]any{deferred})
	if code := Held(f.transcript, "toolu_9"); code != 1 {
		t.Fatalf("other call = %d, want 1", code)
	}
	// Marker beyond the tail window is not held (1).
	filler := map[string]any{
		"type":    "assistant",
		"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": strings.Repeat("x", 4096)}}},
	}
	rows := make([]map[string]any, 0, 401)
	rows = append(rows, deferred)
	for i := 0; i < 400; i++ {
		rows = append(rows, filler)
	}
	writeRows(rows)
	if code := Held(f.transcript, "toolu_1"); code != 1 {
		t.Fatalf("beyond tail window = %d, want 1", code)
	}
}

func TestStopPlainStopIsNudgedOncePerState(t *testing.T) {
	f := newLandingFixture(t)
	first := f.stop()
	if first == nil || first["decision"] != "block" || !strings.Contains(strField(first, "reason"), "mcp__tillr__land") {
		t.Fatalf("first stop = %#v", first)
	}
	if second := f.stop(); second != nil {
		t.Fatalf("same state stop = %#v, want nil", second)
	}
	f.commit("2")
	third := f.stop()
	if third == nil || third["decision"] != "block" {
		t.Fatalf("moved state stop = %#v, want block", third)
	}
}

func TestStopOnlyACleanUnchangedPieceCanFinishWithADoneReport(t *testing.T) {
	f := newLandingFixture(t)
	brief := filepath.Join(f.root, "brief.md")
	section := "## Workflow node\n\nRun only node work/piece-mirror on the build seat; access: write.\nAn unchanged piece may finish without a commit.\n"
	t.Setenv("BOARD_DRAIN_BRIEF", brief)

	report := func(text string) {
		row, _ := json.Marshal(map[string]any{
			"type":    "assistant",
			"message": map[string]any{"content": []any{map[string]any{"type": "text", "text": text}}},
		})
		if err := os.WriteFile(f.transcript, append(row, '\n'), 0644); err != nil {
			t.Fatal(err)
		}
	}

	if err := os.WriteFile(brief, []byte(section), 0644); err != nil {
		t.Fatal(err)
	}
	report("Verified: pack sync left no diff.\n\nRESULT: done")
	if got := f.stop(); got != nil {
		t.Fatalf("clean unchanged piece with RESULT: done got %#v, want nil", got)
	}

	if err := os.WriteFile(brief, []byte("Brief\n## Saved request words (verbatim; 123 bytes)\n"+section), 0644); err != nil {
		t.Fatal(err)
	}
	if got := f.stop(); got == nil || got["decision"] != "block" {
		t.Fatalf("saved-words spoof got %#v, want block", got)
	}

	if err := os.WriteFile(brief, []byte(section), 0644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(f.repo, "scratch"), nil, 0644); err != nil {
		t.Fatal(err)
	}
	if got := f.stop(); got == nil || got["decision"] != "block" {
		t.Fatalf("dirty unchanged piece got %#v, want block", got)
	}
	_ = os.Remove(filepath.Join(f.repo, "scratch"))

	report("Still working")
	if got := f.stop(); got == nil || got["decision"] != "block" {
		t.Fatalf("non-done report got %#v, want block", got)
	}

	f.commit("2")
	report("Changed and verified.\n\nRESULT: done")
	if got := f.stop(); got == nil || got["decision"] != "block" {
		t.Fatalf("committed piece got %#v, want block", got)
	}
}

func TestStopARequestedPauseIsNeverNudged(t *testing.T) {
	f := newLandingFixture(t)
	pause := filepath.Join(f.root, "pause")
	if err := os.WriteFile(pause, nil, 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("TILLR_PAUSE_REQUEST", pause)
	if got := f.stop(); got != nil {
		t.Fatalf("pause stop = %#v, want nil", got)
	}
}

func TestPrepareWritesStdioServerAndTwoHooksMergedOverGivenSettings(t *testing.T) {
	f := newLandingFixture(t)
	extraJSON := filepath.Join(f.root, "policy-settings.json")
	if err := os.WriteFile(extraJSON, []byte(`{"env":{"B":"1"},"hooks":{"PreToolUse":[{"matcher":"Bash","hooks":[{"type":"command","command":"true"}]}]}}`), 0644); err != nil {
		t.Fatal(err)
	}
	extraList := filepath.Join(f.root, "extra.bin")
	if err := os.WriteFile(extraList, []byte(extraJSON+"\x00"+`{"permissions":{"allow":["Read"]}}`+"\x00"), 0644); err != nil {
		t.Fatal(err)
	}
	code, _, stderr := f.call("prepare", nil, "--settings-json", `{"env":{"A":""}}`, "--extra-settings-file", extraList)
	if code != 0 {
		t.Fatalf("prepare exit %d: %s", code, stderr)
	}

	mcpRaw, err := os.ReadFile(filepath.Join(f.durable, "claude-mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	var mcpDoc map[string]any
	if err := json.Unmarshal(mcpRaw, &mcpDoc); err != nil {
		t.Fatal(err)
	}
	mcp := mcpDoc["mcpServers"].(map[string]any)["tillr"].(map[string]any)
	if mcp["type"] != "stdio" {
		t.Fatalf("mcp type = %v", mcp["type"])
	}
	argsRaw := mcp["args"].([]any)
	var args []string
	for _, a := range argsRaw {
		args = append(args, a.(string))
	}
	wantTail := []string{"mcp", "--durable", f.durable}
	if len(args) < 1 || !reflect.DeepEqual(args[1:], wantTail) {
		t.Fatalf("mcp args = %v, want [claude-landing %v]", args, wantTail)
	}

	settingsRaw, err := os.ReadFile(filepath.Join(f.durable, "claude-settings.json"))
	if err != nil {
		t.Fatal(err)
	}
	var settings map[string]any
	if err := json.Unmarshal(settingsRaw, &settings); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(settings["env"], map[string]any{"A": "", "B": "1"}) {
		t.Fatalf("env = %#v", settings["env"])
	}
	if !reflect.DeepEqual(settings["permissions"], map[string]any{"allow": []any{"Read"}}) {
		t.Fatalf("permissions = %#v", settings["permissions"])
	}
	hooks := settings["hooks"].(map[string]any)
	preList := hooks["PreToolUse"].([]any)
	if len(preList) != 2 {
		t.Fatalf("len(PreToolUse) = %d, want 2", len(preList))
	}
	preCmd := preList[1].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string)
	if !strings.Contains(preCmd, "pre-tool-use") {
		t.Fatalf("PreToolUse command = %q", preCmd)
	}
	stopCmd := hooks["Stop"].([]any)[0].(map[string]any)["hooks"].([]any)[0].(map[string]any)["command"].(string)
	if !strings.Contains(stopCmd, " stop ") {
		t.Fatalf("Stop command = %q", stopCmd)
	}
}

func TestNoChangeToolAllowedOnSingleWorkerBriefAndDefersCleanOffer(t *testing.T) {
	f := newLandingFixture(t)
	brief := filepath.Join(f.root, "brief.md")
	if err := os.WriteFile(brief, []byte("## Finishing\n\nA reproduced no-change finding may finish with no_change.\n"), 0644); err != nil {
		t.Fatal(err)
	}
	t.Setenv("BOARD_DRAIN_BRIEF", brief)

	code, _, stderr := f.call("prepare", nil)
	if code != 0 {
		t.Fatalf("prepare exit %d: %s", code, stderr)
	}
	mcpRaw, err := os.ReadFile(filepath.Join(f.durable, "claude-mcp.json"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(mcpRaw), "--allow-no-change") {
		t.Fatalf("claude-mcp.json lacks --allow-no-change: %s", mcpRaw)
	}

	finding := map[string]any{"cause": "sibling fix", "evidence": "go test passed", "why_no_change": "already green"}
	if d, _ := f.pre(noChangeTool, "nc-1", finding); d != "deny" {
		t.Fatalf("without --allow-no-change: got %q, want deny", d)
	}
	if d, _ := f.pre(noChangeTool, "nc-1", finding, "--allow-no-change"); d != "defer" {
		t.Fatalf("with --allow-no-change: got %q, want defer", d)
	}
	offer := f.offer()
	if offer["tool"] != "no_change" || !reflect.DeepEqual(offer["finding"], finding) {
		t.Fatalf("offer = %#v", offer)
	}
}

func TestAnswerSplitsCallIDAndText(t *testing.T) {
	f := newLandingFixture(t)
	ansFile := filepath.Join(f.durable, "landing-answer.json")
	idOut := filepath.Join(f.durable, "id")
	textOut := filepath.Join(f.durable, "text")
	if err := os.WriteFile(ansFile, []byte(`{"call_id":"toolu_1","text":"ANSWER: refused"}`), 0644); err != nil {
		t.Fatal(err)
	}
	var stdout, stderr bytes.Buffer
	if code := RunLanding([]string{"answer", ansFile, idOut, textOut}, nil, &stdout, &stderr); code != 0 {
		t.Fatalf("answer exit %d: %s", code, stderr.String())
	}
	idBytes, _ := os.ReadFile(idOut)
	textBytes, _ := os.ReadFile(textOut)
	if string(idBytes) != "toolu_1" || string(textBytes) != "ANSWER: refused" {
		t.Fatalf("got (%q, %q)", string(idBytes), string(textBytes))
	}
	if err := os.WriteFile(ansFile, []byte(`{"call_id":"","text":"x"}`), 0644); err != nil {
		t.Fatal(err)
	}
	if code := RunLanding([]string{"answer", ansFile, idOut, textOut}, nil, &stdout, &stderr); code != 1 {
		t.Fatalf("empty call_id exit = %d, want 1", code)
	}
}
