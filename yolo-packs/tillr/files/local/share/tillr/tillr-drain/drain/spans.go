package drain

import (
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
)

// StderrTailBytes bounds how much of a step's stderr a span keeps. A step is free to print a
// whole test run; the design asks for a tail, which is what makes a failing step diagnosable
// without a second copy of a log the run already has.
const StderrTailBytes = 4096

// StepSpan is one step invocation as the loop observed it: the design's step-span row,
// written as one JSON object per line to <root>/.tillr/loop/<run-id>/steps.jsonl.
//
// The span is the observation and never the judgment. ExitCode is nil when the step never
// started — a program that is not installed made no exit, and 0 would read as a clean pass it
// never had — and StartedAtMS/FinishedAtMS are read from the wall clock rather than from the
// loop's injected clock: the span exists to say how long a step held the tick, which an
// injected clock (fixed in every test) cannot answer.
type StepSpan struct {
	// Step is the loop's own name for the step: run, gate, land, verdict or item-state.
	Step string `json:"step"`
	// Argv is the program and its words, exactly as executed.
	Argv []string `json:"argv"`
	// Cwd is the directory the step ran in.
	Cwd string `json:"cwd"`
	// StartedAtMS and FinishedAtMS are the step's bounds, in Unix milliseconds.
	StartedAtMS  int64 `json:"started_at_ms"`
	FinishedAtMS int64 `json:"finished_at_ms"`
	// ExitCode is the step's exit status, or null when the program could not be started.
	ExitCode *int `json:"exit_code"`
	// StderrTail is the end of what the step wrote on stderr, at most StderrTailBytes with
	// a leading ellipsis when more was dropped.
	StderrTail string `json:"stderr_tail"`
}

// appendStepSpan adds one span to a run's step file. It appends because one run's steps are
// written by both writers the design names — the drain's own ProcessSteps, and the adapters
// `tillr-loop` starts for the gate, the land and the verdict — into one file, one line per
// invocation, in the order the steps finished. The run's directory is created if the adapter
// has not written a record there yet.
func appendStepSpan(root, runID string, span StepSpan) error {
	dir := RunDir(root, runID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Externalf("creating the run's record directory %s: %v", dir, err)
	}
	line, err := json.Marshal(span)
	if err != nil {
		return Internalf("encoding the step span for %s: %v", runID, err)
	}
	path := filepath.Join(dir, StepsFile)
	file, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o644)
	if err != nil {
		return Externalf("opening the run's step file %s: %v", path, err)
	}
	if _, err := file.Write(append(line, '\n')); err != nil {
		file.Close()
		return Externalf("writing the step span to %s: %v", path, err)
	}
	if err := file.Close(); err != nil {
		return Externalf("closing the run's step file %s: %v", path, err)
	}
	return nil
}

// cwdOf is the directory a step ran in, as a span records it: the command's own directory
// where it names one, and this process's where the step inherits it.
func cwdOf(cmd *exec.Cmd) string {
	if cmd.Dir != "" {
		return cmd.Dir
	}
	dir, err := os.Getwd()
	if err != nil {
		return "."
	}
	return dir
}

// exitCodeOf answers a finished command's exit code, and nil for one that never started. A
// program that is not installed, or a process the kernel refused, produced no exit status,
// and reporting 0 for it would be the missing-measurement-read-as-zero defect.
func exitCodeOf(cmd *exec.Cmd) *int {
	if cmd.ProcessState == nil {
		return nil
	}
	code := cmd.ProcessState.ExitCode()
	return &code
}

// stderrTail is a bounded sink for one step's stderr: it keeps the last StderrTailBytes and
// drops what came before, so a chatty step cannot grow the loop's memory. The error a step
// fails with quotes the same tail the span records, so the two cannot disagree about what the
// step said.
type stderrTail struct {
	buf []byte
	// dropped is whether anything was cut from the front, and it is what the ellipsis in
	// the tail says: a tail that silently lost its beginning reads as the whole message.
	dropped bool
}

func (t *stderrTail) Write(p []byte) (int, error) {
	t.buf = append(t.buf, p...)
	if len(t.buf) > StderrTailBytes {
		t.buf = append(t.buf[:0], t.buf[len(t.buf)-StderrTailBytes:]...)
		t.dropped = true
	}
	return len(p), nil
}

// String is the tail as a record holds it: trimmed at both ends, because a step's trailing
// newline is formatting and its leading blank line is not the start of what it said.
func (t *stderrTail) String() string {
	body := strings.TrimSpace(string(t.buf))
	if t.dropped {
		return "…" + body
	}
	return body
}
