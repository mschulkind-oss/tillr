package main_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr-drain/patchmoves"
	"github.com/mschulkind-oss/tillr/event"
	"github.com/mschulkind-oss/tillr/store"
)

// forkLaneFixture is a trunk repository, a lane root, and a fork checkout under the
// repository's .forks/ that is a git repository of its own. That is the shape a
// patch-branch item meets: the run edits the fork checkout and exports its commit to the
// repository's patch branch, while the trunk the landing lane owns is somewhere else.
type forkLaneFixture struct {
	stageFixture
	fork     string
	forkBase string
}

// newForkLaneFixture stages the fixture's repository and adds the fork checkout. The fork
// is initialized separately rather than as a worktree of the trunk: a real fork checkout is
// its own repository under .forks/, so the boundary must not see its HEAD as a worktree
// moving.
func newForkStageFixture(t *testing.T) stageFixture {
	t.Helper()
	id := "item_041"
	f := newStageFixture(t, fencedItemRecord(id, "in-queue", "base.txt"))
	recordForkDelivery(t, f.store, id)
	return f
}

func recordForkDelivery(t *testing.T, path string, ids ...string) {
	t.Helper()
	ctx := context.Background()
	db, err := store.Open(ctx, path, true)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if err := db.WriteTx(ctx, func(tx *store.Tx) error {
		for _, id := range ids {
			if _, err := tx.Append(event.Event{Kind: "drain.delivery-decision", ObjectType: "item", ObjectID: &id, At: time.Now(), Payload: json.RawMessage(`{"kind":"fork","fork":"pi"}`)}); err != nil {
				return err
			}
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}
}

func newForkLaneFixture(t *testing.T) forkLaneFixture {
	t.Helper()
	f := newForkStageFixture(t)
	fork := filepath.Join(f.root, ".forks", "lanes", "lane-1", "pi")
	if err := os.MkdirAll(fork, 0o755); err != nil {
		t.Fatalf("creating the fork checkout failed: %v", err)
	}
	git(t, fork, "init", "-b", "main")
	writeFile(t, filepath.Join(fork, "fork.txt"), "the fork's base\n")
	git(t, fork, "add", "fork.txt")
	git(t, fork, "commit", "-m", "the fork's base")
	git(t, f.root, "fetch", "--no-tags", fork, "refs/heads/main:refs/heads/patch/pi")
	return forkLaneFixture{stageFixture: f, fork: fork, forkBase: git(t, fork, "rev-parse", "HEAD")}
}

func (f forkLaneFixture) worktree(t *testing.T) string {
	t.Helper()
	return filepath.Join(f.laneRoot, "lane-1", "worktree")
}

func (f forkLaneFixture) snapshot(t *testing.T, name string) string {
	t.Helper()
	out := filepath.Join(scratch(t, "fork-snapshots"), name+".json")
	code, output := invoke(t, "boundary", "snapshot",
		"--repo", f.root, "--target", f.worktree(t),
		"--patch-ref", "refs/heads/patch/pi", "--out", out)
	if code != 0 {
		t.Fatalf("boundary snapshot exited %d: %s", code, output)
	}
	return out
}

// A classified fork item stages its checkout under
// .forks/lanes/<lane>/<fork>: staging writes the checkout as the target, the patch branch
// as the delivery, and the fork's own HEAD as the base. The lane worktree is still cut from
// the trunk, because it is the worktree the boundary watches.
func TestAForkLaneStagesTheCheckoutItNames(t *testing.T) {
	f := newForkLaneFixture(t)
	if err := f.stager().Stage(context.Background(), f.spec("lane-1")); err != nil {
		t.Fatalf("staging a fork item failed: %v", err)
	}
	laneDir := filepath.Join(f.laneRoot, "lane-1")
	for name, want := range map[string]string{
		drain.TargetFile:   f.fork,
		drain.PatchRefFile: "refs/heads/patch/pi",
		drain.BaseSHAFile:  f.forkBase,
	} {
		body, err := os.ReadFile(filepath.Join(laneDir, name))
		if err != nil {
			t.Fatalf("staging did not write %s: %v", name, err)
		}
		if got := strings.TrimSpace(string(body)); got != want {
			t.Fatalf("%s is %q, want %q", name, got, want)
		}
	}
	brief, err := os.ReadFile(filepath.Join(laneDir, drain.BriefFile))
	if err != nil {
		t.Fatalf("reading the staged brief failed: %v", err)
	}
	for _, want := range []string{
		"- **Target:** " + f.fork,
		"(fork checkout)",
		".forks/lanes/lane-1/pi",
	} {
		if !strings.Contains(string(brief), want) {
			t.Fatalf("the brief does not name %q:\n%s", want, brief)
		}
	}
	// The lane worktree the boundary watches is still cut from the trunk, not from the
	// fork: a fork item's commit is the fork's, and the trunk is still the landing lane's.
	if head := git(t, f.worktree(t), "rev-parse", "HEAD"); head != f.base {
		t.Fatalf("the lane worktree is at %s, want the trunk %s", head, f.base)
	}
}

// A fork checkout that is not there is refused by the stage with the command that
// materializes it, not with a bare "no such directory": the delivery names a checkout
// the loop cannot reach, and the operator needs the next step.
func TestAForkLaneRefusesAMissingCheckoutByName(t *testing.T) {
	f := newForkStageFixture(t)
	err := f.stager().Stage(context.Background(), f.spec("lane-1"))
	if err == nil {
		t.Fatalf("staging a fork item with no checkout succeeded")
	}
	for _, want := range []string{".forks/lanes/lane-1/pi", "bin/pi-forks", "setup --lane lane-1 --fork pi"} {
		if !strings.Contains(err.Error(), want) {
			t.Fatalf("the refusal does not name %q: %v", want, err)
		}
	}
}

// A fork lane's assigned patch ref (refs/heads/patch/<fork>) is no longer excused during
// a run unless the new commit is named by a refs/tillr/landed/ marker written during the
// bracket. A worker-style fetch into refs/heads/patch/pi is observed (not convicted),
// recorded in patch_moves on boundary.check, and holds publication until acknowledged.
func TestAForkLaneBoundaryObservesItsPatchRefAndIgnoresMarkedLandings(t *testing.T) {
	f := newForkLaneFixture(t)
	if err := f.stager().Stage(context.Background(), f.spec("lane-1")); err != nil {
		t.Fatalf("staging a fork item failed: %v", err)
	}
	loadSnap := func(path string) *drain.Boundary {
		t.Helper()
		raw, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("reading snapshot %s: %v", path, err)
		}
		var b drain.Boundary
		if err := json.Unmarshal(raw, &b); err != nil {
			t.Fatalf("decoding snapshot %s: %v", path, err)
		}
		return &b
	}
	before := f.snapshot(t, "before")

	// The run: one commit in the fork checkout, then a worker-style fetch that moves the
	// repository's patch branch to it (what bin/pi-forks export --lane does).
	writeFile(t, filepath.Join(f.fork, "fork.txt"), "the fork's change\n")
	git(t, f.fork, "add", "fork.txt")
	git(t, f.fork, "commit", "-m", "the fork's change")
	forkHead := git(t, f.fork, "rev-parse", "HEAD")
	git(t, f.root, "fetch", "--no-tags", f.fork, "refs/heads/main:refs/heads/patch/pi")
	if got := git(t, f.root, "rev-parse", "refs/heads/patch/pi"); got != forkHead {
		t.Fatalf("the export left the patch ref at %s, want %s", got, forkHead)
	}
	after := f.snapshot(t, "after")

	// CompareRunObserved does not convict the run (owner's 2026-11-17 ruling), and observes
	// the move of the assigned patch ref.
	violations, observed, err := drain.CompareRunObserved(loadSnap(before), loadSnap(after))
	if err != nil {
		t.Fatalf("CompareRunObserved: %v", err)
	}
	if len(violations) != 0 {
		t.Fatalf("assigned patch ref move convicted the run: %+v", violations)
	}
	if len(observed) != 1 || observed[0].Ref != "refs/heads/patch/pi" || observed[0].Old != f.forkBase || observed[0].New != forkHead {
		t.Fatalf("observed = %+v, want one observation for refs/heads/patch/pi %s -> %s", observed, f.forkBase, forkHead)
	}

	// Standalone boundary check (Compare) reports the unexcused move as a violation.
	if code, output := invoke(t, "boundary", "check", "--before", before, "--after", after); code != 1 || !strings.Contains(string(output), "refs/heads/patch/pi") || !strings.Contains(string(output), forkHead) {
		t.Fatalf("boundary check exited %d, want 1 naming refs/heads/patch/pi and %s:\n%s", code, forkHead, output)
	}

	// A move to a commit named by a refs/tillr/landed/ marker written during the bracket
	// stays ignored in both CompareRunObserved and standalone boundary check.
	git(t, f.root, "update-ref", "refs/tillr/landed/run-sibling", forkHead)
	afterMarked := f.snapshot(t, "after-marked")
	violations, observed, err = drain.CompareRunObserved(loadSnap(before), loadSnap(afterMarked))
	if err != nil || len(violations) != 0 || len(observed) != 0 {
		t.Fatalf("marked landing was not ignored in CompareRunObserved: violations=%+v observed=%+v err=%v", violations, observed, err)
	}
	if code, output := invoke(t, "boundary", "check", "--before", before, "--after", afterMarked); code != 0 || len(output) != 0 {
		t.Fatalf("marked landing was not ignored in boundary check: exit %d\n%s", code, output)
	}

	// End-to-end loop run: a worker-style fetch into refs/heads/patch/pi during the run
	// shows up in patch_moves on boundary.check, prints as observed, holds publication,
	// and clears when tillr drain acknowledge-patch-move is invoked.
	lf := newForkLaneFixture(t)
	writeFile(t, filepath.Join(lf.root, ".git", "info", "exclude"), ".tillr/\n.forks/\n")
	adapter := stub(t, "fork-export-adapter", fmt.Sprintf(`cd %q
echo "the fork's change" > fork.txt
git add fork.txt
git -c user.name=stub -c user.email=stub@example.invalid commit -q -m "the fork's change"
git -C %q fetch --no-tags %q refs/heads/main:refs/heads/patch/pi
printf 'RESULT: done\n'
`, lf.fork, lf.root, lf.fork))
	now := time.Date(2026, 11, 19, 0, 0, 0, 0, time.UTC)
	steps := &drain.ProcessSteps{
		RunBin: sibling(t, "run"), ItemBin: sibling(t, "item"), Store: lf.store, LaneRoot: lf.laneRoot,
		Root: lf.root, Now: func() time.Time { return now },
		GateProgram: always(t, "gate", 0), LandProgram: always(t, "land", 0), VerdictProgram: always(t, "verdict", 0),
	}
	report := &strings.Builder{}
	loop, err := drain.NewLoop(drain.LoopConfig{
		ExitWhenDrained: true,
		Root:            lf.root,
		Store:           lf.store,
		LaneRoot:        lf.laneRoot,
		Lanes:           []string{"lane-1"},
		Policy: &drain.Policy{
			DefaultBackend: "fixture",
			Backends:       map[string]drain.Backend{"fixture": {Adapter: adapter, Model: "fixture-model", TimeoutSeconds: 60}},
		},
		Steps:  &forkTestSteps{&evidenceProcessSteps{steps}},
		Stage:  lf.stager(),
		Now:    func() time.Time { return now },
		Report: report,
	})
	if err != nil {
		t.Fatalf("building the loop failed: %v", err)
	}
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	if result.Stop != drain.StopQueueDrained {
		t.Fatalf("loop stopped %q (%s), want %q", result.Stop, result.Detail, drain.StopQueueDrained)
	}
	if !strings.Contains(report.String(), "refs/heads/patch/pi") || !strings.Contains(report.String(), "not attributed to the run") {
		t.Fatalf("the observation was not reported:\n%s", report.String())
	}
	loopForkHead := git(t, lf.fork, "rev-parse", "HEAD")
	var foundMove bool
	for _, ev := range eventsOf(t, lf.store) {
		if ev.Kind != drain.BoundaryCheckKind {
			continue
		}
		var payload struct {
			Clean      bool              `json:"clean"`
			PatchMoves []patchmoves.Move `json:"patch_moves"`
		}
		if err := json.Unmarshal(ev.Payload, &payload); err != nil {
			t.Fatalf("decoding boundary.check: %v", err)
		}
		if !payload.Clean {
			t.Fatalf("boundary.check was not clean: %s", ev.Payload)
		}
		for _, m := range payload.PatchMoves {
			if m.Ref == "refs/heads/patch/pi" && m.Old == lf.forkBase && m.New == loopForkHead {
				foundMove = true
			}
		}
	}
	if !foundMove {
		t.Fatalf("boundary.check did not record refs/heads/patch/pi (%s -> %s) in patch_moves", lf.forkBase, loopForkHead)
	}
	s, err := store.Open(context.Background(), lf.store, true)
	if err != nil {
		t.Fatal(err)
	}
	var holds []patchmoves.Hold
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		holds, err = patchmoves.Pending(r)
		return err
	}); err != nil {
		s.Close()
		t.Fatal(err)
	}
	s.Close()
	if len(holds) != 1 || holds[0].Fork() != "pi" || holds[0].New != loopForkHead {
		t.Fatalf("pending publication holds = %+v, want one hold for pi -> %s", holds, loopForkHead)
	}
	if code, output := invoke(t, "acknowledge-patch-move", "--store", lf.store, "--observation", fmt.Sprint(holds[0].Observation), "--fork", "pi", "--by", "owner"); code != 0 {
		t.Fatalf("acknowledge-patch-move exited %d: %s", code, output)
	}
	s, err = store.Open(context.Background(), lf.store, true)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.ReadTx(context.Background(), func(r *store.Reader) error {
		var err error
		holds, err = patchmoves.Pending(r)
		return err
	}); err != nil {
		t.Fatal(err)
	}
	if len(holds) != 0 {
		t.Fatalf("pending publication holds after acknowledge = %+v, want none", holds)
	}
}

type forkTestSteps struct{ *evidenceProcessSteps }

func (s *forkTestSteps) Gate(context.Context, drain.Candidate) (drain.GateOutcome, error) {
	return drain.GateOutcome{Outcome: drain.GatePassed}, nil
}

// The negative control: a bracket that does not name the patch ref reports the same move as
// a violation. Without it, the excuse above would pass whether or not the ref was actually
// checked.
func TestAForkLaneBoundaryRefusesAnUnexcusedPatchRef(t *testing.T) {
	f := newForkLaneFixture(t)
	if err := f.stager().Stage(context.Background(), f.spec("lane-1")); err != nil {
		t.Fatalf("staging a fork item failed: %v", err)
	}
	before := filepath.Join(scratch(t, "fork-snapshots"), "unexcused-before.json")
	if code, output := invoke(t, "boundary", "snapshot",
		"--repo", f.root, "--target", f.worktree(t), "--out", before); code != 0 {
		t.Fatalf("boundary snapshot exited %d: %s", code, output)
	}
	writeFile(t, filepath.Join(f.fork, "fork.txt"), "the fork's change\n")
	git(t, f.fork, "add", "fork.txt")
	git(t, f.fork, "commit", "-m", "the fork's change")
	forkHead := git(t, f.fork, "rev-parse", "HEAD")
	git(t, f.root, "fetch", "--no-tags", f.fork, "refs/heads/main:refs/heads/patch/pi")

	after := filepath.Join(scratch(t, "fork-snapshots"), "unexcused-after.json")
	if code, output := invoke(t, "boundary", "snapshot",
		"--repo", f.root, "--target", f.worktree(t), "--out", after); code != 0 {
		t.Fatalf("boundary snapshot exited %d: %s", code, output)
	}
	code, output := invoke(t, "boundary", "check", "--before", before, "--after", after)
	if code != 1 {
		t.Fatalf("an unexcused patch ref answered exit %d, want 1: %s", code, output)
	}
	if !strings.Contains(string(output), "refs/heads/patch/pi") || !strings.Contains(string(output), forkHead) {
		t.Fatalf("the violation does not name the patch ref and its tip:\n%s", output)
	}
}

// The fixture's stager writes a base sha the loop reads back; a fork item's base is the
// fork's HEAD, so the candidate's no-op check compares against the right commit.
func TestAForkLaneBaseIsTheForksHead(t *testing.T) {
	f := newForkLaneFixture(t)
	if err := f.stager().Stage(context.Background(), f.spec("lane-1")); err != nil {
		t.Fatalf("staging a fork item failed: %v", err)
	}
	body, err := os.ReadFile(filepath.Join(f.laneRoot, "lane-1", drain.BaseSHAFile))
	if err != nil {
		t.Fatalf("reading the staged base sha failed: %v", err)
	}
	if got := strings.TrimSpace(string(body)); got != f.forkBase {
		t.Fatalf("the staged base is %s, want the fork's HEAD %s", got, f.forkBase)
	}
}
