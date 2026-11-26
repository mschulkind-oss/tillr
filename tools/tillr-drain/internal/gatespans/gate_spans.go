package gatespans

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"

	"github.com/mschulkind-oss/tillr-drain/internal/drainmodel"
	"github.com/mschulkind-oss/tillr/checkresult"
)

// GateSpansFileName is the gate's per-check span file inside a run's record directory. It is
// written by the judging program at the path TILLR_GATE_SPANS_OUT names and read by `recordGateJudgment`, which
// inserts the executed spans into the `gate_spans` table in the same transaction that
// records the verdict. `skipped_checks` stays in the file: a landed gate skips ~22 steps,
// and one not-run row per skip would make the table mostly silence.
const GateSpansFileName = "gate.spans.json"
const GateFastVerdictFile = "gate.fast.verdict.json"
const GateChecksFile = "gate.checks.jsonl"

// The three states the spans file can be in when the verdict is recorded. A missing or
// unparseable file is not a gate failure: the verdict is the judgment, and the spans are
// the measurement beside it. The state is carried on the verdict event so a reader can
// tell "the gate wrote no spans" from "the spans were written and are empty".
const (
	GateSpansRead       = "read"
	GateSpansAbsent     = "absent"
	GateSpansUnreadable = "unreadable"
)

// GateSpan is one executed check's span as `gate.spans.json` writes it.
type GateSpan struct {
	CheckID        string `json:"check_id"`
	Kind           string `json:"kind"`
	Label          string `json:"label"`
	Status         string `json:"status"`
	StartNS        int64  `json:"start_ns"`
	EndNS          int64  `json:"end_ns"`
	WaitedMSJSON   string `json:"waited_ms_json"`
	AttributesJSON string `json:"attributes_json"`
}

// GateSpansFile is the file's whole shape. Only `plan_span` and `check_spans` become
// rows; `skipped_checks` and `summary` are kept for a reader of the file itself.
type GateSpansFile struct {
	PlanSpan      *GateSpan         `json:"plan_span"`
	CheckSpans    []GateSpan        `json:"check_spans"`
	SkippedChecks []GateSkip        `json:"skipped_checks"`
	ReusedChecks  []string          `json:"reused_checks"`
	ReusedWallMS  map[string]*int64 `json:"reused_wall_ms"`
	NotRunChecks  []string          `json:"not_run_checks"`
	Summary       GateSummary       `json:"summary"`
}

// GateSkip is one step the run did not put a verdict on, in the gate's own words.
type GateSkip struct {
	CheckID string `json:"check_id"`
	Kind    string `json:"kind"`
	Label   string `json:"label"`
	Reason  string `json:"reason"`
	Planned bool   `json:"planned"`
	Owed    bool   `json:"owed"`
	// ExecutedCheck is optional timing for a command classified as a correctness skip.
	ExecutedCheck *struct {
		CheckID string `json:"check_id"`
		GateCheckMeasurement
	} `json:"executed_check,omitempty"`
}

type GateSummary struct {
	Checks   int    `json:"checks"`
	Executed int    `json:"executed"`
	Skipped  int    `json:"skipped"`
	Outcome  string `json:"outcome"`
}

// GateSpanRow is one row the store holds, with the four identity hashes computed.
type GateSpanRow struct {
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

// GateSpanID answers the stable id of one span: hash(verdict_id || '/' || check_id). The
// whole chain is derived from the verdict and the check, so a replayed verdict reproduces
// the same rows and an insert is idempotent by `span_id`.
func GateSpanID(verdictID, checkID string) string {
	return HexDigest(verdictID + "/" + checkID)
}

// GateTraceID answers hash(COALESCE(tick_id, run_id)): a tick's gate spans share one
// trace, and a run whose tick is unknown is its own trace rather than an empty one.
func GateTraceID(passID, runID string) string {
	if passID != "" {
		return HexDigest(passID)
	}
	return HexDigest(runID)
}

// GateParentSpanID answers hash(verdict_id): every check span's parent is the verdict that
// judged the tree, which is what makes the chain tick → run → verdict → check complete.
func GateParentSpanID(verdictID string) string {
	return HexDigest(verdictID)
}

// ReadFailingSpans answers the checks a rolling run recorded as failed, and the paths each
// one wrote. The file is the gate's own `gate.spans.json`, which the rolling program writes
// at TILLR_GATE_SPANS_OUT: a rolling run has no verdict record, so its spans are the only
// account of what was red.
//
// A missing or unreadable file answers nothing. That is not the same as "nothing failed":
// the caller treats no named check as unknown, because a failure with no failing span is a
// run that could not say what was red rather than a run whose failure list is empty.
func ReadFailingSpans(path string) ([]string, map[string][]string) {
	checks, watches, _ := ReadMatchingSpans(path, checkresult.Fail, "", nil)
	return checks, watches
}

func ReadSpansWithStatus(path, status string) ([]string, map[string][]string) {
	checks, watches, _ := ReadSpansWithWatches(path, status, nil)
	return checks, watches
}

func ReadSpansWithWatches(path, status string, declared map[string][]string) ([]string, map[string][]string, error) {
	want := checkresult.Classify(checkresult.Observation{Source: checkresult.LegacySpan, Word: status})
	return ReadMatchingSpans(path, want.Outcome, want.Detail, declared)
}

func ReadMatchingSpans(path string, wantOutcome checkresult.Outcome, wantDetail string, declared map[string][]string) ([]string, map[string][]string, error) {
	body, err := os.ReadFile(path)
	if err != nil {
		return nil, nil, err
	}
	var file GateSpansFile
	if err := json.Unmarshal(body, &file); err != nil {
		return nil, nil, err
	}
	checks := make([]string, 0, len(file.CheckSpans))
	watches := map[string][]string{}
	for _, span := range file.CheckSpans {
		if span.CheckID == "" {
			continue
		}
		// Legacy spans record `FAIL` for a check that ran and failed its tree; `ERR`,
		// `STALE` and `not-run` reach no verdict and classify as `unknown` with detail `error`.
		got := drainmodel.ClassifyCheck(checkresult.Observation{
			Check:  span.CheckID,
			Source: checkresult.LegacySpan,
			Word:   span.Status,
		})
		if got.Outcome != wantOutcome || (wantDetail != "" && got.Detail != wantDetail) {
			continue
		}
		checks = append(checks, span.CheckID)
		var attributes struct {
			Reads   []string `json:"reads"`
			Watches []string `json:"watches"`
			Writes  []string `json:"writes"`
		}
		if span.AttributesJSON != "" {
			if err := json.Unmarshal([]byte(span.AttributesJSON), &attributes); err != nil {
				return nil, nil, err
			}
		}
		paths := append(append(attributes.Reads, attributes.Watches...), attributes.Writes...)
		if declared != nil {
			var ok bool
			paths, ok = declared[span.CheckID]
			if !ok {
				paths, ok = declared[span.Kind+"\x00"+span.Label]
			}
			if !ok {
				return nil, nil, drainmodel.Externalf("legacy check %s has no declared watches", span.CheckID)
			}
		}
		watches[span.CheckID] = paths
	}
	return checks, watches, nil
}

// RollingCoverage is the evidence retained with a rolling run, including skips.
// Ran excludes cached verdicts; absent or unreadable spans never claim zero work.
type RollingCoverage struct {
	SpansState    string
	Ran           int
	Cached        int
	SkippedChecks []GateSkip
}

func (coverage RollingCoverage) Line() string {
	if coverage.SpansState != GateSpansRead {
		return "check counts unavailable (no readable spans)"
	}
	return fmt.Sprintf("%d ran, %d cached, %d skipped", coverage.Ran, coverage.Cached, len(coverage.SkippedChecks))
}

func ReadRollingCoverage(path string) RollingCoverage {
	coverage := RollingCoverage{SpansState: GateSpansAbsent}
	body, err := os.ReadFile(path)
	if err != nil {
		if !errors.Is(err, os.ErrNotExist) {
			coverage.SpansState = GateSpansUnreadable
		}
		return coverage
	}
	var file GateSpansFile
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

func HexDigest(text string) string {
	sum := sha256.Sum256([]byte(text))
	return hex.EncodeToString(sum[:])
}

// ReadGateSpans reads one run's `gate.spans.json` and answers the rows the store keeps,
// together with the state the file was in.
//
// A missing file is `absent`, not an error: a gate that refuses before running a check
// writes no spans, and a caller that treated that as a failed judgment would turn the
// gate's own refusal into the loop's fault. An unparseable file is `unreadable` and is
// likewise not an error, for the same reason — the verdict is the judgment.
func ReadGateSpans(root, runID, verdictID, passID string) ([]GateSpanRow, string) {
	file, state := ReadRemainingGateSpans(filepath.Join(drainmodel.RunDir(root, runID), GateSpansFileName))
	fast, fastState := ReadFastGateSpans(filepath.Join(drainmodel.RunDir(root, runID), GateFastVerdictFile))
	file.CheckSpans = append(file.CheckSpans, fast...)
	if state != GateSpansRead && fastState == GateSpansRead {
		state = GateSpansRead
	}
	if state == GateSpansAbsent && fastState == GateSpansUnreadable {
		state = GateSpansUnreadable
	}
	spans := make([]GateSpan, 0, len(file.CheckSpans)+1)
	if file.PlanSpan != nil {
		spans = append(spans, *file.PlanSpan)
	}
	spans = append(spans, file.CheckSpans...)
	traceID := GateTraceID(passID, runID)
	parent := GateParentSpanID(verdictID)
	rows := make([]GateSpanRow, 0, len(spans))
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
		spanID := GateSpanID(verdictID, checkID)
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
		rows = append(rows, GateSpanRow{
			SpanID: spanID, TraceID: traceID, ParentSpanID: parent,
			VerdictID: verdictID, RunID: runID, TickID: passID,
			CheckID: checkID, Kind: kind, Label: span.Label, Status: status,
			StartUnixNano: span.StartNS, EndUnixNano: span.EndNS,
			WaitedMSJSON: waited, AttributesJSON: attributes,
		})
	}
	return rows, state
}

func ReadRemainingGateSpans(path string) (GateSpansFile, string) {
	var file GateSpansFile
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
// the judging program's remaining spans. The attributes preserve watchdog evidence.
func ReadFastGateSpans(path string) ([]GateSpan, string) {
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
	spans := []GateSpan{}
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
		spans = append(spans, GateSpan{CheckID: entry.CheckID, Kind: "fast", Label: entry.CheckID, Status: entry.Result, StartNS: entry.Start, EndNS: entry.End, WaitedMSJSON: "{}", AttributesJSON: string(raw)})
	}
	return spans, GateSpansRead
}
