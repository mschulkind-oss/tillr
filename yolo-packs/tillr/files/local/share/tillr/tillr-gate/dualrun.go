package main

// dualrun.go is the dual-run harness: it drives this gate and the Python gate
// (`tools/fork-check.py`) over the same real candidates and records what each one planned,
// what each one judged, and the exit code each one answered with.
//
// It is a harness, not a face of the gate's contract: it emits no record kind, writes no
// store, and decides nothing. Its product is a report a reader compares, and a disagreement
// in that report is a finding about the two gates rather than a failure of the harness --
// so nothing here smooths one away.
//
// Both gates are driven as the binaries a caller would run, never as library calls: the
// exit code is part of the evidence, and a face's exit is fixed where its error is made.
// The Python gate is a subprocess by necessity (it is another language's program); this
// gate is a subprocess of itself for the same reason, so the harness cannot accidentally
// attest a plan the `plan` face would not have produced.

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/mschulkind-oss/tillr/record"
)

// The two gates this harness compares, as the report names them.
const (
	GateGo     = "tillr-gate"
	GatePython = "fork-check"
)

// The verdict words a comparison uses. They are the three results a verdict can carry,
// plus `none` for a gate that produced no verdict at all -- which is not `error`: `error`
// is a gate that judged and could not, `none` is a gate that never got that far.
const (
	VerdictPass  = ResultPass
	VerdictFail  = ResultFail
	VerdictError = ResultError
	VerdictNone  = "none"
)

// The disagreement kinds. Each names one way two gates can differ, so a reader can count
// them without reading prose.
const (
	// DisagreeOnlyInGo: the Go plan names a check kind the Python plan does not.
	DisagreeOnlyInGo = "plan/only-in-go"
	// DisagreeOnlyInPython: the Python plan names a check kind the Go plan does not.
	DisagreeOnlyInPython = "plan/only-in-python"
	// DisagreePlanExit: a gate's plan face did not answer 0, so its plan is not a plan.
	DisagreePlanExit = "plan/exit"
	// DisagreeVerdict: the two gates reached different verdicts for one candidate.
	DisagreeVerdict = "verdict"
	// DisagreeInProcess: the Python gate runs a check as its own code, and the Go plan
	// carries it as in-process, which `run` refuses to execute (the next-steps ruling's
	// A-INPROCESS). It is the disagreement that makes every Go verdict on a real
	// candidate `error`.
	DisagreeInProcess = "in-process"
)

// Candidate is one change both gates are asked about: a base commit and the head the
// checkout is at. The head is the checkout's own HEAD because the Python gate plans
// `base..HEAD` of the tree it runs in -- it has no flag for an arbitrary head, which is
// itself a finding the evidence document records.
type Candidate struct {
	Name         string   `json:"name"`
	Base         string   `json:"base"`
	Head         string   `json:"head"`
	ChangedPaths []string `json:"changed_paths"`
}

// GateObservation is one gate's answer for one candidate: the plan it produced, the
// verdict it reached, and the exit code each of those faces returned.
type GateObservation struct {
	Gate string `json:"gate"`
	// PlanOrder is the plan in the order the gate produced it, as check ids (Go) or
	// kinds (Python, whose plan line names kinds and not steps).
	PlanOrder []string `json:"plan_order"`
	// PlanKinds is the sorted, de-duplicated kind set: the comparison key, because a
	// kind is what both gates select by and several steps can share one.
	PlanKinds []string `json:"plan_kinds"`
	PlanExit  int      `json:"plan_exit"`
	Verdict   string   `json:"verdict"`
	// VerdictExit is the exit code of the face that judged, not of the plan.
	VerdictExit int `json:"verdict_exit"`
	// Note is the one line a reader needs to see why this gate answered as it did.
	Note string `json:"note,omitempty"`
}

// Disagreement is one difference between the two gates, named so it can be counted.
type Disagreement struct {
	Kind   string `json:"kind"`
	Check  string `json:"check,omitempty"`
	Go     string `json:"go"`
	Python string `json:"python"`
}

// Comparison is one candidate's dual run.
type Comparison struct {
	Candidate Candidate `json:"candidate"`
	// InProcess names the check kinds the Go plan carries as in-process: checks the
	// Python gate runs as its own code and this gate's `run` face refuses to execute.
	InProcess     []string        `json:"in_process"`
	Go            GateObservation `json:"go"`
	Python        GateObservation `json:"python"`
	Agreement     string          `json:"agreement"`
	Disagreements []Disagreement  `json:"disagreements"`
}

// Report is the harness's whole product: the candidates, both gates' answers, and the
// classification of each candidate as `agree` or `disagree`.
type Report struct {
	Root           string       `json:"root"`
	Registry       string       `json:"registry"`
	RegistryDigest string       `json:"registry_digest"`
	Python         string       `json:"python"`
	Candidates     []Comparison `json:"candidates"`
}

// DualRunConfig is what the harness needs to run: the checkout both gates plan against,
// the two programs, the registry, and the candidates.
type DualRunConfig struct {
	Root      string
	Self      string
	Python    string
	PythonExe string
	Registry  string
	// Work is where the change, plan and verdict files are written. It is the caller's
	// scratch directory, never a path inside the checkout: a harness that wrote into the
	// tree would change the change it is measuring.
	Work       string
	Candidates []Candidate
	Timeout    time.Duration
}

// DualRun runs both gates over every candidate and returns the report.
//
// A candidate whose plan either gate could not produce is still reported: the failure is
// the observation, and dropping it would make the harness's coverage look wider than it is.
func DualRun(cfg DualRunConfig) (Report, error) {
	if cfg.Root == "" || cfg.Self == "" || cfg.Python == "" || cfg.Registry == "" || cfg.Work == "" {
		return Report{}, usagef("dualrun needs a root, a self binary, a python gate, a registry and a work directory")
	}
	if cfg.PythonExe == "" {
		cfg.PythonExe = "python3"
	}
	if cfg.Timeout == 0 {
		cfg.Timeout = 30 * time.Minute
	}
	registry, err := LoadRegistry(cfg.Registry)
	if err != nil {
		return Report{}, incompatiblef("%v", err)
	}
	if err := os.MkdirAll(cfg.Work, 0o755); err != nil {
		return Report{}, internalf("creating the work directory %q: %v", cfg.Work, err)
	}
	head, err := gitOut(cfg.Root, "rev-parse", "HEAD")
	if err != nil {
		return Report{}, usagef("resolving the checkout's HEAD: %v", err)
	}
	report := Report{
		Root:           cfg.Root,
		Registry:       cfg.Registry,
		RegistryDigest: registry.Digest,
		Python:         cfg.Python,
		Candidates:     make([]Comparison, 0, len(cfg.Candidates)),
	}
	for _, candidate := range cfg.Candidates {
		candidate.Head = head
		comparison, err := dualRunOne(cfg, candidate)
		if err != nil {
			return Report{}, err
		}
		report.Candidates = append(report.Candidates, comparison)
	}
	return report, nil
}

// dualRunOne runs both gates over one candidate and classifies the result.
func dualRunOne(cfg DualRunConfig, candidate Candidate) (Comparison, error) {
	base, err := gitOut(cfg.Root, "rev-parse", candidate.Base)
	if err != nil {
		return Comparison{}, usagef("resolving candidate %q's base %q: %v", candidate.Name, candidate.Base, err)
	}
	candidate.Base = base
	paths, err := gitOut(cfg.Root, "diff", "--name-only", base+".."+candidate.Head)
	if err != nil {
		return Comparison{}, usagef("diffing candidate %q: %v", candidate.Name, err)
	}
	candidate.ChangedPaths = splitLines(paths)
	dir := filepath.Join(cfg.Work, candidate.Name)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return Comparison{}, internalf("creating %q: %v", dir, err)
	}
	changePath := filepath.Join(dir, "change.json")
	if err := writeChange(cfg.Root, changePath, candidate); err != nil {
		return Comparison{}, err
	}
	comparison := Comparison{Candidate: candidate}
	comparison.Go, comparison.InProcess, err = observeGo(cfg, dir, changePath)
	if err != nil {
		return Comparison{}, err
	}
	comparison.Python, err = observePython(cfg, candidate)
	if err != nil {
		return Comparison{}, err
	}
	comparison.Disagreements = compare(comparison)
	comparison.Agreement = "agree"
	if len(comparison.Disagreements) > 0 {
		comparison.Agreement = "disagree"
	}
	return comparison, nil
}

// writeChange writes the `gate.change` record the Go plan face reads. The tree sha is the
// candidate head's tree, so the plan is bound to the tree the Python gate also examined.
func writeChange(root, path string, candidate Candidate) error {
	tree, err := gitOut(root, "rev-parse", candidate.Head+"^{tree}")
	if err != nil {
		return internalf("resolving the tree of %s: %v", candidate.Head, err)
	}
	payload, err := json.Marshal(Change{
		BaseSHA:      candidate.Base,
		TreeSHA:      tree,
		ChangedPaths: candidate.ChangedPaths,
		Subject:      candidate.Name,
	})
	if err != nil {
		return internalf("encoding the change payload: %v", err)
	}
	envelope := record.Envelope{
		RecordVersion: record.SupportedVersion,
		Kind:          "gate.change",
		ID:            candidate.Name,
		Producer:      record.Producer{System: record.System, Utility: "gate", Version: Version},
		Payload:       payload,
	}
	raw, err := record.Encode(envelope)
	if err != nil {
		return internalf("encoding the change record: %v", err)
	}
	if err := os.WriteFile(path, append(raw, '\n'), 0o644); err != nil {
		return internalf("writing %s: %v", path, err)
	}
	return nil
}

// observeGo drives this gate's own two faces and reads what they answered.
func observeGo(cfg DualRunConfig, dir, changePath string) (GateObservation, []string, error) {
	observation := GateObservation{Gate: GateGo, Verdict: VerdictNone}
	planPath := filepath.Join(dir, "plan.json")
	verdictPath := filepath.Join(dir, "verdict.json")
	code, out, err := execGate(cfg, "plan", "--registry", cfg.Registry, "--input", changePath,
		"--root", cfg.Root, "--output", planPath)
	if err != nil {
		return observation, nil, err
	}
	observation.PlanExit = code
	if code != 0 {
		observation.Note = firstLine(out)
		return observation, nil, nil
	}
	raw, err := os.ReadFile(planPath)
	if err != nil {
		return observation, nil, internalf("reading the plan %s: %v", planPath, err)
	}
	plan, err := decodePlan(raw)
	if err != nil {
		return observation, nil, err
	}
	payload, err := planPayload(plan)
	if err != nil {
		return observation, nil, err
	}
	for _, check := range payload.Checks {
		observation.PlanOrder = append(observation.PlanOrder, check.CheckID)
	}
	observation.PlanKinds = kindsOf(payload.Checks)
	inProcess := make([]string, 0, len(payload.InProcess))
	for _, check := range payload.InProcess {
		inProcess = append(inProcess, check.Kind)
	}
	sort.Strings(inProcess)
	code, out, err = execGate(cfg, "run", "--input", planPath, "--worktree", cfg.Root,
		"--output", verdictPath, "--logs", filepath.Join(dir, "logs"))
	if err != nil {
		return observation, nil, err
	}
	observation.VerdictExit = code
	observation.Verdict = verdictResult(verdictPath)
	if observation.Verdict == VerdictNone {
		observation.Note = firstLine(out)
	}
	return observation, inProcess, nil
}

// observePython drives the Python gate's plan and verdict faces and reads what they
// answered.
//
// `--explain` is the plan face: it prints the selection as one JSON line and runs no
// check. `--since BASE` is what makes both faces plan the candidate's own range instead of
// the accumulated drift, and it is also what keeps the plan from fetching every fork's
// upstream -- a harness that fetched would be measuring the network as well as the gates.
// `--no-wait` keeps a run from queueing behind another lane's gate: a refusal is an
// observation, a wait is a stall.
func observePython(cfg DualRunConfig, candidate Candidate) (GateObservation, error) {
	observation := GateObservation{Gate: GatePython, Verdict: VerdictNone}
	code, out, err := execPython(cfg, "--explain", "--since", candidate.Base, "--no-wait")
	if err != nil {
		return observation, err
	}
	observation.PlanExit = code
	kinds, err := selectionKinds(out)
	if err != nil {
		observation.Note = firstLine(out)
		return observation, nil
	}
	observation.PlanKinds = kinds
	observation.PlanOrder = kinds
	code, out, err = execPython(cfg, "--since", candidate.Base, "--no-wait")
	if err != nil {
		return observation, err
	}
	observation.VerdictExit = code
	observation.Verdict = pythonVerdict(code)
	if ran, err := planLineKinds(out); err == nil && len(ran) > 0 {
		// The run's own plan line is what actually ran, which is the stronger claim: a
		// plan face and a run face that disagreed with each other would be a finding
		// about the Python gate, and the report keeps the run's answer.
		observation.PlanKinds = ran
	}
	if observation.Verdict == VerdictError {
		observation.Note = firstLine(out)
	}
	return observation, nil
}

// compare classifies one candidate's two observations.
func compare(comparison Comparison) []Disagreement {
	disagreements := make([]Disagreement, 0, 4)
	if comparison.Go.PlanExit != 0 || comparison.Python.PlanExit != 0 {
		disagreements = append(disagreements, Disagreement{
			Kind:   DisagreePlanExit,
			Go:     fmt.Sprintf("exit %d", comparison.Go.PlanExit),
			Python: fmt.Sprintf("exit %d", comparison.Python.PlanExit),
		})
	}
	inGo := setOf(comparison.Go.PlanKinds)
	inPython := setOf(comparison.Python.PlanKinds)
	for _, kind := range comparison.Go.PlanKinds {
		if !inPython[kind] {
			disagreements = append(disagreements, Disagreement{
				Kind: DisagreeOnlyInGo, Check: kind,
				Go: "planned", Python: "not planned",
			})
		}
	}
	for _, kind := range comparison.Python.PlanKinds {
		if !inGo[kind] {
			disagreements = append(disagreements, Disagreement{
				Kind: DisagreeOnlyInPython, Check: kind,
				Go: "not planned", Python: "planned",
			})
		}
	}
	for _, kind := range comparison.InProcess {
		if !inPython[kind] {
			continue
		}
		disagreements = append(disagreements, Disagreement{
			Kind: DisagreeInProcess, Check: kind,
			Go:     "in-process: `run` refuses the plan (exit 4)",
			Python: "runs it as its own code",
		})
	}
	if comparison.Go.Verdict != comparison.Python.Verdict {
		disagreements = append(disagreements, Disagreement{
			Kind:   DisagreeVerdict,
			Go:     fmt.Sprintf("%s (exit %d)", comparison.Go.Verdict, comparison.Go.VerdictExit),
			Python: fmt.Sprintf("%s (exit %d)", comparison.Python.Verdict, comparison.Python.VerdictExit),
		})
	}
	return disagreements
}

// execGate runs this binary's own face and returns its exit code and combined output.
func execGate(cfg DualRunConfig, args ...string) (int, string, error) {
	return execProgram(cfg, cfg.Self, args...)
}

// execPython runs the Python gate and returns its exit code and combined output.
func execPython(cfg DualRunConfig, args ...string) (int, string, error) {
	return execProgram(cfg, cfg.PythonExe, append([]string{cfg.Python}, args...)...)
}

// execProgram runs one program in the checkout and returns its exit code and output.
//
// A non-zero exit is an answer, not an error: the harness records it. Only a program that
// could not be started, or one that outlived the timeout, is an error.
func execProgram(cfg DualRunConfig, program string, args ...string) (int, string, error) {
	ctx, cancel := context.WithTimeout(context.Background(), cfg.Timeout)
	defer cancel()
	cmd := exec.CommandContext(ctx, program, args...)
	cmd.Dir = cfg.Root
	out, err := cmd.CombinedOutput()
	if ctx.Err() != nil {
		return 0, string(out), externalf("%s did not finish within %s", program, cfg.Timeout)
	}
	var exit *exec.ExitError
	if err != nil && !asExitError(err, &exit) {
		return 0, string(out), externalf("running %s: %v", program, err)
	}
	return cmd.ProcessState.ExitCode(), string(out), nil
}

// asExitError reports whether err is a program that ran and answered with a non-zero code.
func asExitError(err error, target **exec.ExitError) bool {
	exit, ok := err.(*exec.ExitError)
	if ok {
		*target = exit
	}
	return ok
}

// gitOut runs one git command in the checkout and returns its trimmed output.
func gitOut(root string, args ...string) (string, error) {
	cmd := exec.Command("git", append([]string{"-C", root}, args...)...)
	out, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("git %s: %v", strings.Join(args, " "), err)
	}
	return strings.TrimSpace(string(out)), nil
}

// selectionKinds reads the Python plan face's `fork-check: selection {...}` line and
// returns the kinds it planned, sorted.
func selectionKinds(out string) ([]string, error) {
	const marker = "fork-check: selection "
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		var selection struct {
			Checks []struct {
				Kind string `json:"kind"`
			} `json:"checks"`
		}
		if err := json.Unmarshal([]byte(strings.TrimPrefix(line, marker)), &selection); err != nil {
			return nil, fmt.Errorf("decoding the selection line: %v", err)
		}
		kinds := make([]string, 0, len(selection.Checks))
		for _, check := range selection.Checks {
			kinds = append(kinds, check.Kind)
		}
		return sortedUnique(kinds), nil
	}
	return nil, fmt.Errorf("the plan face printed no selection line")
}

// planLineKinds reads the kinds out of the Python run face's `fork-check: plan ...` line,
// which is the last line of every run and names what actually ran.
func planLineKinds(out string) ([]string, error) {
	const marker = "fork-check: plan "
	for _, line := range strings.Split(out, "\n") {
		if !strings.HasPrefix(line, marker) {
			continue
		}
		for _, field := range strings.Fields(line) {
			if !strings.HasPrefix(field, "checks=") {
				continue
			}
			value := strings.TrimPrefix(field, "checks=")
			if value == "-" {
				return nil, nil
			}
			return sortedUnique(strings.Split(value, ",")), nil
		}
	}
	return nil, fmt.Errorf("the run face printed no plan line")
}

// pythonVerdict maps the Python gate's exit code onto the three verdict words. Its
// documented contract is 0 verified, 1 a check failed, 2 nothing could be verified.
func pythonVerdict(code int) string {
	switch code {
	case 0:
		return VerdictPass
	case 1:
		return VerdictFail
	default:
		return VerdictError
	}
}

// verdictOf reads the result out of a verdict record, or `none` when the face wrote none.
func verdictResult(path string) string {
	raw, err := os.ReadFile(path)
	if err != nil {
		return VerdictNone
	}
	envelope, err := record.Decode(raw)
	if err != nil {
		return VerdictNone
	}
	var payload VerdictPayload
	if err := json.Unmarshal(envelope.Payload, &payload); err != nil {
		return VerdictNone
	}
	if payload.Result == "" {
		return VerdictNone
	}
	return payload.Result
}

// kindsOf is the sorted, de-duplicated kind set of a plan's checks.
func kindsOf(checks []PlanCheck) []string {
	kinds := make([]string, 0, len(checks))
	for _, check := range checks {
		kinds = append(kinds, check.Kind)
	}
	return sortedUnique(kinds)
}

func sortedUnique(values []string) []string {
	seen := map[string]bool{}
	unique := make([]string, 0, len(values))
	for _, value := range values {
		if value == "" || seen[value] {
			continue
		}
		seen[value] = true
		unique = append(unique, value)
	}
	sort.Strings(unique)
	return unique
}

func setOf(values []string) map[string]bool {
	set := make(map[string]bool, len(values))
	for _, value := range values {
		set[value] = true
	}
	return set
}

func splitLines(text string) []string {
	lines := make([]string, 0, 8)
	for _, line := range strings.Split(text, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			lines = append(lines, line)
		}
	}
	return lines
}

// firstLine is the one line of a program's output a report keeps: the first non-empty one,
// which is where both gates put their refusal.
func firstLine(out string) string {
	for _, line := range strings.Split(out, "\n") {
		if line = strings.TrimSpace(line); line != "" {
			return line
		}
	}
	return ""
}
