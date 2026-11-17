package main

import (
	"database/sql"
	"encoding/json"
	"fmt"
	"math"
	"net/http"
	"net/url"
	"sort"
	"strconv"

	"github.com/mschulkind-oss/tillr/store"
)

// performanceRange is a half-open interval of Unix milliseconds. An omitted bound
// is unbounded; an entirely unbounded request also includes executions without clocks.
type performanceRange struct {
	FromMS *int64 `json:"from_ms"`
	ToMS   *int64 `json:"to_ms"`
}

func parsePerformanceRange(req *http.Request) (performanceRange, *apiFailure) {
	var out performanceRange
	query, err := url.ParseQuery(req.URL.RawQuery)
	if err != nil {
		return out, failure(codeBadQuery, "invalid query: %v", err)
	}
	for _, field := range []struct {
		name string
		dest **int64
	}{{"from_ms", &out.FromMS}, {"to_ms", &out.ToMS}} {
		values, present := query[field.name]
		if !present {
			continue
		}
		if len(values) != 1 {
			return out, failure(codeBadQuery, "%s must appear once as a nonnegative Unix millisecond integer", field.name)
		}
		value, err := strconv.ParseInt(values[0], 10, 64)
		if err != nil || value < 0 {
			return out, failure(codeBadQuery, "%s must be a nonnegative Unix millisecond integer", field.name)
		}
		*field.dest = &value
	}
	if out.FromMS != nil && out.ToMS != nil && *out.FromMS >= *out.ToMS {
		return out, failure(codeBadQuery, "from_ms must be less than to_ms (the upper bound is exclusive)")
	}
	return out, nil
}

// predicate uses bound parameters and divides recorded nanoseconds in SQLite rather
// than multiplying a caller's milliseconds, which could overflow an int64.
func (p performanceRange) predicate(clock, recordedClock string) (string, []any) {
	clause := ""
	args := []any{}
	if p.FromMS != nil || p.ToMS != nil {
		clause += " AND " + recordedClock + " > 0"
	}
	if p.FromMS != nil {
		clause += " AND " + clock + " >= ?"
		args = append(args, *p.FromMS)
	}
	if p.ToMS != nil {
		clause += " AND " + clock + " < ?"
		args = append(args, *p.ToMS)
	}
	return clause, args
}

type checkExecution struct {
	gateSpanView
	RunID      string   `json:"run_id"`
	ItemID     *string  `json:"item_id"`
	VerdictID  string   `json:"verdict_id"`
	TickID     string   `json:"tick_id"`
	DurationMS *float64 `json:"duration_ms"`
}

// readCheckExecutions retains every execution, including one whose run or clock is
// absent. The left join leaves an unknown item null rather than dropping its check.
func readCheckExecutions(r *store.Reader, bounds performanceRange, checkID string) ([]checkExecution, *apiFailure) {
	exists, err := gateSpansTable(r)
	if err != nil {
		return nil, unreadable(err)
	}
	if !exists {
		return nil, failure(codeTableAbsent, "the store has no gate_spans table: no per-check measurements were recorded by this store's writer")
	}
	clause, args := bounds.predicate("(g.start_unix_nano / 1000000)", "g.start_unix_nano")
	if checkID != "" {
		clause += " AND g.check_id = ?"
		args = append(args, checkID)
	} else {
		clause += " AND g.check_id != 'plan'"
	}
	rows, err := r.Query(`SELECT g.span_id, g.trace_id, g.parent_span_id, g.verdict_id, g.run_id,
  g.tick_id, g.check_id, g.kind, g.label, g.status, g.start_unix_nano, g.end_unix_nano,
  g.waited_ms_json, g.attributes_json, r.item_id FROM gate_spans g
  LEFT JOIN runs r ON r.run_id = g.run_id WHERE 1=1`+clause+` ORDER BY g.start_unix_nano, g.span_id`, args...)
	if err != nil {
		return nil, unreadable(err)
	}
	defer rows.Close()
	out := []checkExecution{}
	for rows.Next() {
		var span storeGateSpan
		var item sql.NullString
		if err := rows.Scan(&span.SpanID, &span.TraceID, &span.ParentSpanID, &span.VerdictID, &span.RunID, &span.TickID, &span.CheckID, &span.Kind, &span.Label, &span.Status, &span.StartUnixNano, &span.EndUnixNano, &span.WaitedMSJSON, &span.AttributesJSON, &item); err != nil {
			return nil, unreadable(err)
		}
		execution := checkExecution{gateSpanView: gateSpanViewOf(span), RunID: span.RunID, VerdictID: span.VerdictID, TickID: span.TickID}
		if item.Valid {
			execution.ItemID = &item.String
		}
		if span.StartUnixNano > 0 && span.EndUnixNano >= span.StartUnixNano {
			duration := float64(span.EndUnixNano-span.StartUnixNano) / 1e6
			execution.DurationMS = &duration
		}
		out = append(out, execution)
	}
	if err := rows.Err(); err != nil {
		return nil, unreadable(err)
	}
	return out, nil
}

type checkPerformance struct {
	CheckID     string   `json:"check_id"`
	Count       int      `json:"count"`
	SampleCount int      `json:"sample_count"`
	MedianMS    *float64 `json:"median_ms"`
	P95MS       *float64 `json:"p95_ms"`
	MaxMS       *float64 `json:"max_ms"`
	WaitedMS    float64  `json:"waited_ms"`
	Failures    int      `json:"failures"`
	BudgetMS    *int64   `json:"budget_ms"`
	Overruns    int      `json:"overruns"`
	Stalls      int      `json:"stalls"`
	samples     []float64
}

// recordedWait sums disjoint wait causes. The producer records heavy and exclusive
// as subcategories already included in pool, not additional waits. Malformed or
// negative measurements (including subcategories) are unreadable, never zero.
func recordedWait(raw string) (float64, error) {
	var categories map[string]*float64
	if err := json.Unmarshal([]byte(raw), &categories); err != nil {
		return 0, err
	}
	if categories == nil {
		return 0, fmt.Errorf("waited_ms_json must be an object")
	}
	var total float64
	for name, recorded := range categories {
		if recorded == nil {
			return 0, fmt.Errorf("wait for %s was not recorded as a number", name)
		}
		value := *recorded
		if value < 0 || math.IsInf(value, 0) || math.IsNaN(value) {
			return 0, fmt.Errorf("invalid wait for %s", name)
		}
		if name != "heavy" && name != "exclusive" {
			total += value
		}
	}
	if math.IsInf(total, 0) {
		return 0, fmt.Errorf("wait total overflow")
	}
	return total, nil
}

func (s *server) performanceChecksPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	bounds, fail := parsePerformanceRange(req)
	if fail != nil {
		return nil, fail
	}
	executions, fail := readCheckExecutions(r, bounds, "")
	if fail != nil {
		return nil, fail
	}
	groups := map[string]*checkPerformance{}
	for _, execution := range executions {
		group := groups[execution.CheckID]
		if group == nil {
			group = &checkPerformance{CheckID: execution.CheckID}
			groups[execution.CheckID] = group
		}
		group.Count++
		if execution.DurationMS != nil {
			group.samples = append(group.samples, *execution.DurationMS)
		}
		if execution.Status == "FAIL" || execution.Status == "REFUSED" || execution.Status == "fail" {
			group.Failures++
		}
		var attrs struct {
			BudgetMS     *int64 `json:"budget_ms"`
			OverBudgetMS int64  `json:"over_budget_ms"`
		}
		if err := json.Unmarshal([]byte(execution.AttributesJSON), &attrs); err != nil {
			return nil, unreadable(fmt.Errorf("check span %s attributes: %w", execution.SpanID, err))
		}
		if attrs.BudgetMS != nil {
			group.BudgetMS = attrs.BudgetMS
		}
		if attrs.OverBudgetMS > 0 {
			group.Overruns++
		}
		if execution.Status == "stalled" {
			group.Stalls++
		}
		waited, err := recordedWait(execution.WaitedMSJSON)
		if err != nil {
			return nil, unreadable(fmt.Errorf("check span %s wait: %w", execution.SpanID, err))
		}
		group.WaitedMS += waited
		if math.IsInf(group.WaitedMS, 0) {
			return nil, unreadable(fmt.Errorf("check %s wait total overflow", execution.CheckID))
		}
	}
	checks := []checkPerformance{}
	for _, group := range groups {
		sort.Float64s(group.samples)
		n := len(group.samples)
		group.SampleCount = n
		if n > 0 {
			median := group.samples[n/2]
			if n%2 == 0 {
				median = group.samples[n/2-1]/2 + median/2
			}
			maximum := group.samples[n-1]
			group.MedianMS, group.MaxMS = &median, &maximum
			// Nearest-rank 95th percentile: the sorted sample at ceil(0.95*n).
			// Fewer than five timed executions is insufficient, so it stays null.
			if n >= 5 {
				p95 := group.samples[int(math.Ceil(.95*float64(n)))-1]
				group.P95MS = &p95
			}
		}
		checks = append(checks, *group)
	}
	sort.Slice(checks, func(i, j int) bool { return checks[i].CheckID < checks[j].CheckID })
	return struct {
		Range  performanceRange   `json:"range"`
		Checks []checkPerformance `json:"checks"`
	}{bounds, checks}, nil
}

func (s *server) checkExecutionsPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	id := req.PathValue("id")
	executions, fail := readCheckExecutions(r, performanceRange{}, id)
	if fail != nil {
		return nil, fail
	}
	if len(executions) == 0 {
		return nil, failure(codeCheckNotFound, "the store carries no executions of check %q: read /api/v1/perf/checks for recorded checks", id)
	}
	return struct {
		CheckID    string           `json:"check_id"`
		Executions []checkExecution `json:"executions"`
	}{id, executions}, nil
}

type itemPerformance struct {
	Usage                usageSum `json:"usage"`
	ItemID               string   `json:"item_id"`
	RunCount             int64    `json:"run_count"`
	UsageTokens          *int64   `json:"usage_tokens"`
	LowerBound           bool     `json:"lower_bound"`
	UnrecordedRuns       int64    `json:"unrecorded_runs"`
	TokensUnrecordedRuns int64    `json:"tokens_unrecorded_runs"`
}

func (s *server) performanceItemsPayload(r *store.Reader, req *http.Request) (any, *apiFailure) {
	bounds, fail := parsePerformanceRange(req)
	if fail != nil {
		return nil, fail
	}
	runs, err := readUsageRuns(r)
	if err != nil {
		return nil, unreadable(err)
	}
	groups := map[string][]storeRun{}
	for _, run := range runs {
		if run.ItemID != "" && usageInRange(run.StartedAtMS, bounds) {
			groups[run.ItemID] = append(groups[run.ItemID], run)
		}
	}
	ids := []string{}
	for id := range groups {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	items := []itemPerformance{}
	for _, id := range ids {
		usage, err := sumUsage(r, groups[id])
		if err != nil {
			return nil, unreadable(err)
		}
		items = append(items, itemPerformance{ItemID: id, RunCount: int64(len(groups[id])), Usage: usage,
			UsageTokens: usage.Tokens, LowerBound: usage.LowerBound,
			UnrecordedRuns: int64(usage.UnrecordedRuns), TokensUnrecordedRuns: int64(usage.UnrecordedRuns)})
	}
	return struct {
		Range performanceRange  `json:"range"`
		Items []itemPerformance `json:"items"`
	}{bounds, items}, nil
}
