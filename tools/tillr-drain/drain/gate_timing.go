package drain

import (
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
)

const gateTimingFile = "gate.timing.jsonl"

// This is an observation of the invocation, not a correctness verdict. In
// particular, a reused check's historic command duration is not its wall time here.
type gateCheckMeasurement struct {
	Execution string          `json:"execution,omitempty"`
	WallMS    *int64          `json:"wall_ms,omitempty"`
	GoBuild   json.RawMessage `json:"go_build,omitempty"`
}

func (s *ProcessSteps) recordGateTiming(c Candidate, span StepSpan) {
	if s.Root == "" || span.ExitCode == nil {
		return // no run directory or the program never started
	}
	dir := RunDir(s.Root, c.RunID)
	observation := struct {
		RunID       string                          `json:"run_id"`
		ExecutionID string                          `json:"gate_execution_id"`
		WallMS      *int64                          `json:"wall_ms,omitempty"`
		ExitCode    int                             `json:"exit_code"`
		Checks      map[string]gateCheckMeasurement `json:"checks"`
	}{RunID: c.RunID, ExecutionID: rand.Text(), ExitCode: *span.ExitCode, Checks: readGateTimingChecks(dir)}
	if ms := span.FinishedAtMS - span.StartedAtMS; ms >= 0 {
		observation.WallMS = &ms
	}
	if err := appendRunLine(s.Root, c.RunID, gateTimingFile, observation); err != nil {
		fmt.Fprintf(os.Stderr, "drain: could not record fast gate timing for %s: %v\n", c.RunID, err)
	}
}

// Only the two phase handoffs are evidence here. The outer verdict judges the
// wrapper, not an individual check, and may be deferred or later rewritten.
func readGateTimingChecks(dir string) map[string]gateCheckMeasurement {
	checks := map[string]gateCheckMeasurement{}
	add := func(id string, m gateCheckMeasurement) {
		if id == "" || id == "plan" || strings.HasPrefix(id, "gate:") {
			return
		}
		prev, seen := checks[id]
		// A skip in one phase must not mask execution or reuse in the other.
		if !seen || (prev.Execution == "" && m.Execution != "") ||
			(prev.Execution == "not-run" && (m.Execution == "ran" || m.Execution == "reused")) {
			checks[id] = m
		} else if prev.Execution == m.Execution {
			if prev.WallMS == nil {
				prev.WallMS = m.WallMS
			}
			if len(prev.GoBuild) == 0 {
				prev.GoBuild = m.GoBuild
			}
			checks[id] = prev
		}
	}
	if body, err := os.ReadFile(filepath.Join(dir, GateFastVerdictFile)); err == nil {
		type check struct {
			ID string `json:"check_id"`
			gateCheckMeasurement
		}
		var verdict struct {
			Payload struct {
				Checks     []check `json:"checks"`
				FastTiming struct {
					Checks []check `json:"checks"`
				} `json:"fast_timing"`
			} `json:"payload"`
		}
		if json.Unmarshal(body, &verdict) == nil {
			// A landing-tier stall can replace the fast verdict with a bare
			// payload envelope and attach the original phase under fast_timing.
			for _, group := range [][]check{verdict.Payload.FastTiming.Checks, verdict.Payload.Checks} {
				for _, c := range group {
					add(c.ID, c.gateCheckMeasurement)
				}
			}
		}
	}
	var spans gateSpansFile
	body, err := os.ReadFile(filepath.Join(dir, GateSpansFile))
	if err != nil || json.Unmarshal(body, &spans) != nil {
		return checks
	}
	for _, span := range spans.CheckSpans {
		attrs := struct {
			gateCheckMeasurement
			Cached bool `json:"cached"`
		}{}
		if span.AttributesJSON != "" && json.Unmarshal([]byte(span.AttributesJSON), &attrs) != nil {
			continue
		}
		if attrs.Execution == "" {
			switch {
			case attrs.Cached || span.Status == "CACHED":
				attrs.Execution = "reused"
			case span.Status == "not-run":
				attrs.Execution = "not-run"
			case span.StartNS > 0:
				attrs.Execution = "ran"
			}
		}
		// Do not fall back to the span timestamps: an unfinished span can
		// have its end copied from its start, which is not a measured zero.
		add(span.CheckID, attrs.gateCheckMeasurement)
	}
	for _, id := range spans.ReusedChecks {
		add(id, gateCheckMeasurement{Execution: "reused", WallMS: spans.ReusedWallMS[id]})
	}
	for _, id := range spans.NotRunChecks {
		add(id, gateCheckMeasurement{Execution: "not-run"})
	}
	for _, skip := range spans.SkippedChecks {
		if ran := skip.ExecutedCheck; ran != nil {
			add(ran.CheckID, ran.gateCheckMeasurement)
		} else {
			add(skip.CheckID, gateCheckMeasurement{Execution: "not-run"})
		}
	}
	return checks
}
