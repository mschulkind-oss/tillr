package checkresult

import "slices"

// AggregateOrder names the consumer's precedence among classified checks.
type AggregateOrder string

const (
	RunnerOrder       AggregateOrder = "runner"
	ShadowOrder       AggregateOrder = "shadow"
	LegacyGateOrder   AggregateOrder = "legacy-gate"
	LegacyReplayOrder AggregateOrder = "legacy-replay"
)

// RunResult carries the prevailing outcome and the consumer's run-level word.
// Shadow runs report only the word; ExitCode is unused for ShadowOrder.
type RunResult struct {
	Outcome  Outcome
	Detail   string
	ExitCode int
}

type aggregateRule struct {
	matches func(Result) bool
	result  RunResult
}

func hasOutcome(outcome Outcome) func(Result) bool {
	return func(r Result) bool { return r.Outcome == outcome }
}

func hasDetail(outcome Outcome, detail string) func(Result) bool {
	return func(r Result) bool { return r.Outcome == outcome && r.Detail == detail }
}

var aggregateRules = map[AggregateOrder][]aggregateRule{
	RunnerOrder: {
		{hasDetail(Unknown, "error"), RunResult{Unknown, "error", 4}},
		{hasDetail(Unknown, "stalled"), RunResult{Unknown, "stalled", 6}},
		{hasOutcome(Fail), RunResult{Fail, "fail", 1}},
		{nil, RunResult{Pass, "pass", 0}},
	},
	ShadowOrder: {
		{hasDetail(Unknown, "error"), RunResult{Unknown, "error", 0}},
		{hasOutcome(Fail), RunResult{Fail, "fail", 0}},
		{nil, RunResult{Pass, "pass", 0}},
	},
	LegacyGateOrder: {
		{hasDetail(Unknown, "stalled"), RunResult{Unknown, "stalled", 6}},
		{hasOutcome(Fail), RunResult{Fail, "failed", 1}},
		{func(r Result) bool { return r.Outcome == Unknown && r.Detail != "not-run" }, RunResult{Unknown, "unproven", 2}},
		{nil, RunResult{Pass, "verified", 0}},
	},
	LegacyReplayOrder: {
		// A legacy check's skipped detail is the machine-local sentinel skip.
		{func(r Result) bool {
			return r.Outcome == Unknown || (r.Outcome == Unavailable && r.Detail == "skipped" && r.RawWord != "skipped: no changed path is read")
		}, RunResult{Unknown, "error", 2}},
		{hasOutcome(Fail), RunResult{Fail, "fail", 1}},
		{nil, RunResult{Pass, "pass", 0}},
	},
}

// Aggregate answers with the first matching rank, independent of the order of
// the checks. An empty run takes the order's fallback. Unknown orders fail closed.
func Aggregate(order AggregateOrder, checks []Result) RunResult {
	for _, rule := range aggregateRules[order] {
		if rule.matches == nil || slices.ContainsFunc(checks, rule.matches) {
			return rule.result
		}
	}
	return RunResult{Unknown, "error", 4}
}
