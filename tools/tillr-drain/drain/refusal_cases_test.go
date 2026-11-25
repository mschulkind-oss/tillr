package drain

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

const triageFixtureOneCase = "sh -c 'cd tools/tillr-drain && go test -run ^TestGatheredWriteFairness$ ./drain'"

// triageFixtureBlock is the block tools/fork-check.py prints into the gate's whole log
// (print_triage), with the lines around it the gate log really has.
const triageFixtureBlock = `fork-check: FAILED -- 1 of 18 check(s)
fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-drain: tools/tillr-drain's suite passes
       command: sh -c 'cd tools/tillr-drain && go test ./...'
       case: TestGatheredWriteFairness -- tools/tillr-drain/drain/gathered_fairness_test.go:113
       assertion: a check asserts elapsed time, which the gate's timing rule forbids
       paths: tools/tillr-drain/drain/gathered_fairness_test.go
       touched tools/tillr-drain/drain/gathered_fairness_test.go: 03d689eec fix(x): y
       reproduce (one file): sh -c 'cd tools/tillr-drain && go test ./...'
       one case: ` + triageFixtureOneCase + `
       repair: dispatch a debugger with this packet
fork-check: the machine-readable packet is /x/triage.json
fork-check: 1 recorded failure(s) did not run here -- historical, not this run's failures:
           format: Tracked Go source is gofmt-clean -- failed 2026-11-20T07:13:13Z at 2de522482512
`

func writeTriageRun(t *testing.T, whole string) (runDir, ref string) {
	t.Helper()
	runDir = t.TempDir()
	if err := os.WriteFile(filepath.Join(runDir, "00-gate.log"), []byte(whole), 0600); err != nil {
		t.Fatal(err)
	}
	// The bounded triage sibling is what the verdict's log_ref names; it keeps no case lines.
	ref = filepath.Join(runDir, "00-gate.triage.log")
	if err := os.WriteFile(ref, []byte("tillr-gate: bounded\n"), 0600); err != nil {
		t.Fatal(err)
	}
	return runDir, ref
}

func TestGateFailingChecksReadsTheRecordedCases(t *testing.T) {
	dir, ref := writeTriageRun(t, triageFixtureBlock)
	verdict := mustJSON(map[string]any{"payload": map[string]any{"checks": []map[string]any{{"check_id": "gate:python3", "result": "fail", "log_ref": ref}}}})
	if err := os.WriteFile(filepath.Join(dir, "gate.verdict.json"), []byte(verdict), 0600); err != nil {
		t.Fatal(err)
	}
	checks := gateFailingChecks(dir, "run-x")
	if len(checks) != 1 || len(checks[0].Failing) != 1 {
		t.Fatalf("checks = %+v", checks)
	}
	got := checks[0].Failing[0]
	want := FailingCheck{ID: "go-drain", Label: "tools/tillr-drain's suite passes", Cases: []FailingCase{{
		Name: "TestGatheredWriteFairness", Where: "tools/tillr-drain/drain/gathered_fairness_test.go:113",
		Assertion: "a check asserts elapsed time, which the gate's timing rule forbids", Reproduce: triageFixtureOneCase}}}
	if got.ID != want.ID || got.Label != want.Label || len(got.Cases) != 1 || got.Cases[0] != want.Cases[0] {
		t.Fatalf("failing = %+v, want %+v", got, want)
	}
}

func TestFailureSummaryNamesTheCaseAndTheCommand(t *testing.T) {
	_, ref := writeTriageRun(t, triageFixtureBlock)
	summary := failureSummary([]EndingCheck{{CheckID: "gate:python3", LogRef: ref, Failing: readTriageBlock(ref)}})
	for _, want := range []string{failureHeading, "check go-drain", "case: TestGatheredWriteFairness",
		"at: tools/tillr-drain/drain/gathered_fairness_test.go:113", "assertion: a check asserts elapsed time",
		"reproduce only this case: " + triageFixtureOneCase, "not the whole suite and not the gate"} {
		if !strings.Contains(summary, want) {
			t.Errorf("summary lacks %q:\n%s", want, summary)
		}
	}
	if strings.Contains(summary, noCaseRecorded) {
		t.Errorf("a recorded case was reported as missing:\n%s", summary)
	}
}

func TestFailureSummaryWithoutAPacketSaysSoAndKeepsThePointer(t *testing.T) {
	_, ref := writeTriageRun(t, "tillr-gate: nothing printed a triage block\n")
	if got := readTriageBlock(ref); len(got) != 0 {
		t.Fatalf("invented %+v", got)
	}
	summary := failureSummary([]EndingCheck{{CheckID: "gate:python3", LogRef: ref}})
	if !strings.Contains(summary, "check gate:python3: "+noCaseRecorded) || !strings.Contains(summary, ref) || strings.Contains(summary, "Run the named test") {
		t.Fatalf("fallback = %q", summary)
	}
	if missing := readTriageBlock(filepath.Join(t.TempDir(), "gone.triage.log")); missing != nil {
		t.Fatalf("an unreadable log produced %+v", missing)
	}
}

func TestFailureSummaryNamesGoTestsAndPackagesWithoutTriagePacket(t *testing.T) {
	t.Run("go test output in whole log sibling", func(t *testing.T) {
		whole := `ok  	github.com/example/repo/okpkg	0.010s
--- FAIL: TestAlpha (0.01s)
    alpha_test.go:12: expected 1, got 2
--- FAIL: TestBeta (0.02s)
    --- FAIL: TestBeta/subcase (0.01s)
        beta_test.go:25: subcase failed
FAIL
FAIL	github.com/example/repo/pkgone	0.123s
--- FAIL: TestGamma (0.00s)
    gamma_test.go:5: boom
FAIL
FAIL	github.com/example/repo/pkgtwo	0.050s
`
		_, ref := writeTriageRun(t, whole)
		summary := failureSummary([]EndingCheck{{CheckID: "go-test", LogRef: ref}})
		for _, want := range []string{
			"check go-test",
			"TestAlpha",
			"github.com/example/repo/pkgone",
			"TestBeta/subcase",
			"TestGamma",
			"github.com/example/repo/pkgtwo",
		} {
			if !strings.Contains(summary, want) {
				t.Errorf("summary lacks %q:\n%s", want, summary)
			}
		}
		if strings.Contains(summary, noCaseRecorded) {
			t.Errorf("unexpected %q in summary:\n%s", noCaseRecorded, summary)
		}
	})

	t.Run("go-test-shards output", func(t *testing.T) {
		whole := "go-test-shards: github.com/example/repo/drain shard 2 failed (exit 1): TestShardOne, TestShardTwo\n"
		_, ref := writeTriageRun(t, whole)
		summary := failureSummary([]EndingCheck{{CheckID: "go-drain", LogRef: ref}})
		for _, want := range []string{"TestShardOne", "TestShardTwo", "github.com/example/repo/drain"} {
			if !strings.Contains(summary, want) {
				t.Errorf("summary lacks %q:\n%s", want, summary)
			}
		}
		if strings.Contains(summary, noCaseRecorded) {
			t.Errorf("unexpected %q in summary:\n%s", noCaseRecorded, summary)
		}
	})

	t.Run("go test -json output", func(t *testing.T) {
		whole := `{"Action":"run","Package":"github.com/example/repo/j","Test":"TestJSON"}
{"Action":"fail","Package":"github.com/example/repo/j","Test":"TestJSON","Elapsed":0.01}
{"Action":"fail","Package":"github.com/example/repo/j","Elapsed":0.02}
`
		_, ref := writeTriageRun(t, whole)
		summary := failureSummary([]EndingCheck{{CheckID: "go-json", LogRef: ref}})
		for _, want := range []string{"TestJSON", "github.com/example/repo/j"} {
			if !strings.Contains(summary, want) {
				t.Errorf("summary lacks %q:\n%s", want, summary)
			}
		}
		if strings.Contains(summary, noCaseRecorded) {
			t.Errorf("unexpected %q in summary:\n%s", noCaseRecorded, summary)
		}
	})

	t.Run("package build failure without named test", func(t *testing.T) {
		whole := "# github.com/example/repo/broken\nbroken.go:10:2: undefined: x\nFAIL\tgithub.com/example/repo/broken [build failed]\n"
		_, ref := writeTriageRun(t, whole)
		summary := failureSummary([]EndingCheck{{CheckID: "go-build", LogRef: ref}})
		if !strings.Contains(summary, "github.com/example/repo/broken") {
			t.Errorf("summary lacks package:\n%s", summary)
		}
		if strings.Contains(summary, noCaseRecorded) {
			t.Errorf("unexpected %q in summary:\n%s", noCaseRecorded, summary)
		}
	})

	t.Run("triage block with no case falls back to go test output in log", func(t *testing.T) {
		whole := `--- FAIL: TestFromLog (0.01s)
FAIL
FAIL	github.com/example/repo/fallback	0.100s
fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-drain: tools/tillr-drain's suite passes
       no case named in the output; reproduce with: go test ./...
`
		_, ref := writeTriageRun(t, whole)
		summary := failureSummary([]EndingCheck{{CheckID: "gate:python3", LogRef: ref, Failing: readTriageBlock(ref)}})
		for _, want := range []string{"TestFromLog", "github.com/example/repo/fallback"} {
			if !strings.Contains(summary, want) {
				t.Errorf("summary lacks %q:\n%s", want, summary)
			}
		}
		if strings.Contains(summary, noCaseRecorded) {
			t.Errorf("unexpected %q in summary:\n%s", noCaseRecorded, summary)
		}
	})

	t.Run("passing go tests without failing test does not claim no failing case was recorded", func(t *testing.T) {
		whole := "ok  \tgithub.com/example/repo/okpkg\t0.010s\nlint failed\n"
		_, ref := writeTriageRun(t, whole)
		summary := failureSummary([]EndingCheck{{CheckID: "check-x", LogRef: ref}})
		if strings.Contains(summary, noCaseRecorded) {
			t.Errorf("unexpected %q in summary:\n%s", noCaseRecorded, summary)
		}
	})
}

func TestFailureSummaryIsCutBetweenChecks(t *testing.T) {
	var failing []FailingCheck
	for i := 0; i < 200; i++ {
		failing = append(failing, FailingCheck{ID: "c", Cases: []FailingCase{{Name: "TestA", Assertion: strings.Repeat("x", 190), Reproduce: "go test"}}})
	}
	summary := failureSummary([]EndingCheck{{CheckID: "g", Failing: failing}})
	if len(summary) > failureSummaryMax+400 || !strings.Contains(summary, "omitted") || !strings.HasSuffix(summary, "\n") {
		t.Fatalf("not bounded on a line boundary: %d bytes", len(summary))
	}
}

func TestReturnedBriefOpensWithTheGateAnswer(t *testing.T) {
	_, ref := writeTriageRun(t, triageFixtureBlock)
	checks := []EndingCheck{{CheckID: "gate:python3", LogRef: ref, Failing: readTriageBlock(ref)}}
	brief := returnedBrief(ReturnedEnding{RunID: "run-x", Cause: RefusalGate, Checks: checks})
	answer, previous := strings.Index(brief, "check go-drain"), strings.Index(brief, "## Your previous attempt")
	if !strings.HasPrefix(strings.TrimSpace(brief), failureHeading) || answer < 0 || previous < answer ||
		!strings.Contains(brief, "reproduce only this case: "+triageFixtureOneCase) || !strings.Contains(brief, ref) {
		t.Fatalf("continuation does not open with the answer:\n%s", brief)
	}
	// An answer text that already holds the summary (a parked call's) is not repeated.
	again := returnedBrief(ReturnedEnding{RunID: "run-x", Cause: RefusalGate, Detail: failureSummary(checks), Checks: checks})
	if strings.Count(again, failureHeading) != 1 {
		t.Fatalf("summary repeated:\n%s", again)
	}
}

func TestReturnedSessionTurnOpensWithTheGateAnswer(t *testing.T) {
	for name, tc := range map[string]struct {
		output string
		want   []string
	}{
		"packet":    {triageFixtureBlock, []string{"check go-drain", "case: TestGatheredWriteFairness", "reproduce only this case: " + triageFixtureOneCase}},
		"no packet": {"dead anchor\n", []string{"check documents: " + noCaseRecorded, "documents.log"}},
	} {
		t.Run(name, func(t *testing.T) {
			_, l, _, base := returnedSessionFixtureWith(t, RefusalGate, tc.output)
			p := mustPlan(t, l)[0]
			d := applyReturnedTurn(t, l, p, base)
			prompt := d.RunRequest.ResumePrompt
			if !strings.HasPrefix(strings.TrimSpace(prompt), failureHeading) || strings.Index(prompt, "## Your previous attempt") < 0 {
				t.Fatalf("resumed turn does not open with the answer:\n%s", prompt)
			}
			for _, want := range tc.want {
				if !strings.Contains(prompt, want) {
					t.Errorf("resumed turn lacks %q:\n%s", want, prompt)
				}
			}
		})
	}
}
