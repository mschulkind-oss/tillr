package main_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

type briefCapturingStager struct {
	t      *testing.T
	inner  drain.Stager
	root   string
	specs  []drain.StageSpec
	briefs []string
	limit  int
	stop   context.CancelFunc
}

func (s *briefCapturingStager) Stage(ctx context.Context, spec drain.StageSpec) error {
	s.specs = append(s.specs, spec)
	if s.stop != nil && len(s.specs) > s.limit {
		s.stop()
	}
	if err := s.inner.Stage(ctx, spec); err != nil {
		if s.t != nil {
			s.t.Logf("stage %s error: %v", spec.RunID, err)
		}
		return err
	}
	raw, err := os.ReadFile(filepath.Join(s.root, spec.LaneID, drain.BriefFile))
	if err == nil {
		s.briefs = append(s.briefs, string(raw))
	} else {
		s.briefs = append(s.briefs, "")
	}
	return nil
}

// TestWorkflowSecondIdenticalConflictRestartsFromFirstStageAndThirdParksForOwner tests
// D-JL12 end-to-end through Loop.Run with a two-stage workflow (plan -> build) and real
// LaneStager brief rendering:
//   - stage 2 (build) hits squash-conflict on moved trunk -> -cfix -> squash-conflict ->
//     returned -> squash-conflict (second identical conflict);
//   - the loop discards the candidate, resets the lane to trunk, and the planner dispatches
//     a fresh attempt starting at stage 1 (plan), whose rendered brief carries body_ref,
//     states the earlier attempt conflicted twice, names base.txt, and omits conflict-fix
//     instructions and markers;
//   - when the fresh attempt conflicts again at landing, the loop parks the item for the
//     owner (conflict-owner) with no further dispatch, even though body_ref and a design
//     seat are present.
func TestWorkflowSecondIdenticalConflictRestartsFromFirstStageAndThirdParksForOwner(t *testing.T) {
	adapter := stub(t, "adapter", counting+`case "$TILLR_RUN_ID" in
 *-n1)
  printf 'RESULT: done\n'
  exit 0 ;;
 *)
  echo candidate > base.txt
  git add base.txt
  git -c user.name=stub -c user.email=stub@example.invalid commit -qm candidate
  printf 'RESULT: done\n' ;;
esac
`)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, envelope("item.record", "item-item_041", map[string]any{
		"item_id": "item_041", "title": "the item item_041", "state": "in-queue",
		"allowed_paths": []string{"base.txt"},
		"criteria":      []any{map[string]any{"criterion_id": "c1", "text": "Update base.txt."}},
		"body_ref":      "docs/work.md#work",
	}))
	f.policy.Seats = map[string]string{"plan": "fixture", "build": "fixture", "design": "fixture"}
	if err := os.MkdirAll(filepath.Join(f.root, "config/tillr/workflows"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "config/tillr/workflows/plan-then-build@v1.json"),
		`{"name":"plan-then-build","version":1,"stages":[{"id":"plan","kind":"agent","seat":"plan","access":"read_only"},{"id":"build","kind":"agent","seat":"build","access":"write"}],"edges":[{"from_node":"plan","to_node":"build"}]}`)
	if err := os.MkdirAll(filepath.Join(f.root, "docs"), 0o755); err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(f.root, "docs/work.md"), "# Work\n\nBody.\n")
	git(t, f.root, "add", ".")
	git(t, f.root, "commit", "-qm", "workflow and document")

	ctx := context.Background()
	s, err := store.Open(ctx, f.store, true)
	if err != nil {
		t.Fatal(err)
	}
	err = s.WriteTx(ctx, func(tx *store.Tx) error {
		_, err := tx.Exec(`UPDATE items SET seat='plan', workflow='plan-then-build@v1', body_ref='docs/work.md#work' WHERE item_id='item_041'`)
		return err
	})
	s.Close()
	if err != nil {
		t.Fatal(err)
	}

	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	land := conflictLandScript(t, f.root, conflict, conflict, 1)
	runCtx, cancel := context.WithCancel(ctx)
	defer cancel()
	stage := &briefCapturingStager{
		t: t,
		inner: &drain.LaneStager{
			Root:     f.root,
			LaneRoot: f.laneRoot,
			Store:    f.store,
			Template: filepath.Join("..", "tillr-run", "backends", "brief-template.md"),
		},
		root:  f.laneRoot,
		limit: 9,
		stop:  cancel,
	}
	steps := f.steps(t, always(t, "gate", 0), land, always(t, "verdict", 0))
	loop, err := drain.NewLoop(drain.LoopConfig{
		ExitWhenDrained: true,
		Root:            f.root,
		Store:           f.store,
		LaneRoot:        f.laneRoot,
		Lanes:           []string{"lane-1"},
		Policy:          f.policy,
		Steps:           steps,
		Stage:           stage,
		Now:             steps.Now,
		Report:          os.Stderr,
	})
	if err != nil {
		t.Fatal(err)
	}
	result, err := loop.Run(runCtx)
	if err != nil {
		t.Fatalf("loop failed: %v (staged %d)", err, len(stage.specs))
	}

	// Expected dispatches:
	// 0: attempt 1 stage 1 (plan)
	// 1: attempt 1 stage 2 (build) -> squash-conflict on moved trunk
	// 2: -cfix on build -> squash-conflict on unmoved trunk
	// 3: returned run: no session continues, so the workflow starts again at stage 1 (plan)
	// 4: returned run, stage 2 (build) -> squash-conflict (second identical conflict -> restart!)
	// 5: fresh restart stage 1 (plan)
	// 6: fresh restart stage 2 (build) -> squash-conflict -> conflict-owner park!
	if len(stage.specs) != 7 {
		t.Fatalf("staged %d run(s), want 7 (plan, build, cfix, returned plan, returned build, restart-plan, restart-build): %s", len(stage.specs), result.Detail)
	}
	restartPlan := stage.specs[5]
	if restartPlan.Node == nil || restartPlan.Node.ID != "plan" || restartPlan.Node.Seat != "plan" {
		t.Fatalf("restarted run did not start at first stage (plan): %+v", restartPlan.Node)
	}
	if restartPlan.Conflict != nil || strings.HasSuffix(restartPlan.RunID, drain.ConflictFixSuffix) {
		t.Fatalf("restarted run was staged as conflict-fix: %+v", restartPlan)
	}
	restartBrief := stage.briefs[5]
	if !strings.Contains(restartBrief, "docs/work.md#work") {
		t.Fatalf("restarted brief missing body_ref:\n%s", restartBrief)
	}
	if !strings.Contains(strings.ToLower(restartBrief), "conflicted twice") {
		t.Fatalf("restarted brief does not state earlier attempt conflicted twice:\n%s", restartBrief)
	}
	if !strings.Contains(restartBrief, "base.txt") {
		t.Fatalf("restarted brief does not name conflicting path base.txt:\n%s", restartBrief)
	}
	for _, forbidden := range []string{
		"This is the one conflict-fix run",
		"This is a conflict-fix run",
		"conflict-markers.txt",
		"<<<<<<<",
		"Re-apply",
	} {
		if strings.Contains(restartBrief, forbidden) {
			t.Fatalf("restarted brief contains forbidden conflict-fix text %q:\n%s", forbidden, restartBrief)
		}
	}
	if !strings.Contains(result.Detail, fmt.Sprintf("item_041 (%s)", drain.ReasonConflictOwner)) {
		t.Fatalf("fresh attempt conflict did not park for owner: %s", result.Detail)
	}
}
