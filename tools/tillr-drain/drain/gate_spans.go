package drain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
)

// GateSpansFile is the gate's per-check span file inside a run's record directory. It is
// written by `tools/fork-check.py --spans-out` and read by `recordGateJudgment`, which
// inserts the executed spans into the `gate_spans` table in the same transaction that
// records the verdict. `skipped_checks` stays in the file: a landed gate skips ~22 steps,
// and one not-run row per skip would make the table mostly silence.
const GateSpansFile = "gate.spans.json"
const GateFastVerdictFile = "gate.fast.verdict.json"

// GateSpanDDL is drain's third table: one row per executed gate span, keyed by the
// verdict it belongs to.
//
// It is a table rather than an event for the reason `drain_passes` is: a span is a
// measurement, not a transition, and registering a kind for it would claim one (the
// events table's own rule forbids measurement rows among transitions). The durations
// alone would not do either — two ten-second checks that ran in parallel and two that
// serialized on a lock read the same as `duration_ms`; the start and end on one axis are
// what tell them apart.
//
// The four identity columns are hashes so the chain tick → run → verdict → check is
// complete without a second naming scheme: `span_id` is hash(verdict_id || '/' ||
// check_id), `parent_span_id` is hash(verdict_id), and `trace_id` is
// hash(COALESCE(tick_id, run_id)). An authorized rerun uses the verdict event's
// gate_execution_id (verdict_id plus its transition epoch) in the span and parent
// hashes so a deterministic verdict id cannot overwrite the earlier observation.
const GateSpanDDL = `
CREATE TABLE IF NOT EXISTS gate_spans (
  span_id          TEXT PRIMARY KEY,
  trace_id         TEXT NOT NULL,
  parent_span_id   TEXT NOT NULL,
  verdict_id       TEXT NOT NULL,
  run_id           TEXT NOT NULL,
  tick_id          TEXT NOT NULL,
  check_id         TEXT NOT NULL,
  kind             TEXT NOT NULL,
  label            TEXT NOT NULL,
  status           TEXT NOT NULL,
  start_unix_nano  INTEGER NOT NULL,
  end_unix_nano    INTEGER NOT NULL,
  waited_ms_json   TEXT NOT NULL,
  attributes_json  TEXT NOT NULL
);
CREATE INDEX IF NOT EXISTS gate_spans_run ON gate_spans (run_id);
`

// The three states the spans file can be in when the verdict is recorded. A missing or
// unparseable file is not a gate failure: the verdict is the judgment, and the spans are
// the measurement beside it. The state is carried on the verdict event so a reader can
// tell "the gate wrote no spans" from "the spans were written and are empty".
const (
	GateSpansRead       = "read"
	GateSpansAbsent     = "absent"
	GateSpansUnreadable = "unreadable"
)

// gateSpan is one executed check's span as `gate.spans.json` writes it.
type gateSpan struct {
	CheckID        string `json:"check_id"`
	Kind           string `json:"kind"`
	Label          string `json:"label"`
	Status         string `json:"status"`
	StartNS        int64  `json:"start_ns"`
	EndNS          int64  `json:"end_ns"`
	WaitedMSJSON   string `json:"waited_ms_json"`
	AttributesJSON string `json:"attributes_json"`
}

// gateSpansFile is the file's whole shape. Only `plan_span` and `check_spans` become
// rows; `skipped_checks` and `summary` are kept for a reader of the file itself.
type gateSpansFile struct {
	PlanSpan      *gateSpan   `json:"plan_span"`
	CheckSpans    []gateSpan  `json:"check_spans"`
	SkippedChecks []gateSkip  `json:"skipped_checks"`
	Summary       gateSummary `json:"summary"`
}

// gateSkip is one step the run did not put a verdict on, in the gate's own words.
type gateSkip struct {
	CheckID string `json:"check_id"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Reason  string `json:"reason"`
	Planned bool   `json:"planned"`
	Owed    bool   `json:"owed"`
}

type gateSummary struct {
	Checks   int    `json:"checks"`
	Executed int    `json:"executed"`
	Skipped  int    `json:"skipped"`
	Outcome  string `json:"outcome"`
}

// gateSpanRow is one row the store holds, with the four identity hashes computed.
type gateSpanRow struct {
	SpanID         string
	TraceID        string
	ParentSpanID   string
	VerdictID      string
	RunID          string
	TickID         string
	CheckID        string
	Kind           string
	Label          string
	Status         string
	StartUnixNano  int64
	EndUnixNano    int64
	WaitedMSJSON   string
	AttributesJSON string
}

// gateSpanID answers the stable id of one span: hash(verdict_id || '/' || check_id). The
// whole chain is derived from the verdict and the check, so a replayed verdict reproduces
// the same rows and an insert is idempotent by `span_id`.
func gateSpanID(verdictID, checkID string) string {
	return hexDigest(verdictID + "/" + checkID)
}

// gateTraceID answers hash(COALESCE(tick_id, run_id)): a tick's gate spans share one
// trace, and a run whose tick is unknown is its own trace rather than an empty one.
func gateTraceID(passID, runID string) string {
	if passID != "" {
		return hexDigest(passID)
	}
	return hexDigest(runID)
}

// gateParentSpanID answers hash(verdict_id): every check span's parent is the verdict that
// judged the tree, which is what makes the chain tick → run → verdict → check complete.
func gateParentSpanID(verdictID string) string {
	return hexDigest(verdictID)
}

// readFailingSpans answers the checks a rolling run recorded as failed, and the paths each
// one wrote. The file is the gate's own `gate.spans.json`, which the rolling program writes
// at TILLR_GATE_SPANS_OUT: a rolling run has no verdict record, so its spans are the only
// account of what was red.
//
// A missing or unreadable file answers nothing. That is not the same as "nothing failed":
// the caller treats no named check as unknown, because a failure with no failing span is a
// run that could not say what was red rather than a run whose failure list is empty.
func readFailingSpans(path string) ([]string, map[string][]string) {
	return readSpansWithStatus(path, "FAIL")
}

func readSpansWithStatus(path, status string) ([]string, map[string][]string) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, nil
	}
	var file gateSpansFile
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, nil
	}
	checks := make([]string, 0, len(file.CheckSpans))
	watches := map[string][]string{}
	for _, span := range file.CheckSpans {
		// `FAIL` is the gate's own spelling for a check that ran and failed its tree; the
		// `ERR`, `STALE` and `not-run` statuses reach no verdict and exit the run 2, which
		// the caller reads as unknown before it ever looks for failing checks.
		if span.Status != status || span.CheckID == "" {
			continue
		}
		checks = append(checks, span.CheckID)
		var attributes struct {
			Writes []string `json:"writes"`
		}
		if err := json.Unmarshal([]byte(span.AttributesJSON), &attributes); err == nil && len(attributes.Writes) > 0 {
			watches[span.CheckID] = attributes.Writes
		}
	}
	return checks, watches
}

// RollingCoverage is the evidence retained with a rolling run, including skips.
// Ran excludes cached verdicts; absent or unreadable spans never claim zero work.
type RollingCoverage struct {
	SpansState    string
	Ran           int
	Cached        int
	SkippedChecks []gateSkip
}

func (coverage RollingCoverage) line() string {
	if coverage.SpansState != GateSpansRead {
		return "check counts unavailable (no readable spans)"
	}
	return fmt.Sprintf("%d ran, %d cached, %d skipped", coverage.Ran, coverage.Cached, len(coverage.SkippedChecks))
}

func readRollingCoverage(path string) RollingCoverage {
	coverage := RollingCoverage{SpansState: GateSpansAbsent}
	body, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			coverage.SpansState = GateSpansUnreadable
		}
		return coverage
	}
	var file gateSpansFile
	if json.Unmarshal(body, &file) != nil {
		coverage.SpansState = GateSpansUnreadable
		return coverage
	}
	coverage.SpansState = GateSpansRead
	coverage.SkippedChecks = file.SkippedChecks
	for _, span := range file.CheckSpans {
		var attributes struct {
			Cached bool `json:"cached"`
		}
		if span.AttributesJSON != "" && json.Unmarshal([]byte(span.AttributesJSON), &attributes) != nil {
			return RollingCoverage{SpansState: GateSpansUnreadable}
		}
		if attributes.Cached {
			coverage.Cached++
		} else {
			coverage.Ran++
		}
	}
	return coverage
}

func hexDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// readGateSpans reads one run's `gate.spans.json` and answers the rows the store keeps,
// together with the state the file was in.
//
// A missing file is `absent`, not an error: a gate that refuses before running a check
// writes no spans, and a caller that treated that as a failed judgment would turn the
// gate's own refusal into the loop's fault. An unparseable file is `unreadable` and is
// likewise not an error, for the same reason — the verdict is the judgment.
func readGateSpans(root, runID, verdictID, passID string) ([]gateSpanRow, string) {
	file, state := readRemainingGateSpans(filepath.Join(RunDir(root, runID), GateSpansFile))
	fast, fastState := readFastGateSpans(filepath.Join(RunDir(root, runID), GateFastVerdictFile))
	file.CheckSpans = append(file.CheckSpans, fast...)
	if state != GateSpansRead && fastState == GateSpansRead {
		state = GateSpansRead
	}
	if state == GateSpansAbsent && fastState == GateSpansUnreadable {
		state = GateSpansUnreadable
	}
	spans := make([]gateSpan, 0, len(file.CheckSpans)+1)
	if file.PlanSpan != nil {
		spans = append(spans, *file.PlanSpan)
	}
	spans = append(spans, file.CheckSpans...)
	traceID := gateTraceID(passID, runID)
	parent := gateParentSpanID(verdictID)
	rows := make([]gateSpanRow, 0, len(spans))
	seen := map[string]bool{}
	for _, span := range spans {
		checkID := span.CheckID
		if checkID == "" {
			// A span the gate did not key is not addressable, and a made-up id would
			// collide with a real check on the next run. It is named in the log rather
			// than inserted under a guess.
			fmt.Fprintf(os.Stderr, "drain: the gate spans for %s hold a span with no check id; it is not recorded\n", runID)
			continue
		}
		spanID := gateSpanID(verdictID, checkID)
		if seen[spanID] {
			continue
		}
		seen[spanID] = true
		kind := span.Kind
		if kind == "" {
			kind = "check"
		}
		status := span.Status
		if status == "" {
			status = "unknown"
		}
		waited := span.WaitedMSJSON
		if waited == "" {
			waited = "{}"
		}
		attributes := span.AttributesJSON
		if attributes == "" {
			attributes = "{}"
		}
		rows = append(rows, gateSpanRow{
			SpanID: spanID, TraceID: traceID, ParentSpanID: parent,
			VerdictID: verdictID, RunID: runID, TickID: passID,
			CheckID: checkID, Kind: kind, Label: span.Label, Status: status,
			StartUnixNano: span.StartNS, EndUnixNano: span.EndNS,
			WaitedMSJSON: waited, AttributesJSON: attributes,
		})
	}
	return rows, state
}

func readRemainingGateSpans(path string) (gateSpansFile, string) {
	var file gateSpansFile
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return file, GateSpansAbsent
	}
	if err != nil || json.Unmarshal(body, &file) != nil {
		return file, GateSpansUnreadable
	}
	return file, GateSpansRead
}

// Fast entries retain their own ids and measurements beside, never in place of,
// the remaining fork-check spans. The attributes preserve watchdog evidence.
func readFastGateSpans(path string) ([]gateSpan, string) {
	body, err := os.ReadFile(path)
	if errors.Is(err, os.ErrNotExist) {
		return nil, GateSpansAbsent
	}
	var file struct {
		Payload struct {
			Checks []json.RawMessage `json:"checks"`
		} `json:"payload"`
	}
	if err != nil || json.Unmarshal(body, &file) != nil {
		return nil, GateSpansUnreadable
	}
	spans := []gateSpan{}
	for _, raw := range file.Payload.Checks {
		var entry struct {
			CheckID string `json:"check_id"`
			Result  string `json:"result"`
			Start   int64  `json:"start_unix_nano"`
			End     int64  `json:"end_unix_nano"`
		}
		if json.Unmarshal(raw, &entry) != nil {
			return nil, GateSpansUnreadable
		}
		spans = append(spans, gateSpan{CheckID: entry.CheckID, Kind: "fast", Label: entry.CheckID, Status: entry.Result, StartNS: entry.Start, EndNS: entry.End, WaitedMSJSON: "{}", AttributesJSON: string(raw)})
	}
	return spans, GateSpansRead
}
