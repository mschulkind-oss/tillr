// Architecture and invariants: docs/reference/tillr-drain-successor.md
package drain

import (
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// recordResult writes the loop's outcome beside the runs' own records, so "why did the loop
// stop" is answered from the store's directory rather than only from the terminal of
// whoever supervised it.
//
// It is a report and not a stop condition: the loop has already stopped, and a directory it
// cannot write is printed rather than turned into a second, different failure — the stop the
// caller reads stays the one the loop actually made.
func (l *Loop) recordResult(result *LoopResult) {
	// The repeated-refusal report is finalized here, on the one path every stop goes
	// through, so the record and the live report agree on the window the loop actually
	// spent: the tick that found the pile drained is not a dispatch, and only the final
	// totals carry it.
	l.finalizeRepeatedRefusal(result)
	l.reportLandingMeasurements()
	l.recordLoopEnded(*result)
	RecordLoopResult(l.cfg.Root, l.cfg.Now().UTC(), l.build, *result, l.cfg.Report)
}

// RecordLoopResult writes one loop's outcome at LoopResultFile(root, at). It is the one
// writer of that record, so a loop that never reached its first tick and one that ran all
// night leave the same shape behind, and a reader that can answer "why did it stop" for the
// second can answer it for the first.
//
// The record carries the same stamp the startup line printed, so the answer survives the
// terminal that ran the loop. A stamp that names nothing is omitted, which is the reader's
// signal that this build cannot be named.
func RecordLoopResult(root string, at time.Time, build BuildStamp, result LoopResult, report io.Writer) {
	if report == nil {
		report = io.Discard
	}
	result.StopExplanation = ExplainStop(result, at)
	path := LoopResultFile(root, result.FirstGeneration, result.LastGeneration)
	if build.named() {
		result.Build = &build
	}
	payload, err := json.Marshal(result)
	if err != nil {
		reportRecordFailure(report, path, Internalf("encoding the loop's result: %v", err))
		return
	}
	env := record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          LoopResultKind,
		ID:            "loop-" + strings.TrimSuffix(strings.TrimPrefix(filepath.Base(path), "drain-"), ".json"),
		Producer:      record.Producer{System: record.System, Utility: Utility, Version: Version},
		CreatedAt:     at,
		Payload:       payload,
	}
	body, err := record.Encode(env)
	if err != nil {
		reportRecordFailure(report, path, Internalf("encoding the loop's result record: %v", err))
		return
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		reportRecordFailure(report, path, Externalf("creating %s: %v", filepath.Dir(path), err))
		return
	}
	if result.FirstGeneration == "" && result.LastGeneration == "" {
		if prior, err := os.ReadFile(path); err == nil {
			archive := filepath.Join(filepath.Dir(path), fmt.Sprintf("drain-0-0-%d.json", at.UnixNano()))
			if err := os.WriteFile(archive, prior, 0o644); err != nil {
				reportRecordFailure(report, archive, Externalf("archiving %s: %v", path, err))
			}
		} else if !os.IsNotExist(err) {
			reportRecordFailure(report, path, Externalf("reading %s: %v", path, err))
		}
	}
	if err := os.WriteFile(path, append(body, '\n'), 0o644); err != nil {
		reportRecordFailure(report, path, Externalf("writing %s: %v", path, err))
	}
}

// RecordStartupFault writes the terminal record for a loop that could not start: a policy
// that does not load, a program it cannot be given, a configuration it refuses. The record
// names the fault in Detail and the status the caller is about to answer in ExitCode, so a
// face that died before its first tick is readable after the terminal that printed it is
// gone — which is the only place such a death used to live.
func RecordStartupFault(root string, at time.Time, err error, report io.Writer) {
	if err == nil {
		return
	}
	RecordLoopResult(root, at, ReadBuildStamp(), LoopResult{
		Stop:     StopFault,
		StopStep: "startup",
		Detail:   err.Error(),
		ExitCode: ExitCode(err),
	}, report)
}

// reportRecordFailure says, in the loop's own report, that a record could not be written.
// A record that is missing and silent is the plausible-not-truthful timeline the observatory
// design forbids; the loop still stops with the reason it made, and the line names the file.
func reportRecordFailure(report io.Writer, path string, err error) {
	fmt.Fprintf(report, "loop: the loop's result was not recorded at %s: %v\n", path, err)
}
