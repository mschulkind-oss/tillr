package checkresult

import "testing"

func TestClassifyRawResults(t *testing.T) {
	exit := func(code int) *int { return &code }
	cases := []struct {
		name   string
		in     Observation
		want   Outcome
		detail string
	}{
		{"command pass", Observation{Source: CheckCommand, ExitCode: exit(0)}, Pass, "passed"},
		{"command cannot judge", Observation{Source: CheckCommand, ExitCode: exit(4)}, Unknown, "error"},
		{"command signalled", Observation{Source: CheckCommand, ExitCode: exit(-1)}, Unknown, "error"},
		{"command wait failed", Observation{Source: CheckCommand, ExitCode: exit(0), ProcessError: true}, Unknown, "error"},
		{"command failure", Observation{Source: CheckCommand, ExitCode: exit(1)}, Fail, "failed"},
		{"command exit six is failure", Observation{Source: CheckCommand, ExitCode: exit(6)}, Fail, "failed"},
		{"command exit 77 is failure", Observation{Source: CheckCommand, ExitCode: exit(77)}, Fail, "failed"},
		{"command under threshold", Observation{Source: CheckCommand, ExitCode: exit(0), SilentSamples: 59}, Pass, "passed"},
		{"watchdog overrides wait", Observation{Source: CheckCommand, ProcessError: true, SilentSamples: 60}, Unknown, "stalled"},
		{"gate pass", Observation{Source: JudgingProgram, ExitCode: exit(0)}, Pass, "passed"},
		{"gate fail", Observation{Source: JudgingProgram, ExitCode: exit(1)}, Fail, "failed"},
		{"gate stall", Observation{Source: JudgingProgram, ExitCode: exit(6)}, Unknown, "stalled"},
		{"gate drops", Observation{Source: JudgingProgram, ExitCode: exit(77)}, Unavailable, "dropped"},
		{"gate other exit", Observation{Source: JudgingProgram, ExitCode: exit(8)}, Unknown, "error"},
		{"gate did not start", Observation{Source: JudgingProgram}, Unknown, "error"},
		{"binary program pass", Observation{Source: BinaryProgram, ExitCode: exit(0)}, Pass, "passed"},
		{"binary program fail", Observation{Source: BinaryProgram, ExitCode: exit(1)}, Fail, "failed"},
		{"binary program exit six is error", Observation{Source: BinaryProgram, ExitCode: exit(6)}, Unknown, "error"},
		{"binary program exit 77 is error", Observation{Source: BinaryProgram, ExitCode: exit(77)}, Unknown, "error"},
		{"binary program process error", Observation{Source: BinaryProgram, ExitCode: exit(0), ProcessError: true}, Unknown, "error"},
		{"binary program no exit", Observation{Source: BinaryProgram}, Unknown, "error"},
		{"go build exit zero", Observation{Source: GoModuleBuild, ExitCode: exit(0)}, Pass, "passed"},
		{"go build exit four is failure (P6)", Observation{Source: GoModuleBuild, ExitCode: exit(4)}, Fail, "failed"},
		{"go build negative exit is failure", Observation{Source: GoModuleBuild, ExitCode: exit(-9)}, Fail, "failed"},
		{"go build no exit", Observation{Source: GoModuleBuild}, Unknown, "error"},
		{"render exit zero with report", Observation{Source: DocumentRender, ExitCode: exit(0), ReportRead: true}, Pass, "passed"},
		{"render exit zero without report", Observation{Source: DocumentRender, ExitCode: exit(0)}, Unknown, "error"},
		{"render exit one with finding", Observation{Source: DocumentRender, ExitCode: exit(1), ReportRead: true, HasFinding: true}, Fail, "failed"},
		{"render exit one without finding", Observation{Source: DocumentRender, ExitCode: exit(1), ReportRead: true}, Unknown, "error"},
		{"render other exit even with finding", Observation{Source: DocumentRender, ExitCode: exit(4), ReportRead: true, HasFinding: true}, Unknown, "error"},
		{"render no exit", Observation{Source: DocumentRender}, Unknown, "error"},
		{"cached pass", Observation{Source: CachedRecord, Word: "pass"}, Pass, "cached"},
		{"cached red", Observation{Source: CachedRecord, Word: "fail"}, Fail, "failed"},
		{"cached unreadable", Observation{Source: CachedRecord}, Unknown, "error"},
		{"cached unexpected", Observation{Source: CachedRecord, Word: "ok"}, Unknown, "error"},
		{"focused go test package absent", Observation{Source: FocusedGoTest, PackageAbsent: true}, Pass, DetailPackageAbsent},
		{"focused go test package absent overrides error", Observation{Source: FocusedGoTest, PackageAbsent: true, ProcessError: true, ExitCode: exit(2), Failed: true}, Pass, DetailPackageAbsent},
		{"focused go test exit 0 passed", Observation{Source: FocusedGoTest, ExitCode: exit(0), Passed: true}, Pass, DetailPassed},
		{"focused go test exit 0 skipped", Observation{Source: FocusedGoTest, ExitCode: exit(0), Skipped: true}, Pass, DetailFocusedTestSkipped},
		{"focused go test exit 0 absent", Observation{Source: FocusedGoTest, ExitCode: exit(0)}, Pass, DetailFocusedTestAbsent},
		{"focused go test exit 0 with process error", Observation{Source: FocusedGoTest, ExitCode: exit(0), ProcessError: true, Passed: true}, Unknown, DetailError},
		{"focused go test exit 0 with failed", Observation{Source: FocusedGoTest, ExitCode: exit(0), Failed: true}, Unknown, DetailError},
		{"focused go test exit 1 with failed", Observation{Source: FocusedGoTest, ExitCode: exit(1), Failed: true}, Fail, DetailFailed},
		{"focused go test exit 1 without failed", Observation{Source: FocusedGoTest, ExitCode: exit(1)}, Unknown, DetailError},
		{"focused go test exit 2 with failed", Observation{Source: FocusedGoTest, ExitCode: exit(2), Failed: true}, Unknown, DetailError},
		{"focused go test nil exit", Observation{Source: FocusedGoTest}, Unknown, DetailError},
		{"missing source", Observation{ExitCode: exit(0)}, Unknown, "error"},
	}
	words := []struct {
		raw     string
		outcome Outcome
		detail  string
	}{
		{"pass", Pass, "passed"}, {"PASS", Pass, "passed"}, {"ok", Pass, "passed"},
		{"CACHED", Pass, "cached"}, {"remembered", Pass, "remembered"},
		{"fail", Fail, "failed"}, {"FAIL", Fail, "failed"},
		{"stalled", Unknown, "stalled"}, {"STALLED", Unknown, "stalled"},
		{"unavailable", Unavailable, "unavailable"}, {"skip", Unavailable, "skipped"},
		{"QUARANTINED", Unavailable, "quarantined"}, {"skipped: no changed path is read", Unavailable, "skipped"},
		{"skipped: custom reason", Unavailable, "skipped"},
		{"dropped", Unavailable, "dropped"}, {"absent", Unavailable, "absent"},
		{"disabled", Disabled, "disabled"},
		{"error", Unknown, "error"}, {"ERR", Unknown, "error"}, {"ERROR", Unknown, "error"},
		{"STALE", Unknown, "error"}, {"not-run", Unknown, "error"}, {"over-budget", Unknown, "error"},
		{"", Unknown, "error"}, {"other", Unknown, "error"},
		{"inherited", Unknown, "error"}, {"pass-inherited", Unknown, "error"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			tc.in.Detail = "raw diagnostic\n"
			assertResult(t, Classify(tc.in), Result{Outcome: tc.want, Detail: tc.detail, RawWord: tc.in.Word, RawDetail: tc.in.Detail})
		})
	}
	for _, tc := range words {
		t.Run("word/"+tc.raw, func(t *testing.T) {
			assertResult(t, Classify(Observation{Source: ResultWord, Word: tc.raw}), Result{Outcome: tc.outcome, Detail: tc.detail, RawWord: tc.raw})
		})
	}
	for _, tc := range []struct {
		source  Source
		raw     string
		outcome Outcome
		detail  string
	}{
		{HeavyCheckWord, "pass", Pass, "passed"},
		{HeavyCheckWord, "fail", Fail, "failed"},
		{HeavyCheckWord, "unavailable", Unavailable, "unavailable"},
		{HeavyCheckWord, "stalled", Unknown, "stalled"},
		{HeavyCheckWord, "error", Unknown, "error"},
		{HeavyCheckWord, "PASS", Unknown, "unrecognized"},
		{HeavyCheckWord, "ok", Unknown, "unrecognized"},
		{HeavyCheckWord, "disabled", Unknown, "unrecognized"},
		{HeavyCheckWord, "", Unknown, "unrecognized"},
		{FixerCheckWord, "pass", Pass, "passed"},
		{FixerCheckWord, "fail", Fail, "failed"},
		{FixerCheckWord, "dropped", Unavailable, "dropped"},
		{FixerCheckWord, "absent", Unavailable, "absent"},
		{FixerCheckWord, "error", Unknown, "error"},
		{FixerCheckWord, "stalled", Unknown, "error"},
		{FixerCheckWord, "unavailable", Unknown, "error"},
		{FixerCheckWord, "PASS", Unknown, "error"},
		{FixerCheckWord, "", Unknown, "error"},
		{LegacySpan, "PASS", Pass, "passed"},
		{LegacySpan, "ok", Pass, "passed"},
		{LegacySpan, "CACHED", Pass, "cached"},
		{LegacySpan, "skip", Unavailable, "skipped"},
		{LegacySpan, "QUARANTINED", Unavailable, "quarantined"},
		{LegacySpan, "FAIL", Fail, "failed"},
		{LegacySpan, "STALLED", Unknown, "stalled"},
		{LegacySpan, "ERR", Unknown, "error"},
		{LegacySpan, "STALE", Unknown, "error"},
		{LegacySpan, "not-run", Unknown, "error"},
		{LegacySpan, "pass", Unknown, "error"},
		{LegacySpan, "", Unknown, "error"},
		{CheckSpan, "PASS", Pass, "passed"},
		{CheckSpan, "ok", Pass, "passed"},
		{CheckSpan, "CACHED", Pass, "cached"},
		{CheckSpan, "skip", Unavailable, "skipped"},
		{CheckSpan, "QUARANTINED", Unavailable, "quarantined"},
		{CheckSpan, "FAIL", Fail, "failed"},
		{CheckSpan, "STALLED", Unknown, "stalled"},
		{CheckSpan, "not-run", Unknown, "not-run"},
		{CheckSpan, "STALE", Unknown, "stale"},
		{CheckSpan, "ERR", Unknown, "error"},
		{CheckSpan, "ERROR", Unknown, "unrecognized"},
		{CheckSpan, "", Unknown, "unrecognized"},
		{TriageSpan, "FAIL", Fail, DetailFailed},
		{TriageSpan, "fail", Fail, DetailFailed},
		{TriageSpan, "STALLED", Unknown, DetailStalled},
		{TriageSpan, "stalled", Unknown, DetailStalled},
		{TriageSpan, "ERROR", Unknown, DetailError},
		{TriageSpan, "error", Unknown, DetailError},
		{TriageSpan, "PASS", Pass, DetailPassed},
		{TriageSpan, "pass", Pass, DetailPassed},
		{TriageSpan, "OK", Pass, DetailPassed},
		{TriageSpan, "ok", Pass, DetailPassed},
		{TriageSpan, "CACHED", Pass, DetailPassed},
		{TriageSpan, "cached", Pass, DetailPassed},
		{TriageSpan, "SKIP", Unavailable, DetailSkipped},
		{TriageSpan, "skip", Unavailable, DetailSkipped},
		{TriageSpan, "QUARANTINED", Unavailable, DetailSkipped},
		{TriageSpan, "quarantined", Unavailable, DetailSkipped},
		{TriageSpan, "ERR", Unknown, DetailNotRetained},
		{TriageSpan, "STALE", Unknown, DetailNotRetained},
		{TriageSpan, "not-run", Unknown, DetailNotRetained},
		{TriageSpan, "", Unknown, DetailNotRetained},
		{TriageSpan, " PASS ", Unknown, DetailNotRetained},
		{TriageSpan, "FAIL\n", Unknown, DetailNotRetained},
	} {
		t.Run(string(tc.source)+"/"+tc.raw, func(t *testing.T) {
			assertResult(t, Classify(Observation{Source: tc.source, Word: tc.raw}), Result{Outcome: tc.outcome, Detail: tc.detail, RawWord: tc.raw})
		})
	}
}

func assertResult(t *testing.T, got, want Result) {
	t.Helper()
	if got != want {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestClassifyLegacyCheck(t *testing.T) {
	// Give each earlier step a competing later one to show which wins.
	cases := []struct {
		name    string
		code    int
		facts   LegacyFacts
		outcome Outcome
		detail  string
	}{
		{"not run before cache", 1, LegacyFacts{NotRun: true, Cached: true}, Unknown, "not-run"},
		{"cache before quarantine", 1, LegacyFacts{Cached: true, Quarantined: true}, Pass, "cached"},
		{"quarantine before sentinel skip", 1, LegacyFacts{Quarantined: true, SentinelSkip: true}, Unavailable, "quarantined"},
		{"sentinel skip before stale", 1, LegacyFacts{SentinelSkip: true, Stale: true}, Unavailable, "skipped"},
		{"stale before stall", -1000, LegacyFacts{Stale: true}, Unknown, "stale"},
		{"stall before generic nonzero", -1000, LegacyFacts{}, Unknown, "stalled"},
		{"success before group", 0, LegacyFacts{Group: LegacyNamed}, Pass, "passed"},
		{"ordinary exit four (F3)", 4, LegacyFacts{Group: LegacyVerdict}, Fail, "failed"},
		{"timeout (F3)", 124, LegacyFacts{Group: LegacyVerdict}, Unknown, "error"},
		{"runner unavailable", 125, LegacyFacts{}, Unknown, "error"},
		{"not executable", 126, LegacyFacts{}, Unknown, "error"},
		{"not found", 127, LegacyFacts{}, Unknown, "error"},
		{"killed", 137, LegacyFacts{}, Unknown, "error"},
		{"outside signal", -15, LegacyFacts{}, Unknown, "error"},
		{"last outside signal", -64, LegacyFacts{}, Unknown, "error"},
		{"negative non-signal exit", -65, LegacyFacts{}, Fail, "failed"},
		{"reserved exit four", 4, LegacyFacts{ReservesExit4: true}, Unknown, "error"},
		{"reserved exit does not change exit one", 1, LegacyFacts{ReservesExit4: true}, Fail, "failed"},
		{"fast tree refused before run", 1, LegacyFacts{FastTree: true, PreflightRefused: true}, Unknown, "error"},
		{"fast tree ran", 1, LegacyFacts{FastTree: true}, Fail, "failed"},
		{"unable refused before run", 1, LegacyFacts{Group: LegacyUnable, PreflightRefused: true}, Unknown, "error"},
		{"unable ran", 1, LegacyFacts{Group: LegacyUnable}, Fail, "failed"},
		{"ordinary preflight flag is not sufficient", 1, LegacyFacts{PreflightRefused: true}, Fail, "failed"},
		{"fork candidate refused linked worktree", 1, LegacyFacts{Group: LegacyUnable, LinkedWorktreeRefusal: true}, Unknown, "error"},
		{"named without case", 1, LegacyFacts{Group: LegacyNamed}, Unknown, "error"},
		{"named with case", 1, LegacyFacts{Group: LegacyNamed, NamedCase: true}, Fail, "failed"},
		{"invariant without match", 1, LegacyFacts{Group: LegacyInvariant}, Unknown, "error"},
		{"invariant with match", 1, LegacyFacts{Group: LegacyInvariant, NamedInvariant: true}, Fail, "failed"},
		{"unlisted group", 1, LegacyFacts{Group: "new-kind"}, Fail, "failed"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			got := Classify(Observation{Source: LegacyCheck, ExitCode: &tc.code, Legacy: &tc.facts})
			assertResult(t, got, Result{Outcome: tc.outcome, Detail: tc.detail})
		})
	}
	assertResult(t, Classify(Observation{Source: LegacyCheck}), Result{Outcome: Unknown, Detail: "error"})
}

func TestAggregatePrecedence(t *testing.T) {
	// Inputs are ranked in descending order independently for each consumer.
	pass := Result{Outcome: Pass, Detail: "cached"}
	fail := Result{Outcome: Fail, Detail: "failed"}
	stalled := Result{Outcome: Unknown, Detail: "stalled"}
	unanswered := Result{Outcome: Unknown, Detail: "error"}
	stale := Result{Outcome: Unknown, Detail: "stale"}
	notRun := Result{Outcome: Unknown, Detail: "not-run"}
	skipped := Result{Outcome: Unavailable, Detail: "skipped"}
	omitted := []Result{
		{Outcome: Disabled},
		{Outcome: Unavailable, Detail: "quarantined"},
		{Outcome: Unavailable, Detail: "dropped"},
		Classify(Observation{Source: ResultWord, Word: "skipped: no changed path is read"}),
	}
	type rank struct {
		inputs []Result
		want   RunResult
	}
	for _, tc := range []struct {
		order AggregateOrder
		ranks []rank
	}{
		{RunnerOrder, []rank{
			{[]Result{unanswered}, RunResult{Unknown, "error", 4}},
			{[]Result{stalled}, RunResult{Unknown, "stalled", 6}},
			{[]Result{fail}, RunResult{Fail, "fail", 1}},
			{[]Result{pass, skipped}, RunResult{Pass, "pass", 0}},
		}},
		{ShadowOrder, []rank{
			{[]Result{unanswered}, RunResult{Unknown, "error", 0}},
			{[]Result{fail}, RunResult{Fail, "fail", 0}},
			{[]Result{pass, stalled, notRun, skipped}, RunResult{Pass, "pass", 0}},
		}},
		{LegacyGateOrder, []rank{
			{[]Result{stalled}, RunResult{Unknown, "stalled", 6}},
			{[]Result{fail}, RunResult{Fail, "failed", 1}},
			{[]Result{unanswered, stale}, RunResult{Unknown, "unproven", 2}},
			{[]Result{pass, notRun, skipped}, RunResult{Pass, "verified", 0}},
		}},
		{LegacyReplayOrder, []rank{
			{[]Result{unanswered, stale, stalled, notRun, skipped}, RunResult{Unknown, "error", 2}},
			{[]Result{fail}, RunResult{Fail, "fail", 1}},
			{[]Result{pass}, RunResult{Pass, "pass", 0}},
		}},
		{HeavyOrder, []rank{
			{[]Result{unanswered, stale, stalled, notRun}, RunResult{Unknown, "unknown", 4}},
			{[]Result{fail}, RunResult{Fail, "red", 1}},
			{[]Result{pass, skipped}, RunResult{Pass, "green", 0}},
		}},
	} {
		t.Run(string(tc.order), func(t *testing.T) {
			assert := func(want RunResult, inputs ...Result) {
				t.Helper()
				if got := Aggregate(tc.order, inputs); got != want {
					t.Errorf("%+v: got %+v, want %+v", inputs, got, want)
				}
			}
			fallback := tc.ranks[len(tc.ranks)-1].want
			assert(fallback)
			assert(fallback, omitted...)
			for i, higher := range tc.ranks {
				for _, hi := range higher.inputs {
					assert(higher.want, hi)
					for _, lower := range tc.ranks[i:] {
						for _, lo := range lower.inputs {
							assert(higher.want, hi, lo)
							assert(higher.want, lo, hi)
						}
					}
				}
			}
		})
	}
	// F6: no answer does not mask a red, but a stall does.
	if got := Aggregate(LegacyGateOrder, []Result{fail, unanswered}); got != (RunResult{Fail, "failed", 1}) {
		t.Fatalf("legacy gate red with no answer: %+v", got)
	}
	if got := Aggregate(LegacyGateOrder, []Result{fail, unanswered, stalled}); got != (RunResult{Unknown, "stalled", 6}) {
		t.Fatalf("legacy gate red with no answer and stall: %+v", got)
	}
	if got := Aggregate("unrecognized", nil); got != (RunResult{Unknown, "error", 4}) {
		t.Fatalf("unrecognized order: %+v", got)
	}
}
