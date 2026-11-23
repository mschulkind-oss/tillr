package main

import (
	"bytes"
	"strings"
	"testing"
)

func TestDisabledCheckExcludedFromPassAndFailCountsAndListedAsDisabled(t *testing.T) {
	verdict := VerdictPayload{
		TreeSHA: "tree-disabled-summary",
		Result:  ResultPass,
		Checks: []VerdictCheck{
			{CheckID: "fresh-green", Result: ResultPass},
			{CheckID: "cached-green", Result: ResultPass, Memory: "remembered", PassTime: "2026-11-21T01:00:00Z"},
			{
				CheckID:    "fast-sample",
				Result:     ResultDisabled,
				PassTime:   "2026-11-21T02:00:00Z",
				PassLogRef: "/store/disabled/ep1.pass.log",
				FailTime:   "2026-11-21T02:05:00Z",
				FailLogRef: "/store/disabled/ep1.fail.log",
				RepairItem: "flaky-fast-sample",
			},
			{
				CheckID:  "legacy-sample",
				Result:   ResultDisabled,
				PassTime: "2026-11-21T03:00:00Z",
				FailTime: "2026-11-21T03:01:00Z",
			},
		},
	}

	var buf bytes.Buffer
	writeRunSummary(&buf, verdict)
	out := buf.String()

	wantCounts := "checks: 1 passed, 1 remembered, 0 unavailable, 2 disabled, 0 failed"
	if !strings.Contains(out, wantCounts) {
		t.Fatalf("summary output missing %q:\n%s", wantCounts, out)
	}
	wantFull := "fast-sample: disabled; pass at 2026-11-21T02:00:00Z (/store/disabled/ep1.pass.log), fail at 2026-11-21T02:05:00Z (/store/disabled/ep1.fail.log); repair item flaky-fast-sample"
	if !strings.Contains(out, wantFull) {
		t.Fatalf("summary output missing disabled check listing %q:\n%s", wantFull, out)
	}
	wantFallback := "legacy-sample: disabled; pass at 2026-11-21T03:00:00Z (output location not recorded), fail at 2026-11-21T03:01:00Z (output location not recorded)"
	if !strings.Contains(out, wantFallback) {
		t.Fatalf("summary output missing fallback disabled listing %q:\n%s", wantFallback, out)
	}

	// Even when the only planned check is disabled, the verdict summary counts zero passed and zero failed.
	var onlyBuf bytes.Buffer
	writeRunSummary(&onlyBuf, VerdictPayload{
		TreeSHA: "tree-only-disabled",
		Result:  ResultPass,
		Checks:  []VerdictCheck{{CheckID: "solo-flaky", Result: ResultDisabled}},
	})
	onlyOut := onlyBuf.String()
	wantOnlyCounts := "checks: 0 passed, 0 remembered, 0 unavailable, 1 disabled, 0 failed"
	if !strings.Contains(onlyOut, wantOnlyCounts) {
		t.Fatalf("only-disabled summary output missing %q:\n%s", wantOnlyCounts, onlyOut)
	}
	if !strings.Contains(onlyOut, "solo-flaky: disabled\n") {
		t.Fatalf("only-disabled summary output missing bare disabled listing:\n%s", onlyOut)
	}
}
