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
