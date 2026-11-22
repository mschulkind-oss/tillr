package drain_test

import (
	"context"
	"github.com/mschulkind-oss/tillr-drain/drain"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLandingRetiresItsDocumentBlockBeforeGateOutsideFence(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, itemRecord("item-target", "in-queue"), itemRecord("item-other", "not-started"))
	before := "# Fixture\n\nKeep this prose.\n<!-- tillr: item\n id=\"item-target\" paths=\"candidate.txt\"\n-->\n\n- **c1:** Make the candidate.\n\n## Empty heading\n"
	want := "# Fixture\n\nKeep this prose.\n\n\n## Empty heading\n"
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "docs", "fixture.md"), before)
	dependency := "# Other\n<!-- tillr: item id=\"item-other\" paths=\"other.txt\"\n blocked_by=\"item-target\" -->\n- **c1:** Keep this criterion.\n"
	writeFile(t, filepath.Join(f.root, "docs", "other.md"), dependency)
	// The other item must remain parked when sync reads its unchanged block.
	writeFile(t, filepath.Join(f.root, "docs", "roadmap.md"), "## Icebox\n- [Other](other.md)\n")
	git(t, f.root, "add", "docs/fixture.md", "docs/other.md", "docs/roadmap.md")
	git(t, f.root, "commit", "-m", "fixture document")
	t.Setenv("RETIRE_WANT", want)
	gate := stub(t, "gate", counting+`set -eu
[ "$(git show HEAD:docs/fixture.md)" = "$(printf '%s' "$RETIRE_WANT")" ]
[ "$(git log -1 --format=%s)" = "chore: retire landed document block" ]
[ "$(git diff --name-only HEAD~1 HEAD)" = "$(printf 'docs/fixture.md\ndocs/other.md')" ]
git show HEAD:docs/other.md | rg -q 'blocked_by=""'
[ "$(git diff --name-only HEAD~2 HEAD~1)" = "candidate.txt" ]
`)
	land := stub(t, "land", counting+`set -eu
while [ "$#" -gt 0 ]; do
 case "$1" in --head) head=$2;; --root) root=$2;; esac
 shift 2
done
git -C "$root" merge --ff-only "$head" >/dev/null
`)
	report := &strings.Builder{}
	loop := retirementLoop(t, f, f.steps(t, gate, land, always(t, "verdict", 0)), []string{"candidate.txt"}, report)
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if result.Landed != 1 || result.Rejected != 0 {
		t.Fatalf("result %+v; %s", result, report)
	}
	if got := git(t, f.root, "show", "HEAD:docs/fixture.md"); got != strings.TrimSpace(want) {
		t.Fatalf("document = %q", got)
	}
	if got := git(t, f.root, "show", "HEAD:docs/other.md"); got != strings.TrimSpace(strings.Replace(dependency, "blocked_by=\"item-target\"", "blocked_by=\"\"", 1)) {
		t.Fatalf("dependency document = %q", got)
	}
	cmd := exec.Command(sibling(t, "item"), "sync", "--dry-run", "--store", f.store, "--root", f.root)
	out, err := cmd.CombinedOutput()
	if err != nil || !strings.Contains(string(out), "stale_doc_blocks: 0") {
		t.Fatalf("sync: %v\n%s", err, out)
	}
}

func TestMissingBlockDoesNotMakeAnEmptyRetirementCommit(t *testing.T) {
	for _, removed := range []bool{false, true} {
		t.Run(map[bool]string{false: "absent", true: "worker removed"}[removed], func(t *testing.T) {
			body := committingAdapter
			fence := []string{"candidate.txt"}
			if removed {
				body = counting + committingAdapterBody + `printf '# Fixture\n' > docs/fixture.md
git add docs/fixture.md
git -c user.name=stub -c user.email=stub@example.invalid commit -q --amend --no-edit
printf 'RESULT: done\n'
`
				fence = append(fence, "docs/fixture.md")
			}
			f := newLoopFixture(t, stub(t, "adapter", body))
			fixtureAt(t, f.store, itemRecord("item-target", "in-queue"))
			if removed {
				if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
					t.Fatal(err)
				}
				writeFile(t, filepath.Join(f.root, "docs", "fixture.md"), "# Fixture\n<!-- tillr: item id=\"item-target\" paths=\"candidate.txt,docs/fixture.md\" -->\n- **c1:** Make candidate.\n")
				git(t, f.root, "add", "docs/fixture.md")
				git(t, f.root, "commit", "-m", "fixture")
			}
			gate := stub(t, "gate", counting+`[ "$(git log -1 --format=%s)" = candidate ]`)
			report := &strings.Builder{}
			result, err := retirementLoop(t, f, f.steps(t, gate, always(t, "land", 0), always(t, "verdict", 0)), fence, report).Run(context.Background())
			if removed {
				// Refused, returned to its worker with the evidence, and refused the same way
				// again: an identical repeat (the owner's ruling of 2026-11-17).
				if err != nil || result.Landed != 0 || result.Rejected != 2 || countMatching(lines(t, f.count), "gate") != 0 {
					t.Fatalf("worker removal must be refused before the gate: result %+v, err %v; %s", result, err, report)
				}
				if !strings.Contains(report.String(), "worker edited its own item block docs/fixture.md#fixture") {
					t.Fatalf("refusal did not name the block: %s", report)
				}
				return
			}
			if err != nil || result.Landed != 1 {
				t.Fatalf("result %+v, err %v; %s", result, err, report)
			}
			if !strings.Contains(report.String(), "block not found") {
				t.Fatalf("no missing-block report: %s", report)
			}
		})
	}
}

func retirementLoop(t *testing.T, f loopFixture, steps *drain.ProcessSteps, fence []string, report *strings.Builder) *drain.Loop {
	t.Helper()
	return retirementLoopWithSteps(t, f, steps, steps.Now, fence, report)
}

func retirementLoopWithSteps(t *testing.T, f loopFixture, steps drain.Steps, now func() time.Time, fence []string, report *strings.Builder) *drain.Loop {
	t.Helper()
	index := indexStub(t, map[string]any{"path": "docs/fixture.md", "questions": []any{}})
	if process, ok := steps.(*drain.ProcessSteps); ok {
		steps = &evidenceProcessSteps{process}
	}
	loop, err := drain.NewLoop(drain.LoopConfig{ExitWhenDrained: true,
		Root: f.root, Store: f.store, LaneRoot: f.laneRoot, Lanes: []string{"lane-1"},
		Policy: f.policy, Steps: steps, Stage: passStager{laneRoot: f.laneRoot, fence: fence},
		Now: now, Report: report, QuestionsCmd: strings.Fields(index),
	})
	if err != nil {
		t.Fatal(err)
	}
	return loop
}

// A failed gate step leaves the retirement candidate held for operator repair; unlike
// a gate that answered no verdict, it cannot be re-gated until the fault is cleared.
type retirementGateSteps struct {
	*drain.ProcessSteps
	fail  bool
	heads []string
}

func (s *retirementGateSteps) Land(ctx context.Context, c drain.Candidate) (drain.LandOutcome, error) {
	return (&evidenceProcessSteps{s.ProcessSteps}).Land(ctx, c)
}
func (s *retirementGateSteps) Verdict(ctx context.Context, c drain.Candidate, verdict string) error {
	return (&evidenceProcessSteps{s.ProcessSteps}).Verdict(ctx, c, verdict)
}
func (s *retirementGateSteps) Gate(ctx context.Context, c drain.Candidate) (drain.GateOutcome, error) {
	s.heads = append(s.heads, c.HeadSHA)
	if s.fail {
		return drain.GateOutcome{}, drain.Externalf("the gate step could not start")
	}
	return s.ProcessSteps.Gate(ctx, c)
}

func TestRestartGatesAndLandsTheRetiredHead(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	fixtureAt(t, f.store, envelope("item.record", "item-item-target", map[string]any{
		"item_id": "item-target", "title": "the item item-target", "state": "in-queue",
		"allowed_paths": []string{"candidate.txt"}, "criteria": []any{},
		"body_ref": "docs/fixture.md#fixture",
	}))
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "docs", "fixture.md"), "# Fixture\n<!-- tillr: item id=\"item-target\" paths=\"candidate.txt\" -->\n- **c1:** Make candidate.\n")
	git(t, f.root, "add", "docs/fixture.md")
	git(t, f.root, "commit", "-m", "fixture")
	gate := stub(t, "gate", counting+`set -eu
[ "$(git show HEAD:docs/fixture.md)" = '# Fixture' ]
`)
	land := stub(t, "land", counting+`set -eu
while [ "$#" -gt 0 ]; do
 case "$1" in --head) head=$2;; --root) root=$2;; esac
 shift 2
done
[ "$(git -C "$root" show "$head:docs/fixture.md")" = '# Fixture' ]
git -C "$root" merge --ff-only "$head" >/dev/null
`)
	processSteps := f.steps(t, gate, land, always(t, "verdict", 0))
	firstSteps := &retirementGateSteps{ProcessSteps: processSteps, fail: true}
	report := &strings.Builder{}
	first, err := retirementLoopWithSteps(t, f, firstSteps, processSteps.Now, []string{"candidate.txt"}, report).Run(context.Background())
	if err != nil || first.Stop != drain.StopQueueDrained || first.Landed != 0 || len(firstSteps.heads) != 1 {
		t.Fatalf("first=%+v, heads %v, err %v; %s", first, firstSteps.heads, err, report)
	}
	clearFault(t, f.store, "item-target")
	secondSteps := &retirementGateSteps{ProcessSteps: f.steps(t, gate, land, always(t, "verdict", 0))}
	second, err := retirementLoopWithSteps(t, f, secondSteps, secondSteps.Now, []string{"candidate.txt"}, report).Run(context.Background())
	if err != nil || second.Landed != 1 {
		t.Fatalf("second=%+v, err %v; %s", second, err, report)
	}
	if len(secondSteps.heads) != 1 || firstSteps.heads[0] == "" || firstSteps.heads[0] != secondSteps.heads[0] {
		t.Fatalf("restart did not gate the same retired head: %v then %v", firstSteps.heads, secondSteps.heads)
	}
	if got := git(t, f.root, "show", "HEAD:docs/fixture.md"); got != "# Fixture" {
		t.Fatal(got)
	}
	if countMatching(lines(t, f.count), "adapter") != 1 {
		t.Fatal("restart redispatched worker")
	}
}

func TestPatchBranchRetirementDoesNotChangeTheExportedForkHead(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter))
	// Staged as staging stages a fork item: the checkout under .forks/lanes/<lane>/<fork>,
	// named by the lane's target and its fence, so the candidate is in its lane's target.
	fork := filepath.Join(f.root, ".forks", "lanes", "lane-1", "fixture")
	if err := os.MkdirAll(fork, 0o755); err != nil {
		t.Fatal(err)
	}
	git(t, fork, "init", "-b", "main")
	if err := os.MkdirAll(filepath.Join(fork, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	document := "# Fixture\n<!-- tillr: item id=\"item-target\" paths=\"candidate.txt\" -->\n- **c1:** Make candidate.\n"
	writeFile(t, filepath.Join(fork, "docs", "fixture.md"), document)
	git(t, fork, "add", ".")
	git(t, fork, "commit", "-m", "base")
	base := git(t, fork, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(fork, "candidate.txt"), "change\n")
	git(t, fork, "add", "candidate.txt")
	git(t, fork, "commit", "-m", "exported change")
	head := git(t, fork, "rev-parse", "HEAD")
	if err := writeFence(f.laneRoot, "lane-1", []string{".forks/lanes/lane-1/fixture/"}); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.laneRoot, "lane-1", drain.TargetFile), fork+"\n")
	writeFile(t, filepath.Join(f.laneRoot, "lane-1", drain.PatchRefFile), "refs/heads/patch/fixture\n")
	fixtureAt(t, f.store, itemRecord("item-target", "in-queue"))
	helper, err := os.ReadFile("../fork-gate.py")
	if err != nil {
		t.Fatal(err)
	}
	for _, dir := range []string{"tools/tillr-drain", "yolo-packs/fixture"} {
		if err := os.MkdirAll(filepath.Join(f.root, dir), 0755); err != nil {
			t.Fatal(err)
		}
	}
	writeFile(t, filepath.Join(f.root, "tools/tillr-drain/fork-gate.py"), string(helper))
	writeFile(t, filepath.Join(f.root, "yolo-packs/fixture/pack.json"), `{"contributes":[{"patch_branch":"git+https://example.invalid/repo?ref=patch/fixture","build":"test -f candidate.txt"}]}`)
	t.Setenv("PATH", filepath.Dir(sibling(t, "gate"))+string(os.PathListSeparator)+os.Getenv("PATH"))
	loop := f.newLoop(t, 1, f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0)))
	candidate := drain.Candidate{ItemID: "item-target", LaneID: "lane-1", RunID: "run-fork", Worktree: fork, BaseRef: "refs/heads/patch/fixture", BaseSHA: base, HeadSHA: head}
	_, landed, err := loop.Verify(context.Background(), candidate, &drain.LoopResult{}, nil)
	if err != nil || !landed {
		t.Fatalf("landed %v, err %v", landed, err)
	}
	if got := git(t, fork, "rev-parse", "HEAD"); got != head {
		t.Fatalf("fork head moved: %s -> %s", head, got)
	}
	if got := git(t, fork, "show", "HEAD:docs/fixture.md"); got != strings.TrimSpace(document) {
		t.Fatal("fork document changed")
	}
}
