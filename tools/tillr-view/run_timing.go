package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

// A run's wall time, split into where it went: the model thinking, tools running, the
// part of tool time spent waiting for a test slot, and remaining uncovered harness time.
// Every figure is read from the run's own records and states its provenance; a missing
// record answers ms: null ("not recorded") and never zero, because zero is a measurement.
//
//	<lane-root>/<lane-id>/<run-id>.log   harness stream (Pi `--mode json` or Claude `stream-json`)
//	<loop-dir>/<run-id>/proc-exec.jsonl  subprocess intervals when the log lacks tool durations
//	<loop-dir>/<run-id>/slot-waits.jsonl one `slot.wait` row per test-slot acquisition
//
// Concurrent intervals are unioned before they are summed so parallel tool calls and
// concurrent shard threads never add up to more than the wall clock they spanned.
const (
	// stateDerived is a figure computed from recorded intervals or clocks by this reader.
	stateDerived = "derived"
	// stateLowerBound is a figure that is at least this much and may be higher.
	stateLowerBound = "lower_bound"
)

// timingMetric is one figure of the split. MS is null when the record is missing.
type timingMetric struct {
	MS             *int64 `json:"ms"`
	EpistemicState string `json:"epistemic_state"`
	Source         string `json:"source,omitempty"`
	Note           string `json:"note,omitempty"`
}

// runTiming is the per-run wall-time split. ToolMS includes time a tool waited for a test
// slot; ToolActiveMS subtracts the slot wait; UncoveredMS is wall time outside model and tool work.
type runTiming struct {
	WallMS       timingMetric `json:"wall_ms"`
	ModelMS      timingMetric `json:"model_ms"`
	ToolMS       timingMetric `json:"tool_ms"`
	ToolActiveMS timingMetric `json:"tool_active_ms"`
	SlotWaitMS   timingMetric `json:"slot_wait_ms"`
	UncoveredMS  timingMetric `json:"uncovered_ms"`
}

type msInterval struct{ start, end int64 }

// mergeIntervals sorts and coalesces overlapping or touching windows.
func mergeIntervals(in []msInterval) []msInterval {
	valid := make([]msInterval, 0, len(in))
	for _, i := range in {
		if i.end > i.start {
			valid = append(valid, i)
		}
	}
	sort.Slice(valid, func(a, b int) bool { return valid[a].start < valid[b].start })
	var out []msInterval
	for _, i := range valid {
		if n := len(out); n > 0 && i.start <= out[n-1].end {
			if i.end > out[n-1].end {
				out[n-1].end = i.end
			}
			continue
		}
		out = append(out, i)
	}
	return out
}

// clipIntervals restricts windows to [minStart, maxEnd] when minStart > 0 && maxEnd >= minStart.
func clipIntervals(in []msInterval, minStart, maxEnd int64) []msInterval {
	if minStart <= 0 || maxEnd < minStart {
		return in
	}
	out := make([]msInterval, 0, len(in))
	for _, iv := range in {
		s, e := iv.start, iv.end
		if s < minStart {
			s = minStart
		}
		if e > maxEnd {
			e = maxEnd
		}
		if e > s {
			out = append(out, msInterval{s, e})
		}
	}
	return out
}

func sumIntervals(in []msInterval) int64 {
	var total int64
	for _, i := range in {
		total += i.end - i.start
	}
	return total
}

// subtractIntervals removes merged b from merged a.
func subtractIntervals(a, b []msInterval) []msInterval {
	var out []msInterval
	for _, x := range a {
		cur := x.start
		for _, y := range b {
			if y.end <= cur || y.start >= x.end {
				continue
			}
			if y.start > cur {
				out = append(out, msInterval{cur, y.start})
			}
			if y.end > cur {
				cur = y.end
			}
		}
		if cur < x.end {
			out = append(out, msInterval{cur, x.end})
		}
	}
	return out
}

// slotNotice matches line-anchored `go-test-shards: waiting for a test slot after 35s: ...`
// and the initial untimed `<30s` notice `go-test-shards: waiting for a test slot: ...`.
// Line-anchoring plus shell-tool scoping avoids mistaking source files or test fixtures read
// by the agent for real shard-runner stderr notices.
var slotNotice = regexp.MustCompile(`(?m)^go-test-shards: [^\r\n"]*?waiting for a test slot(?: after ([0-9]+)s)?:`)

func isShellToolName(name string) bool {
	switch strings.ToLower(strings.TrimSpace(name)) {
	case "", "bash", "sh", "shell", "exec", "exec_command", "run_terminal_cmd", "background_task":
		return true
	default:
		return false
	}
}

func metric(ms int64, state, source, note string) timingMetric {
	return timingMetric{MS: &ms, EpistemicState: state, Source: source, Note: note}
}

func absentMetric(note string) timingMetric {
	return timingMetric{EpistemicState: stateAbsent, Note: note}
}

// slotNoticeBound finds the longest `after <elapsed>s` notice in one tool result, and
// distinguishes timed (`after Ns`, N > 0) from untimed (`<30s`, no elapsed figure) notices.
func slotNoticeBound(text string) (longestMS int64, timedSeen bool, untimedSeen bool) {
	for _, m := range slotNotice.FindAllStringSubmatch(text, -1) {
		if m[1] != "" {
			if s, err := strconv.ParseInt(m[1], 10, 64); err == nil && s > 0 {
				timedSeen = true
				if s*1000 > longestMS {
					longestMS = s * 1000
				}
				continue
			}
		}
		untimedSeen = true
	}
	return longestMS, timedSeen, untimedSeen
}

// slotWaitRow is one `slot.wait` line from `<loop-dir>/<run-id>/slot-waits.jsonl`.
type slotWaitRow struct {
	Kind     string  `json:"kind"`
	TS       string  `json:"ts"`
	TSEnd    string  `json:"ts_end"`
	StartMS  int64   `json:"ts_start_ms"`
	EndMS    int64   `json:"ts_end_ms"`
	WaitedMS int64   `json:"waited_ms"`
	Slot     *string `json:"slot"`
	Class    string  `json:"class"`
	Run      string  `json:"run"`
	Label    string  `json:"label"`
	Acquired bool    `json:"acquired"`
	Raw      string  `json:"-"`
}

// readSlotWaitRows reads structured `slot.wait` spans emitted by `tools/go-test-shards.py`.
func readSlotWaitRows(path string) ([]slotWaitRow, bool) {
	if path == "" {
		return nil, false
	}
	stream, err := openJSONStream(path, 0)
	if err != nil {
		return nil, false
	}
	defer stream.close()
	var out []slotWaitRow
	seen := false
	for {
		raw, _, err := stream.next()
		if err != nil {
			return out, seen
		}
		var row slotWaitRow
		if json.Unmarshal(raw, &row) != nil || row.Kind != "slot.wait" {
			continue
		}
		seen = true
		if row.StartMS <= 0 && row.TS != "" {
			if ms, ok := parseISOClock(row.TS); ok {
				row.StartMS = ms
			}
		}
		if row.EndMS <= 0 && row.TSEnd != "" {
			if ms, ok := parseISOClock(row.TSEnd); ok {
				row.EndMS = ms
			}
		}
		if row.StartMS <= 0 && row.EndMS > 0 {
			row.StartMS = row.EndMS - row.WaitedMS
		}
		if row.EndMS <= 0 && row.StartMS > 0 {
			row.EndMS = row.StartMS + row.WaitedMS
		}
		row.Raw = strings.TrimSpace(string(raw))
		out = append(out, row)
	}
}

type procTiming struct {
	present         bool
	rowsSeen        int
	intervals       []msInterval
	shardRunnerSeen bool
}

func readProcTiming(path string) procTiming {
	var out procTiming
	if path == "" {
		return out
	}
	stream, err := openJSONStream(path, 0)
	if err != nil {
		return out
	}
	defer stream.close()
	out.present = true
	for {
		raw, _, err := stream.next()
		if err != nil {
			return out
		}
		var row procLine
		if json.Unmarshal(raw, &row) != nil {
			continue
		}
		out.rowsSeen++
		if strings.Contains(row.Cmd, "go-test-shards") {
			out.shardRunnerSeen = true
		}
		startMS, startOK := parseISOClock(row.TS)
		endMS, endOK := parseISOClock(row.TSEnd)
		switch {
		case startOK && endOK && endMS >= startMS:
			out.intervals = append(out.intervals, msInterval{startMS, endMS})
		case endOK && row.DurationMS > 0:
			out.intervals = append(out.intervals, msInterval{endMS - row.DurationMS, endMS})
		case startOK && row.DurationMS > 0:
			out.intervals = append(out.intervals, msInterval{startMS, startMS + row.DurationMS})
		}
	}
}

type timingLogMessage struct {
	logMessage
	ResponseID       string `json:"responseId"`
	RequestStartedAt int64  `json:"requestStartedAt"`
}

type timingToolResult struct {
	Content logContent `json:"content"`
}

type timingLogEvent struct {
	Type          string             `json:"type"`
	Timestamp     json.RawMessage    `json:"timestamp"`
	Message       *timingLogMessage  `json:"message"`
	ToolResults   []timingLogMessage `json:"toolResults"`
	ToolCallID    string             `json:"toolCallId"`
	ToolName      string             `json:"toolName"`
	DurationMS    int64              `json:"durationMs"`
	Result        *timingToolResult  `json:"result"`
	DurationAPIMS *int64             `json:"duration_api_ms"`
}

type recordedToolSpan struct {
	interval msInterval
	anchored bool
	duration int64
}

type logTiming struct {
	present            bool
	lines              int
	model              []msInterval
	claudeDerivedModel []msInterval
	tool               []msInterval
	unanchoredToolMS   int64
	claudeAPI          int64
	claudeAPISeen      bool
	noticeBound        int64
	timedNoticeSeen    bool
	untimedNoticeSeen  bool
	turnsSeen          bool
	toolCallsSeen      int
	toolResultsSeen    int
	toolInputsObserved bool
	shardRunnerSeen    bool
	claudeStyle        bool
}

func readLogTiming(path string, runStartMS int64) logTiming {
	out := logTiming{}
	if path == "" {
		return out
	}
	stream, err := openJSONStream(path, 0)
	if err != nil {
		return out
	}
	defer stream.close()
	out.present = true

	seenTurns := map[string]bool{}
	piToolStart := map[string]int64{}
	piToolNames := map[string]string{}
	piToolSpans := map[string]recordedToolSpan{}
	var unkeyedTools []msInterval
	seenNotices := map[string]int64{}
	seenUntimed := map[string]bool{}
	claudeToolOpen := map[string]int64{}
	claudeToolNames := map[string]string{}
	var lastClock int64
	claudeAnchor := runStartMS

	recordNotice := func(callID, text string) {
		bound, timed, untimed := slotNoticeBound(text)
		if !timed && !untimed {
			return
		}
		if callID == "" {
			if timed {
				out.timedNoticeSeen = true
				out.noticeBound += bound
			} else {
				out.untimedNoticeSeen = true
			}
			return
		}
		if timed {
			out.timedNoticeSeen = true
			if prev := seenNotices[callID]; bound > prev {
				out.noticeBound += bound - prev
				seenNotices[callID] = bound
			}
		} else if !seenUntimed[callID] && seenNotices[callID] == 0 {
			seenUntimed[callID] = true
			out.untimedNoticeSeen = true
		}
	}

	recordPiAssistant := func(m *timingLogMessage) {
		out.turnsSeen = true
		start := m.Timestamp
		if m.RequestStartedAt > 0 {
			start = m.RequestStartedAt
		}
		if start > 0 {
			lastClock = start + m.DurationMS
		}
		for _, block := range m.Content.Blocks {
			if block.Type == "toolCall" {
				out.toolCallsSeen++
				if strings.Contains(string(block.Arguments), "go-test-shards") || strings.Contains(block.Name, "go-test-shards") {
					out.shardRunnerSeen = true
				}
				if block.ID != "" {
					piToolNames[block.ID] = block.Name
					if lastClock > 0 {
						piToolStart[block.ID] = lastClock
					}
				}
			}
		}
		if start > 0 && m.DurationMS > 0 {
			key := m.ResponseID
			if key == "" {
				key = fmt.Sprintf("%d:%d", start, m.DurationMS)
			}
			if !seenTurns[key] {
				seenTurns[key] = true
				out.model = append(out.model, msInterval{start, start + m.DurationMS})
			}
		}
	}

	recordPiToolResult := func(callID, toolName string, timestamp, durationMS int64, text string) {
		out.toolResultsSeen++
		if toolName == "" {
			toolName = piToolNames[callID]
		}
		if isShellToolName(toolName) {
			recordNotice(callID, text)
		}
		if durationMS <= 0 {
			return
		}
		if callID == "" {
			if timestamp > 0 {
				unkeyedTools = append(unkeyedTools, msInterval{timestamp - durationMS, timestamp})
			} else if lastClock > 0 {
				unkeyedTools = append(unkeyedTools, msInterval{lastClock, lastClock + durationMS})
			} else {
				out.unanchoredToolMS += durationMS
			}
			return
		}
		prev, exists := piToolSpans[callID]
		if timestamp > 0 {
			piToolSpans[callID] = recordedToolSpan{
				interval: msInterval{timestamp - durationMS, timestamp},
				anchored: true,
				duration: durationMS,
			}
			if timestamp > lastClock {
				lastClock = timestamp
			}
			return
		}
		if exists && prev.anchored {
			return
		}
		if anchor := piToolStart[callID]; anchor > 0 {
			piToolSpans[callID] = recordedToolSpan{
				interval: msInterval{anchor, anchor + durationMS},
				anchored: true,
				duration: durationMS,
			}
			return
		}
		if lastClock > 0 {
			piToolSpans[callID] = recordedToolSpan{
				interval: msInterval{lastClock, lastClock + durationMS},
				anchored: true,
				duration: durationMS,
			}
			return
		}
		piToolSpans[callID] = recordedToolSpan{duration: durationMS}
	}

	for {
		raw, _, err := stream.next()
		if err != nil {
			break
		}
		var event timingLogEvent
		if json.Unmarshal(raw, &event) != nil {
			continue
		}
		out.lines++
		probe := logEvent{Type: event.Type, Timestamp: event.Timestamp}
		if event.Message != nil {
			probe.Message = &event.Message.logMessage
		}
		if at, ok := logClock(&probe); ok && at > lastClock {
			lastClock = at
		}

		switch event.Type {
		case "message_end", "message":
			if event.Message == nil {
				continue
			}
			switch event.Message.Role {
			case "assistant":
				recordPiAssistant(event.Message)
			case "toolResult":
				m := event.Message
				recordPiToolResult(m.ToolCallID, m.ToolName, m.Timestamp, m.DurationMS, m.Content.Text+"\n"+blocksText(m.Content.Blocks))
			}
		case "turn_end":
			if event.Message != nil && event.Message.Role == "assistant" {
				recordPiAssistant(event.Message)
			}
			for i := range event.ToolResults {
				tr := &event.ToolResults[i]
				recordPiToolResult(tr.ToolCallID, tr.ToolName, tr.Timestamp, tr.DurationMS, tr.Content.Text+"\n"+blocksText(tr.Content.Blocks))
			}
		case "tool_execution_end":
			var text string
			if event.Result != nil {
				text = event.Result.Content.Text + "\n" + blocksText(event.Result.Content.Blocks)
			}
			var stamp int64
			if at, ok := logClock(&probe); ok {
				stamp = at
			}
			recordPiToolResult(event.ToolCallID, event.ToolName, stamp, event.DurationMS, text)
		case "system":
			if at, ok := logClock(&probe); ok {
				claudeAnchor = at
			}
		case "assistant", "user":
			if event.Message == nil {
				continue
			}
			out.claudeStyle = true
			at, ok := logClock(&probe)
			if event.Type == "assistant" {
				out.turnsSeen = true
				if ok {
					if claudeAnchor > 0 && at > claudeAnchor {
						out.claudeDerivedModel = append(out.claudeDerivedModel, msInterval{claudeAnchor, at})
					}
					claudeAnchor = at
				}
			} else if ok {
				claudeAnchor = at
			}
			for _, block := range event.Message.Content.Blocks {
				switch block.Type {
				case "tool_use":
					out.toolCallsSeen++
					if block.ID != "" {
						claudeToolNames[block.ID] = block.Name
					}
					if strings.Contains(string(block.Input), "go-test-shards") || strings.Contains(block.Name, "go-test-shards") {
						out.shardRunnerSeen = true
					}
					if ok {
						claudeToolOpen[block.ID] = at
					}
				case "tool_result":
					out.toolResultsSeen++
					if start, open := claudeToolOpen[block.ToolUseID]; open && ok && at >= start {
						out.tool = append(out.tool, msInterval{start, at})
					}
					if isShellToolName(claudeToolNames[block.ToolUseID]) {
						recordNotice(block.ToolUseID, claudeResultText(block.Result))
					}
				}
			}
		case "result":
			if event.DurationAPIMS != nil && *event.DurationAPIMS >= 0 {
				out.claudeAPI, out.claudeAPISeen = *event.DurationAPIMS, true
			}
		}
	}

	out.tool = append(out.tool, unkeyedTools...)
	toolIDs := make([]string, 0, len(piToolSpans))
	for id := range piToolSpans {
		toolIDs = append(toolIDs, id)
	}
	sort.Strings(toolIDs)
	for _, id := range toolIDs {
		sp := piToolSpans[id]
		if sp.anchored {
			out.tool = append(out.tool, sp.interval)
		} else {
			out.unanchoredToolMS += sp.duration
		}
	}
	out.toolInputsObserved = out.toolCallsSeen > 0 && out.toolCallsSeen >= out.toolResultsSeen
	return out
}

func usageTurnIntervals(turns []turnUsage) []msInterval {
	var out []msInterval
	for _, t := range turns {
		if t.StartedMS != nil && t.FinishedMS != nil {
			s, e := int64(*t.StartedMS), int64(*t.FinishedMS)
			if e > s {
				out = append(out, msInterval{s, e})
			}
		}
	}
	return out
}

// computeRunTiming assembles the wall-time split for one run. `now` is the clock an
// unfinished run's wall time is measured against.
func computeRunTiming(run storeRun, usage runUsage, laneRoot, loopDir string, now int64) runTiming {
	var t runTiming
	var runEnd int64
	switch {
	case run.StartedAtMS > 0 && run.FinishedAtMS >= run.StartedAtMS:
		runEnd = run.FinishedAtMS
		t.WallMS = metric(run.FinishedAtMS-run.StartedAtMS, stateObserved, "runs.started_at..finished_at", "")
	case run.StartedAtMS > 0 && run.FinishedAtMS == 0 && now >= run.StartedAtMS:
		runEnd = now
		t.WallMS = metric(now-run.StartedAtMS, stateDerivedClock, "runs.started_at..now", "the run has not finished; measured to the present")
	default:
		t.WallMS = absentMetric("the run's start or finish time is not recorded")
	}

	path := runLogPath(laneRoot, run.LaneID, run.RunID)
	if run.TranscriptPath != "" {
		path = run.TranscriptPath
	}
	log := readLogTiming(path, run.StartedAtMS)
	var proc procTiming
	if loopDir != "" {
		proc = readProcTiming(filepath.Join(loopDir, run.RunID, "proc-exec.jsonl"))
	}

	missing := "the run's log is not present, so its model and tool time are not recorded"
	if run.Backend == "command" {
		missing = "a command run has no model; its tool time is not recorded in a log"
	} else if log.present && log.lines == 0 {
		missing = "the run's log holds no lines, so its model and tool time are not recorded"
	}

	var modelWindows []msInterval
	usageWindows := usageTurnIntervals(usage.Turns)
	switch {
	case log.claudeAPISeen:
		t.ModelMS = metric(log.claudeAPI, stateObserved, "claude result duration_api_ms", "")
	case len(log.model) > 0:
		modelWindows = mergeIntervals(log.model)
		t.ModelMS = metric(sumIntervals(modelWindows), stateObserved, "assistant message durationMs", "")
	case len(usageWindows) > 0:
		modelWindows = mergeIntervals(usageWindows)
		t.ModelMS = metric(sumIntervals(modelWindows), stateObserved, "run.finish usage turns", "")
	case len(log.claudeDerivedModel) > 0:
		modelWindows = mergeIntervals(log.claudeDerivedModel)
		t.ModelMS = metric(sumIntervals(modelWindows), stateDerived, "claude assistant turn timestamps",
			"derived from turn timestamps because the Claude log has no closing result line yet")
	case !log.present || log.lines == 0:
		t.ModelMS = absentMetric(missing)
	default:
		t.ModelMS = absentMetric("the log records no model turn timings (a Claude run records exact API time on its closing result line)")
	}

	var toolWindows []msInterval
	switch {
	case len(log.tool) > 0 || log.unanchoredToolMS > 0:
		toolWindows = mergeIntervals(log.tool)
		state, source := stateObserved, "toolResult durationMs"
		if log.claudeStyle && log.unanchoredToolMS == 0 {
			state, source = stateDerived, "tool_use to tool_result timestamps"
		}
		t.ToolMS = metric(sumIntervals(toolWindows)+log.unanchoredToolMS, state, source, "")
	case len(proc.intervals) > 0:
		toolWindows = mergeIntervals(clipIntervals(proc.intervals, run.StartedAtMS, runEnd))
		t.ToolMS = metric(sumIntervals(toolWindows), stateDerived, "proc-exec.jsonl",
			"derived from subprocess intervals because the run log carries no tool durations")
	case log.present && log.turnsSeen && log.toolCallsSeen == 0 && log.toolResultsSeen == 0 && (len(modelWindows) > 0 || log.claudeAPISeen):
		t.ToolMS = metric(0, stateObserved, "log", "the log records no tool call")
	default:
		t.ToolMS = absentMetric(missing)
	}

	var slotPath string
	if loopDir != "" {
		slotPath = filepath.Join(loopDir, run.RunID, "slot-waits.jsonl")
	}
	var slotWindows []msInterval
	slotRows, slotSeen := readSlotWaitRows(slotPath)
	switch {
	case slotSeen:
		rawWaits := make([]msInterval, 0, len(slotRows))
		for _, r := range slotRows {
			rawWaits = append(rawWaits, msInterval{r.StartMS, r.EndMS})
		}
		slotWindows = mergeIntervals(clipIntervals(rawWaits, run.StartedAtMS, runEnd))
		waited := sumIntervals(slotWindows)
		note := ""
		if waited == 0 {
			note = "every recorded slot acquisition was immediate"
		}
		t.SlotWaitMS = metric(waited, stateObserved, "slot-waits.jsonl", note)
	case log.timedNoticeSeen:
		t.SlotWaitMS = metric(log.noticeBound, stateLowerBound, "run log notices",
			"at least this long: the shard runner reports a wait only every 30 seconds, and the last notice is the last the log saw")
	case log.untimedNoticeSeen:
		t.SlotWaitMS = absentMetric("the log records a \"waiting for a test slot\" notice under 30 seconds with no elapsed figure, so the duration is not recorded")
	case slotPath != "" && fileExists(slotPath):
		t.SlotWaitMS = absentMetric("slot-waits.jsonl is present and holds no slot.wait row")
	case t.ToolMS.MS != nil && *t.ToolMS.MS == 0 && log.toolCallsSeen == 0 && !proc.shardRunnerSeen:
		t.SlotWaitMS = metric(0, stateObserved, "log", "no tool call ran go-test-shards")
	case (log.toolInputsObserved || (proc.present && proc.rowsSeen > 0)) && !log.shardRunnerSeen && !proc.shardRunnerSeen:
		source := "log"
		if proc.present && proc.rowsSeen > 0 && !log.toolInputsObserved {
			source = "proc-exec.jsonl"
		}
		t.SlotWaitMS = metric(0, stateObserved, source, "no observed command ran go-test-shards")
	case log.shardRunnerSeen || proc.shardRunnerSeen:
		t.SlotWaitMS = absentMetric("go-test-shards ran without slot-waits.jsonl or a timed wait notice, so test-slot wait time is not recorded")
	default:
		t.SlotWaitMS = absentMetric("this run recorded no slot wait: no slot-waits.jsonl, and its log has no \"waiting for a test slot\" notice, so a wait is neither shown nor ruled out")
	}

	switch {
	case t.ToolMS.MS == nil:
		t.ToolActiveMS = absentMetric("tool time is not recorded")
	case t.SlotWaitMS.MS == nil:
		t.ToolActiveMS = absentMetric("slot wait is not recorded, so tool time cannot be split; read tool time whole")
	case t.SlotWaitMS.EpistemicState != stateObserved:
		t.ToolActiveMS = absentMetric("slot wait is only a log lower bound without interval timestamps, so active tool time cannot be separated; read tool time whole")
	default:
		active := *t.ToolMS.MS - *t.SlotWaitMS.MS
		if len(slotWindows) > 0 && len(toolWindows) > 0 {
			active = sumIntervals(subtractIntervals(toolWindows, slotWindows)) + log.unanchoredToolMS
		}
		if active < 0 {
			active = 0
		}
		t.ToolActiveMS = metric(active, stateDerived, "tool_ms - slot_wait_ms", "tool time minus slot wait")
	}

	switch {
	case t.WallMS.MS == nil || t.ModelMS.MS == nil || t.ToolMS.MS == nil:
		t.UncoveredMS = absentMetric("wall, model or tool time is not recorded, so remaining wall time cannot be derived")
	case len(modelWindows) > 0 && len(toolWindows) > 0 && log.unanchoredToolMS == 0 && run.StartedAtMS > 0 && runEnd >= run.StartedAtMS:
		combined := make([]msInterval, 0, len(modelWindows)+len(toolWindows)+len(slotWindows))
		combined = append(combined, modelWindows...)
		combined = append(combined, toolWindows...)
		combined = append(combined, slotWindows...)
		covered := sumIntervals(mergeIntervals(clipIntervals(combined, run.StartedAtMS, runEnd)))
		uncovered := *t.WallMS.MS - covered
		if uncovered < 0 {
			uncovered = 0
		}
		t.UncoveredMS = metric(uncovered, stateDerived, "wall_ms - covered", "wall time outside recorded model and tool intervals")
	default:
		uncovered := *t.WallMS.MS - *t.ModelMS.MS - *t.ToolMS.MS
		if uncovered < 0 {
			uncovered = 0
		}
		t.UncoveredMS = metric(uncovered, stateDerived, "wall_ms - model_ms - tool_ms", "wall time outside recorded model and tool totals")
	}

	return t
}

func fileExists(path string) bool {
	_, err := os.Stat(path)
	return err == nil
}
