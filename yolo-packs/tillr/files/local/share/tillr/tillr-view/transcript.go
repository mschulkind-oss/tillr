package main

import (
	"bufio"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"time"
	"unicode/utf8"
)

// The transcript is the run's own record of what the agent did, in the order it did it:
// the messages it wrote, the reasoning it recorded, and every tool call with the arguments
// it passed and what came back. It is read from the run's own artifacts — never
// reconstructed from the brief — and it is deliberately absent from the snapshot: a run's
// log is megabytes, and a board that carried it in every frame would be a board that cannot
// load while a drain runs.
//
// The sources, and what each one is the record of:
//
//	<lane-root>/<lane-id>/<run-id>.log   the harness's own stream (`pi --mode json`): one
//	                                     JSON value per line, in the order the agent
//	                                     produced them
//	<loop-dir>/<run-id>/steps.jsonl      the tick's record of the run's own step: the argv
//	                                     it was invoked with, its clocks and its exit code
//	<loop-dir>/<run-id>/proc-exec.jsonl  the bash shim's record of every subprocess a shell
//	                                     in the run executed, with its own exit code
//
// The three are merged by the clock each line recorded. Only the shim and the step record
// stamp every line; the harness's stream stamps a message and not the tool execution the
// message asked for, so a line that carries no clock inherits the last one its source did
// record and the entry says `derived_clock` rather than pretending to a timestamp.
//
// Nothing here writes, and no clock is invented.

// The transcript's bounds. A page is at most this many entries AND at most this much entry
// body, so a 4.6 MB run log costs the board one page rather than the file; within a page one
// entry's body is truncated with the number of bytes it really holds, and
// `GET /api/v1/runs/{id}/transcript/{seq}` answers that one entry whole.
const (
	transcriptPageLimit  = 100
	transcriptPageMax    = 500
	transcriptEntryBytes = 8 << 10
	transcriptPageBytes  = 256 << 10
)

// How a source's file was found. They are the same three words the artifact reads use, plus
// `empty`: a file a writer created and wrote nothing into is not a file with no writer.
const (
	statePresent = "present"
	stateAbsent  = "absent"
	stateEmpty   = "empty"
)

// The transcript's own vocabulary, in the sources' own words.
const (
	// transcriptSourceLog is the harness's stream; steps and proc are the tick's records.
	transcriptSourceLog   = "log"
	transcriptSourceSteps = "steps"
	transcriptSourceProc  = "proc"

	// entryStep is one step of the run as the tick recorded it.
	entryStep = "step"
	// entryMessage is a message the agent or its harness wrote: the task, the model's
	// prose, a custom hook's notice.
	entryMessage = "message"
	// entryThinking is the model's recorded reasoning, which is not its message.
	entryThinking = "thinking"
	// entryToolCall is one call the model made: the arguments it passed and what came back.
	entryToolCall = "tool_call"
	// entryToolResult is what one call came back with, linked to it by tool_call_id. The
	// stream records a call and its result as two messages, so the transcript keeps them as
	// two entries: the call, then whatever ran because of it, then the result.
	entryToolResult = "tool_result"
	// entryProc is one subprocess a shell in the run executed.
	entryProc = "proc"
	// entryNotice is a fact about the run that is not a message: the session's start, a
	// tool that ran past its bound, the agent settling.
	entryNotice = "notice"
)

// stateNoneClock is the entry state of a line whose source never recorded a clock. It is
// not one of the five provenance states: those describe a record's relationship to a clock,
// and this is the absence of any clock at all.
const stateNoneClock = "no_clock"

// runLogPath is the file `tillr-run` redirects the run's harness stream into. It is one
// spelling in two places (here and the live channel's log watch), because two spellings of
// a path a reader depends on is how a board watches a file nobody writes.
func runLogPath(laneRoot, laneID, runID string) string {
	if laneRoot == "" || laneID == "" || runID == "" {
		return ""
	}
	nodeDir := filepath.Join(laneRoot, laneID, "nodes", runID)
	if info, err := os.Stat(nodeDir); err == nil && info.IsDir() {
		return filepath.Join(nodeDir, runID+".log")
	}
	return filepath.Join(laneRoot, laneID, runID+".log")
}

// transcriptCursor resumes each append-only source at a byte offset and an entry index
// within that value. Seq continues display numbering; it is not a record identity.
// Boundary names the first entry excluded by a backward page, so newly merged records
// cannot move that boundary. Forward cursors leave Boundary empty.
type transcriptCursor struct {
	Log                             int64
	Steps                           int64
	Proc                            int64
	Seq                             int
	LogIndex, StepsIndex, ProcIndex int
	Boundary                        string
}

// encode spells a cursor as one opaque token. It is opaque on purpose: a client that built
// one from parts would be a second implementation of the resume rule.
func (c transcriptCursor) encode() string {
	raw := fmt.Sprintf("2:%d:%d:%d:%d:%d:%d:%d:%s", c.Log, c.Steps, c.Proc, c.Seq, c.LogIndex, c.StepsIndex, c.ProcIndex, c.Boundary)
	return base64.RawURLEncoding.EncodeToString([]byte(raw))
}

// atHead answers whether this cursor is the run's own first page.
func (c transcriptCursor) atHead() bool {
	return c.Log == 0 && c.Steps == 0 && c.Proc == 0 && c.Seq == 0 && c.LogIndex == 0 && c.StepsIndex == 0 && c.ProcIndex == 0
}

// decodeTranscriptCursor reads a token this face issued. A token that is not one is a
// caller's mistake and answers a bad query rather than a silent page from the start.
func decodeTranscriptCursor(token string) (transcriptCursor, bool) {
	raw, err := base64.RawURLEncoding.DecodeString(token)
	if err != nil {
		return transcriptCursor{}, false
	}
	parts := strings.SplitN(string(raw), ":", 9)
	boundary := ""
	modern := len(parts) == 9 && parts[0] == "2"
	if modern {
		boundary = parts[8]
		parts = parts[1:8]
		if boundary != "" {
			identity := strings.Split(boundary, ":")
			if len(identity) != 3 || (identity[0] != transcriptSourceLog && identity[0] != transcriptSourceSteps && identity[0] != transcriptSourceProc) {
				return transcriptCursor{}, false
			}
			for _, part := range identity[1:] {
				n, err := strconv.ParseInt(part, 10, 64)
				if err != nil || n < 0 {
					return transcriptCursor{}, false
				}
			}
		}
	} else if len(parts) != 4 {
		return transcriptCursor{}, false
	}
	values := make([]int64, len(parts))
	for i, part := range parts {
		n, err := strconv.ParseInt(part, 10, 64)
		if err != nil || n < 0 {
			return transcriptCursor{}, false
		}
		values[i] = n
	}
	cursor := transcriptCursor{Log: values[0], Steps: values[1], Proc: values[2], Seq: int(values[3]), Boundary: boundary}
	if modern {
		cursor.LogIndex, cursor.StepsIndex, cursor.ProcIndex = int(values[4]), int(values[5]), int(values[6])
	}
	return cursor, true
}

// toolCallView is one tool call as the transcript carries it: the arguments the model
// passed, and the name of the tool it passed them to.
type toolCallView struct {
	ID        string `json:"id"`
	Name      string `json:"name"`
	Arguments string `json:"arguments"`
}

// transcriptEntry is one line of the transcript. Every field is nullable where the record
// can be missing one: an entry with no clock says null rather than 1970.
type transcriptEntry struct {
	ID     string `json:"id"`
	Seq    int    `json:"seq"`
	Source string `json:"source"`
	Kind   string `json:"kind"`
	Role   string `json:"role,omitempty"`
	AtMS   *int64 `json:"at_ms"`
	// ToolCallID links a result to the call it answers, and names the call itself.
	ToolCallID string        `json:"tool_call_id,omitempty"`
	Tool       *toolCallView `json:"tool,omitempty"`
	// Text is the entry's body: a message, a reasoning block, a command, a result.
	Text string `json:"text,omitempty"`
	// Bytes is the record's own length, so a reader knows what asking for the entry whole
	// would fetch. Truncated says Text is shorter than that.
	Bytes          int    `json:"bytes"`
	Truncated      bool   `json:"truncated"`
	IsError        *bool  `json:"is_error,omitempty"`
	ExitCode       *int   `json:"exit_code,omitempty"`
	DurationMS     *int64 `json:"duration_ms,omitempty"`
	EpistemicState string `json:"epistemic_state"`
	Note           string `json:"note,omitempty"`

	// start and end are the cursors before and after this entry: the resume points a page
	// is cut at. They never travel on the wire — a client pages with the page's cursors,
	// never with one built from an entry.
	start transcriptCursor
	end   transcriptCursor
}

// transcriptSources is how each of the run's three records was found, so a reader can tell
// "the run said nothing" from "nothing was recording".
type transcriptSources struct {
	Log   sourceState `json:"log"`
	Steps sourceState `json:"steps"`
	Proc  sourceState `json:"proc"`
}

// transcriptData is one page of a run's transcript.
type transcriptData struct {
	Run     string            `json:"run"`
	Entries []transcriptEntry `json:"entries"`
	Sources transcriptSources `json:"sources"`
	// Cursor resumes after this page: ask for `after=<cursor>` for what came later.
	Cursor string `json:"cursor"`
	// Before resumes before this page, and is null when the page starts at the run's first
	// entry: it is the only thing that tells a client whether there is more to load.
	Before  *string `json:"before"`
	HasMore bool    `json:"has_more"`
	Limit   int     `json:"limit"`
	// EpistemicState is the transcript's own provenance: `observed` when the harness's
	// stream is there, `unobserved_seam` when it is not, `unreadable` when it could not be
	// read. The entries keep their own states either way.
	EpistemicState string `json:"epistemic_state"`
	Note           string `json:"note,omitempty"`
}

// transcriptQuery is one page request.
type transcriptQuery struct {
	Limit       int
	Before      *transcriptCursor
	After       *transcriptCursor
	Entry       *int
	EntryID     string
	Untruncated bool
}

// jsonStream reads one JSON value per line from a file, starting at a byte offset. It is
// the only way a source is read: a `bufio.Scanner` refuses a line longer than its buffer,
// and a run's tool result is routinely longer than any fixed buffer.
type jsonStream struct {
	file   *os.File
	dec    *json.Decoder
	offset int64
	base   int64
}

func openJSONStream(path string, offset int64) (*jsonStream, error) {
	file, err := os.Open(path)
	if err != nil {
		return nil, err
	}
	if _, err := file.Seek(offset, io.SeekStart); err != nil {
		_ = file.Close()
		return nil, err
	}
	return &jsonStream{
		file:   file,
		dec:    json.NewDecoder(bufio.NewReaderSize(file, 1<<16)),
		offset: offset,
		base:   offset,
	}, nil
}

func (s *jsonStream) close() {
	if s != nil && s.file != nil {
		_ = s.file.Close()
	}
}

// next answers the next complete value and the offset just past it. The end of the stream
// and a value cut off mid-write are the same answer — io.EOF — and in both cases the offset
// stays at the last complete value, so a live writer's half-written line is read again
// rather than recorded as a truncated entry.
func (s *jsonStream) next() (json.RawMessage, int64, error) {
	var raw json.RawMessage
	if err := s.dec.Decode(&raw); err != nil {
		if err == io.EOF || err == io.ErrUnexpectedEOF {
			return nil, s.offset, io.EOF
		}
		return nil, s.offset, err
	}
	s.offset = s.base + s.dec.InputOffset()
	return raw, s.offset, nil
}

// transcriptLine is one line of a source and the entries it produced, in order. A line is
// held until every entry has been emitted. A cursor cut inside this value carries its
// next entry index as well as the value's byte offset.
type transcriptLine struct {
	start, end int64
	clock      int64
	haveClock  bool
	entries    []transcriptEntry
	next       int
}

// transcriptSource is one of the run's records being walked.
type transcriptSource struct {
	name string
	// state is what the source's own file says: present, empty, absent, unreadable.
	state string
	// note says where the file was looked for, or why it is not one this face can read.
	note   string
	path   string
	stream *jsonStream
	// offset is the byte offset of the next line to read, and the resume point a page
	// records when this source has nothing held.
	offset      int64
	resumeIndex int
	// held is the line whose entries are being emitted.
	held *transcriptLine
	// clock is the last clock this source recorded; a line without one inherits it.
	clock     int64
	haveClock bool
	done      bool
	err       error
}

// advance fills the source's held line until it has an entry to give, and answers whether
// it has one.
func (s *transcriptSource) advance() bool {
	for {
		if s.held != nil && s.held.next < len(s.held.entries) {
			return true
		}
		if s.held != nil {
			s.offset = s.held.end
			s.held = nil
		}
		if s.done {
			return false
		}
		raw, end, err := s.stream.next()
		if err != nil {
			s.done = true
			if err != io.EOF {
				s.err = err
			}
			continue
		}
		s.held = s.parse(raw, s.offset, end)
		if s.resumeIndex != 0 {
			s.held.next = s.resumeIndex
			s.resumeIndex = 0
		}
	}
}

// at answers the clock of the next entry this source will give, for the merge to compare.
func (s *transcriptSource) at() int64 {
	if s.held == nil {
		return 0
	}
	return s.held.clock
}

// close releases the source's file.
func (s *transcriptSource) close() { s.stream.close() }

// resolveClock is the one place a line's clock is decided: the one it recorded, else the
// last one its source did, and the entry says which. A source that has never recorded a
// clock answers "none", and an entry with no clock renders as none rather than as 1970.
func (s *transcriptSource) resolveClock(recorded int64, ok bool) (int64, bool, string) {
	if ok {
		s.clock, s.haveClock = recorded, true
		return recorded, true, stateObserved
	}
	if s.haveClock {
		return s.clock, true, stateDerivedClock
	}
	return 0, false, stateNoneClock
}

// body is one entry's text and the record's own length. Truncation happens once, in the
// walk, because it is a fact about the page and not about the record.
func (e *transcriptEntry) body(text string) {
	e.Text = text
	e.Bytes = len(text)
}

// truncateUTF8 cuts a body to a byte budget without splitting a rune: a body cut mid-rune
// is not the record's text, and a renderer would draw a replacement character instead.
func truncateUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	cut := limit
	for cut > 0 && !utf8.ValidString(text[:cut]) {
		cut--
	}
	return text[:cut]
}

// logEvent is one line of the harness's stream, read for the fields the transcript keeps.
// The stream's own vocabulary is wider than this struct: a field it does not name is a
// delta the transcript does not need.
type logEvent struct {
	Type        string          `json:"type"`
	Timestamp   json.RawMessage `json:"timestamp"`
	Cwd         string          `json:"cwd"`
	Message     *logMessage     `json:"message"`
	Aborted     *bool           `json:"aborted"`
	ToolName    string          `json:"toolName"`
	ThresholdMS int64           `json:"thresholdMs"`
	Notice      string          `json:"notice"`
}

// logMessage is one message of the stream. `content` is a block list on a message a model
// wrote and a plain string on the system prompt, which is why it is decoded by hand.
type logMessage struct {
	Role       string            `json:"role"`
	Timestamp  int64             `json:"timestamp"`
	ToolCallID string            `json:"toolCallId"`
	ToolName   string            `json:"toolName"`
	IsError    bool              `json:"isError"`
	DurationMS int64             `json:"durationMs"`
	CustomType string            `json:"customType"`
	Sections   map[string]string `json:"sections"`
	Content    logContent        `json:"content"`
}

// logContent is a message's content in both the spellings the stream uses for it.
type logContent struct {
	Blocks []logBlock
	Text   string
}

func (c *logContent) UnmarshalJSON(raw []byte) error {
	var blocks []logBlock
	if err := json.Unmarshal(raw, &blocks); err == nil {
		c.Blocks = blocks
		return nil
	}
	var text string
	if err := json.Unmarshal(raw, &text); err == nil {
		c.Text = text
		return nil
	}
	return fmt.Errorf("content is neither a block list nor a string")
}

// logBlock is one block of a message: prose, reasoning, or a tool call.
type logBlock struct {
	Type      string          `json:"type"`
	Text      string          `json:"text"`
	Thinking  string          `json:"thinking"`
	ID        string          `json:"id"`
	Name      string          `json:"name"`
	Arguments json.RawMessage `json:"arguments"`
}

// procLine is one line of the bash shim's subprocess record. The shim writes the row from
// the shell's EXIT trap, so a row's position is the order its command *finished* in and
// `ts_end` is the clock the file is sorted by; `ts` is when the command started, and 26 of
// this run's 761 rows start before their predecessor finished.
type procLine struct {
	TS         string `json:"ts"`
	TSEnd      string `json:"ts_end"`
	DurationMS int64  `json:"duration_ms"`
	ExitCode   *int   `json:"exit_code"`
	Cmd        string `json:"cmd"`
	Cwd        string `json:"cwd"`
	RepoRelCwd string `json:"repo_rel_cwd"`
}

// stepLine is one line of the tick's step record.
type stepLine struct {
	Step         string   `json:"step"`
	Argv         []string `json:"argv"`
	Cwd          string   `json:"cwd"`
	StartedAtMS  int64    `json:"started_at_ms"`
	FinishedAtMS int64    `json:"finished_at_ms"`
	ExitCode     *int     `json:"exit_code"`
	StderrTail   string   `json:"stderr_tail"`
}

// parseLog turns one line of the harness's stream into entries. The stream's event set is
// wider than the transcript's: streaming deltas (`message_update`, `turn_end`) repeat a
// message the transcript already has, and are read and dropped, because a transcript that
// carried every delta would be the log with a scrollbar.
func (s *transcriptSource) parseLog(raw json.RawMessage, start, end int64) *transcriptLine {
	line := &transcriptLine{start: start, end: end}
	var event logEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		line.entries = []transcriptEntry{{
			Source: transcriptSourceLog, Kind: entryNotice, EpistemicState: stateUnreadable,
			Text: fmt.Sprintf("a line of the run's stream could not be read as JSON: %v", err),
		}}
		return line
	}
	recorded, recordedOK := logClock(&event)
	switch event.Type {
	case "session":
		clock, have, state := s.resolveClock(recorded, recordedOK)
		line.clock, line.haveClock = clock, have
		text := "the agent's session started"
		if event.Cwd != "" {
			text += " in " + event.Cwd
		}
		line.entries = []transcriptEntry{notice(transcriptSourceLog, text, state, clock, have)}
		return line
	case "tool_call_stall":
		clock, have, state := s.resolveClock(recorded, recordedOK)
		line.clock, line.haveClock = clock, have
		text := fmt.Sprintf("the %q tool ran past its %s bound", event.ToolName,
			durationText(event.ThresholdMS))
		if event.Notice != "" {
			text = event.Notice
		}
		line.entries = []transcriptEntry{notice(transcriptSourceLog, text, state, clock, have)}
		return line
	case "agent_end":
		clock, have, state := s.resolveClock(recorded, recordedOK)
		line.clock, line.haveClock = clock, have
		text := "the agent finished its turn"
		if event.Aborted != nil && *event.Aborted {
			text = "the agent's turn was aborted"
		}
		line.entries = []transcriptEntry{notice(transcriptSourceLog, text, state, clock, have)}
		return line
	case "message_end":
	default:
		// a streaming delta, a turn boundary, an agent start: the transcript has the
		// message these describe, or the fact they carry is about the run's frame
		return line
	}
	if event.Message == nil {
		return line
	}
	clock, have, state := s.resolveClock(recorded, recordedOK)
	line.clock, line.haveClock = clock, have
	at := clockPointer(clock, have)
	message := event.Message
	switch message.Role {
	case "system":
		// The prompt is the run's context, not its activity: its text is the tool catalog
		// and the briefings, so it is recorded as the sections it is made of rather than
		// as prose the transcript would have to re-flow.
		names := make([]string, 0, len(message.Sections))
		size := 0
		for name, text := range message.Sections {
			names = append(names, name)
			size += len(text)
		}
		sort.Strings(names)
		summary := fmt.Sprintf("the system prompt: %d bytes in %d sections (%s)",
			size, len(names), strings.Join(names, ", "))
		line.entries = []transcriptEntry{{
			Source: transcriptSourceLog, Kind: entryNotice, Role: "system", AtMS: at,
			EpistemicState: state, Text: summary, Bytes: len(summary),
		}}
	case "assistant":
		line.entries = append(line.entries, s.assistantEntries(message, at, state)...)
	case "toolResult":
		line.entries = append(line.entries, s.toolResultEntries(message, at, state)...)
	default:
		role := message.Role
		if message.CustomType != "" {
			role = message.CustomType
		}
		entry := transcriptEntry{
			Source: transcriptSourceLog, Kind: entryMessage, Role: role, AtMS: at,
			EpistemicState: state,
		}
		entry.body(message.Content.Text)
		line.entries = []transcriptEntry{entry}
	}
	return line
}

// assistantEntries answers a model message's blocks: its prose, its reasoning, and one
// entry per tool call it asked for, carrying the arguments the model passed. The result is
// the stream's next message and its own entry, which is why a call and its answer are
// separated by whatever the call actually ran.
func (s *transcriptSource) assistantEntries(message *logMessage, at *int64, state string) []transcriptEntry {
	var out []transcriptEntry
	for _, block := range message.Content.Blocks {
		switch block.Type {
		case "text":
			if block.Text == "" {
				continue
			}
			entry := transcriptEntry{
				Source: transcriptSourceLog, Kind: entryMessage, Role: "assistant", AtMS: at,
				EpistemicState: state,
			}
			entry.body(block.Text)
			out = append(out, entry)
		case "thinking":
			if block.Thinking == "" {
				continue
			}
			entry := transcriptEntry{
				Source: transcriptSourceLog, Kind: entryThinking, Role: "assistant", AtMS: at,
				EpistemicState: state,
			}
			entry.body(block.Thinking)
			out = append(out, entry)
		case "toolCall":
			arguments := string(block.Arguments)
			out = append(out, transcriptEntry{
				Source: transcriptSourceLog, Kind: entryToolCall, Role: "assistant", AtMS: at,
				EpistemicState: state, ToolCallID: block.ID, Bytes: len(arguments),
				Tool: &toolCallView{ID: block.ID, Name: block.Name, Arguments: arguments},
			})
		}
	}
	return out
}

// toolResultEntries answers what one call came back with: the result the stream recorded
// against that call id, the error flag and the duration the harness measured.
func (s *transcriptSource) toolResultEntries(message *logMessage, at *int64, state string) []transcriptEntry {
	entry := transcriptEntry{
		Source: transcriptSourceLog, Kind: entryToolResult, Role: message.ToolName,
		AtMS: at, EpistemicState: state, ToolCallID: message.ToolCallID,
	}
	if message.IsError {
		isError := true
		entry.IsError = &isError
	}
	if message.DurationMS > 0 {
		duration := message.DurationMS
		entry.DurationMS = &duration
	}
	entry.body(blocksText(message.Content.Blocks))
	return []transcriptEntry{entry}
}

// parseProc turns one line of the shim's record into one entry: the command a shell ran,
// its exit code and its duration. This is the record of what actually ran, as distinct from
// the tool call that asked for it.
func (s *transcriptSource) parseProc(raw json.RawMessage, start, end int64) *transcriptLine {
	line := &transcriptLine{start: start, end: end}
	var row procLine
	if err := json.Unmarshal(raw, &row); err != nil {
		line.entries = []transcriptEntry{{
			Source: transcriptSourceProc, Kind: entryNotice, EpistemicState: stateUnreadable,
			Text: fmt.Sprintf("a line of the subprocess record could not be read as JSON: %v", err),
		}}
		return line
	}
	// the row is written on exit, so its clock is the completion: a merge by the start
	// would put a long command after the short ones that overtook it
	recorded, recordedOK := parseISOClock(row.TSEnd)
	if !recordedOK {
		recorded, recordedOK = parseISOClock(row.TS)
	}
	clock, have, state := s.resolveClock(recorded, recordedOK)
	entry := transcriptEntry{
		Source: transcriptSourceProc, Kind: entryProc, AtMS: clockPointer(clock, have),
		EpistemicState: state, ExitCode: row.ExitCode, Note: row.RepoRelCwd,
	}
	duration := row.DurationMS
	entry.DurationMS = &duration
	entry.body(row.Cmd)
	line.clock, line.haveClock = clock, have
	line.entries = []transcriptEntry{entry}
	return line
}

// parseStep turns one line of the tick's step record into one entry: the run's own step,
// with the argv it was invoked with and the exit code it ended on.
func (s *transcriptSource) parseStep(raw json.RawMessage, start, end int64) *transcriptLine {
	line := &transcriptLine{start: start, end: end}
	var row stepLine
	if err := json.Unmarshal(raw, &row); err != nil {
		line.entries = []transcriptEntry{{
			Source: transcriptSourceSteps, Kind: entryNotice, EpistemicState: stateUnreadable,
			Text: fmt.Sprintf("a line of the step record could not be read as JSON: %v", err),
		}}
		return line
	}
	clock, have, state := s.resolveClock(row.StartedAtMS, row.StartedAtMS > 0)
	text := strings.Join(row.Argv, " ")
	if text == "" {
		text = row.Step
	}
	entry := transcriptEntry{
		Source: transcriptSourceSteps, Kind: entryStep, Role: row.Step,
		AtMS: clockPointer(clock, have), EpistemicState: state, ExitCode: row.ExitCode,
		Note: row.Cwd,
	}
	if row.FinishedAtMS > row.StartedAtMS {
		duration := row.FinishedAtMS - row.StartedAtMS
		entry.DurationMS = &duration
	}
	entry.body(text)
	line.clock, line.haveClock = clock, have
	line.entries = []transcriptEntry{entry}
	return line
}

// parse sends one line to the parser its source uses.
func (s *transcriptSource) parse(raw json.RawMessage, start, end int64) *transcriptLine {
	switch s.name {
	case transcriptSourceSteps:
		return s.parseStep(raw, start, end)
	case transcriptSourceProc:
		return s.parseProc(raw, start, end)
	default:
		return s.parseLog(raw, start, end)
	}
}

// logClock is the clock a line of the stream recorded. The stream stamps three kinds of
// line — the session's start, a request's start and every message — and no tool execution,
// which is why a tool call's clock is the message that asked for it.
func logClock(event *logEvent) (int64, bool) {
	if event.Message != nil && event.Message.Timestamp > 0 {
		return event.Message.Timestamp, true
	}
	if len(event.Timestamp) == 0 {
		return 0, false
	}
	// the session stamps an ISO string, a request an epoch; both spellings are the writer's
	var text string
	if err := json.Unmarshal(event.Timestamp, &text); err == nil {
		return parseISOClock(text)
	}
	var ms int64
	if err := json.Unmarshal(event.Timestamp, &ms); err == nil && ms > 0 {
		return ms, true
	}
	return 0, false
}

// parseISOClock reads the design's `2026-11-12T19:04:12.104Z` spelling. A clock it cannot
// read is a gap, and the entry inherits its source's last one rather than claiming this one.
func parseISOClock(value string) (int64, bool) {
	if value == "" {
		return 0, false
	}
	if ms := parseEpochMS(value); ms != nil {
		return *ms, true
	}
	return 0, false
}

// blocksText is the text of a message's content blocks, one block per line: the content is a
// list, and the transcript shows what each block said.
func blocksText(blocks []logBlock) string {
	parts := make([]string, 0, len(blocks))
	for _, block := range blocks {
		switch block.Type {
		case "text":
			parts = append(parts, block.Text)
		case "thinking":
			parts = append(parts, block.Thinking)
		}
	}
	return strings.Join(parts, "\n\n")
}

// clockPointer answers an optional clock: a nil pointer is "this record has no clock", and a
// zero would be a record that claims 1970.
func clockPointer(clock int64, ok bool) *int64 {
	if !ok {
		return nil
	}
	value := clock
	return &value
}

func notice(source, text, state string, clock int64, have bool) transcriptEntry {
	return transcriptEntry{
		Source: source, Kind: entryNotice, Text: text, Bytes: len(text),
		AtMS: clockPointer(clock, have), EpistemicState: state,
	}
}

// durationText spells a bound the way the run's own notice spells it.
func durationText(ms int64) string {
	if ms <= 0 {
		return "an unrecorded"
	}
	return time.Duration(ms * int64(time.Millisecond)).String()
}

// transcriptWalk is one pass over the run's three records, merged by the clock each line
// recorded. The merge reads one line ahead per source — that is what makes the cursor a line
// boundary on every source at once — and it holds at most one page of entries.
type transcriptWalk struct {
	sources []*transcriptSource
	start   transcriptCursor
	prev    transcriptCursor
	seq     int
	limit   int
	// keepLast keeps the last `limit` entries rather than the first, which is what the tail
	// page is: an operator reads what the run is doing now, not its first line.
	keepLast bool
	// stopAt ends the walk at this cursor; stopSeq ends it at that entry's sequence number.
	// Both include the entry that reaches them and exclude what comes after.
	stopAt  *transcriptCursor
	stopSeq *int
	stopID  string
	// untruncated leaves every body whole, which only the single-entry route asks for.
	untruncated bool
	// shimRows answers whether the shim's subprocess rows are entries. They are only when
	// the agent's own stream is not there: the shim runs on every shell in the run, its own
	// bootstrap included, so its rows are a record of the shell rather than of the agent —
	// and a row is not a message. With the stream missing there is nothing else to read, and
	// the shim's record is exactly the evidence a seam needs.
	shimRows bool
}

// pick answers the ready source whose next entry has the earliest clock. Ties go to the
// source listed first in the walk, so two entries the record cannot order keep one order on
// every page and every read.
func (w *transcriptWalk) pick() *transcriptSource {
	var best *transcriptSource
	for _, source := range w.sources {
		if !source.advance() {
			continue
		}
		if best == nil || source.at() < best.at() {
			best = source
		}
	}
	return best
}

// cursor captures the next unconsumed entry of every source, including held values.
func (w *transcriptWalk) cursor(seq int) transcriptCursor {
	out := transcriptCursor{Seq: seq}
	for _, source := range w.sources {
		at, index := source.offset, 0
		if source.held != nil {
			at, index = source.held.start, source.held.next
		}
		switch source.name {
		case transcriptSourceLog:
			out.Log, out.LogIndex = at, index
		case transcriptSourceSteps:
			out.Steps, out.StepsIndex = at, index
		case transcriptSourceProc:
			out.Proc, out.ProcIndex = at, index
		}
	}
	return out
}

// take emits the next entry of one source, with its sequence number, its body budget and the
// cursors on either side of it.
func (w *transcriptWalk) take(source *transcriptSource) (transcriptEntry, transcriptCursor) {
	line := source.held
	entry := line.entries[line.next]
	entry.Seq = w.seq
	entry.ID = fmt.Sprintf("%s:%d:%d", source.name, line.start, line.next)
	entry.start = w.prev
	if !w.untruncated {
		if len(entry.Text) > transcriptEntryBytes {
			entry.Text = truncateUTF8(entry.Text, transcriptEntryBytes)
			entry.Truncated = true
		}
		if entry.Tool != nil && len(entry.Tool.Arguments) > transcriptEntryBytes {
			entry.Tool.Arguments = truncateUTF8(entry.Tool.Arguments, transcriptEntryBytes)
			entry.Truncated = true
		}
	}
	line.next++
	last := line.next >= len(line.entries)
	if last {
		source.offset = line.end
		source.held = nil
	}
	w.seq++
	after := w.cursor(w.seq)
	entry.end = after
	w.prev = after
	return entry, after
}

// transcriptPage is one page of entries with the cursors around it.
type transcriptPage struct {
	entries []transcriptEntry
	cursor  transcriptCursor
	before  *transcriptCursor
	hasMore bool
}

// run collects one page.
func (w *transcriptWalk) run() transcriptPage {
	page := transcriptPage{cursor: w.start}
	page.cursor.Seq = w.seq
	w.prev = w.start
	w.prev.Seq = w.seq
	kept := make([]transcriptEntry, 0, w.limit)
	body := 0
	for {
		// The cursor a page was cut at is a point the walk passes through, and the walk passes
		// through positions no entry has an end for — a skipped shim row's end among them. The
		// check is here, at the top, so every step of the walk can reach it.
		if w.stopAt != nil && w.stopAt.Boundary == "" && w.prev == *w.stopAt {
			page.hasMore = true
			break
		}
		source := w.pick()
		if source == nil {
			// A missing backward anchor is not permission to return the tail.
			if w.stopAt != nil && w.stopAt.Boundary != "" {
				kept = nil
			}
			break
		}
		if w.stopAt != nil && w.stopAt.Boundary == fmt.Sprintf("%s:%d:%d", source.name, source.held.start, source.held.next) {
			page.hasMore = true
			break
		}
		entry, after := w.take(source)
		if entry.Kind == entryProc && !w.shimRows {
			// the shim's row: a shell command the agent's stream already describes as a tool
			// call, so it is not a line of the transcript. The source panel reports it, and it
			// becomes the transcript when the stream is missing.
			continue
		}
		kept = append(kept, entry)
		body += len(entry.Text)
		page.cursor = after
		if (w.stopID != "" && entry.ID == w.stopID) || (w.stopID == "" && w.stopSeq != nil && entry.Seq == *w.stopSeq) {
			// the single-entry page is the entry itself, not the walk up to it
			page.hasMore = true
			kept = kept[len(kept)-1:]
			break
		}
		if w.keepLast {
			if len(kept) > w.limit {
				kept = kept[1:]
			}
			continue
		}
		if w.stopSeq == nil && (len(kept) >= w.limit || body >= transcriptPageBytes) {
			page.hasMore = true
			break
		}
	}
	if w.keepLast && len(kept) > 0 {
		start := kept[0].start
		start.Boundary = kept[0].ID
		if !start.atHead() {
			page.before = &start
		}
	}
	if w.stopID != "" && (len(kept) != 1 || kept[0].ID != w.stopID) {
		kept = nil
	}
	page.entries = kept
	return page
}

// transcript answers one page of one run's transcript.
func (obs *observations) transcript(run storeRun, query transcriptQuery) transcriptData {
	out := transcriptData{Run: run.RunID, Entries: []transcriptEntry{}, Limit: query.Limit}
	if out.Limit <= 0 {
		out.Limit = transcriptPageLimit
	}
	if out.Limit > transcriptPageMax {
		out.Limit = transcriptPageMax
	}

	start := transcriptCursor{}
	if query.After != nil {
		start = *query.After
	}
	walk, sources, note, state := obs.openTranscript(run, start)
	defer func() {
		for _, source := range sources {
			source.close()
		}
	}()
	out.Sources = transcriptSourcesOf(sources)
	out.EpistemicState = state
	out.Note = note

	walk.limit = out.Limit
	walk.untruncated = query.Untruncated
	walk.shimRows = state != stateObserved
	switch {
	case query.EntryID != "":
		walk.stopID = query.EntryID
		walk.keepLast = true
		walk.limit = 1
	case query.Entry != nil:
		// one entry, answered whole: the walk stops at its sequence number and keeps only
		// that entry, because the page is the entry and not the road to it
		walk.stopSeq = query.Entry
		walk.keepLast = true
		walk.limit = 1
	case query.After != nil:
		walk.start = *query.After
		walk.seq = query.After.Seq
	case query.Before != nil:
		walk.keepLast = true
		walk.stopAt = query.Before
	default:
		walk.keepLast = true
	}
	page := walk.run()
	out.Entries = page.entries
	out.Cursor = page.cursor.encode()
	out.HasMore = page.hasMore
	if page.before != nil && !page.before.atHead() {
		encoded := page.before.encode()
		out.Before = &encoded
	}
	return out
}

// openTranscript opens the run's three records and answers the walk over them, plus what the
// transcript's own provenance is. A run whose harness stream is absent is a seam, not an
// empty transcript: the step and subprocess records still say what ran.
func (obs *observations) openTranscript(run storeRun, start transcriptCursor) (*transcriptWalk, []*transcriptSource, string, string) {
	runDir := obs.runDir(run.RunID)
	paths := []struct {
		name   string
		path   string
		offset int64
		index  int
	}{
		{transcriptSourceSteps, filepath.Join(runDir, "steps.jsonl"), start.Steps, start.StepsIndex},
		{transcriptSourceLog, runLogPath(obs.laneRoot, run.LaneID, run.RunID), start.Log, start.LogIndex},
		{transcriptSourceProc, filepath.Join(runDir, "proc-exec.jsonl"), start.Proc, start.ProcIndex},
	}
	sources := make([]*transcriptSource, 0, len(paths))
	for _, p := range paths {
		source := openTranscriptSource(p.name, p.path, p.offset)
		source.resumeIndex = p.index
		sources = append(sources, source)
	}
	walk := &transcriptWalk{sources: sources}

	// The transcript's own provenance is the harness's stream: the other two records are the
	// supervisor's view of what ran, and the agent's own words are what a transcript is.
	log := sources[1]
	switch log.state {
	case stateAbsent:
		return walk, sources,
			"the agent's own stream is absent: its messages, its thinking and its tool calls are unobserved, and what follows is the step and subprocess record that survives",
			stateUnobservedSeam
	case stateEmpty:
		return walk, sources,
			"the agent's own stream holds no bytes: the run started and recorded nothing, which is a gap and not an agent that did nothing",
			stateUnobservedSeam
	case stateUnreadable:
		return walk, sources,
			"the agent's own stream could not be read: " + log.note, stateUnreadable
	}
	return walk, sources, "", stateObserved
}

// openTranscriptSource opens one record, or answers why it is not one this face can read. A
// file that is not there is an absence to name, not a failure to hide.
func openTranscriptSource(name, path string, offset int64) *transcriptSource {
	// a source with no readable file is exhausted before it begins: a seam, not a failure
	source := &transcriptSource{name: name, path: path, state: stateAbsent, done: true}
	if path == "" {
		source.note = "the run names no lane, so its stream has no path to be at"
		return source
	}
	info, err := os.Stat(path)
	switch {
	case err != nil:
		source.note = "no writer produced this file at this path"
		return source
	case info.IsDir():
		source.state = stateUnreadable
		source.note = "this path is a directory, not the record it names"
		return source
	case info.Size() == 0:
		source.state = stateEmpty
		source.note = "the file is there and holds no bytes"
	}
	stream, err := openJSONStream(path, offset)
	if err != nil {
		source.state = stateUnreadable
		source.note = err.Error()
		return source
	}
	// a file that is there and empty keeps saying so: it was opened so its lines could be
	// read (there are none), and that is not the same fact as a file with content
	if source.state == stateAbsent {
		source.state = statePresent
	}
	source.done = false
	source.offset = offset
	source.stream = stream
	return source
}

// transcriptSourcesOf is the three sources as the response reports them.
func transcriptSourcesOf(sources []*transcriptSource) transcriptSources {
	out := transcriptSources{}
	for _, source := range sources {
		state := sourceState{Path: source.path, State: source.state, Note: source.note}
		switch source.name {
		case transcriptSourceLog:
			out.Log = state
		case transcriptSourceSteps:
			out.Steps = state
		case transcriptSourceProc:
			out.Proc = state
		}
	}
	return out
}
