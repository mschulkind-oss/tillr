package gatespans

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const GateTimingFile = "gate.timing.jsonl"

const (
	GateExecutionRan    = "ran"
	GateExecutionReused = "reused"
	GateExecutionNotRun = "not-run"
)

// GateCheckMeasurement is an observation of the invocation, not a correctness verdict. In
// particular, a reused check's historic command duration is not its wall time here.
type GateCheckMeasurement struct {
	Execution string          `json:"execution,omitempty"`
	WallMS    *int64          `json:"wall_ms,omitempty"`
	GoBuild   json.RawMessage `json:"go_build,omitempty"`
	SelfSuite json.RawMessage `json:"self_suite,omitempty"`
}

type FastGateTimingPayload struct {
	ItemID         string `json:"item_id"`
	RunID          string `json:"run_id"`
	WallMS         int64  `json:"wall_ms"`
	ExitCode       int    `json:"exit_code"`
	SlowestCheck   string `json:"slowest_check"`
	SlowestCheckMS *int64 `json:"slowest_check_ms,omitempty"`
	Median20WallMS *int64 `json:"median20_wall_ms,omitempty"`
}

func RecordGateTiming(ctx context.Context, root, storePath, itemID, runID string, span drainmodel.StepSpan) {
	if root == "" || span.ExitCode == nil {
		return // no run directory or the program never started
	}
	dir := drainmodel.RunDir(root, runID)
	observation := struct {
		RunID       string                          `json:"run_id"`
		ExecutionID string                          `json:"gate_execution_id"`
		WallMS      *int64                          `json:"wall_ms,omitempty"`
		ExitCode    int                             `json:"exit_code"`
		Checks      map[string]GateCheckMeasurement `json:"checks"`
	}{RunID: runID, ExecutionID: rand.Text(), ExitCode: *span.ExitCode, Checks: ReadGateTimingChecks(dir, span)}
	if ms := span.FinishedAtMS - span.StartedAtMS; ms >= 0 {
		observation.WallMS = &ms
	}
	if err := drainmodel.AppendRunLine(root, runID, GateTimingFile, observation); err != nil {
		fmt.Fprintf(os.Stderr, "drain: could not record fast gate timing for %s: %v\n", runID, err)
		return
	}
	if observation.WallMS == nil || storePath == "" {
		return
	}
	p := FastGateTimingPayload{
		ItemID: itemID, RunID: runID, WallMS: *observation.WallMS, ExitCode: observation.ExitCode,
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
	if err := AppendFastGateTiming(ctx, storePath, p, time.UnixMilli(span.FinishedAtMS)); err != nil {
		// A measurement is not a judgment: keep the file and the gate outcome.
		fmt.Fprintf(os.Stderr, "drain: could not publish fast gate timing for %s: %v\n", runID, err)
	}
}

func AppendFastGateTiming(ctx context.Context, path string, p FastGateTimingPayload, at time.Time) error {
	s, err := store.Open(ctx, path, false)
	if err != nil {
		return err
	}
	defer s.Close()
	return s.WriteTx(ctx, func(tx *store.Tx) error {
		// Serialize the read and append so the window ends at this event's
		// position in the store, even when two gate attempts finish together.
		median, err := MedianFastGateTiming(tx, p.WallMS)
		if err != nil {
			return err
		}
		p.Median20WallMS = median
		body, err := json.Marshal(p)
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{
			Kind: storelog.FastGateTimingKind, ObjectType: "run", ObjectID: &p.RunID, Payload: body, At: at,
		})
		return err
	})
}

func MedianFastGateTiming(tx *store.Tx, wallMS int64) (*int64, error) {
	rows, err := tx.Query(`SELECT json_extract(payload, '$.wall_ms')
		FROM events INDEXED BY events_by_kind WHERE kind = ? ORDER BY seq DESC LIMIT 19`, storelog.FastGateTimingKind)
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

// ReadGateTimingChecks reads per-check execution and wall timing from the two phase
// handoffs. The outer verdict judges the wrapper, not an individual check, and may be
// deferred or later rewritten.
func ReadGateTimingChecks(dir string, gateSpans ...drainmodel.StepSpan) map[string]GateCheckMeasurement {
	var gateSpan drainmodel.StepSpan
	if len(gateSpans) > 0 {
		gateSpan = gateSpans[0]
	}
	unfinishedWallMS := func(startedAtMS int64) *int64 {
		if startedAtMS <= 0 || gateSpan.FinishedAtMS <= 0 || gateSpan.FinishedAtMS < gateSpan.StartedAtMS {
			return nil
		}
		if gateSpan.StartedAtMS > 0 && startedAtMS < gateSpan.StartedAtMS {
			return nil
		}
		ms := gateSpan.FinishedAtMS - startedAtMS
		if ms < 0 {
			ms = 0
		}
		return &ms
	}
	checks := map[string]GateCheckMeasurement{}
	add := func(id string, m GateCheckMeasurement) {
		if id == "" || id == "plan" || strings.HasPrefix(id, "gate:") {
			return
		}
		prev, seen := checks[id]
		// A skip in one phase must not mask execution or reuse in the other.
		if !seen || (prev.Execution == "" && m.Execution != "") ||
			(prev.Execution == GateExecutionNotRun && (m.Execution == GateExecutionRan || m.Execution == GateExecutionReused)) {
			checks[id] = m
		} else if prev.Execution == m.Execution {
			if prev.WallMS == nil {
				prev.WallMS = m.WallMS
			}
			if len(prev.GoBuild) == 0 {
				prev.GoBuild = m.GoBuild
			}
			if len(prev.SelfSuite) == 0 {
				prev.SelfSuite = m.SelfSuite
			}
			checks[id] = prev
		}
	}
	if body, err := os.ReadFile(filepath.Join(dir, GateFastVerdictFile)); err == nil {
		type check struct {
			ID string `json:"check_id"`
			GateCheckMeasurement
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
					m := c.GateCheckMeasurement
					if m.Execution == "" {
						memRes := drainmodel.ClassifyCheck(checkresult.Observation{
							Check:  c.ID,
							Source: checkresult.ResultWord,
							Word:   c.Memory,
						})
						res := drainmodel.ClassifyCheck(checkresult.Observation{
							Check:  c.ID,
							Source: checkresult.ResultWord,
							Word:   c.Result,
						})
						switch {
						case (memRes.Outcome == checkresult.Pass && memRes.Detail == checkresult.DetailRemembered) ||
							(res.Outcome == checkresult.Pass && res.Detail == checkresult.DetailRemembered):
							m.Execution = GateExecutionReused
						case res.Outcome == checkresult.Unavailable && res.Detail == checkresult.DetailSkipped:
							m.Execution = GateExecutionNotRun
						case ((res.Outcome == checkresult.Pass && res.Detail == checkresult.DetailPassed) || res.Outcome == checkresult.Fail) &&
							c.StartUnixNano > 0 && c.EndUnixNano >= c.StartUnixNano:
							m.Execution = GateExecutionRan
							if m.WallMS == nil && c.DurationMS != nil && *c.DurationMS >= 0 {
								ms := *c.DurationMS
								m.WallMS = &ms
							}
						case c.StartUnixNano > 0 && c.EndUnixNano <= 0:
							m.Execution = GateExecutionRan
						}
					}
					if m.Execution == GateExecutionRan && m.WallMS == nil && c.StartUnixNano > 0 && c.EndUnixNano <= 0 {
						m.WallMS = unfinishedWallMS(c.StartUnixNano / int64(time.Millisecond))
					}
					add(c.ID, m)
				}
			}
		}
	}
	var spans GateSpansFile
	if body, err := os.ReadFile(filepath.Join(dir, GateSpansFileName)); err == nil && json.Unmarshal(body, &spans) == nil {
		for _, span := range spans.CheckSpans {
			attrs := struct {
				GateCheckMeasurement
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
				res := drainmodel.ClassifyCheck(checkresult.Observation{
					Check:  span.CheckID,
					Source: checkresult.CheckSpan,
					Word:   span.Status,
				})
				switch {
				case attrs.Cached || (res.Outcome == checkresult.Pass && res.Detail == checkresult.DetailCached):
					attrs.Execution = GateExecutionReused
				case res.Outcome == checkresult.Unknown && res.Detail == checkresult.DetailNotRun:
					attrs.Execution = GateExecutionNotRun
				case span.StartNS > 0:
					attrs.Execution = GateExecutionRan
				}
			}
			// Only fall back to span timestamps when wall_ms was not emitted at all
			// (for example, a judging program that reports no wall time) and the
			// span actually advanced past its start; an unfinished span can have its
			// end copied from its start, which is not a measured zero.
			if attrs.WallMS == nil && !hasWallMS && attrs.Execution == GateExecutionRan && span.StartNS > 0 {
				if span.EndNS > span.StartNS {
					ms := (span.EndNS - span.StartNS) / int64(time.Millisecond)
					attrs.WallMS = &ms
				} else if span.EndNS <= 0 {
					attrs.WallMS = unfinishedWallMS(span.StartNS / int64(time.Millisecond))
				}
			}
			add(span.CheckID, attrs.GateCheckMeasurement)
		}
		for _, id := range spans.ReusedChecks {
			add(id, GateCheckMeasurement{Execution: "reused", WallMS: spans.ReusedWallMS[id]})
		}
		for _, id := range spans.NotRunChecks {
			add(id, GateCheckMeasurement{Execution: "not-run"})
		}
		for _, skip := range spans.SkippedChecks {
			if ran := skip.ExecutedCheck; ran != nil {
				add(ran.CheckID, ran.GateCheckMeasurement)
			} else {
				add(skip.CheckID, GateCheckMeasurement{Execution: "not-run"})
			}
		}
	}

	type checkTimingLine struct {
		CheckID      string `json:"check_id"`
		StartedAtMS  int64  `json:"started_at_ms"`
		FinishedAtMS *int64 `json:"finished_at_ms"`
	}
	for _, rel := range []string{filepath.Join("change-logs", GateChecksFile), GateChecksFile} {
		raw, err := os.ReadFile(filepath.Join(dir, rel))
		if err != nil {
			continue
		}
		type partialCheck struct {
			startedAtMS  int64
			finishedAtMS *int64
		}
		var order []string
		byID := map[string]partialCheck{}
		for _, line := range bytes.Split(raw, []byte("\n")) {
			line = bytes.TrimSpace(line)
			if len(line) == 0 {
				continue
			}
			var entry checkTimingLine
			if json.Unmarshal(line, &entry) != nil || entry.CheckID == "" {
				continue
			}
			p, seen := byID[entry.CheckID]
			if !seen {
				order = append(order, entry.CheckID)
			}
			if entry.StartedAtMS > 0 && (p.startedAtMS == 0 || entry.StartedAtMS < p.startedAtMS) {
				p.startedAtMS = entry.StartedAtMS
			}
			if entry.FinishedAtMS != nil && (p.finishedAtMS == nil || *entry.FinishedAtMS >= *p.finishedAtMS) {
				fin := *entry.FinishedAtMS
				p.finishedAtMS = &fin
			}
			byID[entry.CheckID] = p
		}
		for _, id := range order {
			p := byID[id]
			if p.startedAtMS <= 0 {
				continue
			}
			var wallMS *int64
			if p.finishedAtMS != nil {
				if ms := *p.finishedAtMS - p.startedAtMS; ms >= 0 {
					wallMS = &ms
				}
			} else {
				wallMS = unfinishedWallMS(p.startedAtMS)
			}
			add(id, GateCheckMeasurement{Execution: GateExecutionRan, WallMS: wallMS})
		}
	}
	return checks
}
