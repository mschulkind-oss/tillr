package main

import (
	"bufio"
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync/atomic"
	"time"
)

const (
	// Unreadable is the obs.event kind for an artifact a reader could not read.
	Unreadable = "unreadable"

	ExcerptMax = 200

	// GitRetired is the git_state value because git trace2 recording was retired on 2026-11-17.
	GitRetired = "retired"

	// DefaultRuns is how many runs whole-queue aggregates read when --runs is omitted.
	DefaultRuns = 100
)

var (
	credentialRe = regexp.MustCompile(
		`(Bearer\s+\S+` +
			`|sk-[A-Za-z0-9_-]{16,}` +
			`|gh[pousr]_[A-Za-z0-9_]{20,}` +
			`|AKIA[0-9A-Z]{16}` +
			`|(--token|--api-key|--password|--secret)[= ]\S+)`,
	)

	testCommandRe = regexp.MustCompile(
		`(^|[\s;&|])(go test|pytest|py\.test|node --test|python3? [\w./-]*test[\w.-]*\.py` +
			`|just done|uvx? [\w-]+|npm (run )?test)\b`,
	)

	editTools = map[string]bool{"edit": true, "write": true}

	logMarkers = [][]byte{
		[]byte(`"session"`),
		[]byte(`"request_start"`),
		[]byte(`"tool_execution_start"`),
		[]byte(`"tool_execution_end"`),
		[]byte(`"turn_end"`),
	}

	turnMarkers = [][]byte{
		[]byte(`"turn_end"`),
	}

	globalSeq atomic.Int64
)

func nextSeq() int64 {
	return globalSeq.Add(1)
}

func isoFromMs(ms *float64) *string {
	if ms == nil {
		return nil
	}
	v := int64(math.Floor(*ms))
	s := time.UnixMilli(v).UTC().Format("2006-01-02T15:04:05.000Z")
	return &s
}

func isoFromIntMs(ms *int64) *string {
	if ms == nil {
		return nil
	}
	s := time.UnixMilli(*ms).UTC().Format("2006-01-02T15:04:05.000Z")
	return &s
}

func isoFromText(text string) *string {
	if text == "" {
		return nil
	}
	if strings.HasSuffix(text, "Z") && strings.Contains(text, ".") {
		parts := strings.SplitN(text[:len(text)-1], ".", 2)
		frac := parts[1]
		if len(frac) > 3 {
			frac = frac[:3]
		}
		out := parts[0] + "." + frac + "Z"
		return &out
	}
	out := text
	return &out
}

func msFromISO(text string) (int64, bool) {
	if text == "" {
		return 0, false
	}
	t, err := time.Parse(time.RFC3339Nano, text)
	if err != nil {
		t, err = time.Parse("2006-01-02T15:04:05.999999999", text)
		if err != nil {
			return 0, false
		}
		t = t.UTC()
	}
	return t.UnixMilli(), true
}

func nowISO() string {
	return time.Now().UTC().Format("2006-01-02T15:04:05.000Z")
}

func truncateRunes(s string, limit int) string {
	if limit <= 0 {
		return ""
	}
	if len(s) <= limit {
		return s
	}
	n := 0
	for i := range s {
		if n == limit {
			return s[:i]
		}
		n++
	}
	return s
}

func scrub(text string, limit int) string {
	if text == "" {
		return ""
	}
	cleaned := credentialRe.ReplaceAllString(text, "[REDACTED]")
	return truncateRunes(cleaned, limit)
}

func traceRootDefault() string {
	if override := os.Getenv("TILLR_TRACE_ROOT"); override != "" {
		return override
	}
	state := os.Getenv("XDG_STATE_HOME")
	if state == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			home = "."
		}
		state = filepath.Join(home, ".local", "state")
	}
	return filepath.Join(state, "tillr-trace", "v1")
}

func marshalJSONNoEscape(v any) string {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return "{}"
	}
	// Encode appends a trailing newline; trim it.
	b := buf.Bytes()
	if len(b) > 0 && b[len(b)-1] == '\n' {
		b = b[:len(b)-1]
	}
	return string(b)
}

type TraceContext struct {
	RunID     string
	LaneID    *string
	ItemID    *string
	TickID    *string
	SessionID *string
	RouteID   *string
	SpanID    *string
	Worktree  *string
}

func (tc *TraceContext) clone() *TraceContext {
	if tc == nil {
		return &TraceContext{}
	}
	cp := *tc
	return &cp
}

type ObsRow struct {
	ObsID      string
	Seq        int64
	Kind       string
	TsStart    string
	TsEnd      *string
	DurationMs *int64
	Seam       *string
	RunID      string
	LaneID     *string
	ItemID     *string
	TickID     *string
	SessionID  *string
	RouteID    *string
	SpanID     *string
	Summary    *string
	Payload    string
	Source     string
}

type Rows struct {
	Rows    []ObsRow
	Origin  string
	Source  string
	Ordinal int
	slug    string
}

func sourceSlug(path string) string {
	sum := sha256.Sum256([]byte(path))
	return hex.EncodeToString(sum[:])[:12]
}

func newRows(origin, source string) *Rows {
	return &Rows{
		Origin: origin,
		Source: source,
		slug:   sourceSlug(source),
	}
}

func (r *Rows) add(kind string, trace *TraceContext, tsStart string, tsEnd *string, durationMs *int64, seam *string, spanID *string, summary *string, payload map[string]any) {
	ordinal := r.Ordinal
	r.Ordinal++
	runID := "unattributed"
	if trace != nil && trace.RunID != "" {
		runID = trace.RunID
	}
	var laneID, itemID, tickID, sessionID, routeID, tcSpanID *string
	if trace != nil {
		laneID = trace.LaneID
		itemID = trace.ItemID
		tickID = trace.TickID
		sessionID = trace.SessionID
		routeID = trace.RouteID
		tcSpanID = trace.SpanID
	}
	effSpan := spanID
	if effSpan == nil || *effSpan == "" {
		effSpan = tcSpanID
	}
	if payload == nil {
		payload = map[string]any{}
	}
	r.Rows = append(r.Rows, ObsRow{
		ObsID:      fmt.Sprintf("obs-%s-%s-%06d", runID, r.slug, ordinal),
		Seq:        nextSeq(),
		Kind:       kind,
		TsStart:    tsStart,
		TsEnd:      tsEnd,
		DurationMs: durationMs,
		Seam:       seam,
		RunID:      runID,
		LaneID:     laneID,
		ItemID:     itemID,
		TickID:     tickID,
		SessionID:  sessionID,
		RouteID:    routeID,
		SpanID:     effSpan,
		Summary:    summary,
		Payload:    marshalJSONNoEscape(payload),
		Source:     r.Source,
	})
}

func (r *Rows) addUnreadable(trace *TraceContext, artifact, path, reason string) {
	runID := "unattributed"
	if trace != nil && trace.RunID != "" {
		runID = trace.RunID
	}
	var laneID, itemID, tickID, sessionID *string
	if trace != nil {
		laneID = trace.LaneID
		itemID = trace.ItemID
		tickID = trace.TickID
		sessionID = trace.SessionID
	}
	seam := "reader"
	spanID := "unreadable-" + artifact
	summary := fmt.Sprintf("%s: unreadable (%s)", artifact, reason)
	payload := map[string]any{
		"artifact": artifact,
		"path":     path,
		"reason":   reason,
	}
	r.Rows = append(r.Rows, ObsRow{
		ObsID:      fmt.Sprintf("obs-%s-unreadable-%s", runID, artifact),
		Seq:        nextSeq(),
		Kind:       Unreadable,
		TsStart:    nowISO(),
		TsEnd:      nil,
		DurationMs: nil,
		Seam:       &seam,
		RunID:      runID,
		LaneID:     laneID,
		ItemID:     itemID,
		TickID:     tickID,
		SessionID:  sessionID,
		RouteID:    nil,
		SpanID:     &spanID,
		Summary:    &summary,
		Payload:    marshalJSONNoEscape(payload),
		Source:     r.Source,
	})
}

type LogSummary struct {
	Turns     int
	TokensIn  int64
	TokensOut int64
	CacheRead int64
	Reasoning int64
	CostUSD   float64
	ToolCalls int
	FinalText string
}

type Parsed struct {
	Rows    *Rows
	State   string
	Detail  string
	Summary LogSummary
}

type Stream struct {
	Bad       int
	Truncated bool
}

func (s *Stream) feed(parsed, complete bool) bool {
	if !parsed {
		if !complete {
			s.Truncated = true
			return false
		}
		s.Bad++
	}
	return true
}

func (s *Stream) state(rows int) (string, string) {
	if s.Truncated && s.Bad == 0 {
		return "truncated", fmt.Sprintf("%d rows, final line incomplete", rows)
	}
	if s.Bad > 0 {
		return "corrupt", fmt.Sprintf("%d rows, %d unparseable line(s)", rows, s.Bad)
	}
	return "ok", fmt.Sprintf("%d rows", rows)
}

func containsAny(raw []byte, markers [][]byte) bool {
	for _, m := range markers {
		if bytes.Contains(raw, m) {
			return true
		}
	}
	return false
}

func streamJSONL(path string, wanted [][]byte, fn func(obj any, parsed, complete bool) bool) error {
	f, err := os.Open(path)
	if err != nil {
		return err
	}
	defer f.Close()

	reader := bufio.NewReaderSize(f, 64*1024)
	for {
		raw, err := reader.ReadBytes('\n')
		if len(raw) == 0 && err == io.EOF {
			break
		}
		complete := bytes.HasSuffix(raw, []byte("\n"))
		if wanted != nil && complete && !containsAny(raw, wanted) {
			if !fn(nil, true, complete) {
				break
			}
			if err == io.EOF {
				break
			}
			continue
		}
		trimmed := bytes.TrimSpace(raw)
		if len(trimmed) == 0 {
			if !fn(nil, true, complete) {
				break
			}
			if err == io.EOF {
				break
			}
			continue
		}
		var val any
		dec := json.NewDecoder(bytes.NewReader(trimmed))
		dec.UseNumber()
		if jerr := dec.Decode(&val); jerr != nil {
			if !fn(nil, false, complete) {
				break
			}
		} else {
			if !fn(val, true, complete) {
				break
			}
		}
		if err != nil {
			if err == io.EOF {
				break
			}
			return err
		}
	}
	return nil
}

func asMap(v any) map[string]any {
	if m, ok := v.(map[string]any); ok && m != nil {
		return m
	}
	return map[string]any{}
}

func asString(v any) (string, bool) {
	if v == nil {
		return "", false
	}
	s, ok := v.(string)
	return s, ok
}

func asFloat(v any) (float64, bool) {
	if v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case json.Number:
		f, err := n.Float64()
		return f, err == nil
	case float64:
		return n, true
	case int64:
		return float64(n), true
	case int:
		return float64(n), true
	default:
		return 0, false
	}
}

func asInt64(v any) (int64, bool) {
	if v == nil {
		return 0, false
	}
	switch n := v.(type) {
	case json.Number:
		if i, err := n.Int64(); err == nil {
			return i, true
		}
		if f, err := n.Float64(); err == nil {
			return int64(f), true
		}
		return 0, false
	case float64:
		return int64(n), true
	case int64:
		return n, true
	case int:
		return int64(n), true
	default:
		return 0, false
	}
}

func strPtr(v any) *string {
	if s, ok := asString(v); ok {
		return &s
	}
	return nil
}

func int64Ptr(v any) *int64 {
	if n, ok := asInt64(v); ok {
		return &n
	}
	return nil
}

func parseRunLog(path string, trace *TraceContext, turnsOnly bool) Parsed {
	rows := newRows("log", path)
	if path == "" {
		return Parsed{Rows: rows, State: "missing", Detail: "no log path recorded"}
	}

	argsByCall := make(map[string]map[string]any)
	turnNo := 0
	finalText := ""
	var anchorMs *float64
	stream := &Stream{}
	markers := logMarkers
	if turnsOnly {
		markers = turnMarkers
	}

	var summary LogSummary
	seam := "pi-log"

	err := streamJSONL(path, markers, func(obj any, parsed, complete bool) bool {
		if !stream.feed(parsed, complete) {
			return false
		}
		if !parsed {
			return true
		}
		event, ok := obj.(map[string]any)
		if !ok || event == nil {
			return true
		}
		kind, _ := asString(event["type"])
		switch kind {
		case "session":
			tc := trace.clone()
			if tc.SessionID == nil {
				tc.SessionID = strPtr(event["id"])
			}
			tc.Worktree = strPtr(event["cwd"])
			trace = tc
			if tsStr, ok := asString(event["timestamp"]); ok {
				if ms, ok := msFromISO(tsStr); ok {
					f := float64(ms)
					anchorMs = &f
				}
			}
			tsStart := nowISO()
			if tsStr, ok := asString(event["timestamp"]); ok {
				if iso := isoFromText(tsStr); iso != nil {
					tsStart = *iso
				}
			}
			spanID := "session-header"
			idVal := "None"
			if s, ok := asString(event["id"]); ok {
				idVal = s
			}
			sum := "session " + idVal
			rows.add("run.lifecycle", trace, tsStart, nil, nil, &seam, &spanID, &sum, map[string]any{
				"state":   "started",
				"cwd":     event["cwd"],
				"version": event["version"],
			})
		case "request_start":
			if f, ok := asFloat(event["timestamp"]); ok {
				anchorMs = &f
			}
		case "tool_execution_start":
			callID, _ := asString(event["toolCallId"])
			argsByCall[callID] = asMap(event["args"])
		case "tool_execution_end":
			callID, _ := asString(event["toolCallId"])
			args := argsByCall[callID]
			delete(argsByCall, callID)
			if args == nil {
				args = map[string]any{}
			}
			tool, _ := asString(event["toolName"])
			if tool == "" {
				tool = "unknown"
			}
			result := event["result"]
			if result == nil {
				result = map[string]any{}
			}
			structured := asMap(asMap(result)["structuredContent"])
			target, _ := asString(args["path"])
			command, _ := asString(args["command"])
			var targetAny, cmdPreviewAny any
			if target != "" {
				targetAny = target
			} else if args["path"] != nil {
				targetAny = args["path"]
			}
			if command != "" {
				cmdPreviewAny = scrub(command, ExcerptMax)
			}
			tsStart := ""
			tsSource := "unknown"
			if anchorMs != nil {
				if iso := isoFromMs(anchorMs); iso != nil {
					tsStart = *iso
				}
				tsSource = "derived_from_preceding_clock"
			}
			suffix := target
			if suffix == "" {
				suffix = command
			}
			sum := truncateRunes(strings.TrimSpace(tool+" "+suffix), ExcerptMax)
			isError := false
			if b, ok := event["isError"].(bool); ok {
				isError = b
			}
			resultBytes := len(marshalJSONNoEscape(result))
			dur := int64Ptr(event["durationMs"])
			var spanPtr *string
			if callID != "" {
				spanPtr = &callID
			}
			rows.add("tool.call", trace, tsStart, nil, dur, &seam, spanPtr, &sum, map[string]any{
				"tool_call_id":    event["toolCallId"],
				"tool_name":       tool,
				"target_path":     targetAny,
				"command_preview": cmdPreviewAny,
				"repo_rel_cwd":    nil,
				"is_error":        isError,
				"exit_code":       structured["exit_code"],
				"ts_source":       tsSource,
				"result_bytes":    resultBytes,
				"duration_ms":     event["durationMs"],
			})
			summary.ToolCalls++
		case "turn_end":
			message := asMap(event["message"])
			role, _ := asString(message["role"])
			if role != "assistant" {
				return true
			}
			turnNo++
			if f, ok := asFloat(message["timestamp"]); ok {
				anchorMs = &f
			}
			usage := asMap(message["usage"])
			cost := asMap(usage["cost"])
			provider, _ := asString(message["provider"])
			model, _ := asString(message["model"])
			thinking, _ := asString(message["thinkingLevel"])

			var routeParts []string
			for _, p := range []string{provider, model, thinking} {
				if p != "" {
					routeParts = append(routeParts, p)
				}
			}
			routeID := strings.Join(routeParts, ":")
			turnTrace := trace.clone()
			turnTrace.RouteID = &routeID

			var startedMs, endedMs *float64
			if f, ok := asFloat(message["requestStartedAt"]); ok {
				startedMs = &f
			}
			if f, ok := asFloat(message["timestamp"]); ok {
				endedMs = &f
			}
			tsStart := nowISO()
			if iso := isoFromMs(startedMs); iso != nil {
				tsStart = *iso
			}
			tsEnd := isoFromMs(endedMs)

			respModel, _ := asString(message["responseModel"])
			dispModel := respModel
			if dispModel == "" {
				dispModel = model
			}
			provDisp := "None"
			if provider != "" {
				provDisp = provider
			}
			if dispModel == "" {
				dispModel = "None"
			}
			thinkDisp := "None"
			if thinking != "" {
				thinkDisp = thinking
			}
			sum := fmt.Sprintf("%s/%s %s", provDisp, dispModel, thinkDisp)
			spanID := fmt.Sprintf("turn-%03d", turnNo)
			dur := int64Ptr(message["durationMs"])

			tokIn, _ := asInt64(usage["input"])
			tokOut, _ := asInt64(usage["output"])
			tokCacheR, _ := asInt64(usage["cacheRead"])
			tokCacheW, _ := asInt64(usage["cacheWrite"])
			tokReason, _ := asInt64(usage["reasoning"])
			costTotal, _ := asFloat(cost["total"])
			credFP := asMap(message["credential"])["fingerprint"]

			rows.add("llm.turn", turnTrace, tsStart, tsEnd, dur, &seam, &spanID, &sum, map[string]any{
				"provider":       message["provider"],
				"model":          message["model"],
				"response_model": message["responseModel"],
				"upstream_route": message["model"],
				"thinking_level": message["thinkingLevel"],
				"tokens": map[string]any{
					"in":          tokIn,
					"out":         tokOut,
					"cache_read":  tokCacheR,
					"cache_write": tokCacheW,
					"reasoning":   tokReason,
				},
				"cost_usd":      costTotal,
				"credential_fp": credFP,
				"stop_reason":   message["stopReason"],
			})

			summary.Turns++
			summary.TokensIn += tokIn
			summary.TokensOut += tokOut
			summary.CacheRead += tokCacheR
			summary.Reasoning += tokReason
			summary.CostUSD += costTotal

			if content, ok := message["content"].([]any); ok {
				var texts []string
				hasNonEmpty := false
				for _, item := range content {
					part, ok := item.(map[string]any)
					if !ok {
						continue
					}
					if pt, _ := asString(part["type"]); pt == "text" {
						t, _ := asString(part["text"])
						texts = append(texts, t)
						if t != "" {
							hasNonEmpty = true
						}
					}
				}
				if hasNonEmpty {
					finalText = strings.Join(texts, "\n")
				}
			}
		}
		return true
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Parsed{Rows: rows, State: "missing", Detail: fmt.Sprintf("no file at %s", path), Summary: summary}
		}
		return Parsed{Rows: rows, State: "unreadable", Detail: err.Error(), Summary: summary}
	}

	summary.FinalText = scrub(finalText, 20000)
	state, detail := stream.state(rows.Ordinal)
	return Parsed{Rows: rows, State: state, Detail: detail, Summary: summary}
}

func parseProcExec(path string, trace *TraceContext) Parsed {
	rows := newRows("proc", path)
	if path == "" {
		return Parsed{Rows: rows, State: "missing", Detail: "no proc-exec.jsonl recorded"}
	}
	stream := &Stream{}
	err := streamJSONL(path, nil, func(obj any, parsed, complete bool) bool {
		if !stream.feed(parsed, complete) {
			return false
		}
		if !parsed {
			return true
		}
		event, ok := obj.(map[string]any)
		if !ok || event == nil {
			return true
		}
		procRow(rows, trace, event)
		return true
	})
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Parsed{Rows: rows, State: "missing", Detail: fmt.Sprintf("no file at %s", path)}
		}
		return Parsed{Rows: rows, State: "unreadable", Detail: err.Error()}
	}
	if len(rows.Rows) == 0 {
		return Parsed{Rows: rows, State: "missing", Detail: fmt.Sprintf("%s: no proc.exec rows", path)}
	}
	state, detail := stream.state(rows.Ordinal)
	return Parsed{Rows: rows, State: state, Detail: detail}
}

func procRow(rows *Rows, trace *TraceContext, event map[string]any) {
	var argv []string
	if rawArgv, ok := event["argv"].([]any); ok {
		for _, a := range rawArgv {
			argv = append(argv, fmt.Sprint(a))
		}
	} else if cmd, ok := asString(event["cmd"]); ok && cmd != "" {
		argv = []string{cmd}
	}
	span, _ := asString(event["span_id"])
	if span == "" {
		span = fmt.Sprintf("sh-%05d", rows.Ordinal+1)
	}
	tsRaw, _ := asString(event["ts"])
	if tsRaw == "" {
		tsRaw, _ = asString(event["ts_start"])
	}
	tsStart := nowISO()
	if iso := isoFromText(tsRaw); iso != nil {
		tsStart = *iso
	}
	tsEndRaw, _ := asString(event["ts_end"])
	tsEnd := isoFromText(tsEndRaw)
	seam, _ := asString(event["seam"])
	if seam == "" {
		seam = "bash-shim"
	}
	summary := scrub(strings.Join(argv, " "), 120)
	scrubbedArgv := make([]string, len(argv))
	for i, a := range argv {
		scrubbedArgv[i] = scrub(a, 512)
	}
	exitCode := event["exit_code"]
	if exitCode == nil {
		exitCode = event["rc"]
	}
	tailExcerpt, _ := asString(event["tail_excerpt"])
	dur := int64Ptr(event["duration_ms"])

	rows.add("proc.exec", trace, tsStart, tsEnd, dur, &seam, &span, &summary, map[string]any{
		"argv":          scrubbedArgv,
		"cwd":           event["cwd"],
		"repo_rel_cwd":  event["repo_rel_cwd"],
		"exit_code":     exitCode,
		"signal":        event["signal"],
		"stdout_sha256": event["stdout_sha256"],
		"tail_excerpt":  scrub(tailExcerpt, ExcerptMax),
		"pid":           event["pid"],
	})
}

func parseGateRecord(path string, trace *TraceContext) Parsed {
	base := filepath.Base(path)
	kind := "gate.plan"
	if strings.HasPrefix(base, "gate.verdict") {
		kind = "gate.verdict"
	}
	rows := newRows("gate", path)
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return Parsed{Rows: rows, State: "missing", Detail: fmt.Sprintf("no file at %s", path)}
		}
		reason := fmt.Sprintf("unparseable: %s", err)
		rows.addUnreadable(trace, base, path, reason)
		return Parsed{Rows: rows, State: "corrupt", Detail: reason}
	}
	var envelope map[string]any
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	if err := dec.Decode(&envelope); err != nil || envelope == nil {
		reason := fmt.Sprintf("unparseable: %v", err)
		rows.addUnreadable(trace, base, path, reason)
		return Parsed{Rows: rows, State: "corrupt", Detail: reason}
	}
	envKind, _ := asString(envelope["kind"])
	if envKind != kind {
		reason := fmt.Sprintf("not a %s record", kind)
		rows.addUnreadable(trace, base, path, reason)
		return Parsed{Rows: rows, State: "corrupt", Detail: reason}
	}
	payload, ok := envelope["payload"].(map[string]any)
	if !ok || payload == nil {
		reason := fmt.Sprintf("%s payload is not an object", kind)
		rows.addUnreadable(trace, base, path, reason)
		return Parsed{Rows: rows, State: "corrupt", Detail: reason}
	}
	createdAt, _ := asString(envelope["created_at"])
	tsStart := nowISO()
	if iso := isoFromText(createdAt); iso != nil {
		tsStart = *iso
	}
	seam := "tillr-gate"
	spanID, _ := asString(envelope["id"])
	if spanID == "" {
		spanID = kind
	}
	idSuffix, _ := asString(payload["plan_id"])
	if idSuffix == "" {
		idSuffix, _ = asString(payload["verdict_id"])
	}
	sum := strings.TrimSpace(kind + " " + idSuffix)
	rows.add(kind, trace, tsStart, nil, nil, &seam, &spanID, &sum, gatePayload(kind, payload))
	return Parsed{Rows: rows, State: "ok", Detail: fmt.Sprintf("%s read", kind)}
}

func gatePayload(kind string, payload map[string]any) map[string]any {
	checks, _ := payload["checks"].([]any)
	checkIDs := []string{}
	for _, c := range checks {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		if id, ok := asString(cm["check_id"]); ok && id != "" {
			checkIDs = append(checkIDs, id)
		}
	}
	common := map[string]any{
		"plan_id":         payload["plan_id"],
		"base_sha":        payload["base_sha"],
		"tree_sha":        payload["tree_sha"],
		"registry_digest": payload["registry_digest"],
		"check_ids":       checkIDs,
	}
	if kind == "gate.plan" {
		common["checks_count"] = len(checks)
		return common
	}
	common["verdict"] = payload["result"]
	common["verdict_id"] = payload["verdict_id"]
	failed := []string{}
	timings := map[string]any{}
	for _, c := range checks {
		cm, ok := c.(map[string]any)
		if !ok {
			continue
		}
		id, _ := asString(cm["check_id"])
		res, _ := asString(cm["result"])
		if res != "pass" {
			failed = append(failed, id)
		}
		if id != "" {
			timings[id] = cm["duration_ms"]
		}
	}
	common["failed_check_ids"] = failed
	common["check_timings_ms"] = timings
	return common
}

func fileExists(path string) bool {
	if path == "" {
		return false
	}
	_, err := os.Stat(path)
	return err == nil
}

func runArtifact(dirs []string, name string) string {
	for _, dir := range dirs {
		cand := filepath.Join(dir, name)
		if fileExists(cand) {
			return cand
		}
	}
	if len(dirs) > 0 {
		return filepath.Join(dirs[0], name)
	}
	return ""
}

func existingArtifact(dirs []string, name string) *string {
	if len(dirs) == 0 {
		return nil
	}
	p := runArtifact(dirs, name)
	if p != "" && fileExists(p) {
		return &p
	}
	return nil
}

func readJSONMap(path string) map[string]any {
	if path == "" {
		return map[string]any{}
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		return map[string]any{}
	}
	var m map[string]any
	if err := json.Unmarshal(raw, &m); err != nil || m == nil {
		return map[string]any{}
	}
	return m
}
