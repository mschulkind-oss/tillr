package drain_test

import (
	"context"
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/record"
	"github.com/mschulkind-oss/tillr/store"
	_ "modernc.org/sqlite"
)

// These cases are OBS-1: the loop's evidence has to survive the next tick.
//
// Three gaps made a timeline built afterwards a plausible record rather than a truthful one.
// A lane is reused, so `<lane-dir>/brief.md` was the *last* run's brief and a reader had no
// way to know it was not this run's; the loop's `per-item pass` decisions lived in memory while
// only `once` wrote `dispatches`, so a held item's reason vanished with the process; and a
// step's stderr was read and thrown away, so "the gate failed" had no text behind it. Each
// case below fails if its half of the fix is removed.

// runInputs reads the three staged inputs a run's own directory keeps.
func runInputs(t *testing.T, root, runID string) map[string]string {
	t.Helper()
	dir := drain.RunDir(root, runID)
	got := map[string]string{}
	for _, name := range []string{drain.BriefFile, drain.FenceFile, drain.BaseSHAFile} {
		body, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatalf("the run %s kept no %s: %v", runID, name, err)
		}
		got[name] = string(body)
	}
	return got
}

// Staging keeps each run's brief, fence and base sha after the lane has moved on.
//
// The lane is reused on purpose: the second run is staged from a trunk that has moved and for
// a different item, with a different fence, so every one of the three files differs between
// the two runs. What the first run was given is then only readable from its own directory.
//
// Negative control: writing the three files into the lane directory alone (the shape before
// this change) leaves the first run with no record at all, and a copy taken from the lane at
// read time answers the second run's brief — the exact "item 1 ran under item 2's brief" the
// observatory design exists to make impossible.
func TestStagingKeepsEachRunsBriefFenceAndBaseSHAAfterTheLaneMovesOn(t *testing.T) {
	f := newStageFixture(t,
		fencedItemRecord("item_041", "in-queue", "tools/tillr-drain/"),
		fencedItemRecord("item_042", "in-queue", "tools/tillr-loop/"),
	)
	stager := f.stager()
	ctx := context.Background()

	first := f.spec("lane-1")
	first.RunID = "run-item_041-20261112T000000Z"
	first.Node = &drain.WorkflowStage{ID: "work", Kind: "agent", Access: "write", Prompt: "build", WriteScope: drain.WorkflowScope{Paths: []string{"tools/tillr-drain/"}}}
	if err := stager.Stage(ctx, first); err != nil {
		t.Fatalf("staging the first run failed: %v", err)
	}
	// The trunk moves between the two ticks, so the second run's base sha is not the first's.
	writeFile(t, filepath.Join(f.root, "base.txt"), "base\nmoved\n")
	git(t, f.root, "commit", "-qam", "trunk moved")
	second := f.spec("lane-1")
	second.ItemID = "item_042"
	second.RunID = "run-item_042-20261112T000010Z"
	second.Node = &drain.WorkflowStage{ID: "work", Kind: "agent", Access: "write", Prompt: "build", WriteScope: drain.WorkflowScope{Paths: []string{"tools/tillr-view/"}}}
	second.BaseSHA = git(t, f.root, "rev-parse", "main")
	if second.BaseSHA == first.BaseSHA {
		t.Fatalf("the fixture's trunk did not move, so the two base shas are one and the copy under test is invisible")
	}
	if err := stager.Stage(ctx, second); err != nil {
		t.Fatalf("staging the second run on the same lane failed: %v", err)
	}

	kept := runInputs(t, f.root, first.RunID)
	if !strings.Contains(kept[drain.BriefFile], "item_041") {
		t.Fatalf("the first run's kept brief is not its own (item_041 is not in it):\n%s", kept[drain.BriefFile])
	}
	if strings.Contains(kept[drain.BriefFile], "item_042") {
		t.Fatalf("the first run's kept brief names the second run's item:\n%s", kept[drain.BriefFile])
	}
	// The kept fence is the first run's own, mirror line and all: it names item_041's path,
	// the pack's copy of that module and the gate companions the derivation adds for it, and
	// nothing of the second run's.
	wantFence := "tools/tillr-drain/\n" +
		"yolo-packs/tillr/files/local/share/tillr/tillr-drain/\n" +
		"yolo-packs/tillr/skills/tillr-operating/SKILL.md\n" +
		"tools/tillr/tillrdocs/\n" +
		"yolo-packs/tillr/files/local/share/tillr/tillr/tillrdocs/\n" +
		"tools/tillr/buildstamp/support-revision\n" +
		"yolo-packs/tillr/files/local/share/tillr/tillr/buildstamp/support-revision"
	if fence := strings.TrimSpace(kept[drain.FenceFile]); fence != wantFence {
		t.Fatalf("the first run's kept fence is %q, want its own staged scope %q", fence, wantFence)
	}
	if base := strings.TrimSpace(kept[drain.BaseSHAFile]); base != first.BaseSHA {
		t.Fatalf("the first run's kept base sha is %q, want the %s it was staged from", base, first.BaseSHA)
	}

	moved := runInputs(t, f.root, second.RunID)
	if !strings.Contains(moved[drain.BriefFile], "item_042") {
		t.Fatalf("the second run's kept brief is not its own:\n%s", moved[drain.BriefFile])
	}
	wantMoved := "tools/tillr-view/\n" +
		"yolo-packs/tillr/files/local/share/tillr/tillr-view/\n" +
		"yolo-packs/tillr/skills/tillr-operating/SKILL.md\n" +
		"tools/tillr/tillrdocs/\n" +
		"yolo-packs/tillr/files/local/share/tillr/tillr/tillrdocs/\n" +
		"tools/tillr/buildstamp/support-revision\n" +
		"yolo-packs/tillr/files/local/share/tillr/tillr/buildstamp/support-revision"
	if fence := strings.TrimSpace(moved[drain.FenceFile]); fence != wantMoved {
		t.Fatalf("the second run's kept fence is %q, want its own staged scope %q", fence, wantMoved)
	}
	if base := strings.TrimSpace(moved[drain.BaseSHAFile]); base != second.BaseSHA {
		t.Fatalf("the second run's kept base sha is %q, want the %s it was staged from", base, second.BaseSHA)
	}

	// The lane directory is the second run's: it is the one place a lane-local read still
	// gets the wrong answer, which is why the run directory is the one a timeline reads.
	laneBrief, err := os.ReadFile(filepath.Join(f.laneRoot, "lane-1", drain.BriefFile))
	if err != nil {
		t.Fatalf("reading the lane's brief failed: %v", err)
	}
	if !strings.Contains(string(laneBrief), "item_042") {
		t.Fatalf("the lane directory no longer holds the latest run's brief:\n%s", laneBrief)
	}
}

// readJSONFile decodes one JSON file a face wrote.
func readJSONFile(t *testing.T, path string) map[string]any {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s failed: %v", path, err)
	}
	var body map[string]any
	if err := json.Unmarshal(raw, &body); err != nil {
		t.Fatalf("decoding %s failed: %v\n%s", path, err, raw)
	}
	return body
}

// tickingClock advances one second per read for cases that exercise timing records.
// Decision and run identities do not depend on this clock.
func tickingClock(from time.Time) func() time.Time {
	next := from
	return func() time.Time {
		at := next
		next = next.Add(time.Second)
		return at
	}
}

// The loop records every distinct decision in `dispatches`, not only in the line it prints.
// The loop records applied transitions, not holds or repeated observations.
// With one lane and two queued items each item records dispatch, settlement and
// judgment exactly once. Plans and the fixed-point stop leave no extra row.
func TestTheLoopRecordsOnlyAppliedTransitionsInTheDispatchesTable(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
	)
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	loop := f.newLoopAt(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, io.Discard,
		tickingClock(time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)))

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Dispatched != 2 || result.Landed != 2 {
		t.Fatalf("the loop stopped %q after dispatching %d and landing %d, want %q after 2 and 2: %s",
			result.Stop, result.Dispatched, result.Landed, drain.StopQueueDrained, result.Detail)
	}

	byItem := map[string][]dispatchRow{}
	for _, row := range dispatchRows(t, f.store) {
		byItem[row.ItemID] = append(byItem[row.ItemID], row)
	}
	if len(byItem) != 2 {
		t.Fatalf("the store holds decisions about %d item(s), want both: %+v", len(byItem), byItem)
	}
	for itemID, rows := range byItem {
		if len(rows) != 3 {
			t.Fatalf("%s recorded observations beyond its three transitions: %+v", itemID, rows)
		}
		// Scheduling can observe a live run or its terminal record. Either way,
		// each effect has one immutable decision, not one row per clock reading.
		for _, action := range []string{drain.ActionDispatch, drain.ActionSettle, drain.ActionJudge} {
			count := 0
			for _, row := range rows {
				if row.Action == action {
					count++
				}
			}
			if count != 1 {
				t.Fatalf("%s has %d %s decisions, want one: %+v", itemID, count, action, rows)
			}
		}
	}

	// Both items are eventually dispatched on the same lane; the capacity hold
	// before the second dispatch is only a plan, not a stored transition.
	first, second := byItem["item_041"][0], byItem["item_042"][0]
	if first.Action != drain.ActionDispatch || first.Reason != drain.ReasonReady {
		t.Fatalf("the first tick's decision about item_041 is %s/%s, want %s/%s",
			first.Action, first.Reason, drain.ActionDispatch, drain.ReasonReady)
	}
	if first.LaneID != "lane-1" || !strings.HasPrefix(first.RunID, "run-item_041-") {
		t.Fatalf("item_041's dispatch row names lane %q and run %q, want lane-1 and the run it was sent to",
			first.LaneID, first.RunID)
	}
	if second.Action != drain.ActionDispatch || second.Reason != drain.ReasonReady {
		t.Fatalf("item_042's first recorded transition is %s/%s, want dispatch/ready", second.Action, second.Reason)
	}
	var sent dispatchRow
	for _, row := range byItem["item_042"] {
		if row.Action == drain.ActionDispatch {
			sent = row
		}
	}
	if sent.Action != drain.ActionDispatch || sent.Reason != drain.ReasonReady {
		t.Fatalf("the second tick's decision about item_042 is %s/%s, want %s/%s",
			sent.Action, sent.Reason, drain.ActionDispatch, drain.ReasonReady)
	}
	if sent.LaneID != "lane-1" || !strings.HasPrefix(sent.RunID, "run-item_042-") {
		t.Fatalf("item_042's dispatch row names lane %q and run %q, want lane-1 and the run it was sent to",
			sent.LaneID, sent.RunID)
	}
	// The fixed-point pass writes nothing after the last judgment.
	last := byItem["item_041"][len(byItem["item_041"])-1]
	if last.Action != drain.ActionJudge {
		t.Fatalf("last recorded transition is %s, want judge", last.Action)
	}
}

// The loop writes its outcome beside the runs' records when it stops.
//
// A stop reason exists only on the caller's terminal today, so a night's answer to "why did it
// stop" dies with the shell that ran it. The file is a `drain.loop` record, the same envelope
// shape the gate's and the land's records use, so one reader can walk the whole directory.
//
// Negative control: without the write the file is absent and the case fails reading it.
func TestTheLoopWritesItsResultWhenItStops(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("the loop stopped %q, want %q: %s", result.Stop, drain.StopQueueDrained, result.Detail)
	}

	// The clock the loop was given is the fixture's fixed one, so the file it names is fixed
	// too: drain-<the tick's stamp>.json.
	path := drain.LoopResultFile(f.root, result.FirstGeneration, result.LastGeneration)
	body := readJSONFile(t, path)
	if body["kind"] != drain.LoopResultKind {
		t.Fatalf("%s is a %q record, want %s", path, body["kind"], drain.LoopResultKind)
	}
	env, err := record.Decode(mustJSON(t, body))
	if err != nil {
		t.Fatalf("the loop's result at %s is not a decodable record: %v", path, err)
	}
	var kept drain.LoopResult
	if err := json.Unmarshal(env.Payload, &kept); err != nil {
		t.Fatalf("decoding the loop's result payload failed: %v", err)
	}
	if kept.Stop != result.Stop || kept.Ticks != result.Ticks || kept.Dispatched != result.Dispatched || kept.Landed != result.Landed {
		t.Fatalf("the recorded result is %+v, want the %+v the loop answered", kept, result)
	}
	if kept.Dispatched != 1 || kept.Landed != 1 {
		t.Fatalf("the recorded result dispatched %d and landed %d, want 1 and 1", kept.Dispatched, kept.Landed)
	}
}

// mustJSON re-encodes a decoded body, so a case can hand it to the record decoder.
func mustJSON(t *testing.T, body map[string]any) []byte {
	t.Helper()
	raw, err := json.Marshal(body)
	if err != nil {
		t.Fatalf("re-encoding the record failed: %v", err)
	}
	return raw
}

// stepSpan is one line of a run's steps.jsonl, as a case reads it.
type stepSpan struct {
	Step         string   `json:"step"`
	Argv         []string `json:"argv"`
	Cwd          string   `json:"cwd"`
	StartedAtMS  int64    `json:"started_at_ms"`
	FinishedAtMS int64    `json:"finished_at_ms"`
	ExitCode     *int     `json:"exit_code"`
	StderrTail   string   `json:"stderr_tail"`
}

// stepSpans reads one run's step file, in the order the steps were written.
func stepSpans(t *testing.T, root, runID string) []stepSpan {
	t.Helper()
	path := filepath.Join(drain.RunDir(root, runID), drain.StepsFile)
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading the run's step file %s failed: %v", path, err)
	}
	spans := []stepSpan{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var span stepSpan
		if err := json.Unmarshal([]byte(line), &span); err != nil {
			t.Fatalf("decoding a step span from %s failed: %v\n%s", path, err, line)
		}
		spans = append(spans, span)
	}
	return spans
}

// The loop records a span for every step of a run: the run, the gate, the land, the verdict
// and the item state move.
//
// The stderr tail is the half that did not exist at all: `ProcessSteps` read a step's stderr
// and dropped it, so "the gate failed" reached the record with no text behind it. The gate
// stub writes one marker line and passes, which is the case that matters — a step that
// *succeeded* still has stderr worth keeping, and a tail taken only on failure would be
// missing here.
//
// Negative control: with the span writes removed the file is absent; with the stderr dropped
// the gate's line is empty, and the case fails on that assertion alone.
func TestTheLoopRecordsAStepSpanForEveryStepItRuns(t *testing.T) {
	const marker = "gate: read the candidate check registry"
	gate := stub(t, "gate", "echo '"+marker+"' >&2\n"+counting+"exit 0\n")
	land := stub(t, "land", "echo 'land: trunk carried' >&2\n"+counting+"exit 0\n")
	verdict := stub(t, "verdict", "echo 'verdict: two criteria retired' >&2\n"+counting+"exit 0\n")

	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	steps := f.steps(t, gate, land, verdict)
	loop := f.newLoop(t, 1, steps)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Landed != 1 {
		t.Fatalf("the loop landed %d candidate(s), want 1: %s", result.Landed, result.Detail)
	}

	// The run id is the plan's: the same id the store records in `runs`, which is what makes
	// the step file and the run row one run.
	spans := stepSpans(t, f.root, "run-item_041-a1")
	want := []string{"run", "gate", "land", "verdict", "item-state"}
	if len(spans) != len(want) {
		t.Fatalf("the run recorded %d step span(s), want %d (%s): %+v", len(spans), len(want), strings.Join(want, ", "), spans)
	}
	byStep := map[string]stepSpan{}
	for i, span := range spans {
		if span.Step != want[i] {
			t.Fatalf("step %d is %q, want %q (the loop's own order): %+v", i, span.Step, want[i], spans)
		}
		if len(span.Argv) == 0 || span.Cwd == "" {
			t.Fatalf("the %s span carries argv %v and cwd %q, want both named", span.Step, span.Argv, span.Cwd)
		}
		if span.ExitCode == nil || *span.ExitCode != 0 {
			t.Fatalf("the %s span carries exit %v, want 0: the stub passed", span.Step, span.ExitCode)
		}
		if span.FinishedAtMS < span.StartedAtMS {
			t.Fatalf("the %s span ends (%d) before it starts (%d)", span.Step, span.FinishedAtMS, span.StartedAtMS)
		}
		byStep[span.Step] = span
	}

	// The gate runs in the candidate's worktree, which is the argument the loop's own
	// boundary is built on: a span that named the repository would say the gate judged the
	// wrong tree.
	if want := filepath.Join(f.laneRoot, "lane-1", drain.WorktreeDir); byStep["gate"].Cwd != want {
		t.Fatalf("the gate span's cwd is %q, want the candidate worktree %q", byStep["gate"].Cwd, want)
	}
	for _, step := range []string{"gate", "land", "verdict"} {
		if !strings.Contains(byStep[step].StderrTail, step+":") {
			t.Fatalf("the %s span's stderr tail is %q, want the line the stub wrote", step, byStep[step].StderrTail)
		}
	}
	// The argv is the program and the words it was given, not a summary: a reader can re-run
	// the command from the span.
	if argv := strings.Join(byStep["gate"].Argv, " "); !strings.Contains(argv, "--item item_041") || !strings.Contains(argv, "--head") {
		t.Fatalf("the gate span's argv is %q, want the candidate flags the loop passed", argv)
	}
	if argv := strings.Join(byStep["item-state"].Argv, " "); !strings.Contains(argv, "state") || !strings.Contains(argv, "landed") {
		t.Fatalf("the item-state span's argv is %q, want the state move the loop made", argv)
	}
	// The run step's span names the utility and the request file it was handed.
	if argv := strings.Join(byStep["run"].Argv, " "); !strings.Contains(argv, "exec") || !strings.Contains(argv, ".request.json") {
		t.Fatalf("the run span's argv is %q, want `exec ... --input <run>.request.json`", argv)
	}
}

// A step that could not be started records no exit code rather than a zero: a missing program
// made no exit, and a span claiming 0 for it is the missing-measurement-as-zero defect.
func TestAStepThatNeverStartedRecordsNoExitCode(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	// A gate program that is a path with no file behind it: exec fails before any process
	// exists, which is the one way a step has no exit status.
	missing := filepath.Join(scratch(t, "no-program"), "not-a-gate")
	steps := f.steps(t, missing, always(t, "land", 0), always(t, "verdict", 0))
	loop := f.newLoop(t, 1, steps)

	// The run step's own span is written before the gate's, so the gate's failure leaves the
	// run's row behind: the case reads the gate's line out of a file that exists.
	if result, err := loop.Run(context.Background()); err != nil || result.Stop != drain.StopQueueDrained || result.Landed != 0 {
		t.Fatalf("the missing gate did not hold just its item: %+v / %v", result, err)
	}
	s, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := drain.ReadItemFaults(r)
		if err == nil && faults["item_041"].Reason != drain.StopGateHarnessError {
			t.Errorf("missing gate not recorded: %+v", faults)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	spans := stepSpans(t, f.root, "run-item_041-a1")
	var gate *stepSpan
	for i := range spans {
		if spans[i].Step == "gate" {
			gate = &spans[i]
		}
	}
	if gate == nil {
		t.Fatalf("the unstartable gate left no span: %+v", spans)
	}
	if gate.ExitCode != nil {
		t.Fatalf("the gate that never started recorded exit code %d, want null", *gate.ExitCode)
	}
	if gate.Argv[0] != missing {
		t.Fatalf("the gate span names %q, want the program the loop was given (%q)", gate.Argv[0], missing)
	}
}
