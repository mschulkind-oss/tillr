package drain_test

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/mschulkind-oss/tillr-drain/drain"
	"github.com/mschulkind-oss/tillr/store"
)

func itemRecordSeatedWithCriterion(itemID, state, seat string) string {
	return envelope("item.record", "item-"+itemID, map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"state":         state,
		"blocked_by":    []string{},
		"rank":          "",
		"allowed_paths": []string{"items/" + itemID + "/"},
		"criteria": []any{
			map[string]any{"criterion_id": "c1", "text": "the work is done", "state": "done"},
		},
		"seat": seat,
	})
}

func itemRecordReadyWithFence(itemID, state string, allowedPaths ...string) string {
	return envelope("item.record", "item-"+itemID, map[string]any{
		"item_id":       itemID,
		"title":         "the item " + itemID,
		"state":         state,
		"blocked_by":    []string{},
		"rank":          "",
		"allowed_paths": allowedPaths,
		"criteria": []any{
			map[string]any{"criterion_id": "c1", "text": "the work is done", "state": "done"},
		},
	})
}

type itemRunWithLane struct {
	RunID   string
	Backend string
	LaneID  string
}

func runsWithLaneForItem(t *testing.T, storePath, itemID string) []itemRunWithLane {
	t.Helper()
	ctx := context.Background()
	s, err := store.OpenRead(ctx, storePath)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	var out []itemRunWithLane
	if err := s.ReadTx(ctx, func(r *store.Reader) error {
		rows, err := r.Query(`SELECT run_id, backend, lane_id FROM runs WHERE item_id = ? ORDER BY rowid`, itemID)
		if err != nil {
			return err
		}
		defer rows.Close()
		for rows.Next() {
			var row itemRunWithLane
			if err := rows.Scan(&row.RunID, &row.Backend, &row.LaneID); err != nil {
				return err
			}
			out = append(out, row)
		}
		return rows.Err()
	}); err != nil {
		t.Fatal(err)
	}
	return out
}

func newItemWorktreesLoop(t *testing.T, f loopFixture, lanes int, steps drain.Steps, stage drain.Stager, report io.Writer, now func() time.Time) *drain.Loop {
	t.Helper()
	ids := make([]string, lanes)
	for i := range ids {
		ids[i] = laneName(i + 1)
	}
	if stage == nil {
		stage = &drain.LaneStager{
			Root:     f.root,
			LaneRoot: f.laneRoot,
			Store:    f.store,
			Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
			Now:      now,
		}
	}
	if ps, ok := steps.(*drain.ProcessSteps); ok {
		steps = &evidenceProcessSteps{ps}
	}
	l, err := drain.NewLoop(drain.LoopConfig{
		Root:            f.root,
		Store:           f.store,
		LaneRoot:        f.laneRoot,
		Lanes:           ids,
		ItemWorktrees:   true,
		Policy:          f.policy,
		Steps:           steps,
		Stage:           stage,
		Now:             now,
		Report:          report,
		ExitWhenDrained: true,
	})
	if err != nil {
		t.Fatalf("NewLoop(ItemWorktrees): %v", err)
	}
	return l
}

func TestItemWorktreesFaultLeavesTwoRunsToFinishAndReplacementJudgesOnce(t *testing.T) {
	markers := scratch(t, "survivors-item-wt")
	t.Setenv("SURVIVOR_MARKERS", markers)
	t.Cleanup(func() {
		writeFile(t, filepath.Join(markers, "item_a.finish"), "finish")
		writeFile(t, filepath.Join(markers, "item_b.finish"), "finish")
	})
	adapter := stub(t, "survivor", counting+`echo started > "$SURVIVOR_MARKERS/$TILLR_ITEM_ID.started"
while [ ! -f "$SURVIVOR_MARKERS/$TILLR_ITEM_ID.finish" ]; do sleep 0.02; done
`+committingAdapterBody+`echo finished > "$SURVIVOR_MARKERS/$TILLR_ITEM_ID.finished"
printf 'RESULT: done\n'`)
	adapter = stub(t, "identified-survivor", `TILLR_ITEM_ID=$(printf '%s' "$TILLR_RUN_ID" | cut -d- -f2)
export TILLR_ITEM_ID
exec "`+adapter+`" "$@"`)
	f := newLoopFixture(t, adapter)
	t.Cleanup(func() { cleanupStoreRuns(t, f.store) })
	fixtureAt(t, f.store,
		itemRecordWithFence("item_a", "in-queue", "candidate.txt"),
		itemRecordWithFence("item_b", "in-queue", "candidate.txt"),
		itemRecordWithFence("item_fault", "in-queue", "candidate.txt"),
	)
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	l := newItemWorktreesLoop(t, f, 3, faultWithSiblings{steps, markers}, nil, io.Discard, steps.Now)
	done := make(chan error, 1)
	go func() { _, err := l.Run(context.Background()); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("fixture fault was not reported")
		}
	case <-time.After(hangGuard):
		t.Fatal("fault stop waited for or canceled its runs")
	}
	writeFile(t, filepath.Join(markers, "item_a.finish"), "finish")
	waitForFile(t, filepath.Join(markers, "item_a.finished"), hangGuard)

	// One run finishes offline, the other while the replacement supervises it.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replacement := newItemWorktreesLoop(t, f, 3, steps, nil, io.Discard, steps.Now)
	go func() { _, err := replacement.Run(ctx); done <- err }()
	writeFile(t, filepath.Join(markers, "item_b.finish"), "finish")
	deadline := time.Now().Add(hangGuard)
	for len(callsNamed(lines(t, f.count), "land")) < 2 && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(hangGuard):
		t.Fatal("replacement did not stop")
	}
	if n := len(callsNamed(lines(t, f.count), "land")); n != 2 {
		t.Fatalf("land calls=%d, want 2", n)
	}
	for _, id := range []string{"item_a", "item_b"} {
		runs := runsWithLaneForItem(t, f.store, id)
		if len(runs) != 1 {
			t.Fatalf("%s attempts=%d, want 1", id, len(runs))
		}
		if runs[0].LaneID != id {
			t.Fatalf("%s ran in lane %q, want %q", id, runs[0].LaneID, id)
		}
		for _, face := range []string{"gate", "land"} {
			n := 0
			for _, call := range callsNamed(lines(t, f.count), face) {
				if strings.Contains(call, "--run run-"+id+"-a1 ") {
					n++
				}
			}
			if n != 1 {
				t.Fatalf("%s %s calls=%d, want 1", id, face, n)
			}
		}
	}
}

func TestItemWorktreesHoldsSpentWindowAcrossRestartAndReopens(t *testing.T) {
	refused := stub(t, "spent", counting+"exit 1\n")
	fallback := stub(t, "fallback", committingAdapter)
	f := newLoopFixture(t, refused)
	f.policy.Seats = map[string]string{"build": "fixture"}
	f.policy.Backends["fallback"] = drain.Backend{Adapter: fallback, Model: "floor"}
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	now := steps.Now()
	reset := now.Add(time.Hour)
	steps.BackendsDir = spendBackends(t, refused, "# run report\ntokens: unknown\ncost usd: unknown\nrate limit kind: five_hour\nfraction: 1\nstatus: rejected\nreset: "+strconv.FormatInt(reset.Unix(), 10)+"\n---\n"+strings.Repeat("x", 70000))
	fixtureAt(t, f.store, itemRecordSeatedWithCriterion("item_051", "in-queue", "build"), itemRecordSeatedWithCriterion("item_052", "in-queue", "build"))
	runLoop := func() drain.LoopResult {
		t.Helper()
		steps.Now = func() time.Time { return now }
		result, err := newItemWorktreesLoop(t, f, 1, steps, nil, io.Discard, steps.Now).Run(context.Background())
		if err != nil || result.Stop != drain.StopQueueDrained {
			t.Fatalf("loop: %+v / %v", result, err)
		}
		return result
	}
	runLoop()
	first := runsWithLaneForItem(t, f.store, "item_051")
	second := runsWithLaneForItem(t, f.store, "item_052")
	if len(first) != 1 || first[0].Backend != "fixture" || first[0].LaneID != "item_051" {
		t.Fatalf("first: %+v", first)
	}
	if len(second) != 0 {
		t.Fatalf("work started while its backend was shut: %+v", second)
	}
	fixtureAt(t, f.store, itemRecordSeatedWithCriterion("item_053", "in-queue", "build"))
	runLoop()
	if third := runsWithLaneForItem(t, f.store, "item_053"); len(third) != 0 {
		t.Fatalf("restart lost hold: %+v", third)
	}
	now = reset
	if err := os.WriteFile(refused, []byte("#!/bin/sh\n"+committingAdapter), 0o755); err != nil {
		t.Fatal(err)
	}
	fixtureAt(t, f.store, itemRecordSeatedWithCriterion("item_054", "in-queue", "build"))
	result := runLoop()
	if result.Landed != 4 {
		t.Fatalf("reopened backend did not land all four queued items: %+v", result)
	}
	for _, id := range []string{"item_051", "item_052", "item_053", "item_054"} {
		runs := runsWithLaneForItem(t, f.store, id)
		if len(runs) == 0 {
			t.Fatalf("%s: expected runs on reopened backend, got none", id)
		}
		for _, r := range runs {
			if r.LaneID != id {
				t.Errorf("%s: run %s used lane %q, want %q", id, r.RunID, r.LaneID, id)
			}
		}
	}
}

type failingItemWorktreeRepairStager struct {
	*drain.LaneStager
	signals   string
	calls     int
	corrected bool
}

func (s *failingItemWorktreeRepairStager) Stage(ctx context.Context, spec drain.StageSpec) error {
	if spec.Conflict != nil {
		s.calls++
		for _, name := range []string{"started-item_042", "started-item_043"} {
			if _, err := os.Stat(filepath.Join(s.signals, name)); err != nil {
				return fmt.Errorf("sibling not active: %w", err)
			}
		}
		if !s.corrected {
			if err := os.WriteFile(filepath.Join(s.signals, "release"), nil, 0o600); err != nil {
				return err
			}
			return fmt.Errorf("conflict facts contain a literal brace pair")
		}
	}
	return s.LaneStager.Stage(ctx, spec)
}

func TestItemWorktreesRepairStagingFailureSurvivesRestartWithoutCancelingSiblings(t *testing.T) {
	signals := scratch(t, "repair-signals-item-wt")
	t.Setenv("REPAIR_SIGNALS", signals)
	adapter := stub(t, "adapter", `lane=$(basename "$(dirname "$(pwd)")")
case "$lane" in
 item_041) while [ ! -f "$REPAIR_SIGNALS/started-item_042" ] || [ ! -f "$REPAIR_SIGNALS/started-item_043" ]; do sleep 0.01; done ;;
 *) touch "$REPAIR_SIGNALS/started-$lane"
    while [ ! -f "$REPAIR_SIGNALS/release" ]; do sleep 0.01; done
    file="sibling-$lane.txt"
    echo candidate > "$file"
    git add "$file"
    git -c user.name=stub -c user.email=stub@example.invalid commit -q -m candidate
    printf 'RESULT: done\n'
    exit 0 ;;
esac
`+conflictAdapter)
	f := newLoopFixture(t, adapter)
	t.Cleanup(func() { cleanupStoreRuns(t, f.store) })
	fixtureAt(t, f.store, itemRecordReadyWithFence("item_041", "in-queue", "base.txt"), itemRecordReadyWithFence("item_042", "in-queue", "sibling-item_042.txt"), itemRecordReadyWithFence("item_043", "in-queue", "sibling-item_043.txt"))
	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	base := git(t, f.root, "rev-parse", "HEAD")
	writeFile(t, filepath.Join(f.root, "base.txt"), "trunk\n")
	git(t, f.root, "add", "base.txt")
	git(t, f.root, "commit", "-qm", "moved trunk fixture")
	moved := git(t, f.root, "rev-parse", "HEAD")
	git(t, f.root, "reset", "--hard", base)
	git(t, f.root, "checkout", "-b", "operator")
	landFirst := stub(t, "land-conflict", fmt.Sprintf(`unset GIT_DIR GIT_WORK_TREE GIT_INDEX_FILE
if [ ! -f %q/land-once ]; then
 touch %q/land-once
 git -C %q update-ref refs/heads/main %q
 printf '%%s\n' '%s'
 exit 1
fi
printf '%%s\n' '%s'
`, signals, signals, f.root, moved, conflict, landingEnvelope(t, "landed", nil)))
	land := stub(t, "land-select", fmt.Sprintf(`case " $* " in
 *" --item item_041 "*) exec %q "$@" ;;
 *) exit 0 ;;
esac
`, landFirst))
	steps := f.steps(t, always(t, "gate", 0), land, always(t, "verdict", 0))
	stage := &failingItemWorktreeRepairStager{
		LaneStager: &drain.LaneStager{
			Root:     f.root,
			LaneRoot: f.laneRoot,
			Store:    f.store,
			Template: filepath.Join("..", "..", "tillr-run", "backends", "brief-template.md"),
			Now:      steps.Now,
		},
		signals: signals,
	}
	result, err := newItemWorktreesLoop(t, f, 3, steps, stage, os.Stderr, steps.Now).Run(context.Background())
	if err != nil {
		t.Fatalf("repair failure canceled loop and siblings: %+v / %v", result, err)
	}
	if result.Landed != 2 || stage.calls != 1 {
		t.Fatalf("siblings did not finish or repair repeated: %+v, calls=%d", result, stage.calls)
	}
	for _, id := range []string{"item_042", "item_043"} {
		if state := itemStateOf(t, f.store, id); state != "landed" {
			t.Fatalf("%s: %s", id, state)
		}
		runs := runsWithLaneForItem(t, f.store, id)
		if len(runs) != 1 || runs[0].LaneID != id {
			t.Fatalf("%s runs=%+v, want 1 run in lane %q", id, runs, id)
		}
	}
	// Restart with unchanged inputs: failure must still be known and not re-staged.
	newer := git(t, f.root, "commit-tree", moved+"^{tree}", "-p", moved, "-m", "later trunk fixture")
	git(t, f.root, "update-ref", "refs/heads/main", newer)
	result, err = newItemWorktreesLoop(t, f, 3, steps, stage, os.Stderr, steps.Now).Run(context.Background())
	if err != nil || result.Dispatched != 0 || stage.calls != 1 || !strings.Contains(result.Detail, "conflict-repair-staging-failed") {
		t.Fatalf("restart repeated unchanged failure: %+v / %v calls=%d", result, err, stage.calls)
	}
	// Correct the staging input and restart: item_041 recovers in its item directory.
	stage.corrected = true
	backend := f.policy.Backends["fixture"]
	backend.Model = "corrected-model"
	f.policy.Backends["fixture"] = backend
	result, err = newItemWorktreesLoop(t, f, 3, steps, stage, os.Stderr, steps.Now).Run(context.Background())
	if err != nil || result.Landed != 1 || stage.calls != 2 || itemStateOf(t, f.store, "item_041") != "landed" {
		t.Fatalf("corrected repair did not recover: %+v / %v calls=%d", result, err, stage.calls)
	}
	for _, r := range runsWithLaneForItem(t, f.store, "item_041") {
		if r.LaneID != "item_041" {
			t.Fatalf("item_041 run %s used lane %q, want item_041", r.RunID, r.LaneID)
		}
	}
}

func TestItemWorktreesLoopLetsExistingPoolLaneRunsFinish(t *testing.T) {
	markers := scratch(t, "pool-transition")
	t.Setenv("SURVIVOR_MARKERS", markers)
	t.Cleanup(func() {
		writeFile(t, filepath.Join(markers, "item_a.finish"), "finish")
		writeFile(t, filepath.Join(markers, "item_b.finish"), "finish")
	})
	adapter := stub(t, "pool-survivor", counting+`echo started > "$SURVIVOR_MARKERS/$TILLR_ITEM_ID.started"
if [ "$TILLR_ITEM_ID" = "item_a" ] || [ "$TILLR_ITEM_ID" = "item_b" ]; then
  while [ ! -f "$SURVIVOR_MARKERS/$TILLR_ITEM_ID.finish" ]; do sleep 0.02; done
fi
`+committingAdapterBody+`echo finished > "$SURVIVOR_MARKERS/$TILLR_ITEM_ID.finished"
printf 'RESULT: done\n'`)
	adapter = stub(t, "identified-pool-survivor", `TILLR_ITEM_ID=$(printf '%s' "$TILLR_RUN_ID" | cut -d- -f2)
export TILLR_ITEM_ID
exec "`+adapter+`" "$@"`)
	f := newLoopFixture(t, adapter)
	t.Cleanup(func() { cleanupStoreRuns(t, f.store) })
	fixtureAt(t, f.store,
		itemRecordReadyWithFence("item_a", "in-queue", "candidate.txt"),
		itemRecordReadyWithFence("item_b", "in-queue", "candidate.txt"),
		itemRecordReadyWithFence("item_fault", "in-queue", "candidate.txt"),
	)
	steps := f.steps(t, always(t, "gate", 0), always(t, "land", 0), always(t, "verdict", 0))
	// Start loop 1 with ItemWorktrees OFF (pool lanes): item_a and item_b start on pool lanes.
	first, err := drain.NewLoop(drain.LoopConfig{
		Root:            f.root,
		Store:           f.store,
		LaneRoot:        f.laneRoot,
		Lanes:           []string{"lane-1", "lane-2", "lane-3"},
		Policy:          f.policy,
		Steps:           faultWithSiblings{steps, markers},
		Stage:           passStager{laneRoot: f.laneRoot, fence: []string{"candidate.txt"}},
		Now:             steps.Now,
		ExitWhenDrained: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() { _, err := first.Run(context.Background()); done <- err }()
	select {
	case err := <-done:
		if err == nil {
			t.Fatal("fixture fault was not reported")
		}
	case <-time.After(hangGuard):
		t.Fatal("first loop did not stop on fault")
	}

	// Enqueue a brand-new item while item_a and item_b are still live/leased on pool lanes.
	fixtureAt(t, f.store, itemRecordReadyWithFence("item_new", "in-queue", "candidate.txt"))
	writeFile(t, filepath.Join(markers, "item_a.finish"), "finish")
	waitForFile(t, filepath.Join(markers, "item_a.finished"), hangGuard)

	// Start loop 2 with ItemWorktrees ON: item_a and item_b must finish on their pool lanes,
	// while item_new runs in its own item directory.
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	replacement := newItemWorktreesLoop(t, f, 3, steps, nil, io.Discard, steps.Now)
	go func() { _, err := replacement.Run(ctx); done <- err }()
	writeFile(t, filepath.Join(markers, "item_b.finish"), "finish")

	deadline := time.Now().Add(hangGuard)
	for (itemStateOf(t, f.store, "item_a") != "landed" || itemStateOf(t, f.store, "item_b") != "landed" || itemStateOf(t, f.store, "item_new") != "landed") && time.Now().Before(deadline) {
		time.Sleep(20 * time.Millisecond)
	}
	cancel()
	select {
	case err := <-done:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(hangGuard):
		t.Fatal("replacement loop did not stop")
	}

	for _, id := range []string{"item_a", "item_b"} {
		runs := runsWithLaneForItem(t, f.store, id)
		if len(runs) != 1 || !strings.HasPrefix(runs[0].LaneID, "lane-") {
			t.Fatalf("%s: expected 1 run on its original pool lane, got %+v", id, runs)
		}
		if state := itemStateOf(t, f.store, id); state != "landed" {
			t.Fatalf("%s state=%q, want landed", id, state)
		}
	}
	newRuns := runsWithLaneForItem(t, f.store, "item_new")
	if len(newRuns) != 1 || newRuns[0].LaneID != "item_new" {
		t.Fatalf("item_new: expected 1 run in its own item directory, got %+v", newRuns)
	}
	if state := itemStateOf(t, f.store, "item_new"); state != "landed" {
		t.Fatalf("item_new state=%q, want landed", state)
	}
	// Ensure no json import warning if unused; keep check on events.
	var sawCandidate bool
	for _, e := range eventsOf(t, f.store) {
		if e.Kind == "drain.candidate" {
			var c drain.Candidate
			if err := json.Unmarshal(e.Payload, &c); err == nil && c.ItemID == "item_new" && c.LaneID == "item_new" {
				sawCandidate = true
			}
		}
	}
	if !sawCandidate {
		t.Fatalf("expected drain.candidate for item_new in lane item_new")
	}
}
