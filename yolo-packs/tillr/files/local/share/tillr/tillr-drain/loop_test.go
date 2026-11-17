package main_test

import (
	"context"
	"database/sql"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-run/run"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// stub writes one executable shell script and answers its path. Every program the loop
// drives is a script here, so a case spends no model tokens and needs no harness: the
// report a run writes is three lines of `sh`.
func stub(t *testing.T, name, body string) string {
	t.Helper()
	path := filepath.Join(scratch(t, "stub"), name)
	writeFile(t, filepath.Join(filepath.Dir(path), "pause-kind"), "stops-where-it-stands\n")
	writeFile(t, path, "#!/bin/sh\n"+body+"\n")
	if err := os.Chmod(path, 0o755); err != nil {
		t.Fatalf("making %s executable failed: %v", path, err)
	}
	return path
}

// counting is a stub body that appends its own name to $STUB_COUNT, so a case answers "how
// many times did this run" from a file rather than from a clock.
const counting = `if [ -n "$STUB_COUNT" ]; then echo "$0 $*" >> "$STUB_COUNT"; fi
`

// committingAdapterBody is the work a stub adapter does: it writes one file and commits it on
// the lane's branch, which is the candidate the loop reads.
const committingAdapterBody = `echo candidate > candidate.txt
git add candidate.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
`

// committingAdapter is a stub adapter: it writes one file, commits it on the lane's branch
// and prints a brief-conforming report footer. It is the whole of the model in these cases.
const committingAdapter = counting + committingAdapterBody + `printf 'RESULT: done\n'
`

// failingAdapter is a stub adapter that refuses before running anything: exit 2 with no
// report is the adapters' "ran nothing" exit, which is the infrastructure class.
const failingAdapter = counting + `exit 2
`

// loopFixture is one loop's fixture: a repository with a trunk, a lane root outside it, a
// store, and the four programs the loop drives.
type loopFixture struct {
	root     string
	laneRoot string
	store    string
	policy   *drain.Policy
	// count is the file every stub appends its invocation to, so a case reads what ran.
	count string
}

// newLoopFixture builds the fixture. adapter is the stub adapter program the policy names.
//
// The lane root and the store live outside the repository: a lane worktree inside it would
// be an untracked directory in the main checkout, and the store must not be inside any
// bracket at all (run writes it).
func newLoopFixture(t *testing.T, adapter string) loopFixture {
	t.Helper()
	root := filepath.Join(scratch(t, "loop-repo"), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("creating the fixture repository failed: %v", err)
	}
	git(t, root, "init", "-b", "main")
	writeFile(t, filepath.Join(root, "base.txt"), "base\n")
	git(t, root, "add", "base.txt")
	git(t, root, "commit", "-m", "base")
	// This repository ignores .tillr (see the repository's own gitignore), and the loop's
	// records live there: the fixture ignores it too, so a span written inside a boundary
	// bracket is not a change to the repository. It goes in info/exclude rather than a
	// tracked file so the fixture's base commit stays the one the case built.
	writeFile(t, filepath.Join(root, ".git", "info", "exclude"), ".tillr/\n")

	count := filepath.Join(scratch(t, "loop-count"), "count")
	t.Setenv("STUB_COUNT", count)
	laneRoot := filepath.Join(scratch(t, "loop-lanes"), "lanes")
	if err := os.MkdirAll(laneRoot, 0o755); err != nil {
		t.Fatalf("creating the fixture lane root failed: %v", err)
	}
	f := loopFixture{
		root:     root,
		laneRoot: laneRoot,
		store:    filepath.Join(scratch(t, "loop-store"), "board.sqlite3"),
		count:    count,
		policy: &drain.Policy{
			DefaultBackend: "fixture",
			Preference:     []string{"fixture"},
			Backends: map[string]drain.Backend{
				"fixture": {Adapter: adapter, Model: "fixture-model", TimeoutSeconds: 60},
			},
		},
	}
	return f
}

// steps answers the process steps the loop drives, with the three judging programs named.
//
// The run and item binaries are the real ones, built beside this module: they are the tillr
// utilities' own faces, and a stub in their place would let the loop's record of an
// outcome disagree with the store a later tick plans from — which is the defect these
// cases are about.
func (f loopFixture) steps(t *testing.T, gate, land, verdict string) *drain.ProcessSteps {
	t.Helper()
	now := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	return &drain.ProcessSteps{
		RunBin: sibling(t, "run"), ItemBin: sibling(t, "item"), Store: f.store, LaneRoot: f.laneRoot,
		Root:        f.root,
		Now:         func() time.Time { return now },
		GateProgram: gate, LandProgram: land, VerdictProgram: verdict,
	}
}

// passStager is the stager the loop's step-level cases drive: it stages no worktree and
// renders no brief, because those cases are about the order the loop keeps and the lane
// worktrees stageLanes already created are enough for them. It does write the lane's fence,
// which is the one staged artifact the loop itself reads back: a stager that wrote none
// would leave every candidate in these cases outside a fence that does not exist. The real
// stager has its own cases (stage_test.go).
type passStager struct {
	laneRoot string
	fence    []string
}

func (s passStager) Stage(_ context.Context, spec drain.StageSpec) error {
	return writeFence(s.laneRoot, spec.LaneID, s.fence)
}

// writeFence writes the fence staging writes: <lane-root>/<lane>/fence.txt, one repo-relative
// path per line.
func writeFence(laneRoot, lane string, entries []string) error {
	dir := filepath.Join(laneRoot, lane)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	return os.WriteFile(filepath.Join(dir, "fence.txt"), []byte(strings.Join(entries, "\n")+"\n"), 0o644)
}

// newLoop answers the loop this fixture describes, staging the fence the stub adapter's one
// committed file needs and reporting to stderr.
func (f loopFixture) newLoop(t *testing.T, lanes int, steps *drain.ProcessSteps) *drain.Loop {
	t.Helper()
	return f.newLoopWith(t, lanes, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, os.Stderr)
}

// newLoopWith is newLoop with the stager and the report writer named, so a case can read the
// line the loop prints for the decision it is about.
func (f loopFixture) newLoopWith(t *testing.T, lanes int, steps *drain.ProcessSteps, stage drain.Stager, report io.Writer) *drain.Loop {
	t.Helper()
	return f.newLoopAt(t, lanes, steps, stage, report, steps.Now)
}

// newLoopAt is newLoopWith with the loop's own clock named separately from the steps', so a
// case can give the loop a clock that moves between ticks while the adapters keep the
// fixed one, independently of attempt-based decision and run ids.
func (f loopFixture) newLoopAt(t *testing.T, lanes int, steps *drain.ProcessSteps, stage drain.Stager, report io.Writer, now func() time.Time) *drain.Loop {
	t.Helper()
	ids := []string{}
	for i := 1; i <= lanes; i++ {
		ids = append(ids, laneName(i))
	}
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: ids,
		Policy: f.policy, Steps: steps, Stage: stage,
		Now: now, Report: report,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	return loop
}

// laneName is the lane id the loop's own face names for lane n.
func laneName(n int) string { return "lane-" + strconv.Itoa(n) }

// fixtureAt seeds the loop's store from the records given, in the order given.
func fixtureAt(t *testing.T, path string, records ...string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.InitFixture(ctx, path)
	if err != nil {
		t.Fatalf("creating the fixture store failed: %v", err)
	}
	defer s.Close()
	raw, err := json.Marshal(map[string]any{
		"kind":    "fixture.seed",
		"payload": map[string]any{"records": rawRecords(records)},
	})
	if err != nil {
		t.Fatalf("building the seed bundle failed: %v", err)
	}
	if err := s.Seed(ctx, raw); err != nil {
		t.Fatalf("seeding the fixture store failed: %v", err)
	}
}

// always records real step calls, not startup help discovery. It models a legacy
// step with harmless help but no parse-only support.
func always(t *testing.T, name string, code int) string {
	return stub(t, name, "[ \"$1\" = --help ] && exit 0\n"+counting+"exit "+strconv.Itoa(code))
}

// lines reads a stub's record of what ran, one line per invocation.
func lines(t *testing.T, path string) []string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if os.IsNotExist(err) {
		return nil
	}
	if err != nil {
		t.Fatalf("reading %s failed: %v", path, err)
	}
	got := []string{}
	for _, line := range strings.Split(strings.TrimSpace(string(raw)), "\n") {
		if line != "" {
			got = append(got, line)
		}
	}
	return got
}

// readText answers one file's body, failing the case when it is not there: a stub writes it,
// so a missing file is the fixture failing rather than a state to report.
func readText(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s failed: %v", path, err)
	}
	return string(raw)
}

// Two consecutive ticks dispatch and record: the first tick's item is run, gated, landed and
// given a verdict, and the second tick's item is a different one — the item that already
// ran holds, which is A-NORETRY read through `runs.item_id`.
func TestTwoConsecutiveTicksDispatchAndRecord(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
	)
	gate := always(t, "gate", 0)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)

	loop := f.newLoop(t, 1, f.steps(t, gate, land, verdict))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	// Two items take dispatch, settlement and judgment in separate passes; no
	// count limits the work, and the freed lane is reused for the second item.
	if result.Stop != drain.StopQueueDrained || result.Ticks != 6 {
		t.Fatalf("the loop stopped %q after %d pass(es), want %q after 6", result.Stop, result.Ticks, drain.StopQueueDrained)
	}
	if result.Dispatched != 2 || result.Landed != 2 {
		t.Fatalf("the loop dispatched %d and landed %d, want 2 and 2", result.Dispatched, result.Landed)
	}
	// Each tick ran a different item: the second tick must not re-dispatch the item the
	// first one ran.
	runs := lines(t, f.count)
	adapterRuns := 0
	for _, line := range runs {
		if strings.Contains(line, "adapter") {
			adapterRuns++
		}
	}
	if adapterRuns != 2 {
		t.Fatalf("the adapter ran %d time(s), want one per tick: %v", adapterRuns, runs)
	}
	// One gate per tick, each on the item that tick dispatched: the two ticks are two
	// different items, which is the record the case is about.
	gates := lines(t, f.count)
	if countMatching(gates, "gate --item item_041") != 1 || countMatching(gates, "gate --item item_042") != 1 {
		t.Fatalf("the gate did not judge one candidate per tick: %v", gates)
	}
}

// spendBackends writes the given footer's extractor beside the adapter a run names and
// answers the backends directory a ProcessSteps is handed. The extractor belongs to the
// adapter's dialect, not to the backend's name, so it goes in the adapter's own directory:
// a backend named `fixture` whose adapter lives elsewhere would find no extractor there and
// every run's spend would read unknown. The real extractors read a harness stream; these
// cases are about the loop, not a dialect.
func spendBackends(t *testing.T, adapter, report string) string {
	t.Helper()
	dir := filepath.Dir(adapter)
	writeFile(t, filepath.Join(dir, "extract-report"),
		"#!/bin/sh\nfor a in \"$@\"; do if [ \"$prev\" = --out ]; then out=$a; fi; prev=$a; done\nprintf '"+report+"' > \"$out\"\n")
	if err := os.Chmod(filepath.Join(dir, "extract-report"), 0o755); err != nil {
		t.Fatalf("making the extractor executable failed: %v", err)
	}
	return dir
}

// The spend a run reports is measured through the whole chain — the stub adapter's run, the
// extractor beside the backend, the run record, the loop's own count — and it never stops
// the loop: the pile is the only throttle (docs/design/momentum-principle.md), so a total a
// night reached is not a reason to leave work queued.
//
// This case is the removed token ceiling's rewrite. It used to answer `max-spend-reached`
// one tick in, with a million tokens against a one-token ceiling; it now pins that the same
// million tokens buys the next item instead, and that both items are worked.
func TestTheLoopMeasuresWhatEveryRunSpentAndRunsOn(t *testing.T) {
	adapter := stub(t, "adapter", committingAdapter)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store,
		itemRecord("item_051", "in-queue"),
		itemRecord("item_052", "in-queue"),
	)
	gate := always(t, "gate", 0)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)
	steps := f.steps(t, gate, land, verdict)
	steps.BackendsDir = spendBackends(t, adapter, "# run report\ntokens: 1000000\ncost usd: 3.000000\n---\n")

	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{laneName(1)},
		Policy: f.policy, Steps: steps, Stage: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}},
		Now: steps.Now, Report: io.Discard,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Ticks != 6 || result.Landed != 2 {
		t.Fatalf("the loop stopped %q after %d tick(s) and landed %d, want %q after 2 and 2: %s",
			result.Stop, result.Ticks, result.Landed, drain.StopQueueDrained, result.Detail)
	}
	if result.Tokens != 2_000_000 || result.SpendUSD != 6 {
		t.Fatalf("the loop measured %d tokens and $%f, want 2000000 and $6 read from the runs' records",
			result.Tokens, result.SpendUSD)
	}
	if result.SpendUnknown != 0 || result.CostUnknown != 0 {
		t.Fatalf("both runs reported their spend, and the loop counted %d unknown tokens and %d unknown costs",
			result.SpendUnknown, result.CostUnknown)
	}
}

// The spend line is printed for every run and summed at the end, and there is no ceiling in
// the report to compare it against: the total is an introspection answer, not a wall.
//
// This case is the removed "no ceiling set" control's rewrite: it used to end at a tick
// budget with a line per run, and it now ends at the drained pile with the same lines.
func TestTheLoopPrintsOneSpendLinePerRunAndComparesNoCeiling(t *testing.T) {
	adapter := stub(t, "adapter", committingAdapter)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store,
		itemRecord("item_071", "in-queue"),
		itemRecord("item_072", "in-queue"),
	)
	gate := always(t, "gate", 0)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)
	steps := f.steps(t, gate, land, verdict)
	steps.BackendsDir = spendBackends(t, adapter, "# run report\ntokens: 1000000\ncost usd: 3.000000\n---\n")
	var report strings.Builder

	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{laneName(1)},
		Policy: f.policy, Steps: steps, Stage: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}},
		Now: steps.Now, Report: &report,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Ticks != 6 {
		t.Fatalf("the loop stopped %q (%s) after %d pass(es), want %q after 6",
			result.Stop, result.Detail, result.Ticks, drain.StopQueueDrained)
	}
	if result.Tokens != 2_000_000 || result.SpendUSD != 6 {
		t.Fatalf("the loop measured %d tokens and $%f, want 2000000 and $6", result.Tokens, result.SpendUSD)
	}
	if countMatching(strings.Split(report.String(), "\n"), "spent 1000000 tokens, $3.000000") != 2 {
		t.Fatalf("the loop did not print one spend line per run: %s", report.String())
	}
	if strings.Contains(report.String(), "ceiling") {
		t.Fatalf("the loop printed a ceiling it has no flag for: %s", report.String())
	}
}

// A backend that reports tokens and no cost is measured as unknown and the loop runs on:
// muse and agy are that shape, and a total that added their nil as zero would be a number
// nobody measured. Nothing here stops, which is the difference between recording an unknown
// and refusing to spend.
//
// This case is the removed dollar ceiling's rewrite: it used to answer `spend-unreported`
// on a $10 ceiling, and it now pins that the same cost-less run is worked and the total says
// how much of it is unmeasured.
func TestACostTheBackendNeverReportedIsMeasuredAsUnknownNotZero(t *testing.T) {
	adapter := stub(t, "adapter", committingAdapter)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, itemRecord("item_061", "in-queue"))
	gate := always(t, "gate", 0)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)
	steps := f.steps(t, gate, land, verdict)
	steps.BackendsDir = spendBackends(t, adapter, "# run report\ntokens: 42\ncost usd: unknown\n---\n")

	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{laneName(1)},
		Policy: f.policy, Steps: steps, Stage: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}},
		Now: steps.Now, Report: io.Discard,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Tokens != 42 || result.SpendUSD != 0 {
		t.Fatalf("the loop measured %d tokens and $%f, want 42 and $0", result.Tokens, result.SpendUSD)
	}
	if result.CostUnknown != 1 || result.CostUnknownExample == "" {
		t.Fatalf("%d run(s) reported no cost (first: %q), want 1 with the run named", result.CostUnknown, result.CostUnknownExample)
	}
	if result.Stop != drain.StopQueueDrained || result.Landed != 1 {
		t.Fatalf("a cost-less run stopped the loop at %q and landed %d, want %q and 1", result.Stop, result.Landed, drain.StopQueueDrained)
	}
}

// countMatching counts the invocations whose argv names needle. A bare program
// name matches the executable, not a directory or argument containing that word.
func countMatching(lines []string, needle string) int {
	n := 0
	for _, line := range lines {
		if !strings.ContainsAny(needle, " /") && !strings.HasPrefix(needle, "-") {
			words := strings.Fields(line)
			if len(words) > 0 && filepath.Base(words[0]) == needle {
				n++
			}
		} else if strings.Contains(line, needle) {
			n++
		}
	}
	return n
}

// A sibling-worktree violation holds the responsible item before gating or landing.
func TestABoundaryViolationHoldsTheItemAndNamesThePath(t *testing.T) {
	escape := stub(t, "escaping-adapter", counting+`echo escaped > "$STUB_ESCAPE/escaped.txt"
printf 'RESULT: done\n'
`)
	f := newLoopFixture(t, escape)
	t.Setenv("STUB_ESCAPE", filepath.Join(f.laneRoot, "lane-2", "worktree"))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))

	gate := always(t, "gate", 0)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)

	loop := f.newLoop(t, 2, f.steps(t, gate, land, verdict))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("the boundary hold stopped the loop %q", result.Stop)
	}
	s, err := store.OpenRead(context.Background(), f.store)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		faults, err := drain.ReadItemFaults(r)
		fault := faults["item_041"]
		if err == nil && (fault.Reason != drain.StopHostBoundaryViolation || !strings.Contains(fault.Detail, "escaped.txt") || fault.RunID == "" || fault.LaneID != "lane-1") {
			t.Errorf("boundary hold lost attribution: %+v", fault)
		}
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if again := lines(t, f.count); countMatching(again, "gate --item") != 0 {
		t.Fatalf("the loop gated a candidate after a boundary violation: %v", again)
	}
}

// A candidate failure is a verdict, not a stopping condition, and it does not fail over: the
// adapter answers, the gate says no, the loop records a failing verdict and moves on.
func TestACandidateFailureDoesNotStopTheLoopAndDoesNotFailOver(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
	)
	gate := always(t, "gate", 1)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)

	loop := f.newLoop(t, 1, f.steps(t, gate, land, verdict))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	// The loop ran both items and then found nothing dispatchable: a red gate is a verdict
	// about one candidate, never a reason to stop or to re-run it.
	if result.Stop != drain.StopQueueDrained || result.Ticks != 6 {
		t.Fatalf("a candidate failure stopped the loop after %d pass(es): %q (%s)", result.Ticks, result.Stop, result.Detail)
	}
	if result.Rejected != 2 || result.Landed != 0 {
		t.Fatalf("the loop rejected %d and landed %d, want 2 and 0", result.Rejected, result.Landed)
	}
	// Two ticks, two candidates, and the adapter ran exactly twice: one attempt per item,
	// because judged work never fails over.
	if runs := lines(t, f.count); countMatching(runs, "adapter") != 2 {
		t.Fatalf("the adapter ran %d time(s), want 2 with no failover: %v", countMatching(runs, "adapter"), runs)
	}
	// The stub writes "$0 $*", so the land program is named by its path and not by the
	// bare word: the scratch directory a lane runs under can hold "landed" (this lane's
	// does), which would match every invocation and turn this assertion into a tautology.
	if lands := lines(t, f.count); countMatching(lands, "/land ") != 0 {
		t.Fatalf("the loop landed a candidate the gate failed: %v", lands)
	}
	// A red gate is a refusal, not a verdict (MQ1): the criteria a judge was shown are not
	// retired by a judge that said no, so no failing verdict is frozen against either item.
	if verdicts := lines(t, f.count); countMatching(verdicts, "--verdict fail") != 0 {
		t.Fatalf("the loop recorded a failing verdict for a red gate: %v", verdicts)
	}
}

// Consecutive infrastructure failures do not stop the loop: it works every item it can and
// ends where the pile ends. Nothing about an item was learned from a run that ran nothing, so
// each item is held as `run-infra-exhausted` and each attempt is reported with its reason.
//
// This case is the removed circuit breaker's rewrite. It used to stop with
// `infra-circuit-breaker` at the third failure, which is a stop with work still queued; it
// now pins that three failures buy three attempts and no stop, and that no backoff was
// applied — a tick that dispatched a run always has work to be getting on with, so the wait
// (which belongs to a tick that dispatched nothing) never rations it.
func TestConsecutiveInfrastructureFailuresDoNotStopTheLoop(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", failingAdapter))
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
		itemRecord("item_043", "in-queue"),
	)
	gate := always(t, "gate", 0)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)
	var report strings.Builder

	loop := f.newLoopWith(t, 1, f.steps(t, gate, land, verdict),
		passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, &report)
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("the loop stopped %q (%s), want %q: a fault is not a budget",
			result.Stop, result.Detail, drain.StopQueueDrained)
	}
	if result.InfraFailures != 3 || result.Ticks != 6 {
		t.Fatalf("the loop saw %d infrastructure failure(s) over %d tick(s), want 3 over 3",
			result.InfraFailures, result.Ticks)
	}
	if result.Rejected != 0 || result.Dispatched != 3 {
		t.Fatalf("the loop dispatched %d and rejected %d, want 3 and 0: a failed harness is not a verdict",
			result.Dispatched, result.Rejected)
	}
	if gates := lines(t, f.count); countMatching(gates, "gate --item") != 0 {
		t.Fatalf("the loop gated a run that failed in the infrastructure class: %v", gates)
	}
	// One report line per failure, each naming the attempt, and no wait: every tick here had
	// an item to dispatch, so there was nothing for a backoff to make visible.
	if countMatching(strings.Split(report.String(), "\n"), "failed in the infrastructure class") != 3 {
		t.Fatalf("the loop did not report one line per infrastructure failure: %s", report.String())
	}
	if strings.Contains(report.String(), "waiting") {
		t.Fatalf("the loop backed off while it had items it could dispatch: %s", report.String())
	}
}

// The one-hop failover, end to end: the first backend's adapter fails in the infrastructure
// class, and the loop re-dispatches the same item once on the next backend in the policy's
// preference, after resetting the lane. The run face writes both records, so the store — not
// the loop's echo — is what the case reads for the backends and the run ids.
//
// The first attempt leaves a commit before it fails, which is the leftover the reset is for:
// staging refuses a lane holding an unlanded commit, so the failover's own reset is what
// makes the same worktree runnable on the second backend. The second adapter records the
// base it was given and whether that commit is still in the tree, so the assertion about
// the trunk is the adapter's reading rather than an assumption about the reset's code.
//
// The negative controls are the run rows: a loop that re-dispatched without walking the
// policy would leave one row on `pi`, and a loop that re-dispatched without resetting would
// leave the second adapter's base at the first attempt's commit instead of the trunk.
func TestAnInfrastructureFailureFailsOverOnceToTheNextBackend(t *testing.T) {
	pi := stub(t, "pi-adapter", counting+`echo leftover > leftover.txt
git add leftover.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m leftover
exit 4
`)
	baseFile := filepath.Join(scratch(t, "failover-base"), "base")
	leftoverFile := filepath.Join(scratch(t, "failover-leftover"), "leftover")
	agy := stub(t, "agy-adapter", counting+`git rev-parse HEAD > "$STUB_BASE"
if [ -e leftover.txt ]; then echo present > "$STUB_LEFTOVER"; else echo absent > "$STUB_LEFTOVER"; fi
echo candidate > candidate.txt
git add candidate.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
printf 'RESULT: done\n'
`)
	t.Setenv("STUB_BASE", baseFile)
	t.Setenv("STUB_LEFTOVER", leftoverFile)

	f := newLoopFixture(t, pi)
	f.policy = &drain.Policy{
		DefaultBackend: "pi",
		Preference:     []string{"pi", "agy"},
		Backends: map[string]drain.Backend{
			"pi":  {Adapter: pi, Model: "pi-model", TimeoutSeconds: 60},
			"agy": {Adapter: agy, Model: "agy-model", TimeoutSeconds: 60},
		},
	}
	fixtureAt(t, f.store, fencedItemRecord("item_041", "in-queue", "candidate.txt"))
	trunk := git(t, f.root, "rev-parse", "main")

	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatalf("encoding the policy failed: %v", err)
	}
	policyPath := filepath.Join(scratch(t, "failover-policy"), "policy.json")
	writeFile(t, policyPath, string(raw))

	cmd := exec.Command(bin, "loop", "--exit-when-drained",
		"--root", f.root, "--store", f.store, "--lane-root", f.laneRoot,
		"--lanes", "1", "--policy", policyPath,
		"--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"),
		"--brief-template", filepath.Join("..", "tillr-run", "backends", "brief-template.md"),
		"--gate", always(t, "gate", 0), "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0),
		"--trace-bin", "/bin/true",
	)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("the loop exited %v: %s", err, out)
	}

	runs := runsForItem(t, f.store, "item_041")
	if len(runs) != 2 {
		t.Fatalf("the store holds %d run(s) for item_041, want 2: %+v", len(runs), runs)
	}
	if runs[0].Backend != "pi" {
		t.Fatalf("the first run names backend %q, want pi: %+v", runs[0].Backend, runs)
	}
	if runs[1].Backend != "agy" {
		t.Fatalf("the second run names backend %q, want agy: %+v", runs[1].Backend, runs)
	}
	if runs[1].RunID != "run-item_041-a2" {
		t.Fatalf("the failover run is %q, want run-item_041-a2", runs[1].RunID)
	}
	if got := strings.TrimSpace(readText(t, baseFile)); got != trunk {
		t.Fatalf("the failover run started from %q, want the trunk %q", got, trunk)
	}
	if got := strings.TrimSpace(readText(t, leftoverFile)); got != "absent" {
		t.Fatalf("the failover run's tree still holds the first attempt's leftover (%q)", got)
	}
	// The item landed, so it is not held as an exhausted failover: the hold the stop detail
	// names is the ordinary `not-ready` of an item that is no longer queued.
	if got := itemStateOf(t, f.store, "item_041"); got != "landed" {
		t.Fatalf("the item is %q after the failover, want landed", got)
	}
	if strings.Contains(string(out), drain.ReasonInfraExhausted) {
		t.Fatalf("the loop held the item as %s: %s", drain.ReasonInfraExhausted, out)
	}
	if !strings.Contains(string(out), "failover item_041 -> agy on lane-1") {
		t.Fatalf("the loop did not report the failover: %s", out)
	}
	// One attempt per backend: a failover that walked more than one hop would run the
	// second adapter twice.
	invocations := lines(t, f.count)
	if countMatching(invocations, "pi-adapter") != 1 || countMatching(invocations, "agy-adapter") != 1 {
		t.Fatalf("the adapters did not run once each: %v", invocations)
	}
}

// stoppingReport is a report writer that stops the loop when it reads the line it was given:
// an operator stop that arrives while the loop is waiting is exactly this, and the case needs
// it to arrive at a known moment rather than after a sleep.
type stoppingReport struct {
	mu       sync.Mutex
	body     strings.Builder
	cancelOn string
	cancel   context.CancelFunc
}

func (r *stoppingReport) Write(p []byte) (int, error) {
	r.mu.Lock()
	r.body.Write(p)
	hit := strings.Contains(string(p), r.cancelOn)
	r.mu.Unlock()
	if hit {
		r.cancel()
	}
	return len(p), nil
}

func (r *stoppingReport) String() string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.body.String()
}

// failingStager refuses every stage: machinery that is broken and stays broken.
type failingStager struct{}

func (failingStager) Stage(_ context.Context, _ drain.StageSpec) error {
	return fmt.Errorf("the brief could not be rendered")
}

// An operator stop interrupts a backoff: the loop reports the wait, the signal arrives, and
// the loop stops instead of sleeping out the wait it announced. The base here is 30s, so a
// wait that ignored the context would hang this case rather than pass it slowly.
//
// The negative control is the base itself: make waitFor select only on the timer and this
// case never returns (it fails as a test timeout) instead of reporting operator-stop.
func TestAnOperatorStopInterruptsTheBackoff(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	report := &stoppingReport{cancelOn: "waiting", cancel: cancel}
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{laneName(1)},
		Policy: f.policy, Steps: f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)),
		Stage: failingStager{}, BackoffBase: 30 * time.Second, Report: report,
		Now: time.Now,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	result, err := loop.Run(ctx)
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopOperatorStop {
		t.Fatalf("the loop stopped %q (%s), want %q", result.Stop, result.Detail, drain.StopOperatorStop)
	}
	if !strings.Contains(report.String(), "waiting 30s") {
		t.Fatalf("the loop did not report the wait it was interrupted in: %s", report.String())
	}
	// The detail names the interrupted wait, which is what tells an interruption from a wait
	// that ran to its end and found the signal afterwards: a wait that ignored the context
	// reports "the loop was asked to stop" from the top of the loop instead.
	if !strings.Contains(result.Detail, "waiting out an infrastructure failure") {
		t.Fatalf("the stop detail reads %q, want the interrupted wait named", result.Detail)
	}
	if code := drain.StopExitCode(result.Stop); code != 0 {
		t.Fatalf("an operator stop answers exit %d, want 0", code)
	}
}

// A tick that plans nothing dispatchable stops the loop cleanly: the queue is the answer, and
// another tick would spend a run's worth of tokens saying so again.
func TestAnEmptyTickStopsTheLoop(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "landed"))

	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Ticks != 0 || result.Dispatched != 0 {
		t.Fatalf("the loop stopped %q after %d tick(s) with %d dispatch(es), want %q after none",
			result.Stop, result.Ticks, result.Dispatched, drain.StopQueueDrained)
	}
	if code := drain.StopExitCode(result.Stop); code != 0 {
		t.Fatalf("a drained queue answers exit %d, want 0", code)
	}
}

// The loop has no tick budget, so it does not stop after a count with work still queued:
// two items on one lane are two ticks and then the drained pile.
//
// This case is `TestMaxTicksStopsTheLoop`'s rewrite, inverted. It used to pin that
// `--max-ticks 1` ran exactly one tick while the second item stayed queued; that stop is the
// outcome docs/design/momentum-principle.md rules out, and the case now pins its absence.
func TestTheLoopHasNoTickBudget(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
	)
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Ticks != 6 || result.Dispatched != 2 || result.Landed != 2 {
		t.Fatalf("the loop stopped %q after %d tick(s), %d dispatch(es) and %d landing(s), want %q after 2, 2 and 2: %s",
			result.Stop, result.Ticks, result.Dispatched, result.Landed, drain.StopQueueDrained, result.Detail)
	}
}

// The loop's own lane creation names the holder of a lane's branch: a lane directory moved
// aside keeps its registration and its branch, and the loop would otherwise die on git's
// `fatal: cannot force update the branch ...` before any tick or staging ran. The branch is
// scoped by the lane root, so the holder is always this lane's own moved directory rather
// than another root's lane, and the refusal says so by naming its path.
//
// The negative control is the holder's path: a refusal that named only the lane would leave
// the operator with the same `fatal:` line to decode.
func TestTheLoopNamesTheWorktreeHoldingALanesBranch(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, fencedItemRecord("item_041", "in-queue", "tools/tillr-drain"))
	base := strings.TrimSpace(git(t, f.root, "rev-parse", "HEAD"))
	moved := filepath.Join(f.laneRoot, "lane-1-old", "worktree")
	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatalf("creating the moved lane's directory failed: %v", err)
	}
	git(t, f.root, "worktree", "add", "-b", drain.LaneBranch(f.laneRoot, "lane-1"), moved, base)

	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	_, err := loop.Run(context.Background())
	if err == nil {
		t.Fatalf("the loop staged a lane whose branch a moved worktree holds")
	}
	if strings.Contains(err.Error(), "fatal:") {
		t.Fatalf("the loop's refusal is git's own line rather than a named one: %v", err)
	}
	if !strings.Contains(err.Error(), moved) {
		t.Fatalf("the loop's refusal does not name the worktree holding the branch %s: %v", moved, err)
	}
}

// The stop reasons answer the exit statuses the report documents: clean stops exit 0, a
// boundary violation exits 1, and a gate that could not judge exits the gate's own error
// code. The table is the closed set: a budget stop, a spend stop and the infrastructure
// circuit breaker are gone by ruling (docs/design/momentum-principle.md), so there is no row
// for them and a name that came back would not answer an exit code any case expects.
func TestStopReasonsAnswerTheirExitStatuses(t *testing.T) {
	for _, c := range []struct {
		reason string
		code   int
	}{
		{drain.StopQueueDrained, 0},
		{drain.StopOperatorStop, 0},
		{drain.StopHostBoundaryViolation, 1},
		{drain.StopLanesLeased, 1},
		{drain.StopGateHarnessError, 4},
	} {
		if got := drain.StopExitCode(c.reason); got != c.code {
			t.Fatalf("stop reason %q answers exit %d, want %d", c.reason, got, c.code)
		}
	}
}

// rendezvousAdapter is a stub adapter that only finishes once its sibling has started: each
// run drops a marker under $STUB_MARKERS and waits for the other run's, giving up after a
// bounded wait so a serial loop fails the case instead of hanging the suite. A pair that runs
// one after the other leaves the `serial` marker behind, and a pair in flight together never
// does.
const rendezvousAdapter = counting + `here=$(git rev-parse --show-toplevel)
lane=$(basename "$(dirname "$here")")
: > "$STUB_MARKERS/$lane"
other=lane-1
if [ "$lane" = lane-1 ]; then other=lane-2; fi
n=0
while [ ! -e "$STUB_MARKERS/$other" ]; do
  n=$((n + 1))
  if [ "$n" -gt 60 ]; then
    : > "$STUB_MARKERS/serial"
    break
  fi
  sleep 0.05
done
echo candidate > candidate.txt
git add candidate.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
printf 'RESULT: done\n'
`

// Two dispatches are in flight at the same time, between one bracket.
//
// The stub is the test: it cannot finish unless its sibling has started, so a loop that
// dispatched the two runs one after the other fails this case instead of passing it slowly.
// Both runs commit on their own lanes and neither is a violation, which is the positive
// control for the bracket excusing each target worktree's own writes and its own branch.
func TestTwoLanesRunConcurrentlyInOneBatch(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", rendezvousAdapter))
	markers := scratch(t, "markers")
	t.Setenv("STUB_MARKERS", markers)
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
	)

	loop := f.newLoop(t, 2, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Dispatched != 2 {
		t.Fatalf("the loop stopped %q after dispatching %d, want %q after 2 (%s)",
			result.Stop, result.Dispatched, drain.StopQueueDrained, result.Detail)
	}
	if _, err := os.Stat(filepath.Join(markers, "serial")); err == nil {
		t.Fatalf("a run waited out its sibling: the tick's dispatches were not in flight together")
	}
	for _, lane := range []string{laneName(1), laneName(2)} {
		if _, err := os.Stat(filepath.Join(markers, lane)); err != nil {
			t.Fatalf("lane %s never ran: %v", lane, err)
		}
	}
	if result.Landed != 2 || result.Rejected != 0 {
		t.Fatalf("the loop landed %d and rejected %d, want 2 and 0: both candidates are clean",
			result.Landed, result.Rejected)
	}
}

// A run that writes into a sibling lane's worktree is still a violation: lane worktrees are
// named, and a run's own target is the only worktree excused.
//
// The negative control is the case's shape: two lanes, one dispatch, so lane-2 is named as a
// lane and is not a target, and the write is caught by the status comparison alone.
func TestAWriteToALaneOutsideTheRunIsCaught(t *testing.T) {
	escape := stub(t, "escaping-adapter", counting+`echo escaped > "$STUB_LANES/lane-2/worktree/escaped.txt"
echo candidate > candidate.txt
git add candidate.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
printf 'RESULT: done\n'
`)
	f := newLoopFixture(t, escape)
	t.Setenv("STUB_LANES", f.laneRoot)
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))

	loop := f.newLoop(t, 2, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	fault := faultOf(t, f.store, "item_041")
	if result.Stop != drain.StopQueueDrained || fault.Reason != drain.StopHostBoundaryViolation {
		t.Fatalf("the boundary fault stopped siblings: %+v", result)
	}
	if !strings.Contains(fault.Detail, filepath.Join(f.laneRoot, "lane-2", "worktree")) || !strings.Contains(fault.Detail, "escaped.txt") {
		t.Fatalf("the hold does not name the write: %+v", fault)
	}
	if gates := lines(t, f.count); countMatching(gates, "gate --item") != 0 {
		t.Fatalf("the loop gated a candidate after a boundary violation: %v", gates)
	}
}

// Main-checkout edits during one of two runs convict neither run.
func TestMainCheckoutEditsDoNotStopConcurrentRuns(t *testing.T) {
	escape := stub(t, "escaping-adapter", counting+`here=$(git rev-parse --show-toplevel)
lane=$(basename "$(dirname "$here")")
echo candidate > candidate.txt
git add candidate.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
if [ "$lane" = lane-1 ]; then echo escaped > "$STUB_ESCAPE/escaped.txt"; fi
printf 'RESULT: done\n'
`)
	f := newLoopFixture(t, escape)
	t.Setenv("STUB_ESCAPE", f.root)
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
	)

	loop := f.newLoop(t, 2, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Landed != 2 {
		t.Fatalf("owner edits stopped concurrent runs: %+v", result)
	}
	if result.Dispatched != 2 {
		t.Fatalf("the loop dispatched %d run(s), want both: %s", result.Dispatched, result.Detail)
	}
	if gates := lines(t, f.count); countMatching(gates, "gate --item") != 2 {
		t.Fatalf("owner edits prevented judgment: %v", gates)
	}
}

// An unassigned ref in tillr's namespace remains a boundary violation even when
// it is created inside the target worktree. Ordinary tags are not attributable.
// A ref written during a run is observed, not convicted: two snapshots cannot tell a
// worker's `git update-ref` from an operator's hand-written pin or a fork export, and the
// owner ruled on 2026-11-17 that ordinary Git use must never break the drain
// (CompareRunObserved). The candidate is still gated and landed on its own merits, and the
// movement is named on the run's boundary.check and in one report line.
func TestARefWrittenDuringARunIsObservedNotConvicted(t *testing.T) {
	tagging := stub(t, "tagging-adapter", counting+committingAdapterBody+`git update-ref refs/tillr/unauthorized HEAD
printf 'RESULT: done\n'
`)
	f := newLoopFixture(t, tagging)
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	report := &strings.Builder{}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)), passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, report)
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if fault := faultOf(t, f.store, "item_041"); fault.Reason != "" {
		t.Fatalf("a ref written during the run held the item: %+v", fault)
	}
	if result.Stop != drain.StopQueueDrained || result.Landed != 1 {
		t.Fatalf("the run was not landed on its merits: %+v", result)
	}
	if !strings.Contains(report.String(), "refs/tillr/unauthorized") || !strings.Contains(report.String(), "not attributed to the run") {
		t.Fatalf("the observation was not reported: %q", report.String())
	}
}

// dirtyAdapterBody is the stub adapter's out-of-commit work: it commits the candidate and
// then leaves a file the commit does not carry, which is the tree a check reading the working
// tree would judge in place of the one that lands.
const dirtyAdapterBody = `echo leftover > leftover.txt
`

// A candidate whose commit touches a path its lane does not fence is refused, and the gate
// never runs: the gate answers whether the change works, and this change is not the change
// the item asked for, however well it works.
//
// Negative control: the same adapter, the same commit and a fence that names the file is
// TestACandidateInsideItsFenceIsGated below, so a loop that refused every candidate passes
// here and fails there, and a loop that refused none passes there and fails here.
func TestACandidateOutsideItsFenceIsRefusedWithoutTheGate(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	report := &strings.Builder{}
	// The stub commits candidate.txt; the fence names a directory that does not contain it.
	stage := passStager{laneRoot: f.laneRoot, fence: []string{"docs/"}}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)), stage, report)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	// A refusal is a verdict about the work, so the loop runs on to the drained pile
	// instead of stopping: stopping would end an unattended night over one item's fence.
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("the loop stopped %q (%s), want %q", result.Stop, result.Detail, drain.StopQueueDrained)
	}
	if result.Rejected != 1 || result.Landed != 0 {
		t.Fatalf("the loop rejected %d and landed %d, want 1 and 0", result.Rejected, result.Landed)
	}
	runs := lines(t, f.count)
	if countMatching(runs, "gate --item") != 0 {
		t.Fatalf("the gate judged a candidate its fence refused: %v", runs)
	}
	// The gate never judged the candidate, so there is no verdict to record: the loop's
	// `drain.refusal` event is the record, and the item stays where it was.
	if countMatching(runs, "verdict") != 0 {
		t.Fatalf("the fence refusal recorded %d verdict(s), want none: %v", countMatching(runs, "verdict"), runs)
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "in-queue" {
		t.Fatalf("the fence refusal left the item %q, want in-queue", got)
	}
	refusals := refusalsOf(t, eventsOf(t, f.store))
	if len(refusals) != 1 || refusals[0].step != drain.RefusalFence {
		t.Fatalf("the fence refusal recorded %+v, want one %q refusal", refusals, drain.RefusalFence)
	}
	if !strings.Contains(report.String(), drain.ScopeEscape) || !strings.Contains(report.String(), "candidate.txt") {
		t.Fatalf("the report does not name the escape and the path: %s", report.String())
	}
	// The next tick names the outcome rather than the retired `not-ready` dead end.
	if !strings.Contains(result.Detail, "item_041 ("+drain.ReasonBoundaryScopeEscape+")") {
		t.Fatalf("the stop does not name the fence refusal's own hold: %s", result.Detail)
	}
}

// refusalEvent is one `drain.refusal` the loop appended: the run it names and the step that
// refused it.
type refusalEvent struct {
	runID string
	step  string
	// fixRunID and conflictPaths are the squash-conflict refusal's own fields. They are
	// empty on every other step, so a case can join a conflict to the one fix run it
	// staged without decoding the payload a second time.
	fixRunID      string
	conflictPaths []string
}

// refusalsOf answers the `drain.refusal` events in a log, in seq order.
func refusalsOf(t *testing.T, log []event.Event) []refusalEvent {
	t.Helper()
	refusals := []refusalEvent{}
	for _, e := range log {
		if e.Kind != drain.RefusalKind {
			continue
		}
		if e.ObjectType != "run" || e.ObjectID == nil || *e.ObjectID == "" {
			t.Fatalf("a drain.refusal event does not name a run: %+v", e)
		}
		payload := eventPayload(t, e)
		step, _ := payload["step"].(string)
		refusal := refusalEvent{runID: *e.ObjectID, step: step}
		refusal.fixRunID, _ = payload["fix_run_id"].(string)
		if raw, ok := payload["conflict_paths"].([]any); ok {
			for _, entry := range raw {
				if path, ok := entry.(string); ok {
					refusal.conflictPaths = append(refusal.conflictPaths, path)
				}
			}
		}
		refusals = append(refusals, refusal)
	}
	return refusals
}

// noOpAdapter answers with a report and commits nothing: the honest shape of a run that read
// the brief and found nothing to change. The loop must not treat it as a judged candidate.
const noOpAdapter = counting + `printf 'RESULT: done\n'
`

// blockedAdapter commits what it can and then answers `blocked`: the model's own statement
// that the item cannot be done as written.
const blockedAdapter = counting + committingAdapterBody + `printf 'RESULT: blocked\n'
`

// wallAdapter is a run a wall cut short: the adapter's own exit 124, which is the harness's
// timeout contract, not a judgment about the work.
const wallAdapter = counting + `exit 124
`

// A run that answers but commits nothing is `candidate-no-op`, not a failing verdict: no
// judge saw its work, so its item keeps its criteria, stays in the queue, and the next tick
// holds it with the outcome it actually reached.
func TestARunThatCommitsNothingLeavesTheItemQueuedAndHoldsItAsANoOp(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", noOpAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Landed != 0 || result.Rejected != 1 || result.Candidates != 0 {
		t.Fatalf("the loop landed %d, rejected %d and counted %d candidate(s), want 0, 1 and 0: %s",
			result.Landed, result.Rejected, result.Candidates, result.Detail)
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "in-queue" {
		t.Fatalf("the no-op left the item %q, want in-queue", got)
	}
	runs := lines(t, f.count)
	if countMatching(runs, "gate --item") != 0 || countMatching(runs, "verdict") != 0 {
		t.Fatalf("a no-op was judged: %v", runs)
	}
	refusals := refusalsOf(t, eventsOf(t, f.store))
	if len(refusals) != 1 || refusals[0].step != drain.RefusalNoOp {
		t.Fatalf("the no-op recorded %+v, want one %q refusal", refusals, drain.RefusalNoOp)
	}
	if !strings.Contains(result.Detail, "item_041 ("+drain.ReasonCandidateNoOp+")") {
		t.Fatalf("the stop does not name the no-op's own hold: %s", result.Detail)
	}
}

// A run whose report answers `blocked` is a spec conflict, not a failing candidate: the item
// is blocked for a person to decide, nothing is judged, and the gate never runs. The block
// is checked before the candidate path, so a blocked run that did commit still lands nothing.
func TestARunThatReportsBlockedBlocksTheItemWithoutJudging(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", blockedAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Landed != 0 || result.Rejected != 0 {
		t.Fatalf("the loop landed %d and rejected %d, want 0 and 0: %s", result.Landed, result.Rejected, result.Detail)
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "blocked" {
		t.Fatalf("the blocked run left the item %q, want blocked", got)
	}
	runs := lines(t, f.count)
	if countMatching(runs, "gate --item") != 0 || countMatching(runs, "verdict") != 0 {
		t.Fatalf("a blocked run was judged: %v", runs)
	}
	if !strings.Contains(result.Detail, "item_041 ("+drain.ReasonConflictOwner+")") {
		t.Fatalf("the stop does not name the block: %s", result.Detail)
	}
	if refusals := refusalsOf(t, eventsOf(t, f.store)); len(refusals) != 0 {
		t.Fatalf("a blocked run was recorded as a refusal: %+v", refusals)
	}
}

// A run a wall cut short stays queued and is held as `run-unjudged`: the tree may hold
// half-finished work, so nothing re-dispatches it and nothing calls it a candidate.
func TestARunCutShortByAWallLeavesTheItemQueuedAndHoldsItUnjudged(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", wallAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Landed != 0 || result.Rejected != 0 || result.Candidates != 0 {
		t.Fatalf("the loop landed %d, rejected %d and counted %d candidate(s), want none: %s",
			result.Landed, result.Rejected, result.Candidates, result.Detail)
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "in-queue" {
		t.Fatalf("the cut-short run left the item %q, want in-queue", got)
	}
	runs := lines(t, f.count)
	if countMatching(runs, "gate --item") != 0 || countMatching(runs, "verdict") != 0 {
		t.Fatalf("a cut-short run was judged: %v", runs)
	}
	if !strings.Contains(result.Detail, "item_041 ("+drain.ReasonRunUnjudged+")") {
		t.Fatalf("the stop does not name the unjudged hold: %s", result.Detail)
	}
	if refusals := refusalsOf(t, eventsOf(t, f.store)); len(refusals) != 0 {
		t.Fatalf("a cut-short run was recorded as a refusal: %+v", refusals)
	}
}

// A candidate whose commit stays inside its fence is gated and landed: the fence refuses the
// change it was not written for and nothing else.
func TestACandidateInsideItsFenceIsGated(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	report := &strings.Builder{}
	stage := passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)), stage, report)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Rejected != 0 || result.Landed != 1 {
		t.Fatalf("the loop rejected %d and landed %d, want 0 and 1: %s", result.Rejected, result.Landed, report)
	}
	if runs := lines(t, f.count); countMatching(runs, "gate --item") != 1 {
		t.Fatalf("the gate ran %d time(s), want once: %v", countMatching(runs, "gate --item"), runs)
	}
}

// A worktree dirty past its own commit is refused too, even when every committed path is
// inside the fence: the candidate that lands is the commit, and a check reading the working
// tree can pass on files the commit does not carry.
//
// Negative control: committingAdapter makes the same commit and leaves nothing behind, which
// is TestACandidateInsideItsFenceIsGated — the two differ only in the leftover file.
func TestADirtyCandidateWorktreeIsRefused(t *testing.T) {
	f := newLoopFixture(t, stub(t, "dirty-adapter", counting+committingAdapterBody+dirtyAdapterBody+`printf 'RESULT: done\n'
`))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	report := &strings.Builder{}
	stage := passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)), stage, report)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Rejected != 1 || result.Landed != 0 {
		t.Fatalf("the loop rejected %d and landed %d, want 1 and 0", result.Rejected, result.Landed)
	}
	if runs := lines(t, f.count); countMatching(runs, "gate --item") != 0 {
		t.Fatalf("the gate judged a worktree that does not match its commit: %v", runs)
	}
	if !strings.Contains(report.String(), drain.ScopeEscape) || !strings.Contains(report.String(), "leftover.txt") {
		t.Fatalf("the report does not name the escape and the path: %s", report.String())
	}
}

// TestTheVerdictProgramIsGivenTheStoreTheLoopPlannedFrom pins the argument that made a live
// canary stop after its gate had already judged: the verdict program was invoked with no
// `--store`, so it looked for a store under the root and refused with exit 4, and a recorded
// verdict that found no store is a landed candidate the item cannot see.
//
// The negative control is the assertion itself: drop `--store` from the step and the stub's
// argv no longer carries the loop's store path, so the case fails while every other case in
// this file still passes.
func TestTheVerdictProgramIsGivenTheStoreTheLoopPlannedFrom(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	verdict := stub(t, "verdict", counting+"exit 0\n")
	steps := f.steps(t, stub(t, "gate", counting+"exit 0\n"), stub(t, "land", counting+"exit 0\n"), verdict)

	candidate := drain.Candidate{ItemID: "item-1", LaneID: "lane-1", RunID: "run-1", Worktree: f.root, BaseSHA: "base", HeadSHA: "head"}
	if err := steps.Verdict(context.Background(), candidate, "pass"); err != nil {
		t.Fatalf("the verdict step failed: %v", err)
	}
	invocations, err := os.ReadFile(f.count)
	if err != nil {
		t.Fatalf("the stub wrote no invocation: %v", err)
	}
	if !strings.Contains(string(invocations), "--store "+f.store) {
		t.Fatalf("the verdict program was not told the store the loop planned from:\n%s", invocations)
	}
}

// itemStateOf answers the board's own record of one item's state, through the substrate's
// reader. A case that read the loop's echo instead would pin the loop's words about the
// board rather than the row a later tick plans from — which is the defect these cases are
// about, so the read is deliberately not the loop's.
func itemStateOf(t *testing.T, storePath, id string) string {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	var got string
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		return r.QueryRow(`SELECT state FROM items WHERE item_id = ?`, id).Scan(&got)
	}); err != nil {
		t.Fatalf("reading the state of %q failed: %v", id, err)
	}
	return got
}

// A landed candidate moves its item to `landed`, and the tick after it does not plan the
// item again: the queue shrinks by exactly the work that landed.
//
// This is the live canary's measurement as a case. The canary completed a full tick, landed
// its candidate, advanced the trunk and moved the criterion to `done` — and the item was
// still `in-queue`, where the planner held it for a retry all night. The assertions below
// are the two halves of the repair: the row says `landed`, and the adapter ran once.
func TestALandedCandidateMovesItsItemToLanded(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Landed != 1 || result.Rejected != 0 {
		t.Fatalf("the loop landed %d and rejected %d, want 1 and 0", result.Landed, result.Rejected)
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "landed" {
		t.Fatalf("the landed item is %q in the store, want landed", got)
	}
	// The second tick planned nothing, because the only item had landed: the loop stopped
	// cleanly after one tick rather than re-dispatching what it had just landed.
	if result.Stop != drain.StopQueueDrained || result.Ticks != 3 || result.Dispatched != 1 {
		t.Fatalf("the loop stopped %q after %d tick(s) and %d dispatch(es), want %q after one and one: %s",
			result.Stop, result.Ticks, result.Dispatched, drain.StopQueueDrained, result.Detail)
	}
	if runs := lines(t, f.count); countMatching(runs, "adapter") != 1 {
		t.Fatalf("the adapter ran %d time(s) for one landed candidate, want 1: %v", countMatching(runs, "adapter"), runs)
	}
	if !strings.Contains(result.Detail, "item_041") || !strings.Contains(result.Detail, drain.ReasonNotReady) {
		t.Fatalf("the stop does not name the landed item and its hold reason: %s", result.Detail)
	}
}

// A failed candidate keeps its item in the queue, records the gate's failing verdict, and
// the next tick holds it with the gate's own outcome, `gate-failed` — work a person must
// look at again, not a retry and not a stop. The item's disposition is the owner's, so the
// loop does not move it.
//
// The negative control is the pair this case has: the state is `in-queue` and not `blocked`
// or `closed`, and the tick after it holds the item instead of dispatching it, so a loop
// that moved the item (or re-dispatched it) fails here, and a loop that stopped the night
// over one red gate fails the tick-budget assertion.
func TestAFailedCandidateLeavesItsItemQueuedAndTheNextTickHoldsIt(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 1), always(t, "land", 0), always(t, "verdict", 0)))

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Rejected != 1 || result.Landed != 0 {
		t.Fatalf("the loop rejected %d and landed %d, want 1 and 0", result.Rejected, result.Landed)
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "in-queue" {
		t.Fatalf("the failed item is %q in the store, want in-queue", got)
	}
	if result.Stop != drain.StopQueueDrained || result.Ticks != 3 {
		t.Fatalf("the loop stopped %q after %d tick(s), want %q after one: %s",
			result.Stop, result.Ticks, drain.StopQueueDrained, result.Detail)
	}
	runs := lines(t, f.count)
	if countMatching(runs, "adapter") != 1 {
		t.Fatalf("the adapter ran %d time(s) for one failed candidate, want 1: %v", countMatching(runs, "adapter"), runs)
	}
	if countMatching(runs, "--verdict fail") != 0 {
		t.Fatalf("the gate's failure recorded %d failing verdict(s), want none: a red gate freezes no verdict (MQ1): %v",
			countMatching(runs, "--verdict fail"), runs)
	}
	if !strings.Contains(result.Detail, "item_041") || !strings.Contains(result.Detail, drain.ReasonGateFailed) {
		t.Fatalf("the stop does not name the held item and its reason: %s", result.Detail)
	}
}

// A drain that has refused every candidate it produced is not N unlucky items: the fence
// refused both, the gate never ran, and the loop says so in one line that carries the
// window it cost. This is the regression for the incident the item records — three ticks,
// two items left in-progress, and a log that read as ordinary rejection.
//
// Negative controls: the same fixture with a fence that admits the candidate lands both
// (TestALandedDrainIsNotARepeatedRefusal), and one refusal is not yet a pattern
// (TestASingleRefusalIsNotARepeatedRefusal), so a loop that named every refusal or none
// fails one of the three.
func TestADrainThatRefusedEveryCandidateNamesItAsARepeatedRefusal(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
	)
	report := &strings.Builder{}
	// The stub commits candidate.txt; the fence names a directory that does not contain
	// it, so the fence refuses both candidates and the gate never runs.
	stage := passStager{laneRoot: f.laneRoot, fence: []string{"docs/"}}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)), stage, report)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Landed != 0 || result.Rejected != 2 || result.Candidates != 2 {
		t.Fatalf("the loop stopped %q with %d landed, %d rejected and %d candidate(s), want %q with 0, 2 and 2: %s",
			result.Stop, result.Landed, result.Rejected, result.Candidates, drain.StopQueueDrained, result.Detail)
	}
	if result.RepeatedRefusal == nil {
		t.Fatalf("a drain that refused every candidate reported no repeated refusal: %s", report.String())
	}
	got := result.RepeatedRefusal
	if got.Candidates != 2 || got.Rejected != 2 || got.Steps[drain.RefusalFence] != 2 {
		t.Fatalf("the repeated refusal is %+v, want 2 candidates and 2 refusals at the fence", got)
	}
	if strings.Join(got.Items, ",") != "item_041,item_042" {
		t.Fatalf("the repeated refusal names items %v, want both in the order they were refused", got.Items)
	}
	line := drain.RepeatedRefusalLine(result)
	for _, want := range []string{
		"every one of its 2 candidate(s) was refused",
		"fence=2",
		"window: 6 tick(s)",
		"items left in the queue: item_041, item_042",
	} {
		if !strings.Contains(line, want) {
			t.Fatalf("the repeated-refusal line does not say %q: %s", want, line)
		}
	}
	if !strings.Contains(report.String(), "repeated-refusal") {
		t.Fatalf("the loop's report does not name the repeated refusal: %s", report.String())
	}
	// The record carries the same report, so a reader who arrives after the terminal is
	// gone can still tell a drain that never landed from one that was unlucky.
	body := readJSONFile(t, drain.LoopResultFile(f.root, result.FirstGeneration, result.LastGeneration))
	payload, ok := body["payload"].(map[string]any)
	if !ok {
		t.Fatalf("the loop's record has no payload: %v", body)
	}
	kept, ok := payload["repeated_refusal"].(map[string]any)
	if !ok {
		t.Fatalf("the loop's record carries no repeated_refusal: %v", payload)
	}
	if kept["candidates"] != float64(2) {
		t.Fatalf("the recorded repeated refusal names %v candidates, want 2", kept["candidates"])
	}
}

// One refusal is a bad item, not a pattern: the threshold is what keeps the report from
// firing on the first candidate every drain refuses.
func TestASingleRefusalIsNotARepeatedRefusal(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	report := &strings.Builder{}
	stage := passStager{laneRoot: f.laneRoot, fence: []string{"docs/"}}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)), stage, report)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Rejected != 1 || result.Landed != 0 {
		t.Fatalf("the loop rejected %d and landed %d, want 1 and 0", result.Rejected, result.Landed)
	}
	if result.RepeatedRefusal != nil {
		t.Fatalf("one refusal was reported as a repeated refusal: %+v", result.RepeatedRefusal)
	}
	if strings.Contains(report.String(), "repeated-refusal") {
		t.Fatalf("the report named a repeated refusal after one refusal: %s", report.String())
	}
}

// A drain that lands both candidates is not a repeated refusal, whatever it refused on
// the way: the report is about a run that has never landed, not about a rejection count.
func TestALandedDrainIsNotARepeatedRefusal(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
	)
	report := &strings.Builder{}
	stage := passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)), stage, report)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Landed != 2 || result.Rejected != 0 {
		t.Fatalf("the loop landed %d and rejected %d, want 2 and 0", result.Landed, result.Rejected)
	}
	if result.RepeatedRefusal != nil {
		t.Fatalf("a drain that landed both candidates reported a repeated refusal: %+v", result.RepeatedRefusal)
	}
	if strings.Contains(report.String(), "repeated-refusal") {
		t.Fatalf("the report named a repeated refusal for a drain that landed: %s", report.String())
	}
}

// The step that refused is part of the report: a gate that fails everything is a different
// fact from a fence that admits nothing, and an operator chasing the wrong one wastes the
// night the report exists to save.
func TestARepeatedRefusalNamesTheGateWhenTheGateRefused(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
	)
	report := &strings.Builder{}
	stage := passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}
	loop := f.newLoopWith(t, 1, f.steps(t, always(t, "gate", 1), always(t, "land", 0), always(t, "verdict", 0)), stage, report)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.RepeatedRefusal == nil {
		t.Fatalf("a drain whose gate refused every candidate reported no repeated refusal: %s", report.String())
	}
	if got := result.RepeatedRefusal.Steps[drain.RefusalGate]; got != 2 {
		t.Fatalf("the repeated refusal names %d gate refusals, want 2: %+v", got, result.RepeatedRefusal)
	}
	if line := drain.RepeatedRefusalLine(result); !strings.Contains(line, "gate=2") {
		t.Fatalf("the repeated-refusal line does not name the gate: %s", line)
	}
}

// The stop detail names every held item and the plan's reason for each, rather than the
// bare "nothing dispatchable" that hid three held items behind one sentence.
func TestTheStopDetailNamesEveryHeldItemAndItsReason(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store,
		itemRecord("item_041", "landed"),
		itemRecord("item_042", "blocked"),
		itemRecord("item_043", "not-started"),
	)
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Ticks != 0 || result.Dispatched != 0 {
		t.Fatalf("the loop stopped %q after %d tick(s) and %d dispatch(es), want %q with none",
			result.Stop, result.Ticks, result.Dispatched, drain.StopQueueDrained)
	}
	for _, want := range []string{
		"3 held item(s)",
		"item_041 (" + drain.ReasonNotReady + ")",
		"item_042 (" + drain.ReasonBlocked + ")",
		"item_043 (" + drain.ReasonNotReady + ")",
	} {
		if !strings.Contains(result.Detail, want) {
			t.Fatalf("the stop detail does not name %q: %s", want, result.Detail)
		}
	}
	if runs := lines(t, f.count); countMatching(runs, "adapter") != 0 {
		t.Fatalf("the loop dispatched an item the detail says was held: %v", runs)
	}
}

// mayBeARetry survives a run that did not land: an item that has already run and is queued
// again is held as a possible retry, never dispatched as a first attempt. Leaving the item
// in the queue records where the work stands; it is not a licence to spend a second run on
// the same item.
func TestARetryStillHoldsAfterANonLandingRun(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		runRecord("run-item_041-20261112T000000Z", "item_041", "lane-1", "fixture", "exited", exitCode(0), ""),
	)
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Dispatched != 0 || result.Ticks != 0 {
		t.Fatalf("the loop dispatched %d run(s) in %d tick(s) for an item that had already run",
			result.Dispatched, result.Ticks)
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("the loop stopped %q (%s), want %q", result.Stop, result.Detail, drain.StopQueueDrained)
	}
	if !strings.Contains(result.Detail, "item_041 ("+drain.ReasonRunLinkMissing+")") {
		t.Fatalf("the stop does not name the retry hold: %s", result.Detail)
	}
	if runs := lines(t, f.count); countMatching(runs, "adapter") != 0 {
		t.Fatalf("the adapter ran for an item held as a possible retry: %v", runs)
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "in-queue" {
		t.Fatalf("a hold moved the item to %q, want the in-queue it was seeded with", got)
	}
}

// itemRecordWithFence is itemRecord with the fence and the criteria the real stager needs:
// staging refuses an item with no allowed_paths or no criteria, and the SIGTERM case drives
// the loop binary's own stager rather than a stub.
func itemRecordWithFence(itemID, state string, allowedPaths ...string) string {
	return envelope("item.record", "item-"+itemID, map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"state":         state,
		"blocked_by":    []string{},
		"rank":          "",
		"allowed_paths": allowedPaths,
		"criteria": []any{
			map[string]any{"criterion_id": "c1", "text": "the work is done", "state": "not-done"},
		},
	})
}

// seedLeaseNoExpiry writes a lane row held by a run with no expiry: the one lease no clock
// can free, which is the `lanes-leased` stop's subject.
func seedLeaseNoExpiry(t *testing.T, path, laneID, runID string) {
	t.Helper()
	ctx := context.Background()
	s, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatalf("opening %s failed: %v", path, err)
	}
	defer s.Close()
	if err := s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(
			`INSERT INTO lanes (lane_id, state, run_id, leased_until) VALUES (?, 'leased', ?, NULL)`,
			laneID, runID,
		)
		return err
	}); err != nil {
		t.Fatalf("seeding lane %q failed: %v", laneID, err)
	}
}

// A lane held by a finite lease is waited on, not turned into a stop: the loop waits until
// the lease expires and re-plans, and the item then dispatches. The negative control is the
// stop the first night's drain made: without the wait, the loop reports `queue-drained` while
// the item is `no-lane` and the lane is still held.
func TestALaneHeldByAFiniteLeaseIsWaitedOnNotStopped(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	// The lease is real and long enough to outlive the loop's own staging (which creates a
	// git worktree before the first tick): the loop's clock is the wall clock, so the wait
	// is the time until the lease expires and nothing else.
	seedLease(t, f.store, "lane-1", "run-1", time.Now().Add(2*time.Second).UnixMilli())
	report := &strings.Builder{}
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	loop := f.newLoopAt(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, report, time.Now)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained || result.Landed != 1 {
		t.Fatalf("the loop stopped %q after landing %d (%s), want %q and 1",
			result.Stop, result.Landed, result.Detail, drain.StopQueueDrained)
	}
	if !strings.Contains(report.String(), "waiting") {
		t.Fatalf("the loop did not wait for the lease it held: %s", report.String())
	}
	if strings.Contains(report.String(), "queue-drained") {
		t.Fatalf("the loop reported a drained queue while the item was held: %s", report.String())
	}
}

// A lane held with no expiry is a hard stop, not a wait: no clock frees it, so the loop
// stops `lanes-leased` non-zero and names the release that is the missing act.
func TestALaneHeldWithNoExpiryStopsTheLoopAsLanesLeased(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	seedLeaseNoExpiry(t, f.store, "lane-1", "run-1")

	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopLanesLeased {
		t.Fatalf("the loop stopped %q (%s), want %q", result.Stop, result.Detail, drain.StopLanesLeased)
	}
	if code := drain.StopExitCode(result.Stop); code == 0 {
		t.Fatalf("a lane held with no expiry answers exit 0, want a non-zero stop")
	}
	if !strings.Contains(result.Detail, "tillr run release lane-1") {
		t.Fatalf("the stop detail does not name the missing release: %s", result.Detail)
	}
	if runs := lines(t, f.count); countMatching(runs, "adapter") != 0 {
		t.Fatalf("the loop dispatched onto a lane held with no expiry: %v", runs)
	}
}

// A raw supervisor signal leaves the run alive. The explicit stop face is
// covered separately, including its opt-in end-runs behavior.
func TestASigtermStopsOnlyTheLoop(t *testing.T) {
	marker := filepath.Join(scratch(t, "sigterm-marker"), "started")
	adapter := stub(t, "sleeping-adapter", counting+`echo started > "$STUB_MARKER"
while [ ! -f "$STUB_MARKER.release" ]; do sleep 0.02; done
printf 'RESULT: done\n'
`)
	f := newLoopFixture(t, adapter)
	t.Setenv("STUB_MARKER", marker)
	fixtureAt(t, f.store, itemRecordWithFence("item_041", "in-queue", "candidate.txt"))
	policyPath := filepath.Join(scratch(t, "policy"), "policy.json")
	raw, err := json.Marshal(f.policy)
	if err != nil {
		t.Fatalf("encoding the policy failed: %v", err)
	}
	writeFile(t, policyPath, string(raw))

	cmd := exec.Command(bin, "loop",
		"--root", f.root, "--store", f.store, "--lane-root", f.laneRoot,
		"--lanes", "1", "--policy", policyPath,
		"--run-bin", sibling(t, "run"), "--item-bin", sibling(t, "item"),
		"--brief-template", filepath.Join("..", "tillr-run", "backends", "brief-template.md"),
		"--gate", always(t, "gate", 0), "--land", always(t, "land", 0), "--verdict", always(t, "verdict", 0),
	)
	cmd.SysProcAttr = &syscall.SysProcAttr{Setpgid: true}
	var out strings.Builder
	cmd.Stdout = &out
	cmd.Stderr = &out
	if err := cmd.Start(); err != nil {
		t.Fatalf("starting the loop failed: %v", err)
	}
	// A failure before cmd.Wait leaves the loop running; kill it so the case never leaves an
	// orphan behind. A process that already exited answers an error this ignores.
	t.Cleanup(func() { _ = cmd.Process.Kill() })
	waitForFile(t, marker, 30*time.Second)
	// Like a terminal signal, this targets the supervisor's whole process group.
	if err := syscall.Kill(-cmd.Process.Pid, syscall.SIGTERM); err != nil {
		cmd.Process.Kill()
		t.Fatalf("signalling the loop failed: %v", err)
	}
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("the loop exited %v after SIGTERM, want 0: %s", err, out.String())
		}
	case <-time.After(60 * time.Second):
		cmd.Process.Kill()
		t.Fatalf("the loop did not stop after SIGTERM: %s", out.String())
	}
	if pids := matchingPIDs(t, f.store); len(pids) == 0 {
		t.Fatal("the ordinary loop signal ended its run")
	}
	writeFile(t, marker+".release", "finish")
	deadline := time.Now().Add(10 * time.Second)
	for len(terminalRunRows(t, f.store)) == 0 && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	// Exactly one terminal runs row, and the lane is free after the run finishes.
	rows := terminalRunRows(t, f.store)
	if len(rows) != 1 {
		t.Fatalf("the stopped run left %d terminal runs row(s), want exactly 1: %v", len(rows), rows)
	}
	if rows[0].state != "exited" || rows[0].exitCode == nil {
		t.Fatalf("the stopped run's row reads %+v, want a terminal exited row with an exit code", rows[0])
	}
	for _, lane := range lanesIn(t, f.store) {
		if lane.LaneID != "lane-1" {
			continue
		}
		if lane.State != "idle" || lane.LeasedUntil != nil || lane.RunID != nil {
			t.Fatalf("the lane reads %+v after the stop, want it free with no lease", lane)
		}
	}
}

// waitForFile waits until path exists, bounded by timeout. It is a readiness wait, not a
// timing assertion: the case fails if the file never appears, never because it appeared
// slowly.
func waitForFile(t *testing.T, path string, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for {
		if _, err := os.Stat(path); err == nil {
			return
		}
		if time.Now().After(deadline) {
			t.Fatalf("%s did not appear within %s", path, timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// matchingPIDs answers the processes whose command line names the store, which is the run
// face's argv and nothing else's once the loop is gone.
func matchingPIDs(t *testing.T, storePath string) []string {
	t.Helper()
	out, err := exec.Command("pgrep", "-f", storePath).Output()
	if err != nil {
		if exit, ok := err.(*exec.ExitError); ok && exit.ExitCode() == 1 {
			return nil
		}
		t.Fatalf("pgrep failed: %v", err)
	}
	return strings.Fields(string(out))
}

// terminalRun is one row of the runs table as the stop case reads it.
type terminalRun struct {
	state    string
	exitCode *int
}

func terminalRunRows(t *testing.T, storePath string) []terminalRun {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	var rows []terminalRun
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		found, err := r.Query(`SELECT state, exit_code FROM runs`)
		if err != nil {
			return err
		}
		defer found.Close()
		for found.Next() {
			var (
				state string
				code  sql.NullInt64
			)
			if err := found.Scan(&state, &code); err != nil {
				return err
			}
			run := terminalRun{state: state}
			if code.Valid {
				n := int(code.Int64)
				run.exitCode = &n
			}
			rows = append(rows, run)
		}
		return found.Err()
	}); err != nil {
		t.Fatalf("reading the runs failed: %v", err)
	}
	return rows
}

// itemRun is one runs row as the failover case reads it: the id the run face wrote and the
// backend the request named.
type itemRun struct {
	RunID   string
	Backend string
}

// runsForItem answers the store's run rows for one item, oldest first. The run face writes
// them, so this is the record of what ran rather than the loop's account of it.
func runsForItem(t *testing.T, storePath, itemID string) []itemRun {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer s.Close()
	var runs []itemRun
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		found, err := r.Query(`SELECT run_id, backend FROM runs WHERE item_id = ? ORDER BY started_at, run_id`, itemID)
		if err != nil {
			return err
		}
		defer found.Close()
		for found.Next() {
			var row itemRun
			if err := found.Scan(&row.RunID, &row.Backend); err != nil {
				return err
			}
			runs = append(runs, row)
		}
		return found.Err()
	}); err != nil {
		t.Fatalf("reading the runs for %q failed: %v", itemID, err)
	}
	return runs
}

// The loop's negative direction: an item that already ran is not re-dispatched until a
// person authorizes it. A harness-refused run plus an authorization is a retry the loop
// plans, dispatches, gates and lands — and the retry's own run is what retires the
// authorization, so a second loop over the same store holds instead of spending again.
func TestTheLoopDispatchesAnAuthorizedRetry(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	const failedRun = "run-item_041-20260909T000000Z"
	fixtureAt(t, f.store,
		fencedItemRecord("item_041", "in-queue", "candidate.txt"),
		runRecord(failedRun, "item_041", "lane-1", "fixture", run.StateExited, exitCode(2), ""),
	)
	if code, output := authorizeRetry(t, f.store, "item_041", failedRun,
		"the owner", "the harness refused before the work started", "2026-11-12T00:00:00Z"); code != 0 {
		t.Fatalf("retry exited %d: %s", code, output)
	}
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Dispatched != 1 || result.Landed != 1 {
		t.Fatalf("the loop dispatched %d and landed %d, want the retry to do both", result.Dispatched, result.Landed)
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "landed" {
		t.Fatalf("the retried item is %q, want landed", got)
	}
	// The second loop sees the item landed, so it dispatches nothing: the retry run's row
	// is what retired the authorization, and no second copy of the claim is left behind.
	second := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	again, err := second.Run(context.Background())
	if err != nil {
		t.Fatalf("the second loop failed: %v", err)
	}
	if again.Dispatched != 0 || again.Ticks != 0 {
		t.Fatalf("the second loop dispatched %d run(s) in %d tick(s) for a landed item", again.Dispatched, again.Ticks)
	}
}

// TestEveryNonLandingOutcomeRecovers drives the four outcomes a non-landing run can leave
// and proves each one recovers through the verb it needs: a fence refusal and a `blocked`
// report through a bare retry, a gate failure through `--acknowledge gate-failed`, and a
// wall cut short through `--acknowledge unjudged`.
//
// The recovery must not move an item through `tillr-item state`: the item is already in the
// queue (or, for `blocked`, moved back by `tillr-item apply`, which is the record that its
// criteria were never frozen), and the retry authorization is what the next loop pass
// dispatches. A retry's attempt-based run id differs from the run it supersedes,
// so the authorization retires after one dispatch even under a fixed clock.
func TestEveryNonLandingOutcomeRecovers(t *testing.T) {
	// The adapter is one program that answers a different outcome per item on the first
	// pass. Once STUB_RECOVER is set, every item is ordinary committed work, so the second
	// pass is the recovery's proof rather than a second copy of the same failure.
	adapter := stub(t, "adapter", counting+`if [ -n "$STUB_RECOVER" ]; then
  echo candidate > candidate.txt
  git add candidate.txt
  git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
  printf 'RESULT: done\n'
  exit 0
fi
case "$TILLR_RUN_ID" in
  *item_041*)
    mkdir -p docs
    echo escape > docs/escape.txt
    git add docs/escape.txt
    git -c user.name=stub -c user.email=stub@example.invalid commit -q -m escape
    printf 'RESULT: done\n'
    ;;
  *item_042*)
    echo candidate > candidate.txt
    git add candidate.txt
    git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
    printf 'RESULT: blocked\n'
    ;;
  *item_043*)
    echo candidate > candidate.txt
    git add candidate.txt
    git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
    printf 'RESULT: done\n'
    ;;
  *item_044*)
    exit 124
    ;;
esac
`)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
		itemRecord("item_043", "in-queue"),
		itemRecord("item_044", "in-queue"),
	)

	// The gate fails only item_043, and only on the first pass.
	gate := stub(t, "gate", counting+`if [ "$1" = "--item" ] && [ "$2" = "item_043" ] && [ -z "$STUB_RECOVER" ]; then
  exit 1
fi
exit 0
`)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)

	// The wrapper records every `tillr-item` invocation so the recovery can prove it did
	// not move an item through `tillr-item state`.
	calls := filepath.Join(scratch(t, "item-calls"), "calls")
	item := stub(t, "item", `echo "$@" >> "$STUB_ITEM_CALLS"
exec "$STUB_ITEM_REAL" "$@"
`)
	t.Setenv("STUB_ITEM_CALLS", calls)
	t.Setenv("STUB_ITEM_REAL", sibling(t, "item"))

	steps := f.steps(t, gate, land, verdict)
	steps.ItemBin = item

	// This case advances the clock between passes; identity_test.go also covers
	// recovery with a fixed clock, where attempt identities retire the authorization.
	start := time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC)
	var mu sync.Mutex
	elapsed := 0
	now := func() time.Time {
		mu.Lock()
		defer mu.Unlock()
		at := start.Add(time.Duration(elapsed) * time.Second)
		elapsed++
		return at
	}

	first := f.newLoopAt(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, io.Discard, now)
	got, err := first.Run(context.Background())
	if err != nil {
		t.Fatalf("the first loop failed: %v", err)
	}
	if got.Stop != drain.StopQueueDrained {
		t.Fatalf("the first loop stopped %q (%s), want %q", got.Stop, got.Detail, drain.StopQueueDrained)
	}
	if strings.Contains(got.Detail, drain.ReasonNotReady) {
		t.Fatalf("the first loop held an item with %s: %s", drain.ReasonNotReady, got.Detail)
	}
	for _, want := range []string{
		"item_041 (" + drain.ReasonBoundaryScopeEscape + ")",
		"item_042 (" + drain.ReasonConflictOwner + ")",
		"item_043 (" + drain.ReasonGateFailed + ")",
		"item_044 (" + drain.ReasonRunUnjudged + ")",
	} {
		if !strings.Contains(got.Detail, want) {
			t.Fatalf("the first loop's stop does not name %q: %s", want, got.Detail)
		}
	}

	failed := map[string]string{}
	for _, id := range []string{"item_041", "item_042", "item_043", "item_044"} {
		runs := runsForItem(t, f.store, id)
		if len(runs) != 1 {
			t.Fatalf("item %s has %d run(s) after the first pass, want 1", id, len(runs))
		}
		failed[id] = runs[0].RunID
	}

	// The recovery: apply is the blocked item's record that its criteria were never frozen,
	// and every item's retry is what the next pass dispatches. Nothing here asks the item
	// binary to move a state.
	if err := os.Remove(calls); err != nil && !os.IsNotExist(err) {
		t.Fatalf("clearing the item calls failed: %v", err)
	}
	itemPath := filepath.Join(scratch(t, "recover"), "item_042.json")
	writeFile(t, itemPath, itemRecord("item_042", "in-queue"))
	driveOK(t, item, "apply", "--store", f.store, "--input", itemPath, "--now", "2026-11-12T00:00:30Z")
	if got := itemStateOf(t, f.store, "item_042"); got != "in-queue" {
		t.Fatalf("the blocked item is %q after apply, want in-queue", got)
	}
	for _, c := range []struct {
		id, ack string
	}{
		{"item_041", ""},
		{"item_042", ""},
		{"item_043", drain.RetryAckGateFailed},
		{"item_044", drain.RetryAckUnjudged},
	} {
		code, out := authorizeRetryWith(t, f.store, c.id, failed[c.id],
			"the owner", "recovering a non-landing outcome", "2026-11-12T00:00:30Z", c.ack)
		if code != 0 {
			t.Fatalf("retry %s (%q) exited %d: %s", c.id, c.ack, code, out)
		}
	}
	for _, line := range lines(t, calls) {
		if strings.HasPrefix(line, "state ") {
			t.Fatalf("the recovery moved an item through `tillr-item state`: %s", line)
		}
	}

	// The plan the next pass would run holds none of the four with not-ready: each is a
	// retry the authorization makes dispatchable.
	tickPath := tick(t, "2026-11-12T00:00:30Z", []string{"lane-1", "lane-2", "lane-3", "lane-4"}, 4, policy(t, "/bin/true"))
	for _, d := range plan(t, f.store, tickPath) {
		if d.Payload.Action != drain.ActionRetry {
			t.Fatalf("item %s plans %q / %q after recovery, want a retry", d.Payload.ItemID, d.Payload.Action, d.Payload.Reason)
		}
	}

	// The next pass dispatches and lands all four. The adapter answers `done` for every
	// item once STUB_RECOVER is set.
	t.Setenv("STUB_RECOVER", "1")
	second := f.newLoopAt(t, 1, steps, passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}}, io.Discard, now)
	again, err := second.Run(context.Background())
	if err != nil {
		t.Fatalf("the second loop failed: %v", err)
	}
	if again.Stop != drain.StopQueueDrained {
		t.Fatalf("the second loop stopped %q (%s), want %q", again.Stop, again.Detail, drain.StopQueueDrained)
	}
	if again.Dispatched != 4 || again.Landed != 4 {
		t.Fatalf("the second loop dispatched %d and landed %d, want 4 and 4: %s", again.Dispatched, again.Landed, again.Detail)
	}
	for _, id := range []string{"item_041", "item_042", "item_043", "item_044"} {
		if got := itemStateOf(t, f.store, id); got != "landed" {
			t.Fatalf("item %s is %q after the recovery pass, want landed", id, got)
		}
	}
}

// A lane's own trunk commit does not stop another lane's run: each lane's bracket excuses
// the trunk, the lane branches and the tillr pins, so the loop no longer stops on its own
// deliverable. This is the case that used to stop with StopHostBoundaryViolation whenever a
// second lane landed, reset its branch or committed on the host while another lane ran.
func TestALaneLandingDoesNotStopTheOtherLane(t *testing.T) {
	adapter := stub(t, "adapter", counting+`lane=$(basename "$(dirname "$(git rev-parse --show-toplevel)")")
echo candidate > candidate.txt
git add candidate.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
if [ "$lane" = lane-2 ]; then
  git -C "$STUB_ROOT" commit --allow-empty -q -m "host commit"
fi
printf 'RESULT: done\n'
`)
	f := newLoopFixture(t, adapter)
	t.Setenv("STUB_ROOT", f.root)
	fixtureAt(t, f.store,
		itemRecord("item_041", "in-queue"),
		itemRecord("item_042", "in-queue"),
	)

	loop := f.newLoop(t, 2, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("a lane's own trunk commit stopped the loop: %q (%s)", result.Stop, result.Detail)
	}
	if result.Landed != 2 {
		t.Fatalf("the loop landed %d candidate(s), want 2", result.Landed)
	}
}

// A red gate pins the head it judged and freezes no verdict: the lane reset is what would
// make the head unreachable, and no judge retired the item's criteria. Both halves are MQ1's
// rule read in one place — the head survives for inspection and the item stays in-queue.
func TestARedGatePinsTheHeadAndFreezesNoVerdict(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 1), always(t, "land", 0), always(t, "verdict", 0)))

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("the loop stopped %q (%s), want %q", result.Stop, result.Detail, drain.StopQueueDrained)
	}
	pinned := strings.TrimSpace(git(t, f.root, "rev-parse", "refs/tillr/runs/run-item_041-a1"))
	if pinned == "" {
		t.Fatal("the refused head was not pinned")
	}
	if got := itemStateOf(t, f.store, "item_041"); got != "in-queue" {
		t.Fatalf("the failed item is %q in the store, want in-queue", got)
	}
	if runs := lines(t, f.count); countMatching(runs, "--verdict fail") != 0 {
		t.Fatalf("a red gate froze a failing verdict: %v", runs)
	}
}
