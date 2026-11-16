package main_test

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
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
func newForkLaneFixture(t *testing.T) forkLaneFixture {
	t.Helper()
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", ".forks/lanes/lane-1/pi"))
	fork := filepath.Join(f.root, ".forks", "lanes", "lane-1", "pi")
	if err := os.MkdirAll(fork, 0o755); err != nil {
		t.Fatalf("creating the fork checkout failed: %v", err)
	}
	git(t, fork, "init", "-b", "main")
	writeFile(t, filepath.Join(fork, "fork.txt"), "the fork's base\n")
	git(t, fork, "add", "fork.txt")
	git(t, fork, "commit", "-m", "the fork's base")
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

// A fork-series item is stageable when its fence names the checkout under
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
// materializes it, not with a bare "no such directory": the lane's fence named a checkout
// the loop cannot reach, and the operator needs the next step.
func TestAForkLaneRefusesAMissingCheckoutByName(t *testing.T) {
	f := newStageFixture(t, fencedItemRecord("item_041", "in-queue", ".forks/lanes/lane-1/pi"))
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

// The movement the review names: a run exports its fork commit to refs/heads/patch/<fork>,
// and the bracket excuses that ref for the lane whose fence names the fork checkout. Once
// the boundary excuses it, land pass moves the trunk and leaves the patch ref where the
// run put it, so the patch ref moved exactly once and the trunk only through the landing.
func TestAForkLaneBoundaryExcusesItsPatchRefAndLandMovesOnlyTheTrunk(t *testing.T) {
	f := newForkLaneFixture(t)
	if err := f.stager().Stage(context.Background(), f.spec("lane-1")); err != nil {
		t.Fatalf("staging a fork item failed: %v", err)
	}
	before := f.snapshot(t, "before")

	// The run: one commit in the fork checkout, then the export that moves the repository's
	// patch branch to it. The export fetches the commit into the trunk repository, which is
	// exactly what bin/pi-forks export --lane does.
	writeFile(t, filepath.Join(f.fork, "fork.txt"), "the fork's change\n")
	git(t, f.fork, "add", "fork.txt")
	git(t, f.fork, "commit", "-m", "the fork's change")
	forkHead := git(t, f.fork, "rev-parse", "HEAD")
	forkTree := git(t, f.fork, "rev-parse", "HEAD^{tree}")
	git(t, f.root, "fetch", "--no-tags", f.fork, "refs/heads/main:refs/heads/patch/pi")
	if got := git(t, f.root, "rev-parse", "refs/heads/patch/pi"); got != forkHead {
		t.Fatalf("the export left the patch ref at %s, want %s", got, forkHead)
	}
	after := f.snapshot(t, "after")
	if code, output := invoke(t, "boundary", "check", "--before", before, "--after", after); code != 0 {
		t.Fatalf("the patch ref's move was reported as a boundary violation: exit %d\n%s", code, output)
	}

	// Land: the run already delivered the patch branch, so the landing refuses to move it a
	// second time and the trunk has not moved.
	land := sibling(t, "land")
	store := filepath.Join(scratch(t, "fork-store"), "board.sqlite3")
	globalConfig := filepath.Join(scratch(t, "fork-identity"), "gitconfig")
	writeFile(t, globalConfig, "[user]\n\tname = Fixture Landing\n\temail = landing@example.invalid\n")
	t.Setenv("GIT_CONFIG_GLOBAL", globalConfig)

	verdict := filepath.Join(scratch(t, "fork-records"), "patch-verdict.json")
	writeFile(t, verdict, envelope("gate.verdict", "verdict_patch", map[string]any{
		"verdict_id": "verdict_patch", "plan_id": "plan_patch",
		"registry_digest": "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"base_sha":        f.forkBase, "tree_sha": forkTree, "result": "pass",
		"checks": []map[string]any{},
	}))
	code, output := driveRecord(t, land, "pass", "--store", store, "--repo", f.root,
		"--item", "item_041", "--run", "run-patch", "--base", f.forkBase, "--head", forkHead,
		"--base-ref", "refs/heads/patch/pi", "--verdict", verdict, "--now", "2026-11-10T00:00:00Z")
	if code != 1 {
		t.Fatalf("land pass on an already-delivered patch branch exited %d, want 1 (refused): %s", code, output)
	}
	var attempt struct {
		Payload struct {
			Result string `json:"result"`
			Reason string `json:"reason"`
		} `json:"payload"`
	}
	if err := json.Unmarshal(output, &attempt); err != nil {
		t.Fatalf("land pass emitted no record: %v\n%s", err, output)
	}
	if attempt.Payload.Result != "refused" || attempt.Payload.Reason != "patch-at-head" {
		t.Fatalf("the attempt is %q/%q, want refused/patch-at-head", attempt.Payload.Result, attempt.Payload.Reason)
	}
	if got := git(t, f.root, "rev-parse", "refs/heads/patch/pi"); got != forkHead {
		t.Fatalf("the refused landing moved the patch ref to %s, want it left at %s", got, forkHead)
	}
	if got := git(t, f.root, "rev-parse", "main"); got != f.base {
		t.Fatalf("the patch-branch landing moved the trunk to %s, want it left at %s", got, f.base)
	}

	// The trunk moves only through land pass: a second candidate, on the trunk this time,
	// is the one that advances main.
	git(t, f.root, "checkout", "-b", "trunk-candidate", f.base)
	writeFile(t, filepath.Join(f.root, "trunk.txt"), "the trunk's change\n")
	git(t, f.root, "add", "trunk.txt")
	git(t, f.root, "commit", "-m", "the trunk's change")
	trunkHead := git(t, f.root, "rev-parse", "HEAD")
	trunkTree := git(t, f.root, "rev-parse", "HEAD^{tree}")
	git(t, f.root, "checkout", "main")

	trunkVerdict := filepath.Join(scratch(t, "fork-records"), "trunk-verdict.json")
	writeFile(t, trunkVerdict, envelope("gate.verdict", "verdict_trunk", map[string]any{
		"verdict_id": "verdict_trunk", "plan_id": "plan_trunk",
		"registry_digest": "sha256:0000000000000000000000000000000000000000000000000000000000000000",
		"base_sha":        f.base, "tree_sha": trunkTree, "result": "pass",
		"checks": []map[string]any{},
	}))
	driveOK(t, land, "pass", "--store", store, "--repo", f.root,
		"--item", "item_trunk", "--run", "run-trunk", "--base", f.base, "--head", trunkHead,
		"--base-ref", "main", "--verdict", trunkVerdict, "--now", "2026-11-10T00:00:00Z")
	if got := git(t, f.root, "rev-parse", "main^{tree}"); got != trunkTree {
		t.Fatalf("the trunk carries tree %s, want the landed tree %s", got, trunkTree)
	}
	if got := git(t, f.root, "rev-parse", "refs/heads/patch/pi"); got != forkHead {
		t.Fatalf("the trunk landing moved the patch ref to %s, want it left at %s", got, forkHead)
	}
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
