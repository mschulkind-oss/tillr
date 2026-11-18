package main_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

// retiredConflictFixture is a gated candidate the loop retired — its prepared head is the
// worker's commit plus the loop's own "chore: retire landed document block" — whose squash
// conflicts on a trunk move, so the loop stages its one conflict-fix run. repair is the
// fix worker's shell, run in the lane worktree with the original run id in $orig.
func retiredConflictFixture(t *testing.T, repair string) (drain.LoopResult, *recordingStager, loopFixture) {
	t.Helper()
	adapter := counting + `case "$TILLR_RUN_ID" in
 *-cfix)
  orig="${TILLR_RUN_ID%-cfix}"
` + repair + `
  echo repaired > base.txt
  git add base.txt docs/work.md
  git -c user.name=stub -c user.email=stub@example.invalid commit -qm repaired
  printf 'RESULT: done\n'
  exit 0 ;;
esac
` + strings.TrimPrefix(conflictAdapter, counting)
	f := newLoopFixture(t, stub(t, "adapter", adapter))
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "docs", "work.md"), "# Work\n\n## The item\n\n<!-- tillr: item id=\"item-repair\" paths=\"base.txt,docs/work.md\" -->\n\n- **c1:** Make the candidate.\n\n## Next\n\nKeep this.\n")
	git(t, f.root, "add", "docs/work.md")
	git(t, f.root, "commit", "-m", "fixture document")
	fixtureAt(t, f.store, envelope("item.record", "item-item-repair", map[string]any{
		"item_id": "item-repair", "title": "the item item-repair", "state": "in-queue",
		"allowed_paths": []string{"base.txt", "docs/work.md"}, "criteria": []any{},
		"body_ref": "docs/work.md#the-item",
	}))
	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	land := conflictLandScript(t, f.root, conflict, landingEnvelope(t, "landed", nil), 0)
	gate := stub(t, "gate", counting+`set -eu
! git show HEAD:docs/work.md | rg -q 'id="item-repair"'
`)
	stage := &recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"base.txt", "docs/work.md"}}}
	result, err := routeLoop(t, f, f.steps(t, gate, land, always(t, "verdict", 0)), stage, os.Stderr).Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v", err)
	}
	return result, stage, f
}

// On 2026-11-17 a conflict-fix run of a retired candidate re-applied the head pinned at
// refs/tillr/runs/<run>, which carries the loop's own retirement commit, and the fence
// refused the fix as the worker editing its own item block. The block removal the loop
// recorded for this item is not the worker's edit: the fix lands.
func TestAConflictFixCarryingTheLoopsRetirementIsNotTheWorkersEdit(t *testing.T) {
	result, stage, f := retiredConflictFixture(t, `  git show "refs/tillr/runs/$orig:docs/work.md" > docs/work.md`)
	if result.Landed != 1 || result.ConflictFixes != 1 || result.Rejected != 0 {
		t.Fatalf("the fix that carried the loop's retirement was refused: %+v", result)
	}
	if len(stage.specs) != 2 || stage.specs[1].Conflict == nil {
		t.Fatalf("repair specs: %+v", stage.specs)
	}
	// The brief names the worker's own head to re-apply and the pinned prepared head apart,
	// so the worker is told which commit is the loop's.
	brief := stage.specs[1].Conflict
	worktree := filepath.Join(f.laneRoot, "lane-1", "worktree")
	pinned := git(t, worktree, "rev-parse", "refs/tillr/runs/"+stage.specs[0].RunID)
	if brief.PreparedHead != pinned || brief.HeadSHA == pinned || git(t, worktree, "rev-parse", pinned+"^1") != brief.HeadSHA {
		t.Fatalf("the brief does not separate the worker head %s from the pinned retirement %s (pin %s)", brief.HeadSHA, brief.PreparedHead, pinned)
	}
}

// A conflict-fix worker that really edits its own item block is still refused, retired
// candidate or not: the excuse is the loop's recorded removal, not any edit to the block.
func TestAConflictFixThatEditsItsOwnBlockIsStillRefused(t *testing.T) {
	result, _, _ := retiredConflictFixture(t, `  sed -i 's/Make the candidate./Make anything at all./' docs/work.md`)
	// The refused fix goes back to the item's worker with the evidence (the owner's ruling of
	// 2026-11-17), so the item may land afterwards; the fix itself must have been refused at
	// the fence.
	if result.Rejected != 1 || result.RefusedSteps[drain.RefusalFence] != 1 {
		t.Fatalf("a fix that rewrote its own criterion was not refused: %+v", result)
	}
}
