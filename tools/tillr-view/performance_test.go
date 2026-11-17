package main

import (
	"database/sql"
	"fmt"
	"net/http"
	"testing"
)

func perfExec(t *testing.T, h *serveHarness, query string, args ...any) {
	t.Helper()
	db, err := sql.Open("sqlite", "file:"+h.store+"?_pragma=busy_timeout(5000)")
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(query, args...); err != nil {
		t.Fatal(err)
	}
}

func seedPerformance(t *testing.T, h *serveHarness) {
	t.Helper()
	perfExec(t, h, `DELETE FROM gate_spans`)
	// Five samples, one failure, fractional durations and two kinds of wait.
	for i, duration := range []float64{1, 2, 3, 4, 10.5} {
		status := "ok"
		if i == 4 {
			status = "FAIL"
		}
		perfExec(t, h, `INSERT INTO gate_spans VALUES (?, 'trace', 'parent', ?, 'run-1', 'tick',
   'gate/python3', 'gate', 'Python gate', ?, ?, ?, '{"pool":1.5,"lock":2}', '{}')`,
			fmt.Sprintf("perf-%d", i), fmt.Sprintf("verdict-%d", i), status,
			int64(1000+i*100)*1e6, int64(1000+i*100)*1e6+int64(duration*1e6))
	}
	// Plan, outside-range and unclocked rows must not contaminate duration samples.
	perfExec(t, h, `INSERT INTO gate_spans VALUES
  ('plan', 'trace', 'parent', 'v-plan', 'run-1', 'tick', 'plan', 'plan', 'plan', 'verified', 1000000000, 9000000000, '{}', '{}'),
  ('outside', 'trace', 'parent', 'v-out', 'run-2', 'tick', 'gate/python3', 'gate', 'Python gate', 'FAIL', 2000000000, 9000000000, '{}', '{}'),
  ('unclocked', 'trace', 'parent', 'v-none', 'run-2', 'tick', 'gate/python3', 'gate', 'Python gate', 'unknown', 0, 0, '{}', '{}')`)
}

func performanceData(t *testing.T, h *serveHarness, path string) map[string]any {
	t.Helper()
	status, body, raw := h.get(path)
	if status != http.StatusOK {
		t.Fatalf("%s: HTTP %d: %s", path, status, raw)
	}
	data, ok := body["data"].(map[string]any)
	if !ok {
		t.Fatalf("%s: no data: %s", path, raw)
	}
	return data
}

func TestPerformanceChecksRangeAndStatistics(t *testing.T) {
	h := newServeHarness(t)
	seedPerformance(t, h)
	data := performanceData(t, h, "/api/v1/perf/checks?from_ms=1000&to_ms=2000")
	checks := data["checks"].([]any)
	if len(checks) != 1 {
		t.Fatalf("checks = %v", checks)
	}
	check := checks[0].(map[string]any)
	for key, want := range map[string]any{"check_id": "gate/python3", "count": float64(5), "sample_count": float64(5), "median_ms": float64(3), "p95_ms": float64(10.5), "max_ms": float64(10.5), "waited_ms": float64(17.5), "failures": float64(1)} {
		if check[key] != want {
			t.Errorf("%s = %v, want %v", key, check[key], want)
		}
	}
	check = performanceData(t, h, "/api/v1/perf/checks?from_ms=1000&to_ms=1400")["checks"].([]any)[0].(map[string]any)
	if check["p95_ms"] != nil || check["count"] != float64(4) || check["median_ms"] != float64(2.5) {
		t.Fatalf("small sample = %v", check)
	}
	if got := performanceData(t, h, "/api/v1/perf/checks?from_ms=3000")["checks"].([]any); len(got) != 0 {
		t.Fatalf("empty range = %v", got)
	}
}

func TestPerformanceCheckExecutions(t *testing.T) {
	h := newServeHarness(t)
	seedPerformance(t, h)
	data := performanceData(t, h, "/api/v1/checks/gate%2Fpython3")
	executions := data["executions"].([]any)
	if len(executions) != 7 {
		t.Fatalf("executions = %v", executions)
	}
	first := executions[0].(map[string]any)
	if first["run_id"] != "run-2" || first["item_id"] != "item-seed-04" || first["duration_ms"] != nil {
		t.Fatalf("unclocked execution = %v", first)
	}
	last := executions[6].(map[string]any)
	if last["run_id"] != "run-2" || last["item_id"] != "item-seed-04" {
		t.Fatalf("last execution = %v", last)
	}
	status, body, _ := h.get("/api/v1/checks/missing")
	if status != 404 || body["error"].(map[string]any)["code"] != "check-not-found" {
		t.Fatalf("unknown check: %d %v", status, body)
	}
}

func TestPerformanceItemsLowerBounds(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `INSERT INTO runs (run_id,item_id,lane_id,backend,state,started_at,finished_at,usage_tokens,usage_cost_usd) VALUES
  ('partial','item-seed-02','lane-a','pi','finished',1200,1300,10,NULL),
  ('none','item-seed-02','lane-a','pi','finished',1400,1500,NULL,NULL),
  ('zero','item-seed-02','lane-a','pi','finished',2000,2100,0,0)`)
	data := performanceData(t, h, "/api/v1/perf/items?from_ms=1000&to_ms=2000")
	items := data["items"].([]any)
	if len(items) != 1 {
		t.Fatalf("items = %v", items)
	}
	item := items[0].(map[string]any)
	for key, want := range map[string]any{"item_id": "item-seed-02", "run_count": float64(3), "usage_tokens": float64(1244), "cost_usd": float64(.25), "unrecorded_runs": float64(1), "tokens_unrecorded_runs": float64(1), "cost_unrecorded_runs": float64(2), "lower_bound": true} {
		if item[key] != want {
			t.Errorf("%s = %v, want %v", key, item[key], want)
		}
	}
	item = performanceData(t, h, "/api/v1/perf/items?from_ms=3000")["items"].([]any)[0].(map[string]any)
	if item["usage_tokens"] != nil || item["cost_usd"] != nil || item["unrecorded_runs"] != float64(1) {
		t.Fatalf("unrecorded = %v", item)
	}
	item = performanceData(t, h, "/api/v1/perf/items?from_ms=2000&to_ms=3000")["items"].([]any)[0].(map[string]any)
	if item["usage_tokens"] != float64(0) || item["cost_usd"] != float64(0) {
		t.Fatalf("recorded zero = %v", item)
	}
}

func TestPerformancePreservesUnassociatedAndSubmillisecondChecks(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `DELETE FROM gate_spans`)
	perfExec(t, h, `INSERT INTO gate_spans VALUES
		('early', 'trace', 'parent', 'v', 'missing-run', 'tick', 'early-check', 'check', 'early', 'ERR', 100, 600, '{}', '{}'),
		('inverted', 'trace', 'parent', 'v2', 'run-1', 'tick', 'early-check', 'check', 'early', 'STALE', 500000, 400000, '{}', '{}')`)
	data := performanceData(t, h, "/api/v1/checks/early-check")
	execution := data["executions"].([]any)[0].(map[string]any)
	if execution["item_id"] != nil || execution["run_id"] != "missing-run" || execution["duration_ms"] != float64(.0005) {
		t.Fatalf("unassociated execution = %v", execution)
	}
	check := performanceData(t, h, "/api/v1/perf/checks?from_ms=0&to_ms=1")["checks"].([]any)
	if len(check) != 1 {
		t.Fatalf("submillisecond checks = %v", check)
	}
	row := check[0].(map[string]any)
	if row["count"] != float64(2) || row["sample_count"] != float64(1) || row["failures"] != float64(0) {
		t.Fatalf("submillisecond statistics = %v", row)
	}
}

func TestPerformancePercentileIsNotAlwaysTheMaximum(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `DELETE FROM gate_spans`)
	for i := 1; i <= 20; i++ {
		perfExec(t, h, `INSERT INTO gate_spans VALUES (?, 'trace', 'parent', ?, 'run-1',
			'tick', 'twenty', 'check', 'twenty', 'ok', 1000000000, ?, '{}', '{}')`,
			fmt.Sprintf("sample-%02d", i), fmt.Sprintf("v-%d", i), int64(1000+i)*1e6)
	}
	row := performanceData(t, h, "/api/v1/perf/checks")["checks"].([]any)[0].(map[string]any)
	if row["p95_ms"] != float64(19) || row["max_ms"] != float64(20) || row["median_ms"] != float64(10.5) {
		t.Fatalf("twenty samples = %v", row)
	}
}

func TestPerformanceUsesProducerWaitAndFailureSemantics(t *testing.T) {
	h := newServeHarness(t)
	seedPerformance(t, h)
	perfExec(t, h, `UPDATE gate_spans SET waited_ms_json = '{}'`)
	perfExec(t, h, `UPDATE gate_spans SET waited_ms_json = '{"pool":100,"heavy":60,"exclusive":40,"writes":20,"cheap":5,"after":7}', status = 'REFUSED' WHERE span_id = 'perf-0'`)
	check := performanceData(t, h, "/api/v1/perf/checks?from_ms=1000&to_ms=2000")["checks"].([]any)[0].(map[string]any)
	if check["waited_ms"] != float64(132) || check["failures"] != float64(2) {
		t.Fatalf("producer-shaped measurements = %v, want 132 ms and two negative judgments", check)
	}
	perfExec(t, h, `UPDATE gate_spans SET waited_ms_json = '{"pool":100,"heavy":null}' WHERE span_id = 'perf-0'`)
	status, _, raw := h.get("/api/v1/perf/checks")
	if status != 503 {
		t.Fatalf("malformed wait subcategory: %d %s", status, raw)
	}
}

func TestPerformanceBadRanges(t *testing.T) {
	h := newServeHarness(t)
	for _, route := range []string{"perf/checks", "perf/items"} {
		for _, query := range []string{"from_ms=no", "to_ms=-1", "from_ms=2&to_ms=1", "from_ms=1&to_ms=1", "from_ms=9223372036854775808", "from_ms=1&from_ms=2", "from_ms="} {
			status, body, _ := h.get("/api/v1/" + route + "?" + query)
			if status != 400 || body["error"].(map[string]any)["code"] != "bad-query" {
				t.Errorf("%s?%s: %d %v", route, query, status, body)
			}
		}
	}
}

func TestPerformanceMissingAndUnreadableMeasurements(t *testing.T) {
	t.Run("missing table", func(t *testing.T) {
		h := newServeHarness(t)
		perfExec(t, h, `DROP TABLE gate_spans`)
		for _, path := range []string{"/api/v1/perf/checks", "/api/v1/checks/test"} {
			status, body, _ := h.get(path)
			if status != 503 || body["error"].(map[string]any)["code"] != "table-absent" {
				t.Errorf("%s: %d %v", path, status, body)
			}
		}
	})
	t.Run("invalid wait", func(t *testing.T) {
		h := newServeHarness(t)
		seedPerformance(t, h)
		for _, rawWait := range []string{`{"pool":"unknown"}`, `{"pool":null}`, `{"pool":-1}`, `null`, `[1]`} {
			perfExec(t, h, `UPDATE gate_spans SET waited_ms_json = ? WHERE span_id='perf-0'`, rawWait)
			status, _, raw := h.get("/api/v1/perf/checks")
			if status != 503 {
				t.Fatalf("invalid recorded wait %s: %d %s", rawWait, status, raw)
			}
		}
	})
	t.Run("missing clocks", func(t *testing.T) {
		h := newServeHarness(t)
		seedPerformance(t, h)
		perfExec(t, h, `DELETE FROM gate_spans WHERE span_id != 'unclocked'`)
		check := performanceData(t, h, "/api/v1/perf/checks")["checks"].([]any)[0].(map[string]any)
		if check["count"] != float64(1) || check["sample_count"] != float64(0) || check["median_ms"] != nil || check["max_ms"] != nil || check["p95_ms"] != nil {
			t.Fatalf("unclocked = %v", check)
		}
	})
}

func TestPerformanceFastChecksCarryBudgetOverrunsAndStalls(t *testing.T) {
	h := newServeHarness(t)
	perfExec(t, h, `DELETE FROM gate_spans`)
	for i, status := range []string{"pass", "stalled", "pass", "fail", "pass"} {
		perfExec(t, h, `INSERT INTO gate_spans VALUES (?, 'trace','parent',?,'run-1','tick','documents','fast','documents',?,1000000000,?, '{}', '{"budget_ms":2000,"over_budget_ms":100,"duration_ms":2100,"silent_samples":60}')`, fmt.Sprintf("fast-%d", i), fmt.Sprintf("v-%d", i), status, int64(3100)*1e6)
	}
	check := performanceData(t, h, "/api/v1/perf/checks")["checks"].([]any)[0].(map[string]any)
	for key, want := range map[string]any{"budget_ms": float64(2000), "overruns": float64(5), "stalls": float64(1), "failures": float64(1), "median_ms": float64(2100), "p95_ms": float64(2100), "max_ms": float64(2100)} {
		if check[key] != want {
			t.Errorf("%s = %v want %v", key, check[key], want)
		}
	}
}
