package main

import (
	"database/sql"
	"net/http"
	"strconv"
	"testing"
)

// seedGateSpans inserts one plan span and one check span for the harness's run-1. The
// nanosecond bounds are deliberately around 1.7e18: a JSON number that large is above
// JavaScript's `Number.MAX_SAFE_INTEGER` (~9.007e15) and silently loses its low digits in
// a browser, so the wire form is a decimal string and this fixture is what proves it.
func seedGateSpans(t *testing.T, storePath string, start, end int64) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+storePath+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatalf("opening the store to seed gate spans: %v", err)
	}
	defer db.Close()
	checkStart := start + 1_000
	checkEnd := end - 1_000
	statements := []struct {
		spanID, trace, parent, verdict, run, tick, check, kind, label, status string
		start, end                                                            int64
	}{
		{"span-plan", "trace-1", "parent-1", "verdict-1", "run-1", "tick-1",
			"plan", "plan", "gate plan", "verified", start, end},
		{"span-gate", "trace-1", "parent-1", "verdict-1", "run-1", "tick-1",
			"gate/python3", "gate", "gate:python3", "pass", checkStart, checkEnd},
	}
	for _, row := range statements {
		if _, err := db.Exec(
			`INSERT INTO gate_spans (span_id, trace_id, parent_span_id, verdict_id, run_id,
			   tick_id, check_id, kind, label, status, start_unix_nano, end_unix_nano,
			   waited_ms_json, attributes_json)
			 VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, '{"pool":1.5}', '{}')`,
			row.spanID, row.trace, row.parent, row.verdict, row.run, row.tick,
			row.check, row.kind, row.label, row.status, row.start, row.end,
		); err != nil {
			t.Fatalf("seeding the gate span %s: %v", row.spanID, err)
		}
	}
}

// TestGatePayloadCarriesNanosecondStrings is the round-trip the wire contract turns on: a
// Unix nanosecond value around 1.7e18 exceeds `Number.MAX_SAFE_INTEGER`, so the JSON field
// is a decimal string and a client that parses it reads back the exact value. The computed
// `started_at_ms` float rides beside it for the bar geometry.
func TestGatePayloadCarriesNanosecondStrings(t *testing.T) {
	h := newServeHarness(t)
	const start = int64(1_700_000_000_000_000_000) // 1.7e18, above 2^53
	const end = int64(1_700_000_000_000_178_000)
	seedGateSpans(t, h.store, start, end)

	status, body, raw := h.get("/api/v1/runs/run-1/gate")
	if status != http.StatusOK {
		t.Fatalf("the gate route answered %d: %s", status, raw)
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("the gate response carries no data: %s", raw)
	}
	plan, ok := data["plan_span"].(map[string]any)
	if !ok {
		t.Fatalf("the gate response carries no plan span: %s", raw)
	}
	want := strconv.FormatInt(start, 10)
	got, ok := plan["start_unix_nano"].(string)
	if !ok {
		t.Fatalf("start_unix_nano is %T, want a decimal string: %s", plan["start_unix_nano"], raw)
	}
	if got != want {
		t.Fatalf("start_unix_nano is %q, want %q", got, want)
	}
	// The exact value survives: parse the string back and compare the int64. A JSON
	// number would have lost the low digits here, which is the whole defect the string
	// spelling exists to prevent.
	round, err := strconv.ParseInt(got, 10, 64)
	if err != nil {
		t.Fatalf("parsing start_unix_nano %q: %v", got, err)
	}
	if round != start {
		t.Fatalf("start_unix_nano round-trips to %d, want %d", round, start)
	}
	// The float beside it is for the bar's geometry.
	if _, ok := plan["started_at_ms"].(float64); !ok {
		t.Fatalf("started_at_ms is %T, want a number: %s", plan["started_at_ms"], raw)
	}
	spans, ok := data["spans"].([]any)
	if !ok || len(spans) != 1 {
		t.Fatalf("the gate response carries %d check spans, want 1: %s", len(spans), raw)
	}
	check, ok := spans[0].(map[string]any)
	if !ok {
		t.Fatalf("the first span is not an object: %s", raw)
	}
	if check["check_id"] != "gate/python3" {
		t.Fatalf("the check span's id is %v, want gate/python3", check["check_id"])
	}
	checkStart, ok := check["start_unix_nano"].(string)
	if !ok {
		t.Fatalf("the check span's start_unix_nano is %T, want a decimal string", check["start_unix_nano"])
	}
	if want := strconv.FormatInt(start+1_000, 10); checkStart != want {
		t.Fatalf("the check span's start_unix_nano is %q, want %q", checkStart, want)
	}
}
