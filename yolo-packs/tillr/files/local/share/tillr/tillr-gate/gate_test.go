package main_test

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// fakeGate writes a program that plays the gate's part. Its exit status is the thing the
// mapping table is about, and its output is what the verdict's evidence is read from.
func fakeGate(t *testing.T, body string) string {
	t.Helper()
	path := filepath.Join(scratch(t, "gate"), "fake-gate.sh")
	if err := os.WriteFile(path, []byte(body), 0o755); err != nil {
		t.Fatalf("writing the fake gate failed: %v", err)
	}
	return path
}

// gateArgs spells a gate program and its arguments as the run face's flags: the program
// under `--gate`, everything after it as a repeated `--gate-arg`.
func gateArgs(program []string) []string {
	args := []string{"--gate", program[0]}
	for _, arg := range program[1:] {
		args = append(args, "--gate-arg", arg)
	}
	return args
}

// The mapping table is the whole adapter: the gate program's exit is the verdict, and the
// three codes it prints are the three the frozen contract has. 0 is `pass`, 1 is `fail`,
// and 2 -- the gate refusing to judge -- is `error`/4, never a `fail`: "the harness could
// not judge" is not "the code is bad", and collapsing them is how a red gate stops being
// believed. A code the gate does not use, a signal, and a program that is not there are
// `error` too, because none of them judged the tree either.
//
// Negative control: the rows disagree with each other, so a face that answered one code
// for every exit fails at least one of them -- a face that read every nonzero exit as an
// error fails the `/bin/false` row, and a face that read every nonzero exit as a fail
// fails the exit-2 row.
func TestTheGateProgramsExitIsTheVerdict(t *testing.T) {
	cases := []struct {
		name   string
		gate   []string
		code   int
		result string
	}{
		{"verified", []string{"/bin/true"}, 0, "pass"},
		{"a check failed", []string{"/bin/false"}, 1, "fail"},
		{"refused to judge", []string{"sh", "-c", "exit 2"}, 4, "error"},
		{"a code the gate does not use", []string{"sh", "-c", "exit 3"}, 4, "error"},
		{"killed by a signal", []string{"sh", "-c", "kill -TERM $$"}, 4, "error"},
		{"the program is not there", []string{"/nonexistent/gate"}, 4, "error"},
	}
	for _, one := range cases {
		t.Run(one.name, func(t *testing.T) {
			plan := planRecord(t, planIn{
				PlanID: "p-gate", BaseSHA: "base", TreeSHA: "tree-bound", RegistryDigest: "d-1",
			})
			code, raw := runPlan(t, plan, tree(t), gateArgs(one.gate)...)
			if code != one.code {
				t.Fatalf("the gate program %v answered %d through the face, want %d: %s",
					one.gate, code, one.code, raw)
			}
			got := decodeVerdict(t, raw)
			if got.Payload.Result != one.result {
				t.Fatalf("the gate program %v produced verdict %q, want %q",
					one.gate, got.Payload.Result, one.result)
			}
			// The tree binding survives the adapter: land refuses a verdict that does not
			// name the tree it examined, whichever path produced it.
			if got.Payload.PlanID != "p-gate" || got.Payload.TreeSHA != "tree-bound" ||
				got.Payload.RegistryDigest != "d-1" {
				t.Fatalf("the verdict binds %+v", got.Payload)
			}
			if len(got.Payload.Checks) != 1 {
				t.Fatalf("the gate program is recorded as %d check(s), want 1: %+v",
					len(got.Payload.Checks), got.Payload.Checks)
			}
			check := got.Payload.Checks[0]
			if check.Result != one.result {
				t.Fatalf("the recorded check is %q, want %q", check.Result, one.result)
			}
			if want := "gate:" + filepath.Base(one.gate[0]); check.CheckID != want {
				t.Fatalf("the recorded check is %q, want %q", check.CheckID, want)
			}
			// A verdict that points at nothing is a re-run: the face writes the gate's
			// own triage and the record names it.
			if _, err := os.ReadFile(check.LogRef); err != nil {
				t.Fatalf("the verdict's evidence %q cannot be read: %v", check.LogRef, err)
			}
		})
	}
}

// A plan that owes an in-process check is still judged when a `--gate` program is named:
// the named program runs its own plan, in-process checks included, so the face hands it
// the tree and records what it answered. This is the one seam tonight's loop uses — the
// real registry owes `file-size`, `scratch` and `design-citations` on every change, and a
// face that refused every plan carrying them would refuse every candidate.
//
// Negative control: the same plan without `--gate` is `TestRunRefusesAPlanThatOwesAnInProcessCheck`
// (run_test.go), which is exit 4, so a face that answered 0 for this plan regardless of the
// program named there fails that case, and a face that refused this one fails here.
func TestAPlanOwingAnInProcessCheckIsJudgedByTheNamedProgram(t *testing.T) {
	plan := planIn{
		PlanID: "p-gate-in-process", BaseSHA: "base", TreeSHA: "tree-bound", RegistryDigest: "d-1",
	}
	plan.InProcess = append(plan.InProcess, struct {
		CheckID string `json:"check_id"`
	}{CheckID: "file-size"}, struct {
		CheckID string `json:"check_id"`
	}{CheckID: "scratch"})
	code, raw := runPlan(t, planRecord(t, plan), tree(t), gateArgs([]string{"/bin/true"})...)
	if code != 0 {
		t.Fatalf("a plan owing in-process checks, judged by a named program, exited %d, want 0: %s", code, raw)
	}
	got := decodeVerdict(t, raw)
	if got.Payload.Result != "pass" {
		t.Fatalf("the verdict result is %q, want pass", got.Payload.Result)
	}
	// One check, the program's: the face does not claim to have run the in-process checks
	// itself, so a verdict that listed them would be a record of a run that did not happen.
	if len(got.Payload.Checks) != 1 || got.Payload.Checks[0].CheckID != "gate:true" {
		t.Fatalf("the verdict records %+v, want the one named program", got.Payload.Checks)
	}
}

// A red verdict must say which check failed without a human re-running the gate, and it
// must not carry the whole log: the record points at a bounded triage of the gate's own
// status lines, and the program's ordinary output stays beside it as a file.
//
// Negative control: the fake gate prints 200 lines of a check's output before its one
// `triage` line, so a triage that kept the last lines of the run, or the whole log, fails
// the "names the failing check" and the "bounded" assertions respectively.
func TestTheRedVerdictCarriesTheGatesBoundedTriage(t *testing.T) {
	gate := fakeGate(t, `#!/bin/sh
i=0
while [ "$i" -lt 200 ]; do
  echo "  step $i: building the candidate"
  i=$((i + 1))
done
echo "fork-check: FAILED -- 1 of 12 check(s) (812.0s); each failure is diagnosed from its own output, and none was re-run"
echo "           documents: uvx vantage-check"
echo "fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:"
echo "  triage documents: uvx vantage-check rejects the added citation"
exit 1
`)
	plan := planRecord(t, planIn{PlanID: "p-triage", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "d-1"})
	code, raw := runPlan(t, plan, tree(t), "--gate", gate)
	if code != 1 {
		t.Fatalf("the fake gate's failure exited %d, want 1: %s", code, raw)
	}
	got := decodeVerdict(t, raw)
	if got.Payload.Result != "fail" {
		t.Fatalf("the verdict result is %q, want fail", got.Payload.Result)
	}
	logRef := got.Payload.Checks[0].LogRef
	triage, err := os.ReadFile(logRef)
	if err != nil {
		t.Fatalf("reading the verdict's evidence failed: %v", err)
	}
	if !strings.Contains(string(triage), "triage documents: uvx vantage-check") {
		t.Fatalf("the verdict's evidence does not name the failing check:\n%s", triage)
	}
	if strings.Contains(string(triage), "step 0: building") {
		t.Fatalf("the verdict's evidence is the log, not the gate's triage:\n%s", triage)
	}
	if lines := strings.Count(string(triage), "\n"); lines > 30 {
		t.Fatalf("the verdict's evidence is %d lines, which is a log rather than evidence", lines)
	}
	whole, err := os.ReadFile(strings.TrimSuffix(logRef, ".triage.log") + ".log")
	if err != nil {
		t.Fatalf("the whole output is not readable beside the triage: %v", err)
	}
	if !strings.Contains(string(whole), "step 199: building") {
		t.Fatal("the whole output was not kept: the triage replaced it rather than bounding it")
	}
}

// A fast check that ran past its budget is reported by the declaration face on a
// `tillr-gate checks:` line, and that line must survive into the verdict's evidence: the
// overrun is a defect against the check's declared cost, and the triage is where a reader
// of the verdict looks. Negative control: the overrun line is followed by 200 lines of
// other output and then a `fork-check:` line, so neither the tail fallback nor another
// prefix can keep it -- only `tillr-gate checks:` being a gate prefix does.
func TestTheTriageKeepsTheDeclarationFacesOverrunLines(t *testing.T) {
	gate := fakeGate(t, `#!/bin/sh
echo "tillr-gate checks: go-build: ran 5700 ms, 3000 ms over its budget_ms; its verdict (pass) stands"
i=0
while [ "$i" -lt 200 ]; do
  echo "  step $i: running a check"
  i=$((i + 1))
done
echo "fork-check: PASSED"
exit 0
`)
	plan := planRecord(t, planIn{PlanID: "p-overrun-triage", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "d-1"})
	code, raw := runPlan(t, plan, tree(t), "--gate", gate)
	if code != 0 {
		t.Fatalf("the fake gate's pass exited %d, want 0: %s", code, raw)
	}
	triage, err := os.ReadFile(decodeVerdict(t, raw).Payload.Checks[0].LogRef)
	if err != nil {
		t.Fatalf("reading the verdict's evidence failed: %v", err)
	}
	if !strings.Contains(string(triage), "tillr-gate checks: go-build: ran 5700 ms") {
		t.Fatalf("the verdict's evidence lost the overrun line:\n%s", triage)
	}
}

// A gate is slow, so its run is bounded, and an expired bound must not hang and must not
// pass: the process group is killed and the verdict is `error`/4, because a gate that did
// not finish judged nothing -- a `pass` there would certify a tree nothing checked.
//
// The assertion is the verdict and the recorded reason, never an elapsed time: a case that
// measured wall clock would fail on a loaded machine for a run that behaved correctly.
func TestAnExpiredGateBoundIsAnErrorNotAPass(t *testing.T) {
	plan := planRecord(t, planIn{PlanID: "p-bound", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "d-1"})
	code, raw := runPlan(t, plan, tree(t), "--gate", "sh",
		"--gate-arg", "-c", "--gate-arg", "sleep 30", "--gate-timeout", "200ms")
	if code != 4 {
		t.Fatalf("a gate program that outlived its bound exited %d, want 4: %s", code, raw)
	}
	got := decodeVerdict(t, raw)
	if got.Payload.Result != "error" {
		t.Fatalf("an expired bound produced verdict %q, want error", got.Payload.Result)
	}
	triage, err := os.ReadFile(got.Payload.Checks[0].LogRef)
	if err != nil {
		t.Fatalf("reading the verdict's evidence failed: %v", err)
	}
	if !strings.Contains(string(triage), "--gate-timeout") {
		t.Fatalf("the verdict does not record why the run was killed:\n%s", triage)
	}
}

// `--gate-arg` without `--gate` is a usage error: an argument list with no program is not
// a gate, and a face that accepted it would run the plan's own checks while the caller
// believed a named program had.
func TestGateArgumentsWithoutAGateProgramAreAUsageError(t *testing.T) {
	plan := planRecord(t, planIn{PlanID: "p-args", BaseSHA: "base", TreeSHA: "tree", RegistryDigest: "d-1"})
	code, raw := runPlan(t, plan, tree(t), "--gate-arg", "--all")
	if code != 2 {
		t.Fatalf("--gate-arg without --gate exited %d, want 2: %s", code, raw)
	}
}
