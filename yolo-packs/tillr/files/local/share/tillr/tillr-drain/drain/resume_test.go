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
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// resumableFixture is one lane staged for a run that can be killed and continued: the real
// stager (so the session id, the brief, the fence and the worktree are the ones a tick
// writes), the real adapters (so the harness flag the session id travels in is the one the
// adapter really passes), and a stub harness on PATH in place of the model.
type resumableFixture struct {
	loop  loopFixture
	item  string
	runID string
	base  string
	// laneDir and worktree are the staged lane's, in the spelling the test can compare.
	laneDir  string
	worktree string
	// argv is where the stub harness records the argv it was executed with, one line per
	// segment, so a case reads the session id the adapter really passed.
	argv string
}

// resumingHarn is the stub harness: it records its argv, commits one file outside the fence
// on its first invocation and exits non-zero (the run that dies without a verdict), and on
// the second commits one file inside the fence and exits 0 (the continuation). Segment
// markers go to stdout, which `run exec` keeps as the run's log, so a case can prove the log
// was appended to rather than truncated.
const resumingHarness = `prev=
for arg in "$@"; do
    if [ "$prev" = "--session-id" ] || [ "$prev" = "--session" ]; then printf 'session-id %s\n' "$arg" >> "$STUB_ARGV"; fi
    prev=$arg
done
if [ -f "$STUB_SEGMENT" ]; then
    seg=2
else
    seg=1
    : > "$STUB_SEGMENT"
fi
echo "harness-segment: $seg"
if [ "$seg" = 1 ]; then
    mkdir -p .forks
    echo leak > .forks/leak.txt
    git add -f .forks/leak.txt
    git -c user.name=stub -c user.email=stub@example.invalid commit -q -m "leak"
    exit 1
fi
echo candidate > candidate.txt
git add candidate.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m "candidate"
exit 0
`

// newResumableFixture builds the fixture and stages the lane. The item's fence names
// candidate.txt only, so the first segment's committed leak.txt is a path the fence refuses.
func newResumableFixture(t *testing.T) resumableFixture {
	t.Helper()
	adapter, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "pi", "run-pi"))
	if err != nil {
		t.Fatalf("resolving the pi adapter failed: %v", err)
	}
	harness := stub(t, "pi", "[ \"${1:-}\" != --features ] || exit 2\n"+resumingHarness)
	t.Setenv("PATH", filepath.Dir(harness)+string(os.PathListSeparator)+os.Getenv("PATH"))
	argv := filepath.Join(scratch(t, "resume-argv"), "argv")
	t.Setenv("STUB_ARGV", argv)
	t.Setenv("STUB_SEGMENT", filepath.Join(scratch(t, "resume-segment"), "segment"))
	f := newLoopFixture(t, adapter)
	// The adapter's own name is `pi` (or a `pi-<seat>` alias), and it refuses a backend value
	// that disagrees with it: the policy's backend for this fixture is therefore pi's own.
	f.policy.DefaultBackend = "pi"
	delete(f.policy.Backends, "fixture")
	f.policy.Backends["pi"] = drain.Backend{
		Adapter: adapter, Model: "fixture-model", Effort: "low", TimeoutSeconds: 0,
	}
	fixtureAt(t, f.store, fencedItemRecord("item_041", "in-queue", "candidate.txt"))
	stager := &drain.LaneStager{
		Root: f.root, LaneRoot: f.laneRoot, Store: f.store,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      func() time.Time { return time.Date(2026, 11, 15, 6, 0, 0, 0, time.UTC) },
	}
	runID := "run-item_041-1"
	if err := stager.Stage(context.Background(), drain.StageSpec{
		LaneID: "lane-1", ItemID: "item_041", RunID: runID, Backend: "pi",
		BaseSHA: f.rootBase(t), WorkingDir: "worktree", TimeoutMS: 0,
	}); err != nil {
		t.Fatalf("staging the lane failed: %v", err)
	}
	return resumableFixture{
		loop: f, item: "item_041", runID: runID, base: f.rootBase(t),
		laneDir:  filepath.Join(f.laneRoot, "lane-1"),
		worktree: filepath.Join(f.laneRoot, "lane-1", "worktree"),
		argv:     argv,
	}
}

// rootBase answers the fixture repository's trunk tip.
func (f loopFixture) rootBase(t *testing.T) string {
	t.Helper()
	return git(t, f.root, "rev-parse", "main")
}

// steps answers the real step set for this fixture: the run binary is the built one, and the
// judging programs are absent because a resume judges nothing.
func (f resumableFixture) steps(t *testing.T) *drain.ProcessSteps {
	t.Helper()
	now := time.Date(2026, 11, 15, 6, 0, 0, 0, time.UTC)
	return &drain.ProcessSteps{
		RunBin: sibling(t, "run"), ItemBin: sibling(t, "item"),
		Store: f.loop.store, LaneRoot: f.loop.laneRoot, Root: f.loop.root,
		Now: func() time.Time { return now },
	}
}

// firstSegment runs the run once and leaves it dead without a verdict: the state a resume
// exists for.
func (f resumableFixture) firstSegment(t *testing.T, steps *drain.ProcessSteps) {
	t.Helper()
	request := f.loop.policy.Backends["pi"].Request(f.item, "lane-1", f.runID, "pi")
	rec, _, err := steps.Run(context.Background(), request)
	if err != nil {
		t.Fatalf("running the first segment failed: %v", err)
	}
	if rec.ExitCode == nil || *rec.ExitCode == 0 {
		t.Fatalf("the first segment's record says it succeeded (%+v): the stub harness exits non-zero and a face that hid that would make the case meaningless", rec)
	}
	if f.head(t) == f.base {
		logText, _ := os.ReadFile(filepath.Join(f.laneDir, f.runID+".log"))
		streamErr, _ := os.ReadFile(filepath.Join(f.laneDir, "stream.err"))
		report, _ := os.ReadFile(filepath.Join(f.laneDir, "report.md"))
		t.Fatalf("the first segment committed nothing, so there is nothing a fence could refuse\nrecord: %+v\nlog: %s\nstream.err: %s\nreport: %s", rec, logText, streamErr, report)
	}
}

// head answers the lane worktree's HEAD.
func (f resumableFixture) head(t *testing.T) string {
	t.Helper()
	return git(t, f.worktree, "rev-parse", "HEAD")
}

// sessionID answers the lane's staged session id.
func (f resumableFixture) sessionID(t *testing.T) string {
	t.Helper()
	return strings.TrimSpace(readText(t, filepath.Join(f.laneDir, drain.SessionIDFile)))
}

// TestAResumeContinuesTheKilledRunsOwnSession is the item's second criterion, end to end: a
// run that died without a verdict is continued from its own harness session, reusing the
// lane, the brief and the fence, with one runs row and the resume recorded as its own span.
func TestAResumeContinuesTheKilledRunsOwnSession(t *testing.T) {
	f := newResumableFixture(t)
	steps := f.steps(t)
	f.firstSegment(t, steps)
	sessionID := f.sessionID(t)
	if sessionID == "" {
		t.Fatalf("staging wrote no session id, so there is no session a resume could continue")
	}
	killed := f.head(t)

	result, err := drain.Resume(context.Background(), drain.ResumeConfig{
		Root: f.loop.root, Store: f.loop.store, LaneRoot: f.loop.laneRoot,
		Steps: steps, Now: steps.Now,
	}, drain.ResumeSpec{RunID: f.runID})
	if err != nil {
		t.Fatalf("resuming the killed run failed: %v", err)
	}
	if !result.Run.Resumed {
		t.Fatalf("the resumed segment's record does not say it was resumed: %+v", result.Run)
	}
	if result.SessionID != sessionID {
		t.Fatalf("the resume continued session %q, but the lane pinned %q", result.SessionID, sessionID)
	}
	// The adapter is what carries the session id to the harness, so the flag it really passed
	// is the evidence: the same --session-id on both segments, and it is the staged one.
	invocations := lines(t, f.argv)
	if len(invocations) != 2 {
		t.Fatalf("the harness ran %d time(s), want 2 (the killed segment and the resume): %v", len(invocations), invocations)
	}
	for i, recorded := range invocations {
		if recorded != "session-id "+sessionID {
			t.Fatalf("harness invocation %d recorded %q, want %q: a harness that was not given the staged id mints one of its own, and the loop then cannot name the session it would resume", i+1, recorded, "session-id "+sessionID)
		}
	}
	// The log is the run's record of both segments: a resume that truncated it would have
	// deleted the transcript the continuation extends.
	logText := readText(t, filepath.Join(f.laneDir, f.runID+".log"))
	for _, want := range []string{"harness-segment: 1", "harness-segment: 2"} {
		if !strings.Contains(logText, want) {
			t.Fatalf("the run's log does not hold %q, so the resume truncated the killed segment's log:\n%s", want, logText)
		}
	}
	// One run, two segments: the runs row is the same row, and the resume is a span on it.
	if rows := countRows(t, f.loop.store, `SELECT count(*) FROM runs WHERE run_id = ?`, f.runID); rows != 1 {
		t.Fatalf("the store holds %d runs rows for %s, want 1: a resume is one attempt continued, not a second attempt", rows, f.runID)
	}
	if resumes := countRows(t, f.loop.store, `SELECT count(*) FROM events WHERE kind = 'run.resume' AND object_id = ?`, f.runID); resumes != 1 {
		t.Fatalf("the store holds %d run.resume event(s) for %s, want 1", resumes, f.runID)
	}
	if result.Before == nil || result.After == nil {
		t.Fatalf("the resume took no bracket")
	}
	if got := worktreeHead(t, result.Before, f.worktree); got != killed {
		t.Fatalf("the resume's bracket starts at %s, want the killed segment's commit %s: the bracket is the one re-taken at resume", got, killed)
	}
}

// TestAResumeRetakesTheBracketAndRefusesWhatTheStaleOneAdmits is the item's third criterion,
// with its negative control.
//
// The resume re-takes the bracket: its before-snapshot is the state the continuation starts
// from (the killed segment's commit), not the dispatch's. And the stale bracket's blind spot
// is demonstrated rather than asserted — the killed segment's committed leak.txt is inside the
// target worktree, which Compare excuses, so the stale bracket admits it; the fence is what
// refuses it. That is why a resume re-derives the fence as well as re-taking the bracket.
func TestAResumeRetakesTheBracketAndRefusesWhatTheStaleOneAdmits(t *testing.T) {
	f := newResumableFixture(t)
	installLoopTestDenyList(t, f.loop.root)
	steps := f.steps(t)
	// The bracket a dispatch takes: before the killed segment has written anything.
	stale, err := drain.TakeBoundary(context.Background(), drain.BoundarySpec{
		Repo: f.loop.root, Targets: []string{f.worktree},
	})
	if err != nil {
		t.Fatalf("taking the dispatch-time bracket failed: %v", err)
	}
	if got := worktreeHead(t, stale, f.worktree); got != f.base {
		t.Fatalf("the stale bracket starts at %s, want the base %s", got, f.base)
	}
	f.firstSegment(t, steps)
	killed := f.head(t)
	result, err := drain.Resume(context.Background(), drain.ResumeConfig{
		Root: f.loop.root, Store: f.loop.store, LaneRoot: f.loop.laneRoot,
		Steps: steps, Now: steps.Now,
	}, drain.ResumeSpec{RunID: f.runID})
	if err != nil {
		t.Fatalf("resuming the killed run failed: %v", err)
	}
	if got := worktreeHead(t, result.Before, f.worktree); got != killed {
		t.Fatalf("the resume's bracket starts at %s, want %s", got, killed)
	}
	if head := f.head(t); head == killed {
		t.Fatalf("the resumed segment committed nothing, so the case proves nothing")
	}
	// The negative control: the stale bracket admits the out-of-fence commit.
	staleViolations, err := drain.Compare(stale, result.After)
	if err != nil {
		t.Fatalf("comparing the stale bracket failed: %v", err)
	}
	for _, violation := range staleViolations {
		if strings.Contains(violation.Detail, ".forks/leak.txt") {
			t.Fatalf("the stale bracket reported .forks/leak.txt, so this case cannot show what it misses: %s", violation)
		}
	}
	// …and the fence refuses it, which is why a resume re-derives the fence rather than
	// trusting the staged one and the bracket rather than trusting the dispatch's.
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
		Root: f.loop.root, Store: f.loop.store, LaneRoot: f.loop.laneRoot, Lanes: []string{"lane-1"},
		Policy: f.loop.policy, Steps: steps,
		Stage: &drain.LaneStager{Root: f.loop.root, LaneRoot: f.loop.laneRoot, Store: f.loop.store},
		Now:   steps.Now, Report: io.Discard,
	})
	if err != nil {
		t.Fatalf("building a loop to read the fence failed: %v", err)
	}
	var judged drain.LoopResult
	if _, _, err := loop.Verify(context.Background(), drain.Candidate{
		ItemID: f.item, LaneID: "lane-1", RunID: f.runID, Worktree: f.worktree,
		BaseSHA: f.base, HeadSHA: f.head(t),
	}, &judged, nil); err != nil {
		t.Fatalf("checking the candidate's fence failed: %v", err)
	}
	if judged.RefusedSteps[drain.RefusalFence] == 0 {
		t.Fatalf("the fence admitted a change it must refuse (leak.txt): the negative control is not a control: %+v", judged.RefusedSteps)
	}
}

// TestAResumeRefusesARunWithNoSessionToContinue is the refusal that keeps a cold start from
// wearing a resume's name: with no session id there is no transcript to reuse, and paying for
// the whole run again is a dispatch, not a continuation.
func TestAResumeRefusesARunWithNoSessionToContinue(t *testing.T) {
	f := newResumableFixture(t)
	steps := f.steps(t)
	f.firstSegment(t, steps)
	if err := os.Remove(filepath.Join(f.laneDir, drain.SessionIDFile)); err != nil {
		t.Fatalf("removing the lane's session id failed: %v", err)
	}
	if _, err := drain.Resume(context.Background(), drain.ResumeConfig{
		Root: f.loop.root, Store: f.loop.store, LaneRoot: f.loop.laneRoot,
		Steps: steps, Now: steps.Now,
	}, drain.ResumeSpec{RunID: f.runID}); err == nil {
		t.Fatalf("a resume with no session to continue was allowed")
	}
}

// TestAResumeRefusesAJudgedRun keeps the resume out of the attempt budget's territory: a run
// the gate judged is continued by a retry, which a person authorizes, and not by a resume.
func TestAResumeRefusesAJudgedRun(t *testing.T) {
	f := newResumableFixture(t)
	steps := f.steps(t)
	f.firstSegment(t, steps)
	s, err := store.Open(context.Background(), f.loop.store, true)
	if err != nil {
		t.Fatalf("opening the fixture store failed: %v", err)
	}
	if err := s.WriteTx(context.Background(), func(tx *store.Tx) error {
		body, err := json.Marshal(map[string]string{"run_id": f.runID})
		if err != nil {
			return err
		}
		_, err = tx.Append(event.Event{
			Kind: drain.GateVerdictKind, ObjectType: "run", ObjectID: &f.runID, Payload: body,
		})
		return err
	}); err != nil {
		_ = s.Close()
		t.Skipf("the fixture cannot append the gate verdict this case needs: %v", err)
	}
	_ = s.Close()
	if _, err := drain.Resume(context.Background(), drain.ResumeConfig{
		Root: f.loop.root, Store: f.loop.store, LaneRoot: f.loop.laneRoot,
		Steps: steps, Now: steps.Now,
	}, drain.ResumeSpec{RunID: f.runID}); err == nil {
		t.Fatalf("a judged run was resumed, which spends a second attempt on judged work")
	}
}

// worktreeHead answers the HEAD a bracket recorded for the lane worktree, comparing canonical
// paths because TakeBoundary resolves them.
func worktreeHead(t *testing.T, bracket *drain.Boundary, worktree string) string {
	t.Helper()
	want, err := filepath.EvalSymlinks(worktree)
	if err != nil {
		t.Fatalf("resolving %s failed: %v", worktree, err)
	}
	for _, wt := range bracket.Worktrees {
		path, err := filepath.EvalSymlinks(wt.Path)
		if err != nil {
			continue
		}
		if path == want {
			return wt.Head
		}
	}
	t.Fatalf("the bracket holds no worktree at %s: %+v", worktree, bracket.Worktrees)
	return ""
}

// countRows answers one COUNT(*) query against a store.
func countRows(t *testing.T, storePath, query string, args ...any) int {
	t.Helper()
	r, err := store.OpenRead(context.Background(), storePath)
	if err != nil {
		t.Fatalf("opening %s failed: %v", storePath, err)
	}
	defer r.Close()
	var count int
	if err := r.ReadTx(context.Background(), func(reader *store.Reader) error {
		return reader.QueryRow(query, args...).Scan(&count)
	}); err != nil {
		t.Fatalf("counting rows in %s failed: %v", storePath, err)
	}
	return count
}
