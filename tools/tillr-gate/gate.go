package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// GateProgram is the gate a caller names for the run face to execute: one command, and
// the bound it may run under.
//
// There is one gate implementation, `tools/fork-check.py`, and this face does not own the
// checks: it runs that program once and maps its exit onto the frozen verdict contract.
// The program is named by the caller rather than hardcoded, so a test can substitute
// `/bin/true` and a fixture can name a scripted refusal.
type GateProgram struct {
	Command []string
	Timeout time.Duration
}

// The gate program's exits that are verdicts, as its own `verdict_of` documents them: 0
// VERIFIED, and 1 a verdict -- at least one check ran and failed. Its 2 is not a verdict
// (the run refused to judge, or a check reached no verdict), and 2 is named in
// `gateResult`'s default rather than here, because nothing about it is a verdict.
const (
	gateExitVerified = 0
	gateExitFailed   = 1
)

// gateResult maps the gate program's exit onto the verdict's result.
//
// 0 is `pass` and 1 is `fail`. 2 is `error`, because "the harness could not judge" is not
// "the code is bad", and collapsing the two is how a red gate stops being believed. Every
// other code is `error` for the same reason: a program that exited 3, a signal, or a
// timeout judged nothing.
func gateResult(exit int) string {
	switch exit {
	case gateExitVerified:
		return ResultPass
	case gateExitFailed:
		return ResultFail
	case ExitStalled:
		return ResultStalled
	default:
		// 2 is the gate's own "not a verdict" -- a refusal, or a check that reached no
		// verdict -- and it lands here with every other code that is not 0 or 1.
		return ResultError
	}
}

// RunGate runs the named gate program once in the worktree and returns the verdict record
// bound to the plan's tree.
//
// The plan is the binding, not the work list: its `plan_id`, `registry_digest`, `base_sha`
// and `tree_sha` are what the verdict must carry, and `land pass` refuses a mismatch on
// them. Its `checks` and `in_process` lists are not executed here -- the named program is
// the gate, and it runs its own plan itself, in-process checks included. Running the plan's
// commands as well would check the tree twice and could disagree with itself, which is the
// one thing this adapter exists to avoid.
//
// The returned verdict is written by the face before the exit code is answered, exactly as
// the plan path's is: a run that could not judge is still a record, and exit 4 with no
// record would leave the caller unable to tell a tree that was not judged from a face that
// never ran.
func RunGate(planRecord record.Envelope, worktree, logDir string, program GateProgram) (record.Envelope, error) {
	if len(program.Command) == 0 {
		return record.Envelope{}, usagef("no gate program was named")
	}
	plan, root, logs, err := prepareRun(planRecord, worktree, logDir)
	if err != nil {
		return record.Envelope{}, err
	}
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return record.Envelope{}, internalf("creating the log directory %q: %v", logs, err)
	}
	verdict := newVerdict(plan)
	outcome := runGateProgram(program, root, logs)
	verdict.Checks = append(verdict.Checks, outcome.check)
	verdict.Checks = append(verdict.Checks, outcome.checks...)
	verdict.Result = outcome.check.Result
	if len(outcome.checks) > 0 {
		verdict.Result = ResultStalled
	}
	envelope, err := encodeVerdict(verdict, planRecord)
	if err != nil {
		return record.Envelope{}, err
	}
	return envelope, outcome.failure
}

// gateRun is one execution of the gate program: the verdict's one check for it, and the
// exit this face answers with when the run did not pass.
type gateRun struct {
	check   VerdictCheck
	checks  []VerdictCheck
	failure error
}

// runGateProgram runs the program once and answers what the verdict records.
//
// The program leads its own process group and the bound kills the group, the same shape a
// planned check runs under: a bound that reached only the process would leave a gate's
// children building past its verdict. A bound that expires is `error`, never a `pass` -- a
// gate that did not finish judged nothing -- and it is never a hang either, because the
// group is killed rather than waited on.
func runGateProgram(program GateProgram, root, logDir string) gateRun {
	checkID := "gate:" + filepath.Base(program.Command[0])
	out := gateRun{check: VerdictCheck{CheckID: checkID, Result: ResultError}}
	base := logName(0, checkID)
	wholePath := filepath.Join(logDir, base)
	triagePath := filepath.Join(logDir, triageName(base))
	out.check.LogRef = triagePath
	triage := &gateTriage{}

	whole, err := os.Create(wholePath)
	if err != nil {
		out.check.LogRef = ""
		out.failure = internalf("creating the gate program's log %q: %v", wholePath, err)
		return out
	}
	defer whole.Close()

	ctx := context.Background()
	if program.Timeout > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, program.Timeout)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, program.Command[0], program.Command[1:]...)
	cmd.Dir = root
	fastPath := os.Getenv("TILLR_FAST_VERDICT_OUT")
	if fastPath == "" {
		fastPath = filepath.Join(logDir, "fast.verdict.json")
	}
	if err := os.Remove(fastPath); err != nil && !os.IsNotExist(err) {
		out.failure = externalf("clearing the preceding fast verdict: %v", err)
		return out
	}
	cmd.Env = append(os.Environ(), "TILLR_FAST_VERDICT_OUT="+fastPath)
	cmd.Stdout = io.MultiWriter(whole, triage)
	cmd.Stderr = cmd.Stdout
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

	started := time.Now()
	if err := cmd.Start(); err != nil {
		out.check.DurationMS = time.Since(started).Milliseconds()
		triage.flush()
		header := []string{gateHeader(program, root, "it could not be started", time.Since(started), 0, wholePath)}
		if writeTriage(&out, triagePath, triage, header) != nil {
			return out
		}
		out.failure = externalf("the gate program %q could not be started: %v; its triage is %s",
			strings.Join(program.Command, " "), err, triagePath)
		return out
	}
	waitErr := cmd.Wait()
	out.check.DurationMS = time.Since(started).Milliseconds()
	triage.flush()
	// A process that exited normally carries its code; one killed by a signal answers -1.
	// The bound is checked first, because our own SIGKILL is what ended it.
	exit := cmd.ProcessState.ExitCode()
	how := fmt.Sprintf("exit %d", exit)
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		out.check.Result = ResultError
		how = fmt.Sprintf("no exit: killed at the --gate-timeout bound of %s", program.Timeout)
		out.failure = externalf("the gate program outlived its timeout of %s and was killed; "+
			"nothing was judged, so the verdict is error and never a pass: %s", program.Timeout, triagePath)
	case exit < 0:
		out.check.Result = ResultError
		how = fmt.Sprintf("no exit: killed by a signal (%v)", waitErr)
		out.failure = externalf("the gate program was killed by a signal rather than answering (%v); "+
			"nothing was judged: %s", waitErr, triagePath)
	default:
		out.check.Result = gateResult(exit)
		switch out.check.Result {
		case ResultStalled:
			fast, err := readFastVerdict(fastPath)
			if err != nil {
				out.check.Result = ResultError
				out.failure = externalf("gate exited stalled without check observations: %v", err)
			} else {
				out.checks = fast.Checks
				out.failure = &exitError{code: ExitStalled, err: fmt.Errorf("gate stalled; observations in %s", fastPath)}
				// The wrapper itself did not stall; the named check did.
				out.check.Result = ResultUnavailable
			}
		case ResultFail:
			out.failure = &exitError{code: record.ExitNegative,
				err: fmt.Errorf("the gate program failed (exit %d); its triage is %s", exit, triagePath)}
		case ResultError:
			out.failure = externalf("the gate program exited %d, which is not a verdict: it refused "+
				"to judge, so the tree was neither passed nor failed: %s", exit, triagePath)
		}
	}
	header := []string{gateHeader(program, root, how, time.Since(started), triage.seen, wholePath)}
	if writeTriage(&out, triagePath, triage, header) != nil {
		return out
	}
	return out
}

// writeTriage writes the bounded triage the verdict's `log_ref` names, and answers the
// failure the run must carry when it could not be written.
//
// A verdict whose evidence is not there is not a verdict this face can hand on: the record
// would point at a file that does not exist, and the next reader would re-run the gate to
// find out what failed. The check's result is moved to `error` for that reason.
func writeTriage(out *gateRun, path string, triage *gateTriage, header []string) error {
	lines := append(header, triage.lines()...)
	body := strings.Join(lines, "\n") + "\n"
	if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
		out.check.LogRef = ""
		out.check.Result = ResultError
		return internalf("writing the gate program's triage %q: %v", path, err)
	}
	return nil
}

// gateHeader is the triage's first line: what ran, where, how it ended, and where the
// whole output is. It is one line so a reader who keeps only the head of the file still
// has the command and the exit.
func gateHeader(program GateProgram, root, how string, elapsed time.Duration, seen int, wholePath string) string {
	return fmt.Sprintf("tillr-gate: gate program %q in %s: %s after %.1fs; "+
		"this is a bounded triage of %d output line(s), the whole output is %s",
		strings.Join(program.Command, " "), root, how, elapsed.Seconds(), seen, wholePath)
}

// triageName is the bounded triage's file name, derived from the whole log's so the two
// sit together: a reader holding `log_ref` can find the full output by name.
func triageName(logFile string) string {
	return strings.TrimSuffix(logFile, ".log") + ".triage.log"
}

// What the verdict's `log_ref` points at is bounded: a gate run's output is a log, not a
// record field, and a verdict that carried a megabyte of another program's stdout would be
// a verdict nobody reads. The triage keeps the program's own status lines -- the ones that
// say which check failed -- and the last few lines when it printed none, and it cuts a
// line at triageLineLen characters (marked, so a cut line is not read as a whole one).
const (
	triageLines   = 24
	triageLineLen = 400
)

// gatePrefixes are the spellings `tools/fork-check.py` uses to name an outcome of its own:
// `fork-check:` opens every status line, and a failing check is diagnosed on a
// `triage <check>: <label>` line. `tillr-gate checks:` is the declaration checks' face,
// which `tools/gate/change-gate.py` runs first: its lines name a check that could not
// judge or ran past its budget. A program that uses none is still triaged -- by its
// last lines -- so this table narrows what is kept, never whether anything is.
var gatePrefixes = []string{"fork-check:", "triage ", "tillr-gate checks:"}

// gateTriage accumulates a bounded reading of the gate program's output as it is written,
// so the whole log is never read back to produce the record's evidence.
type gateTriage struct {
	pending []byte
	marked  []string
	tail    []string
	seen    int
	cut     bool // the line being assembled was longer than triageLineLen
}

func (triage *gateTriage) Write(chunk []byte) (int, error) {
	for _, b := range chunk {
		if b != '\n' {
			if len(triage.pending) < triageLineLen {
				triage.pending = append(triage.pending, b)
			} else {
				triage.cut = true
			}
			continue
		}
		triage.endLine()
	}
	return len(chunk), nil
}

// flush files a last line the program wrote without a newline.
func (triage *gateTriage) flush() {
	if len(triage.pending) > 0 {
		triage.endLine()
	}
}

// endLine files one finished line: a line the program marked as its own outcome is kept
// wherever it appears, and every line is a candidate for the tail.
func (triage *gateTriage) endLine() {
	line := strings.TrimRight(string(triage.pending), " \r\t")
	triage.pending = triage.pending[:0]
	if triage.cut {
		// A truncated line says so: a triage that ended mid-sentence without a mark
		// would read as the whole of what the gate said.
		line += "...[truncated]"
		triage.cut = false
	}
	if line == "" {
		return
	}
	triage.seen++
	if gateMarked(line) {
		triage.marked = keepBounded(triage.marked, line)
	}
	triage.tail = keepBounded(triage.tail, line)
}

// gateMarked reports whether a line is one the gate program printed as an outcome of its
// own, rather than as a check's output.
func gateMarked(line string) bool {
	trimmed := strings.TrimSpace(line)
	for _, prefix := range gatePrefixes {
		if strings.HasPrefix(trimmed, prefix) {
			return true
		}
	}
	return false
}

// keepBounded appends a line and forgets the oldest once the list is full, so what a
// triage holds is its last triageLines lines: the end of a run is where its verdict is.
func keepBounded(lines []string, line string) []string {
	lines = append(lines, line)
	if len(lines) > triageLines {
		lines = lines[len(lines)-triageLines:]
	}
	return lines
}

// lines is what the triage holds: the marked lines when the program printed any, and the
// tail of its output when it printed none.
func (triage *gateTriage) lines() []string {
	if len(triage.marked) > 0 {
		return triage.marked
	}
	return triage.tail
}
