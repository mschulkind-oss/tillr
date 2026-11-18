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
)

func TestInterruptedRecoveredRepairSettlesExistingCommitWithoutColdRestart(t *testing.T) {
	signals := scratch(t, "interrupted-repair")
	for _, name := range []string{"started-2", "started-3"} {
		writeFile(t, filepath.Join(signals, name), "fixture\n")
	}
	marker := filepath.Join(signals, "repair-committed")
	t.Cleanup(func() { writeFile(t, marker+".release", "finish") })
	adapter := stub(t, "adapter", fmt.Sprintf(`case "$TILLR_RUN_ID" in
 *-cfix)
  if [ -f %q ]; then echo duplicate-cold-repair; printf 'RESULT: done\n'; exit 0; fi
  echo repair-only-marker
  echo repaired > base.txt
  git add base.txt
  git -c user.name=stub -c user.email=stub@example.invalid commit -q -m repaired
  touch %q
  while [ ! -f %q ]; do sleep 0.01; done
  printf 'RESULT: done\n'; exit 0 ;;
esac
`, marker, marker, marker+".release")+conflictAdapter)
	f := newLoopFixture(t, adapter)
	fixtureAt(t, f.store, itemRecord("item_041", "in-queue"))
	conflict := landingEnvelope(t, "squash-conflict", []string{"CONFLICT (content): Merge conflict in base.txt"})
	land := conflictLandScript(t, f.root, conflict, landingEnvelope(t, "landed", nil), 0)
	stage := &failingRepairStager{passStager: passStager{laneRoot: f.laneRoot, fence: []string{"base.txt"}}, signals: signals}
	steps := f.steps(t, always(t, "gate", 0), land, always(t, "verdict", 0))
	first, err := f.newLoopWith(t, 1, steps, stage, os.Stderr).Run(context.Background())
	if err != nil || stage.calls != 1 {
		t.Fatalf("initial hold: %+v / %v calls=%d", first, err, stage.calls)
	}
	stage.corrected = true
	backend := f.policy.Backends["fixture"]
	backend.Model = "corrected-model"
	backend.TimeoutSeconds = 0
	f.policy.Backends["fixture"] = backend
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	finished := make(chan struct{})
	var stopped drain.LoopResult
	go func() { stopped, err = f.newLoopWith(t, 1, steps, stage, os.Stderr).Run(ctx); close(finished) }()
	deadline := time.Now().Add(hangGuard)
	for {
		if _, e := os.Stat(marker); e == nil {
			break
		}
		select {
		case <-finished:
			t.Fatalf("repair ended before interruption: %+v / %v", stopped, err)
		default:
		}
		if time.Now().After(deadline) {
			cancel()
			<-finished
			t.Fatal("repair never committed")
		}
		time.Sleep(10 * time.Millisecond)
	}
	head := git(t, filepath.Join(f.laneRoot, "lane-1", "worktree"), "rev-parse", "HEAD")
	cancel()
	select {
	case <-finished:
	case <-time.After(hangGuard):
		t.Fatal("interrupted repair did not stop")
	}
	if err != nil || stopped.Stop != drain.StopOperatorStop {
		t.Fatalf("operator stop: %+v / %v", stopped, err)
	}
	logPath := filepath.Join(f.laneRoot, "lane-1", "run-item_041-a1-cfix.log")
	before := readText(t, logPath)
	writeFile(t, marker+".release", "finish")
	restarted, restartErr := f.newLoopWith(t, 1, steps, stage, os.Stderr).Run(context.Background())
	if restartErr != nil || restarted.Dispatched != 0 || restarted.Landed != 1 || stage.calls != 2 {
		t.Fatalf("restarted a successfully staged repair: %+v / %v calls=%d", restarted, restartErr, stage.calls)
	}
	after := readText(t, logPath)
	if !strings.HasPrefix(after, before) || !strings.Contains(after, "repair-only-marker") || strings.Contains(after, "duplicate-cold-repair") {
		t.Fatalf("repair transcript overwritten: %q -> %q", before, after)
	}
	found := false
	owned := false
	for _, e := range eventsOf(t, f.store) {
		if e.Kind == "drain.started" && e.ObjectID != nil && *e.ObjectID == "run-item_041-a1-cfix" {
			var work struct {
				Decision    drain.Decision `json:"decision"`
				ParentRunID string         `json:"parent_run_id"`
			}
			if err := json.Unmarshal(e.Payload, &work); err != nil {
				t.Fatal(err)
			}
			if work.Decision.DecisionID == "" || work.Decision.PassID == "" || work.ParentRunID != "run-item_041-a1" {
				t.Fatalf("repair ownership has no distinct decision/pass/parent: %+v", work)
			}
			owned = true
		}
		if e.Kind == "drain.candidate" && strings.Contains(string(e.Payload), "run-item_041-a1-cfix") && strings.Contains(string(e.Payload), head) {
			found = true
		}
	}
	if !found || !owned {
		t.Fatal("restart did not own and judge the existing repair commit")
	}
}
