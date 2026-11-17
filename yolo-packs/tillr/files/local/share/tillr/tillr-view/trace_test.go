package main

import (
	"bytes"
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
)

type parsedTraceSpan struct {
	ID, Parent, Trace string
	Start, End        int64
	Attributes        map[string]any
}

// Decode only the public standard shapes, not the serializer's Go types.
func parseTrace(t *testing.T, raw []byte, format string) []parsedTraceSpan {
	t.Helper()
	var doc map[string]any
	if err := json.Unmarshal(raw, &doc); err != nil {
		t.Fatal(err)
	}
	out := []parsedTraceSpan{}
	if format == "otlp-json" {
		for _, resource := range doc["resourceSpans"].([]any) {
			for _, scope := range resource.(map[string]any)["scopeSpans"].([]any) {
				for _, value := range scope.(map[string]any)["spans"].([]any) {
					row := value.(map[string]any)
					start, err := strconv.ParseInt(row["startTimeUnixNano"].(string), 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					end, err := strconv.ParseInt(row["endTimeUnixNano"].(string), 10, 64)
					if err != nil {
						t.Fatal(err)
					}
					attrs := map[string]any{}
					for _, a := range row["attributes"].([]any) {
						attr := a.(map[string]any)
						for _, v := range attr["value"].(map[string]any) {
							attrs[attr["key"].(string)] = v
						}
					}
					parent, _ := row["parentSpanId"].(string)
					out = append(out, parsedTraceSpan{ID: row["spanId"].(string), Parent: parent, Trace: row["traceId"].(string), Start: start, End: end, Attributes: attrs})
				}
			}
		}
	} else {
		tracks := map[string]float64{}
		usedTracks := map[string]bool{}
		parents := map[string]string{}
		starts, finishes := map[string]map[string]any{}, map[string]map[string]any{}
		for _, value := range doc["traceEvents"].([]any) {
			row := value.(map[string]any)
			if row["ph"] == "s" {
				starts[row["id"].(string)] = row
				continue
			}
			if row["ph"] == "f" {
				finishes[row["id"].(string)] = row
				continue
			}
			if row["ph"] != "X" {
				t.Fatalf("not a complete event or parent flow: %v", row)
			}
			if _, ok := row["tid"].(float64); !ok {
				t.Fatalf("thread id is not numeric: %v", row)
			}
			attrs := row["args"].(map[string]any)
			id := attrs["span_id"].(string)
			track := fmt.Sprint(row["pid"], "/", row["tid"])
			if usedTracks[track] {
				t.Fatalf("two duration spans share a track and can acquire false nesting: %v", row)
			}
			usedTracks[track] = true
			tracks[id] = row["tid"].(float64)
			parents[id] = attrs["parent_span_id"].(string)
			// Whole microseconds: the producer must not rely on fractional ones.
			tsUS, durUS := row["ts"].(float64), row["dur"].(float64)
			if tsUS != math.Trunc(tsUS) || durUS != math.Trunc(durUS) {
				t.Fatalf("chrome ts/dur must be whole microseconds: %v", row)
			}
			start := int64(tsUS) * 1000
			end := start + int64(durUS)*1000
			out = append(out, parsedTraceSpan{ID: attrs["span_id"].(string), Parent: attrs["parent_span_id"].(string), Trace: attrs["trace_id"].(string), Start: start, End: end, Attributes: attrs})
		}
		links := 0
		for id, parent := range parents {
			if parent == "" {
				continue
			}
			links++
			start, finish := starts[id], finishes[id]
			if start == nil || finish == nil || start["tid"] != tracks[parent] || finish["tid"] != tracks[id] || start["pid"] != finish["pid"] || start["ts"] != finish["ts"] || finish["bp"] != "e" || tracks[parent] == tracks[id] {
				t.Fatalf("invalid parent flow for %s: %v -> %v", id, start, finish)
			}
		}
		if len(starts) != links || len(finishes) != links {
			t.Fatalf("flow count does not match parent links")
		}
	}
	return out
}

func assertTraceTree(t *testing.T, spans []parsedTraceSpan) {
	t.Helper()
	byID := map[string]parsedTraceSpan{}
	for _, s := range spans {
		if len(s.ID) != 16 || len(s.Trace) != 32 {
			t.Fatalf("invalid OTLP id widths: %v", s)
		}
		if _, err := strconv.ParseUint(s.ID, 16, 64); err != nil {
			t.Fatal(err)
		}
		if _, exists := byID[s.ID]; exists {
			t.Fatalf("duplicate id: %s", s.ID)
		}
		byID[s.ID] = s
	}
	roots := 0
	for _, s := range spans {
		if s.Parent == "" {
			roots++
			continue
		}
		p, found := byID[s.Parent]
		if !found || p.Trace != s.Trace {
			t.Fatalf("unresolvable parent: %v", s)
		}
		if s.Start < p.Start || s.End > p.End {
			t.Fatalf("child %s [%d,%d] escapes parent [%d,%d]", s.ID, s.Start, s.End, p.Start, p.End)
		}
	}
	if roots != 1 {
		t.Fatalf("roots: %d", roots)
	}
}

func seedTraceChecks(t *testing.T, h *serveHarness) {
	t.Helper()
	db, err := sql.Open("sqlite", h.store)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	for _, stmt := range []string{
		`INSERT INTO gate_spans VALUES ('plan-span','gate-trace','','verdict-1','run-1','tick-1','plan','plan','plan','pass',3000000001,5000000009,'{}','{}')`,
		`INSERT INTO gate_spans VALUES ('check-span','gate-trace','plan-span','verdict-1','run-1','tick-1','unit','check','unit tests','pass',3100000003,4900000007,'{}','{"command":"go test"}')`,
		`INSERT INTO runs VALUES ('run-3','item-seed-02','lane-b','claude','finished',0,3000,4000,'[]',3,NULL,NULL)`,
		`INSERT INTO dispatches VALUES ('decision-3','item-seed-02','lane-b','run-3','dispatch','ready',1500)`,
	} {
		if _, err := db.Exec(stmt); err != nil {
			t.Fatal(err)
		}
	}
}

func TestTraceFormats(t *testing.T) {
	h := newServeHarness(t)
	seedTraceChecks(t, h)
	_, timeline, _ := h.get("/api/v1/runs/run-1/timeline")
	count := len(timeline["data"].(map[string]any)["spans"].([]any))
	for _, format := range []string{"otlp-json", "chrome-trace"} {
		t.Run(format, func(t *testing.T) {
			status, raw := h.getRaw("/api/v1/runs/run-1/trace?format=" + format)
			if status != http.StatusOK {
				t.Fatalf("trace: %d %s", status, raw)
			}
			spans := parseTrace(t, raw, format)
			if len(spans) != count {
				t.Fatalf("export count %d, waterfall count %d", len(spans), count)
			}
			assertTraceTree(t, spans)
			missing, check := false, false
			for _, s := range spans {
				for key, value := range map[string]string{"item_id": "item-seed-02", "run_id": "run-1", "lane_id": "lane-a", "backend": "pi", "pass_id": "19700101T000001Z"} {
					if s.Attributes[key] != value {
						t.Fatalf("attribute %s: %v", key, s.Attributes)
					}
				}
				if s.Attributes["timing_missing"] == true {
					missing = true
					if s.End <= s.Start || s.Attributes["timing_source"] != "parent_envelope" {
						t.Fatalf("missing timing exported as measured/zero: %v", s)
					}
				}
				if s.Attributes["check_id"] == "unit" {
					check = true
					// OTLP preserves nanoseconds; Chrome's event clock is microseconds.
					if format == "otlp-json" && (s.Start != 3100000003 || s.End != 4900000007) {
						t.Fatalf("nanoseconds lost: %v", s)
					}
				}
			}
			if !missing || !check {
				t.Fatalf("missing=%v check=%v", missing, check)
			}
			var cli bytes.Buffer
			if code := traceFace([]string{"--store", h.store, "--trace-index", h.index, "--run", "run-1", "--format", format}, &cli); code != 0 {
				t.Fatalf("CLI exit %d", code)
			}
			var httpBody, cliBody any
			if err := json.Unmarshal(raw, &httpBody); err != nil {
				t.Fatal(err)
			}
			if err := json.Unmarshal(cli.Bytes(), &cliBody); err != nil {
				t.Fatal(err)
			}
			a, _ := json.Marshal(httpBody)
			// The HTTP observatory is deliberately price-free; the CLI retains
			// its recorded trace attributes for terminal analysis.
			b, _ := json.Marshal(stripPrices(cliBody))
			if !bytes.Equal(a, b) {
				t.Fatalf("CLI and route disagree\n%s\n%s", a, b)
			}
		})
	}
}

func TestTraceSelectionsAndRefusals(t *testing.T) {
	h := newServeHarness(t)
	seedTraceChecks(t, h)
	for _, format := range []string{"otlp-json", "chrome-trace"} {
		for _, selector := range [][]string{{"--item", "item-seed-02"}, {"--pass", "19700101T000001Z"}} {
			var out bytes.Buffer
			args := append([]string{"--store", h.store, "--trace-index", h.index, "--format", format}, selector...)
			if code := traceFace(args, &out); code != 0 {
				t.Fatalf("%v exit %d", args, code)
			}
			runs := map[string]string{}
			for _, s := range parseTrace(t, out.Bytes(), format) {
				runs[s.Attributes["run_id"].(string)] = s.Trace
			}
			if len(runs) != 2 || runs["run-1"] == "" || runs["run-3"] == "" || runs["run-1"] == runs["run-3"] {
				t.Fatalf("selected runs: %v", runs)
			}
		}
	}
	for _, test := range []struct {
		args []string
		code int
	}{
		{[]string{"--store", h.store}, 2},
		{[]string{"--store", h.store, "--run", "run-1", "--item", "item-seed-02"}, 2},
		{[]string{"--store", h.store, "--run", "run-1", "--format", "bogus"}, 2},
		{[]string{"--store", h.store, "--pass", "bogus"}, 2},
		{[]string{"--store", h.store, "--run", "absent"}, 1},
		{[]string{"--store", h.store, "--item", "absent"}, 1},
		{[]string{"--store", h.store, "--pass", "20000101T000000Z"}, 1},
	} {
		var out bytes.Buffer
		if code := traceFace(test.args, &out); code != test.code || out.Len() != 0 {
			t.Fatalf("%v: code=%d output=%s", test.args, code, out.String())
		}
	}
	for _, test := range []struct {
		path string
		code int
	}{
		{"/api/v1/runs/run-1/trace?format=bogus", 400},
		{"/api/v1/runs/absent/trace", 404},
		{"/api/v1/runs/run-1/trace", 200},
	} {
		status, raw := h.getRaw(test.path)
		if status != test.code {
			t.Fatalf("%s: %d %s", test.path, status, raw)
		}
	}
}

func TestTraceCommandDispatch(t *testing.T) {
	h := newServeHarness(t)
	bin := filepath.Join(t.TempDir(), "tillr-view")
	if out, err := exec.Command("go", "build", "-o", bin, ".").CombinedOutput(); err != nil {
		t.Fatalf("build: %v %s", err, out)
	}
	cmd := exec.Command(bin, "trace", "--store", h.store, "--trace-index", h.index, "--run", "run-1")
	raw, err := cmd.Output()
	if err != nil {
		t.Fatal(err)
	}
	assertTraceTree(t, parseTrace(t, raw, "otlp-json"))
}

func TestTraceObservationNanoseconds(t *testing.T) {
	db, err := sql.Open("sqlite", ":memory:")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	db.SetMaxOpenConns(1)
	_, err = db.Exec(`
CREATE TABLE runs_index (run_id, log_path, log_state, proc_path, proc_state, git_path, git_state, brief_path, fence_path, base_sha, detail);
INSERT INTO runs_index VALUES ('run',NULL,'ok',NULL,'ok',NULL,'ok',NULL,NULL,NULL,'');
CREATE TABLE obs_events (obs_id,kind,ts_start,ts_end,duration_ms,seam,span_id,summary,payload,run_id,seq);
INSERT INTO obs_events VALUES ('turn','llm.turn','1970-01-01T00:00:01.123456789Z','1970-01-01T00:00:01.987654321Z',864,NULL,NULL,'turn','{}','run',1);`)
	if err != nil {
		t.Fatal(err)
	}
	obs := observations{loopDir: t.TempDir(), trace: &traceIndex{db: db}}
	run := storeRun{RunID: "run", StartedAtMS: 1000, FinishedAtMS: 2000}
	spans, err := exportSpans(run, "", obs.timeline(run))
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, span := range spans {
		if span.ID != "turn" {
			continue
		}
		found = true
		if span.Start != 1123456789 || span.End != 1987654321 || span.Attr["recorded_start_unix_nano"] != "1123456789" {
			t.Fatalf("observation nanoseconds lost: %v", span)
		}
	}
	if !found {
		t.Fatal("indexed observation was not exported")
	}
}

// A fractional-microsecond ts and dur at Unix-epoch scale do not add back to the end
// they came from (float64 has 0.25 us there), so a child could end after its parent.
// The values are the epoch-scale ones measured failing under load. Fixed inputs, no
// clock: equal and rounding-reversed times are listed outright.
func TestChromeWindowKeepsChildInsideParent(t *testing.T) {
	const base = 1794880672995000064
	for _, parentEnd := range []int64{base + 1000000000 - 64, base + 1000000000, base + 1000000000 + 1} {
		for _, childEnd := range []int64{parentEnd, parentEnd - 1, parentEnd - 64} {
			for _, childStart := range []int64{base, base + 1, base + 999} {
				pts, pdur := chromeWindow(base-1, parentEnd)
				cts, cdur := chromeWindow(childStart, childEnd)
				for _, v := range []float64{pts, pdur, cts, cdur} {
					if v != math.Trunc(v) {
						t.Fatalf("fractional microseconds are not exact at epoch scale: %v %v %v %v", pts, pdur, cts, cdur)
					}
				}
				if cts < pts || cts+cdur > pts+pdur {
					t.Fatalf("child [%v,+%v] escapes parent [%v,+%v] for ns child [%d,%d] parent end %d", cts, cdur, pts, pdur, childStart, childEnd, parentEnd)
				}
			}
		}
	}
	if ts, dur := chromeWindow(1500, 2500); ts != 1 || dur != 2 {
		t.Fatalf("round down start and up end: %v %v", ts, dur)
	}
	if ts, dur := chromeWindow(1000, 1000); ts != 1 || dur != 0 {
		t.Fatalf("instant: %v %v", ts, dur)
	}
}

func TestExportSpanNormalization(t *testing.T) {
	ms := func(v int64) *int64 { return &v }
	parent := func(v string) *string { return &v }
	run := storeRun{RunID: "run", LaneID: "lane"}
	spans := []span{
		{SpanID: "run/run", StartedAtMS: ms(1000), FinishedAtMS: ms(2000)},
		{SpanID: "turn", ParentSpanID: parent("run/run"), StartedAtMS: ms(500), FinishedAtMS: ms(3000)},
		{SpanID: "tool", ParentSpanID: parent("turn"), StartedAtMS: ms(600), FinishedAtMS: ms(2900)},
		{SpanID: "unknown", ParentSpanID: parent("turn")},
	}
	normalized, err := exportSpans(run, "", spans)
	if err != nil {
		t.Fatal(err)
	}
	if normalized[0].Start != 500000000 || normalized[0].End != 3000000000 || normalized[0].Attr["recorded_start_unix_nano"] != "1000000000" {
		t.Fatalf("root expansion: %v", normalized[0])
	}
	if normalized[3].Start != normalized[1].Start || normalized[3].End != normalized[1].End || normalized[3].Attr["timing_missing"] != true {
		t.Fatalf("unknown interval: %v", normalized[3])
	}
	spans[3].ParentSpanID = parent("group")
	spans = append(spans,
		span{SpanID: "group", ParentSpanID: parent("run/run")},
		span{SpanID: "nested", ParentSpanID: parent("group"), StartedAtMS: ms(600), FinishedAtMS: ms(900)},
	)
	normalized, err = exportSpans(run, "", spans)
	if err != nil {
		t.Fatal(err)
	}
	if normalized[4].Start != normalized[0].Start || normalized[4].End != normalized[0].End || normalized[4].Attr["timing_source"] != "parent_envelope" {
		t.Fatalf("untimed internal span did not inherit its parent's envelope: %v", normalized[4])
	}
	spans[1].ParentSpanID = parent("tool")
	if _, err := exportSpans(run, "", spans); err == nil || !strings.Contains(err.Error(), "cyclic") {
		t.Fatalf("cycle: %v", err)
	}
	spans = []span{
		{SpanID: "run/run", StartedAtMS: ms(1000), FinishedAtMS: ms(2000)},
		{SpanID: "instant", ParentSpanID: parent("run/run"), StartedAtMS: ms(1500), FinishedAtMS: ms(1500)},
		{SpanID: "unknown", ParentSpanID: parent("instant")},
	}
	normalized, err = exportSpans(run, "", spans)
	if err != nil {
		t.Fatal(err)
	}
	if normalized[2].End <= normalized[2].Start || normalized[2].Start < normalized[1].Start || normalized[2].End > normalized[1].End {
		t.Fatalf("unknown child of a recorded instant became zero or escaped its parent: %v", normalized)
	}
	if _, err := exportSpans(run, "", []span{{SpanID: "run/run"}}); err == nil {
		t.Fatal("invented a clock for an entirely unrecorded run")
	}
}
