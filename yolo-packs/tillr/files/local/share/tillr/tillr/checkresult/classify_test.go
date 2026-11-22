package checkresult

import (
	"encoding/json"
	"strings"
	"testing"
)

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

func TestClassifyRulesOneToFour(t *testing.T) {
	exit := func(code int) *int { return &code }
	const (
		fp     = "aabbccddeeff00112233445566778899aabbccddeeff00112233445566778899"
		tPass  = "2026-11-22T10:00:00.123456789Z"
		tFail  = "2026-11-22T10:05:00.987654321Z"
		tEn    = "2026-11-22T10:10:00Z"
		tAfter = "2026-11-22T10:15:00Z"
		tFinal = "2026-11-22T10:20:00Z"
	)
	ep := FormatEpisode(fp, tPass, tFail)
	marker := &DisabledMarker{
		CheckID:     "go-build",
		Episode:     ep,
		Fingerprint: fp,
		PassAt:      tPass,
		PassLogRef:  "/store/disabled/pass.log",
		FailAt:      tFail,
		FailLogRef:  "",
		RepairItem:  "flaky-go-build",
	}

	// Rule 1: proving skips rules 2 and 3.
	assertResult(t, Classify(Observation{
		CheckID:     "go-build",
		Proving:     true,
		Fingerprint: fp,
		Marker:      marker,
		StoredPass:  &StoredRecord{At: tPass, LogRef: "/logs/pass"},
		StoredFail:  &StoredRecord{At: tFail, LogRef: "/logs/fail"},
		Source:      CheckCommand,
		ExitCode:    exit(1),
	}), Result{Outcome: Fail, Detail: DetailFailed, CheckID: "go-build"})

	assertResult(t, Classify(Observation{
		CheckID:     "go-build",
		Proving:     true,
		Fingerprint: fp,
		Marker:      marker,
		Source:      CheckCommand,
		ExitCode:    exit(0),
	}), Result{Outcome: Pass, Detail: DetailPassed, CheckID: "go-build"})

	assertResult(t, Classify(Observation{
		CheckID: "go-build",
		Proving: true,
		Marker:  marker,
	}), Result{Outcome: Unknown, Detail: DetailUnobserved, CheckID: "go-build"})

	// Rule 2: active marker disables with marker evidence regardless of raw result or fingerprint.
	wantMarker := Result{
		Outcome:     Disabled,
		Detail:      DetailDisabled,
		CheckID:     "go-build",
		Episode:     ep,
		Fingerprint: fp,
		PassAt:      tPass,
		PassLogRef:  "/store/disabled/pass.log",
		FailAt:      tFail,
		FailLogRef:  "",
		RepairItem:  "flaky-go-build",
	}
	assertResult(t, Classify(Observation{
		Check:       "go-build:compile",
		Fingerprint: "different-fingerprint",
		Marker:      marker,
	}), wantMarker)

	wantMarkerPass := wantMarker
	wantMarkerPass.RawDetail = "ok\n"
	assertResult(t, Classify(Observation{
		CheckID:     "go-build",
		Fingerprint: "different-fingerprint",
		Marker:      marker,
		Source:      CheckCommand,
		ExitCode:    exit(0),
		Detail:      "ok\n",
	}), wantMarkerPass)

	// Rule 3: stored pass + stored fail (pre-run query, unrecorded fail log).
	assertResult(t, Classify(Observation{
		CheckID:     "go-build",
		Fingerprint: fp,
		StoredPass:  &StoredRecord{At: tPass, LogRef: "/logs/p.log"},
		StoredFail:  &StoredRecord{At: tFail},
	}), Result{
		Outcome:     Disabled,
		Detail:      DetailDisabled,
		CheckID:     "go-build",
		Episode:     ep,
		Fingerprint: fp,
		PassAt:      tPass,
		PassLogRef:  "/logs/p.log",
		FailAt:      tFail,
		FailLogRef:  "",
	})

	// Rule 3: stored pass + fresh fail observation.
	assertResult(t, Classify(Observation{
		CheckID:     "go-build",
		Fingerprint: fp,
		ObservedAt:  tFail,
		LogRef:      "/logs/fresh-fail.log",
		StoredPass:  &StoredRecord{At: tPass},
		Source:      CheckCommand,
		ExitCode:    exit(1),
	}), Result{
		Outcome:     Disabled,
		Detail:      DetailDisabled,
		CheckID:     "go-build",
		Episode:     ep,
		Fingerprint: fp,
		PassAt:      tPass,
		PassLogRef:  "",
		FailAt:      tFail,
		FailLogRef:  "/logs/fresh-fail.log",
	})

	// Rule 3: stored fail + fresh pass observation.
	assertResult(t, Classify(Observation{
		CheckID:     "go-build",
		Fingerprint: fp,
		ObservedAt:  tPass,
		LogRef:      "/logs/fresh-pass.log",
		StoredFail:  &StoredRecord{At: tFail, LogRef: "/logs/stored-fail.log"},
		Source:      CheckCommand,
		ExitCode:    exit(0),
	}), Result{
		Outcome:     Disabled,
		Detail:      DetailDisabled,
		CheckID:     "go-build",
		Episode:     ep,
		Fingerprint: fp,
		PassAt:      tPass,
		PassLogRef:  "/logs/fresh-pass.log",
		FailAt:      tFail,
		FailLogRef:  "/logs/stored-fail.log",
	})

	// Non-verdict (exit 4 -> unknown/error) against stored pass does not contradict.
	assertResult(t, Classify(Observation{
		CheckID:     "go-build",
		Fingerprint: fp,
		ObservedAt:  tFail,
		StoredPass:  &StoredRecord{At: tPass},
		Source:      CheckCommand,
		ExitCode:    exit(4),
	}), Result{Outcome: Unknown, Detail: DetailError, CheckID: "go-build"})

	// Resolved episode in enabled/ does not re-disable on the resolved pair or on a stale cached proof.
	assertResult(t, Classify(Observation{
		CheckID:          "go-build",
		Fingerprint:      fp,
		StoredPass:       &StoredRecord{At: tPass},
		StoredFail:       &StoredRecord{At: tFail},
		ResolvedEpisodes: []string{ep},
		EnabledAt:        tEn,
	}), Result{Outcome: Unknown, Detail: DetailUnobserved, CheckID: "go-build"})

	assertResult(t, Classify(Observation{
		CheckID:          "go-build",
		Fingerprint:      fp,
		StoredFail:       &StoredRecord{At: tAfter, LogRef: "/logs/post-fail.log"},
		ResolvedEpisodes: []string{ep},
		EnabledAt:        tEn,
		Source:           CachedRecord,
		Word:             "pass",
	}), Result{Outcome: Pass, Detail: DetailCached, RawWord: "pass", CheckID: "go-build"})

	// A brand-new fresh contradiction after repair DOES disable with a new episode.
	newEp := FormatEpisode(fp, tAfter, tFinal)
	assertResult(t, Classify(Observation{
		CheckID:          "go-build",
		Fingerprint:      fp,
		ObservedAt:       tFinal,
		LogRef:           "/logs/new-fail.log",
		StoredPass:       &StoredRecord{At: tAfter, LogRef: "/logs/new-pass.log"},
		ResolvedEpisodes: []string{ep},
		EnabledAt:        tEn,
		Source:           CheckCommand,
		ExitCode:         exit(1),
	}), Result{
		Outcome:     Disabled,
		Detail:      DetailDisabled,
		CheckID:     "go-build",
		Episode:     newEp,
		Fingerprint: fp,
		PassAt:      tAfter,
		PassLogRef:  "/logs/new-pass.log",
		FailAt:      tFinal,
		FailLogRef:  "/logs/new-fail.log",
	})

	// Rule 4: pre-run query on an enabled check with no raw result.
	assertResult(t, Classify(Observation{
		Check:       "go-build",
		CheckID:     "go-build",
		Fingerprint: fp,
		StoredPass:  &StoredRecord{At: tPass},
	}), Result{Outcome: Unknown, Detail: DetailUnobserved, CheckID: "go-build"})
}

func TestParseEpisode(t *testing.T) {
	fp, p, f, err := ParseEpisode("fp-1|2026-11-22T10:00:00Z|2026-11-22T10:01:02.123456789Z")
	if err != nil || fp != "fp-1" || p != "2026-11-22T10:00:00Z" || f != "2026-11-22T10:01:02.123456789Z" {
		t.Fatalf("ParseEpisode valid: (%q, %q, %q, %v)", fp, p, f, err)
	}
	for _, bad := range []string{
		"",
		"fp-1|2026-11-22T10:00:00Z",
		"fp-1|2026-11-22T10:00:00Z|2026-11-22T10:01:00Z|extra",
		"|2026-11-22T10:00:00Z|2026-11-22T10:01:00Z",
		"fp/1|2026-11-22T10:00:00Z|2026-11-22T10:01:00Z",
		" fp-1|2026-11-22T10:00:00Z|2026-11-22T10:01:00Z",
		"fp-1|2026-11-22T05:00:00-05:00|2026-11-22T10:01:00Z",
		"fp-1|2026-11-22T10:00:00Z|2026-11-22T10:01:00+00:00",
		"fp-1|not-a-time|2026-11-22T10:01:00Z",
		"fp-1|2026-11-22T10:00:00Z|not-a-time",
	} {
		if _, _, _, err := ParseEpisode(bad); err == nil {
			t.Errorf("ParseEpisode(%q) succeeded; want error", bad)
		}
	}
}

func TestDisabledResultJSONAndWorktreeUnmarshal(t *testing.T) {
	disabled := Result{
		Outcome:     Disabled,
		Detail:      DetailDisabled,
		CheckID:     "go-build",
		Episode:     "fp|2026-11-22T10:00:00Z|2026-11-22T10:01:00Z",
		Fingerprint: "fp",
		PassAt:      "2026-11-22T10:00:00Z",
		PassLogRef:  "",
		FailAt:      "2026-11-22T10:01:00Z",
		FailLogRef:  "",
	}
	raw, err := json.Marshal(disabled)
	if err != nil {
		t.Fatal(err)
	}
	encoded := string(raw)
	for _, want := range []string{`"pass_log_ref":""`, `"fail_log_ref":""`, `"episode":"fp|2026-11-22T10:00:00Z|2026-11-22T10:01:00Z"`} {
		if !strings.Contains(encoded, want) {
			t.Fatalf("disabled JSON %s missing %s", encoded, want)
		}
	}
	plainRaw, err := json.Marshal(Result{Outcome: Disabled, Detail: DetailDisabled, RawWord: "disabled"})
	if err != nil {
		t.Fatal(err)
	}
	if string(plainRaw) != `{"outcome":"disabled","detail":"disabled","raw_word":"disabled"}` {
		t.Fatalf("plain disabled JSON = %s", plainRaw)
	}

	for input, want := range map[string]bool{
		`{"worktree":true}`:  true,
		`{"worktree":"."}`:   true,
		`{"worktree":false}`: false,
		`{"worktree":""}`:    false,
		`{}`:                 false,
	} {
		var obs Observation
		if err := json.Unmarshal([]byte(input), &obs); err != nil {
			t.Fatalf("unmarshal %s: %v", input, err)
		}
		if obs.WorktreeEnabled() != want {
			t.Fatalf("%s: WorktreeEnabled() = %v, want %v", input, obs.WorktreeEnabled(), want)
		}
	}
	var bad Observation
	if err := json.Unmarshal([]byte(`{"worktree":123}`), &bad); err == nil {
		t.Fatal("expected numeric worktree to fail unmarshal")
	}
}

func TestAggregateDisabledExclusion(t *testing.T) {
	disabledOne := Result{
		Outcome:     Disabled,
		Detail:      DetailDisabled,
		CheckID:     "flaky-a",
		Episode:     "fp1|2026-11-22T10:00:00Z|2026-11-22T10:01:00Z",
		Fingerprint: "fp1",
		PassAt:      "2026-11-22T10:00:00Z",
		FailAt:      "2026-11-22T10:01:00Z",
	}
	disabledTwo := Result{Outcome: Disabled, Detail: DetailDisabled, CheckID: "flaky-b"}
	pass := Result{Outcome: Pass, Detail: DetailPassed}
	fail := Result{Outcome: Fail, Detail: DetailFailed}
	stalled := Result{Outcome: Unknown, Detail: DetailStalled}
	errored := Result{Outcome: Unknown, Detail: DetailError}

	for _, tc := range []struct {
		order    AggregateOrder
		wantPass RunResult
	}{
		{RunnerOrder, RunResult{Pass, "pass", 0}},
		{ShadowOrder, RunResult{Pass, "pass", 0}},
		{LegacyGateOrder, RunResult{Pass, "verified", 0}},
		{LegacyReplayOrder, RunResult{Pass, "pass", 0}},
		{HeavyOrder, RunResult{Pass, "green", 0}},
	} {
		t.Run(string(tc.order), func(t *testing.T) {
			// All-disabled runs (0 passed, 1+ disabled) pass in every order.
			if got := Aggregate(tc.order, []Result{disabledOne}); got != tc.wantPass {
				t.Fatalf("1 disabled: got %+v, want %+v", got, tc.wantPass)
			}
			if got := Aggregate(tc.order, []Result{disabledOne, disabledTwo}); got != tc.wantPass {
				t.Fatalf("2 disabled: got %+v, want %+v", got, tc.wantPass)
			}
			for _, base := range [][]Result{
				{pass},
				{fail},
				{stalled},
				{errored},
				{pass, fail},
			} {
				want := Aggregate(tc.order, base)
				withDisabled := append(append([]Result{disabledOne}, base...), disabledTwo)
				if got := Aggregate(tc.order, withDisabled); got != want {
					t.Fatalf("with disabled around %+v: got %+v, want %+v", base, got, want)
				}
			}
		})
	}
}
