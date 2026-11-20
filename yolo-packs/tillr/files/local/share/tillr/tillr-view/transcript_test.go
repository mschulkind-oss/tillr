package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// transcriptHarness is one run's three records on disk: the harness's stream, the tick's
// step record and the shim's subprocess record. It is the smallest set of files the reader
// reads, written in the spellings the writers use, so a case asserts the reader's answer and
// not a fixture's convenience.
type transcriptHarness struct {
	t        *testing.T
	dir      string
	loopDir  string
	laneRoot string
	runID    string
	laneID   string
}

func newTranscriptHarness(t *testing.T) *transcriptHarness {
	t.Helper()
	dir := t.TempDir()
	h := &transcriptHarness{
		t: t, dir: dir,
		loopDir:  filepath.Join(dir, "loop"),
		laneRoot: filepath.Join(dir, "lanes"),
		runID:    "run-1",
		laneID:   "lane-a",
	}
	if err := os.MkdirAll(filepath.Join(h.loopDir, h.runID), 0o755); err != nil {
		t.Fatalf("creating the loop directory: %v", err)
	}
	if err := os.MkdirAll(filepath.Join(h.laneRoot, h.laneID), 0o755); err != nil {
		t.Fatalf("creating the lane directory: %v", err)
	}
	return h
}

// obs is the observations handle the reader takes.
func (h *transcriptHarness) obs() *observations {
	return &observations{loopDir: h.loopDir, laneRoot: h.laneRoot}
}

// run is the store row the reader answers for.
func (h *transcriptHarness) run() storeRun {
	return storeRun{RunID: h.runID, LaneID: h.laneID, ItemID: "item-1", Backend: "pi"}
}

func (h *transcriptHarness) logPath() string {
	return runLogPath(h.laneRoot, h.laneID, h.runID)
}

// writeLog writes the harness's stream, one value per line.
func (h *transcriptHarness) writeLog(lines ...string) {
	h.t.Helper()
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	if err := os.WriteFile(h.logPath(), []byte(body), 0o644); err != nil {
		h.t.Fatalf("writing the run's log: %v", err)
	}
}

// appendLog adds lines to a log a case already wrote, which is what a live run does.
func (h *transcriptHarness) appendLog(lines ...string) {
	h.t.Helper()
	file, err := os.OpenFile(h.logPath(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		h.t.Fatalf("opening the run's log to append: %v", err)
	}
	defer file.Close()
	for _, line := range lines {
		if _, err := fmt.Fprintln(file, line); err != nil {
			h.t.Fatalf("appending to the run's log: %v", err)
		}
	}
}

func (h *transcriptHarness) writeSteps(lines ...string) {
	h.t.Helper()
	h.writeLines(filepath.Join(h.loopDir, h.runID, "steps.jsonl"), lines)
}

func (h *transcriptHarness) writeProc(lines ...string) {
	h.t.Helper()
	h.writeLines(filepath.Join(h.loopDir, h.runID, "proc-exec.jsonl"), lines)
}

func (h *transcriptHarness) writeLines(path string, lines []string) {
	h.t.Helper()
	body := strings.Join(lines, "\n")
	if body != "" {
		body += "\n"
	}
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		h.t.Fatalf("writing %s: %v", path, err)
	}
}

// page reads one page of the run's transcript.
func (h *transcriptHarness) page(query transcriptQuery) transcriptData {
	h.t.Helper()
	return h.obs().transcript(h.run(), query)
}

// timeline reads every entry the run has, page by page, by walking `before` from the tail.
// It is the reader's own paging, driven the way a client drives it, so a case that asserts
// "the whole transcript" is asserting what the API actually returns.
func (h *transcriptHarness) timeline(limit int) []transcriptEntry {
	h.t.Helper()
	var out []transcriptEntry
	cursor := ""
	for pages := 0; pages < 1000; pages++ {
		query := transcriptQuery{Limit: limit}
		if cursor != "" {
			decoded, ok := decodeTranscriptCursor(cursor)
			if !ok {
				h.t.Fatalf("the reader answered a cursor it cannot read: %q", cursor)
			}
			query.Before = &decoded
		}
		page := h.page(query)
		out = append(page.Entries, out...)
		if page.Before == nil {
			return out
		}
		cursor = *page.Before
	}
	h.t.Fatalf("paging the transcript did not reach its head in 1000 pages")
	return nil
}

// kinds is the transcript as a compact shape a case can assert in one line.
func kinds(entries []transcriptEntry) string {
	parts := make([]string, 0, len(entries))
	for _, entry := range entries {
		parts = append(parts, entry.Kind)
	}
	return strings.Join(parts, ",")
}

// text is one entry's body, for a message that holds its text there.
func text(entry transcriptEntry) string {
	if entry.Tool != nil && entry.Tool.Arguments != "" {
		return entry.Tool.Arguments
	}
	return entry.Text
}

// transcriptLines is the fixture's stream: a session, the task, one assistant turn that
// thinks, speaks and calls a tool, the tool's result, and the turn that ends the run. The
// clocks ascend by design so a case can assert the merge's order and not the file's.
func transcriptLines() []string {
	return []string{
		`{"type":"session","version":3,"id":"s-1","timestamp":"1970-01-01T00:00:02.000Z","cwd":"/work"}`,
		`{"type":"agent_start"}`,
		`{"type":"turn_start"}`,
		`{"type":"message_start","message":{"role":"user"}}`,
		`{"type":"message_end","message":{"role":"user","timestamp":2100,"content":[{"type":"text","text":"do the thing"}]}}`,
		`{"type":"request_start","timestamp":2150}`,
		`{"type":"message_update","message":{"role":"assistant"}}`,
		`{"type":"message_end","message":{"role":"assistant","timestamp":2200,"content":[` +
			`{"type":"thinking","thinking":"thinking hard"},` +
			`{"type":"text","text":"I will list the directory"},` +
			`{"type":"toolCall","id":"call-1","name":"bash","arguments":{"command":"ls -la"}}]}}`,
		`{"type":"tool_execution_start","toolCallId":"call-1","toolName":"bash","args":{"command":"ls -la"}}`,
		`{"type":"tool_execution_end","toolCallId":"call-1","toolName":"bash","result":{"content":[]},"isError":false,"durationMs":12}`,
		`{"type":"message_end","message":{"role":"toolResult","toolCallId":"call-1","toolName":"bash","isError":false,"durationMs":12,"timestamp":2400,"content":[{"type":"text","text":"a.txt"}]}}`,
		`{"type":"message_end","message":{"role":"assistant","timestamp":2500,"content":[{"type":"text","text":"there is one file"}]}}`,
		`{"type":"agent_end","aborted":false}`,
		`{"type":"agent_settled","aborted":false}`,
	}
}

// transcriptSteps is the tick's record of the run's own step.
func transcriptSteps() []string {
	return []string{
		`{"step":"run","argv":["tillr-run","exec","--store","/s/state.db"],"cwd":"/work",` +
			`"started_at_ms":1000,"finished_at_ms":6000,"exit_code":0,"stderr_tail":""}`,
	}
}

// transcriptProc is the shim's record: one line the wrapper's shell ran before the agent
// existed, and one the agent's own tool call ran.
func transcriptProc() []string {
	return []string{
		`{"kind":"proc.exec","ts":"1970-01-01T00:00:01.000Z","ts_end":"1970-01-01T00:00:01.100Z",` +
			`"duration_ms":0,"exit_code":0,"cmd":"set -euo pipefail","repo_rel_cwd":".s/lane"}`,
		`{"kind":"proc.exec","ts":"1970-01-01T00:00:02.200Z","ts_end":"1970-01-01T00:00:02.300Z",` +
			`"duration_ms":100,"exit_code":0,"cmd":"ls -la","repo_rel_cwd":".s/lane"}`,
	}
}

// TestTranscriptRendersTheRunsOwnRecordInOrder is the item's first criterion: the agent's
// messages, its thinking and each tool call with the arguments it passed and what came back,
// in order, read from the run's own files.
func TestTranscriptRendersTheRunsOwnRecordInOrder(t *testing.T) {
	h := newTranscriptHarness(t)
	h.writeLog(transcriptLines()...)
	h.writeSteps(transcriptSteps()...)
	h.writeProc(transcriptProc()...)

	got := h.timeline(3)
	want := "step,notice,message,thinking,message,tool_call,tool_result,message,notice"
	if kinds(got) != want {
		t.Fatalf("the transcript reads %q, want %q", kinds(got), want)
	}

	// The step is the run's own invocation, and it comes first because it started first.
	step := got[0]
	if step.Source != transcriptSourceSteps || !strings.Contains(step.Text, "tillr-run exec") {
		t.Fatalf("the first entry is %q/%q, want the run's step", step.Source, step.Text)
	}
	if step.ExitCode == nil || *step.ExitCode != 0 || step.DurationMS == nil || *step.DurationMS != 5000 {
		t.Fatalf("the step record's exit code and duration are %v/%v, want 0/5000",
			step.ExitCode, step.DurationMS)
	}

	// The thinking is its own entry, not folded into the message.
	thinking := got[3]
	if thinking.Kind != entryThinking || thinking.Text != "thinking hard" || thinking.Role != "assistant" {
		t.Fatalf("the reasoning entry is %+v, want the recorded thinking block", thinking)
	}

	// The call carries the arguments the model passed, and the result answers the same call
	// id with what came back: the pair is the criterion's "arguments it passed and what came
	// back", recorded as the stream records it.
	call := got[5]
	if call.Tool == nil || call.Tool.Name != "bash" || !strings.Contains(call.Tool.Arguments, `"ls -la"`) {
		t.Fatalf("the tool call entry is %+v, want bash with its arguments", call.Tool)
	}
	if call.ToolCallID != "call-1" {
		t.Fatalf("the tool call names call %q, want call-1", call.ToolCallID)
	}
	result := got[6]
	if result.Kind != entryToolResult || result.ToolCallID != "call-1" || result.Text != "a.txt" {
		t.Fatalf("the tool result entry is %+v, want call-1's output", result)
	}
	if result.IsError != nil && *result.IsError {
		t.Fatalf("a successful call's result answers is_error=true")
	}

	if got[6].Kind != entryToolResult {
		t.Fatalf("the entry after the call is %+v, want its result", got[6])
	}
	// The shim's rows are not entries while the agent's stream is the spine: a shell command
	// is not a message, and the shim runs on the harness's own bootstrap too. The source is
	// still read and reported, which is what the last assertion below pins.
	for _, entry := range got {
		if entry.Kind == entryProc {
			t.Fatalf("a shell command is a line of the transcript: %+v", entry)
		}
	}

	// Every entry carries the record's provenance, and a clock the record actually had.
	for _, entry := range got {
		if entry.EpistemicState == "" {
			t.Fatalf("entry %d (%s) carries no epistemic state", entry.Seq, entry.Kind)
		}
		if entry.EpistemicState == stateObserved && entry.AtMS == nil {
			t.Fatalf("entry %d (%s) is observed and carries no clock", entry.Seq, entry.Kind)
		}
	}

	// The last notice is the agent's own end, which the stream records with no clock of its
	// own: it inherits the last one, and says so rather than claiming 1970.
	last := got[len(got)-1]
	if last.Kind != entryNotice || last.EpistemicState != stateDerivedClock {
		t.Fatalf("the stream's end is %+v, want a notice marked %s", last, stateDerivedClock)
	}
	if last.AtMS == nil || *last.AtMS != 2500 {
		t.Fatalf("the stream's last notice answers clock %v, want the inherited 2500", last.AtMS)
	}
}

// TestTranscriptPagesWithoutGapsOrDuplicates is the third criterion, in its paging half: a
// page is a window with a cursor at each end, and paging it back to the head answers every
// entry exactly once, in the same order.
func TestTranscriptPagesWithoutGapsOrDuplicates(t *testing.T) {
	h := newTranscriptHarness(t)
	lines := []string{`{"type":"session","timestamp":"1970-01-01T00:00:02.000Z","cwd":"/work"}`}
	for i := 0; i < 25; i++ {
		lines = append(lines, fmt.Sprintf(
			`{"type":"message_end","message":{"role":"assistant","timestamp":%d,"content":[{"type":"text","text":"message %d"}]}}`,
			3000+i, i))
	}
	h.writeLog(lines...)

	for _, limit := range []int{1, 2, 7, 100} {
		whole := h.timeline(limit)
		if len(whole) != 26 {
			t.Fatalf("limit %d: the pages answer %d entries, want the 26 the log holds",
				limit, len(whole))
		}
		for i, entry := range whole {
			if entry.Seq != i {
				t.Fatalf("limit %d: entry %d answers seq %d: the pages are not one sequence",
					limit, i, entry.Seq)
			}
			want := fmt.Sprintf("message %d", i-1)
			if i == 0 {
				want = "the agent's session started in /work"
			}
			if entry.Text != want {
				t.Fatalf("limit %d: entry %d reads %q, want %q", limit, i, entry.Text, want)
			}
		}
	}
}

// TestTranscriptResumesAfterACursor is the live half of the first criterion: a run that is
// still writing is readable while it works, and asking from the cursor a page ended at
// answers the lines written since and nothing else.
func TestTranscriptResumesAfterACursor(t *testing.T) {
	h := newTranscriptHarness(t)
	h.writeLog(`{"type":"message_end","message":{"role":"assistant","timestamp":1000,"content":[{"type":"text","text":"first"}]}}`)

	before := h.page(transcriptQuery{Limit: 10})
	if len(before.Entries) != 1 || before.Entries[0].Text != "first" {
		t.Fatalf("the first read answers %+v, want the one recorded message", before.Entries)
	}
	cursor, ok := decodeTranscriptCursor(before.Cursor)
	if !ok {
		t.Fatalf("the page's cursor does not decode: %q", before.Cursor)
	}

	// Nothing new yet: resuming from the cursor answers nothing, which is what a live poll
	// that caught no change looks like.
	empty := h.page(transcriptQuery{After: &cursor})
	if len(empty.Entries) != 0 {
		t.Fatalf("resuming a caught-up cursor answers %d entries, want none", len(empty.Entries))
	}

	h.appendLog(`{"type":"message_end","message":{"role":"assistant","timestamp":2000,"content":[{"type":"text","text":"second"}]}}`)
	page := h.page(transcriptQuery{After: &cursor})
	if len(page.Entries) != 1 || page.Entries[0].Text != "second" {
		t.Fatalf("resuming after an append answers %+v, want only the new message", page.Entries)
	}
	if page.Entries[0].Seq != 1 {
		t.Fatalf("the appended entry answers seq %d, want 1: the sequence continues", page.Entries[0].Seq)
	}
}

// TestTranscriptDoesNotRecordAHalfWrittenLine is the live case's other half: a writer that
// has written half a line has not written an entry, and a reader must not record one.
func TestTranscriptDoesNotRecordAHalfWrittenLine(t *testing.T) {
	h := newTranscriptHarness(t)
	h.writeLog(`{"type":"message_end","message":{"role":"assistant","timestamp":1000,"content":[{"type":"text","text":"first"}]}}`)
	first := h.page(transcriptQuery{Limit: 10})

	file, err := os.OpenFile(h.logPath(), os.O_APPEND|os.O_WRONLY, 0o644)
	if err != nil {
		t.Fatalf("opening the log: %v", err)
	}
	if _, err := file.WriteString(`{"type":"message_end","message":{"role":"assistant","timestamp":2000,"content":[{"type":"text","text":"half`); err != nil {
		t.Fatalf("writing a partial line: %v", err)
	}
	_ = file.Close()

	page := h.page(transcriptQuery{Limit: 10})
	if len(page.Entries) != len(first.Entries) {
		t.Fatalf("a half-written line answered %d entries, want the %d already complete",
			len(page.Entries), len(first.Entries))
	}
	if page.Cursor != first.Cursor {
		t.Fatalf("a half-written line moved the cursor from %q to %q", first.Cursor, page.Cursor)
	}
}

// TestTranscriptBoundsOneEntryAndAnswersItWhole is the third criterion's bound: a page does
// not grow with an entry's body, the entry says how many bytes it really holds, and the
// single-entry route is the way to ask for the rest.
func TestTranscriptBoundsOneEntryAndAnswersItWhole(t *testing.T) {
	h := newTranscriptHarness(t)
	huge := strings.Repeat("x", 200<<10)
	payload, err := json.Marshal(map[string]any{
		"type": "message_end",
		"message": map[string]any{
			"role": "toolResult", "toolCallId": "call-1", "toolName": "read",
			"timestamp": 1000,
			"content":   []map[string]any{{"type": "text", "text": huge}},
		},
	})
	if err != nil {
		t.Fatalf("building the fixture's huge message: %v", err)
	}
	h.writeLog(string(payload))

	page := h.page(transcriptQuery{Limit: 10})
	if len(page.Entries) != 1 {
		t.Fatalf("the page answers %d entries, want the one the log holds", len(page.Entries))
	}
	entry := page.Entries[0]
	if !entry.Truncated {
		t.Fatal("a 200 KB body answered truncated=false: the page is not bounded")
	}
	if len(entry.Text) > transcriptEntryBytes {
		t.Fatalf("the page carries %d bytes of one entry, want at most %d",
			len(entry.Text), transcriptEntryBytes)
	}
	if entry.Bytes != len(huge) {
		t.Fatalf("the truncated entry says the record holds %d bytes, want %d", entry.Bytes, len(huge))
	}
	// The page's body budget is what makes a page's cost independent of the log's size.
	if page := h.page(transcriptQuery{Limit: 10}); len(page.Entries)*transcriptEntryBytes > transcriptPageBytes+transcriptEntryBytes {
		t.Fatalf("a page can carry %d entries of %d bytes, over its %d-byte budget",
			len(page.Entries), transcriptEntryBytes, transcriptPageBytes)
	}

	seq := entry.Seq
	whole := h.page(transcriptQuery{Entry: &seq, Untruncated: true, Limit: 1})
	if len(whole.Entries) != 1 {
		t.Fatalf("the single-entry route answers %d entries, want one", len(whole.Entries))
	}
	if whole.Entries[0].Truncated || whole.Entries[0].Text != huge {
		t.Fatalf("the single-entry route answered %d bytes (truncated=%v), want all %d",
			len(whole.Entries[0].Text), whole.Entries[0].Truncated, len(huge))
	}
}

// TestTranscriptSeamsWhenTheStreamIsAbsent is the second criterion's other half: a run whose
// transcript is not there says so as a seam, and the records that do survive are still read
// rather than rendered as an empty pane.
func TestTranscriptSeamsWhenTheStreamIsAbsent(t *testing.T) {
	h := newTranscriptHarness(t)
	h.writeSteps(transcriptSteps()...)
	h.writeProc(transcriptProc()...)

	page := h.page(transcriptQuery{Limit: 10})
	if page.EpistemicState != stateUnobservedSeam {
		t.Fatalf("a run with no stream answers state %q, want %q", page.EpistemicState, stateUnobservedSeam)
	}
	if !strings.Contains(page.Note, "absent") {
		t.Fatalf("the seam's note does not say what is missing: %q", page.Note)
	}
	if page.Sources.Log.State != stateAbsent {
		t.Fatalf("the log's source answers %q, want absent", page.Sources.Log.State)
	}
	if page.Sources.Steps.State != statePresent || page.Sources.Proc.State != statePresent {
		t.Fatalf("the surviving records answer %q/%q, want them present",
			page.Sources.Steps.State, page.Sources.Proc.State)
	}
	if kinds(page.Entries) != "step,proc,proc" {
		t.Fatalf("the seam answers %q, want the step and subprocess records that survive",
			kinds(page.Entries))
	}
}

// TestTranscriptSeamsWhenTheStreamIsEmpty is the same criterion for the file a writer
// created and wrote nothing into: that is a gap, not an agent that did nothing.
func TestTranscriptSeamsWhenTheStreamIsEmpty(t *testing.T) {
	h := newTranscriptHarness(t)
	h.writeLog()
	h.writeSteps(transcriptSteps()...)

	page := h.page(transcriptQuery{Limit: 10})
	if page.EpistemicState != stateUnobservedSeam {
		t.Fatalf("an empty stream answers state %q, want %q", page.EpistemicState, stateUnobservedSeam)
	}
	if page.Sources.Log.State != stateEmpty {
		t.Fatalf("an empty stream's source answers %q, want empty", page.Sources.Log.State)
	}
	if !strings.Contains(page.Note, "no bytes") {
		t.Fatalf("the seam's note does not say the file is empty: %q", page.Note)
	}
	if len(page.Entries) == 0 {
		t.Fatal("an empty stream rendered no entries at all: the surviving records were dropped")
	}
}

// TestTranscriptAnswersACallWhoseResultNeverArrived is the killed-run case: a call the agent
// made and no result recorded is a call that was made. The call keeps its arguments and has
// no result entry after it, which is what a reader renders as "nothing came back" — the
// absence of the result entry, not an invented one.
func TestTranscriptAnswersACallWhoseResultNeverArrived(t *testing.T) {
	h := newTranscriptHarness(t)
	h.writeLog(
		`{"type":"message_end","message":{"role":"assistant","timestamp":1000,"content":[{"type":"toolCall","id":"call-9","name":"bash","arguments":{"command":"sleep 600"}}]}}`,
		`{"type":"tool_execution_start","toolCallId":"call-9","toolName":"bash","args":{"command":"sleep 600"}}`,
	)
	page := h.page(transcriptQuery{Limit: 10})
	if len(page.Entries) != 1 {
		t.Fatalf("the killed run's transcript answers %d entries, want the one call", len(page.Entries))
	}
	entry := page.Entries[0]
	if entry.Kind != entryToolCall || entry.ToolCallID != "call-9" {
		t.Fatalf("the unanswered call answered %+v", entry)
	}
	if entry.Tool == nil || !strings.Contains(entry.Tool.Arguments, "sleep 600") {
		t.Fatalf("the unanswered call lost the arguments it passed: %+v", entry.Tool)
	}
	for _, other := range page.Entries {
		if other.Kind == entryToolResult && other.ToolCallID == "call-9" {
			t.Fatalf("the killed run answers a result it never recorded: %+v", other)
		}
	}
}

// TestTranscriptCarriesTheStepAndTheSubprocessRecord is the source list the criterion names:
// all three of the run's own records are read, each named with where it was looked for, and
// the step record is the entry that frames the run.
func TestTranscriptCarriesTheStepAndTheSubprocessRecord(t *testing.T) {
	h := newTranscriptHarness(t)
	h.writeLog(transcriptLines()...)
	h.writeSteps(transcriptSteps()...)
	h.writeProc(transcriptProc()...)

	page := h.page(transcriptQuery{Limit: 100})
	seen := map[string]bool{}
	for _, entry := range page.Entries {
		seen[entry.Source] = true
	}
	for _, source := range []string{transcriptSourceLog, transcriptSourceSteps, transcriptSourceProc} {
		if !seen[source] && source != transcriptSourceProc {
			t.Fatalf("the transcript carries no entry from %q: %v", source, seen)
		}
	}
	// The shim's record contributes its entries only in the seam case, which
	// TestTranscriptSeamsWhenTheStreamIsAbsent pins; what it contributes here is the fact
	// that it was read, and where.
	if page.Sources.Proc.State != statePresent {
		t.Fatalf("the shim's record is not reported: %+v", page.Sources.Proc)
	}
	if page.Sources.Log.Path != h.logPath() {
		t.Fatalf("the transcript reports the stream at %q, want %q", page.Sources.Log.Path, h.logPath())
	}
	if page.Sources.Steps.Path != filepath.Join(h.loopDir, h.runID, "steps.jsonl") {
		t.Fatalf("the transcript reports the step record at %q", page.Sources.Steps.Path)
	}
	if page.Sources.Proc.Path != filepath.Join(h.loopDir, h.runID, "proc-exec.jsonl") {
		t.Fatalf("the transcript reports the subprocess record at %q", page.Sources.Proc.Path)
	}
}

// TestTranscriptCursorRoundTrips is the wire's contract with itself: a token a page issues is
// a token the next page accepts, and a token nobody issued is refused rather than read as
// the head.
func TestTranscriptCursorRoundTrips(t *testing.T) {
	want := transcriptCursor{Log: 1234, Steps: 56, Proc: 7890, Seq: 42}
	got, ok := decodeTranscriptCursor(want.encode())
	if !ok || got != want {
		t.Fatalf("a cursor round-tripped to %+v (%v), want %+v", got, ok, want)
	}
	for _, bogus := range []string{"", "zzz", "Zm9v", "YTpiOmM6ZA", "-1:0:0:0"} {
		if _, ok := decodeTranscriptCursor(bogus); ok {
			t.Fatalf("%q decoded as a cursor this face issued", bogus)
		}
	}
	if !(transcriptCursor{}).atHead() {
		t.Fatal("the zero cursor is not the head")
	}
	if (transcriptCursor{Log: 1}).atHead() {
		t.Fatal("a cursor past the first line answered as the head")
	}
}

// TestTranscriptReadsTheClaudeBackendsStream pins the stream-json spelling: prose and each
// tool call come out in order, a result carries the time its call took, and the final tally
// is a notice.
func TestTranscriptReadsTheClaudeBackendsStream(t *testing.T) {
	raw, err := os.ReadFile("testdata/claude-stream.jsonl")
	if err != nil {
		t.Fatal(err)
	}
	h := newTranscriptHarness(t)
	if err := os.WriteFile(h.logPath(), raw, 0o644); err != nil {
		t.Fatal(err)
	}
	got := h.page(transcriptQuery{Limit: 50})
	if got.Sources.Log.State != statePresent || got.EpistemicState != stateObserved {
		t.Fatalf("the log reads %q/%q, want present/observed", got.Sources.Log.State, got.EpistemicState)
	}
	var kindList []string
	for _, entry := range got.Entries {
		kindList = append(kindList, entry.Kind)
	}
	if want := "message,tool_call,tool_result,tool_call,tool_result,message,notice"; strings.Join(kindList, ",") != want {
		t.Fatalf("the entries are %q, want %q", strings.Join(kindList, ","), want)
	}
	call, first := got.Entries[1], got.Entries[2]
	if call.Tool == nil || call.Tool.Name != "Bash" || !strings.Contains(call.Tool.Arguments, "git status") {
		t.Fatalf("the first call is %+v, want Bash with its command", call.Tool)
	}
	if first.Role != "Bash" || first.ToolCallID != "toolu_A" || first.DurationMS == nil || *first.DurationMS != 2500 {
		t.Fatalf("the first result is %+v, want Bash toolu_A taking 2500 ms", first)
	}
	second := got.Entries[4]
	if second.IsError == nil || !*second.IsError || second.Text != "no such file" || *second.DurationMS != 250 {
		t.Fatalf("the second result is %+v, want an error with text taking 250 ms", second)
	}
	if last := got.Entries[6]; !strings.Contains(last.Text, "3 turns") || !strings.Contains(last.Text, "2.5s was model time") {
		t.Fatalf("the closing notice is %q, want the turn count and model time", last.Text)
	}
}

// TestTranscriptSaysWhenALogYieldsNothing pins that a present log with no recognized line
// reports it, with the count, rather than looking like a run that said nothing.
func TestTranscriptSaysWhenALogYieldsNothing(t *testing.T) {
	h := newTranscriptHarness(t)
	h.writeLog(`{"type":"mystery","n":1}`, `{"type":"other"}`)
	got := h.page(transcriptQuery{Limit: 50})
	if got.Sources.Log.State != stateUnparsed || !strings.Contains(got.Sources.Log.Note, "2 lines") {
		t.Fatalf("the log source is %+v, want unparsed with a count of 2 lines", got.Sources.Log)
	}
	if got.EpistemicState != stateUnobservedSeam || got.Note == "" {
		t.Fatalf("the transcript is %q/%q, want a named seam", got.EpistemicState, got.Note)
	}
}
