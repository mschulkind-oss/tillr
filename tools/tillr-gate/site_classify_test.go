package main

import (
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/record"
)

func withClassifier(t *testing.T, fn func(checkresult.Observation) checkresult.Result) {
	t.Helper()
	prev := classifyCheck
	classifyCheck = fn
	t.Cleanup(func() { classifyCheck = prev })
}

func withAggregator(t *testing.T, fn func(checkresult.AggregateOrder, []checkresult.Result) checkresult.RunResult) {
	t.Helper()
	prev := aggregateChecks
	aggregateChecks = fn
	t.Cleanup(func() { aggregateChecks = prev })
}

func writeVerdictFile(t *testing.T, path string, payload VerdictPayload) {
	t.Helper()
	env, err := encodeVerdict(payload, record.Envelope{})
	if err != nil {
		t.Fatal(err)
	}
	raw, err := encodeVerdictRecord(env)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSiteG1RunCheckUsesClassifier(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(t.TempDir(), "logs")
	if err := os.MkdirAll(logs, 0o755); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct {
		name string
		cmd  []string
		want string
	}{
		{name: "exit 0 pass", cmd: []string{"sh", "-c", "exit 0"}, want: ResultPass},
		{name: "exit 1 fail", cmd: []string{"sh", "-c", "exit 1"}, want: ResultFail},
		{name: "exit 4 error", cmd: []string{"sh", "-c", "exit 4"}, want: ResultError},
		{name: "exit 6 fail without silent samples", cmd: []string{"sh", "-c", "exit 6"}, want: ResultFail},
		{name: "exit 77 fail on check command", cmd: []string{"sh", "-c", "exit 77"}, want: ResultFail},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := runCheck(PlanCheck{CheckID: "g1", Command: tc.cmd}, root, logs, 0)
			if got.Result != tc.want {
				t.Fatalf("got %q, want %q", got.Result, tc.want)
			}
		})
	}

	var sources []checkresult.Source
	withClassifier(t, func(obs checkresult.Observation) checkresult.Result {
		sources = append(sources, obs.Source)
		if obs.Source == checkresult.CheckCommand && obs.ExitCode != nil && *obs.ExitCode == 0 {
			return checkresult.Result{Outcome: checkresult.Unknown, Detail: checkresult.DetailStalled}
		}
		return checkresult.Classify(obs)
	})
	got := runCheck(PlanCheck{CheckID: "g1-override", Command: []string{"sh", "-c", "exit 0"}}, root, logs, 1)
	if got.Result != ResultStalled {
		t.Fatalf("runCheck ignored classifier override: got %q, want %q", got.Result, ResultStalled)
	}
	if !slices.Contains(sources, checkresult.CheckCommand) {
		t.Fatalf("expected CheckCommand observation, saw %v", sources)
	}
	logBytes, err := os.ReadFile(got.LogRef)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(logBytes), "stalled after") {
		t.Fatalf("expected stall note in log, got %q", string(logBytes))
	}
}

func TestSiteG2TreeMemoryUsesClassifier(t *testing.T) {
	storeDir := filepath.Join(t.TempDir(), "store")
	if err := os.MkdirAll(storeDir, 0o755); err != nil {
		t.Fatal(err)
	}
	store := resultStore{dir: storeDir}

	var sawCached bool
	withClassifier(t, func(obs checkresult.Observation) checkresult.Result {
		if obs.Source == checkresult.CachedRecord && obs.Check == "g2-check" {
			sawCached = true
			return checkresult.Result{Outcome: checkresult.Disabled, Detail: checkresult.DetailDisabled}
		}
		return checkresult.Classify(obs)
	})
	if err := store.publish("k", storedResult{Result: ResultPass, CheckID: "g2-check", Tree: "t", Time: "2026-04-17T00:00:00Z"}); err == nil {
		t.Fatal("expected publish to reject result when classifier overrides CachedRecord to Disabled")
	}
	if !sawCached {
		t.Fatal("expected publish to consult classifyCheck with CachedRecord")
	}

	// Fresh run on a clean git tree: if ResultWord classifies outcome.Result as Unknown/error,
	// (*treeMemory).run must not publish a .pass.json record to the result store.
	root, tree := memoryRepo(t)
	marker := filepath.Join(t.TempDir(), "ran")
	check := memoryCheck(marker)
	withClassifier(t, func(obs checkresult.Observation) checkresult.Result {
		if obs.Source == checkresult.ResultWord && obs.Check == check.CheckID {
			return checkresult.Result{Outcome: checkresult.Unknown, Detail: checkresult.DetailError}
		}
		return checkresult.Classify(obs)
	})
	outcome, _ := memoryRun(t, root, tree, check)
	if outcome.Result != ResultPass {
		t.Fatalf("expected runCheck outcome pass before ResultWord publish gate, got %q", outcome.Result)
	}
	if got := memoryFiles(t, memoryStore(t, root)); len(got) != 0 {
		t.Fatalf("expected no stored record when ResultWord classified as Unknown, found %d files", len(got))
	}
}

func TestSiteG3AnnotateMemoryUsesClassifier(t *testing.T) {
	withClassifier(t, func(obs checkresult.Observation) checkresult.Result {
		switch {
		case obs.Source == checkresult.CachedRecord && obs.Word == "cached-green":
			return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailCached}
		case obs.Source == checkresult.CachedRecord && obs.Word == "cached-red":
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		case obs.Source == checkresult.ResultWord && obs.Word == "fresh-green":
			return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailPassed}
		default:
			return checkresult.Classify(obs)
		}
	})
	outcome := VerdictCheck{CheckID: "g3", Result: "fresh-green"}
	annotateMemory(
		&outcome,
		&storedResult{CheckID: "g3", Result: "cached-green", Tree: "t", Time: "2026-04-17T01:00:00Z"},
		&storedResult{CheckID: "g3", Result: "cached-red", Tree: "t", Time: "2026-04-17T02:00:00Z"},
		false,
	)
	if outcome.Memory != "flaky" {
		t.Fatalf("annotateMemory ignored classifier: got Memory=%q, want flaky", outcome.Memory)
	}
}

func TestSiteG4RunWithUsesRunnerOrderAggregate(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(t.TempDir(), "logs")
	plan := planEnvelope(t, PlanPayload{
		PlanID:         "plan-g4",
		BaseSHA:        "base",
		RegistryDigest: "sha256:g4",
		TreeSHA:        "tree-g4",
		Checks: []PlanCheck{
			{CheckID: "c1", Scope: "change", Command: []string{"sh", "-c", "exit 0"}},
		},
	})

	var calledRunnerOrder bool
	withAggregator(t, func(order checkresult.AggregateOrder, results []checkresult.Result) checkresult.RunResult {
		if order == checkresult.RunnerOrder {
			calledRunnerOrder = true
			return checkresult.RunResult{
				Outcome:  checkresult.Unknown,
				Detail:   checkresult.DetailStalled,
				ExitCode: ExitStalled,
			}
		}
		return checkresult.Aggregate(order, results)
	})

	env, err := RunWith(plan, root, logs, RunOptions{})
	if !calledRunnerOrder {
		t.Fatal("RunWith did not call aggregateChecks with RunnerOrder")
	}
	var verdict VerdictPayload
	if decErr := json.Unmarshal(env.Payload, &verdict); decErr != nil {
		t.Fatal(decErr)
	}
	if verdict.Result != ResultStalled {
		t.Fatalf("got verdict.Result=%q, want %q", verdict.Result, ResultStalled)
	}
	if exitCode(err) != ExitStalled {
		t.Fatalf("got exit code %d, want %d (err=%v)", exitCode(err), ExitStalled, err)
	}
}

func TestSiteG5GateProgramUsesClassifier(t *testing.T) {
	root := t.TempDir()
	logs := filepath.Join(t.TempDir(), "logs")
	script := filepath.Join(t.TempDir(), "gate.sh")
	if err := os.WriteFile(script, []byte("#!/bin/sh\nexit 0\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	plan := planEnvelope(t, PlanPayload{
		PlanID:         "plan-g5",
		BaseSHA:        "base",
		TreeSHA:        "tree-g5",
		RegistryDigest: "sha256:g5",
	})

	var sawJudging bool
	withClassifier(t, func(obs checkresult.Observation) checkresult.Result {
		if obs.Source == checkresult.JudgingProgram {
			sawJudging = true
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(obs)
	})

	if got := gateResult(0); got != ResultFail {
		t.Fatalf("gateResult(0) ignored classifier: got %q, want %q", got, ResultFail)
	}

	env, err := RunGate(plan, root, logs, GateProgram{Command: []string{script}})
	if !sawJudging {
		t.Fatal("RunGate did not call classifyCheck with JudgingProgram")
	}
	var verdict VerdictPayload
	if decErr := json.Unmarshal(env.Payload, &verdict); decErr != nil {
		t.Fatal(decErr)
	}
	if verdict.Result != ResultFail || exitCode(err) != record.ExitNegative {
		t.Fatalf("RunGate ignored classifier: result=%q exit=%d err=%v", verdict.Result, exitCode(err), err)
	}
}

func TestSiteG6ReadFastVerdictUsesClassifier(t *testing.T) {
	path := filepath.Join(t.TempDir(), "fast.verdict.json")
	writeVerdictFile(t, path, VerdictPayload{
		VerdictID: "v-g6",
		PlanID:    "plan-g6",
		TreeSHA:   "tree-g6",
		Result:    ResultStalled,
		Checks: []VerdictCheck{
			{
				CheckID:       "slow-check",
				Result:        "synthetic-stall",
				SilentSamples: WatchdogSilentSamples,
			},
		},
	})

	if _, err := readFastVerdict(path); err == nil {
		t.Fatal("expected readFastVerdict to reject unclassified synthetic-stall word")
	}

	withClassifier(t, func(obs checkresult.Observation) checkresult.Result {
		if obs.Source == checkresult.ResultWord && obs.Word == "synthetic-stall" {
			return checkresult.Result{Outcome: checkresult.Unknown, Detail: checkresult.DetailStalled}
		}
		return checkresult.Classify(obs)
	})
	got, err := readFastVerdict(path)
	if err != nil {
		t.Fatalf("readFastVerdict failed under classifier override: %v", err)
	}
	if len(got.Checks) != 1 || got.Checks[0].CheckID != "slow-check" {
		t.Fatalf("unexpected fast verdict payload: %+v", got)
	}
}

func TestSiteG7RunSummaryUsesClassifier(t *testing.T) {
	withClassifier(t, func(obs checkresult.Observation) checkresult.Result {
		if obs.Word == ResultInherited {
			t.Fatalf("settlement %q must not be passed to classifyCheck", obs.Word)
		}
		switch obs.Word {
		case "word-rem":
			return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailRemembered}
		case "word-pass":
			return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailPassed}
		case "word-unavail":
			return checkresult.Result{Outcome: checkresult.Unavailable, Detail: checkresult.DetailUnavailable}
		case "word-skip":
			return checkresult.Result{Outcome: checkresult.Unavailable, Detail: checkresult.DetailSkipped}
		case "word-fail":
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		default:
			return checkresult.Classify(obs)
		}
	})

	verdict := VerdictPayload{
		TreeSHA: "tree-g7",
		Checks: []VerdictCheck{
			{CheckID: "c-rem", Result: "word-pass", Memory: "word-rem", PassTime: "2026-04-17T00:00:00Z"},
			{CheckID: "c-unavail", Result: "word-unavail"},
			{CheckID: "c-inh", Result: ResultInherited},
			{CheckID: "c-skip", Result: "word-skip"},
			{CheckID: "c-fail", Result: "word-fail"},
		},
	}
	var buf bytes.Buffer
	writeRunSummary(&buf, verdict)
	out := buf.String()
	wantLine := "checks: 0 passed, 1 remembered, 1 unavailable, 1 inherited, 1 skipped, 1 failed"
	if !strings.Contains(out, wantLine) {
		t.Fatalf("summary output %q missing %q", out, wantLine)
	}
	if !strings.Contains(out, "c-rem: remembered pass at 2026-04-17T00:00:00Z") {
		t.Fatalf("summary output %q missing remembered timestamp line", out)
	}
}

func TestSiteG8ChecksFaceSkipUsesClassifier(t *testing.T) {
	repo, _ := memoryRepo(t)
	baseSHA := memoryGit(t, repo, "rev-parse", "HEAD")
	memoryWrite(t, filepath.Join(repo, "outside"), "two\n")
	memoryWrite(t, filepath.Join(repo, "checks/unread.json"),
		`{"id":"unread-check","label":"unread","scope":"tree","tier":"fast","budget_ms":5000,"remember":false,"command":["sh","-c","exit 0"],"reads":["input/"]}`)
	memoryGit(t, repo, "add", ".")
	memoryGit(t, repo, "-c", "user.name=Fixture", "-c", "user.email=fixture@example.invalid", "commit", "-qm", "touch outside")

	logsDir := filepath.Join(t.TempDir(), "logs")

	withClassifier(t, func(obs checkresult.Observation) checkresult.Result {
		if obs.Source == checkresult.ResultWord && obs.Word == ResultSkipped {
			return checkresult.Result{Outcome: checkresult.Disabled, Detail: checkresult.DetailDisabled}
		}
		return checkresult.Classify(obs)
	})

	prevStdout := os.Stdout
	rPipe, wPipe, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	os.Stdout = wPipe
	code := checksFace([]string{
		"--fast-tree",
		"--base", baseSHA,
		"--head", "HEAD",
		"--worktree", repo,
		"--checks", "checks",
		"--logs", logsDir,
		"--scope-to-change",
	})
	_ = wPipe.Close()
	os.Stdout = prevStdout
	var outBuf bytes.Buffer
	if _, err := outBuf.ReadFrom(rPipe); err != nil {
		t.Fatal(err)
	}
	_ = rPipe.Close()
	if code != record.ExitOK {
		t.Fatalf("checksFace exited %d", code)
	}
	raw := outBuf.Bytes()
	env, err := record.Decode(raw)
	if err != nil {
		t.Fatal(err)
	}
	var payload VerdictPayload
	if err := json.Unmarshal(env.Payload, &payload); err != nil {
		t.Fatal(err)
	}
	if payload.Result != ResultPass {
		t.Fatalf("got payload.Result=%q, want %q", payload.Result, ResultPass)
	}
	if len(payload.Checks) != 1 || payload.Checks[0].Result != ResultDisabled {
		t.Fatalf("expected skipped check to take classifier answer %q, got %+v", ResultDisabled, payload.Checks)
	}
}

func TestSiteG9ApplyInheritedFailuresUsesClassifier(t *testing.T) {
	withClassifier(t, func(obs checkresult.Observation) checkresult.Result {
		if obs.Word == ResultInherited || obs.Word == ResultPassInherited {
			t.Fatalf("settlement %q must not be passed to classifyCheck", obs.Word)
		}
		if obs.Source == checkresult.ResultWord && obs.Word == "custom-red" {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(obs)
	})

	alreadySettled := VerdictPayload{
		Result: ResultPassInherited,
		Checks: []VerdictCheck{{CheckID: "c1", Result: ResultInherited}},
	}
	if _, ok := ApplyInheritedFailures(alreadySettled, []InheritedFailure{{CheckID: "c1"}}); ok {
		t.Fatal("expected already-settled verdict to remain unchanged without calling classifier")
	}

	verdict := VerdictPayload{
		TreeSHA: "tree-g9",
		Result:  "custom-red",
		Checks: []VerdictCheck{
			{CheckID: "c-inh", Result: ResultInherited},
			{CheckID: "c-red", Result: "custom-red"},
		},
	}
	got, promoted := ApplyInheritedFailures(verdict, []InheritedFailure{{CheckID: "c-red", TipSHA: "trunk"}})
	if !promoted || got.Result != ResultPassInherited || got.Checks[1].Result != ResultInherited {
		t.Fatalf("ApplyInheritedFailures ignored classifier: promoted=%v got=%+v", promoted, got)
	}
}

func TestSiteG10ShadowUsesShadowOrderAggregate(t *testing.T) {
	var sawShadowOrder bool
	withAggregator(t, func(order checkresult.AggregateOrder, results []checkresult.Result) checkresult.RunResult {
		if order == checkresult.ShadowOrder {
			sawShadowOrder = true
			if len(results) != 1 {
				t.Fatalf("expected only Ran check in ShadowOrder slice, got %d", len(results))
			}
			return checkresult.RunResult{Outcome: checkresult.Fail, Detail: ResultFail, ExitCode: 1}
		}
		return checkresult.Aggregate(order, results)
	})

	got := shadowResult([]ShadowCheck{
		{CheckID: "ran-pass", Ran: true, Result: ResultPass},
		{CheckID: "not-ran", Ran: false, Result: ResultError},
	})
	if !sawShadowOrder || got != ResultFail {
		t.Fatalf("shadowResult ignored ShadowOrder aggregate: saw=%v got=%q", sawShadowOrder, got)
	}
}

func TestSiteG12BudgetNotesUsesBudgetNoteClassifier(t *testing.T) {
	var sawBudgetNote bool
	withClassifier(t, func(obs checkresult.Observation) checkresult.Result {
		if obs.Source == checkresult.BudgetNote {
			sawBudgetNote = true
			if obs.Word == "custom-defer" {
				return checkresult.Result{Outcome: checkresult.Unknown, Detail: checkresult.DetailOverBudget}
			}
		}
		return checkresult.Classify(obs)
	})

	notes := budgetNotes(VerdictPayload{
		TreeSHA: "tree-g12",
		Checks: []VerdictCheck{
			{CheckID: "c-defer", Result: "custom-defer", DurationMS: 250},
		},
	})
	if !sawBudgetNote {
		t.Fatal("budgetNotes did not call classifyCheck with BudgetNote")
	}
	want := "c-defer: over-budget after 250 ms; stopped, judgment deferred to the heavy tier"
	if !slices.Equal(notes, []string{want}) {
		t.Fatalf("got notes %v, want [%q]", notes, want)
	}
}
