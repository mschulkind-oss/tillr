package main_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

// The three stubs the wall cases drive, all on the real pi adapter so the harness flag the
// session id travels in is the one the adapter really passes (design D-RW2, D-RW3).
//
// The first commits before it hits the wall: the shape `04b` and `04c` took, where a
// committed candidate was stranded by the exit code alone.
const committedWallHarness = harnessCount + `echo candidate > candidate.txt
git add candidate.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
printf 'RESULT: done\n'
exit 124
`

// harnessCount marks one harness invocation in the fixture's count file, so a case answers
// "how many segments ran" without reading a clock.
const harnessCount = `if [ -n "$STUB_COUNT" ]; then echo "harness segment" >> "$STUB_COUNT"; fi
`

// The second hits the wall on its first segment with nothing committed, then commits on the
// continuation: the shape `04d` took, where the work was on disk and only the wall was
// between it and a judged commit.
const resumingWallHarness = harnessCount + `prev=
for arg in "$@"; do
    if [ "$prev" = "--session-id" ] || [ "$prev" = "--session" ]; then printf 'session-id %s\n' "$arg" >> "$STUB_ARGV"; fi
    prev=$arg
done
if [ -f "$STUB_SEGMENT" ]; then seg=2; else seg=1; : > "$STUB_SEGMENT"; fi
echo "harness-segment: $seg"
if [ "$seg" = 1 ]; then
    exit 124
fi
echo candidate > candidate.txt
git add candidate.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
printf 'RESULT: done\n'
exit 0
`

// The third hits the wall on both segments: the negative control for the resume's bound, so
// a loop that resumed forever would fail here rather than only in production.
const doubleWallHarness = harnessCount + `prev=
for arg in "$@"; do
    if [ "$prev" = "--session-id" ] || [ "$prev" = "--session" ]; then printf 'session-id %s\n' "$arg" >> "$STUB_ARGV"; fi
    prev=$arg
done
exit 124
`

// walledFixture is one lane staged for a run a wall can cut short: the real stager, so the
// brief, the session id, the fence and the worktree are the ones a tick writes.
type walledFixture struct {
	loop     loopFixture
	item     string
	laneDir  string
	worktree string
	// argv is where the stub harness records the session id it was handed, one line per
	// segment.
	argv string
}

// newWalledFixture builds the fixture and stages lane-1 for item_041 with the given stub
// harness in place of the model.
func newWalledFixture(t *testing.T, harness string) walledFixture {
	t.Helper()
	adapter, err := filepath.Abs(filepath.Join("..", "tillr-run", "backends", "pi", "run-pi"))
	if err != nil {
		t.Fatalf("resolving the pi adapter failed: %v", err)
	}
	stubHarness := stub(t, "pi", "[ \"${1:-}\" != --features ] || exit 2\n"+harness)
	t.Setenv("PATH", filepath.Dir(stubHarness)+string(os.PathListSeparator)+os.Getenv("PATH"))
	argv := filepath.Join(scratch(t, "wall-argv"), "argv")
	t.Setenv("STUB_ARGV", argv)
	t.Setenv("STUB_SEGMENT", filepath.Join(scratch(t, "wall-segment"), "segment"))
	f := newLoopFixture(t, adapter)
	// The adapter refuses a backend value that is not its own name or a seat alias, so the
	// fixture's backend is pi's.
	f.policy.DefaultBackend = "pi"
	delete(f.policy.Backends, "fixture")
	f.policy.Backends["pi"] = drain.Backend{
		Adapter: adapter, Model: "fixture-model", Effort: "low", TimeoutSeconds: 60,
	}
	fixtureAt(t, f.store, fencedItemRecord("item_041", "in-queue", "candidate.txt"))
	return walledFixture{
		loop: f, item: "item_041",
		laneDir:  filepath.Join(f.laneRoot, "lane-1"),
		worktree: filepath.Join(f.laneRoot, "lane-1", "worktree"),
		argv:     argv,
	}
}

// newLoop answers the loop this fixture describes: the real stager, so the brief and the
// session id are staged, and the real run binary with the three judging programs stubbed.
func (f walledFixture) newLoop(t *testing.T, gate, land, verdict string, report *strings.Builder) *drain.Loop {
	t.Helper()
	now := time.Date(2026, 11, 15, 6, 0, 0, 0, time.UTC)
	steps := &drain.ProcessSteps{
		RunBin: sibling(t, "run"), ItemBin: sibling(t, "item"),
		Store: f.loop.store, LaneRoot: f.loop.laneRoot, Root: f.loop.root,
		Now:         func() time.Time { return now },
		GateProgram: gate, LandProgram: land, VerdictProgram: verdict,
	}
	stager := &drain.LaneStager{
		Root: f.loop.root, LaneRoot: f.loop.laneRoot, Store: f.loop.store,
		Template: filepath.Join("..", "tillr-run", "backends", "brief-template.md"),
		Now:      func() time.Time { return now },
	}
	return f.loop.newLoopWith(t, 1, steps, stager, report)
}

// TestACommittedRunCutShortByAWallIsJudged is the item's third criterion: a walled run whose
// target HEAD moved off its base is a candidate, not a stranded tree, so it goes through the
// fence, the gate and the land exactly as an answered run would (design D-RW2).
func TestACommittedRunCutShortByAWallIsJudged(t *testing.T) {
	f := newWalledFixture(t, committedWallHarness)
	report := &strings.Builder{}
	gate := always(t, "gate", 0)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)
	loop := f.newLoop(t, gate, land, verdict, report)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v\n%s", err, report)
	}
	if result.Landed != 1 || result.Candidates != 1 {
		t.Fatalf("the loop landed %d and counted %d candidate(s), want 1 and 1: %s\n%s",
			result.Landed, result.Candidates, result.Detail, report)
	}
	runs := lines(t, f.loop.count)
	if countMatching(runs, "gate --item item_041") != 1 {
		t.Fatalf("a committed run cut short by a wall was not gated: %v", runs)
	}
	if countMatching(runs, "verdict") != 1 {
		t.Fatalf("a committed run cut short by a wall was not given a verdict: %v", runs)
	}
	// One attempt, one harness invocation: a wall on a committed tree is judged, never
	// resumed. A resume here would pay again for work already done.
	if n := countMatching(runs, "harness segment"); n != 1 {
		t.Fatalf("the harness ran %d time(s), want 1 (a walled commit is judged, never resumed): %v", n, runs)
	}
}

// TestAnUncommittedRunCutShortByAWallResumesOnceAndLands is the item's fourth criterion's
// first half: with the lane still holding its brief, its session id and its worktree, the
// attempt gets one continuation in place, and a commit from it is judged and landed
// (design D-RW3).
func TestAnUncommittedRunCutShortByAWallResumesOnceAndLands(t *testing.T) {
	f := newWalledFixture(t, resumingWallHarness)
	report := &strings.Builder{}
	gate := always(t, "gate", 0)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)
	loop := f.newLoop(t, gate, land, verdict, report)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v\n%s", err, report)
	}
	if result.Landed != 1 {
		t.Fatalf("the loop landed %d, want the resumed segment's candidate: %s\n%s",
			result.Landed, result.Detail, report)
	}
	segments := lines(t, f.argv)
	if len(segments) != 2 {
		t.Fatalf("the harness ran %d segment(s), want 2 (the wall, then the continuation): %v", len(segments), segments)
	}
	if n := countMatching(lines(t, f.loop.count), "harness segment"); n != 2 {
		t.Fatalf("the harness body ran %d time(s), want 2: %v", n, lines(t, f.loop.count))
	}
	if segments[0] != segments[1] {
		t.Fatalf("the continuation did not continue the first segment's session: %v", segments)
	}
}

// TestAnUncommittedRunCutShortByAWallTwiceIsHeldUnjudged is the second half: the resume is
// bounded at one per attempt, so a continuation that hits the wall again with nothing
// committed leaves the item held as `run-unjudged` rather than looping (design D-RW3).
func TestAnUncommittedRunCutShortByAWallTwiceIsHeldUnjudged(t *testing.T) {
	f := newWalledFixture(t, doubleWallHarness)
	report := &strings.Builder{}
	gate := always(t, "gate", 0)
	land := always(t, "land", 0)
	verdict := always(t, "verdict", 0)
	loop := f.newLoop(t, gate, land, verdict, report)

	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v\n%s", err, report)
	}
	if result.Landed != 0 || result.Rejected != 0 {
		t.Fatalf("the loop landed %d and rejected %d, want none: %s", result.Landed, result.Rejected, result.Detail)
	}
	if got := itemStateOf(t, f.loop.store, "item_041"); got != "in-queue" {
		t.Fatalf("the twice-walled run left the item %q, want in-queue", got)
	}
	runs := lines(t, f.loop.count)
	if countMatching(runs, "gate --item") != 0 {
		t.Fatalf("a run with no commit was gated: %v", runs)
	}
	if !strings.Contains(result.Detail, "item_041 ("+drain.ReasonRunUnjudged+")") {
		t.Fatalf("the stop does not name the unjudged hold: %s\n%s", result.Detail, report)
	}
	// One dispatch plus exactly one continuation: `Record.Resumed` is what bounds it, not a
	// count the loop keeps.
	if n := len(lines(t, f.argv)); n != 2 {
		t.Fatalf("the harness ran %d time(s), want 2 (one resume, never more): %v", n, lines(t, f.argv))
	}
}
