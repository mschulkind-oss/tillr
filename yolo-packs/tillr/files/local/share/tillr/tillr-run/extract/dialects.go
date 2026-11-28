package extract

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-run/run"
)

var (
	piUsageCounters     = []string{"input", "output", "cacheRead", "cacheWrite", "reasoning"}
	claudeUsageCounters = []string{"input_tokens", "cache_creation_input_tokens", "cache_read_input_tokens", "output_tokens"}
	agyUsageCounters    = []string{"input_tokens", "output_tokens", "thinking_tokens", "cache_read_tokens"}
	museUsageCounters   = []string{"input_tokens", "output_tokens", "cached_tokens", "reasoning_tokens"}
)

func compactJSON(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "null"
	}
	return strings.TrimSuffix(buf.String(), "\n")
}

func piBlocksText(message map[string]any) string {
	content, ok := message["content"].([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, raw := range content {
		block := asMap(raw)
		if block == nil {
			continue
		}
		if t, _ := asString(block["type"]); t == "text" {
			if txt, ok := asString(block["text"]); ok {
				b.WriteString(txt)
			}
		}
	}
	return b.String()
}

func piIsReasoningOnly(message map[string]any) bool {
	if message == nil {
		return false
	}
	content, ok := message["content"].([]any)
	if !ok {
		return false
	}
	if strings.TrimSpace(piBlocksText(message)) != "" {
		return false
	}
	for _, raw := range content {
		block := asMap(raw)
		if block != nil {
			if t, _ := asString(block["type"]); t == "toolCall" {
				return false
			}
		}
	}
	return true
}

func piCurrentText(deltas, settled, snapshotVals []string) string {
	if len(deltas) > 0 {
		return strings.Join(deltas, "")
	}
	if len(settled) > 0 {
		return strings.Join(settled, "")
	}
	return strings.Join(snapshotVals, "")
}

func piIsPromptMessage(message map[string]any) bool {
	if message == nil {
		return false
	}
	role, _ := asString(message["role"])
	if role == "user" {
		return true
	}
	if role == "custom" {
		ct, _ := asString(message["customType"])
		return ct != "memory-context"
	}
	return false
}

type piEnding struct {
	text          string
	lastAssistant map[string]any
}

func PiDialect(streamText string, liveProbe bool) DialectResult {
	records, unparseable := Records(streamText)
	var deltas []string
	var settled []string
	snapshotIdx := make(map[string]int)
	var snapshotVals []string

	var lastAssistant map[string]any
	var lastResultEnding *piEnding
	var postCompactionCandidate *piEnding
	var ended *EndedInfo

	var tokens int64
	var cost float64
	sawTokens := false
	sawCost := false
	var session string
	var firstAt, lastAt *float64
	hasParked := false

	for _, record := range records {
		for _, key := range []string{"sessionId", "session_id"} {
			if session == "" {
				if s, ok := asString(record[key]); ok {
					session = s
				}
			}
		}
		recordType, _ := asString(record["type"])
		if session == "" && recordType == "session" {
			if s, ok := asString(record["id"]); ok {
				session = s
			}
		}
		if session == "" {
			if sess := asMap(record["session"]); sess != nil {
				if s, ok := asString(sess["id"]); ok {
					session = s
				}
			}
		}
		if ts, ok := asFloat(record["timestamp"]); ok {
			micros := ts * 1000
			if firstAt == nil {
				v := micros
				firstAt = &v
			}
			v := micros
			lastAt = &v
		}

		if recordType == "turn_end" {
			if msg := asMap(record["message"]); msg != nil {
				if usage := asMap(msg["usage"]); usage != nil {
					var total *float64
					if t, ok := asFloat(usage["totalTokens"]); ok {
						total = &t
					} else {
						var sum float64
						saw := false
						for _, key := range piUsageCounters {
							if v, ok := asFloat(usage[key]); ok {
								sum += v
								saw = true
							}
						}
						if saw {
							total = &sum
						}
					}
					if total != nil {
						tokens += int64(*total)
						sawTokens = true
					}
					if costMap := asMap(usage["cost"]); costMap != nil {
						if billed, ok := asFloat(costMap["total"]); ok {
							cost += billed
							sawCost = true
						}
					}
				}
			}
		}

		if recordType == "loop_detected" {
			if action, _ := asString(record["action"]); action != "recovered" {
				argsJSON := compactJSON(record["args"])
				tool, _ := asString(record["toolName"])
				if tool == "" {
					tool = "(no tool)"
				}
				streak := "None"
				if st, ok := asFloat(record["streak"]); ok {
					streak = strconv.FormatInt(int64(st), 10)
				}
				ended = &EndedInfo{
					Cause: "repeating-call",
					Detail: fmt.Sprintf(
						"pi ended the session because the model repeated one identical call %s times in a row: %s %s",
						streak, tool, argsJSON,
					),
				}
			}
		}

		switch recordType {
		case "compaction_start":
			reason, _ := asString(record["reason"])
			if reason != "overflow" && lastResultEnding != nil && postCompactionCandidate == nil {
				postCompactionCandidate = lastResultEnding
			}
		case "compaction_end":
			willRetry, _ := record["willRetry"].(bool)
			reason, _ := asString(record["reason"])
			if willRetry || reason == "overflow" {
				postCompactionCandidate = nil
			} else if lastResultEnding != nil && postCompactionCandidate == nil {
				postCompactionCandidate = lastResultEnding
			}
		case "parked":
			hasParked = true
			postCompactionCandidate = nil
		}

		if postCompactionCandidate != nil {
			switch recordType {
			case "message", "message_start", "message_end", "turn_end":
				if piIsPromptMessage(asMap(record["message"])) {
					postCompactionCandidate = nil
				}
			case "agent_end":
				if msgs, ok := record["messages"].([]any); ok {
					for _, raw := range msgs {
						if piIsPromptMessage(asMap(raw)) {
							postCompactionCandidate = nil
							break
						}
					}
				}
			}
		}

		if update := asMap(record["assistantMessageEvent"]); update != nil {
			if ut, _ := asString(update["type"]); ut == "text_delta" {
				if d, ok := asString(update["delta"]); ok {
					deltas = append(deltas, d)
				}
			}
		}
		if recordType == "agent_end" {
			if msgs, ok := record["messages"].([]any); ok {
				for _, raw := range msgs {
					msg := asMap(raw)
					if msg != nil {
						if role, _ := asString(msg["role"]); role == "assistant" {
							lastAssistant = msg
							settled = append(settled, piBlocksText(msg))
						}
					}
				}
			}
			if postCompactionCandidate == nil {
				endingText := piCurrentText(deltas, settled, snapshotVals)
				if !piIsReasoningOnly(lastAssistant) && FooterResult(endingText) != "" {
					lastResultEnding = &piEnding{text: endingText, lastAssistant: lastAssistant}
				} else {
					lastResultEnding = nil
				}
			}
		}
		if msg := asMap(record["message"]); msg != nil {
			if role, _ := asString(msg["role"]); role == "assistant" {
				if recordType == "message" || recordType == "message_end" || recordType == "turn_end" {
					lastAssistant = msg
				} else {
					lastAssistant = nil
				}
				key, _ := asString(msg["id"])
				if key == "" {
					key = fmt.Sprintf("snapshot-%d", len(snapshotVals))
				}
				text := piBlocksText(msg)
				if idx, exists := snapshotIdx[key]; exists {
					snapshotVals[idx] = text
				} else {
					snapshotIdx[key] = len(snapshotVals)
					snapshotVals = append(snapshotVals, text)
				}
			}
		}
	}

	var text string
	if postCompactionCandidate != nil {
		text = postCompactionCandidate.text
		lastAssistant = postCompactionCandidate.lastAssistant
	} else {
		text = piCurrentText(deltas, settled, snapshotVals)
	}

	if stop, _ := asString(lastAssistant["stopReason"]); stop == "error" && !hasParked {
		// The provider refused the request: say so, rather than calling the turn empty.
		if ended == nil {
			message, _ := asString(lastAssistant["errorMessage"])
			if message = strings.TrimSpace(message); message == "" {
				message = "the provider reported an error without a message"
			}
			ended = &EndedInfo{Cause: "provider-error", Detail: message}
		}
	} else if piIsReasoningOnly(lastAssistant) {
		text = "[Pi's last assistant turn had no text or tool call.]\n"
	}
	if liveProbe && hasParked {
		text += "\n[land call parked; waiting for the harness to exit]"
	}
	if ended != nil {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += fmt.Sprintf("[%s]\n", ended.Detail)
	}

	var startedStr, finishedStr string
	if firstAt != nil {
		startedStr = MicrosToRFC3339(*firstAt)
	}
	if lastAt != nil {
		finishedStr = MicrosToRFC3339(*lastAt)
	}
	var tokensPtr *int64
	if sawTokens {
		tokensPtr = &tokens
	}
	var costPtr *float64
	if sawCost {
		costPtr = &cost
	}

	return DialectResult{
		Ended:        ended,
		UsageDetails: Details(records, "pi"),
		Text:         text,
		Session:      session,
		Started:      startedStr,
		Finished:     finishedStr,
		Events:       len(records),
		Unparseable:  unparseable,
		Tokens:       tokensPtr,
		Cost:         costPtr,
	}
}

func claudeUsageTotal(usage map[string]any) *int64 {
	if usage == nil {
		return nil
	}
	var sum float64
	saw := false
	for _, key := range claudeUsageCounters {
		if v, ok := asFloat(usage[key]); ok {
			sum += v
			saw = true
		}
	}
	if !saw {
		return nil
	}
	n := int64(sum)
	return &n
}

func claudeTextBlocks(message map[string]any) string {
	content, ok := message["content"].([]any)
	if !ok {
		return ""
	}
	var b strings.Builder
	for _, raw := range content {
		block := asMap(raw)
		if block == nil {
			continue
		}
		if t, _ := asString(block["type"]); t == "text" {
			if txt, ok := asString(block["text"]); ok {
				b.WriteString(txt)
			}
		}
	}
	return b.String()
}

func claudeRFC3339(v any) string {
	s, ok := asString(v)
	if !ok {
		return ""
	}
	t, ok := parseISO8601WithTZ(s)
	if !ok {
		return ""
	}
	return t.UTC().Format(time.RFC3339)
}

func normalizeWindow(info map[string]any) *WindowInfo {
	if info == nil {
		return nil
	}
	w := &WindowInfo{
		Status:   "",
		ResetsAt: info["resetsAt"],
	}
	if k, ok := asString(info["rateLimitType"]); ok {
		w.RateLimitType = k
	}
	if s, ok := asString(info["status"]); ok {
		w.Status = s
	}
	if u, ok := asFloat(info["utilization"]); ok {
		w.Utilization = &u
	} else if unified := asMap(info["unifiedWindows"]); unified != nil && w.RateLimitType != "" {
		if sub := asMap(unified[w.RateLimitType]); sub != nil {
			if u, ok := asFloat(sub["utilization"]); ok {
				w.Utilization = &u
			}
		}
	}
	return w
}

func ClaudeDialect(streamText string) DialectResult {
	records, unparseable := Records(streamText)
	var pieces []string
	messageUsage := make(map[string]int64)
	var final map[string]any
	var window *WindowInfo
	var initSession, model string
	var firstAt, lastAt string

	for index, record := range records {
		kind, _ := asString(record["type"])
		if st := claudeRFC3339(record["timestamp"]); st != "" {
			if firstAt == "" {
				firstAt = st
			}
			lastAt = st
		}
		switch kind {
		case "system":
			if sub, _ := asString(record["subtype"]); sub == "init" {
				if initSession == "" {
					if s, ok := asString(record["session_id"]); ok {
						initSession = s
					}
				}
				if model == "" {
					if m, ok := asString(record["model"]); ok {
						model = m
					}
				}
			}
		case "assistant":
			msg := asMap(record["message"])
			if msg == nil {
				continue
			}
			pieces = append(pieces, claudeTextBlocks(msg))
			if total := claudeUsageTotal(asMap(msg["usage"])); total != nil {
				key, _ := asString(msg["id"])
				if key == "" {
					key = fmt.Sprintf("line-%d", index)
				}
				if _, exists := messageUsage[key]; !exists {
					messageUsage[key] = *total
				}
			}
		case "rate_limit_event":
			window = normalizeWindow(asMap(record["rate_limit_info"]))
		case "result":
			final = record
		}
	}

	var tokens *int64
	var cost *float64
	session := initSession
	var text string

	if final != nil {
		if res, ok := asString(final["result"]); ok {
			text = res
		} else {
			text = strings.Join(pieces, "")
		}
		if sr, _ := asString(final["stop_reason"]); sr == "tool_deferred" && strings.TrimSpace(text) == "" {
			text = "[delivery call deferred; waiting for the gate's answer]"
		}
		tokens = claudeUsageTotal(asMap(final["usage"]))
		if c, ok := asFloat(final["total_cost_usd"]); ok {
			cost = &c
		}
		if s, ok := asString(final["session_id"]); ok {
			session = s
		}
	} else {
		text = strings.Join(pieces, "")
	}

	if tokens == nil && len(messageUsage) > 0 {
		var sum int64
		for _, v := range messageUsage {
			sum += v
		}
		tokens = &sum
	}

	return DialectResult{
		Window:       window,
		UsageDetails: Details(records, "claude"),
		Text:         text,
		Session:      session,
		Model:        model,
		Started:      firstAt,
		Finished:     lastAt,
		Events:       len(records),
		Unparseable:  unparseable,
		Tokens:       tokens,
		Cost:         cost,
	}
}

func agyEnvelope(streamText string) map[string]any {
	stripped := strings.TrimSpace(streamText)
	if stripped == "" {
		return nil
	}
	var val map[string]any
	if err := json.Unmarshal([]byte(stripped), &val); err == nil && val != nil {
		return val
	}
	var found map[string]any
	for _, line := range SplitLines(streamText) {
		var candidate map[string]any
		if err := json.Unmarshal([]byte(line), &candidate); err == nil && candidate != nil {
			found = candidate
		}
	}
	return found
}

func AgyDialect(streamText string, stderr io.Writer) DialectResult {
	envelope := agyEnvelope(streamText)
	if envelope == nil {
		return DialectResult{
			Text:        "",
			Unparseable: 1,
			Events:      0,
		}
	}
	usage := asMap(envelope["usage"])
	var tokens int64
	sawTokens := false
	if usage != nil {
		for _, key := range agyUsageCounters {
			if v, ok := asFloat(usage[key]); ok {
				tokens += int64(v)
				sawTokens = true
			}
		}
	}
	text, _ := asString(envelope["response"])
	var denied []any
	if d, ok := envelope["denied_actions"].([]any); ok {
		denied = d
	}
	if strings.TrimSpace(text) == "" && tokens == 0 {
		fmt.Fprintln(stderr, "extract-report: the envelope carries no response and no usage: this is a print timeout, not a report")
	}
	if len(denied) > 0 {
		names := make([]string, len(denied))
		for i, entry := range denied {
			names[i] = fmt.Sprint(entry)
		}
		fmt.Fprintf(stderr, "extract-report: the harness auto-denied tool calls (%s): the run could not ask for permission, so a write it needed never happened\n", strings.Join(names, ", "))
	}

	session, _ := asString(envelope["conversation_id"])
	var tokensPtr *int64
	if sawTokens {
		tokensPtr = &tokens
	}
	return DialectResult{
		Text:        text,
		Session:     session,
		Events:      1,
		Unparseable: 0,
		Tokens:      tokensPtr,
	}
}

func MuseDialect(streamText string) DialectResult {
	records, unparseable := Records(streamText)
	var texts []string
	var session, model string
	var tokens int64
	sawTokens := false
	var firstAt, lastAt *float64

	for _, record := range records {
		payload := asMap(record["payload"])
		stream := asMap(record["stream"])
		if session == "" && stream != nil {
			if s, ok := asString(stream["id"]); ok {
				session = s
			}
		}
		if recAt, ok := asFloat(record["recorded_at"]); ok {
			if firstAt == nil {
				v := recAt
				firstAt = &v
			}
			v := recAt
			lastAt = &v
		}
		pt, _ := asString(record["payload_type"])
		switch pt {
		case "run.output.delta":
			if payload != nil {
				if t, ok := asString(payload["text"]); ok {
					texts = append(texts, t)
				}
			}
		case "run.usage":
			if payload != nil {
				if models, ok := payload["models"].([]any); ok {
					for _, raw := range models {
						entry := asMap(raw)
						if entry == nil {
							continue
						}
						if model == "" {
							if m, ok := asString(entry["model"]); ok {
								model = m
							}
						}
						var sum int64
						saw := false
						for _, key := range museUsageCounters {
							if v, ok := asFloat(entry[key]); ok {
								sum += int64(v)
								saw = true
							}
						}
						if saw {
							tokens += sum
							sawTokens = true
						}
					}
				}
			}
		}
	}

	var startedStr, finishedStr string
	if firstAt != nil {
		startedStr = MicrosToRFC3339(*firstAt)
	}
	if lastAt != nil {
		finishedStr = MicrosToRFC3339(*lastAt)
	}
	var tokensPtr *int64
	if sawTokens {
		tokensPtr = &tokens
	}

	return DialectResult{
		Text:        strings.Join(texts, ""),
		Session:     session,
		Model:       model,
		Started:     startedStr,
		Finished:    finishedStr,
		Events:      len(records),
		Unparseable: unparseable,
		Tokens:      tokensPtr,
	}
}

// NativeDialect extracts the 19-key run report and per-turn UsageDetails from
// the JSONL stream emitted by `tillr-run native-agent`.
func NativeDialect(streamText string, liveProbe bool) DialectResult {
	records, unparseable := Records(streamText)

	var (
		session         string
		model           string
		firstAt, lastAt *float64
		tokens          int64
		cost            float64
		sawTokens       bool
		sawCost         bool
		window          *WindowInfo
		ended           *EndedInfo
		hasParked       bool
		completedTexts  []string
		pendingDeltas   []string
	)

	var (
		states       []*turnState
		active       *turnState
		runStart     *float64
		runFirstSeen bool
		runFirstAt   *float64
	)
	if len(records) > 0 {
		runStart = stamp(records[0])
	}
	newState := func(at *float64, id string) *turnState {
		if id == "" {
			id = fmt.Sprintf("turn-%d", len(states)+1)
		}
		st := &turnState{start: at, id: id}
		states = append(states, st)
		return st
	}
	firstDelta := func(st *turnState, at *float64) {
		if !runFirstSeen {
			runFirstSeen = true
			runFirstAt = at
		}
		if !st.firstSeen {
			st.firstSeen = true
			st.first = at
		}
	}

	for _, record := range records {
		kind, _ := asString(record["type"])
		at := stamp(record)
		if at != nil {
			if firstAt == nil {
				v := *at
				firstAt = &v
			}
			v := *at
			lastAt = &v
		}
		if session == "" {
			for _, key := range []string{"sessionId", "session_id"} {
				if s, ok := asString(record[key]); ok && s != "" {
					session = s
					break
				}
			}
		}

		switch kind {
		case "session":
			if session == "" {
				if s, ok := asString(record["id"]); ok && s != "" {
					session = s
				}
			}
			if model == "" {
				if m, ok := asString(record["model"]); ok && m != "" {
					model = m
				}
			}
		case "message_start":
			msg := asMap(record["message"])
			if role, _ := asString(msg["role"]); role == "assistant" {
				id, _ := asString(msg["id"])
				active = newState(at, id)
				pendingDeltas = nil
			}
		case "message_update":
			ev := asMap(record["assistantMessageEvent"])
			dt, _ := asString(ev["type"])
			delta, _ := asString(ev["delta"])
			if (dt == "text_delta" || dt == "thinking_delta") && delta != "" {
				if active == nil {
					active = newState(nil, "")
				}
				firstDelta(active, at)
				if dt == "text_delta" {
					pendingDeltas = append(pendingDeltas, delta)
				}
			}
		case "message_end":
			msg := asMap(record["message"])
			if role, _ := asString(msg["role"]); role != "assistant" {
				continue
			}
			if active == nil {
				id, _ := asString(msg["id"])
				active = newState(nil, id)
			}
			if id, ok := asString(msg["id"]); ok && id != "" {
				active.id = id
			}
			active.end = at
			if m, ok := asString(msg["model"]); ok && m != "" {
				if model == "" {
					model = m
				}
			}
			if txt := piBlocksText(msg); txt != "" {
				completedTexts = append(completedTexts, txt)
			} else if len(pendingDeltas) > 0 {
				completedTexts = append(completedTexts, strings.Join(pendingDeltas, ""))
			}
			pendingDeltas = nil

			if usage := asMap(msg["usage"]); usage != nil {
				var msgModel *string
				if m, ok := asString(msg["model"]); ok && m != "" {
					msgModel = &m
				} else if model != "" {
					m := model
					msgModel = &m
				}
				c := counters(usage, "pi", msgModel)
				active.counters = &c
				if c.Tokens != nil {
					tokens += *c.Tokens
					sawTokens = true
				}
				turnCostSet := false
				if costMap := asMap(usage["cost"]); costMap != nil {
					if billed, ok := asFloat(costMap["total"]); ok {
						cost += billed
						sawCost = true
						turnCostSet = true
					}
				}
				if !turnCostSet && hasAnyCounter(c) && msgModel != nil {
					if est, ok := EstimateModelCostUSD(*msgModel, c); ok {
						cost += est
						sawCost = true
					}
				}
			}
			active = nil
		case "rate_limit_event":
			window = normalizeWindow(asMap(record["rate_limit_info"]))
		case "parked":
			hasParked = true
		case "loop_detected":
			if action, _ := asString(record["action"]); action != "recovered" {
				argsJSON := compactJSON(record["args"])
				tool, _ := asString(record["toolName"])
				if tool == "" {
					tool = "(no tool)"
				}
				streak := "None"
				if st, ok := asFloat(record["streak"]); ok {
					streak = strconv.FormatInt(int64(st), 10)
				}
				ended = &EndedInfo{
					Cause: "repeating-call",
					Detail: fmt.Sprintf(
						"native-agent ended the session because the model repeated one identical call %s times in a row: %s %s",
						streak, tool, argsJSON,
					),
				}
			}
		}
	}

	if len(pendingDeltas) > 0 {
		completedTexts = append(completedTexts, strings.Join(pendingDeltas, ""))
	}
	text := strings.Join(completedTexts, "")
	if liveProbe && hasParked {
		text += "\n[land call parked; waiting for the harness to exit]"
	}
	if ended != nil {
		if text != "" && !strings.HasSuffix(text, "\n") {
			text += "\n"
		}
		text += fmt.Sprintf("[%s]\n", ended.Detail)
	}

	var startedStr, finishedStr string
	if firstAt != nil {
		startedStr = MicrosToRFC3339(*firstAt * 1000)
	}
	if lastAt != nil {
		finishedStr = MicrosToRFC3339(*lastAt * 1000)
	}
	var tokensPtr *int64
	if sawTokens {
		tokensPtr = &tokens
	}
	var costPtr *float64
	if sawCost {
		costPtr = &cost
	}

	return DialectResult{
		Text:         text,
		Session:      session,
		Model:        model,
		Started:      startedStr,
		Finished:     finishedStr,
		Events:       len(records),
		Unparseable:  unparseable,
		Tokens:       tokensPtr,
		Cost:         costPtr,
		Window:       window,
		Ended:        ended,
		UsageDetails: buildNativeUsageDetails(states, runStart, runFirstAt),
	}
}

func buildNativeUsageDetails(states []*turnState, runStart, runFirstAt *float64) *run.UsageDetails {
	turns := []run.TurnUsage{}
	for _, st := range states {
		var row run.TurnUsage
		if st.counters != nil {
			row.ModelUsage = *st.counters
		}
		if !hasAnyCounter(row.ModelUsage) {
			continue
		}
		row.ID = st.id
		if st.start != nil {
			v := *st.start
			row.StartedMS = &v
		}
		if st.end != nil {
			v := *st.end
			row.FinishedMS = &v
		}
		if st.first != nil {
			v := *st.first
			row.FirstOutputAtMS = &v
		}
		if st.start != nil && st.first != nil && *st.first >= *st.start {
			v := *st.first - *st.start
			row.FirstOutputMS = &v
		}
		if st.first != nil && st.end != nil && *st.end > *st.first && row.Output != nil {
			tps := float64(*row.Output) * 1000 / (*st.end - *st.first)
			row.OutputTPS = &tps
		}
		turns = append(turns, row)
	}

	var modelOrder []*string
	seenNil := false
	seenNamed := make(map[string]bool)
	for _, t := range turns {
		if t.Model == nil {
			if !seenNil {
				seenNil = true
				modelOrder = append(modelOrder, nil)
			}
		} else if !seenNamed[*t.Model] {
			seenNamed[*t.Model] = true
			m := *t.Model
			modelOrder = append(modelOrder, &m)
		}
	}

	models := make([]run.ModelUsage, 0, len(modelOrder))
	for _, name := range modelOrder {
		var rows []run.TurnUsage
		for _, t := range turns {
			if (name == nil && t.Model == nil) || (name != nil && t.Model != nil && *name == *t.Model) {
				rows = append(rows, t)
			}
		}
		models = append(models, summed(rows, name))
	}
	if len(models) == 0 {
		return nil
	}

	out := &run.UsageDetails{
		Models: models,
		Turns:  turns,
	}
	if runStart != nil && runFirstAt != nil && *runFirstAt >= *runStart {
		v := *runFirstAt - *runStart
		out.FirstOutputMS = &v
	}
	if len(turns) > 0 && len(turns) == len(states) {
		allTPS := true
		for _, t := range turns {
			if t.OutputTPS == nil {
				allTPS = false
				break
			}
		}
		allOutput := true
		var totalOutput int64
		for _, m := range models {
			if m.Output == nil {
				allOutput = false
				break
			}
			totalOutput += *m.Output
		}
		if allTPS && allOutput {
			var seconds float64
			for _, t := range turns {
				seconds += (*t.FinishedMS - *t.FirstOutputAtMS) / 1000.0
			}
			if seconds > 0 {
				tps := float64(totalOutput) / seconds
				out.OutputTPS = &tps
			}
		}
	}
	return out
}
