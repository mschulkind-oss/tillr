package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"sort"
	"strconv"
	"time"

	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
)

// traceDocument bypasses the HTTP view envelope: trace consumers need the standard
// document at the top level. Failures still use the API's normal refusal envelope.
type traceDocument struct{ Body any }

func traceFormat(format string) error {
	if format != "otlp-json" && format != "chrome-trace" {
		return fmt.Errorf("--format must be otlp-json or chrome-trace")
	}
	return nil
}

func traceFace(args []string, out io.Writer) int {
	fs := newFlagSet("trace")
	path := fs.String("store", "", "the store to read (required)")
	runID := fs.String("run", "", "export this run")
	itemID := fs.String("item", "", "export every run of this item")
	passID := fs.String("pass", "", "export runs dispatched in this UTC pass (YYYYMMDDTHHMMSSZ)")
	format := fs.String("format", "otlp-json", "otlp-json or chrome-trace")
	loop := fs.String("loop-dir", "", "the loop record directory (default: store sibling loop)")
	index := fs.String("trace-index", defaultTraceIndex(), "the tillr-trace index to read")
	if code := parseFlags(fs, args); code != record.ExitOK {
		if code == helpExit {
			return record.ExitOK
		}
		return code
	}
	selectors := 0
	for _, value := range []string{*runID, *itemID, *passID} {
		if value != "" {
			selectors++
		}
	}
	if *path == "" || selectors != 1 {
		return usagef("trace needs --store and exactly one of --run, --item or --pass")
	}
	if err := traceFormat(*format); err != nil {
		return usagef("%v", err)
	}
	if *passID != "" {
		if _, err := time.Parse("20060102T150405Z", *passID); err != nil {
			return usagef("--pass needs a UTC stamp YYYYMMDDTHHMMSSZ")
		}
	}
	ctx := context.Background()
	s := newServer(ctx, *path, *loop, "", *index, ".", "", nil)
	defer s.close()
	if s.problem != nil {
		fmt.Fprintf(os.Stderr, "tillr-view: %s\n", s.problem.note)
		if s.problem.code == codeSchemaTooNew || s.problem.code == codeSchemaUninitialized {
			return record.ExitIncompatible
		}
		return record.ExitInternal
	}
	var body any
	var fail *apiFailure
	err := s.store.ReadTx(ctx, func(r *store.Reader) error {
		runs, err := selectTraceRuns(r, *runID, *itemID, *passID)
		if err != nil {
			return err
		}
		if len(runs) == 0 {
			fail = runNotFound(*runID + *itemID + *passID)
			return nil
		}
		body, err = s.exportTrace(r, runs, *format)
		return err
	})
	if err != nil {
		return exitFor(err)
	}
	if fail != nil {
		fmt.Fprintf(os.Stderr, "tillr-view: %s\n", fail.Error.Message)
		return record.ExitNegative
	}
	if err := json.NewEncoder(out).Encode(body); err != nil {
		return exitFor(err)
	}
	return record.ExitOK
}

func selectTraceRuns(r *store.Reader, runID, itemID, passID string) ([]storeRun, error) {
	if runID != "" {
		run, found, err := readRun(r, runID)
		if err != nil || !found {
			return nil, err
		}
		return []storeRun{run}, nil
	}
	if itemID != "" {
		return readItemRuns(r, itemID)
	}
	stamp, err := time.Parse("20060102T150405Z", passID)
	if err != nil {
		return nil, err
	}
	rows, err := r.Query(`SELECT DISTINCT run_id FROM dispatches WHERE action = 'dispatch'
 AND run_id IS NOT NULL AND dispatched_at >= ? AND dispatched_at < ? ORDER BY run_id`, stamp.UnixMilli(), stamp.UnixMilli()+1000)
	if err != nil {
		return nil, err
	}
	var ids []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		ids = append(ids, id)
	}
	err = rows.Err()
	rows.Close()
	if err != nil {
		return nil, err
	}
	runs := []storeRun{}
	for _, id := range ids {
		run, found, err := readRun(r, id)
		if err != nil {
			return nil, err
		}
		if found {
			runs = append(runs, run)
		}
	}
	return runs, nil
}

func tracePass(r *store.Reader, runID string) (string, error) {
	rows, err := r.Query(`SELECT dispatched_at FROM dispatches WHERE run_id = ? AND action = 'dispatch' ORDER BY dispatched_at LIMIT 1`, runID)
	if isMissingTable(err) {
		return "", nil
	}
	if err != nil {
		return "", err
	}
	defer rows.Close()
	if !rows.Next() {
		return "", rows.Err()
	}
	var ms int64
	if err := rows.Scan(&ms); err != nil {
		return "", err
	}
	return time.UnixMilli(ms).UTC().Format("20060102T150405Z"), nil
}

// runSpans is shared by the waterfall and exports. The store's gate measurements
// supplement the observation index; their exact nanoseconds must not round-trip via ms.
func (s *server) runSpans(r *store.Reader, run storeRun) ([]span, error) {
	spans := s.obs.timeline(run)
	gates, err := readGateSpans(r, run.RunID)
	if err != nil {
		return nil, err
	}
	for _, g := range gates {
		parent := g.ParentSpanID
		start, end := g.StartUnixNano/1e6, g.EndUnixNano/1e6
		var startMS, endMS *int64
		if g.StartUnixNano > 0 {
			startMS = &start
		}
		if g.EndUnixNano > 0 {
			endMS = &end
		}
		spans = append(spans, span{SpanID: g.SpanID, ParentSpanID: &parent, LaneID: run.LaneID,
			Category: "gate_check", Label: g.Label, Status: g.Status, CheckID: g.CheckID,
			StartedAtMS: startMS, FinishedAtMS: endMS, StartUnixNano: strconv.FormatInt(g.StartUnixNano, 10),
			EndUnixNano: strconv.FormatInt(g.EndUnixNano, 10), AttributesJSON: g.AttributesJSON,
			TSSource: "store", EpistemicState: stateObserved})
	}
	return spans, nil
}

func (s *server) tracePayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	format := req.URL.Query().Get("format")
	if format == "" {
		format = "otlp-json"
	}
	if err := traceFormat(format); err != nil {
		return nil, failure(codeBadQuery, "%v", err)
	}
	id := req.PathValue("id")
	run, found, err := readRun(r, id)
	if err != nil {
		return nil, unreadable(err)
	}
	if !found {
		return nil, runNotFound(id)
	}
	body, err := s.exportTrace(r, []storeRun{run}, format)
	if err != nil {
		return nil, unreadable(err)
	}
	return traceDocument{Body: body}, nil
}

// IDs are stable, nonzero and scoped by run, with the 16/8 byte widths OTLP requires.
func traceHex(value string, bytes int) string {
	sum := sha256.Sum256([]byte(value))
	return hex.EncodeToString(sum[:bytes])
}

type exportSpan struct {
	ID, Parent, Name, Category string
	Start, End                 int64
	Attr                       map[string]any
}

func spanNS(exact string, ms *int64) int64 {
	if exact != "" {
		ns, _ := strconv.ParseInt(exact, 10, 64)
		return ns
	}
	if ms != nil && *ms > 0 {
		return *ms * 1e6
	}
	return 0
}

// exportSpans derives enclosing windows without destroying measured clocks. An
// unrecorded interval borrows its parent's window and is explicitly marked as such;
// it is never represented as a measured zero-duration span. Original bounds remain
// attributes when a parent must expand to cover work after the agent exited.
func exportSpans(run storeRun, pass string, spans []span) ([]exportSpan, error) {
	root := run.RunID + "/run"
	out := make([]exportSpan, len(spans))
	byID := map[string]int{}
	for i, sp := range spans {
		if _, ok := byID[sp.SpanID]; ok {
			return nil, fmt.Errorf("duplicate span %q", sp.SpanID)
		}
		byID[sp.SpanID] = i
		parent := ""
		if sp.ParentSpanID != nil {
			parent = *sp.ParentSpanID
		}
		attrs := map[string]any{"item_id": run.ItemID, "run_id": run.RunID, "pass_id": pass, "lane_id": run.LaneID, "backend": run.Backend, "check_id": sp.CheckID,
			"source_span_id": sp.SpanID, "ts_source": sp.TSSource, "epistemic_state": sp.EpistemicState, "status": sp.Status}
		if sp.AttributesJSON != "" {
			attrs["recorded_attributes_json"] = sp.AttributesJSON
		}
		if sp.CostUSD != nil {
			attrs["cost_usd"] = *sp.CostUSD
		}
		if sp.ExchangeID != nil {
			attrs["exchange_id"] = *sp.ExchangeID
		}
		start, end := spanNS(sp.StartUnixNano, sp.StartedAtMS), spanNS(sp.EndUnixNano, sp.FinishedAtMS)
		attrs["recorded_start_unix_nano"] = strconv.FormatInt(start, 10)
		attrs["recorded_end_unix_nano"] = strconv.FormatInt(end, 10)
		missing := start <= 0 || end < start
		if missing {
			attrs["timing_missing"] = true
			start = 0
			end = 0
		}
		out[i] = exportSpan{ID: sp.SpanID, Parent: parent, Name: sp.Label, Category: sp.Category, Start: start, End: end, Attr: attrs}
	}
	rootIndex, ok := byID[root]
	if !ok {
		return nil, fmt.Errorf("run has no root span")
	}
	for i := range out {
		if i == rootIndex {
			out[i].Parent = ""
			continue
		}
		if _, ok := byID[out[i].Parent]; !ok || out[i].Parent == "" {
			out[i].Attr["source_parent_span_id"] = out[i].Parent
			out[i].Parent = root
		}
	}
	children := make(map[string][]int)
	for i := range out {
		children[out[i].Parent] = append(children[out[i].Parent], i)
	}
	// Postorder traversal detects corrupt parent cycles and expands only grouping bounds.
	state := make([]int, len(out))
	var visit func(int) error
	visit = func(i int) error {
		if state[i] == 1 {
			return fmt.Errorf("cyclic span parent at %q", out[i].ID)
		}
		if state[i] == 2 {
			return nil
		}
		state[i] = 1
		for _, j := range children[out[i].ID] {
			if err := visit(j); err != nil {
				return err
			}
			if out[j].Start > 0 && (out[i].Start == 0 || out[j].Start < out[i].Start) {
				out[i].Start = out[j].Start
				out[i].Attr["timing_expanded"] = true
			}
			if out[j].End > out[i].End {
				out[i].End = out[j].End
				out[i].Attr["timing_expanded"] = true
			}
		}
		if out[i].Attr["timing_missing"] == true && out[i].Start > 0 {
			out[i].Attr["timing_source"] = "children_envelope"
		}
		state[i] = 2
		return nil
	}
	for i := range out {
		if err := visit(i); err != nil {
			return nil, err
		}
	}
	if out[rootIndex].Start <= 0 || out[rootIndex].End <= out[rootIndex].Start {
		return nil, fmt.Errorf("run %s has no positive recorded interval to enclose unrecorded spans", run.RunID)
	}
	var fill func(int)
	fill = func(i int) {
		if i != rootIndex && out[i].Attr["timing_missing"] == true {
			p := byID[out[i].Parent]
			out[i].Start = out[p].Start
			out[i].End = out[p].End
			out[i].Attr["timing_source"] = "parent_envelope"
		}
		if i != rootIndex && out[i].Start == out[i].End && len(children[out[i].ID]) > 0 {
			p := byID[out[i].Parent]
			out[i].Start, out[i].End = out[p].Start, out[p].End
			out[i].Attr["timing_expanded"] = true
			out[i].Attr["timing_source"] = "parent_envelope"
		}
		for _, j := range children[out[i].ID] {
			fill(j)
		}
	}
	fill(rootIndex)
	return out, nil
}

func (s *server) exportTrace(r *store.Reader, runs []storeRun, format string) (any, error) {
	resources := []any{}
	events := []any{}
	for n, run := range runs {
		spans, err := s.runSpans(r, run)
		if err != nil {
			return nil, err
		}
		pass, err := tracePass(r, run.RunID)
		if err != nil {
			return nil, err
		}
		normalized, err := exportSpans(run, pass, spans)
		if err != nil {
			return nil, err
		}
		traceID := traceHex(run.RunID, 16)
		otlp := []any{}
		tracks := make(map[string]int, len(normalized))
		for i, sp := range normalized {
			tracks[sp.ID] = i + 1
		}
		for _, sp := range normalized {
			id := traceHex(run.RunID+"/"+sp.ID, 8)
			parent := ""
			if sp.Parent != "" {
				parent = traceHex(run.RunID+"/"+sp.Parent, 8)
			}
			if format == "otlp-json" {
				row := map[string]any{"traceId": traceID, "spanId": id, "name": sp.Name, "kind": 1,
					"startTimeUnixNano": strconv.FormatInt(sp.Start, 10), "endTimeUnixNano": strconv.FormatInt(sp.End, 10), "attributes": otlpAttributes(sp.Attr)}
				if parent != "" {
					row["parentSpanId"] = parent
				}
				otlp = append(otlp, row)
			} else {
				chromeTS, chromeDur := chromeWindow(sp.Start, sp.End)
				sp.Attr["trace_id"] = traceID
				sp.Attr["span_id"] = id
				sp.Attr["parent_span_id"] = parent
				// Each span has its own track: overlapping siblings must never become
				// false children through Chrome's interval-containment inference.
				events = append(events, map[string]any{"name": sp.Name, "cat": sp.Category, "ph": "X", "pid": n + 1, "tid": tracks[sp.ID],
					"ts": chromeTS, "dur": chromeDur, "args": sp.Attr})
				if sp.Parent != "" {
					// Standard flow start/finish events link the enclosing parent slice
					// to this child. They are links, not additional duration spans.
					events = append(events,
						map[string]any{"name": "parent", "cat": "tillr.parent", "ph": "s", "id": id, "pid": n + 1, "tid": tracks[sp.Parent], "ts": chromeTS},
						map[string]any{"name": "parent", "cat": "tillr.parent", "ph": "f", "id": id, "pid": n + 1, "tid": tracks[sp.ID], "ts": chromeTS, "bp": "e"},
					)
				}
			}
		}
		if format == "otlp-json" {
			resources = append(resources, map[string]any{"resource": map[string]any{"attributes": otlpAttributes(map[string]any{"service.name": "tillr", "run_id": run.RunID})},
				"scopeSpans": []any{map[string]any{"scope": map[string]any{"name": "tillr-view", "version": version}, "spans": otlp}}})
		}
	}
	if format == "otlp-json" {
		return map[string]any{"resourceSpans": resources}, nil
	}
	return map[string]any{"traceEvents": events, "displayTimeUnit": "ms"}, nil
}

// chromeWindow converts a nanosecond interval to the Chrome trace's microsecond
// clock as whole microseconds: the start rounds down and the end rounds up. Both
// roundings are monotonic, so a child inside its parent in nanoseconds is still
// inside it in microseconds. Fractional microseconds are not used: at Unix-epoch
// scale (about 1.8e15 us) a float64 has only 0.25 us of resolution, so a fractional
// ts and dur do not add back to the end they were computed from, and a child could
// end a few nanoseconds after its parent. Whole microseconds below 2^53 are exact.
func chromeWindow(startNS, endNS int64) (ts, dur float64) {
	startUS := floorDiv(startNS, 1000)
	endUS := -floorDiv(-endNS, 1000)
	return float64(startUS), float64(endUS - startUS)
}

func floorDiv(a, b int64) int64 {
	q := a / b
	if a%b != 0 && (a < 0) != (b < 0) {
		q--
	}
	return q
}

func otlpAttributes(attrs map[string]any) []any {
	keys := make([]string, 0, len(attrs))
	for key := range attrs {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	out := make([]any, 0, len(keys))
	for _, key := range keys {
		value := map[string]any{}
		switch v := attrs[key].(type) {
		case bool:
			value["boolValue"] = v
		case float64:
			value["doubleValue"] = v
		default:
			value["stringValue"] = fmt.Sprint(v)
		}
		out = append(out, map[string]any{"key": key, "value": value})
	}
	return out
}
