package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-item/item"
	"github.com/mschulkind-oss/tillr/checkresult"
	"github.com/mschulkind-oss/tillr/store"
)

type syncBuffer struct {
	mu  sync.Mutex
	buf bytes.Buffer
}

func (b *syncBuffer) Write(p []byte) (int, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.Write(p)
}

func (b *syncBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.buf.String()
}

func writeGreenProofJSON(t *testing.T, dir, commit string, passed []string, provedAt time.Time) {
	t.Helper()
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	raw, err := json.Marshal(map[string]any{
		"commit":    commit,
		"proved_at": provedAt.UTC().Format(time.RFC3339),
		"passed":    passed,
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "green-"+commit+".json"), raw, 0o644); err != nil {
		t.Fatal(err)
	}
}

func TestSiteD17AndD18RollingUsesClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t, "item-d17")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)

	// RollingProgram writes a gate.spans.json with a PASS span and exits 0.
	spansFile := filepath.Join(f.laneRoot, ".rolling", GateSpansFile)
	script := executable(t, `cat > "$TILLR_GATE_SPANS_OUT" <<'EOF'
{"check_spans":[{"check_id":"roll-chk","status":"PASS"}]}
EOF
exit 0`)
	steps := &ProcessSteps{
		Root:           f.root,
		LaneRoot:       f.laneRoot,
		Store:          f.storePath,
		RollingProgram: script,
		Now:            l.cfg.Now,
	}

	// Override JudgingProgram(0) -> Fail and LegacySpan("PASS") -> Fail for D17.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.JudgingProgram && o.ExitCode != nil && *o.ExitCode == 0 {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		if o.Source == checkresult.LegacySpan && o.Check == "roll-chk" {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	out, err := steps.Rolling(ctx, "", tip)
	if err != nil {
		t.Fatal(err)
	}
	if out.Green || !out.Known || !slices.Equal(out.FailingChecks, []string{"roll-chk"}) {
		t.Fatalf("Rolling(Fail override) = %+v, want Known=true FailingChecks=[roll-chk]", out)
	}

	// Exercise D18 downstream: recordRollingRun + maybeRolling + rollingUnknownStreak + repeatedRollingStalls.
	if err := l.recordRollingRun(ctx, "", tip, out); err != nil {
		t.Fatal(err)
	}
	if streak := l.rollingUnknownStreak(ctx); streak != 0 {
		t.Fatalf("rollingUnknownStreak after red = %d, want 0", streak)
	}
	// Override JudgingProgram(0) -> Unknown/DetailStalled and LegacySpan("PASS") -> Unknown/DetailStalled.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.JudgingProgram && o.ExitCode != nil && *o.ExitCode == 0 {
			return checkresult.Result{Outcome: checkresult.Unknown, Detail: checkresult.DetailStalled}
		}
		if o.Source == checkresult.LegacySpan && o.Check == "roll-chk" {
			return checkresult.Result{Outcome: checkresult.Unknown, Detail: checkresult.DetailStalled}
		}
		return checkresult.Classify(o)
	})
	stalledOut, err := steps.Rolling(ctx, "", tip)
	if err != nil {
		t.Fatal(err)
	}
	if !stalledOut.Stalled || !slices.Equal(stalledOut.FailingChecks, []string{"roll-chk"}) {
		t.Fatalf("Rolling(Stalled override) = %+v, want Stalled=true FailingChecks=[roll-chk]", stalledOut)
	}
	_ = os.Remove(spansFile)

	l.cfg.Steps = &processStepsWithStoreState{ProcessSteps: steps, storePath: f.storePath}
	var res LoopResult
	if err := l.maybeRolling(ctx, tip, true, &res); err != nil {
		t.Fatal(err)
	}
	if streak := l.rollingUnknownStreak(ctx); streak != 0 {
		t.Fatalf("rollingUnknownStreak after stall = %d, want 0", streak)
	}
	if err := l.recordRollingRun(ctx, "", tip, RollingOutcome{Known: false}); err != nil {
		t.Fatal(err)
	}
	if streak := l.rollingUnknownStreak(ctx); streak != 1 {
		t.Fatalf("rollingUnknownStreak after unknown = %d, want 1", streak)
	}
	if _, err := l.repeatedRollingStalls(ctx, "", tip, []string{"roll-chk"}); err != nil {
		t.Fatal(err)
	}
}

func TestSiteD19TriageSpanUsesClassifier(t *testing.T) {
	dir := t.TempDir()
	spansPath := filepath.Join(dir, "gate.spans.json")
	raw, err := json.Marshal(gateSpansFile{
		CheckSpans: []gateSpan{
			{CheckID: "span-pass", Status: "PASS"},
			{CheckID: "span-fail", Status: "FAIL"},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(spansPath, raw, 0o644); err != nil {
		t.Fatal(err)
	}

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.TriageSpan {
			switch o.Check {
			case "span-pass":
				return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
			case "span-fail":
				return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailPassed}
			}
		}
		return checkresult.Classify(o)
	})

	got := readFailingSpanMeta(spansPath)
	if len(got) != 1 || got[0].CheckID != "span-pass" {
		t.Fatalf("readFailingSpanMeta = %+v, want [span-pass]", got)
	}
}

func TestSiteD20HeavyStepsJudgeAndLogsUseClassifier(t *testing.T) {
	dir := t.TempDir()
	logFile := filepath.Join(dir, "chk.log")
	if err := os.WriteFile(logFile, []byte("failure details\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	// Invert HeavyCheckWord: "pass" -> Fail, "fail" -> Pass.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.HeavyCheckWord {
			switch o.Word {
			case "pass":
				return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
			case "fail":
				return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailPassed}
			}
		}
		return checkresult.Classify(o)
	})

	if got := legacyKeptLog(logFile, "pass"); got != logFile {
		t.Fatalf("legacyKeptLog(pass->Fail) = %q, want %q", got, logFile)
	}
	if got := legacyKeptLog(logFile, "fail"); got != "" {
		t.Fatalf("legacyKeptLog(fail->Pass) = %q, want empty", got)
	}

	verdictJSON, err := json.Marshal(map[string]any{
		"kind": "gate.verdict",
		"payload": map[string]any{
			"checks": []map[string]any{
				{"check_id": "c-pass", "result": "pass", "log_ref": logFile},
				{"check_id": "c-fail", "result": "fail", "log_ref": logFile},
			},
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	checks, err := decodeHeavyVerdict(verdictJSON)
	if err != nil {
		t.Fatal(err)
	}
	if checks[0].LogPath != logFile || checks[1].LogPath != "" {
		t.Fatalf("decodeHeavyVerdict LogPaths = (%q, %q), want (%q, \"\")", checks[0].LogPath, checks[1].LogPath, logFile)
	}

	// Also exercise JudgeHeavy reporting via HeavyCheckWord.
	ctx := context.WithValue(context.Background(), heavyUnownedKey{}, true)
	f, _ := oqgp7Fixture(t, "item-d20")
	if err := os.MkdirAll(filepath.Join(f.root, "tools", "gate", "checks"), 0o755); err != nil {
		t.Fatal(err)
	}
	script := executable(t, `printf '%s\n' '{"check_spans":[{"check_id":"leg-chk","status":"PASS","attributes_json":"{}"}]}' > "$TILLR_GATE_SPANS_OUT"
exit 0`)
	steps := &ProcessSteps{
		Root:           f.root,
		LaneRoot:       f.laneRoot,
		Store:          f.storePath,
		RollingProgram: script,
		Now:            time.Now,
	}
	var reportBuf bytes.Buffer
	ctx = context.WithValue(ctx, heavyProgressReportKey{}, func(line string) {
		reportBuf.WriteString(line)
	})
	if _, err := steps.JudgeHeavy(ctx, f.root, ""); err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(reportBuf.String(), "leg-chk") {
		t.Fatalf("JudgeHeavy report = %q, want leg-chk listed as failing via HeavyCheckWord override", reportBuf.String())
	}
}

func TestSiteD21RunHeavyLegacyUsesClassifier(t *testing.T) {
	ctx := context.WithValue(context.Background(), heavyUnownedKey{}, true)
	f, _ := oqgp7Fixture(t, "item-d21")
	script := executable(t, `printf '%s\n' '{"check_spans":[{"check_id":"leg-a","status":"PASS","attributes_json":"{}"}]}' > "$TILLR_GATE_SPANS_OUT"
echo "replay output"
exit 0`)
	steps := &ProcessSteps{
		Root:           f.root,
		LaneRoot:       f.laneRoot,
		Store:          f.storePath,
		RollingProgram: script,
		ReplayProgram:  script,
		Now:            time.Now,
	}
	steps.heavyReplaySelectors.Store("leg-a", "kind:leg-a")

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.BinaryProgram && o.ExitCode != nil && *o.ExitCode == 0 {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		if o.Source == checkresult.LegacySpan && o.Check == "leg-a" {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	whole, err := steps.runHeavyLegacy(ctx, f.root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(whole) != 1 || whole[0].Result != "fail" {
		t.Fatalf("runHeavyLegacy(whole) = %+v, want [Result: fail]", whole)
	}

	single, err := steps.runHeavyLegacy(ctx, f.root, "leg-a")
	if err != nil {
		t.Fatal(err)
	}
	if len(single) != 1 || single[0].Result != "fail" {
		t.Fatalf("runHeavyLegacy(single) = %+v, want [Result: fail]", single)
	}

	// Whole-program BinaryProgram(0) -> Unknown appends the legacy-runner error check.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.BinaryProgram && o.Check == "" && o.ExitCode != nil && *o.ExitCode == 0 {
			return checkresult.Result{Outcome: checkresult.Unknown, Detail: checkresult.DetailError}
		}
		return checkresult.Classify(o)
	})
	wholeUnknown, err := steps.runHeavyLegacy(ctx, f.root, "")
	if err != nil {
		t.Fatal(err)
	}
	if len(wholeUnknown) != 2 || wholeUnknown[1].CheckID != "legacy-runner" || wholeUnknown[1].Result != "error" {
		t.Fatalf("runHeavyLegacy(whole Unknown) = %+v, want legacy-runner error check", wholeUnknown)
	}
}

func TestSiteD22JudgeHeavyGoUsesClassifier(t *testing.T) {
	ctx := context.WithValue(context.Background(), heavyUnownedKey{}, true)
	root := t.TempDir()
	modDir := filepath.Join(root, "tools", "d22")
	if err := os.MkdirAll(modDir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "go.mod"), []byte("module example.com/d22\n\ngo 1.21\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(modDir, "sample_test.go"), []byte("package d22\nimport \"testing\"\nfunc TestPass(t *testing.T) {}\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	steps := &ProcessSteps{Root: root, LaneRoot: t.TempDir(), Now: time.Now}

	// Missing package path: override FocusedGoTest(PackageAbsent) -> Fail.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.FocusedGoTest && o.PackageAbsent {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: "overridden absent"}
		}
		if o.Source == checkresult.FocusedGoTest && o.Passed {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	missing, err := steps.judgeHeavyGo(ctx, root, "go-test:tools/d22:example.com/d22/missing:TestNope")
	if err != nil || len(missing) != 1 || missing[0].Result != "fail" || missing[0].Detail != "overridden absent" {
		t.Fatalf("judgeHeavyGo(missing) = %+v, err = %v; want Result=fail Detail=overridden absent", missing, err)
	}

	ran, err := steps.judgeHeavyGo(ctx, root, "go-test:tools/d22:example.com/d22:TestPass")
	if err != nil || len(ran) != 1 || ran[0].Result != "fail" {
		t.Fatalf("judgeHeavyGo(TestPass) = %+v, err = %v; want Result=fail", ran, err)
	}
}

func TestSiteD23D25D29HeavyRunnerAndJudgeTipUseClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t, "item-d23")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	dir, err := heavyDirectory(ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	runner := HeavyRunner{
		Root:  f.root,
		Lock:  loopHeld(t, f.root),
		Store: f.storePath,
		Work:  filepath.Join(f.laneRoot, ".rolling", "worktree"),
		Trunk: TrunkRef,
		Judge: func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
			return []HeavyCheck{{CheckID: "h-chk", Result: "pass"}}, nil
		},
	}

	// Override HeavyCheckWord("pass") -> Fail in judgeTip and HeavyRunner.run.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.HeavyCheckWord && o.Check == "h-chk" {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	attempt, err := runner.run(ctx, tip)
	if err != nil {
		t.Fatal(err)
	}
	if len(attempt.Runs) == 0 || attempt.Runs[0].Result != "red" {
		t.Fatalf("HeavyRunner.run Runs = %+v, want Result=red", attempt.Runs)
	}

	// Also verify HeavyRunner.run records passed checks via HeavyCheckWord -> Pass override.
	runnerPass := runner
	runnerPass.Judge = func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
		return []HeavyCheck{{CheckID: "h-chk-green", Result: "fail"}}, nil
	}
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.HeavyCheckWord && o.Check == "h-chk-green" {
			return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailPassed}
		}
		return checkresult.Classify(o)
	})
	greenAttempt, err := runnerPass.run(ctx, tip)
	if err != nil {
		t.Fatal(err)
	}
	if len(greenAttempt.Runs) == 0 || greenAttempt.Runs[0].Result != "green" {
		t.Fatalf("HeavyRunner.run(Pass override) Runs = %+v, want Result=green", greenAttempt.Runs)
	}
	greenProof, err := readGreenProof(ctx, f.root)
	if err != nil || !slices.Contains(greenProof.Passed, "h-chk-green") {
		t.Fatalf("readGreenProof = %+v, err = %v; want h-chk-green in Passed", greenProof, err)
	}

	// D25: completedHeavyRed reads HeavyRun.Result == "red" from heavy.json.
	if err := publishHeavy(dir, HeavyRun{BaseSHA: tip, TipSHA: tip, Result: "red"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := completedHeavyRed(dir, tip, tip); !ok {
		t.Fatalf("completedHeavyRed = false, want true")
	}

	// D29: revertLanding consumes GateOutcome settlement without raw check comparison.
	l.cfg.Steps = &processStepsWithStoreState{
		ProcessSteps: &ProcessSteps{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, GateProgram: executable(t, "exit 0"), Now: l.cfg.Now},
		storePath:    f.storePath,
	}
	_, _ = l.revertLanding(ctx, tip, tip, []HeavyCheck{{CheckID: "h-chk", Result: "fail"}}, nil)
}

func TestSiteD24HeavyProgressUsesClassifier(t *testing.T) {
	ctx := context.Background()
	f, _ := oqgp7Fixture(t, "item-d24")
	priorTip := runGit(t, f.root, "rev-parse", TrunkRef)
	writeForkFile(t, filepath.Join(f.root, "unrelated.txt"), "hello\n")
	runGit(t, f.root, "add", "unrelated.txt")
	runGit(t, f.root, "commit", "-qm", "advance trunk")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)

	dir := t.TempDir()
	prior := heavyProgress{
		TipSHA: priorTip,
		Checks: map[string]HeavyCheck{
			"p-chk": {CheckID: "p-chk", Result: "fail", Reads: []string{"src/"}},
		},
		PassedAt: map[string]time.Time{"p-chk": time.Now().UTC()},
	}
	if err := publishHeavyJSON(dir, "heavy-progress.json", prior); err != nil {
		t.Fatal(err)
	}

	// Override CachedRecord for p-chk ("fail") -> Pass.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.CachedRecord && o.Check == "p-chk" {
			return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailPassed}
		}
		return checkresult.Classify(o)
	})

	prog, err := loadHeavyProgress(ctx, f.root, dir, tip)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := prog.Checks["p-chk"]; !ok {
		t.Fatalf("loadHeavyProgress did not carry p-chk when CachedRecord returned Pass")
	}

	pCtx := context.WithValue(ctx, heavyProgressKey{}, prog)
	if _, ok := reuseHeavyCheck(pCtx, "p-chk"); !ok {
		t.Fatalf("reuseHeavyCheck = false, want true")
	}
	if err := saveHeavyCheck(pCtx, HeavyCheck{CheckID: "s-chk", Result: "fail"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := prog.Checks["s-chk"]; ok {
		t.Fatalf("saveHeavyCheck saved s-chk despite CachedRecord returning Fail")
	}
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.CachedRecord && o.Check == "s-chk" {
			return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailPassed}
		}
		return checkresult.Classify(o)
	})
	if err := saveHeavyCheck(pCtx, HeavyCheck{CheckID: "s-chk", Result: "fail"}); err != nil {
		t.Fatal(err)
	}
	if _, ok := prog.Checks["s-chk"]; !ok {
		t.Fatalf("saveHeavyCheck did not save s-chk when CachedRecord returned Pass")
	}
}

func TestSiteD26HeavyReplayAndOneLoggedUseClassifier(t *testing.T) {
	ctx := context.Background()
	f, _ := oqgp7Fixture(t, "item-d26")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	dir, err := heavyDirectory(ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := publishHeavyJSON(dir, heavyReplaysFile, map[string]bool{tip + " r-chk": false}); err != nil {
		t.Fatal(err)
	}

	runner := HeavyRunner{
		Root:  f.root,
		Lock:  loopHeld(t, f.root),
		Store: f.storePath,
		Work:  filepath.Join(f.laneRoot, ".rolling", "worktree"),
		Trunk: TrunkRef,
		Judge: func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
			return []HeavyCheck{{CheckID: only, Result: "pass"}}, nil
		},
	}

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.HeavyCheckWord && o.Check == "one-chk" {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		if o.Source == checkresult.CachedRecord && o.Check == "r-chk" {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})

	red, _, err := runner.oneLogged(ctx, tip, "one-chk")
	if err != nil || !red {
		t.Fatalf("oneLogged = (%v, %v), want (true, nil)", red, err)
	}
	if rRed, ok := readHeavyReplay(dir, tip, "r-chk"); !ok || !rRed {
		t.Fatalf("readHeavyReplay = (%v, %v), want (true, true)", rRed, ok)
	}
	if rRed, err := runner.replay(ctx, tip, "r-chk", "test"); err != nil || !rRed {
		t.Fatalf("replay = (%v, %v), want (true, nil)", rRed, err)
	}
}

func TestSiteD27AndD28AttributionAndEarlyUseClassifier(t *testing.T) {
	ctx := context.Background()
	f, _ := oqgp7Fixture(t, "item-d27")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	writeForkFile(t, filepath.Join(f.root, "a.txt"), "1\n")
	runGit(t, f.root, "add", "a.txt")
	runGit(t, f.root, "commit", "-qm", "second")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)

	dir, err := heavyDirectory(ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		t.Fatal(err)
	}

	check := HeavyCheck{CheckID: "attr-chk", Result: "pass", Detail: "boom", Reads: []string{"a.txt"}}
	attrs := map[string]heavyAttribution{
		"attr-chk": {Base: base, Tip: tip, FirstRed: tip, Reads: []string{"a.txt"}, Failure: heavyFailureFingerprint(check)},
	}
	if err := publishHeavyJSON(dir, heavyAttributionsFile, attrs); err != nil {
		t.Fatal(err)
	}

	runner := HeavyRunner{
		Root:  f.root,
		Lock:  loopHeld(t, f.root),
		Store: f.storePath,
		Work:  filepath.Join(f.laneRoot, ".rolling", "worktree"),
		Trunk: TrunkRef,
		Judge: func(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
			return []HeavyCheck{{CheckID: only, Result: "pass"}}, nil
		},
	}

	// D27: findFirstRed reuses prior FirstRed when HeavyCheckWord and CachedRecord return Fail.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if (o.Source == checkresult.HeavyCheckWord || o.Source == checkresult.CachedRecord) && o.Check == "attr-chk" {
			return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
		}
		return checkresult.Classify(o)
	})
	first, fault, _, err := runner.findFirstRed(ctx, HeavyRun{BaseSHA: base, TipSHA: tip}, check)
	if err != nil || fault || first != tip {
		t.Fatalf("findFirstRed = (%q, %v, %v), want (%q, false, nil)", first, fault, err, tip)
	}

	// D27: heavyEarly.offer enqueues when HeavyCheckWord returns Fail.
	early := runner.startEarly(ctx, HeavyRun{BaseSHA: base, TipSHA: tip})
	early.offer(HeavyCheck{CheckID: "attr-chk", Result: "pass", Detail: "boom", Reads: []string{"a.txt"}})
	if err := early.wait(); err != nil {
		t.Fatal(err)
	}
	if _, ok := early.handled["attr-chk"]; !ok {
		t.Fatalf("heavyEarly.offer did not handle attr-chk when HeavyCheckWord returned Fail")
	}

	// D28: forgetGreenAttributions deletes entry when HeavyCheckWord returns Pass.
	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.HeavyCheckWord && o.Check == "attr-chk" {
			return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailPassed}
		}
		return checkresult.Classify(o)
	})
	if err := runner.forgetGreenAttributions(ctx, []HeavyCheck{{CheckID: "attr-chk", Result: "fail"}}); err != nil {
		t.Fatal(err)
	}
	if got := readHeavyAttributions(dir); len(got) != 0 {
		t.Fatalf("forgetGreenAttributions left %+v, want empty", got)
	}
}

type d30HeavySteps struct {
	*ProcessSteps
}

func (s *d30HeavySteps) JudgeHeavy(ctx context.Context, dir, only string) ([]HeavyCheck, error) {
	return []HeavyCheck{{CheckID: "d30-chk", Result: "pass", Detail: "missing tool"}}, nil
}

func TestSiteD30StartHeavyUsesClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t, "item-d30")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	l.cfg.Steps = &d30HeavySteps{ProcessSteps: &ProcessSteps{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Now: l.cfg.Now}}

	var logs syncBuffer
	l.cfg.Report = &logs

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.HeavyCheckWord && o.Check == "d30-chk" {
			return checkresult.Result{Outcome: checkresult.Unavailable, Detail: checkresult.DetailUnavailable}
		}
		return checkresult.Classify(o)
	})

	if err := l.startHeavy(ctx, tip); err != nil {
		t.Fatal(err)
	}
	l.heavyWG.Wait()
	got := logs.String()
	if !strings.Contains(got, "loop: heavy d30-chk unavailable") {
		t.Fatalf("startHeavy report = %q, want unavailable message for d30-chk", got)
	}
}

func TestSiteD31D32D33ProofsAndReleaseUseClassifier(t *testing.T) {
	ctx := context.Background()
	f, l := oqgp7Fixture(t, "item-d31")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	runGit(t, f.root, "update-ref", "refs/tillr/green", tip)
	dir, err := heavyDirectory(ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	now := l.cfg.Now().UTC().Add(time.Minute)
	writeGreenProofJSON(t, dir, tip, []string{"chk-green"}, now)
	if err := publishHeavyJSON(dir, "heavy-progress.json", heavyProgress{
		TipSHA:   tip,
		Checks:   map[string]HeavyCheck{"chk-prog": {CheckID: "chk-prog", Result: "fail"}},
		PassedAt: map[string]time.Time{"chk-prog": now},
	}); err != nil {
		t.Fatal(err)
	}
	if err := publishHeavyJSON(dir, "heavy-legacy-progress.json", map[string]any{
		"tip_sha": tip,
		"checks":  map[string]any{"chk-legacy": map[string]string{"proved_at": now.Format(time.RFC3339)}},
	}); err != nil {
		t.Fatal(err)
	}

	withClassifier(t, func(o checkresult.Observation) checkresult.Result {
		if o.Source == checkresult.CachedRecord {
			switch o.Check {
			case "chk-prog":
				return checkresult.Result{Outcome: checkresult.Pass, Detail: checkresult.DetailPassed}
			case "chk-green", "chk-legacy":
				return checkresult.Result{Outcome: checkresult.Fail, Detail: checkresult.DetailFailed}
			}
		}
		return checkresult.Classify(o)
	})

	proofs, err := readCheckProofs(ctx, f.root)
	if err != nil {
		t.Fatal(err)
	}
	if len(proofs) != 2 {
		t.Fatalf("readCheckProofs len = %d, want 2", len(proofs))
	}
	if proofs[0].passed([]string{"chk-green"}, 0) {
		t.Fatalf("chk-green should not be proved when CachedRecord returned Fail")
	}
	if !proofs[1].passed([]string{"chk-prog"}, 0) {
		t.Fatalf("chk-prog should be proved when CachedRecord returned Pass")
	}
	if proofs[1].passed([]string{"chk-legacy"}, 0) {
		t.Fatalf("chk-legacy should not be proved when CachedRecord returned Fail")
	}

	// D33: obstacleProofPassed uses checkProof populated from CachedRecord.
	if !obstacleProofPassed(proofs[1], []string{"chk-prog"}, 0) {
		t.Fatalf("obstacleProofPassed = false, want true")
	}

	// D32 & D33: resolveProvedFixers resolves open fixer for chk-prog, and repairedTrunkWaiter releases waiter.
	fixerID, err := l.routeTrunkFixer(ctx, tip, []string{"chk-prog"}, GateOutcome{}, "", "item-d31")
	if err != nil || fixerID == "" {
		t.Fatalf("routeTrunkFixer = (%q, %v)", fixerID, err)
	}
	if err := (&processStepsWithStoreState{storePath: f.storePath}).ItemState(ctx, "item-d31", "", StateInQueue, item.WaitingOn{Waiter: "item:" + fixerID, Reason: RefusalTrunkAlreadyRed + ": chk-prog"}); err != nil {
		t.Fatal(err)
	}
	if err := l.resolveProvedFixers(ctx); err != nil {
		t.Fatal(err)
	}
	s, err := store.OpenRead(ctx, f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var released string
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		released, err = l.repairedTrunkWaiter(ctx, r, "item-d31", fixerID, StateInQueue, false)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if released != StateInQueue {
		t.Fatalf("repairedTrunkWaiter = %q, want %q", released, StateInQueue)
	}
}
