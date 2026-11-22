package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

const fastGateTimingTestKind = "drain.fast-gate-timing"

type observedTimingEvent struct {
	ItemID         string `json:"item_id"`
	RunID          string `json:"run_id"`
	WallMS         int64  `json:"wall_ms"`
	ExitCode       int    `json:"exit_code"`
	SlowestCheck   string `json:"slowest_check"`
	SlowestCheckMS *int64 `json:"slowest_check_ms"`
	Median20WallMS *int64 `json:"median20_wall_ms"`
}

func gateTimingStore(t *testing.T, root string) string {
	t.Helper()
	path := filepath.Join(root, "timing.db")
	s, err := store.Open(context.Background(), path, true)
	if err != nil {
		t.Fatal(err)
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	return path
}

func readTimingEvents(t *testing.T, path string) []observedTimingEvent {
	t.Helper()
	s := openFixture(t, path)
	defer s.Close()
	var events []observedTimingEvent
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		rows, err := r.Query(`SELECT payload FROM events WHERE kind = ? ORDER BY seq`, fastGateTimingTestKind)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var raw []byte
			if err := rows.Scan(&raw); err != nil {
				return err
			}
			var e observedTimingEvent
			if err := json.Unmarshal(raw, &e); err != nil {
				return err
			}
			events = append(events, e)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return events
}

func TestGateTimingKeepsFreshAttemptsBeforeJudgment(t *testing.T) {
	root := t.TempDir()
	c := Candidate{ItemID: "item-example", RunID: "run-example", Worktree: root, deferGateJudgment: true}
	dir := RunDir(root, c.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	s := ProcessSteps{Root: root, Store: gateTimingStore(t, root), GateProgram: executable(t, `
set -eu
sleep 0.05
cat > "$TILLR_FAST_VERDICT_OUT" <<'EOF'
{"record_version":1,"kind":"gate.verdict","producer":{"system":"tillr","utility":"tillr-gate"},"payload":{"checks":[{"check_id":"go-build","execution":"ran","wall_ms":23,"go_build":[{"module":"tools/build","packages":[{"import_path":"example/build"}]}]},{"check_id":"docs","execution":"reused","wall_ms":500},{"check_id":"lint/optional","execution":"not-run","wall_ms":400},{"check_id":"go-tool/drain","execution":"not-run"},{"check_id":"gate:python3","execution":"ran","wall_ms":600}]}}
EOF
cat > "$TILLR_GATE_SPANS_OUT" <<'EOF'
{"plan_span":{"check_id":"plan"},"check_spans":[{"check_id":"go-tool/drain","status":"OK","attributes_json":"{\"execution\":\"ran\",\"wall_ms\":23,\"go_build\":[{\"module\":\"tools/drain\",\"packages\":[{\"import_path\":\"example/drain\"}]}]}"}],"skipped_checks":[{"check_id":"go-build"},{"check_id":"lint/optional"}],"reused_checks":["go-tool/land"],"reused_wall_ms":{"go-tool/land":2},"not_run_checks":["go-tool/run"]}
EOF
`)}
	ctx := context.Background()
	outcome, err := s.Gate(ctx, c)
	if err != nil || outcome.Outcome != GatePassed || outcome.finish == nil {
		t.Fatalf("Gate = %+v, %v", outcome, err)
	}
	type measuredCheck struct {
		Execution string `json:"execution"`
		WallMS    *int64 `json:"wall_ms"`
		GoBuild   []struct {
			Packages []struct {
				ImportPath string `json:"import_path"`
			} `json:"packages"`
		} `json:"go_build"`
	}
	type attempt struct {
		RunID       string                   `json:"run_id"`
		ExecutionID string                   `json:"gate_execution_id"`
		WallMS      *int64                   `json:"wall_ms"`
		ExitCode    int                      `json:"exit_code"`
		Checks      map[string]measuredCheck `json:"checks"`
	}
	read := func() [][]byte {
		t.Helper()
		body, err := os.ReadFile(filepath.Join(dir, "gate.timing.jsonl"))
		if err != nil {
			t.Fatal(err)
		}
		return bytes.Split(bytes.TrimSpace(body), []byte("\n"))
	}
	decode := func(raw []byte) attempt {
		t.Helper()
		var a attempt
		if err := json.Unmarshal(raw, &a); err != nil {
			t.Fatal(err)
		}
		if a.RunID != c.RunID || a.ExecutionID == "" || a.WallMS == nil || *a.WallMS < 0 {
			t.Fatalf("missing invocation identity or wall time: %s", raw)
		}
		return a
	}
	lines := read()
	if len(lines) != 1 {
		t.Fatalf("want one attempt before judgment, got %q", lines)
	}
	first := decode(lines[0])
	if first.ExitCode != 0 || *first.WallMS < 50 || len(first.Checks) != 6 {
		t.Fatalf("unexpected first observation: %s", lines[0])
	}
	for _, want := range []struct {
		id, execution, pkg string
		ms                 int64
	}{
		{"go-build", "ran", "example/build", 23}, {"go-tool/drain", "ran", "example/drain", 23},
		{"docs", "reused", "", 500}, {"go-tool/land", "reused", "", 2},
		{"lint/optional", "not-run", "", 400}, {"go-tool/run", "not-run", "", -1},
	} {
		got := first.Checks[want.id]
		if got.Execution != want.execution || (want.ms == -1 && got.WallMS != nil) ||
			(want.ms >= 0 && (got.WallMS == nil || *got.WallMS != want.ms)) {
			t.Fatalf("%s: unexpected execution or duration in %s", want.id, lines[0])
		}
		if want.pkg != "" && (len(got.GoBuild) != 1 || len(got.GoBuild[0].Packages) != 1 || got.GoBuild[0].Packages[0].ImportPath != want.pkg) {
			t.Fatalf("%s: missing selected package %s in %s", want.id, want.pkg, lines[0])
		}
	}

	if got := readTimingEvents(t, s.Store); len(got) != 1 {
		t.Fatalf("want one event before deferred judgment, got %+v", got)
	}
	if _, err := event.Lookup(fastGateTimingTestKind); err != nil {
		t.Fatal(err)
	}

	// An invocation that refuses to judge can leave only partial evidence. It must
	// neither borrow previous checks/time nor depend on a correctness verdict.
	s.GateProgram = executable(t, `
set -eu
cat > "$TILLR_GATE_SPANS_OUT" <<'EOF'
{"check_spans":[{"check_id":"lint/partial","status":"ERR","start_ns":1000000,"end_ns":1000000,"attributes_json":"{\"execution\":\"ran\",\"wall_ms\":null}"}]}
EOF
exit 2
`)
	if _, err := s.Gate(ctx, c); err == nil {
		t.Fatal("usage refusal returned a verdict")
	}
	again := read()
	if len(again) != 2 || !bytes.Equal(again[0], lines[0]) {
		t.Fatalf("earlier attempt was not retained: %q", again)
	}
	second := decode(again[1])
	partial := second.Checks["lint/partial"]
	if second.ExecutionID == first.ExecutionID || second.ExitCode != 2 || len(second.Checks) != 1 || partial.Execution != "ran" || partial.WallMS != nil {
		t.Fatalf("unexpected second observation: %s", again[1])
	}

	s.GateProgram = filepath.Join(root, "missing")
	if _, err := s.Gate(ctx, c); err == nil {
		t.Fatal("missing program returned a verdict")
	}
	if got := read(); len(got) != 2 {
		t.Fatalf("recorded an invocation that never started: %q", got)
	}
	got := readTimingEvents(t, s.Store)
	if len(got) != 2 {
		t.Fatalf("want exactly two events for two file observations, got %+v", got)
	}
	for i, a := range []attempt{first, second} {
		e := got[i]
		if e.ItemID != c.ItemID || e.RunID != a.RunID || e.WallMS != *a.WallMS || e.ExitCode != a.ExitCode || e.Median20WallMS != nil {
			t.Errorf("event %d does not match the observation: %+v", i, e)
		}
	}
	if e := got[0]; e.SlowestCheck != "go-build" || e.SlowestCheckMS == nil || *e.SlowestCheckMS != 23 {
		t.Errorf("event must name the lexically first tied check that ran: %+v", e)
	}
	if e := got[1]; e.SlowestCheck != "unmeasured" || e.SlowestCheckMS != nil {
		t.Errorf("event must leave duration absent when no check is timed: %+v", e)
	}
}

func TestGateTimingEventMedianUsesLastTwentyObservations(t *testing.T) {
	root := t.TempDir()
	s := ProcessSteps{Root: root, Store: gateTimingStore(t, root)}
	c := Candidate{ItemID: "item-median", RunID: "run-median"}
	exit := 0
	// The first 20 durations are a permutation of 0, 3, ..., 57; the last
	// window drops the 0 and adds 60. Timestamps move backwards by days so the
	// most recent measurements by wall clock are the earliest by sequence.
	wallMS := func(i int) int64 {
		if i == 20 {
			return 60
		}
		return int64(3 * ((7 * i) % 20))
	}
	ctx := context.Background()
	for i := 0; i < 21; i++ {
		if i == 19 {
			// An intervening judgment must not count as a timing observation.
			if err := appendJudgment(ctx, s.Store, GateVerdictKind, "run", c.RunID, nil, time.Unix(0, 0)); err != nil {
				t.Fatal(err)
			}
		}
		start := int64(22-i) * 86_400_000
		exit = i % 3
		s.recordGateTiming(ctx, c, StepSpan{StartedAtMS: start, FinishedAtMS: start + wallMS(i), ExitCode: &exit})
	}
	got := readTimingEvents(t, s.Store)
	if len(got) != 21 {
		t.Fatalf("want 21 timing events, got %d", len(got))
	}
	for i, e := range got {
		if e.WallMS != wallMS(i) || e.ExitCode != i%3 {
			t.Errorf("event %d: wrong duration or exit code: %+v", i, e)
		}
		switch i {
		case 19, 20:
			want := int64(28 + 3*(i-19))
			if e.Median20WallMS == nil || *e.Median20WallMS != want {
				t.Errorf("event %d: want median %d, got %v", i, want, e.Median20WallMS)
			}
		default:
			if e.Median20WallMS != nil {
				t.Errorf("event %d: median present before 20 observations", i)
			}
		}
	}
}

func TestGateTimingEventRequiresAppendedMeasuredLine(t *testing.T) {
	root := t.TempDir()
	s := ProcessSteps{Root: root, Store: gateTimingStore(t, root)}
	c := Candidate{ItemID: "item-missing", RunID: "run-missing"}
	path := filepath.Join(RunDir(root, c.RunID), gateTimingFile)
	exit := 0
	s.recordGateTiming(context.Background(), c, StepSpan{StartedAtMS: 2, FinishedAtMS: 1, ExitCode: &exit})
	body, err := os.ReadFile(path)
	if err != nil || !json.Valid(body) || bytes.Contains(body, []byte(`"wall_ms"`)) {
		t.Fatalf("expected the unmeasured file observation to remain: %s, %v", body, err)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(path, 0755); err != nil {
		t.Fatal(err)
	}
	s.recordGateTiming(context.Background(), c, StepSpan{StartedAtMS: 1, FinishedAtMS: 2, ExitCode: &exit})
	if got := readTimingEvents(t, s.Store); len(got) != 0 {
		t.Errorf("unmeasured or unappended observations produced events: %+v", got)
	}
}

func TestJudgeAutomaticRevertDoesNotOptIntoFastTiming(t *testing.T) {
	_, adapter := repositoryFile(t, "tools/gate/change-gate.py")
	_, judge := repositoryFile(t, "tools/fork-check.py")
	t.Setenv("FIXTURE_CHANGE_GATE", adapter)
	t.Setenv("FIXTURE_FORK_CHECK", judge)
	program := executable(t, `exec python3 - <<'PY'
import json, os, runpy, subprocess, sys
from pathlib import Path
from unittest.mock import patch

adapter = runpy.run_path(os.environ['FIXTURE_CHANGE_GATE'])['main']
judge = runpy.run_path(os.environ['FIXTURE_FORK_CHECK'])['run_gate']
fast = Path(os.environ['TILLR_FAST_VERDICT_OUT'])
spans = Path(os.environ['TILLR_GATE_SPANS_OUT'])

def plan(root, options):
    # Stop at the real landing judge's dispatch boundary, before selecting or running checks.
    spans.write_text(json.dumps({'check_spans': [{'check_id': 'gate-runner', 'status': 'OK'}],
                                 'fixture_timing': options.measure_landing}))
    raise judge.__globals__['PlanningRefusal']('fixture stopped before planning')

def invoke(args, **kwargs):
    if args[0] == 'git':
        return subprocess.CompletedProcess(args, 0, stdout='head\n')
    if args == ['fixture-runner', 'checks', '--help']:
        return subprocess.CompletedProcess(args, 0, stdout=b'')
    if args[0] == 'fixture-runner':
        check = {'check_id': 'go-build', 'result': 'pass'}
        if kwargs['env']['TILLR_FAST_TREE_TIMING'] == '1':
            check.update(execution='ran', wall_ms=0)
        return subprocess.CompletedProcess(args, 0, stdout=json.dumps({'payload': {'checks': [check]}}).encode())
    assert args[:2] == [sys.executable, 'tools/fork-check.py'], args
    assert json.loads(fast.read_bytes())['payload']['checks'][0]['result'] == 'pass'
    with patch.dict(judge.__globals__, {'plan_gate': plan}):
        assert judge(args[2:]) == 2
    return subprocess.CompletedProcess(args, 0)

sys.argv = ['change-gate', '--base', 'base', '--head', 'head', '--runner', 'fixture-runner', '--logs', 'logs']
with patch.dict(adapter.__globals__, {'warm_trimmed_go_cache': lambda: None}), patch.object(subprocess, 'run', side_effect=invoke):
    sys.exit(adapter())
PY
`)
	for _, tc := range []struct {
		name, itemID string
		measure      bool
	}{
		{"ordinary", "item-example", true},
		{"automatic revert", RevertItemID("abc123"), false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			// The invocation must choose the opt-in, not inherit it.
			inherited := "1"
			if tc.measure {
				inherited = ""
			}
			t.Setenv("TILLR_ORDINARY_GATE_TIMING", inherited)
			root := t.TempDir()
			c := Candidate{ItemID: tc.itemID, RunID: "run-example", Worktree: root, deferGateJudgment: true}
			dir := RunDir(root, c.RunID)
			if err := os.MkdirAll(dir, 0755); err != nil {
				t.Fatal(err)
			}
			s := ProcessSteps{Root: root, Store: gateTimingStore(t, root), GateProgram: program}
			if outcome, err := s.Gate(context.Background(), c); err != nil || outcome.Outcome != GatePassed {
				steps, _ := os.ReadFile(filepath.Join(dir, StepsFile))
				t.Fatalf("Gate = %+v, %v; steps: %s", outcome, err, steps)
			}
			fast, err := os.ReadFile(filepath.Join(dir, GateFastVerdictFile))
			if err != nil || !bytes.Contains(fast, []byte(`"result": "pass"`)) {
				t.Fatalf("missing correctness verdict handoff: %s, %v", fast, err)
			}
			spans, err := os.ReadFile(filepath.Join(dir, GateSpansFile))
			if err != nil || !bytes.Contains(spans, []byte(`"check_id": "gate-runner"`)) {
				t.Fatalf("missing correctness span handoff: %s, %v", spans, err)
			}
			var landing struct {
				Measure *bool `json:"fixture_timing"`
			}
			if err := json.Unmarshal(spans, &landing); err != nil || landing.Measure == nil {
				t.Fatalf("no landing dispatch observation: %s, %v", spans, err)
			}
			if fastMeasured := bytes.Contains(fast, []byte(`"execution"`)); fastMeasured != tc.measure || *landing.Measure != tc.measure {
				t.Errorf("timing opt-in: fast-tree=%t landing=%t; want both %t", fastMeasured, *landing.Measure, tc.measure)
			}
			body, err := os.ReadFile(filepath.Join(dir, "gate.timing.jsonl"))
			if tc.measure {
				if err != nil || !json.Valid(body) || !bytes.Contains(body, []byte(`"execution":"ran"`)) {
					t.Fatalf("ordinary gate did not append its timing record: %s, %v", body, err)
				}
			} else if !os.IsNotExist(err) {
				t.Fatalf("revert should not leave a timing record: %s, %v", body, err)
			}
			if got := readTimingEvents(t, s.Store); (len(got) == 1) != tc.measure || len(got) > 1 {
				t.Errorf("timing event should follow the ordinary gate opt-in: got %+v, measure=%t", got, tc.measure)
			}
		})
	}
}

func TestGateTimingDoesNotRecordNestedGateSpans(t *testing.T) {
	_, judge := repositoryFile(t, "tools/fork-check.py")
	root, _ := heavyRepo(t)
	body, err := os.ReadFile(judge)
	if err != nil {
		t.Fatal(err)
	}
	judge = filepath.Join(root, "tools", "fork-check.py")
	if err := os.MkdirAll(filepath.Dir(judge), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, judge, string(body))
	base := heavyCommit(t, root, "seed nested judge")
	writeForkFile(t, filepath.Join(root, "README.md"), "# Nested range diagnostic\n")
	head := heavyCommit(t, root, "change docs")
	t.Setenv("FIXTURE_NESTED_BASE", base)
	c := Candidate{ItemID: "item-example", RunID: "run-example", Worktree: root, BaseSHA: base, HeadSHA: head, deferGateJudgment: true}
	dir := RunDir(root, c.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	s := ProcessSteps{Root: root, GateProgram: executable(t, `exec python3 - <<'PY'
import json, os, runpy, sys
from pathlib import Path

judge = runpy.run_path('tools/fork-check.py')
check = judge['Check']('self-suite', 'nested diagnostic', [sys.executable, '-c', '''
import json, os, sys
from pathlib import Path
keys = ['TILLR_ORDINARY_GATE_TIMING', 'TILLR_FAST_TREE_TIMING',
        'TILLR_GATE_SPANS_OUT', 'TILLR_FAST_VERDICT_OUT']
Path('nested-env.json').write_text(json.dumps({key: os.environ[key] for key in keys if key in os.environ}))
os.execv(sys.executable, [sys.executable, 'tools/fork-check.py', '--tier', 'landing',
                         '--since', os.environ['FIXTURE_NESTED_BASE'], '--jobs', '1'])
'''], 'fixture', 0)
judge['execute_check'](check, Path.cwd(), os.environ['FIXTURE_NESTED_BASE'], measure_landing=True)
assert check.rc == 0, check.output
# Simulate a parent that refused to judge and never published its own spans.
sys.exit(2)
PY
`)}
	if outcome, err := s.Gate(context.Background(), c); err == nil {
		steps, _ := os.ReadFile(filepath.Join(dir, StepsFile))
		t.Fatalf("Gate = %+v, %v; steps: %s", outcome, err, steps)
	}
	if env, err := os.ReadFile(filepath.Join(root, "nested-env.json")); err != nil || string(env) != "{}" {
		t.Errorf("nested judge inherited the parent's handoffs: %s, %v", env, err)
	}
	for _, name := range []string{GateSpansFile, GateFastVerdictFile} {
		if _, err := os.Stat(filepath.Join(dir, name)); !os.IsNotExist(err) {
			t.Errorf("nested judge wrote parent handoff %s: %v", name, err)
		}
	}
	var attempt struct {
		ExitCode int                             `json:"exit_code"`
		Checks   map[string]gateCheckMeasurement `json:"checks"`
	}
	body, err = os.ReadFile(filepath.Join(dir, gateTimingFile))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(body, &attempt); err != nil || attempt.ExitCode != 2 || len(attempt.Checks) != 0 {
		t.Fatalf("parent timing includes nested observations: %s, %v", body, err)
	}
}

func TestGateTimingKeepsExecutedWaybarSkip(t *testing.T) {
	_, judge := repositoryFile(t, "tools/fork-check.py")
	root, _ := heavyRepo(t)
	body, err := os.ReadFile(judge)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tools"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools", "fork-check.py"), string(body))
	base := heavyCommit(t, root, "seed judge")
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tests", "waybar-ai-modules.test.sh"), "printf executed > waybar-marker\nexit 77\n")
	writeForkFile(t, filepath.Join(root, "tests", "test_waybar_cpu.py"), "import unittest\nclass TestCPU(unittest.TestCase):\n    def test_pass(self): pass\n")
	head := heavyCommit(t, root, "select Waybar")
	t.Setenv("FIXTURE_WAYBAR_BASE", base)
	c := Candidate{ItemID: "item-example", RunID: "run-waybar", Worktree: root, BaseSHA: base, HeadSHA: head, deferGateJudgment: true}
	dir := RunDir(root, c.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	s := ProcessSteps{Root: root, GateProgram: executable(t, `exec python3 tools/fork-check.py --tier landing --since "$FIXTURE_WAYBAR_BASE" --jobs 1`)}
	if outcome, err := s.Gate(context.Background(), c); err != nil || outcome.Outcome != GatePassed {
		steps, _ := os.ReadFile(filepath.Join(dir, StepsFile))
		t.Fatalf("Gate = %+v, %v; steps: %s", outcome, err, steps)
	}
	if marker, err := os.ReadFile(filepath.Join(root, "waybar-marker")); err != nil || string(marker) != "executed" {
		t.Fatalf("Waybar skip script didn't run: %q, %v", marker, err)
	}
	var spans gateSpansFile
	body, err = os.ReadFile(filepath.Join(dir, GateSpansFile))
	if err != nil || json.Unmarshal(body, &spans) != nil {
		t.Fatalf("missing gate spans: %s, %v", body, err)
	}
	const id = "waybar/waybar-module-configuration"
	const skipID = "waybar/waybar-sh-tests-waybar-ai-modules-test-sh"
	var skipped bool
	for _, skip := range spans.SkippedChecks {
		skipped = skipped || skip.CheckID == skipID
	}
	if !skipped {
		t.Fatalf("exit 77 lost its correctness skip: %s", body)
	}
	var attempt struct {
		Checks map[string]gateCheckMeasurement `json:"checks"`
	}
	body, err = os.ReadFile(filepath.Join(dir, gateTimingFile))
	if err != nil || json.Unmarshal(body, &attempt) != nil {
		t.Fatalf("missing timing record: %s, %v", body, err)
	}
	if got := attempt.Checks[id]; got.Execution != "ran" || got.WallMS == nil {
		t.Errorf("executed skip was not measured under its real check ID: %s", body)
	}
	if _, alias := attempt.Checks[skipID]; alias {
		t.Errorf("executed skip reported twice: %s", body)
	}
}

func TestGateTimingReadsFastChecksPreservedUnderAStall(t *testing.T) {
	dir := t.TempDir()
	writeForkFile(t, filepath.Join(dir, GateFastVerdictFile), `{
  "payload":{"result":"stalled","checks":[{"check_id":"go-tool/drain","duration_ms":100}],
  "fast_timing":{"wall_ms":9,"checks":[{"check_id":"docs","execution":"ran","wall_ms":7}]}}
}`)
	checks := readGateTimingChecks(dir)
	if docs := checks["docs"]; docs.Execution != "ran" || docs.WallMS == nil || *docs.WallMS != 7 {
		t.Fatalf("lost the earlier phase: %+v", checks)
	}
	if stalled, ok := checks["go-tool/drain"]; !ok || stalled.WallMS != nil {
		t.Fatalf("stall without wall_ms should name the check without a measurement: %+v", checks)
	}
}
