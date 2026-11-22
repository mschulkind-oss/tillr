package drain

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const gateTimingFile = "gate.timing.jsonl"

const (
	gateExecutionRan    = "ran"
	gateExecutionReused = "reused"
	gateExecutionNotRun = "not-run"
)

// This is an observation of the invocation, not a correctness verdict. In
// particular, a reused check's historic command duration is not its wall time here.
type gateCheckMeasurement struct {
	Execution string          `json:"execution,omitempty"`
	WallMS    *int64          `json:"wall_ms,omitempty"`
	GoBuild   json.RawMessage `json:"go_build,omitempty"`
}

type fastGateTimingPayload struct {
	ItemID         string `json:"item_id"`
	RunID          string `json:"run_id"`
	WallMS         int64  `json:"wall_ms"`
	ExitCode       int    `json:"exit_code"`
	SlowestCheck   string `json:"slowest_check"`
	SlowestCheckMS *int64 `json:"slowest_check_ms,omitempty"`
	Median20WallMS *int64 `json:"median20_wall_ms,omitempty"`
}

func (s *ProcessSteps) recordGateTiming(ctx context.Context, c Candidate, span StepSpan) {
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
		return
	}
	if observation.WallMS == nil || s.Store == "" {
		return
	}
	p := fastGateTimingPayload{
		ItemID: c.ItemID, RunID: c.RunID, WallMS: *observation.WallMS, ExitCode: observation.ExitCode,
		SlowestCheck: "unmeasured",
	}
	for id, m := range observation.Checks {
		if m.Execution != "ran" || m.WallMS == nil {
			continue
		}
		if p.SlowestCheckMS == nil || *m.WallMS > *p.SlowestCheckMS ||
			(*m.WallMS == *p.SlowestCheckMS && id < p.SlowestCheck) {
			p.SlowestCheck, p.SlowestCheckMS = id, m.WallMS
		}
	}
	if err := appendFastGateTiming(ctx, s.Store, p, time.UnixMilli(span.FinishedAtMS)); err != nil {
		// A measurement is not a judgment: keep the file and the gate outcome.
		fmt.Fprintf(os.Stderr, "drain: could not publish fast gate timing for %s: %v\n", c.RunID, err)
	}
}

func appendFastGateTiming(ctx context.Context, path string, p fastGateTimingPayload, at time.Time) error {
	s, err := store.Open(ctx, path, false)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		// Serialize the read and append so the window ends at this event's
		// position in the store, even when two gate attempts finish together.
		median, err := medianFastGateTiming(tx, p.WallMS)
		if err != nil {
			return err
		}
		p.Median20WallMS = median
		body, err := json.Marshal(p)
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{
			Kind: FastGateTimingKind, ObjectType: "run", ObjectID: &p.RunID, Payload: body, At: at,
		})
		return err
	})
}

func medianFastGateTiming(tx *store.Tx, wallMS int64) (*int64, error) {
	rows, err := tx.Query(`SELECT json_extract(payload, '$.wall_ms')
		FROM events INDEXED BY events_by_kind WHERE kind = ? ORDER BY seq DESC LIMIT 19`, FastGateTimingKind)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	window := make([]int64, 1, 20)
	window[0] = wallMS
	for rows.Next() {
		var ms int64
		if err := rows.Scan(&ms); err != nil {
			return nil, err
		}
		window = append(window, ms)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	if len(window) < 20 {
		return nil, nil
	}
	slices.Sort(window)
	// The measured durations are nonnegative; this floors their mean without
	// overflowing when the two middle values are added.
	median := window[9] + (window[10]-window[9])/2
	return &median, nil
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
			(prev.Execution == gateExecutionNotRun && (m.Execution == gateExecutionRan || m.Execution == gateExecutionReused)) {
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
			Result        string `json:"result"`
			Memory        string `json:"memory"`
			DurationMS    *int64 `json:"duration_ms"`
			StartUnixNano int64  `json:"start_unix_nano"`
			EndUnixNano   int64  `json:"end_unix_nano"`
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
					m := c.gateCheckMeasurement
					if m.Execution == "" {
						memRes := classifyCheck(checkresult.Observation{
							Check:  c.ID,
							Source: checkresult.ResultWord,
							Word:   c.Memory,
						})
						res := classifyCheck(checkresult.Observation{
							Check:  c.ID,
							Source: checkresult.ResultWord,
							Word:   c.Result,
						})
						switch {
						case (memRes.Outcome == checkresult.Pass && memRes.Detail == checkresult.DetailRemembered) ||
							(res.Outcome == checkresult.Pass && res.Detail == checkresult.DetailRemembered):
							m.Execution = gateExecutionReused
						case res.Outcome == checkresult.Unavailable && res.Detail == checkresult.DetailSkipped:
							m.Execution = gateExecutionNotRun
						case ((res.Outcome == checkresult.Pass && res.Detail == checkresult.DetailPassed) || res.Outcome == checkresult.Fail) &&
							c.StartUnixNano > 0 && c.EndUnixNano >= c.StartUnixNano:
							m.Execution = gateExecutionRan
							if m.WallMS == nil && c.DurationMS != nil && *c.DurationMS >= 0 {
								ms := *c.DurationMS
								m.WallMS = &ms
							}
						}
					}
					add(c.ID, m)
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
		var hasWallMS bool
		if span.AttributesJSON != "" {
			if json.Unmarshal([]byte(span.AttributesJSON), &attrs) != nil {
				continue
			}
			var raw map[string]json.RawMessage
			if json.Unmarshal([]byte(span.AttributesJSON), &raw) == nil {
				_, hasWallMS = raw["wall_ms"]
			}
		}
		if attrs.Execution == "" {
			res := classifyCheck(checkresult.Observation{
				Check:  span.CheckID,
				Source: checkresult.CheckSpan,
				Word:   span.Status,
			})
			switch {
			case attrs.Cached || (res.Outcome == checkresult.Pass && res.Detail == checkresult.DetailCached):
				attrs.Execution = gateExecutionReused
			case res.Outcome == checkresult.Unknown && res.Detail == checkresult.DetailNotRun:
				attrs.Execution = gateExecutionNotRun
			case span.StartNS > 0:
				attrs.Execution = gateExecutionRan
			}
		}
		// Only fall back to span timestamps when wall_ms was not emitted at all
		// (for example, an older candidate or direct fork-check run) and the
		// span actually advanced past its start; an unfinished span can have its
		// end copied from its start, which is not a measured zero.
		if attrs.WallMS == nil && !hasWallMS && attrs.Execution == gateExecutionRan &&
			span.StartNS > 0 && span.EndNS > span.StartNS {
			ms := (span.EndNS - span.StartNS) / int64(time.Millisecond)
			attrs.WallMS = &ms
		}
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
