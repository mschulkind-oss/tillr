package main

import (
	"io"
	"os"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

// TestTheSpendLineNamesTheMeasurement pins what the line the loop ends with carries: the
// dollars and the tokens measured across its runs, so an operator can see what a night cost
// without running it again.
//
// This case is the removed ceiling's rewrite: it used to assert the line named the ceiling
// it compared against, and it now asserts the line names no ceiling at all — there is none
// left (docs/design/momentum-principle.md).
func TestTheSpendLineNamesTheMeasurement(t *testing.T) {
	line := spendLine(drain.LoopResult{SpendUSD: 3.5, Tokens: 250_000})
	for _, want := range []string{"$3.500000", "250000 tokens"} {
		if !strings.Contains(line, want) {
			t.Fatalf("the spend line %q does not name %q", line, want)
		}
	}
	if strings.Contains(line, "ceiling") {
		t.Fatalf("the spend line %q names a ceiling the loop has no flag for", line)
	}
}

// TestTheSpendLineSaysWhatWasNotMeasured is the honesty rule at the printed line: a total
// that omits an unmeasured run says so, and says which run it was.
func TestTheSpendLineSaysWhatWasNotMeasured(t *testing.T) {
	line := spendLine(drain.LoopResult{
		SpendUSD: 0.5, Tokens: 100,
		CostUnknown: 1, CostUnknownExample: "run-9 on muse",
		SpendUnknown: 2, SpendUnknownExample: "run-8 on agy",
	})
	for _, want := range []string{
		"$0.500000", "100 tokens",
		"2 run(s) reported no token usage (first: run-8 on agy)",
		"1 run(s) reported no cost (first: run-9 on muse)",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("the spend line %q does not name %q", line, want)
		}
	}
}

// TestTheLoopCommandRefusesTheDeletedBudgets is the operator's half of the ruling: the tick,
// dollar and token budgets are gone from `tillr drain loop`, so a supervisor still passing
// one is told it is a usage error (exit 2) that names the flag, instead of it parsing and the
// loop running to some other fate.
//
// The assertion is the flag parser's own sentence rather than only the exit code, because 2 is
// also what a missing policy answers: put any of the three flags back in loopFace and this
// case fails with the policy refusal in place of "flag provided but not defined".
func TestTheLoopCommandRefusesTheDeletedBudgets(t *testing.T) {
	for _, deleted := range []string{"--max-ticks", "--max-spend-usd", "--max-tokens"} {
		code, stderr := loopFaceOutcome(t, []string{
			"--root", t.TempDir(),
			"--gate", "gate", "--land", "land", "--verdict", "verdict",
			deleted, "1",
		})
		if code != 2 {
			t.Fatalf("`tillr drain loop %s 1` exited %d, want 2: the budget flags are gone", deleted, code)
		}
		if !strings.Contains(stderr, "flag provided but not defined") || !strings.Contains(stderr, strings.TrimPrefix(deleted, "--")) {
			t.Fatalf("`tillr drain loop %s 1` did not refuse the flag by name: %q", deleted, stderr)
		}
	}
}

// loopFaceOutcome runs the loop face with stderr captured, and answers its exit status and the
// diagnostic it printed.
func loopFaceOutcome(t *testing.T, args []string) (int, string) {
	t.Helper()
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatalf("creating the stderr pipe failed: %v", err)
	}
	saved := os.Stderr
	os.Stderr = w
	code := loopFace(args)
	os.Stderr = saved
	if err := w.Close(); err != nil {
		t.Fatalf("closing the captured stderr failed: %v", err)
	}
	body, err := io.ReadAll(r)
	if err != nil {
		t.Fatalf("reading the captured stderr failed: %v", err)
	}
	if err := r.Close(); err != nil {
		t.Fatalf("closing the read end failed: %v", err)
	}
	return code, string(body)
}
