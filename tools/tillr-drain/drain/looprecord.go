// Architecture and invariants: docs/reference/tillr-drain-successor.md
package drain

import (
	"io"
	"time"

	"github.com/mschulkind-oss/tillr-drain/internal/storelog"
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
	result.StopExplanation = ExplainStop(result, at)
	if build.named() {
		result.Build = &build
	}
	storelog.WriteLoopResultRecord(root, result.FirstGeneration, result.LastGeneration, at, result, report)
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
