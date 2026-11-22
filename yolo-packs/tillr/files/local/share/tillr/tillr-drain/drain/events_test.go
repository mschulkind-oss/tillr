package drain_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// judgingGate is a gate stub that writes the two records a real gate writes into the run's
// own record directory, and then exits with the code $GATE_EXIT: the plan, then the verdict.
// It is what makes the loop's judgment observable as both a file and an event, which is the
// pair this case is about.
const judgingGate = counting + `run=; prev=
for a in "$@"; do
  if [ "$prev" = "--run" ]; then run=$a; fi
  prev=$a
done
dir="$GATE_ROOT/.tillr/loop/$run"
mkdir -p "$dir"
payload='{"plan_id":"plan-1","registry_digest":"d1","base_sha":"b1","tree_sha":"t1","result":"pass","checks":[]}'
printf '{"record_version":1,"kind":"gate.plan","id":"plan-1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-13T06:21:56Z","payload":%s}\n' "$payload" > "$dir/gate.plan.json"
printf '{"record_version":1,"kind":"gate.verdict","id":"verdict-1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-13T06:21:56Z","payload":%s}\n' "$payload" > "$dir/gate.verdict.json"
exit "${GATE_EXIT:-0}"
`

// eventsOf reads the store's event log and answers it in seq order.
func eventsOf(t *testing.T, path string) []event.Event {
	t.Helper()
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatalf("opening the fixture store failed: %v", err)
	}
	defer s.Close()
	got, err := s.Events(context.Background(), 0)
	if err != nil {
		t.Fatalf("reading the events failed: %v", err)
	}
	return got
}

// eventPayload reads one event's payload as a field map, which is how a case asserts a
// correlation field without a struct per kind.
func eventPayload(t *testing.T, e event.Event) map[string]any {
	t.Helper()
	var payload map[string]any
	if err := json.Unmarshal(e.Payload, &payload); err != nil {
		t.Fatalf("reading the %s payload failed: %v", e.Kind, err)
	}
	return payload
}

// kinds joins the log's kinds in seq order, so a failure prints the order the store holds.
func kinds(log []event.Event) []string {
	got := make([]string, 0, len(log))
	for _, e := range log {
		got = append(got, e.Kind)
	}
	return got
}

// The loop's judgment steps append the events the store's schema names: a reader
// reconstructing the run from `tillr view events` sees the boundary bracket, the plan and the
// verdict between the run's finish and the land, and each carries the run, item, lane and
// tick it belongs to.
//
// This is the acceptance case for the measured hole: an unattended drain on 2026-11-13 left
// `gate.plan.json` and `gate.verdict.json` on disk and no `gate.plan`, `gate.verdict` or
// `boundary.check` event anywhere, so the log ran `run.finish` -> `land.enqueue` with no
// verdict in between.
func TestTheLoopRecordsItsJudgmentsAsEvents(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_061", "in-queue"))
	t.Setenv("GATE_ROOT", f.root)
	gate := stub(t, "gate", judgingGate)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)

	loop := f.newLoop(t, 1, f.steps(t, gate, land, verdict))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Landed != 1 {
		t.Fatalf("the loop stopped %q having landed %d, want %q and 1", result.Stop, result.Landed, drain.StopQueueDrained)
	}

	log := eventsOf(t, f.store)
	// The run's own events are the runner's, and they bracket the loop's judgment: allocate,
	// the terminal record, then the loop's bracket and the gate's two judgments.
	var got []string
	for _, e := range log {
		switch e.Kind {
		case "run.allocate", "run.finish", drain.BoundaryCheckKind, drain.GatePlanKind, drain.GateVerdictKind:
			got = append(got, e.Kind)
		}
	}
	// Drain reserves the lane atomically with dispatch; run exec refreshes that
	// same claim without recording a second allocation for the same attempt.
	want := []string{"run.allocate", "run.finish", drain.BoundaryCheckKind, drain.GatePlanKind, drain.GateVerdictKind}
	if strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("the log orders the run's judgment as %v, want %v (all kinds: %v)", got, want, kinds(log))
	}
	// The order a reader sees, with the run each judgment is about: the specimen the report
	// quotes, printed rather than asserted so it reads as the log does.
	for _, e := range log {
		object := ""
		if e.ObjectID != nil {
			object = *e.ObjectID
		}
		t.Logf("seq %d %-15s object=%s/%s", e.Seq, e.Kind, e.ObjectType, object)
	}
	// A store the caller wants to keep a copy of, so `tillr view events` can be pointed at
	// the run this case drove rather than at a paraphrase of it. Unset is the ordinary case.
	if specimen := os.Getenv("TILLR_SPECIMEN_STORE"); specimen != "" {
		body, err := os.ReadFile(f.store)
		if err != nil {
			t.Fatalf("reading the fixture store for the specimen failed: %v", err)
		}
		if err := os.MkdirAll(filepath.Dir(specimen), 0o755); err != nil {
			t.Fatalf("creating the specimen directory failed: %v", err)
		}
		if err := os.WriteFile(specimen, body, 0o644); err != nil {
			t.Fatalf("writing the specimen store failed: %v", err)
		}
	}

	// One run id, named by every judgment: the correlation the log was missing. It is read
	// from `run.finish`, the runner's own terminal record — `run.allocate` is about the lane
	// it claimed, not the run.
	runID := ""
	for _, e := range log {
		if e.Kind == "run.finish" && e.ObjectID != nil {
			runID = *e.ObjectID
		}
	}
	if runID == "" {
		t.Fatalf("the run allocated no lane, so nothing correlates: %v", kinds(log))
	}
	for _, e := range log {
		switch e.Kind {
		case drain.GatePlanKind, drain.GateVerdictKind:
			if e.ObjectType != "run" || e.ObjectID == nil || *e.ObjectID != runID {
				t.Fatalf("the %s event is about %v/%v, want run/%s", e.Kind, e.ObjectType, e.ObjectID, runID)
			}
			payload := eventPayload(t, e)
			if payload["run_id"] != runID || payload["item_id"] != "item_061" || payload["lane_id"] != laneName(1) {
				t.Fatalf("the %s payload correlates as %v, want run %s on item_061 in %s",
					e.Kind, payload, runID, laneName(1))
			}
			if tick, _ := payload["tick_id"].(string); !strings.HasPrefix(tick, "gen-") {
				t.Fatalf("the %s payload names generation %v, want the pass it was judged in", e.Kind, payload["tick_id"])
			}
			// The step's own measurement: the program, its exit and its duration, and the
			// record it wrote read back rather than replaced.
			if exit, _ := payload["exit_code"].(float64); exit != 0 {
				t.Fatalf("the %s payload records exit %v, want 0", e.Kind, payload["exit_code"])
			}
			if duration, ok := payload["duration_ms"].(float64); !ok || duration < 0 {
				t.Fatalf("the %s payload records duration %v, want a measurement", e.Kind, payload["duration_ms"])
			}
			program, _ := payload["program"].([]any)
			if len(program) == 0 || program[0] != gate {
				t.Fatalf("the %s payload records program %v, want the gate stub %s", e.Kind, payload["program"], gate)
			}
			if payload["record_state"] != drain.GateRecordRead {
				t.Fatalf("the %s payload records the record as %v, want %q", e.Kind, payload["record_state"], drain.GateRecordRead)
			}
			// The gate's own fields stay at the top level, which is what a reader of the
			// design's field list queries.
			if payload["plan_id"] != "plan-1" || payload["result"] != "pass" {
				t.Fatalf("the %s payload dropped the record's own fields: %v", e.Kind, payload)
			}
		case drain.BoundaryCheckKind:
			payload := eventPayload(t, e)
			if payload["scope"] != drain.BoundaryScopeRun || payload["clean"] != true {
				t.Fatalf("the boundary event records scope %v clean %v, want a clean run", payload["scope"], payload["clean"])
			}
			runs, _ := payload["runs"].([]any)
			if len(runs) != 1 {
				t.Fatalf("the boundary event names %d run(s), want the run's one: %v", len(runs), payload["runs"])
			}
			if one, _ := runs[0].(map[string]any); one["run_id"] != runID || one["item_id"] != "item_061" {
				t.Fatalf("the boundary event names run %v, want %s for item_061", runs[0], runID)
			}
			if tick, _ := payload["tick_id"].(string); tick != "gen-1" {
				t.Fatalf("the boundary event names generation %v, want the dispatch pass it closed", payload["tick_id"])
			}
		}
	}
}

// A step that runs twice appends its judgment once.
//
// The guard is the object: a judgment is one event about one run, and the second call finds
// the first. This is the case a replayed tick or a re-run step would break, and it asserts the
// count rather than the absence of an error, because a second and third append are exactly
// what an idempotency claim that is only documented leaves behind.
func TestAGateStepThatRunsTwiceAppendsOneJudgment(t *testing.T) {
	dir := scratch(t, "replay")
	root := filepath.Join(dir, "repo")
	worktree := filepath.Join(dir, "worktree")
	for _, path := range []string{root, worktree} {
		if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
			t.Fatalf("creating %s failed: %v", path, err)
		}
	}
	t.Setenv("GATE_ROOT", root)
	gate := stub(t, "gate", judgingGate)
	path := filepath.Join(dir, "board.sqlite3")
	now := time.Date(2026, 11, 13, 6, 21, 56, 0, time.UTC)
	steps := &drain.ProcessSteps{
		RunBin: sibling(t, "run"), ItemBin: sibling(t, "item"), Store: path,
		Root: root, Now: func() time.Time { return now }, GateProgram: gate,
	}
	candidate := drain.Candidate{
		ItemID: "item_071", LaneID: laneName(1), RunID: "run-item_071-20261113T062156Z", TickID: "tick-20261113T062156Z-abc",
		Worktree: worktree, BaseSHA: "b1", HeadSHA: "h1",
	}
	for attempt := 1; attempt <= 2; attempt++ {
		if outcome, err := steps.Gate(context.Background(), candidate); err != nil {
			t.Fatalf("attempt %d of the gate step failed: %v", attempt, err)
		} else if outcome.Outcome != drain.GatePassed {
			t.Fatalf("attempt %d answered %q, want %q", attempt, outcome.Outcome, drain.GatePassed)
		}
	}
	counts := map[string]int{}
	for _, e := range eventsOf(t, path) {
		counts[e.Kind]++
	}
	if counts[drain.GatePlanKind] != 1 || counts[drain.GateVerdictKind] != 1 {
		t.Fatalf("two runs of one gate step appended %d gate.plan and %d gate.verdict event(s), want 1 and 1",
			counts[drain.GatePlanKind], counts[drain.GateVerdictKind])
	}
}

// A boundary bracket that found a violation still records its check, and the event names the
// run when the bracket wrapped one.
//
// The passing bracket is the case above; this is its negative, driven through the loop's own
// bracket so the stop and the event are the same moment's: a run that escapes leaves a
// recorded `boundary.check` whose first violation names the path, which is what the design's
// OBS-1 asks for and what a report-only stop could not answer after the process was gone.
func TestABoundaryViolationIsRecordedAsAnEvent(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", escapingAdapter))
	fixtureAt(t, f.store, itemRecord("item_081", "in-queue"))
	t.Setenv("GATE_ROOT", f.root)
	t.Setenv("ESCAPE_LANE", filepath.Join(f.laneRoot, "lane-2", "worktree"))
	gate := stub(t, "gate", judgingGate)
	loop := f.newLoop(t, 2, f.steps(t, gate, always(t, "land", 0), always(t, "verdict", 0)))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || faultOf(t, f.store, "item_081").Reason != drain.StopHostBoundaryViolation {
		t.Fatalf("the boundary fault was not held: %+v", result)
	}
	found := false
	for _, e := range eventsOf(t, f.store) {
		if e.Kind != drain.BoundaryCheckKind {
			continue
		}
		payload := eventPayload(t, e)
		if payload["clean"] != false || payload["scope"] != drain.BoundaryScopeRun {
			t.Fatalf("the violating bracket recorded scope %v clean %v, want a dirty run", payload["scope"], payload["clean"])
		}
		first, _ := payload["first_violation"].(map[string]any)
		if first == nil || first["kind"] == "" || first["detail"] == "" {
			t.Fatalf("the violating bracket recorded no first_violation: %v", payload)
		}
		found = true
	}
	if !found {
		t.Fatalf("a violating bracket appended no boundary.check event: %v", kinds(eventsOf(t, f.store)))
	}
}

// escapingAdapter commits its candidate and then writes outside its worktree, which is the
// one thing the bracket exists to catch.
const escapingAdapter = counting + committingAdapterBody + `echo escape > "$ESCAPE_LANE/escape.txt"
printf 'RESULT: done\n'
`

// A judgment the loop cannot record fails the step it belongs to, rather than being
// swallowed: an event stream with a silent hole is worse than the file-only state this
// replaces.
//
// The fault is a gate record the loop cannot read — the file exists and its payload is not
// the field map an event merges — which is one of the two ways the recording fails (the other
// is the store refusing the append). Both reach the same place: the error leaves
// `recordGateJudgment`, so the gate step answers an error and the tick fails with the
// candidate unjudged. It is driven through the loop rather than the step so the case pins the
// outcome an operator sees: no landing, and a loop that stopped.
func TestAJudgmentTheLoopCannotRecordFailsTheStep(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_091", "in-queue"))
	t.Setenv("GATE_ROOT", f.root)
	steps := f.steps(t, stub(t, "gate", unreadableGate), always(t, "land", 0), always(t, "verdict", 0))
	loop := f.newLoop(t, 1, steps)
	if _, err := loop.Run(context.Background()); err == nil {
		t.Fatal("a loop whose gate judgment could not be recorded answered no error")
	}
	if counts := len(eventsOf(t, f.store)); counts == 0 {
		t.Fatal("the fixture store holds no event at all, so the case proves nothing")
	}
}

// unreadableGate is a gate stub that writes a gate.plan record whose payload is not the field
// map an event merges, and answers a pass: the judgment happened and the loop cannot record
// it.
const unreadableGate = counting + `run=; prev=
for a in "$@"; do
  if [ "$prev" = "--run" ]; then run=$a; fi
  prev=$a
done
dir="$GATE_ROOT/.tillr/loop/$run"
mkdir -p "$dir"
printf '{"record_version":1,"kind":"gate.plan","id":"plan-1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-13T06:21:56Z","payload":"not-a-field-map"}\n' > "$dir/gate.plan.json"
exit 0
`

// spansGate writes a plan, a verdict and a spans file, the way the real gate writes the
// same three. The spans path is the one the step handed it in the environment, so the case
// proves the hand-off as well as the insert.
const spansGate = counting + `run=; prev=
for a in "$@"; do
  if [ "$prev" = "--run" ]; then run=$a; fi
  prev=$a
done
dir="$GATE_ROOT/.tillr/loop/$run"
mkdir -p "$dir"
plan='{"plan_id":"plan-1","registry_digest":"d1","base_sha":"b1","tree_sha":"t1","checks":[]}'
verdict='{"verdict_id":"verdict-1","plan_id":"plan-1","registry_digest":"d1","base_sha":"b1","tree_sha":"t1","result":"pass","checks":[]}'
printf '{"record_version":1,"kind":"gate.plan","id":"plan-1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-15T05:28:33Z","payload":%s}\n' "$plan" > "$dir/gate.plan.json"
printf '{"record_version":1,"kind":"gate.verdict","id":"verdict-1","producer":{"system":"tillr","utility":"gate","version":"0.1.0"},"created_at":"2026-11-15T05:28:33Z","payload":%s}\n' "$verdict" > "$dir/gate.verdict.json"
if [ -n "$TILLR_GATE_SPANS_OUT" ]; then
  cat > "$TILLR_GATE_SPANS_OUT" <<'JSON'
{"plan_span":{"check_id":"plan","kind":"plan","label":"gate plan","status":"verified","start_ns":1700000000000000000,"end_ns":1700000000178057000,"waited_ms_json":"{}","attributes_json":"{}"},"check_spans":[{"check_id":"gate/python3","kind":"gate","label":"gate:python3","status":"ok","start_ns":1700000000001000000,"end_ns":1700000000175820000,"waited_ms_json":"{\"pool\":12.5}","attributes_json":"{}"}],"skipped_checks":[{"check_id":"documents/uvx","kind":"documents","label":"documents","reason":"no tracked Markdown changed","planned":false,"owed":false}],"summary":{"checks":1,"executed":1,"skipped":1,"outcome":"verified"}}
JSON
fi
exit 0
`

// TestGateSpansLandInTheStore is the measurement half of the gate judgment: the spans the
// gate writes land in `gate_spans` in the verdict's own transaction, keyed by
// hash(verdict_id || '/' || check_id), with the trace and parent that make the chain
// tick -> run -> verdict -> check complete. The skipped step stays in the file and is not a
// not-run row.
func TestGateSpansLandInTheStore(t *testing.T) {
	dir := scratch(t, "spans")
	root := filepath.Join(dir, "repo")
	worktree := filepath.Join(dir, "worktree")
	for _, path := range []string{root, worktree} {
		if err := os.MkdirAll(filepath.Join(path, ".git"), 0o755); err != nil {
			t.Fatalf("creating %s failed: %v", path, err)
		}
	}
	t.Setenv("GATE_ROOT", root)
	gate := stub(t, "gate", spansGate)
	path := filepath.Join(dir, "board.sqlite3")
	now := time.Date(2026, 11, 15, 5, 28, 33, 0, time.UTC)
	steps := &drain.ProcessSteps{
		Store: path, Root: root, Now: func() time.Time { return now }, GateProgram: gate,
	}
	candidate := drain.Candidate{
		ItemID: "item_101", LaneID: laneName(1), RunID: "run-item_101-20261115T052833Z",
		TickID: "tick-20261115T052833Z-abc", Worktree: worktree, BaseSHA: "b1", HeadSHA: "h1",
	}
	if _, err := steps.Gate(context.Background(), candidate); err != nil {
		t.Fatalf("the gate step failed: %v", err)
	}
	s, err := store.OpenRead(context.Background(), path)
	if err != nil {
		t.Fatalf("opening the fixture store failed: %v", err)
	}
	defer s.Close()
	type row struct {
		spanID, traceID, parent, verdictID, runID, passID, checkID, kind, status string
		start, end                                                               int64
	}
	var rows []row
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		got, err := r.Query(`SELECT span_id, trace_id, parent_span_id, verdict_id, run_id,
			tick_id, check_id, kind, status, start_unix_nano, end_unix_nano
			FROM gate_spans ORDER BY start_unix_nano`)
		if err != nil {
			return err
		}
		defer got.Close()
		for got.Next() {
			var one row
			if err := got.Scan(&one.spanID, &one.traceID, &one.parent, &one.verdictID,
				&one.runID, &one.passID, &one.checkID, &one.kind, &one.status,
				&one.start, &one.end); err != nil {
				return err
			}
			rows = append(rows, one)
		}
		return got.Err()
	}); err != nil {
		t.Fatalf("reading the gate spans failed: %v", err)
	}
	// The plan span and one executed check. The skipped step is in the file, not here.
	if len(rows) != 2 {
		t.Fatalf("the store holds %d gate span(s), want the plan and one check: %+v", len(rows), rows)
	}
	for _, one := range rows {
		if one.verdictID != "verdict-1" || one.runID != candidate.RunID || one.passID != candidate.TickID {
			t.Fatalf("the span's identity is %+v, want verdict-1 on the run and tick that judged it", one)
		}
		if one.traceID == "" || one.parent == "" || one.spanID == "" {
			t.Fatalf("the span's hash chain is incomplete: %+v", one)
		}
	}
	if rows[0].checkID != "plan" || rows[1].checkID != "gate/python3" {
		t.Fatalf("the store holds check ids %q and %q, want plan and gate/python3",
			rows[0].checkID, rows[1].checkID)
	}
	if rows[0].start != 1_700_000_000_000_000_000 || rows[1].end != 1_700_000_000_175_820_000 {
		t.Fatalf("the store's nanosecond bounds are %d..%d and %d..%d, want the file's exact values",
			rows[0].start, rows[1].end, rows[0].start, rows[1].end)
	}
}
