package drain

import (
	"bytes"
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"syscall"
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
	_, adapter := repositoryFile(t, "tools/tillr-gate/change-gate.py")
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
	_, transportPath := repositoryFile(t, "tools/gate/classify.py")
	root, _ := heavyRepo(t)
	body, err := os.ReadFile(judge)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := os.ReadFile(transportPath)
	if err != nil {
		t.Fatal(err)
	}
	judge = filepath.Join(root, "tools", "fork-check.py")
	if err := os.MkdirAll(filepath.Join(root, "tools", "gate"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, judge, string(body))
	writeForkFile(t, filepath.Join(root, "tools", "gate", "classify.py"), string(transport))
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

func TestGateTimingKeepsExecutedIronbarSkip(t *testing.T) {
	_, judge := repositoryFile(t, "tools/fork-check.py")
	_, transportPath := repositoryFile(t, "tools/gate/classify.py")
	root, _ := heavyRepo(t)
	body, err := os.ReadFile(judge)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := os.ReadFile(transportPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tools", "gate"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools", "fork-check.py"), string(body))
	writeForkFile(t, filepath.Join(root, "tools", "gate", "classify.py"), string(transport))
	base := heavyCommit(t, root, "seed judge")
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tests", "ironbar-modules.test.sh"), "printf executed > ironbar-marker\nexit 77\n")
	head := heavyCommit(t, root, "select Ironbar")
	t.Setenv("FIXTURE_IRONBAR_BASE", base)
	c := Candidate{ItemID: "item-example", RunID: "run-ironbar", Worktree: root, BaseSHA: base, HeadSHA: head, deferGateJudgment: true}
	dir := RunDir(root, c.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	s := ProcessSteps{Root: root, GateProgram: executable(t, `exec python3 tools/fork-check.py --tier landing --since "$FIXTURE_IRONBAR_BASE" --jobs 1`)}
	if outcome, err := s.Gate(context.Background(), c); err != nil || outcome.Outcome != GatePassed {
		steps, _ := os.ReadFile(filepath.Join(dir, StepsFile))
		t.Fatalf("Gate = %+v, %v; steps: %s", outcome, err, steps)
	}
	if marker, err := os.ReadFile(filepath.Join(root, "ironbar-marker")); err != nil || string(marker) != "executed" {
		t.Fatalf("Ironbar skip script didn't run: %q, %v", marker, err)
	}
	var spans gateSpansFile
	body, err = os.ReadFile(filepath.Join(dir, GateSpansFile))
	if err != nil || json.Unmarshal(body, &spans) != nil {
		t.Fatalf("missing gate spans: %s, %v", body, err)
	}
	const id = "ironbar/ironbar-module-configuration"
	const skipID = "ironbar/ironbar-sh-tests-ironbar-modules-test-sh"
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

func TestGateTimingSingleGatePython3PathNamesSlowestCheck(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	c := Candidate{ItemID: "item-python3", RunID: "run-python3", Worktree: root, deferGateJudgment: true}
	dir := RunDir(root, c.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	s := ProcessSteps{Root: root, Store: gateTimingStore(t, root), GateProgram: executable(t, `
set -eu
cat > "`+filepath.Join(dir, GateVerdictRecord)+`" <<'EOF'
{"record_version":1,"kind":"gate.verdict","payload":{"result":"pass","checks":[{"check_id":"gate:python3","result":"pass","duration_ms":236206}]}}
EOF
cat > "$TILLR_FAST_VERDICT_OUT" <<'EOF'
{"record_version":1,"kind":"gate.verdict","payload":{"result":"pass","checks":[
  {"check_id":"go-build","result":"pass","duration_ms":739,"start_unix_nano":1700000000000000000,"end_unix_nano":1700000000739000000},
  {"check_id":"docs","result":"remembered","memory":"remembered","duration_ms":500},
  {"check_id":"gate-budget","result":"skipped: no changed path is read","duration_ms":0}
]}}
EOF
cat > "$TILLR_GATE_SPANS_OUT" <<'EOF'
{"plan_span":{"check_id":"plan"},"check_spans":[
  {"check_id":"self-suite/the-gate-s-own-audit-suite-still-passes","status":"ok","start_ns":1700000001000000000,"end_ns":1700000236000000000,"attributes_json":"{\"cached\":false,\"rc\":0,\"seconds\":235.0}"},
  {"check_id":"file-size/warn","status":"ok","start_ns":1700000000800000000,"end_ns":1700000000813000000,"attributes_json":"{\"cached\":false,\"rc\":0,\"seconds\":0.013}"},
  {"check_id":"unfinished/copied-end","status":"ERR","start_ns":1700000000900000000,"end_ns":1700000000900000000,"attributes_json":"{\"cached\":false}"},
  {"check_id":"null/explicit","status":"ERR","start_ns":1700000000900000000,"end_ns":1700000000950000000,"attributes_json":"{\"execution\":\"ran\",\"wall_ms\":null}"}
]}
EOF
`)}
	outcome, err := s.Gate(ctx, c)
	if err != nil || outcome.Outcome != GatePassed {
		t.Fatalf("Gate = %+v, %v", outcome, err)
	}
	var attempt struct {
		Checks map[string]gateCheckMeasurement `json:"checks"`
	}
	body, err := os.ReadFile(filepath.Join(dir, gateTimingFile))
	if err != nil || json.Unmarshal(body, &attempt) != nil {
		t.Fatalf("missing or invalid timing record: %s, %v", body, err)
	}
	for _, want := range []struct {
		id, exec string
		ms       int64
	}{
		{"go-build", "ran", 739},
		{"docs", "reused", -1},
		{"gate-budget", "not-run", -1},
		{"self-suite/the-gate-s-own-audit-suite-still-passes", "ran", 235000},
		{"file-size/warn", "ran", 13},
		{"unfinished/copied-end", "ran", -1},
		{"null/explicit", "ran", -1},
	} {
		got, ok := attempt.Checks[want.id]
		if !ok || got.Execution != want.exec || (want.ms < 0 && got.WallMS != nil) ||
			(want.ms >= 0 && (got.WallMS == nil || *got.WallMS != want.ms)) {
			t.Fatalf("%s: got %+v, want exec=%s ms=%d (record=%s)", want.id, got, want.exec, want.ms, body)
		}
	}
	if _, leaked := attempt.Checks["gate:python3"]; leaked {
		t.Fatalf("wrapper gate:python3 leaked into per-check timing: %s", body)
	}
	events := readTimingEvents(t, s.Store)
	if len(events) != 1 {
		t.Fatalf("want 1 timing event, got %+v", events)
	}
	if e := events[0]; e.SlowestCheck != "self-suite/the-gate-s-own-audit-suite-still-passes" ||
		e.SlowestCheckMS == nil || *e.SlowestCheckMS != 235000 {
		t.Fatalf("timing event should name the slowest check from the gate:python3 path: %+v", e)
	}
}

func TestSelfSuitePerTestTimingsAreRecordedInGateTiming(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	c := Candidate{ItemID: "item-example", RunID: "run-self-suite", Worktree: root, deferGateJudgment: true}
	dir := RunDir(root, c.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	s := ProcessSteps{Root: root, Store: gateTimingStore(t, root), GateProgram: executable(t, `
set -eu
cat > "$TILLR_FAST_VERDICT_OUT" <<'EOF'
{"record_version":1,"kind":"gate.verdict","payload":{"result":"pass","checks":[]}}
EOF
cat > "$TILLR_GATE_SPANS_OUT" <<'EOF'
{"plan_span":{"check_id":"plan"},"check_spans":[
  {"check_id":"self-suite/the-gate-s-own-audit-suite-still-passes","status":"ok","start_ns":1700000001000000000,"end_ns":1700000032000000000,"attributes_json":"{\"execution\":\"ran\",\"wall_ms\":31000,\"self_suite\":{\"preamble_wall_ms\":420,\"shards\":[{\"name\":\"core-routing\",\"wall_ms\":18200}],\"tests\":[{\"name\":\"1. A fresh repository\",\"shard\":\"core-routing\",\"wall_ms\":1900}]}}"}
]}
EOF
`)}
	if outcome, err := s.Gate(ctx, c); err != nil || outcome.Outcome != GatePassed {
		t.Fatalf("Gate = %+v, %v", outcome, err)
	}
	var attempt struct {
		Checks map[string]gateCheckMeasurement `json:"checks"`
	}
	body, err := os.ReadFile(filepath.Join(dir, gateTimingFile))
	if err != nil || json.Unmarshal(body, &attempt) != nil {
		t.Fatalf("missing or invalid timing record: %s, %v", body, err)
	}
	got := attempt.Checks["self-suite/the-gate-s-own-audit-suite-still-passes"]
	var suite struct {
		PreambleWallMS int64 `json:"preamble_wall_ms"`
		Shards         []struct {
			Name   string `json:"name"`
			WallMS int64  `json:"wall_ms"`
		} `json:"shards"`
		Tests []struct {
			Name   string `json:"name"`
			Shard  string `json:"shard"`
			WallMS int64  `json:"wall_ms"`
		} `json:"tests"`
	}
	if len(got.SelfSuite) == 0 || json.Unmarshal(got.SelfSuite, &suite) != nil {
		t.Fatalf("missing self_suite per-test timing in gate.timing.jsonl: %s", body)
	}
	if suite.PreambleWallMS != 420 || len(suite.Shards) != 1 || suite.Shards[0].Name != "core-routing" ||
		len(suite.Tests) != 1 || suite.Tests[0].Name != "1. A fresh repository" || suite.Tests[0].WallMS != 1900 {
		t.Fatalf("unexpected self_suite payload: %+v", suite)
	}
}

func TestGateTimingCountsUnfinishedCheckWithoutFinishRecord(t *testing.T) {
	ctx := context.Background()
	root := t.TempDir()
	c := Candidate{ItemID: "fixer-main-example", RunID: "run-fixer-main-example-a1", Worktree: root, deferGateJudgment: true}
	dir := RunDir(root, c.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	const hungID = "go-test-shards-suite/sharded-go-test-runner-regression-suite-passes"
	s := ProcessSteps{Root: root, Store: gateTimingStore(t, root), Now: time.Now, GateProgram: executable(t, `
set -eu
cat > "$TILLR_FAST_VERDICT_OUT" <<'EOF'
{"record_version":1,"kind":"gate.verdict","payload":{"result":"pass","checks":[
  {"check_id":"pack-sync","result":"pass","execution":"ran","wall_ms":12}
]}}
EOF
python3 - <<'PY'
import json, os, time
from pathlib import Path

checks_out = Path(os.environ["TILLR_GATE_SPANS_OUT"]).with_name("gate.checks.jsonl")
now_ms = time.time_ns() // 1_000_000
lines = [
    {"check_id": "file-size/warn", "started_at_ms": now_ms},
    {"check_id": "file-size/warn", "started_at_ms": now_ms, "finished_at_ms": now_ms + 5},
    {"check_id": "`+hungID+`", "started_at_ms": now_ms},
]
checks_out.write_text("".join(json.dumps(line) + "\n" for line in lines), encoding="utf-8")
time.sleep(0.05)
PY
exit 4
`)}
	if outcome, err := s.Gate(ctx, c); err == nil && outcome.Outcome == GatePassed {
		t.Fatalf("expected non-passing gate outcome, got %+v", outcome)
	}

	type checkRecord struct {
		CheckID      string `json:"check_id"`
		StartedAtMS  int64  `json:"started_at_ms"`
		FinishedAtMS *int64 `json:"finished_at_ms"`
	}
	rawChecks, err := os.ReadFile(filepath.Join(dir, GateChecksFile))
	if err != nil {
		t.Fatal(err)
	}
	var hungRecords []checkRecord
	for _, line := range bytes.Split(bytes.TrimSpace(rawChecks), []byte("\n")) {
		var rec checkRecord
		if err := json.Unmarshal(line, &rec); err != nil {
			t.Fatal(err)
		}
		if rec.CheckID == hungID {
			hungRecords = append(hungRecords, rec)
		}
	}
	if len(hungRecords) != 1 || hungRecords[0].StartedAtMS <= 0 || hungRecords[0].FinishedAtMS != nil {
		t.Fatalf("expected longest check to have start record and no finish record, got %+v", hungRecords)
	}

	rawSteps, err := os.ReadFile(filepath.Join(dir, StepsFile))
	if err != nil {
		t.Fatal(err)
	}
	var gateStep StepSpan
	if err := json.Unmarshal(bytes.TrimSpace(rawSteps), &gateStep); err != nil {
		t.Fatal(err)
	}
	wantHungMS := gateStep.FinishedAtMS - hungRecords[0].StartedAtMS

	var attempt struct {
		Checks map[string]gateCheckMeasurement `json:"checks"`
	}
	body, err := os.ReadFile(filepath.Join(dir, gateTimingFile))
	if err != nil || json.Unmarshal(bytes.TrimSpace(body), &attempt) != nil {
		t.Fatalf("missing or invalid timing record: %s, %v", body, err)
	}
	for _, want := range []struct {
		id string
		ms int64
	}{
		{"pack-sync", 12},
		{"file-size/warn", 5},
		{hungID, wantHungMS},
	} {
		got := attempt.Checks[want.id]
		if got.Execution != "ran" || got.WallMS == nil || *got.WallMS != want.ms {
			t.Fatalf("%s: got %+v, want exec=ran wall_ms=%d (record=%s)", want.id, got, want.ms, body)
		}
	}

	events := readTimingEvents(t, s.Store)
	if len(events) != 1 {
		t.Fatalf("want 1 timing event, got %+v", events)
	}
	if e := events[0]; e.ExitCode != 4 || e.SlowestCheck != hungID || e.SlowestCheckMS == nil || *e.SlowestCheckMS != wantHungMS {
		t.Fatalf("timing event should name unfinished check with elapsed time %d: %+v", wantHungMS, e)
	}

	// A fresh gate attempt on the same run must clear the earlier unfinished check log.
	s.GateProgram = executable(t, `
set -eu
cat > "$TILLR_FAST_VERDICT_OUT" <<'EOF'
{"record_version":1,"kind":"gate.verdict","payload":{"result":"pass","checks":[
  {"check_id":"pack-sync","result":"pass","execution":"ran","wall_ms":8}
]}}
EOF
`)
	if outcome, err := s.Gate(ctx, c); err != nil || outcome.Outcome != GatePassed {
		t.Fatalf("second Gate = %+v, %v", outcome, err)
	}
	events = readTimingEvents(t, s.Store)
	if len(events) != 2 {
		t.Fatalf("want 2 timing events after retry, got %+v", events)
	}
	if e := events[1]; e.SlowestCheck != "pack-sync" || e.SlowestCheckMS == nil || *e.SlowestCheckMS != 8 {
		t.Fatalf("stale unfinished check leaked into fresh attempt: %+v", e)
	}
}

func TestGateTimingInterruptedForkCheckCountsHungCheck(t *testing.T) {
	_, judge := repositoryFile(t, "tools/fork-check.py")
	_, transportPath := repositoryFile(t, "tools/gate/classify.py")
	root, _ := heavyRepo(t)
	body, err := os.ReadFile(judge)
	if err != nil {
		t.Fatal(err)
	}
	transport, err := os.ReadFile(transportPath)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tools", "gate"), 0755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, "tests"), 0755); err != nil {
		t.Fatal(err)
	}
	writeForkFile(t, filepath.Join(root, "tools", "fork-check.py"), string(body))
	writeForkFile(t, filepath.Join(root, "tools", "gate", "classify.py"), string(transport))
	writeForkFile(t, filepath.Join(root, "tests", "go-test-shards.test.py"), "pass\n")
	base := heavyCommit(t, root, "seed shard suite")

	readyPath := filepath.Join(t.TempDir(), "check-started")
	pidPath := filepath.Join(t.TempDir(), "fork-check.pid")
	writeForkFile(t, filepath.Join(root, "tests", "go-test-shards.test.py"),
		"from pathlib import Path\nimport time\nPath("+strconv.Quote(readyPath)+").write_text('started')\ntime.sleep(60)\n")
	head := heavyCommit(t, root, "hang shard suite")

	t.Setenv("FIXTURE_HUNG_BASE", base)
	t.Setenv("FIXTURE_FORK_CHECK_PID", pidPath)
	c := Candidate{ItemID: "fixer-main-hung", RunID: "run-fixer-main-hung-a1", Worktree: root, BaseSHA: base, HeadSHA: head, deferGateJudgment: true}
	dir := RunDir(root, c.RunID)
	if err := os.MkdirAll(dir, 0755); err != nil {
		t.Fatal(err)
	}
	s := ProcessSteps{Root: root, Store: gateTimingStore(t, root), Now: time.Now, GateProgram: executable(t, `
set -eu
cat > "$TILLR_FAST_VERDICT_OUT" <<'EOF'
{"record_version":1,"kind":"gate.verdict","payload":{"result":"pass","checks":[
  {"check_id":"pack-sync","result":"pass","execution":"ran","wall_ms":10}
]}}
EOF
python3 - <<'PY' || exit 4
import os, subprocess, sys
from pathlib import Path

proc = subprocess.Popen([sys.executable, "tools/fork-check.py", "--tier", "landing",
                         "--since", os.environ["FIXTURE_HUNG_BASE"], "--jobs", "2"])
Path(os.environ["FIXTURE_FORK_CHECK_PID"]).write_text(str(proc.pid))
sys.exit(0 if proc.wait() == 0 else 4)
PY
`)}

	const hungID = "go-test-shards-suite/sharded-go-test-runner-regression-suite-passes"
	const fileSizeID = "file-size/warn-when-a-tracked-ts-file-exceeds-2-000-lines"
	type checkRecord struct {
		CheckID      string `json:"check_id"`
		StartedAtMS  int64  `json:"started_at_ms"`
		FinishedAtMS *int64 `json:"finished_at_ms"`
	}
	parseCheckRecords := func(raw []byte) (map[string][]checkRecord, error) {
		byID := map[string][]checkRecord{}
		for _, line := range bytes.Split(bytes.TrimSpace(raw), []byte("\n")) {
			if len(bytes.TrimSpace(line)) == 0 {
				continue
			}
			var rec checkRecord
			if err := json.Unmarshal(line, &rec); err != nil {
				return nil, err
			}
			byID[rec.CheckID] = append(byID[rec.CheckID], rec)
		}
		return byID, nil
	}

	stopDone := make(chan error, 1)
	go func() {
		deadline := time.Now().Add(hangGuard)
		var hungStartMS int64
		maxFinishedMS := int64(10) // pack-sync wall_ms in gate.fast.verdict.json
		for {
			_, readyErr := os.Stat(readyPath)
			rawChecks, checksErr := os.ReadFile(filepath.Join(dir, GateChecksFile))
			if readyErr == nil && checksErr == nil {
				if byID, err := parseCheckRecords(rawChecks); err == nil {
					hung := byID[hungID]
					fs := byID[fileSizeID]
					if len(hung) == 1 && hung[0].StartedAtMS > 0 && len(fs) >= 2 && fs[len(fs)-1].FinishedAtMS != nil {
						hungStartMS = hung[0].StartedAtMS
						if fsMS := *fs[len(fs)-1].FinishedAtMS - fs[0].StartedAtMS; fsMS > maxFinishedMS {
							maxFinishedMS = fsMS
						}
						break
					}
				}
			}
			if time.Now().After(deadline) {
				stopDone <- os.ErrDeadlineExceeded
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
		rawPID, readErr := os.ReadFile(pidPath)
		if readErr != nil {
			stopDone <- readErr
			return
		}
		pid, parseErr := strconv.Atoi(strings.TrimSpace(string(rawPID)))
		if parseErr != nil {
			stopDone <- parseErr
			return
		}
		targetStop := time.UnixMilli(hungStartMS + maxFinishedMS + 25)
		if wait := time.Until(targetStop); wait > 0 {
			time.Sleep(wait)
		}
		stopDone <- syscall.Kill(pid, syscall.SIGTERM)
	}()

	if outcome, err := s.Gate(context.Background(), c); err == nil && outcome.Outcome == GatePassed {
		t.Fatalf("interrupted gate should not pass: %+v", outcome)
	}
	if killErr := <-stopDone; killErr != nil {
		t.Fatalf("stopping hung fork-check: %v", killErr)
	}
	if _, err := os.Stat(filepath.Join(dir, GateSpansFile)); !os.IsNotExist(err) {
		t.Fatalf("interrupted fork-check should not publish %s: %v", GateSpansFile, err)
	}

	rawChecks, err := os.ReadFile(filepath.Join(dir, GateChecksFile))
	if err != nil {
		t.Fatalf("missing gate.checks.jsonl: %v", err)
	}
	byID, err := parseCheckRecords(rawChecks)
	if err != nil {
		t.Fatal(err)
	}
	hungRecords := byID[hungID]
	if len(hungRecords) != 1 || hungRecords[0].StartedAtMS <= 0 || hungRecords[0].FinishedAtMS != nil {
		t.Fatalf("expected %s to have start record and no finish record, got %+v (raw=%s)", hungID, hungRecords, rawChecks)
	}

	rawSteps, err := os.ReadFile(filepath.Join(dir, StepsFile))
	if err != nil {
		t.Fatal(err)
	}
	var gateStep StepSpan
	if err := json.Unmarshal(bytes.TrimSpace(rawSteps), &gateStep); err != nil {
		t.Fatal(err)
	}
	wantHungMS := gateStep.FinishedAtMS - hungRecords[0].StartedAtMS

	events := readTimingEvents(t, s.Store)
	if len(events) != 1 {
		t.Fatalf("want 1 timing event, got %+v", events)
	}
	if e := events[0]; e.SlowestCheck != hungID || e.SlowestCheckMS == nil || *e.SlowestCheckMS != wantHungMS {
		t.Fatalf("expected slowest_check=%s slowest_check_ms=%d, got %+v", hungID, wantHungMS, e)
	}
}
