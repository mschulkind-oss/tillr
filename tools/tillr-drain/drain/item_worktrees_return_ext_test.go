package drain_test

import (
	"context"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
)

type recordingLaneStager struct {
	stager *drain.LaneStager
	specs  []drain.StageSpec
}

func (r *recordingLaneStager) Stage(ctx context.Context, spec drain.StageSpec) error {
	r.specs = append(r.specs, spec)
	return r.stager.Stage(ctx, spec)
}

type itemDirPassStager struct {
	t     *testing.T
	root  string
	pass  passStager
	specs []drain.StageSpec
}

func (s *itemDirPassStager) Stage(ctx context.Context, spec drain.StageSpec) error {
	s.specs = append(s.specs, spec)
	worktree := filepath.Join(s.pass.laneRoot, spec.LaneID, drain.WorktreeDir)
	if _, err := os.Stat(worktree); os.IsNotExist(err) {
		if err := os.MkdirAll(filepath.Dir(worktree), 0o755); err != nil {
			return err
		}
		base := spec.BaseSHA
		if spec.KeptHeadSHA != "" {
			base = spec.KeptHeadSHA
		}
		if base == "" {
			base = "main"
		}
		branch := drain.LaneBranch(s.pass.laneRoot, spec.LaneID)
		git(s.t, s.root, "worktree", "add", "--force", "-B", branch, worktree, base)
	}
	return s.pass.Stage(ctx, spec)
}

func TestItemWorktreesGateRefusalAndNewerTrunkTreeCheckReturnToItemDirectory(t *testing.T) {
	f := newLoopFixture(t, stub(t, "adapter", committingAdapter)).withSeats()
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	base := git(t, f.root, "rev-parse", "main")
	body := fmt.Sprintf("git -C %q commit --allow-empty -qm 'unrelated trunk move'\n", f.root) + failingTreeGateBody
	steps := &laterRedReplaySteps{
		trunkSteps: &trunkSteps{
			ProcessSteps: f.steps(t, stub(t, "gate", body), always(t, "land", 0), always(t, "verdict", 0)),
		},
		base: base,
	}
	stage := &itemDirPassStager{
		t:    t,
		root: f.root,
		pass: passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}},
	}
	loop, err := drain.NewLoop(drain.LoopConfig{
		ExitWhenDrained: true,
		Root:            f.root,
		Store:           f.store,
		LaneRoot:        f.laneRoot,
		Lanes:           []string{laneName(1)},
		ItemWorktrees:   true,
		Policy:          f.policy,
		Steps:           steps,
		Stage:           stage,
		Now:             steps.Now,
		Report:          io.Discard,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// Charged, returned to its worker in the item's own directory, and charged identically again.
	if result.Rejected != 2 || result.TrunkAcquitted != 0 || firstRefusalStep(t, f.store, "run-item_041-a1") != drain.RefusalGate {
		t.Fatalf("own failure acquitted against later trunk: %+v", result)
	}
	if len(stage.specs) != 2 {
		t.Fatalf("staged %d runs, want 2: %+v", len(stage.specs), stage.specs)
	}
	for i, spec := range stage.specs {
		if spec.LaneID != "item_041" {
			t.Fatalf("stage[%d].LaneID = %q, want item_041", i, spec.LaneID)
		}
	}
}

func TestItemWorktreesConflictFixDispatchedToItemDirectory(t *testing.T) {
	adapter := counting + `case "$TILLR_RUN_ID" in
 *-cfix)
  orig="${TILLR_RUN_ID%-cfix}"
  git show "refs/tillr/runs/$orig:docs/work.md" > docs/work.md
  echo repaired > base.txt
  git add base.txt docs/work.md
  git -c user.name=stub -c user.email=stub@example.invalid commit -qm repaired
  printf 'RESULT: done\n'
  exit 0 ;;
esac
` + strings.TrimPrefix(conflictAdapter, counting)
	f := newLoopFixture(t, stub(t, "adapter", adapter))
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
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
! git show HEAD:docs/work.md | grep -q 'id="item-repair"'
`)
	steps := f.steps(t, gate, land, always(t, "verdict", 0))
	stage := &recordingLaneStager{
		stager: &drain.LaneStager{
			Root:     f.root,
			LaneRoot: f.laneRoot,
			Store:    f.store,
			Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
			Now:      steps.Now,
		},
	}
	var report strings.Builder
	loop, err := drain.NewLoop(drain.LoopConfig{
		ExitWhenDrained: true,
		Root:            f.root,
		Store:           f.store,
		LaneRoot:        f.laneRoot,
		Lanes:           []string{laneName(1)},
		ItemWorktrees:   true,
		Policy:          f.policy,
		Steps:           &evidenceProcessSteps{steps},
		Stage:           stage,
		Now:             steps.Now,
		Report:          &report,
		QuestionsCmd:    strings.Fields(indexStub(t, map[string]any{"path": "docs/work.md", "questions": []any{}})),
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := loop.Run(context.Background())
	if err != nil {
		t.Fatalf("the loop failed: %v\nreport:\n%s", err, report.String())
	}
	if result.Landed != 1 || result.ConflictFixes != 1 || result.Rejected != 0 {
		t.Fatalf("conflict fix under ItemWorktrees failed: %+v\nreport:\n%s", result, report.String())
	}
	if len(stage.specs) != 2 || stage.specs[1].Conflict == nil {
		t.Fatalf("repair specs: %+v", stage.specs)
	}
	for i, spec := range stage.specs {
		if spec.LaneID != "item-repair" {
			t.Fatalf("stage[%d].LaneID = %q, want item-repair", i, spec.LaneID)
		}
	}
	// Confirm no decision held item-repair for no-lane.
	for _, e := range eventsOf(t, f.store) {
		if e.Kind == drain.DecisionKind {
			payload := eventPayload(t, e)
			if payload["item_id"] == "item-repair" && payload["reason"] == drain.ReasonNoLane {
				t.Fatalf("item-repair was held for no-lane: %+v", payload)
			}
		}
	}
	brief := stage.specs[1].Conflict
	worktree := filepath.Join(f.laneRoot, "item-repair", "worktree")
	pinned := git(t, worktree, "rev-parse", "refs/tillr/runs/"+brief.OriginalRunID)
	if brief.PreparedHead != pinned || brief.HeadSHA == pinned || git(t, worktree, "rev-parse", pinned+"^1") != brief.HeadSHA {
		t.Fatalf("the brief does not separate the worker head %s from the pinned retirement %s (pin %s)", brief.HeadSHA, brief.PreparedHead, pinned)
	}
}
