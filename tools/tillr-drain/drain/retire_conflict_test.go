package drain_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

// replayRetirementStager exposes the requested head to the fixture worker, which
// faithfully reapplies its document blob rather than guessing what to preserve.
type replayRetirementStager struct{ recordingStager }

func (s *replayRetirementStager) Stage(ctx context.Context, spec drain.StageSpec) error {
	if err := s.recordingStager.Stage(ctx, spec); err != nil {
		return err
	}
	if spec.Conflict != nil {
		return os.WriteFile(filepath.Join(s.laneRoot, spec.LaneID, "repair-head"), []byte(spec.Conflict.HeadSHA+"\n"), 0600)
	}
	return nil
}

func TestConflictRepairReappliesWorkerHeadThenRegeneratesRetirement(t *testing.T) {
	adapter := counting + `case "$TILLR_RUN_ID" in
 *-cfix)
  read head < ../repair-head
  git show "$head:docs/work.md" > docs/work.md
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
	writeFile(t, filepath.Join(f.root, "docs", "work.md"), "# Work\n<!-- tillr: item id=\"item-repair\" paths=\"base.txt,docs/work.md\" -->\n- **c1:** Make the candidate.\n")
	git(t, f.root, "add", "docs/work.md")
	git(t, f.root, "commit", "-m", "fixture document")
	fixtureAt(t, f.store, envelope("item.record", "item-item-repair", map[string]any{
		"item_id": "item-repair", "title": "the item item-repair", "state": "in-queue",
		"allowed_paths": []string{"base.txt", "docs/work.md"}, "criteria": []any{},
		"body_ref": "docs/work.md#work",
	}))
	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	land := conflictLandScript(t, f.root, conflict, landingEnvelope(t, "landed", nil), 0)
	gate := stub(t, "gate", counting+`set -eu
[ "$(git log -1 --format=%s)" = 'chore: retire landed document block' ]
! git show HEAD:docs/work.md | grep -q 'id="item-repair"'
`)
	stage := &replayRetirementStager{recordingStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"base.txt", "docs/work.md"}}}}
	result, err := routeLoop(t, f, f.steps(t, gate, land, always(t, "verdict", 0)), stage, os.Stderr).Run(context.Background())
	if err != nil || result.Landed != 1 || result.ConflictFixes != 1 || result.Rejected != 0 {
		t.Fatalf("result %+v / %v", result, err)
	}
	if len(stage.specs) != 2 || stage.specs[1].Conflict == nil {
		t.Fatalf("repair specs: %+v", stage.specs)
	}
	worktree := filepath.Join(f.laneRoot, "lane-1", "worktree")
	workerHead := stage.specs[1].Conflict.HeadSHA
	if body := git(t, worktree, "show", workerHead+":docs/work.md"); !strings.Contains(body, "id=\"item-repair\"") {
		t.Fatalf("repair input includes loop-owned retirement: %s", body)
	}
	prepared := git(t, worktree, "show", stage.specs[1].Conflict.PreparedHead+":docs/work.md")
	if strings.Contains(prepared, "id=\"item-repair\"") {
		t.Fatal("prepared candidate evidence was not retained")
	}
	brief := stage.specs[1].Conflict
	if got := git(t, worktree, "rev-parse", "refs/tillr/runs/"+brief.OriginalRunID); got != brief.PreparedHead {
		t.Fatalf("original attempt pin = %s, want %s", got, brief.PreparedHead)
	}
}
