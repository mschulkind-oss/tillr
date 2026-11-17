package main

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"syscall"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/record"
)

// VerdictKind is the record kind the run face emits. Like the plan it is a stdout record
// kind, never an event: a verdict is a function of a plan and a tree, so appending it
// would claim a state transition that does not exist.
const VerdictKind = "gate.verdict"

func init() {
	if err := event.Register(VerdictKind, "gate"); err != nil {
		panic(err)
	}
}

// Check outcomes also include unavailable: an optional declared machine ref is
// absent. It is neither a pass nor a failure, and does not block the run.
// `error` means the judge itself could not run, so the candidate was not judged.
const (
	ResultUnavailable = "unavailable"
	ResultPass        = "pass"
	ResultFail        = "fail"
	ResultError       = "error"
	// ResultOverBudget means a fast tree check must be judged by the heavy run.
	// It is neither a pass that can be remembered nor a candidate failure.
	ResultOverBudget = "over-budget"
)

// VerdictCheck is one check's outcome as the verdict carries it: what ran, how it came
// out, how long it took, and where its output is. `LogRef` is the path of a file that
// holds the check's own words -- a planned check's stdout and stderr, or the bounded
// triage the gate program's output was reduced to -- so a red verdict can be read rather
// than re-run.
type VerdictCheck struct {
	MissingNeeds []MissingNeed `json:"missing_needs,omitempty"`
	// Memory describes result reuse without changing the pass/fail/error contract.
	Memory      string `json:"memory,omitempty"`
	Fingerprint string `json:"fingerprint,omitempty"`
	PassTime    string `json:"pass_time,omitempty"`
	FailTime    string `json:"fail_time,omitempty"`
	UnboundPath string `json:"unbound_path,omitempty"`
	CheckID     string `json:"check_id"`
	Result      string `json:"result"`
	DurationMS  int64  `json:"duration_ms"`
	// OverBudgetMS is how far a fast change check ran past its `budget_ms`. The check
	// still answered, so its result is its verdict; the overrun is a defect against the
	// check's declared cost, not a statement about the candidate (design §6.5).
	OverBudgetMS int64  `json:"over_budget_ms,omitempty"`
	LogRef       string `json:"log_ref"`
}

// FastTierCapMS is the fast tier's admission cap: the sum of every fast `budget_ms` may
// not exceed it (`tools/gate/checks/gate-budget.py`, LIMIT_MS). It is also the default
// ceiling of a fast change check that runs past its own budget: a check still running
// when it alone has taken the whole tier's allowance is stopped, with no verdict.
const FastTierCapMS = 9000

// changeCeilingMS is where a fast change check is stopped. Its budget is not that point:
// a change check is never carried (nothing after the merge has its base), so stopping it
// at its budget would turn machine load into "could not judge" for the item. It runs on
// to its declared `timeout_ms`, or to the tier cap when it declares none.
func changeCeilingMS(check PlanCheck) int {
	if check.TimeoutMS > 0 {
		return check.TimeoutMS
	}
	return FastTierCapMS
}

// VerdictPayload is the `gate.verdict` payload: design §5.7's field list, verbatim.
//
// It binds the plan it ran (`plan_id`), the registry that produced the plan
// (`registry_digest`), and the tree it examined (`base_sha`, `tree_sha`). Those four are
// what `land pass` refuses a mismatch on, so a verdict that dropped one would be
// unusable by the face that consumes it.
type VerdictPayload struct {
	VerdictID      string         `json:"verdict_id"`
	PlanID         string         `json:"plan_id"`
	RegistryDigest string         `json:"registry_digest"`
	BaseSHA        string         `json:"base_sha"`
	TreeSHA        string         `json:"tree_sha"`
	Result         string         `json:"result"`
	Checks         []VerdictCheck `json:"checks"`
}

// Run executes a plan in a worktree and returns the verdict record.
//
// The verdict is returned even when the error is non-nil: a check that could not be run
// is an outcome to record (exit 4), not a silence. The error is the exit the face answers
// with -- nil for a completed verdict, `externalf` for a check that could not be run, and
// a negative exit for a completed `fail`.
//
// Checks run serially in the plan's order. The plan's `writes` sets are what let a runner
// overlap checks that cannot collide, and this face does not overlap any: a serial run is
// the same verdict, later, and the order is the one the plan already fixed.
//
// A plan that owes an in-process check is refused before anything runs. The gate's own
// code checks have no command to execute, so a verdict from this face would omit them and
// assert a transition that did not happen; `error` is the only honest answer until a
// parity lane executes them (the next-steps ruling's A-INPROCESS).
//
// This is the plan-driven path: one command per planned check. Where the checks are the
// one gate implementation's rather than the plan's commands, the face runs the named
// program instead -- see `RunGate`.
func Run(planRecord record.Envelope, worktree, logDir string) (record.Envelope, error) {
	plan, root, logs, err := prepareRun(planRecord, worktree, logDir)
	if err != nil {
		return record.Envelope{}, err
	}
	verdict := newVerdict(plan)

	if len(plan.InProcess) > 0 {
		// Nothing runs: the plan cannot be honoured, and a partial run would cost the
		// caller time for a verdict that is already `error`. Every planned check is
		// recorded as `error` -- it could not be run -- so the verdict names what it
		// owes rather than reporting an empty plan.
		for _, check := range plan.Checks {
			verdict.Checks = append(verdict.Checks, VerdictCheck{CheckID: check.CheckID, Result: ResultError})
		}
		verdict.Result = ResultError
		envelope, err := encodeVerdict(verdict, planRecord)
		if err != nil {
			return record.Envelope{}, err
		}
		return envelope, externalf("the plan owes %d in-process check(s) this face cannot run (%s): "+
			"a verdict that omitted them would assert a transition that did not happen",
			len(plan.InProcess), inProcessNames(plan.InProcess))
	}

	// The log directory is made only once something will write to it: a plan this face
	// refuses leaves no directory behind.
	if err := os.MkdirAll(logs, 0o755); err != nil {
		return record.Envelope{}, internalf("creating the log directory %q: %v", logs, err)
	}
	memory, err := prepareTreeMemory(plan, root)
	if err != nil {
		return record.Envelope{}, externalf("preparing tree result memory: %v", err)
	}
	var failure error
	for index, check := range plan.Checks {
		outcome, memoryErr := memory.run(check, root, logs, index)
		if memoryErr != nil {
			outcome.Result = ResultError
			if failure == nil {
				failure = externalf("tree check %q result memory: %v", check.CheckID, memoryErr)
			}
		}
		verdict.Checks = append(verdict.Checks, outcome)
		switch outcome.Result {
		case ResultError:
			verdict.Result = ResultError
			if failure == nil || exitCode(failure) != record.ExitExternal {
				failure = externalf("the check %q could not be run; its log is %s", check.CheckID, outcome.LogRef)
			}
		case ResultFail:
			if verdict.Result == ResultPass {
				verdict.Result = ResultFail
			}
			if failure == nil {
				failure = &exitError{code: record.ExitNegative,
					err: fmt.Errorf("the check %q failed; its log is %s", check.CheckID, outcome.LogRef)}
			}
		}
	}
	envelope, err := encodeVerdict(verdict, planRecord)
	if err != nil {
		return record.Envelope{}, err
	}
	return envelope, failure
}

// prepareRun is the validation both run paths share: the plan's binding fields, the
// worktree the checks run in, and the log directory, all resolved before anything runs.
// A path that refused after a check had run would have spent the caller's time for a
// verdict it could not write.
func prepareRun(planRecord record.Envelope, worktree, logDir string) (PlanPayload, string, string, error) {
	plan, err := planPayload(planRecord)
	if err != nil {
		return PlanPayload{}, "", "", err
	}
	if plan.PlanID == "" {
		return PlanPayload{}, "", "", usagef("the plan names no plan_id")
	}
	if plan.BaseSHA == "" || plan.TreeSHA == "" {
		return PlanPayload{}, "", "", usagef("the plan names no base_sha or no tree_sha: a verdict is bound to the tree it examined")
	}
	info, err := os.Stat(worktree)
	if err != nil || !info.IsDir() {
		return PlanPayload{}, "", "", usagef("the worktree %q is not a directory", worktree)
	}
	root, err := filepath.Abs(worktree)
	if err != nil {
		return PlanPayload{}, "", "", internalf("resolving the worktree %q: %v", worktree, err)
	}
	logs, err := filepath.Abs(logDir)
	if err != nil {
		return PlanPayload{}, "", "", internalf("resolving the log directory %q: %v", logDir, err)
	}
	return plan, root, logs, nil
}

// newVerdict is the verdict a run starts from: the plan's binding fields, a result a run
// then moves off `pass`, and room for one check per planned step.
func newVerdict(plan PlanPayload) VerdictPayload {
	return VerdictPayload{
		VerdictID:      verdictID(plan.PlanID, plan.TreeSHA),
		PlanID:         plan.PlanID,
		RegistryDigest: plan.RegistryDigest,
		BaseSHA:        plan.BaseSHA,
		TreeSHA:        plan.TreeSHA,
		Result:         ResultPass,
		Checks:         make([]VerdictCheck, 0, len(plan.Checks)),
	}
}

// runCheck runs one planned check in the worktree and answers its outcome.
//
// A check that cannot be started -- no command, a `needs` path that is not there, a
// binary that is not on the path -- is `error`, never `fail`: the candidate was not
// judged, and reading that as a red would isolate a candidate for a missing tool.
func runCheck(check PlanCheck, root, logDir string, index int) VerdictCheck {
	outcome := VerdictCheck{CheckID: check.CheckID, Result: ResultError}
	if len(check.Command) == 0 {
		return outcome
	}
	for _, need := range check.Needs {
		if strings.HasPrefix(need, "ref:") {
			continue // resolved before execution and result lookup
		}
		if _, err := os.Stat(need); err != nil {
			return outcome
		}
	}
	logPath := filepath.Join(logDir, logName(index, check.CheckID))
	logFile, err := os.Create(logPath)
	if err != nil {
		return outcome
	}
	defer logFile.Close()
	outcome.LogRef = logPath

	ctx := context.Background()
	deadlineMS := check.TimeoutMS
	if check.Tier == "fast" {
		if check.BudgetMS <= 0 {
			fmt.Fprintln(logFile, "fast check requires a positive budget_ms; no verdict")
			return outcome
		}
		deadlineMS = check.BudgetMS
		if check.Scope == "change" {
			deadlineMS = changeCeilingMS(check)
		}
	}
	if deadlineMS > 0 {
		var cancel context.CancelFunc
		ctx, cancel = context.WithTimeout(ctx, time.Duration(deadlineMS)*time.Millisecond)
		defer cancel()
	}
	cmd := exec.CommandContext(ctx, check.Command[0], check.Command[1:]...)
	cmd.Dir = root
	cmd.Env = checkEnv(check)
	cmd.Stdout = logFile
	cmd.Stderr = logFile
	// The child leads its own process group and the bound kills the group: a bound that
	// reached only the process would leave a check's children running past its verdict.
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	cmd.Cancel = func() error { return syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL) }

	started := time.Now()
	if err := cmd.Start(); err != nil {
		outcome.DurationMS = time.Since(started).Milliseconds()
		return outcome
	}
	waitErr := cmd.Wait()
	outcome.DurationMS = time.Since(started).Milliseconds()
	switch {
	case ctx.Err() == context.DeadlineExceeded:
		if check.Tier == "fast" && check.Scope == "tree" {
			fmt.Fprintf(logFile, "check %q exceeded budget_ms %d; stopped and recorded over-budget, "+
				"its judgment deferred to the heavy tier\n", check.CheckID, check.BudgetMS)
			outcome.Result = ResultOverBudget
		} else if check.Tier == "fast" {
			// Past its budget and then past its ceiling: the check never answered.
			fmt.Fprintf(logFile, "check %q exceeded budget_ms %d and was stopped at its ceiling of %d ms; "+
				"no verdict\n", check.CheckID, check.BudgetMS, deadlineMS)
			outcome.Result = ResultError
		} else if check.Tier == "heavy" {
			// A heavy declaration's timeout is a limit on the judge, not a
			// completed negative judgment about the candidate.
			fmt.Fprintf(logFile, "check %q exceeded timeout_ms %d; no verdict\n", check.CheckID, check.TimeoutMS)
			outcome.Result = ResultError
		} else {
			// Generated registries retain their existing timeout behavior.
			outcome.Result = ResultFail
		}
	case waitErr != nil:
		var exitErr *exec.ExitError
		if !errors.As(waitErr, &exitErr) {
			return outcome
		}
		if (check.Scope == "change" || check.Scope == "tree") && exitErr.ExitCode() == record.ExitExternal {
			outcome.Result = ResultError
		} else {
			outcome.Result = ResultFail
		}
	default:
		outcome.Result = ResultPass
	}
	if check.Tier == "fast" && check.Scope == "change" && outcome.Result != ResultError &&
		outcome.DurationMS > int64(check.BudgetMS) {
		outcome.OverBudgetMS = outcome.DurationMS - int64(check.BudgetMS)
		fmt.Fprintf(logFile, "check %q ran %d ms, over its budget_ms %d by %d ms; its verdict (%s) stands, "+
			"and the overrun is a defect against the check's declared cost\n",
			check.CheckID, outcome.DurationMS, check.BudgetMS, outcome.OverBudgetMS, outcome.Result)
	}
	return outcome
}

// budgetNotes are the lines a face prints for every fast check that ran past its budget,
// so a slow check is visible on the gate's own output and not only inside its log: a tree
// check that was stopped and carried, and a change check that ran long and still answered.
func budgetNotes(verdict VerdictPayload) []string {
	var notes []string
	for _, check := range verdict.Checks {
		switch {
		case check.Result == ResultOverBudget:
			notes = append(notes, fmt.Sprintf("%s: over-budget after %d ms; stopped, judgment deferred to the heavy tier",
				check.CheckID, check.DurationMS))
		case check.OverBudgetMS > 0:
			notes = append(notes, fmt.Sprintf("%s: ran %d ms, %d ms over its budget_ms; its verdict (%s) stands",
				check.CheckID, check.DurationMS, check.OverBudgetMS, check.Result))
		}
	}
	return notes
}

// checkEnv is the environment a check runs with: this process's, minus the names the plan
// drops, plus the names it sets. A check that inherited a variable the plan drops would
// run under a different environment than the gate it replaces, and its verdict would
// attest to a run that did not happen.
func checkEnv(check PlanCheck) []string {
	dropped := make(map[string]bool, len(check.Drop))
	for _, name := range check.Drop {
		dropped[name] = true
	}
	env := make([]string, 0, len(os.Environ())+len(check.Env))
	for _, entry := range os.Environ() {
		name, _, _ := strings.Cut(entry, "=")
		if dropped[name] {
			continue
		}
		if _, overridden := check.Env[name]; overridden {
			continue
		}
		env = append(env, entry)
	}
	names := make([]string, 0, len(check.Env))
	for name := range check.Env {
		names = append(names, name)
	}
	sort.Strings(names)
	for _, name := range names {
		env = append(env, name+"="+check.Env[name])
	}
	return env
}

// logName is one check's log file name: its plan index and its id, so the logs sort in
// plan order and a check id that carries a slash cannot escape the log directory.
func logName(index int, checkID string) string {
	safe := strings.Map(func(r rune) rune {
		switch {
		case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
			return r
		case r == '.', r == '-', r == '_':
			return r
		default:
			return '_'
		}
	}, checkID)
	return fmt.Sprintf("%02d-%s.log", index, safe)
}

// verdictID is the verdict's identity: a hash of the plan it ran and the tree it
// examined, so two runs of one plan against one tree are one verdict and a replayed
// verdict is recognisable. It is derived rather than counted, which is what lets a
// caller name the log directory before the run starts.
func verdictID(planID, treeSHA string) string {
	sum := sha256.Sum256([]byte(planID + "\x00" + treeSHA))
	return hex.EncodeToString(sum[:])
}

// inProcessNames names the owed in-process checks for the diagnostic.
func inProcessNames(owed []PlanInProcess) string {
	names := make([]string, 0, len(owed))
	for _, check := range owed {
		names = append(names, check.CheckID)
	}
	return strings.Join(names, ", ")
}

// decodePlan reads a `gate.plan` record and returns it, refusing a record of another
// kind before its payload is read.
func decodePlan(raw []byte) (record.Envelope, error) {
	envelope, err := record.Decode(raw)
	if err != nil {
		return record.Envelope{}, usagef("the input is not a tillr record: %v", err)
	}
	if envelope.Kind != PlanKind {
		return record.Envelope{}, usagef("the input is a %q record, not a gate.plan", envelope.Kind)
	}
	if _, err := planPayload(envelope); err != nil {
		return record.Envelope{}, err
	}
	return envelope, nil
}

// encodeVerdictRecord writes the verdict record as the bytes the face emits.
func encodeVerdictRecord(envelope record.Envelope) ([]byte, error) {
	raw, err := record.Encode(envelope)
	if err != nil {
		return nil, internalf("encoding the verdict record: %v", err)
	}
	return append(raw, '\n'), nil
}

// planPayload decodes a plan record's payload.
func planPayload(envelope record.Envelope) (PlanPayload, error) {
	var plan PlanPayload
	if err := json.Unmarshal(envelope.Payload, &plan); err != nil {
		return PlanPayload{}, usagef("decoding the gate.plan payload: %v", err)
	}
	return plan, nil
}

// encodeVerdict writes the verdict record as the bytes the face emits. The plan's subject
// and correlation id are carried through, so a verdict stays attached to the candidate
// and the pipeline run the plan belonged to.
func encodeVerdict(verdict VerdictPayload, plan record.Envelope) (record.Envelope, error) {
	raw, err := json.Marshal(verdict)
	if err != nil {
		return record.Envelope{}, internalf("encoding the verdict payload: %v", err)
	}
	envelope := record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          VerdictKind,
		ID:            verdict.VerdictID,
		Producer:      record.Producer{System: record.System, Utility: "gate", Version: Version},
		CreatedAt:     time.Now().UTC(),
		Subject:       plan.Subject,
		CorrelationID: plan.CorrelationID,
		Payload:       raw,
	}
	return envelope, nil
}
