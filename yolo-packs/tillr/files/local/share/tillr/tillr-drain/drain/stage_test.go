package drain_test

import (
	"context"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

// fencedItemRecord is a seeded item carrying a fence and one criterion: the minimum an item
// needs to be staged at all.
func fencedItemRecord(itemID, state string, paths ...string) string {
	return envelope("item.record", "item-"+itemID, map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"body_ref":      "briefs/" + itemID + ".md",
		"state":         state,
		"blocked_by":    []string{},
		"allowed_paths": paths,
		"criteria": []map[string]string{
			{"criterion_id": "c1", "text": "the first criterion of " + itemID, "state": ""},
		},
	})
}

// stageFixture is one staging fixture: a repository with a trunk commit, a lane root, a store
// and the tracked template.
type stageFixture struct {
	root     string
	laneRoot string
	store    string
	base     string
}

// newStageFixture builds the fixture: a scratch repository under $YOLO_DURABLE_DIR, never the
// repository this suite runs in, so a case cannot stage a worktree into the tree under test.
func newStageFixture(t *testing.T, records ...string) stageFixture {
	t.Helper()
	root := filepath.Join(scratch(t, "stage-repo"), "repo")
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatalf("creating the staging repository failed: %v", err)
	}
	git(t, root, "init", "-b", "main")
	writeFile(t, filepath.Join(root, "base.txt"), "base\n")
	git(t, root, "add", "base.txt")
	git(t, root, "commit", "-m", "base")
	laneRoot := filepath.Join(scratch(t, "stage-lanes"), "lanes")
	if err := os.MkdirAll(laneRoot, 0o755); err != nil {
		t.Fatalf("creating the staging lane root failed: %v", err)
	}
	store := filepath.Join(scratch(t, "stage-store"), "board.sqlite3")
	fixtureAt(t, store, records...)
	return stageFixture{root: root, laneRoot: laneRoot, store: store, base: git(t, root, "rev-parse", "main")}
}

// stager answers the real stager for this fixture, reading the repository's own tracked
// template — a copy would pass after the real one drifted — with an injected clock.
func (f stageFixture) stager() *drain.LaneStager {
	return &drain.LaneStager{
		Root: f.root, LaneRoot: f.laneRoot, Store: f.store,
		Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
		Now:      func() time.Time { return time.Date(2026, 11, 12, 0, 0, 0, 0, time.UTC) },
	}
}

func (f stageFixture) spec(lane string) drain.StageSpec {
	return drain.StageSpec{
		LaneID: lane, ItemID: "item_041", RunID: "run-item_041-1", Backend: "pi",
		BaseSHA: f.base, WorkingDir: "worktree", TimeoutMS: 60000,
	}
}

// formatProblems runs the adapters' OWN preflight over a staged lane directory: the shell
// file the three adapters source, not a copy of its checks. It answers the exit code and the
// refusal text, so a case can assert on both.
func adapterPreflight(t *testing.T, worktree, brief string) (int, string) {
	t.Helper()
	preflight, err := filepath.Abs(filepath.Join("..", "..", "tillr-run", "backends", "preflight.sh"))
	if err != nil {
		t.Fatalf("resolving the adapters' preflight path failed: %v", err)
	}
	if _, err := os.Stat(preflight); err != nil {
		t.Fatalf("the adapters' preflight %s is not there: %v", preflight, err)
	}
	script := `. "$1"
PROG=probe
preflight_brief "$2"
resolve_target "$3"
`
	// The adapter's own cwd is the target, and `resolve_target` refuses anything else; the
	// brief travels as the adapters' default relative path (`../brief.md`), so the case pins
	// the resolution staging depends on and not only the file's contents.
	cmd := exec.Command("sh", "-c", script, "preflight", preflight, brief, worktree)
	cmd.Dir = worktree
	out, err := cmd.CombinedOutput()
	if exit, ok := err.(*exec.ExitError); ok {
		return exit.ExitCode(), string(out)
	}
	if err != nil {
		t.Fatalf("running the adapters' preflight failed: %v: %s", err, out)
	}
	return 0, string(out)
}

func TestStagingDoesNotCarryTheItemsOldAllowList(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "tools/tillr-view"))
	if err := f.stager().Stage(context.Background(), f.spec("lane-1")); err != nil {
		t.Fatal(err)
	}
	body, err := os.ReadFile(filepath.Join(f.laneRoot, "lane-1", drain.FenceFile))
	if err != nil || len(body) != 0 {
		t.Fatalf("ordinary stage inherited the retired list: %q, %v", body, err)
	}
}

// A staged lane satisfies the adapters' preflight: the brief, the fence, the base sha and a
// worktree whose branch is the lane's, all where the template's own comment says they are.
//
// The negative control is the same preflight over a brief that carries an unfilled
// placeholder: it refuses with exit 2, so the clean pass in the first half is a pass the
// preflight could have failed.
func TestStagedLaneSatisfiesTheAdaptersPreflight(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "tools/tillr-drain"))
	stager := f.stager()
	spec := f.spec("lane-1")
	if err := stager.Stage(context.Background(), spec); err != nil {
		t.Fatalf("staging a clean lane failed: %v", err)
	}
	laneDir := filepath.Join(f.laneRoot, "lane-1")
	worktree := filepath.Join(laneDir, "worktree")
	brief := filepath.Join(laneDir, "brief.md")

	if code, out := adapterPreflight(t, worktree, "../brief.md"); code != 0 {
		t.Fatalf("the adapters' preflight refused a staged lane (exit %d): %s", code, out)
	}
	raw, err := os.ReadFile(brief)
	if err != nil {
		t.Fatalf("reading the staged brief failed: %v", err)
	}
	text := string(raw)
	for _, want := range []string{
		"# item_041 — the item item_041",
		"- **Run:** run-item_041-1",
		"- **Backend:** pi",
		"- **Base sha:** " + f.base,
		"- **Target:** " + worktree + " (lane worktree)",
		"1. the first criterion of item_041",
		"## Scope and boundaries",
		"Change what the work needs",
		"briefs/item_041.md",
	} {
		if !strings.Contains(text, want) {
			t.Fatalf("the staged brief does not carry %q:\n%s", want, text)
		}
	}
	if strings.Contains(text, "{{") {
		t.Fatalf("the staged brief still carries an unfilled placeholder:\n%s", text)
	}
	if strings.Contains(text, "{{DRIVER_REASON}}") || strings.Contains(text, "Why not a worktree") {
		t.Fatalf("the staged brief kept the main-checkout specimen it is not:\n%s", text)
	}
	fence, err := os.ReadFile(filepath.Join(laneDir, "fence.txt"))
	if err != nil {
		t.Fatalf("reading the staged fence failed: %v", err)
	}
	// Existing items' allowed_paths lists are ignored (owner ruling 2026-11-19):
	// ordinary staged items write an empty fence.txt and rely on the global deny-list.
	if string(fence) != "" {
		t.Fatalf("the fence is %q, want empty", fence)
	}
	base, err := os.ReadFile(filepath.Join(laneDir, "base_sha"))
	if err != nil {
		t.Fatalf("reading the pinned base sha failed: %v", err)
	}
	if strings.TrimSpace(string(base)) != f.base {
		t.Fatalf("the pinned base sha is %q, want %q", base, f.base)
	}
	if branch, want := git(t, worktree, "rev-parse", "--abbrev-ref", "HEAD"), drain.LaneBranch(f.laneRoot, "lane-1"); branch != want {
		t.Fatalf("the staged worktree is on %q, want the lane's own branch %s", branch, want)
	}

	// The negative control: the same preflight refuses a brief that carries a placeholder.
	broken := filepath.Join(scratch(t, "stage-broken"), "brief.md")
	writeFile(t, broken, strings.Replace(text, "briefs/item_041.md", "{{UNFILLED}}", 1))
	if code, out := adapterPreflight(t, worktree, broken); code != 2 {
		t.Fatalf("the preflight accepted a brief carrying a placeholder (exit %d): %s", code, out)
	}
}

// An item that names no fence is refused by staging, naming the field an author can fill.
//
// The negative control is the message: a stager that shipped the brief anyway would produce
// a lane whose fence is empty, and this assertion would fail on the missing field name.
func TestLargeConflictEvidenceIsStoredOutsideTheBrief(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "tools/tillr-drain"))
	spec := f.spec("lane-1")
	markers := strings.Repeat("rows := []row{{ID: 1}} // {{TITLE}}\n", 20000)
	spec.Conflict = &drain.ConflictBrief{Markers: markers, HeadSHA: f.base, TrunkSHA: f.base, OriginalRunID: "original"}
	if err := f.stager().Stage(context.Background(), spec); err != nil {
		t.Fatal(err)
	}
	laneDir := filepath.Join(f.laneRoot, "lane-1")
	brief := readAll(t, filepath.Join(laneDir, "brief.md"))
	markerPath := filepath.Join(laneDir, "durable", spec.RunID, "conflict-markers.txt")
	if got := readAll(t, markerPath); got != markers {
		t.Fatalf("evidence changed: got %d bytes, want %d", len(got), len(markers))
	}
	if len(brief) > 16000 || !strings.Contains(brief, markerPath) {
		t.Fatalf("brief must stay bounded and reference evidence; got %d bytes", len(brief))
	}
	if code, out := adapterPreflight(t, filepath.Join(laneDir, "worktree"), "../brief.md"); code != 0 {
		t.Fatalf("preflight rejected evidence reference: %d: %s", code, out)
	}
}

func TestStagingAcceptsAnItemWithNoFence(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue"))
	if err := f.stager().Stage(context.Background(), f.spec("lane-1")); err != nil {
		t.Fatalf("staging an item with no allowed_paths failed: %v", err)
	}
	worktree := filepath.Join(f.laneRoot, "lane-1", "worktree")
	if code, out := adapterPreflight(t, worktree, "../brief.md"); code != 0 {
		t.Fatalf("preflight refused a staged item with no allowed_paths (exit %d): %s", code, out)
	}
}

// A stage of a clean lane, run twice, is the same lane: same brief, same fence, same pinned
// sha, one worktree. It is what makes a re-dispatched tick safe.
func TestRestagingACleanLaneIsIdempotent(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "tools/tillr-drain"))
	stager := f.stager()
	ctx := context.Background()
	if err := stager.Stage(ctx, f.spec("lane-1")); err != nil {
		t.Fatalf("the first stage failed: %v", err)
	}
	laneDir := filepath.Join(f.laneRoot, "lane-1")
	first := readAll(t, filepath.Join(laneDir, "brief.md"))
	if err := stager.Stage(ctx, f.spec("lane-1")); err != nil {
		t.Fatalf("re-staging a clean lane failed: %v", err)
	}
	if second := readAll(t, filepath.Join(laneDir, "brief.md")); second != first {
		t.Fatalf("re-staging rewrote the brief:\n%s\nwant:\n%s", second, first)
	}
	if head := git(t, filepath.Join(laneDir, "worktree"), "rev-parse", "HEAD"); head != f.base {
		t.Fatalf("the reused worktree is at %s, want the base %s", head, f.base)
	}
	if listed := git(t, f.root, "worktree", "list", "--porcelain"); strings.Count(listed, "worktree ") != 2 {
		t.Fatalf("re-staging registered another worktree: %s", listed)
	}
}

// A lane whose worktree holds files no commit carries is cleared and staged again: a file is
// not a candidate, and a leftover one would otherwise fail every later tick in the lane (the
// directory exists, so `worktree add` refuses it) or leak into the next run's tree.
//
// The negative control is the second half of the case, and it is the rule this clearing must
// never break: the same lane with the same file committed is refused by name and left alone,
// because a candidate is a commit and resetting a lane deletes the only copy of work nobody
// has judged.
func TestRestagingADirtyLaneClearsJunkAndRefusesACommit(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "tools/tillr-drain"))
	stager := f.stager()
	ctx := context.Background()
	if err := stager.Stage(ctx, f.spec("lane-1")); err != nil {
		t.Fatalf("the first stage failed: %v", err)
	}
	worktree := filepath.Join(f.laneRoot, "lane-1", "worktree")
	stray := filepath.Join(worktree, "stray.txt")
	writeFile(t, stray, "somebody's undecided edit\n")

	if err := stager.Stage(ctx, f.spec("lane-1")); err != nil {
		t.Fatalf("staging over an uncommitted file failed: %v", err)
	}
	if _, err := os.Stat(stray); !os.IsNotExist(err) {
		t.Fatalf("the re-staged lane still holds the file no commit carried (%v)", err)
	}
	if head := git(t, worktree, "rev-parse", "HEAD"); head != f.base {
		t.Fatalf("the re-staged worktree is at %s, want the base %s", head, f.base)
	}
	if listed := git(t, f.root, "worktree", "list", "--porcelain"); strings.Count(listed, "worktree ") != 2 {
		t.Fatalf("re-staging registered another worktree: %s", listed)
	}

	// The same lane, the same file, committed: a candidate, and not the stager's to reset.
	writeFile(t, stray, "a candidate\n")
	git(t, worktree, "add", "stray.txt")
	git(t, worktree, "commit", "-m", "candidate")
	candidate := git(t, worktree, "rev-parse", "HEAD")
	err := stager.Stage(ctx, f.spec("lane-1"))
	if err == nil {
		t.Fatalf("staging over an unlanded commit succeeded")
	}
	if !strings.Contains(err.Error(), candidate) {
		t.Fatalf("the refusal does not name the commit it would have deleted: %v", err)
	}
	if head := git(t, worktree, "rev-parse", "HEAD"); head != candidate {
		t.Fatalf("the refused stage moved HEAD to %s, want the candidate %s", head, candidate)
	}
	if body := readAll(t, stray); body != "a candidate\n" {
		t.Fatalf("the refused stage discarded the candidate's file: %q", body)
	}
}

// A lane whose worktree directory was deleted but whose registration survived is staged
// again: the registration is pruned before the add, so `worktree add` cannot fail on a branch
// a dead worktree still claims.
//
// This is the canary's second attempt as a case: it died on
// `fatal: cannot force update the branch 'tillr/lane-1' used by worktree at ...`, one tick
// after a failed run, with no code change between them.
func TestStagingClearsAStaleWorktreeRegistration(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "tools/tillr-drain"))
	stager := f.stager()
	ctx := context.Background()
	if err := stager.Stage(ctx, f.spec("lane-1")); err != nil {
		t.Fatalf("the first stage failed: %v", err)
	}
	worktree := filepath.Join(f.laneRoot, "lane-1", "worktree")
	// The crash shape: the directory is gone, the registration and the branch remain.
	if err := os.RemoveAll(worktree); err != nil {
		t.Fatalf("removing the worktree directory failed: %v", err)
	}
	if listed := git(t, f.root, "worktree", "list", "--porcelain"); !strings.Contains(listed, worktree) {
		t.Fatalf("the fixture did not leave a stale registration behind: %s", listed)
	}

	if err := stager.Stage(ctx, f.spec("lane-1")); err != nil {
		t.Fatalf("staging over a stale registration failed: %v", err)
	}
	if head := git(t, worktree, "rev-parse", "HEAD"); head != f.base {
		t.Fatalf("the re-staged worktree is at %s, want the base %s", head, f.base)
	}
	if branch, want := git(t, worktree, "rev-parse", "--abbrev-ref", "HEAD"), drain.LaneBranch(f.laneRoot, "lane-1"); branch != want {
		t.Fatalf("the re-staged worktree is on %q, want %s", branch, want)
	}
	if listed := git(t, f.root, "worktree", "list", "--porcelain"); strings.Count(listed, "worktree ") != 2 {
		t.Fatalf("staging over a stale registration registered another worktree: %s", listed)
	}
}

// One repository is shared by every lane root that stages into it, so `lane-1` in two lane
// roots is two lanes: the branch a lane commits onto is scoped by its lane root, and the
// second root's stage succeeds instead of dying on
// `fatal: cannot force update the branch 'tillr/lane-1' used by worktree at ...`
// (docs/design/tillr-drain-acceptance-2026-11-13.md Section 4.6).
//
// The two roots are the shape the acceptance run measured: one outside the repository (the
// fixture's own lane root) and one inside it (`<repo>/canary/lanes`).
func TestTwoLaneRootsOfOneRepositoryStageTheSameLane(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "tools/tillr-drain"))
	ctx := context.Background()
	inside := f.stager()
	inside.LaneRoot = filepath.Join(f.root, "canary", "lanes")
	outside := f.stager()

	for _, stager := range []*drain.LaneStager{inside, outside} {
		if err := stager.Stage(ctx, f.spec("lane-1")); err != nil {
			t.Fatalf("staging lane-1 in the lane root %s failed: %v", stager.LaneRoot, err)
		}
	}

	branches := map[string]string{}
	for _, stager := range []*drain.LaneStager{inside, outside} {
		worktree := filepath.Join(stager.LaneRoot, "lane-1", "worktree")
		if head := git(t, worktree, "rev-parse", "HEAD"); head != f.base {
			t.Fatalf("the lane in %s is at %s, want the base %s", stager.LaneRoot, head, f.base)
		}
		branch := git(t, worktree, "rev-parse", "--abbrev-ref", "HEAD")
		if !strings.HasPrefix(branch, "tillr/") || !strings.HasSuffix(branch, "/lane-1") {
			t.Fatalf("the lane in %s is on %q, want a branch scoped by its lane root and named for lane-1", stager.LaneRoot, branch)
		}
		if holder, seen := branches[branch]; seen {
			t.Fatalf("the lane roots %s and %s both stage lane-1 onto %s: a lane root scopes the branch", holder, stager.LaneRoot, branch)
		}
		branches[branch] = stager.LaneRoot
	}
}

// A worktree that still holds the lane's branch at a directory that exists is named in the
// refusal. `clearWorktree` prunes only registrations whose directories are gone, so a lane
// directory moved aside keeps its registration and its branch, and the `worktree add` that
// follows would otherwise fail on a `fatal:` line that names neither the holder nor the lane
// root it belongs to.
//
// The negative control is the message: `git worktree move` leaves exactly the shape a crashed
// or hand-moved lane does, and the case asserts the holder's path and no `fatal:` line.
func TestAStaleBranchHolderIsRefusedByName(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "tools/tillr-drain"))
	stager := f.stager()
	ctx := context.Background()
	if err := stager.Stage(ctx, f.spec("lane-1")); err != nil {
		t.Fatalf("the first stage failed: %v", err)
	}
	worktree := filepath.Join(f.laneRoot, "lane-1", "worktree")
	moved := filepath.Join(f.laneRoot, "lane-1-old", "worktree")
	if err := os.MkdirAll(filepath.Dir(moved), 0o755); err != nil {
		t.Fatalf("creating the moved lane's directory failed: %v", err)
	}
	git(t, f.root, "worktree", "move", worktree, moved)

	err := stager.Stage(ctx, f.spec("lane-1"))
	if err == nil {
		t.Fatalf("staging over a worktree that holds the lane's branch succeeded")
	}
	if strings.Contains(err.Error(), "fatal:") {
		t.Fatalf("the refusal is git's own line rather than a named one: %v", err)
	}
	if !strings.Contains(err.Error(), moved) {
		t.Fatalf("the refusal does not name the worktree holding the branch %s: %v", moved, err)
	}
}

// commitStager is a stager stub that records its own call where the adapter stubs record
// theirs, so a case reads the order from one file rather than from a clock.
type commitStager struct{ err error }

func (s commitStager) Stage(_ context.Context, spec drain.StageSpec) error {
	if s.err != nil {
		return s.err
	}
	f, err := os.OpenFile(os.Getenv("STUB_COUNT"), os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "STAGE %s on %s\n", spec.ItemID, spec.LaneID)
	return err
}

// A tick stages before it dispatches: the staging call is the batch's first entry in the one
// file every program appends to, and the run is the second.
//
// The negative control is the order itself: a loop that dispatched and staged afterwards (or
// not at all) would leave the adapter's line first, which is the live refusal this step
// exists to make impossible.
func TestATickStagesBeforeItDispatches(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, fencedItemRecord("item_041", "in-queue", "tools/tillr-drain"))
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{"lane-1"},
		Policy: f.policy, Steps: &evidenceProcessSteps{steps}, Stage: commitStager{},
		Now: steps.Now, Report: os.Stderr,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	if _, err := loop.Run(context.Background()); err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	got := lines(t, f.count)
	if len(got) < 2 {
		t.Fatalf("the tick recorded %d invocation(s), want a stage and a run: %v", len(got), got)
	}
	if !strings.HasPrefix(got[0], "STAGE item_041 on lane-1") {
		t.Fatalf("the first invocation was %q, want staging first: %v", got[0], got)
	}
	if !strings.Contains(got[1], "adapter") {
		t.Fatalf("the second invocation was %q, want the run: %v", got[1], got)
	}
}

// recoveringStager refuses its first `failures` calls and stages like commitStager after
// that: the shape of a harness that is broken and comes back, which is the case the ruling
// answers with a backoff rather than a stop.
type recoveringStager struct {
	failures int
	calls    int
	count    string
	laneRoot string
}

func (s *recoveringStager) Stage(_ context.Context, spec drain.StageSpec) error {
	s.calls++
	if s.calls <= s.failures {
		return fmt.Errorf("the brief could not be rendered (attempt %d)", s.calls)
	}
	if err := writeFence(s.laneRoot, spec.LaneID, []string{"candidate.txt"}); err != nil {
		return err
	}
	f, err := os.OpenFile(s.count, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0o644)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = fmt.Fprintf(f, "STAGE %s on %s\n", spec.ItemID, spec.LaneID)
	return err
}

// A staging failure is infrastructure, not a verdict, and it is not a stop: the loop reports
// it, waits, and tries the same plan again until the machinery comes back. The stager here
// refuses twice and then stages, so the case reads "kept trying" as the landing that follows.
//
// This case is the circuit breaker's rewrite at the staging path. It used to stop with
// `infra-circuit-breaker` at the third refusal; it now pins that two refusals cost two waits
// and no stop, that the third attempt dispatches, and that no judging program ran while the
// machinery was broken.
//
// The negative control is the verdict program: it is a counting stub, and an attempt that
// classified the staging failure as a candidate decision would call it. Its single invocation
// is the one the recovered attempt earned.
func TestAStagingFailureBacksOffAndTheLoopKeepsTrying(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	verdict := always(t, "verdict", 0)
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), verdict)
	var report strings.Builder
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{"lane-1"},
		Policy: f.policy, Steps: &evidenceProcessSteps{steps},
		Stage:       &recoveringStager{failures: 2, count: os.Getenv("STUB_COUNT"), laneRoot: f.laneRoot},
		BackoffBase: time.Millisecond,
		Now:         steps.Now, Report: &report,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("the loop stopped %q (%s), want %q: a refused stage is a fault, not a budget",
			result.Stop, result.Detail, drain.StopQueueDrained)
	}
	if result.InfraFailures != 2 {
		t.Fatalf("the loop counted %d infrastructure failure(s), want 2 (dispatched=%d, landed=%d, detail=%q): %s",
			result.InfraFailures, result.Dispatched, result.Landed, result.Detail, report.String())
	}
	if result.Dispatched != 1 || result.Landed != 1 || result.Rejected != 0 {
		t.Fatalf("the recovered attempt dispatched %d, landed %d and rejected %d, want 1, 1 and 0: %s",
			result.Dispatched, result.Landed, result.Rejected, report.String())
	}
	// One line per failure, and one wait per failure: the wait between attempts is what the
	// ruling asks to see, and the delays double from the base the case named.
	if countMatching(strings.Split(report.String(), "\n"), "failed in the infrastructure class") != 2 {
		t.Fatalf("the loop did not report each staging failure: %s", report.String())
	}
	for _, want := range []string{"1 consecutive infrastructure failure(s)", "waiting 1ms", "waiting 2ms"} {
		if !strings.Contains(report.String(), want) {
			t.Fatalf("the report does not carry %q: %s", want, report.String())
		}
	}
	if countMatching(lines(t, f.count), "verdict") != 1 {
		t.Fatalf("the loop judged %d candidate(s) while the machinery was broken, want the recovered attempt's one: %v",
			countMatching(lines(t, f.count), "verdict"), lines(t, f.count))
	}
}

// readAll answers one staged file's text.
func readAll(t *testing.T, path string) string {
	t.Helper()
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("reading %s failed: %v", path, err)
	}
	return string(raw)
}

// The brief's fence section names the run's own scratch directory, because a worker that
// cannot find its scratch puts it in the repository: the durable directory is outside the
// fence, so the fence is the one place that can tell it where scratch belongs.
//
// The path is the one `run exec` exports as YOLO_DURABLE_DIR — `<laneDir>/durable/<run_id>`,
// outside `<laneDir>/worktree` — and the negative control is the brief before this landed:
// it named no scratch directory at all, so a worker had only the coordinator's inherited
// value to write.
func TestTheBriefNamesTheRunsOwnScratchDirectory(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "tools/tillr-drain"))
	spec := f.spec("lane-1")
	if err := f.stager().Stage(context.Background(), spec); err != nil {
		t.Fatalf("staging a lane failed: %v", err)
	}
	laneDir := filepath.Join(f.laneRoot, "lane-1")
	brief := readAll(t, filepath.Join(laneDir, "brief.md"))
	scratch := filepath.Join(laneDir, "durable", spec.RunID)
	start := strings.Index(brief, "## Scope and boundaries")
	if start < 0 {
		t.Fatalf("the brief carries no scope and boundaries section:\n%s", brief)
	}
	fence := brief[start:]
	if end := strings.Index(fence, "\n## "); end >= 0 {
		fence = fence[:end]
	}
	if !strings.Contains(fence, scratch) {
		t.Fatalf("the brief's fence section does not name the run's scratch directory %q:\n%s", scratch, fence)
	}
	// It is outside the worktree, so the fence never has to admit it.
	if strings.HasPrefix(scratch, filepath.Join(laneDir, "worktree")+string(filepath.Separator)) {
		t.Fatalf("the scratch directory %q is inside the worktree", scratch)
	}
}

// sessionIDPattern is a UUID, which is the only form a session id may take: every adapter
// hands the value to a harness flag that keys a session by it.
var sessionIDPattern = regexp.MustCompile(`^[0-9a-f]{8}-[0-9a-f]{4}-4[0-9a-f]{3}-[89ab][0-9a-f]{3}-[0-9a-f]{12}$`)

// Staging mints the harness session one run is launched under, and writes it where the
// adapters read it: beside the brief, under the name `run-pi` resolves.
//
// The item's first criterion, measured 2026-11-14: nothing in the repository wrote this file
// (its only writer was tests/tillr-muse-backend.test.py), so the harness minted an id of its
// own, the driver never learned which session a run used, and a killed run's transcript could
// not be named — let alone continued from its prefix cache.
//
// The lane is NOT the session, which is the half a later item had to add: staging used to read
// the lane's `session-id` back, and `item-stateless-squash-lander` and
// `item-lockless-per-lane-drain` therefore ran on `lane-1` under the identical
// `--session-id 70a4f422-…`, the second item's context carrying the first item's transcript.
// So a second run on the same lane gets a session of its own, and the one caller that keeps a
// lane's id — a resume — names the id it continues. The negative control is the pinned value:
// one that is not a UUID is refused rather than handed to a harness flag.
func TestStagingMintsASessionPerRunAndKeepsAPinnedOne(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "tools/tillr-drain"))
	stager := f.stager()
	ctx := context.Background()
	spec := f.spec("lane-1")
	if err := stager.Stage(ctx, spec); err != nil {
		t.Fatalf("staging a lane failed: %v", err)
	}
	laneDir := filepath.Join(f.laneRoot, "lane-1")
	first := strings.TrimSpace(readAll(t, filepath.Join(laneDir, drain.SessionIDFile)))
	if !sessionIDPattern.MatchString(first) {
		t.Fatalf("the staged session id %q is not a version-4 UUID: every adapter hands it to a harness flag that keys a session by it", first)
	}
	// The run's own copy is the one a reader of <root>/.tillr/loop/<run-id>/ finds; it must be
	// the same id, or a resume that read one of the two would continue a different session.
	runCopy := strings.TrimSpace(readAll(t, filepath.Join(drain.RunDir(f.root, spec.RunID), drain.SessionIDFile)))
	if runCopy != first {
		t.Fatalf("the run directory's session id is %q, the lane's is %q: one run is one session", runCopy, first)
	}
	// The second run on the same lane is a different run, so it is a different session: the
	// transcript beside this lane belongs to the first one.
	second := f.spec("lane-1")
	second.RunID = spec.RunID + "-next"
	if err := stager.Stage(ctx, second); err != nil {
		t.Fatalf("staging a second run on the lane failed: %v", err)
	}
	next := strings.TrimSpace(readAll(t, filepath.Join(laneDir, drain.SessionIDFile)))
	if next == first {
		t.Fatalf("two unrelated runs were staged under the session %q: the second item's context would carry the first item's transcript", first)
	}
	// A caller that pins the id (a resume) keeps it: that is the only way a lane's id is
	// reused, and it is what makes a continuation a continuation.
	pinned := f.spec("lane-1")
	pinned.SessionID = "11111111-1111-4111-8111-111111111111"
	if err := stager.Stage(ctx, pinned); err != nil {
		t.Fatalf("staging with a pinned session id failed: %v", err)
	}
	if got := strings.TrimSpace(readAll(t, filepath.Join(laneDir, drain.SessionIDFile))); got != pinned.SessionID {
		t.Fatalf("staging with pinned session id %q wrote %q", pinned.SessionID, got)
	}
	// The negative control: a pinned value that is not a UUID is refused, not passed on.
	broken := f.spec("lane-1")
	broken.SessionID = "not-a-uuid"
	if err := stager.Stage(ctx, broken); err == nil {
		t.Fatalf("staging accepted a pinned session id that is not a UUID")
	}
}

// The brief's Timeout line carries the wall's unit in its value, because a run with no wall
// says `none`: "0 seconds" would read as a wall that fires immediately, and a unit the
// template appended would print "none seconds" (design D-RW1).
func TestTheBriefStatesTheWallOrNone(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", "tools/tillr-drain"))
	stager := f.stager()
	if err := stager.Stage(context.Background(), f.spec("lane-1")); err != nil {
		t.Fatalf("staging a lane failed: %v", err)
	}
	brief := readAll(t, filepath.Join(f.laneRoot, "lane-1", "brief.md"))
	if !strings.Contains(brief, "- **Timeout:** 60 seconds") {
		t.Fatalf("the brief does not state the wall with its unit:\n%s", brief)
	}

	// The same lane with no wall at all.
	unbounded := f.spec("lane-1")
	unbounded.TimeoutMS = 0
	if err := stager.Stage(context.Background(), unbounded); err != nil {
		t.Fatalf("staging an unbounded lane failed: %v", err)
	}
	brief = readAll(t, filepath.Join(f.laneRoot, "lane-1", "brief.md"))
	if !strings.Contains(brief, "- **Timeout:** none") {
		t.Fatalf("an unbounded brief does not say the run has no wall:\n%s", brief)
	}
	if strings.Contains(brief, "none seconds") {
		t.Fatalf("an unbounded brief prints a unit after the word none:\n%s", brief)
	}
}
