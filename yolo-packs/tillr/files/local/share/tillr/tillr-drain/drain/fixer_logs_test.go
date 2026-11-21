package drain

import (
	"context"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
)

func stageTrunkFixerForTest(t *testing.T, root, laneRoot, storePath, fixerID, baseSHA, runID string) (laneBriefPath, runBriefPath, brief string) {
	t.Helper()
	stager := &LaneStager{
		Root:     root,
		LaneRoot: laneRoot,
		Store:    storePath,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
	}
	spec := StageSpec{
		ItemID:     fixerID,
		RunID:      runID,
		LaneID:     "lane-fixer",
		WorkingDir: WorktreeDir,
		Backend:    "fixture",
		BaseSHA:    baseSHA,
	}
	if err := stager.Stage(context.Background(), spec); err != nil {
		t.Fatalf("Stage(%s): %v", fixerID, err)
	}
	laneBriefPath = filepath.Join(laneRoot, "lane-fixer", BriefFile)
	runBriefPath = filepath.Join(RunDir(root, runID), BriefFile)
	raw, err := os.ReadFile(runBriefPath)
	if err != nil {
		t.Fatalf("reading staged brief: %v", err)
	}
	return laneBriefPath, runBriefPath, string(raw)
}

func TestDeclarationHeavyProofGivesTrunkFixerTriageAndRunLocalLogCopy(t *testing.T) {
	h := newRevertHarness(t, nil)
	h.loop.gatePolicy.AutoRevert = false
	h.land("item-later", "c", "broken\n")
	tip := heavyGit(t, h.root, "rev-parse", "HEAD")

	proofLogsDir := filepath.Join(h.loop.cfg.LaneRoot, ".rolling", "logs", tip)
	if err := os.MkdirAll(proofLogsDir, 0o755); err != nil {
		t.Fatal(err)
	}
	origWhole := filepath.Join(proofLogsDir, "00-unit.log")
	origTriage := filepath.Join(proofLogsDir, "00-unit.triage.log")
	wholeBody := `=== RUN   TestBrokenWidget
    widget_test.go:42: got 2, want 1
--- FAIL: TestBrokenWidget (0.01s)
fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage unit: unit suite passes
       case: TestBrokenWidget -- pkg/widget_test.go:42
       assertion: got 2, want 1
       one case: go test -run ^TestBrokenWidget$ ./pkg
`
	if err := os.WriteFile(origWhole, []byte(wholeBody), 0o644); err != nil {
		t.Fatal(err)
	}
	triageBody := "tillr-gate: triage of unit -- fail -- the whole output is " + origWhole + "\n--- FAIL: TestBrokenWidget (0.01s)\n"
	if err := os.WriteFile(origTriage, []byte(triageBody), 0o644); err != nil {
		t.Fatal(err)
	}

	verdictJSON := mustJSON(map[string]any{
		"kind": "gate.verdict",
		"payload": map[string]any{
			"checks": []map[string]any{{
				"check_id": "unit",
				"result":   "fail",
				"reads":    []string{"c"},
				"log_ref":  origTriage,
			}},
		},
	})
	decoded, err := decodeHeavyVerdict([]byte(verdictJSON))
	if err != nil {
		t.Fatalf("decodeHeavyVerdict: %v", err)
	}
	if len(decoded) != 1 || decoded[0].LogRef != origTriage || decoded[0].LogPath != origWhole {
		t.Fatalf("decoded = %+v", decoded)
	}

	h.steps.judge = func(_ context.Context, _ string, only string) ([]HeavyCheck, error) {
		if only != "" {
			return []HeavyCheck{{CheckID: only, Result: "fail", Reads: []string{"c"}, LogRef: origTriage, LogPath: origWhole}}, nil
		}
		return decoded, nil
	}
	h.proof()

	fixers := h.fixers()
	if len(fixers) != 1 {
		t.Fatalf("fixers = %v", fixers)
	}
	runID := "run-" + fixers[0] + "-a1"
	_, _, brief := stageTrunkFixerForTest(t, h.root, h.loop.cfg.LaneRoot, h.loop.cfg.Store, fixers[0], tip, runID)

	wantCopiedTriage, err := filepath.Abs(filepath.Join(RunDir(h.root, runID), "fixer.logs", "00-unit.triage.log"))
	if err != nil {
		t.Fatal(err)
	}
	wantCopiedWhole := strings.TrimSuffix(wantCopiedTriage, ".triage.log") + ".log"
	for _, want := range []string{
		fixerFailureHeading,
		"check unit (unit suite passes)",
		"case: TestBrokenWidget",
		"at: pkg/widget_test.go:42",
		"assertion: got 2, want 1",
		"reproduce only this case: go test -run ^TestBrokenWidget$ ./pkg",
		"- `unit`: " + wantCopiedTriage,
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("staged fixer brief lacks %q:\n%s", want, brief)
		}
	}
	copiedTriageBytes, err := os.ReadFile(wantCopiedTriage)
	if err != nil {
		t.Fatalf("copied triage log missing: %v", err)
	}
	if !strings.Contains(string(copiedTriageBytes), wantCopiedWhole) {
		t.Fatalf("copied triage log did not rewrite whole-log pointer to %s:\n%s", wantCopiedWhole, copiedTriageBytes)
	}
	if copiedWholeBytes, err := os.ReadFile(wantCopiedWhole); err != nil || !strings.Contains(string(copiedWholeBytes), "TestBrokenWidget") {
		t.Fatalf("copied whole log = %q, err = %v", copiedWholeBytes, err)
	}
}

func TestLegacyForkCheckArtifactsReachFixerBriefAndSurviveLaterOverwrite(t *testing.T) {
	script := `rev=$(git rev-parse HEAD)
common=$(git rev-parse --path-format=absolute --git-common-dir)
mkdir -p "$common/yolo-fork-check"
cat > "$common/yolo-fork-check/triage.json" <<EOF
{"at":"2026-11-20T00:00:00Z","commit":"$rev","checks":[{"check":"go-tool","label":"tools x","reproduce":"go test ./tools/x","single_case":"go test -run ^TestFromTriageJSON$ ./tools/x","cases":[{"name":"TestFromTriageJSON","file":"tools/x/x_test.go","line":19,"assertion":"expected alpha, got beta","single_case":"go test -run ^TestFromTriageJSON$ ./tools/x"}]}]}
EOF
cat > "$common/yolo-fork-check/report.txt" <<EOF
fork-check failure excerpt
run: 2026-11-20T00:00:00Z  commit $rev  scope heavy outcome FAIL

=== go-tool -- tools x
--- FAIL: TestFromTriageJSON (0.01s)
    x_test.go:19: expected alpha, got beta
--- end output ---
EOF
` + legacySpansFail + `exit 1`

	h := legacyProofHarness(t, script)
	h.land("item-later", "c", "unrelated\n")
	tip := heavyGit(t, h.root, "rev-parse", "HEAD")
	h.proof()

	if strings.Contains(h.report.String(), "DEFECT") {
		t.Fatalf("fork-check triage.json/report.txt output was treated as empty:\n%s", h.report.String())
	}
	fixers := h.fixers()
	if len(fixers) != 1 {
		t.Fatalf("fixers = %v", fixers)
	}

	// Overwrite shared yolo-fork-check/{triage.json,report.txt} with a different commit's
	// data before staging, proving filing snapshotted the judged commit's output.
	commonDir := heavyGit(t, h.root, "rev-parse", "--path-format=absolute", "--git-common-dir")
	otherSHA := strings.Repeat("f", 40)
	if err := os.WriteFile(filepath.Join(commonDir, "yolo-fork-check", "triage.json"),
		[]byte(`{"commit":"`+otherSHA+`","checks":[{"check":"go-tool","label":"tools x","cases":[{"name":"TestWrongCommit","assertion":"wrong commit leaked"}]}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(commonDir, "yolo-fork-check", "report.txt"),
		[]byte("fork-check failure excerpt\nrun: 2026-11-20T00:01:00Z  commit "+otherSHA+"  scope fast outcome FAIL\n\n=== go-tool -- tools x\nWRONG COMMIT EXCERPT\n--- end output ---\n"), 0o644); err != nil {
		t.Fatal(err)
	}

	runID := "run-" + fixers[0] + "-a1"
	_, _, brief := stageTrunkFixerForTest(t, h.root, h.loop.cfg.LaneRoot, h.loop.cfg.Store, fixers[0], tip, runID)
	for _, want := range []string{
		"case: TestFromTriageJSON",
		"at: tools/x/x_test.go:19",
		"assertion: expected alpha, got beta",
		"reproduce only this case: go test -run ^TestFromTriageJSON$ ./tools/x",
		"- `go-tool/tools-x`: ",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("staged fixer brief lacks %q:\n%s", want, brief)
		}
	}
	if strings.Contains(brief, "TestWrongCommit") || strings.Contains(brief, "WRONG COMMIT EXCERPT") {
		t.Fatalf("overwritten yolo-fork-check leaked into fixer brief:\n%s", brief)
	}
	copiedMatches, err := filepath.Glob(filepath.Join(RunDir(h.root, runID), "fixer.logs", "*.log"))
	if err != nil || len(copiedMatches) != 1 {
		t.Fatalf("copied fixer logs = %v, err = %v", copiedMatches, err)
	}
	copiedRaw, err := os.ReadFile(copiedMatches[0])
	if err != nil || !strings.Contains(string(copiedRaw), "TestFromTriageJSON") || !strings.Contains(string(copiedRaw), "=== go-tool -- tools x") {
		t.Fatalf("copied log missing triage or report excerpt: %q (%v)", copiedRaw, err)
	}

	// Also confirm extractForkCheckLogs ignores stale yolo-fork-check files from another commit.
	spansPath := filepath.Join(t.TempDir(), "spans.json")
	if err := os.WriteFile(spansPath, []byte(`{"check_spans":[{"check_id":"go-tool/tools-x","kind":"go-tool","label":"tools x","status":"FAIL"}]}`), 0o644); err != nil {
		t.Fatal(err)
	}
	staleOut := extractForkCheckLogs(context.Background(), h.root, tip, spansPath, "", "", t.TempDir())
	if len(staleOut) != 0 {
		t.Fatalf("stale yolo-fork-check from %s leaked into commit %s: %+v", otherSHA, tip, staleOut)
	}
}

func TestBeforeMergeRollingGivesTrunkFixerPerCheckTriageAndLogCopy(t *testing.T) {
	f, l := passFixture(t, "item-a")
	tip := runGit(t, f.root, "rev-parse", TrunkRef)
	rollingBody := `#!/bin/sh
set -eu
rev=$(git rev-parse HEAD)
common=$(git rev-parse --path-format=absolute --git-common-dir)
mkdir -p "$common/yolo-fork-check"
cat > "$common/yolo-fork-check/triage.json" <<EOF
{"commit":"$rev","checks":[{"check":"python-unit","label":"tests/rolling.test.py","single_case":"python3 -m unittest tests.rolling.TestRolling.test_red","cases":[{"name":"test_red","file":"tests/rolling.test.py","line":27,"assertion":"AssertionError: 5 != 6","single_case":"python3 -m unittest tests.rolling.TestRolling.test_red"}]}]}
EOF
cat > "$TILLR_GATE_SPANS_OUT" <<'JSON'
{"check_spans":[{"check_id":"python-unit/tests-rolling-test-py","kind":"python-unit","label":"tests/rolling.test.py","status":"FAIL","attributes_json":"{\"reads\":[\"a.txt\"]}"}]}
JSON
exit 1
`
	prog := filepath.Join(t.TempDir(), "rolling.sh")
	if err := os.WriteFile(prog, []byte(rollingBody), 0o755); err != nil {
		t.Fatal(err)
	}
	ps := &ProcessSteps{Root: f.root, LaneRoot: f.laneRoot, RollingProgram: prog}
	l.cfg.Steps = ps
	l.gatePolicy.LandingStrategy = "before-merge"
	var result LoopResult
	if err := l.maybeRolling(context.Background(), tip, true, &result); err != nil {
		t.Fatalf("maybeRolling: %v", err)
	}
	fixerID := TrunkFixerID(tip)
	_, _, brief := stageTrunkFixerForTest(t, f.root, f.laneRoot, f.storePath, fixerID, tip, "run-"+fixerID+"-a1")
	for _, want := range []string{
		"case: test_red",
		"at: tests/rolling.test.py:27",
		"assertion: AssertionError: 5 != 6",
		"reproduce only this case: python3 -m unittest tests.rolling.TestRolling.test_red",
		"- `python-unit/tests-rolling-test-py`: ",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("rolling fixer brief lacks %q:\n%s", want, brief)
		}
	}
}

func TestWrapperGateAcquittalFiltersToRedChecksAndExtractsForkCheckLogs(t *testing.T) {
	f, l := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", TrunkRef)

	candDir := filepath.Join(f.laneRoot, "lane-cand", WorktreeDir)
	if err := os.MkdirAll(filepath.Dir(candDir), 0o755); err != nil {
		t.Fatal(err)
	}
	runGit(t, f.root, "worktree", "add", "-b", "lane-cand-test", candDir, base)
	if err := os.WriteFile(filepath.Join(candDir, "cand.txt"), []byte("change\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	runGit(t, candDir, "add", "cand.txt")
	runGit(t, candDir, "commit", "-qm", "feat: candidate")
	head := runGit(t, candDir, "rev-parse", "HEAD")

	runID := "run-item-a-a1"
	runDir := RunDir(f.root, runID)
	gateScript := filepath.Join(t.TempDir(), "gate.sh")
	scriptBody := `#!/bin/sh
set -eu
mkdir -p "` + runDir + `/logs"
cat > "` + runDir + `/logs/00-fast-lint.log" <<'EOF'
fast-lint candidate-only failure
EOF
cat > "` + runDir + `/gate.fast.verdict.json" <<'EOF'
{"payload":{"checks":[{"check_id":"fast-lint","result":"fail","log_ref":"` + runDir + `/logs/00-fast-lint.log"}]}}
EOF
cat > "` + runDir + `/logs/00-gate.log" <<'EOF'
fork-check: triage -- 2 failed check(s), diagnosed from this run's own output and never re-run:
  triage go-tool: tools cand
       case: TestCandidateOnly -- tools/cand/cand_test.go:10
       assertion: candidate broke this
       one case: go test -run ^TestCandidateOnly$ ./tools/cand
  triage go-tool: tools trunk
       case: TestTrunkAlreadyBroken -- tools/trunk/trunk_test.go:88
       assertion: trunk already red here
       one case: go test -run ^TestTrunkAlreadyBroken$ ./tools/trunk
EOF
cat > "` + runDir + `/logs/00-gate.triage.log" <<'EOF'
tillr-gate: wrapper triage
EOF
cat > "` + runDir + `/gate.verdict.json" <<'EOF'
{"record_version":1,"kind":"gate.verdict","id":"verdict-1","producer":{"system":"tillr","utility":"tillr-gate","version":"0.1.0"},"created_at":"2026-11-20T00:00:00Z","payload":{"verdict":"fail","checks":[{"check_id":"gate:python3","result":"fail","log_ref":"` + runDir + `/logs/00-gate.triage.log"}]}}
EOF
cat > "` + runDir + `/gate.spans.json" <<'EOF'
{"check_spans":[
  {"check_id":"go-tool/tools-cand","kind":"go-tool","label":"tools cand","status":"FAIL","attributes_json":"{\"reads\":[\"cand.txt\"]}"},
  {"check_id":"go-tool/tools-trunk","kind":"go-tool","label":"tools trunk","status":"FAIL","attributes_json":"{\"reads\":[\"cand.txt\"]}"}
]}
EOF
exit 1
`
	if err := os.WriteFile(gateScript, []byte(scriptBody), 0o755); err != nil {
		t.Fatal(err)
	}
	ps := &ProcessSteps{Root: f.root, LaneRoot: f.laneRoot, Store: f.storePath, Now: l.cfg.Now, GateProgram: gateScript}
	cand := Candidate{ItemID: "item-a", RunID: runID, LaneID: "lane-cand", Worktree: candDir, BaseSHA: base, HeadSHA: head}
	gateOut, err := ps.Gate(context.Background(), cand)
	if err != nil || gateOut.Outcome != GateFailed {
		t.Fatalf("Gate = %+v, err = %v", gateOut, err)
	}

	var loopRes LoopResult
	if _, err := l.acquitRedWithPaths(context.Background(), &cand, gateOut, base, []string{"go-tool/tools-trunk"}, []string{"go-tool/tools-trunk"}, &loopRes); err != nil {
		t.Fatalf("acquitRedWithPaths: %v", err)
	}
	fixerID := TrunkFixerID(base)
	_, _, brief := stageTrunkFixerForTest(t, f.root, f.laneRoot, f.storePath, fixerID, base, "run-"+fixerID+"-a1")
	for _, want := range []string{
		"case: TestTrunkAlreadyBroken",
		"at: tools/trunk/trunk_test.go:88",
		"assertion: trunk already red here",
		"reproduce only this case: go test -run ^TestTrunkAlreadyBroken$ ./tools/trunk",
		"- `go-tool/tools-trunk`: ",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("acquitted fixer brief lacks %q:\n%s", want, brief)
		}
	}
	for _, unwanted := range []string{"TestCandidateOnly", "gate:python3", "fast-lint"} {
		if strings.Contains(brief, unwanted) {
			t.Fatalf("unrelated check %q leaked into trunk fixer brief:\n%s", unwanted, brief)
		}
	}
}

type replayProcessSteps struct {
	Steps
	ps *ProcessSteps
}

func (r replayProcessSteps) Replay(ctx context.Context, candidate Candidate, rev string, checks []string) (ReplayOutcome, error) {
	return r.ps.Replay(ctx, candidate, rev, checks)
}

func TestObstacleTrunkReplayCapturesPerCheckOutputForFixerBrief(t *testing.T) {
	f, l := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	replayScript := filepath.Join(t.TempDir(), "replay.sh")
	replayBody := `#!/bin/sh
set -eu
check="$1"
echo "fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:"
echo "  triage $check: replay check $check"
echo "       case: TestReplayOnly -- tools/replay_test.go:55"
echo "       assertion: replay caught trunk failure"
echo "       one case: go test -run ^TestReplayOnly$ ./tools"
exit 1
`
	if err := os.WriteFile(replayScript, []byte(replayBody), 0o755); err != nil {
		t.Fatal(err)
	}
	ps := &ProcessSteps{Root: f.root, LaneRoot: f.laneRoot, ReplayProgram: replayScript + " {only}"}
	l.cfg.Steps = replayProcessSteps{Steps: l.cfg.Steps, ps: ps}
	cand := Candidate{ItemID: "item-a", RunID: "run-item-a-a1", LaneID: "lane-1", BaseSHA: base}
	reading, err := l.trunkAlreadyRed(context.Background(), cand, GateOutcome{FailingChecks: []string{"go-tool/replay-red"}})
	if err != nil || !reading.Confirmed {
		t.Fatalf("trunkAlreadyRed = %+v, err = %v", reading, err)
	}
	gate := GateOutcome{FailingChecks: reading.Red, CheckLogs: reading.CheckLogs}
	var loopRes LoopResult
	if err := l.acquitRed(context.Background(), cand, gate, reading.RedSHA, reading.Red, &loopRes); err != nil {
		t.Fatalf("acquitRed: %v", err)
	}
	fixerID := TrunkFixerID(reading.RedSHA)
	_, _, brief := stageTrunkFixerForTest(t, f.root, f.laneRoot, f.storePath, fixerID, reading.RedSHA, "run-"+fixerID+"-a1")
	for _, want := range []string{
		"case: TestReplayOnly",
		"at: tools/replay_test.go:55",
		"assertion: replay caught trunk failure",
		"reproduce only this case: go test -run ^TestReplayOnly$ ./tools",
		"- `go-tool/replay-red`: ",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("replay-filed fixer brief lacks %q:\n%s", want, brief)
		}
	}
}

func TestAttachingSecondCheckPreservesBothChecksLogsInFixerBrief(t *testing.T) {
	f, l := passFixture(t, "item-a")
	sha1 := runGit(t, f.root, "rev-parse", TrunkRef)
	logA := filepath.Join(t.TempDir(), "check-a.log")
	logB := filepath.Join(t.TempDir(), "check-b.log")
	if err := os.WriteFile(logA, []byte("--- FAIL: TestAlpha (0.01s)\n    alpha_test.go:12: alpha broken\nFAIL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(logB, []byte("--- FAIL: TestBeta (0.01s)\n    beta_test.go:34: beta broken\nFAIL\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	fixerID, err := l.fileTrunkFixerNoted(context.Background(), sha1, []string{"check-a"}, GateOutcome{
		CheckWatches: map[string][]string{"check-a": {"a.txt"}},
		CheckLogs:    map[string]string{"check-a": logA},
	}, "")
	if err != nil {
		t.Fatal(err)
	}
	sha2 := strings.Repeat("b", 40)
	attachedID, err := l.fileTrunkFixerNoted(context.Background(), sha2, []string{"check-a", "check-b"}, GateOutcome{
		CheckWatches: map[string][]string{"check-a": {"a.txt"}, "check-b": {"b.txt"}},
		CheckLogs:    map[string]string{"check-b": logB},
	}, "")
	if err != nil || attachedID != fixerID {
		t.Fatalf("attachedID = %q (want %q), err = %v", attachedID, fixerID, err)
	}

	_, _, brief := stageTrunkFixerForTest(t, f.root, f.laneRoot, f.storePath, fixerID, sha1, "run-"+fixerID+"-a1")
	for _, want := range []string{
		"TestAlpha",
		"alpha_test.go:12: alpha broken",
		"TestBeta",
		"beta_test.go:34: beta broken",
		"- `check-a`: ",
		"- `check-b`: ",
	} {
		if !strings.Contains(brief, want) {
			t.Fatalf("attached fixer brief lacks %q:\n%s", want, brief)
		}
	}
}

func TestFixerBriefWithTemplateBracesInFailureOutputPassesPreflight(t *testing.T) {
	f, l := passFixture(t, "item-a")
	base := runGit(t, f.root, "rev-parse", TrunkRef)
	logPath := filepath.Join(t.TempDir(), "braces.log")
	logBody := `fork-check: triage -- 1 failed check(s), diagnosed from this run's own output and never re-run:
  triage tpl-check: template check
       case: TestTemplateLiteral -- tpl_test.go:9
       assertion: expected {{TITLE}} and {{UNFILLED_NAME}} and {{lower_brace}}, got empty
       one case: go test -run ^TestTemplateLiteral$ ./tpl
`
	if err := os.WriteFile(logPath, []byte(logBody), 0o644); err != nil {
		t.Fatal(err)
	}
	fixerID, err := l.fileTrunkFixerNoted(context.Background(), base, []string{"tpl-check"}, GateOutcome{
		CheckWatches: map[string][]string{"tpl-check": {"a.txt"}},
		CheckLogs:    map[string]string{"tpl-check": logPath},
	}, "note with {{CRITERIA_BRACE}}")
	if err != nil {
		t.Fatal(err)
	}
	laneBrief, runBrief, brief := stageTrunkFixerForTest(t, f.root, f.laneRoot, f.storePath, fixerID, base, "run-"+fixerID+"-a1")
	if !strings.Contains(brief, "{{TITLE}}") || !strings.Contains(brief, "{{UNFILLED_NAME}}") || !strings.Contains(brief, "{{lower_brace}}") {
		t.Fatalf("brace pairs were stripped instead of carried intact:\n%s", brief)
	}
	preflight, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "preflight.sh"))
	if err != nil {
		t.Fatal(err)
	}
	for _, targetBrief := range []string{laneBrief, runBrief} {
		cmd := exec.Command("sh", "-c", `. "$1"; PROG=probe; preflight_brief "$2"`, "preflight", preflight, targetBrief)
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("preflight_brief refused %s: %v: %s", targetBrief, err, out)
		}
	}
}
